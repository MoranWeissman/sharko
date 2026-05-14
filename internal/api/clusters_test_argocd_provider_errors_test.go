package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V125-1-10.3 — wire ArgoCDProvider's typed errors into the cluster Test
// endpoint so the UI gets a stable machine-readable error_code it can render
// branch-specific copy against (Story 10.5 will consume those codes).
//
// Pinned contract for POST /api/v1/clusters/{name}/test:
//   - credProvider == nil                        → 503 + error_code=no_secrets_backend (BUG-035)
//   - GetCredentials returns *ArgoCDProviderError (Code=…IAMRequired)
//                                                → 503 + error_code=argocd_provider_iam_required
//   - GetCredentials returns *ArgoCDProviderError (Code=…ExecUnsupported)
//                                                → 503 + error_code=argocd_provider_exec_unsupported
//   - GetCredentials returns *ArgoCDProviderError (Code=…UnsupportedAuth)
//                                                → 503 + error_code=argocd_provider_unsupported_auth
//   - GetCredentials returns a generic non-typed error
//                                                → 200 + verify.Result with success=false (existing
//                                                  behavior — preserved so the UI's existing
//                                                  step-by-step rendering keeps working for the
//                                                  "secret not found" / "wrong path" common case)
//   - GetCredentials returns a *Kubeconfig successfully
//                                                → falls through to the next step (kubeconfig parse).
//                                                  In this test the synthesized kubeconfig is
//                                                  invalid YAML so we observe the build-client step
//                                                  failing — that is a 200 with the credentials
//                                                  step reported as "pass", which proves the typed-
//                                                  error branch did NOT swallow the happy path.
//
// The four typed-error cases are the real product surface this story unlocks;
// the no-backend regression guard exists to prove the ArgoCDProvider wiring did
// not accidentally widen the structured-503 path to swallow the BUG-035 hint
// field. The generic-error case proves the new dispatch is precise (errors.As
// only matches *ArgoCDProviderError) and not e.g. errors.Is on every error.

// ----- helpers -------------------------------------------------------------

// installFakeCredProvider mounts a function-backed ClusterCredentialsProvider
// stub on the test server. Tests in the api package can write directly to the
// unexported field; this avoids adding a new exported setter just for tests.
func installFakeCredProvider(t *testing.T, srv *Server, getCreds func(name string) (*providers.Kubeconfig, error)) {
	t.Helper()
	srv.credProvider = &cpStub{getCreds: getCreds}
}

// cpStub satisfies providers.ClusterCredentialsProvider with a function-based
// GetCredentials and stub no-ops for ListClusters / SearchSecrets / HealthCheck.
// The other three methods are never called by the cluster-test handler so the
// stubs only need to satisfy the type system.
type cpStub struct {
	getCreds func(name string) (*providers.Kubeconfig, error)
}

func (s *cpStub) GetCredentials(name string) (*providers.Kubeconfig, error) {
	return s.getCreds(name)
}

func (s *cpStub) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (s *cpStub) SearchSecrets(query string) ([]string, error)   { return nil, nil }
func (s *cpStub) HealthCheck(ctx context.Context) error          { return nil }

// Compile-time guard that cpStub fully satisfies ClusterCredentialsProvider.
var _ providers.ClusterCredentialsProvider = (*cpStub)(nil)

// postTestCluster issues POST /api/v1/clusters/{name}/test against srv and
// returns the response code + decoded body (untyped — caller asserts shape).
func postTestCluster(t *testing.T, srv *Server, clusterName string) (int, map[string]interface{}) {
	t.Helper()
	router := NewRouter(srv, nil)
	body, _ := json.Marshal(map[string]bool{"deep": false})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterName+"/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var decoded map[string]interface{}
	if w.Body.Len() > 0 {
		if err := json.NewDecoder(w.Body).Decode(&decoded); err != nil {
			t.Fatalf("response is not valid JSON: %v (raw=%q)", err, w.Body.String())
		}
	}
	return w.Code, decoded
}

