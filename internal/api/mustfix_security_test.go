package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
)

// Review must-fix 1: an admin makes an admin key and is then moved down to
// viewer. The key kept admin and could create admin users.
func TestMustFix_AKeyNeverHasMoreThanItsMakerHasNow(t *testing.T) {
	srv, h := walkCServer(t, "admin") // bob is admin
	bob := walkCLogin(t, srv, "bob")
	key := walkCCreateKey(t, h, bob, "bob-ci", "admin")
	lowKey := walkCCreateKey(t, h, bob, "bob-read", "viewer")

	root := walkCLogin(t, srv, "root-admin")
	if rw := walkCCall(t, h, root, http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "operator"}); rw.Code != http.StatusOK {
		t.Fatalf("move bob to operator: %d %s", rw.Code, rw.Body.String())
	}

	me := func(token string) string {
		t.Helper()
		rw := walkCCall(t, h, token, http.MethodGet, "/api/v1/users/me", nil)
		if rw.Code != http.StatusOK {
			t.Fatalf("/users/me with the key: %d %s", rw.Code, rw.Body.String())
		}
		var got struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(rw.Body.Bytes(), &got)
		return got.Role
	}
	if got := me(key); got != "operator" {
		t.Errorf("admin key of a user who is now operator works as %q, want operator", got)
	}
	if got := me(lowKey); got != "viewer" {
		t.Errorf("a viewer key must stay viewer, got %q", got)
	}
	rw := walkCCall(t, h, key, http.MethodPost, "/api/v1/users", map[string]any{"username": "backdoor", "role": "admin"})
	if rw.Code != http.StatusForbidden {
		t.Errorf("the key of a user who is no longer admin created a user: %d %s", rw.Code, rw.Body.String())
	}
	if srv.authStore.GetUser("backdoor") != nil {
		t.Error("the user was created anyway")
	}

	// Down to viewer: the key follows.
	if rw := walkCCall(t, h, root, http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "viewer"}); rw.Code != http.StatusOK {
		t.Fatalf("move bob to viewer: %d", rw.Code)
	}
	if got := me(key); got != "viewer" {
		t.Errorf("admin key of a user who is now viewer works as %q, want viewer", got)
	}
	// Back up to admin: the key gets its own saved role back, no more.
	if rw := walkCCall(t, h, root, http.MethodPut, "/api/v1/users/bob", map[string]any{"role": "admin"}); rw.Code != http.StatusOK {
		t.Fatalf("move bob back to admin: %d", rw.Code)
	}
	if got := me(key); got != "admin" {
		t.Errorf("after the maker is admin again the admin key works as %q, want admin", got)
	}
	if got := me(lowKey); got != "viewer" {
		t.Errorf("a viewer key must never grow, got %q", got)
	}
}

