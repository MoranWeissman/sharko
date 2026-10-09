package schema

import (
	"errors"
	"strings"
	"testing"
)

// V2-cleanup-60.2 (H2 forward guard) — an apiVersion that starts with
// `sharko.` but is NOT in the accepted set (sharko.dev/v1, sharko.io/v1)
// must be a hard, actionable error. It must NEVER fall through to the
// bare-YAML legacy reader: that fallthrough is exactly how a v2.1.x binary
// reading a sharko.dev/v1 file "saw" zero clusters and orphan-swept every
// Sharko-managed ArgoCD cluster Secret. This binary can't fix old binaries,
// but it makes the NEXT identity/version change structurally incapable of
// repeating the failure.

func mcBody(apiVersion string) []byte {
	return []byte("apiVersion: " + apiVersion + `
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`)
}

// TestForwardGuard_AcceptedGroupsStillParse pins that BOTH accepted groups
// keep routing to the enveloped reader path — the guard must not narrow the
// accepted set.
func TestForwardGuard_AcceptedGroupsStillParse(t *testing.T) {
	t.Parallel()
	for _, v := range []string{APIVersion, APIVersionLegacy} {
		enveloped, err := IsEnveloped(mcBody(v))
		if err != nil {
			t.Fatalf("IsEnveloped(apiVersion=%s): unexpected error: %v", v, err)
		}
		if !enveloped {
			t.Fatalf("IsEnveloped(apiVersion=%s) = false, want true", v)
		}
	}
}

// TestForwardGuard_UnknownSharkoVersions_HardError pins the guard itself:
// unknown sharko.* apiVersions return a typed, actionable error and never
// route to either reader path.
func TestForwardGuard_UnknownSharkoVersions_HardError(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"sharko.dev/v2", "sharko.example/v1", "sharko.io/v2"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			enveloped, err := IsEnveloped(mcBody(v))
			if enveloped {
				t.Fatalf("IsEnveloped(apiVersion=%s) = true, want false", v)
			}
			if err == nil {
				t.Fatalf("IsEnveloped(apiVersion=%s): want hard error, got nil (silent legacy fallthrough — the H2 failure class)", v)
			}
			var unknown *UnknownSharkoAPIVersionError
			if !errors.As(err, &unknown) {
				t.Fatalf("IsEnveloped(apiVersion=%s): error is %T, want *UnknownSharkoAPIVersionError (%v)", v, err, err)
			}
			if unknown.Found != v {
				t.Errorf("UnknownSharkoAPIVersionError.Found = %q, want %q", unknown.Found, v)
			}
			// The message must be actionable: name the found version, the
			// accepted versions, and the "written by a newer/unknown Sharko"
			// diagnosis.
			msg := err.Error()
			for _, want := range []string{v, APIVersion, APIVersionLegacy, "refusing to guess"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error message missing %q: %s", want, msg)
				}
			}
		})
	}
}

// TestForwardGuard_NonSharkoAndBareYAML_Unchanged pins that the guard is
// scoped to the sharko.* family only — plain bare YAML (no apiVersion) and
// foreign apiVersions still route to the legacy reader without error.
func TestForwardGuard_NonSharkoAndBareYAML_Unchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body []byte
	}{
		{
			name: "bare yaml without apiVersion",
			body: []byte("clusters:\n  - name: prod-eu\n"),
		},
		{
			name: "k8s core apiVersion",
			body: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: foo\n"),
		},
		{
			name: "foreign group apiVersion",
			body: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: foo\n"),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enveloped, err := IsEnveloped(tc.body)
			if err != nil {
				t.Fatalf("IsEnveloped: unexpected error: %v", err)
			}
			if enveloped {
				t.Fatal("IsEnveloped = true, want false (legacy routing)")
			}
		})
	}
}