// ----- tests ---------------------------------------------------------------

func TestTestCluster_ArgoCD_IAMRequired_Returns503WithCode(t *testing.T) {
	srv := newIsolatedTestServer(t)
	installFakeCredProvider(t, srv, func(name string) (*providers.Kubeconfig, error) {
		return nil, &providers.ArgoCDProviderError{
			Code:        providers.ArgoCDProviderCodeIAMRequired,
			ClusterName: name,
			Server:      "https://eks-cluster.example",
			Detail:      "cluster \"" + name + "\" uses AWS IAM authentication (awsAuthConfig). Configure AWS credentials for the Sharko pod's role to enable connectivity tests.",
		}
	})

	code, body := postTestCluster(t, srv, "iam-cluster")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if got, want := body["error_code"], providers.ArgoCDProviderCodeIAMRequired; got != want {
		t.Errorf("error_code = %v, want %q", got, want)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("error message must be non-empty (UI surfaces it as the actionable detail)")
	}
}

func TestTestCluster_ArgoCD_ExecUnsupported_Returns503WithCode(t *testing.T) {
	srv := newIsolatedTestServer(t)
	installFakeCredProvider(t, srv, func(name string) (*providers.Kubeconfig, error) {
		return nil, &providers.ArgoCDProviderError{
			Code:        providers.ArgoCDProviderCodeExecUnsupported,
			ClusterName: name,
			Server:      "https://exec-cluster.example",
			Detail:      "cluster \"" + name + "\" uses exec-plugin auth (command \"aws\"). Exec plugins are not supported in v1.x; tracked for v2.",
		}
	})

	code, body := postTestCluster(t, srv, "exec-cluster")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if got, want := body["error_code"], providers.ArgoCDProviderCodeExecUnsupported; got != want {
		t.Errorf("error_code = %v, want %q", got, want)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("error message must be non-empty (UI surfaces it as the actionable detail)")
	}
}

func TestTestCluster_ArgoCD_UnsupportedAuth_Returns503WithCode(t *testing.T) {
	srv := newIsolatedTestServer(t)
	installFakeCredProvider(t, srv, func(name string) (*providers.Kubeconfig, error) {
		return nil, &providers.ArgoCDProviderError{
			Code:        providers.ArgoCDProviderCodeUnsupportedAuth,
			ClusterName: name,
			Server:      "https://weird-cluster.example",
			Detail:      "argocd cluster secret for \"" + name + "\" has no recognised auth shape (expected bearerToken, awsAuthConfig, or execProviderConfig)",
		}
	})

	code, body := postTestCluster(t, srv, "weird-cluster")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if got, want := body["error_code"], providers.ArgoCDProviderCodeUnsupportedAuth; got != want {
		t.Errorf("error_code = %v, want %q", got, want)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("error message must be non-empty (UI surfaces it as the actionable detail)")
	}
}

// Regression guard for BUG-035 — the typed-error wiring above must NOT have
// widened to swallow the no-credProvider case. credProvider==nil still routes
// to the original structured response with `hint`, distinct from the new
// argocd_provider_* envelope (which intentionally omits hint per design —
// the actionable next step varies per branch and is encoded in the message).
func TestTestCluster_NoSecretsBackend_RegressionGuard(t *testing.T) {
	srv := newIsolatedTestServer(t)
	// credProvider is intentionally nil.

	code, body := postTestCluster(t, srv, "any-cluster")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if got, want := body["error_code"], "no_secrets_backend"; got != want {
		t.Errorf("error_code = %v, want %q (BUG-035 contract — UI keys off this)", got, want)
	}
	if hint, _ := body["hint"].(string); hint == "" {
		t.Errorf("BUG-035 hint must remain populated (operator-actionable next step)")
	}
}

