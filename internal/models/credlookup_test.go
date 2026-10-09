package models

import "testing"

// V2-cleanup-55.1 — unit tests for the shared credential lookup-key
// resolver: stored secretPath wins, plain name is the fallback, and an
// unknown cluster resolves to its own name (byte-identical to the
// pre-resolver behavior).

func TestClusterCredentialLookupKey(t *testing.T) {
	tests := []struct {
		name    string
		cluster Cluster
		want    string
	}{
		{
			name:    "secretPath set — override wins",
			cluster: Cluster{Name: "moran", SecretPath: "sharko-smoke-target-1-kubeconfig"},
			want:    "sharko-smoke-target-1-kubeconfig",
		},
		{
			name:    "secretPath unset — plain name",
			cluster: Cluster{Name: "prod-eu"},
			want:    "prod-eu",
		},
		{
			name:    "empty cluster — empty key",
			cluster: Cluster{},
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cluster.CredentialLookupKey(); got != tt.want {
				t.Errorf("CredentialLookupKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagedClusterEntryCredentialLookupKey(t *testing.T) {
	withPath := ManagedClusterEntry{Name: "moran", SecretPath: "clusters/prod/moran"}
	if got := withPath.CredentialLookupKey(); got != "clusters/prod/moran" {
		t.Errorf("entry with secretPath: CredentialLookupKey() = %q, want %q", got, "clusters/prod/moran")
	}
	withoutPath := ManagedClusterEntry{Name: "moran"}
	if got := withoutPath.CredentialLookupKey(); got != "moran" {
		t.Errorf("entry without secretPath: CredentialLookupKey() = %q, want %q", got, "moran")
	}
}

func TestCredentialLookupKeyFor(t *testing.T) {
	clusters := []Cluster{
		{Name: "moran", SecretPath: "sharko-smoke-target-1-kubeconfig"},
		{Name: "plain"},
	}

	tests := []struct {
		name       string
		lookupName string
		want       string
	}{
		{"secretPath set", "moran", "sharko-smoke-target-1-kubeconfig"},
		{"secretPath unset", "plain", "plain"},
		{"cluster unknown — fallback to name", "ghost", "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CredentialLookupKeyFor(clusters, tt.lookupName); got != tt.want {
				t.Errorf("CredentialLookupKeyFor(%q) = %q, want %q", tt.lookupName, got, tt.want)
			}
		})
	}

	t.Run("nil cluster list — fallback to name", func(t *testing.T) {
		if got := CredentialLookupKeyFor(nil, "anything"); got != "anything" {
			t.Errorf("CredentialLookupKeyFor(nil, anything) = %q, want %q", got, "anything")
		}
	})
}

// TestClusterCredentialsResolvable is the truth table for the
// addon_secrets_ready API field's underlying predicate (V2-cleanup-88.3 —
// lazy credentials). See CredentialsResolvable's doc comment for the full
// rationale of each row.
func TestClusterCredentialsResolvable(t *testing.T) {
	tests := []struct {
		name                string
		credsSource         string
		connectionManagedBy string
		backendConfigured   bool
		want                bool
	}{
		{
			name:        "inline + sharko-managed + backend configured -> true (Sharko wrote the ArgoCD Secret)",
			credsSource: CredsSourceInlineKubeconfig, connectionManagedBy: "", backendConfigured: true,
			want: true,
		},
		{
			name:        "inline + sharko-managed + no backend -> true (still true: Sharko wrote the ArgoCD Secret regardless of backend)",
			credsSource: CredsSourceInlineKubeconfig, connectionManagedBy: "", backendConfigured: false,
			want: true,
		},
		{
			name:        "inline + self-managed (user) -> false (Sharko never writes the Secret for this mode)",
			credsSource: CredsSourceInlineKubeconfig, connectionManagedBy: ConnectionManagedByUser, backendConfigured: true,
			want: false,
		},
		{
			name:        "secret-kubeconfig + backend configured -> true",
			credsSource: CredsSourceSecretKubeconfig, connectionManagedBy: "", backendConfigured: true,
			want: true,
		},
		{
			name:        "secret-kubeconfig + no backend -> false",
			credsSource: CredsSourceSecretKubeconfig, connectionManagedBy: "", backendConfigured: false,
			want: false,
		},
		{
			name:        "eks-token + backend configured -> true",
			credsSource: CredsSourceEKSToken, connectionManagedBy: "", backendConfigured: true,
			want: true,
		},
		{
			name:        "eks-token + no backend -> false",
			credsSource: CredsSourceEKSToken, connectionManagedBy: "", backendConfigured: false,
			want: false,
		},
		{
			name:        "unknown/pre-field source + backend configured -> true (backend-first fallback)",
			credsSource: "", connectionManagedBy: "", backendConfigured: true,
			want: true,
		},
		{
			name:        "unknown/pre-field source + no backend -> false (the lazy-credentials connection-only case)",
			credsSource: "", connectionManagedBy: "", backendConfigured: false,
			want: false,
		},
		{
			name:        "unknown source + self-managed + no backend -> false",
			credsSource: "", connectionManagedBy: ConnectionManagedByUser, backendConfigured: false,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Cluster{CredsSource: tt.credsSource, ConnectionManagedBy: tt.connectionManagedBy}
			if got := c.CredentialsResolvable(tt.backendConfigured); got != tt.want {
				t.Errorf("Cluster.CredentialsResolvable(%v) = %v, want %v", tt.backendConfigured, got, tt.want)
			}
			e := ManagedClusterEntry{CredsSource: tt.credsSource, ConnectionManagedBy: tt.connectionManagedBy}
			if got := e.CredentialsResolvable(tt.backendConfigured); got != tt.want {
				t.Errorf("ManagedClusterEntry.CredentialsResolvable(%v) = %v, want %v", tt.backendConfigured, got, tt.want)
			}
		})
	}
}

// TestCredentialPredicates_DisagreeOnInlineSharkoManaged is a guard test, not
// a coverage test. It exists to STOP a future change that merges
// CredentialsResolvable and ExpectedCredentialsRebuildableWithoutLiveSecret
// into one predicate because they look like the same question.
//
// They are not. On an inline-kubeconfig cluster with a Sharko-managed
// connection they give OPPOSITE answers, and that opposition is load-bearing:
//
//   - CredentialsResolvable says TRUE — Sharko can get working credentials for
//     this cluster, by reading back the ArgoCD cluster Secret it wrote at
//     registration. Its callers (the addon-secrets-ready hint, the enable-addon
//     pre-flight) only need credentials that work.
//   - ExpectedCredentialsRebuildableWithoutLiveSecret says FALSE — the pasted
//     kubeconfig was never stored anywhere but that same Secret, so there is no
//     independent copy to rebuild an EXPECTED Secret from.
//
// If the comparison ever used the first predicate it would build its expected
// Secret out of the live Secret, compare the Secret with itself, match every
// time, and tell the user a badly drifted connection is in sync. That is the
// exact bug this pair of functions is shaped to prevent.
func TestCredentialPredicates_DisagreeOnInlineSharkoManaged(t *testing.T) {
	// The disagreement holds whichever way the backend answer goes — an inline
	// cluster's credentials are not in the backend either way. The two
	// predicates take DIFFERENT booleans (one "a backend is configured", the
	// other "the backend can be read independently of the live Secret"), and
	// this loop feeds both the same value on purpose: even then they must
	// disagree, so the disagreement cannot be an artefact of the input.
	for _, backendAnswer := range []bool{false, true} {
		c := Cluster{
			CredsSource:         CredsSourceInlineKubeconfig,
			ConnectionManagedBy: ConnectionManagedBySharko,
		}
		resolvable := c.CredentialsResolvable(backendAnswer)
		rebuildable := c.ExpectedCredentialsRebuildableWithoutLiveSecret(backendAnswer)

		if !resolvable {
			t.Fatalf("backendAnswer=%v: CredentialsResolvable = false, want true — "+
				"Sharko CAN read an inline cluster's credentials back out of the ArgoCD Secret it wrote",
				backendAnswer)
		}
		if rebuildable {
			t.Fatalf("backendAnswer=%v: ExpectedCredentialsRebuildableWithoutLiveSecret = true, want false — "+
				"an inline kubeconfig has no copy outside the live ArgoCD Secret, so there is nothing to rebuild an "+
				"EXPECTED Secret from. Returning true here makes the comparison compare the Secret with itself and "+
				"report a false in-sync. Do not merge these two predicates.",
				backendAnswer)
		}
		if resolvable == rebuildable {
			t.Fatalf("backendAnswer=%v: the two predicates agreed (%v) on inline-kubeconfig + Sharko-managed. "+
				"They answer different questions and MUST disagree here — see the doc comments on both.",
				backendAnswer, resolvable)
		}

		// Same for the ManagedClusterEntry twins, so a change to only one
		// receiver cannot slip through.
		e := ManagedClusterEntry{
			CredsSource:         CredsSourceInlineKubeconfig,
			ConnectionManagedBy: ConnectionManagedBySharko,
		}
		if e.CredentialsResolvable(backendAnswer) == e.ExpectedCredentialsRebuildableWithoutLiveSecret(backendAnswer) {
			t.Fatalf("backendAnswer=%v: ManagedClusterEntry twins agreed on inline-kubeconfig + Sharko-managed; they must disagree",
				backendAnswer)
		}
	}
}

func TestExpectedCredentialsRebuildableWithoutLiveSecret(t *testing.T) {
	tests := []struct {
		name                         string
		credsSource                  string
		connectionManagedBy          string
		backendCanProvideStoredFacts bool
		want                         bool
	}{
		{
			name:        "secret-kubeconfig + backend configured -> true (re-fetch from the backend)",
			credsSource: CredsSourceSecretKubeconfig, backendCanProvideStoredFacts: true,
			want: true,
		},
		{
			name:        "secret-kubeconfig + no backend -> false (nothing to ask)",
			credsSource: CredsSourceSecretKubeconfig, backendCanProvideStoredFacts: false,
			want: false,
		},
		{
			name:        "eks-token + backend configured -> true (metadata re-readable; the token itself is not comparable)",
			credsSource: CredsSourceEKSToken, backendCanProvideStoredFacts: true,
			want: true,
		},
		{
			name:        "eks-token + no backend -> false",
			credsSource: CredsSourceEKSToken, backendCanProvideStoredFacts: false,
			want: false,
		},
		{
			name:        "inline-kubeconfig + Sharko-managed -> false even with a backend (only copy is the live Secret)",
			credsSource: CredsSourceInlineKubeconfig, connectionManagedBy: ConnectionManagedBySharko, backendCanProvideStoredFacts: true,
			want: false,
		},
		{
			name:        "inline-kubeconfig + self-managed -> false",
			credsSource: CredsSourceInlineKubeconfig, connectionManagedBy: ConnectionManagedByUser, backendCanProvideStoredFacts: true,
			want: false,
		},
		{
			name:        "unknown/pre-field source + backend configured -> false (never guessed, unlike CredentialsResolvable)",
			credsSource: "", backendCanProvideStoredFacts: true,
			want: false,
		},
		{
			name:        "unknown/pre-field source + no backend -> false",
			credsSource: "", backendCanProvideStoredFacts: false,
			want: false,
		},
		{
			name:        "secret-kubeconfig + self-managed -> false (Sharko never writes that Secret's credentials)",
			credsSource: CredsSourceSecretKubeconfig, connectionManagedBy: ConnectionManagedByUser, backendCanProvideStoredFacts: true,
			want: false,
		},
		{
			name:        "eks-token + self-managed -> false",
			credsSource: CredsSourceEKSToken, connectionManagedBy: ConnectionManagedByUser, backendCanProvideStoredFacts: true,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Cluster{CredsSource: tt.credsSource, ConnectionManagedBy: tt.connectionManagedBy}
			if got := c.ExpectedCredentialsRebuildableWithoutLiveSecret(tt.backendCanProvideStoredFacts); got != tt.want {
				t.Errorf("Cluster.ExpectedCredentialsRebuildableWithoutLiveSecret(%v) = %v, want %v", tt.backendCanProvideStoredFacts, got, tt.want)
			}
			e := ManagedClusterEntry{CredsSource: tt.credsSource, ConnectionManagedBy: tt.connectionManagedBy}
			if got := e.ExpectedCredentialsRebuildableWithoutLiveSecret(tt.backendCanProvideStoredFacts); got != tt.want {
				t.Errorf("ManagedClusterEntry.ExpectedCredentialsRebuildableWithoutLiveSecret(%v) = %v, want %v", tt.backendCanProvideStoredFacts, got, tt.want)
			}
		})
	}
}