// Review must-fix 2: the saved AI key was sent to any new address named in a
// test or a save that left the key out.
func TestMustFix_SavedAIKeyIsNotSentToANewAddress(t *testing.T) {
	const savedKey = "sk-saved-key-for-the-test-123456"
	const want = "The saved API key is only used with the saved address. This is a different address, so type the API key again."
	if auth.ReservedNameSentence == "" || AIKeyNeededForNewAddressSentence != want {
		t.Fatalf("AIKeyNeededForNewAddressSentence = %q, want %q", AIKeyNeededForNewAddressSentence, want)
	}

	var calls atomic.Int32
	var gotAuth atomic.Value
	gotAuth.Store("")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer other.Close()
	saved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer saved.Close()

	srv, h := walkCServer(t, "viewer")
	savedCfg := ai.Config{Provider: ai.ProviderCustomOpenAI, APIKey: savedKey, CloudModel: "m", BaseURL: saved.URL}
	srv.aiClient.SetConfig(savedCfg)
	t.Cleanup(func() { srv.aiClient.SetConfig(ai.Config{}) })
	root := walkCLogin(t, srv, "root-admin")

	for _, path := range []string{"/api/v1/ai/test-config", "/api/v1/ai/config"} {
		rw := walkCCall(t, h, root, http.MethodPost, path, map[string]any{"provider": "custom-openai", "base_url": other.URL})
		if rw.Code != http.StatusBadRequest {
			t.Errorf("%s with a new address and no key: %d %s, want 400", path, rw.Code, rw.Body.String())
		}
		if got := walkCError(rw); got != want {
			t.Errorf("%s error = %q, want %q", path, got, want)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the new address was called %d time(s) with Authorization=%q; it must not be called at all", n, gotAuth.Load())
	}
	if got := srv.aiClient.GetConfig(); got.BaseURL != saved.URL || got.APIKey != savedKey {
		t.Errorf("the refused save changed what is saved: address %q", got.BaseURL)
	}

	// The same address, written with a closing slash, is the saved address:
	// the saved key is still used, so "change only the model" keeps working.
	rw := walkCCall(t, h, root, http.MethodPost, "/api/v1/ai/test-config", map[string]any{"provider": "custom-openai", "base_url": saved.URL + "/"})
	if rw.Code != http.StatusOK {
		t.Fatalf("test-config on the saved address: %d %s", rw.Code, rw.Body.String())
	}
	if got, _ := gotAuth.Load().(string); got != "Bearer "+savedKey {
		t.Errorf("the saved address got Authorization=%q, want the saved key", got)
	}

	// A new address WITH a key typed is fine, and gets the typed key only.
	gotAuth.Store("")
	rw = walkCCall(t, h, root, http.MethodPost, "/api/v1/ai/test-config", map[string]any{"provider": "custom-openai", "base_url": other.URL, "api_key": "typed-key", "model": "m"})
	if rw.Code != http.StatusOK {
		t.Fatalf("test-config on a new address with a typed key: %d %s", rw.Code, rw.Body.String())
	}
	if got, _ := gotAuth.Load().(string); got != "Bearer typed-key" {
		t.Errorf("the new address got Authorization=%q, want the typed key", got)
	}
}

func TestMustFix_AIAddressChanged(t *testing.T) {
	cases := []struct {
		typed, saved string
		want         bool
	}{
		{"", "https://a.example.com", false},
		{"https://a.example.com", "https://a.example.com", false},
		{"https://a.example.com/", "https://a.example.com", false},
		{" https://a.example.com ", "https://a.example.com/", false},
		{"https://b.example.com", "https://a.example.com", true},
		{"http://a.example.com", "https://a.example.com", true},
		{"https://a.example.com:8443", "https://a.example.com", true},
		{"https://a.example.com/v2", "https://a.example.com", true},
		{"https://a.example.com", "", true},
	}
	for _, c := range cases {
		if got := aiAddressChanged(c.typed, c.saved); got != c.want {
			t.Errorf("aiAddressChanged(%q, %q) = %v, want %v", c.typed, c.saved, got, c.want)
		}
	}
}

// openAuditStream opens the live stream and hands back its lines, and a
// channel that is closed when the server ends the stream.
func openAuditStream(t *testing.T, srv *Server, ts *httptest.Server, token string, inQuery bool) (lines chan string, ended chan struct{}) {
	t.Helper()
	lines = make(chan string, 50)
	ended = make(chan struct{})
	opened := make(chan int, 1)
	go func() {
		url := ts.URL + "/api/v1/audit/stream"
		if inQuery {
			url += "?token=" + token
		}
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if !inQuery {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			opened <- 0
			close(ended)
			return
		}
		defer resp.Body.Close()
		opened <- resp.StatusCode
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(ended)
	}()
	// The answer headers go out with the first row, so push rows until the
	// stream is open.
	deadline := time.After(5 * time.Second)
	for {
		srv.auditLog.Add(audit.Entry{Event: "first_row", User: "x"})
		select {
		case code := <-opened:
			if code != http.StatusOK {
				t.Fatalf("open stream: %d", code)
			}
			return lines, ended
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("the stream did not open")
		}
	}
}

// noRowAfter fails when the named row arrives on the stream.
func noRowAfter(t *testing.T, lines chan string, ended chan struct{}, event string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case l := <-lines:
			if strings.Contains(l, event) {
				t.Fatalf("a caller who is no longer allowed still got the row: %s", l)
			}
		case <-ended:
			// Drain what was already on the way.
			for {
				select {
				case l := <-lines:
					if strings.Contains(l, event) {
						t.Fatalf("a caller who is no longer allowed still got the row: %s", l)
					}
				default:
					return
				}
			}
		case <-deadline:
			t.Fatal("the stream was not closed")
		}
	}
}

