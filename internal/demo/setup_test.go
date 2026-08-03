package demo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/advisories"
	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/service"
)

// demoLoginToken logs in as the demo admin user (SetupDemoServer always
// creates admin/admin) and returns the bearer token — demo mode creates
// real users, so the router's basicAuthMiddleware requires a valid session
// like any other authenticated caller would present.
func demoLoginToken(t *testing.T, router http.Handler) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": "admin", "password": "admin"})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v; body = %s", err, rw.Body.String())
	}
	if resp.Token == "" {
		t.Fatal("login response had no token")
	}
	return resp.Token
}

// newTestServer builds a minimal but real *api.Server the same way
// cmd/sharko/serve.go does (NewServer + the constructor chain), so this
// test exercises the real HTTP router rather than calling an unexported
// handler directly. Every dependency SetupDemoServer doesn't itself
// replace is nil-safe for the one endpoint this test hits
// (/api/v1/system/capabilities never touches connSvc/clusterSvc/etc).
func newTestServer(t *testing.T) *api.Server {
	t.Helper()
	connSvc := service.NewConnectionService(nil)
	clusterSvc := service.NewClusterService("configuration/managed-clusters.yaml")
	addonSvc := service.NewAddonService("configuration/managed-clusters.yaml")
	dashboardSvc := service.NewDashboardService(connSvc, "configuration/managed-clusters.yaml")
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	advSvc := advisories.NewService(nil)
	aiClient := ai.NewClient(ai.Config{})
	upgradeSvc := service.NewUpgradeService(aiClient, advSvc, "configuration/managed-clusters.yaml")
	return api.NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)
}

// TestSetupDemoServer_FakeAWSIdentity is S1 of the maintainer's 50-cluster
// walk: a real demo instance displayed the maintainer's own AWS work
// identity, because nothing stopped GET /system/capabilities from running
// real sts:GetCallerIdentity detection against the host's ambient AWS
// credential chain. SetupDemoServer must inject a deterministic fake
// identity BEFORE that endpoint can ever build the real detector, so the
// response is always the placeholder demo identity — never whatever
// happens to be ambient on the machine running the demo.
func TestSetupDemoServer_FakeAWSIdentity(t *testing.T) {
	srv := newTestServer(t)

	cleanup, err := SetupDemoServer(srv, DefaultScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	var body struct {
		AWS struct {
			Detected    bool   `json:"detected"`
			Method      string `json:"method"`
			IdentityARN string `json:"identity_arn"`
		} `json:"aws"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rw.Body.String())
	}

	// The exact fake identity SetupDemoServer injects. Not the maintainer's
	// real ARN, not any other cluster's real ARN — a placeholder account
	// number and a role name that says "demo" plainly.
	const wantARN = "arn:aws:iam::000000000000:role/sharko-demo"
	const wantMethod = "demo"

	if !body.AWS.Detected {
		t.Error("aws.detected = false, want true (demo mode always reports the fake identity as detected)")
	}
	if body.AWS.Method != wantMethod {
		t.Errorf("aws.method = %q, want %q", body.AWS.Method, wantMethod)
	}
	if body.AWS.IdentityARN != wantARN {
		t.Errorf("aws.identity_arn = %q, want %q", body.AWS.IdentityARN, wantARN)
	}
}

// TestSetupDemoServer_FakeAWSIdentity_BigEstate repeats the check against
// the generated (non-default) estate path — a second, independent code
// path inside SetupDemoServer (the `estate != nil` branch) that must not
// skip the AWS-detector injection, since that injection happens once up
// front regardless of estate size.
func TestSetupDemoServer_FakeAWSIdentity_BigEstate(t *testing.T) {
	srv := newTestServer(t)

	cleanup, err := SetupDemoServer(srv, ScaleConfig{Clusters: 6, Addons: 4, Seed: 1})
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	var body struct {
		AWS struct {
			Method      string `json:"method"`
			IdentityARN string `json:"identity_arn"`
		} `json:"aws"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rw.Body.String())
	}
	if body.AWS.IdentityARN != "arn:aws:iam::000000000000:role/sharko-demo" {
		t.Errorf("aws.identity_arn = %q, want the fake demo ARN", body.AWS.IdentityARN)
	}
	if body.AWS.Method != "demo" {
		t.Errorf("aws.method = %q, want %q", body.AWS.Method, "demo")
	}
}