// Generic (non-ArgoCDProviderError) errors must NOT route through the
// structured-503 envelope — they continue to surface via the existing
// HTTP 200 + verify.Result + Steps shape. This proves errors.As is precise.
func TestTestCluster_GenericError_StaysOnVerifyResultPath(t *testing.T) {
	srv := newIsolatedTestServer(t)
	installFakeCredProvider(t, srv, func(name string) (*providers.Kubeconfig, error) {
		return nil, errors.New("generic boom: secret not reachable")
	})

	code, body := postTestCluster(t, srv, "generic-error-cluster")

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (generic errors must stay on verify.Result path)", code)
	}
	// Must NOT carry an error_code (that field is reserved for the
	// structured-503 envelope; verify.Result uses error_code for verify.ErrorCode).
	// What matters: success=false and no argocd_provider_* code at the top.
	if success, _ := body["success"].(bool); success {
		t.Errorf("success = true, want false on generic credential failure")
	}
	if ec, _ := body["error_code"].(string); ec != "" && (ec == providers.ArgoCDProviderCodeIAMRequired ||
		ec == providers.ArgoCDProviderCodeExecUnsupported || ec == providers.ArgoCDProviderCodeUnsupportedAuth) {
		t.Errorf("error_code = %q must not be one of the argocd_provider_* codes for a generic error", ec)
	}
}

// Happy-path proof — when GetCredentials succeeds, the handler does NOT
// short-circuit through the typed-error branch and instead proceeds into
// the verify pipeline. We rely on the synthesized kubeconfig being too
// minimal for remoteclient.NewClientFromKubeconfig to fully exercise; that
// returns 200 with credentials step reported as "pass" — sufficient signal
// that the typed-error branch did not steal the happy path.
func TestTestCluster_GetCredentialsSuccess_ContinuesPipeline(t *testing.T) {
	srv := newIsolatedTestServer(t)
	installFakeCredProvider(t, srv, func(name string) (*providers.Kubeconfig, error) {
		// Minimal kubeconfig that parses but points at an unreachable
		// server. remoteclient.NewClientFromKubeconfig will succeed
		// constructing the client (the dial happens later), so the
		// handler will then attempt verify.Stage1 against an
		// unreachable endpoint. Either Stage1 fails on dial or
		// remoteclient errors out — both paths return 200 with the
		// credentials step reported as "pass", which is what we need
		// to prove the typed-error branch did not preempt success.
		raw := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
  name: ` + name + `
contexts:
- context:
    cluster: ` + name + `
    user: ` + name + `
  name: ` + name + `
current-context: ` + name + `
users:
- name: ` + name + `
  user:
    token: dummy
`)
		return &providers.Kubeconfig{
			Raw:    raw,
			Server: "https://127.0.0.1:1",
			Token:  "dummy",
		}, nil
	})

	code, body := postTestCluster(t, srv, "happy-cluster")

	// The downstream pipeline returns 200 even on dial failure (the
	// failure surfaces through verify.Result.Steps); 503 here would
	// indicate the typed-error branch ate the happy path.
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (typed-error branch must not preempt the happy GetCredentials path)", code)
	}
	// Body must NOT carry an argocd_provider_* error_code at the top.
	if ec, _ := body["error_code"].(string); ec == providers.ArgoCDProviderCodeIAMRequired ||
		ec == providers.ArgoCDProviderCodeExecUnsupported ||
		ec == providers.ArgoCDProviderCodeUnsupportedAuth {
		t.Errorf("error_code = %q — typed-error branch must not have fired on a successful GetCredentials", ec)
	}
	// Steps slice should be present and the credentials step should be "pass".
	steps, _ := body["steps"].([]interface{})
	if len(steps) == 0 {
		t.Fatalf("expected steps in response body, got none (body=%v)", body)
	}
	first, _ := steps[0].(map[string]interface{})
	if first["name"] != "Fetch credentials" {
		t.Errorf("first step name = %v, want %q", first["name"], "Fetch credentials")
	}
	if first["status"] != "pass" {
		t.Errorf("first step status = %v, want %q (proves credentials.GetCredentials returned successfully)", first["status"], "pass")
	}
}