// Review must-fix 3: an audit stream opened earlier went on after log out,
// disable and delete.
func TestMustFix_AuditStreamEndsWhenTheCallerIsNoLongerAllowed(t *testing.T) {
	// The timer is long here on purpose: these cases must be stopped by the
	// check before each row, not by the timer.
	old := auditStreamRecheckEvery
	auditStreamRecheckEvery = time.Hour
	t.Cleanup(func() { auditStreamRecheckEvery = old })

	type step func(t *testing.T, srv *Server, h http.Handler, bob, root string)
	cases := map[string]step{
		"log out": func(t *testing.T, _ *Server, h http.Handler, bob, _ string) {
			if rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/auth/logout", nil); rw.Code != http.StatusOK {
				t.Fatalf("logout: %d", rw.Code)
			}
		},
		"disabled": func(t *testing.T, _ *Server, h http.Handler, _, root string) {
			if rw := walkCCall(t, h, root, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": false}); rw.Code != http.StatusOK {
				t.Fatalf("disable: %d %s", rw.Code, rw.Body.String())
			}
		},
		"deleted": func(t *testing.T, _ *Server, h http.Handler, _, root string) {
			if rw := walkCCall(t, h, root, http.MethodDelete, "/api/v1/users/bob", nil); rw.Code != http.StatusOK {
				t.Fatalf("delete: %d %s", rw.Code, rw.Body.String())
			}
		},
		"login ran out": func(t *testing.T, _ *Server, _ http.Handler, bob, _ string) {
			sessionsMu.Lock()
			activeSessions[bob].Expiry = time.Now().Add(-time.Minute)
			sessionsMu.Unlock()
		},
	}
	for name, do := range cases {
		for _, inQuery := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				srv, h := walkCServer(t, "viewer")
				ts := httptest.NewServer(h)
				defer ts.Close()
				defer ts.CloseClientConnections()
				bob := walkCLogin(t, srv, "bob")
				root := walkCLogin(t, srv, "root-admin")
				lines, ended := openAuditStream(t, srv, ts, bob, inQuery)

				do(t, srv, h, bob, root)
				srv.auditLog.Add(audit.Entry{Event: "row_after_the_caller_was_cut_off", User: "root-admin", Resource: "cluster:prod"})
				noRowAfter(t, lines, ended, "row_after_the_caller_was_cut_off")
			})
		}
	}
}

// The same for a stream opened with an API key: revoked, or its maker disabled.
func TestMustFix_AuditStreamEndsWhenTheKeyIsNoLongerGood(t *testing.T) {
	old := auditStreamRecheckEvery
	auditStreamRecheckEvery = time.Hour
	t.Cleanup(func() { auditStreamRecheckEvery = old })

	for _, name := range []string{"revoked", "maker disabled", "made again under the same name"} {
		t.Run(name, func(t *testing.T) {
			srv, h := walkCServer(t, "operator")
			ts := httptest.NewServer(h)
			defer ts.Close()
			defer ts.CloseClientConnections()
			bob := walkCLogin(t, srv, "bob")
			root := walkCLogin(t, srv, "root-admin")
			key := walkCCreateKey(t, h, bob, "bob-tail", "viewer")
			lines, ended := openAuditStream(t, srv, ts, key, false)

			switch name {
			case "revoked":
				if err := srv.authStore.RevokeToken("bob-tail"); err != nil {
					t.Fatal(err)
				}
			case "maker disabled":
				if rw := walkCCall(t, h, root, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": false}); rw.Code != http.StatusOK {
					t.Fatalf("disable: %d", rw.Code)
				}
			default:
				if err := srv.authStore.RevokeToken("bob-tail"); err != nil {
					t.Fatal(err)
				}
				walkCCreateKey(t, h, root, "bob-tail", "viewer")
			}
			srv.auditLog.Add(audit.Entry{Event: "row_after_the_key_was_cut_off", User: "root-admin"})
			noRowAfter(t, lines, ended, "row_after_the_key_was_cut_off")
		})
	}
}

