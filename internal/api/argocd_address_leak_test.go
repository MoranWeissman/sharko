package api

// argocd_address_leak_test.go — BF8, at the boundary a person actually reads.
//
// The unit proof lives in internal/argocd. This file drives the three HTTP
// doors the address was proven to reach, plus the one that needed no failure
// at all — the connection list, which handed the saved address to every
// signed-in viewer on an ordinary successful response.
//
// The sweep and the sentinel are init_status_leak_test.go's, reused: same
// package, same finder, same positive control.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// addressLeakCarriers are the four shapes a credential is written into an
// ArgoCD server address in. Each is built around a host that is not there.
func addressLeakCarriers(hostPort string) []struct{ name, url string } {
	return []struct{ name, url string }{
		{"password slot", "http://x-access-token:" + initLeakSentinel + "@" + hostPort},
		{"username slot", "http://" + initLeakSentinel + "@" + hostPort},
		{"query parameter", "http://" + hostPort + "?access_token=" + initLeakSentinel},
		{"fragment", "http://" + hostPort + "#" + initLeakSentinel},
	}
}

// newServerWithArgocdAddress builds a Server whose ACTIVE connection has a
// well-formed Git half and the given ArgoCD address.
//
// It does not reuse newTestServerWithArgocd: that helper writes a Git block
// whose keys do not match the model, so the connection has no usable Git
// provider and every door refuses before it ever dials ArgoCD. Every
// assertion in this file would then pass on a request that never went
// anywhere. The doors below check for that explicitly.
func newServerWithArgocdAddress(t *testing.T, argoURL, gitToken string) *Server {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sharko-conn-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	store := config.NewFileStore(f.Name())
	if err := store.SaveConnection(models.Connection{
		Name: "test",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "sharko-org",
			Repo:     "addons",
			Token:    gitToken,
		},
		Argocd: models.ArgocdConfig{
			ServerURL: argoURL,
			Token:     "argocd-test-token",
			Namespace: "argocd",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveConnection("test"); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer()
	connSvc := service.NewConnectionService(store)
	// A fake Git provider, so the doors below get PAST the Git half and
	// actually dial ArgoCD. Without it every door refuses on Git first and
	// this file proves nothing — and it would reach out to a real Git host
	// while doing it.
	connSvc.SetGitProviderOverride(newSecretPathFakeGP(map[string][]byte{}))
	srv.connSvc = connSvc
	return srv
}

// deadLoopbackHostPort returns a host:port on the loopback interface that
// nothing is listening on. No network beyond this machine is touched.
func deadLoopbackHostPort(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	hostPort := strings.TrimPrefix(ts.URL, "http://")
	ts.Close()
	return hostPort
}

// TestAddressLeakPositiveControl_TheDialReallyFails proves the fixture is in
// play before any absence below is believed: the host really is dead, and a
// bare client really does quote back the three carriers net/http does not
// mask. Without this, a port that quietly answered would make every assertion
// in this file pass while proving nothing.
func TestAddressLeakPositiveControl_TheDialReallyFails(t *testing.T) {
	hostPort := deadLoopbackHostPort(t)
	for _, carrier := range addressLeakCarriers(hostPort) {
		t.Run(carrier.name, func(t *testing.T) {
			_, err := (&http.Client{}).Get(carrier.url + "/api/v1/clusters")
			if err == nil {
				t.Fatal("the dial to a dead loopback port succeeded — this file can prove nothing")
			}
			carries := strings.Contains(err.Error(), initLeakSentinel)
			if carrier.name == "password slot" {
				// net/http masks a password before it builds the error. Said
				// honestly rather than forced: the standard library is what
				// makes this one shape safe, not Sharko.
				if carries {
					t.Errorf("net/http stopped masking the password slot: %v", err)
				}
				return
			}
			if !carries {
				t.Fatalf("a bare client no longer quotes the %s — the rest of this file sweeps for something that was never in play.\n\nthe error was:\n%v", carrier.name, err)
			}
		})
	}
}

// TestClusterDoors_NeverQuoteTheArgocdAddress drives the two 502 doors the
// address was proven to reach: adopt and register. Both write err.Error()
// straight into the response body, so what they say is whatever the ArgoCD
// client handed them.
func TestClusterDoors_NeverQuoteTheArgocdAddress(t *testing.T) {
	hostPort := deadLoopbackHostPort(t)

	doors := []struct {
		name, method, path, body string
	}{
		{
			name:   "adopt",
			method: http.MethodPost,
			path:   "/api/v1/clusters/adopt",
			body:   `{"clusters":["prod-eu"]}`,
		},
		{
			name:   "register",
			method: http.MethodPost,
			path:   "/api/v1/clusters",
			body:   `{"name":"prod-eu","region":"eu-west-1"}`,
		},
	}

	for _, carrier := range addressLeakCarriers(hostPort) {
		for _, door := range doors {
			t.Run(carrier.name+"/"+door.name, func(t *testing.T) {
				srv := newServerWithArgocdAddress(t, carrier.url, "git-test-token")
				router := NewRouter(srv, nil)

				var body string
				var status int
				logs := captureSlog(t, func() {
					req := httptest.NewRequest(door.method, door.path, strings.NewReader(door.body))
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					status = w.Code
					body = w.Body.String()
				})

				assertNoInitLeak(t, "the "+door.name+" response body when ArgoCD cannot be reached", body)
				assertNoInitLeak(t, "the log output for the "+door.name+" door", logs)

				// Non-vacuity. A door that refused earlier — on the Git half,
				// on a malformed body — never dialled ArgoCD, and an absence
				// swept out of a request that went nowhere is not a result.
				if status != http.StatusBadGateway {
					t.Fatalf("the %s door answered %d, not 502, so it never reached the ArgoCD dial and this case proved nothing.\n\nbody: %s", door.name, status, body)
				}
				if !strings.Contains(body, "Sharko could not get an answer from ArgoCD") {
					t.Fatalf("the %s door's 502 is not the unreachable-ArgoCD answer, so it failed on something else.\n\nbody: %s", door.name, body)
				}
			})
		}
	}
}

// TestInitOperationRecord_NeverQuotesTheArgocdAddress covers the third
// landing site: the operation session an init run writes its failure into,
// which the browser polls and renders line by line.
//
// It calls BootstrapArgoCD and then builds the EXACT string internal/api's
// init flow records —
//
//	s.opsStore.Fail(sessionID, "ArgoCD bootstrap failed: "+bootstrapErr.Error())
//
// rather than driving POST /api/v1/init end to end. Init only reaches this
// step after a pull request has been opened and merged, and a test that
// cannot get past the merge would record nothing and pass on an empty
// operation. This way the value that lands in the record is the value under
// test, and the assertion cannot go vacuous.
func TestInitOperationRecord_NeverQuotesTheArgocdAddress(t *testing.T) {
	hostPort := deadLoopbackHostPort(t)

	const rootApp = `apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: sharko
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-bootstrap
`

	for _, carrier := range addressLeakCarriers(hostPort) {
		t.Run(carrier.name, func(t *testing.T) {
			var gitMu sync.Mutex
			orch := orchestrator.New(
				&gitMu, nil,
				argocd.NewClient(carrier.url, "argocd-test-token", false),
				newSecretPathFakeGP(map[string][]byte{}),
				orchestrator.GitOpsConfig{}, orchestrator.RepoPathsConfig{}, nil,
			)

			var bootstrapErr error
			logs := captureSlog(t, func() {
				bootstrapErr = orch.BootstrapArgoCD(context.Background(), []byte(rootApp))
			})
			if bootstrapErr == nil {
				t.Fatal("bootstrapping against a dead ArgoCD succeeded — nothing was proven")
			}

			// The exact two strings internal/api/init.go records.
			recorded := "ArgoCD bootstrap failed: " + bootstrapErr.Error()
			assertNoInitLeak(t, "the init operation record", recorded)
			assertNoInitLeak(t, "the init operation step detail", bootstrapErr.Error())
			assertNoInitLeak(t, "the log output while bootstrapping", logs)

			// Non-vacuity: the failure really is the ArgoCD write going
			// nowhere, not something that happened before the dial.
			if !strings.Contains(recorded, "Sharko could not get an answer from ArgoCD") {
				t.Fatalf("the bootstrap did not fail on the ArgoCD write, so this case proved nothing: %q", recorded)
			}
		})
	}
}

// TestConnectionList_NeverHandsOutTheSavedAddressesCredential is the one that
// needed nothing to go wrong.
//
// GET /api/v1/connections is a viewer action. It used to return the saved
// ArgoCD address verbatim, sitting between two masking calls, so a credential
// written into that address came back to every signed-in account on an
// ordinary 200.
func TestConnectionList_NeverHandsOutTheSavedAddressesCredential(t *testing.T) {
	for _, carrier := range addressLeakCarriers("argocd.example:443") {
		t.Run(carrier.name, func(t *testing.T) {
			srv := newServerWithArgocdAddress(t, carrier.url, "git-test-token")
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("listing connections did not answer 200, so nothing was proven; got %d: %s", w.Code, w.Body.String())
			}
			assertNoInitLeak(t, "the connection list response", w.Body.String())

			// The host must still be there — it is not the secret, and an
			// operator needs to recognise their own ArgoCD.
			if !strings.Contains(w.Body.String(), "argocd.example") {
				t.Errorf("the connection list no longer names the ArgoCD host at all, so the operator cannot tell which server this is.\n\nbody: %s", w.Body.String())
			}
		})
	}
}

// TestSavedTokensComeBackAsAFixedMask covers the two masked token fields on
// the same response. The mask must be the same width whatever the token was,
// and must contain none of it.
func TestSavedTokensComeBackAsAFixedMask(t *testing.T) {
	short := models.MaskToken("ab")
	long := models.MaskToken(strings.Repeat("z", 512))
	if short != long {
		t.Errorf("the mask width follows the token: %q for a 2-character token, %q for a 512-character one. Width is a fact about a secret.", short, long)
	}
	if models.MaskToken("") != "" {
		t.Error(`an absent token must stay absent — "no token saved" is a different fact from "a token is saved"`)
	}
	const realToken = "ghp_1234567890abcdefghij"
	masked := models.MaskToken(realToken)
	for _, piece := range []string{realToken, realToken[:4], realToken[len(realToken)-4:]} {
		if strings.Contains(masked, piece) {
			t.Errorf("the mask %q still carries %q of the token", masked, piece)
		}
	}
}

// TestSavingACredentialBearingArgocdAddressIsRefused covers the save-time
// rule: all four carriers refused, and an ordinary address still accepted.
func TestSavingACredentialBearingArgocdAddressIsRefused(t *testing.T) {
	for _, carrier := range addressLeakCarriers("argocd.example:443") {
		t.Run(carrier.name, func(t *testing.T) {
			srv, path := newServerWithEmptyConnectionStore(t)
			router := NewRouter(srv, nil)
			_ = path

			payload := map[string]any{
				"name": "test",
				"git": map[string]any{
					"provider": "github", "owner": "sharko-org", "repo": "addons", "token": "t",
				},
				"argocd": map[string]any{"server_url": carrier.url, "token": "t"},
			}
			raw, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", strings.NewReader(string(raw)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("saving an ArgoCD address with a credential in it was not refused; got %d: %s", w.Code, w.Body.String())
			}
			// The refusal must say what to do and must not quote the address.
			if !strings.Contains(w.Body.String(), "The ArgoCD server address must be the address only") {
				t.Errorf("the refusal is not Sharko's fixed sentence: %s", w.Body.String())
			}
			assertNoInitLeak(t, "the refusal body", w.Body.String())
		})
	}

	t.Run("an ordinary address is still accepted", func(t *testing.T) {
		srv, _ := newServerWithEmptyConnectionStore(t)
		router := NewRouter(srv, nil)
		payload := `{"name":"test","git":{"provider":"github","owner":"sharko-org","repo":"addons","token":"t"},"argocd":{"server_url":"https://argocd.example","token":"t"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("an ordinary ArgoCD address was refused; got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestValidateArgocdServerURL_IsStructuralNotAGuessAtWhatLooksSecret pins the
// rule itself: it fires on the three characters a credential needs a place in,
// and on nothing else.
func TestValidateArgocdServerURL_IsStructuralNotAGuessAtWhatLooksSecret(t *testing.T) {
	for _, ok := range []string{
		"",
		"https://argocd.example",
		"https://argocd.example:8080/argo",
		"http://argocd-server.argocd.svc.cluster.local",
		"argocd.example",
	} {
		if err := models.ValidateArgocdServerURL(ok); err != nil {
			t.Errorf("an ordinary address was refused: %q — %v", ok, err)
		}
	}
	for _, bad := range []string{
		"https://tok@argocd.example",
		"https://user:tok@argocd.example",
		"https://argocd.example?access_token=tok",
		"https://argocd.example#tok",
	} {
		if err := models.ValidateArgocdServerURL(bad); err == nil {
			t.Errorf("an address with somewhere for a credential to be was accepted: %q", bad)
		}
	}
}

// newServerWithEmptyConnectionStore builds a Server whose connection store is
// a fresh empty file, so a create can be driven against it.
func newServerWithEmptyConnectionStore(t *testing.T) (*Server, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sharko-conn-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("connections: []\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer()
	srv.connSvc = service.NewConnectionService(config.NewFileStore(f.Name()))
	return srv, f.Name()
}

// TestSafeArgocdServerURL_KeepsTheHostAndDropsTheCredential pins what a person
// sees, exactly. A field that goes blank is a different bug from a field that
// leaks, and both have to be ruled out.
func TestSafeArgocdServerURL_KeepsTheHostAndDropsTheCredential(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://argocd.example", "https://argocd.example"},
		{"https://tok@argocd.example/argo", "https://argocd.example/argo"},
		{"https://x-access-token:tok@argocd.example", "https://argocd.example"},
		{"https://argocd.example?access_token=tok", "https://argocd.example"},
		{"https://argocd.example#tok", "https://argocd.example"},
		// No scheme and nowhere for a credential to be: shown whole, so the
		// operator gets an address rather than a blank.
		{"argocd.example", "argocd.example"},
		// Nothing Sharko can take apart: it says nothing at all.
		{"argocd@example:org/thing", ""},
		{"", ""},
	} {
		if got := models.SafeArgocdServerURL(tc.in); got != tc.want {
			t.Errorf("SafeArgocdServerURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// And the result must still be a usable address for the deep links the UI
	// builds out of it.
	u, err := url.Parse(models.SafeArgocdServerURL("https://tok@argocd.example:8080/argo"))
	if err != nil || u.Host != "argocd.example:8080" {
		t.Errorf("the masked address is no longer usable as a link base: %v (%v)", u, err)
	}
}
