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