// A stream with nothing to send is closed too, by the timer.
func TestMustFix_QuietAuditStreamIsClosedByTheTimer(t *testing.T) {
	old := auditStreamRecheckEvery
	auditStreamRecheckEvery = 50 * time.Millisecond
	t.Cleanup(func() { auditStreamRecheckEvery = old })

	srv, h := walkCServer(t, "viewer")
	ts := httptest.NewServer(h)
	defer ts.Close()
	defer ts.CloseClientConnections()
	bob := walkCLogin(t, srv, "bob")
	_, ended := openAuditStream(t, srv, ts, bob, false)

	// Still allowed: the stream stays open across several timer ticks.
	select {
	case <-ended:
		t.Fatal("the stream of a caller who is still logged in was closed")
	case <-time.After(300 * time.Millisecond):
	}
	// The login runs out. Nothing is written to the audit log for that, so
	// no row comes by to trigger the other check: only the timer can close
	// this stream.
	sessionsMu.Lock()
	activeSessions[bob].Expiry = time.Now().Add(-time.Minute)
	sessionsMu.Unlock()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("a quiet stream was not closed after its login ran out")
	}
}

// Review must-fix 5: a key or a user could be named like Sharko's own actor.
func TestMustFix_NamesKeptForSharkoAreRefused(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	bob := walkCLogin(t, srv, "bob")
	root := walkCLogin(t, srv, "root-admin")
	for _, n := range []string{"sharko", "system", "anonymous", "sharko-bot", "unknown", "ai-agent", "webhook", "Sharko", "SYSTEM"} {
		want := `The name "` + n + `" is kept for Sharko's own work. Pick another name.`

		rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/tokens", map[string]any{"name": n, "role": "operator"})
		if rw.Code != http.StatusBadRequest {
			t.Errorf("key named %q: %d, want 400", n, rw.Code)
		}
		if got := walkCError(rw); got != want {
			t.Errorf("key named %q: error = %q, want %q", n, got, want)
		}
		if _, ok := srv.authStore.GetToken(n); ok {
			t.Errorf("key named %q was stored anyway", n)
		}

		rw = walkCCall(t, h, root, http.MethodPost, "/api/v1/users", map[string]any{"username": n, "role": "viewer"})
		if rw.Code != http.StatusBadRequest {
			t.Errorf("user named %q: %d, want 400", n, rw.Code)
		}
		if got := walkCError(rw); got != want {
			t.Errorf("user named %q: error = %q, want %q", n, got, want)
		}
		if srv.authStore.GetUser(n) != nil {
			t.Errorf("user named %q was stored anyway", n)
		}
	}
	// A name that only holds one of the words is fine.
	if rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "sharko-ci", "role": "operator"}); rw.Code != http.StatusCreated {
		t.Errorf("key named sharko-ci: %d %s, want 201", rw.Code, rw.Body.String())
	}
}

// Review must-fix 4: the two routes where a viewer types a repository address
// used the plain fetcher, which follows a redirect to any address.
func TestMustFix_TypedRepoRoutesUseTheGuardedFetcher(t *testing.T) {
	// The fetcher those routes use never connects to an inside address, even
	// when asked directly.
	var calls atomic.Int32
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("entries:\n  inner-name: []\n"))
	}))
	defer inside.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	charts, err := typedRepoFetcher.ListCharts(ctx, inside.URL)
	if err == nil || len(charts) != 0 {
		t.Errorf("the fetcher for typed addresses read an inside address: charts=%v err=%v", charts, err)
	}
	if calls.Load() != 0 {
		t.Error("the inside address was connected to")
	}

	// And the two routes use that fetcher, not the plain one.
	for file, call := range map[string]string{
		"catalog_validate.go":    "typedRepoFetcher.ListVersions(",
		"catalog_repo_charts.go": "typedRepoFetcher.ListCharts(",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), call) {
			t.Errorf("%s no longer calls %s", file, call)
		}
		if strings.Contains(string(src), "catalogVersionsFetcher.") || strings.Contains(string(src), "helm.NewFetcher()") {
			t.Errorf("%s uses a fetcher that does not check redirects", file)
		}
	}
}
