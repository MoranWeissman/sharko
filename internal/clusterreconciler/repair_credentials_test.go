package clusterreconciler

// repair_credentials_test.go — R3-14 at the writer level: the ONE route a write
// takes to a cluster's credentials, and what it hands the builder.
//
// The provider-level proof that the minting read and the no-mint read produce
// two different connection shapes lives in
// internal/providers/write_vs_check_shape_test.go, where the mint seam is. What
// is proved here is the other half: that this function reaches the credentials
// through the normal backend fetch, carries the per-cluster role precedence, and
// refuses cleanly rather than handing back a spec with no credential in it.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// roleRecordingVault records the role ARN it was asked to fetch with, so the
// per-cluster precedence can be asserted on the value that really reached the
// backend rather than on a copy of the rule.
type roleRecordingVault struct {
	gotLookupKey string
	gotRoleARN   string
	roleCalls    int
	plainCalls   int
	creds        *providers.Kubeconfig
	err          error
}

func (v *roleRecordingVault) GetCredentials(lookupKey string) (*providers.Kubeconfig, error) {
	v.plainCalls++
	v.gotLookupKey = lookupKey
	if v.err != nil {
		return nil, v.err
	}
	return v.creds, nil
}

func (v *roleRecordingVault) GetCredentialsWithRoleARN(lookupKey, roleARN string) (*providers.Kubeconfig, error) {
	v.roleCalls++
	v.gotLookupKey = lookupKey
	v.gotRoleARN = roleARN
	if v.err != nil {
		return nil, v.err
	}
	return v.creds, nil
}

func (v *roleRecordingVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (v *roleRecordingVault) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (v *roleRecordingVault) HealthCheck(context.Context) error              { return nil }

func TestConnectionCredentialSpecForWrite_CarriesEveryPieceOfCredentialMaterial(t *testing.T) {
	// A bearer-token cluster. The values are made up and are not shaped like
	// anything real.
	vault := &roleRecordingVault{creds: &providers.Kubeconfig{
		Server: "https://written.test.invalid",
		Token:  "made-up-not-a-real-token",
		CAData: []byte("not-a-real-ca"),
	}}
	r := &Reconciler{deps: Deps{Vault: vault}}

	spec, err := r.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{
		Name:   "written",
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("the write route failed: %v", err)
	}

	if spec.Server != "https://written.test.invalid" {
		t.Errorf("Server = %q, want the address the backend returned", spec.Server)
	}
	if spec.Token == "" {
		t.Error(`Token is empty although the backend returned one.

A missing token is what made buildSecretConfig fall through to the exec shape, so a repair silently changed how ArgoCD signs in. The token must be carried.`)
	}
	if spec.CAData == "" {
		t.Error("CAData is empty although the backend returned a CA bundle")
	}
	if spec.Region != "eu-west-1" {
		t.Errorf("Region = %q, want it carried from the cluster's entry", spec.Region)
	}
	// Labels and annotations are the caller's, not this function's.
	if spec.Labels != nil {
		t.Errorf("Labels = %v, want nil — the labels are the caller's to merge", spec.Labels)
	}
	if spec.Annotations != nil {
		t.Errorf("Annotations = %v, want nil — provenance is stamped at write time", spec.Annotations)
	}

	// And the shape the builder picks from it is bearerToken, not exec.
	built, buildErr := argosecrets.BuildClusterSecret(spec, "argocd")
	if buildErr != nil {
		t.Fatalf("building the connection: %v", buildErr)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(built.StringData["config"]), &cfg); err != nil {
		t.Fatalf("data.config is not JSON: %v", err)
	}
	if cfg["bearerToken"] == nil {
		t.Errorf("the built connection does not use the bearerToken shape; top-level keys are %v", keysOf(cfg))
	}
	if cfg["execProviderConfig"] != nil {
		t.Error("the built connection fell through to the execProviderConfig shape although the backend gave a token")
	}
}

func TestConnectionCredentialSpecForWrite_RoleARNPrecedence(t *testing.T) {
	// The cluster's own roleArn wins; the connection default applies only when
	// the cluster has none. Both the value handed to the BACKEND (for the mint)
	// and the value put on the spec (for the exec shape's --role-arn) are
	// asserted, because those used to be two copies of the same three lines.
	tests := []struct {
		name         string
		entryRole    string
		defaultRole  string
		wantFetch    string // what the backend is asked to mint with
		wantSpecRole string // what lands on the spec
	}{
		{
			name:         "the cluster's own role wins",
			entryRole:    "arn:aws:iam::000000000000:role/cluster-own",
			defaultRole:  "arn:aws:iam::000000000000:role/connection-default",
			wantFetch:    "arn:aws:iam::000000000000:role/cluster-own",
			wantSpecRole: "arn:aws:iam::000000000000:role/cluster-own",
		},
		{
			name:         "no cluster role falls back to the connection default",
			entryRole:    "",
			defaultRole:  "arn:aws:iam::000000000000:role/connection-default",
			wantFetch:    "", // the fetch forwards the ENTRY's role, which is empty
			wantSpecRole: "arn:aws:iam::000000000000:role/connection-default",
		},
		{
			name:         "neither means no role at all",
			entryRole:    "",
			defaultRole:  "",
			wantFetch:    "",
			wantSpecRole: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vault := &roleRecordingVault{creds: &providers.Kubeconfig{
				Server: "https://roles.test.invalid",
				Token:  "made-up-not-a-real-token",
			}}
			r := &Reconciler{deps: Deps{Vault: vault, DefaultRoleARN: tt.defaultRole}}

			spec, err := r.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{
				Name:    "roles",
				RoleARN: tt.entryRole,
			})
			if err != nil {
				t.Fatalf("the write route failed: %v", err)
			}

			if spec.RoleARN != tt.wantSpecRole {
				t.Errorf("spec.RoleARN = %q, want %q", spec.RoleARN, tt.wantSpecRole)
			}
			if tt.wantFetch != "" {
				if vault.roleCalls != 1 {
					t.Errorf("the backend's role-aware fetch was called %d time(s), want 1 — a cross-account cluster's token must be minted with its own role", vault.roleCalls)
				}
				if vault.gotRoleARN != tt.wantFetch {
					t.Errorf("the backend was asked to fetch with role %q, want %q", vault.gotRoleARN, tt.wantFetch)
				}
			} else if vault.roleCalls != 0 {
				t.Errorf("the backend's role-aware fetch was called %d time(s) with no per-cluster role stored; want the plain fetch", vault.roleCalls)
			}
		})
	}
}

func TestConnectionCredentialSpecForWrite_UsesTheSecretPathOverride(t *testing.T) {
	// The backend lookup key is the cluster's secretPath when one is stored, not
	// its name — the same shared resolver every other fetch uses.
	vault := &roleRecordingVault{creds: &providers.Kubeconfig{Server: "https://x.test.invalid", Token: "t"}}
	r := &Reconciler{deps: Deps{Vault: vault}}

	if _, err := r.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{
		Name:       "friendly-name",
		SecretPath: "clusters/prod/real-path",
	}); err != nil {
		t.Fatalf("the write route failed: %v", err)
	}

	if vault.gotLookupKey != "clusters/prod/real-path" {
		t.Errorf("the backend was asked for %q, want the stored secretPath %q", vault.gotLookupKey, "clusters/prod/real-path")
	}
}

func TestConnectionCredentialSpecForWrite_RefusesInsteadOfReturningAnEmptySpec(t *testing.T) {
	// A backend that cannot answer must produce an ERROR, so the caller writes
	// nothing. Returning a usable-looking spec with no credential in it is
	// exactly how a missing token turned into a changed authentication method
	// instead of a refusal.
	backendFailure := errors.New("the backend did not answer")
	vault := &roleRecordingVault{err: backendFailure}
	r := &Reconciler{deps: Deps{Vault: vault}}

	spec, err := r.ConnectionCredentialSpecForWrite(models.ManagedClusterEntry{Name: "unreachable"})
	if err == nil {
		t.Fatal("a backend failure returned no error; the caller would go on to write a spec with no credential in it")
	}
	if !errors.Is(err, backendFailure) {
		t.Errorf("the error lost its cause: %v — the caller decides what to say, and it decides by TYPE", err)
	}
	if spec.Server != "" || spec.Token != "" || spec.CAData != "" {
		t.Errorf("a refusal came back with credential fields filled in: server set = %v, token set = %v, ca set = %v",
			spec.Server != "", spec.Token != "", spec.CAData != "")
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
