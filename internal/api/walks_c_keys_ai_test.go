package api

// walks_c_keys_ai_test.go — pre-release fix round 2026-11-26, story C.
//
// API keys that could pass for a user, keys of a disabled or deleted user,
// and the two AI test calls that had no role check.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
)

func walkCCreateKey(t *testing.T, h http.Handler, token, name, role string) string {
	t.Helper()
	rw := walkCCall(t, h, token, http.MethodPost, "/api/v1/tokens", map[string]string{"name": name, "role": role})
	if rw.Code != http.StatusCreated {
		t.Fatalf("create key %q: %d %s", name, rw.Code, rw.Body.String())
	}
	var got CreateTokenResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Token
}

func walkCError(rw *httptest.ResponseRecorder) string {
	var body map[string]any
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	msg, _ := body["error"].(string)
	return msg
}

// Walk 6: an operator made a key named "admin" and /users/me and the audit
// log showed the admin.
func TestWalkC_AKeyCannotTakeTheNameOfAUser(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	bob := walkCLogin(t, srv, "bob")

	rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/tokens", map[string]string{"name": "root-admin", "role": "operator"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("a key named like a user must be refused with 400, got %d %s", rw.Code, rw.Body.String())
	}
	const want = `a user is already named "root-admin". Pick another name for the API key.`
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if _, ok := srv.authStore.GetToken("root-admin"); ok {
		t.Error("the key was stored anyway")
	}
}

func TestWalkC_AUserCannotTakeTheNameOfAKey(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	walkCCreateKey(t, h, admin, "ci-bot", "viewer")

	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/users", map[string]string{"username": "ci-bot", "role": "admin"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("a user named like a key must be refused with 400, got %d %s", rw.Code, rw.Body.String())
	}
	const want = `an API key is already named "ci-bot". Pick another user name, or revoke that key first.`
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// A key stored before the rule (or a user added by hand later) that shares a
// user's name is refused when it is used.
func TestWalkC_AnOldKeyWithAUsersNameIsRefused(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	plain, err := srv.authStore.CreateToken("later-user", "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rw := walkCCall(t, h, plain, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusOK {
		t.Fatalf("the key must work while no user has its name: %d", rw.Code)
	}
	if err := srv.authStore.AddUser("later-user", walkCPassword, "admin"); err != nil {
		t.Fatal(err)
	}
	rw := walkCCall(t, h, plain, http.MethodGet, "/api/v1/users/me", nil)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rw.Code)
	}
	const want = `API token "later-user" cannot be used: a user has the same name. Revoke this token and create one with another name`
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestWalkC_UsersMeAndAuditSayItWasAKey(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	bob := walkCLogin(t, srv, "bob")
	key := walkCCreateKey(t, h, bob, "bobs-key", "operator")

	type me struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		APIKey   bool   `json:"api_key"`
		AuthType string `json:"auth_type"`
	}
	var viaKey, viaLogin me
	rw := walkCCall(t, h, key, http.MethodGet, "/api/v1/users/me", nil)
	if err := json.Unmarshal(rw.Body.Bytes(), &viaKey); err != nil || rw.Code != http.StatusOK {
		t.Fatalf("users/me with key: %d %s", rw.Code, rw.Body.String())
	}
	if !viaKey.APIKey || viaKey.AuthType != "api_key" || viaKey.Username != "bobs-key" {
		t.Errorf("with a key: %+v, want api_key true, auth_type api_key, username bobs-key", viaKey)
	}
	rw = walkCCall(t, h, bob, http.MethodGet, "/api/v1/users/me", nil)
	_ = json.Unmarshal(rw.Body.Bytes(), &viaLogin)
	if viaLogin.APIKey || viaLogin.AuthType != "session" || viaLogin.Username != "bob" {
		t.Errorf("with a login: %+v, want api_key false, auth_type session, username bob", viaLogin)
	}

	// The audit log: one changing call with the key, one with the login.
	walkCCall(t, h, key, http.MethodPost, "/api/v1/tokens", map[string]string{"name": "made-by-key", "role": "viewer"})
	walkCCall(t, h, bob, http.MethodPost, "/api/v1/tokens", map[string]string{"name": "made-by-login", "role": "viewer"})
	var keyRow, loginRow *audit.Entry
	for _, e := range srv.auditLog.List(50) {
		e := e
		switch e.Resource {
		case "token:made-by-key":
			keyRow = &e
		case "token:made-by-login":
			loginRow = &e
		}
	}
	if keyRow == nil || loginRow == nil {
		t.Fatal("audit rows not found")
	}
	if keyRow.AuthType != audit.AuthAPIKey || keyRow.User != "bobs-key" {
		t.Errorf("row of the key call: user %q auth_type %q, want bobs-key / api_key", keyRow.User, keyRow.AuthType)
	}
	if loginRow.AuthType != "" || loginRow.User != "bob" {
		t.Errorf("row of the login call: user %q auth_type %q, want bob / empty", loginRow.User, loginRow.AuthType)
	}
	// A key made by a key belongs to the person behind the first key.
	if got := srv.authStore.TokenCreator("made-by-key"); got != "bob" {
		t.Errorf("a key made by bob's key is recorded as made by %q, want bob", got)
	}
}

// A client cannot claim to be (or not to be) a key by sending the header.
func TestWalkC_TheKeyMarkCannotBeSentByTheClient(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	bob := walkCLogin(t, srv, "bob")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+bob)
	req.Header.Set(authKindHeader, authKindAPIKey)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if !strings.Contains(rw.Body.String(), `"auth_type":"session"`) {
		t.Errorf("a login that sent the key header was believed: %s", rw.Body.String())
	}
}

func TestWalkC_AKeyOfADisabledUserIsRefused(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	bob := walkCLogin(t, srv, "bob")
	key := walkCCreateKey(t, h, bob, "bobs-key", "operator")
	child := walkCCreateKey(t, h, key, "bobs-second-key", "viewer")

	if rw := walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": false}); rw.Code != http.StatusOK {
		t.Fatalf("disable: %d", rw.Code)
	}
	rw := walkCCall(t, h, key, http.MethodGet, "/api/v1/users/me", nil)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("key of a disabled user: got %d, want 401", rw.Code)
	}
	const want = `API token "bobs-key" cannot be used: the user who created it is disabled`
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	if rw := walkCCall(t, h, child, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusUnauthorized {
		t.Errorf("a key made with the disabled user's key must be refused too, got %d", rw.Code)
	}

	// Turned back on: the keys work again. Nothing was thrown away.
	if rw := walkCCall(t, h, admin, http.MethodPut, "/api/v1/users/bob", map[string]any{"enabled": true}); rw.Code != http.StatusOK {
		t.Fatalf("enable: %d", rw.Code)
	}
	if rw := walkCCall(t, h, key, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusOK {
		t.Errorf("after enable the key must work again, got %d", rw.Code)
	}
}

func TestWalkC_DeletingAUserRevokesTheKeysTheyMade(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	bob := walkCLogin(t, srv, "bob")
	key := walkCCreateKey(t, h, bob, "bobs-key", "operator")
	adminKey := walkCCreateKey(t, h, admin, "admins-key", "viewer")

	rw := walkCCall(t, h, admin, http.MethodDelete, "/api/v1/users/bob", nil)
	if rw.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rw.Code, rw.Body.String())
	}
	var got struct {
		Revoked []string `json:"revoked_api_keys"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if len(got.Revoked) != 1 || got.Revoked[0] != "bobs-key" {
		t.Errorf("revoked_api_keys = %v, want [bobs-key]", got.Revoked)
	}
	if rw := walkCCall(t, h, key, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusUnauthorized {
		t.Errorf("the deleted user's key still works: %d", rw.Code)
	}
	if rw := walkCCall(t, h, adminKey, http.MethodGet, "/api/v1/users/me", nil); rw.Code != http.StatusOK {
		t.Errorf("somebody else's key must be left alone, got %d", rw.Code)
	}
}

func TestWalkC_AKeyCannotChangeAPassword(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	key := walkCCreateKey(t, h, admin, "admins-key", "admin")
	rw := walkCCall(t, h, key, http.MethodPost, "/api/v1/auth/update-password", map[string]string{
		"current_password": walkCPassword, "new_password": "a-brand-new-long-password",
	})
	if rw.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rw.Code)
	}
	const want = "An API key has no password to change. Log in as the user to change that user's password."
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestWalkC_KeyNamesAreChecked(t *testing.T) {
	const want = "An API key name can use letters, numbers, dots, dashes and underscores, with no spaces, up to 63 characters."
	if auth.InvalidTokenNameSentence != want {
		t.Fatalf("InvalidTokenNameSentence = %q", auth.InvalidTokenNameSentence)
	}
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	for _, bad := range []string{"bad name!", strings.Repeat("k", 200), "=cmd", "a/b"} {
		rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/tokens", map[string]string{"name": bad, "role": "viewer"})
		if rw.Code != http.StatusBadRequest || walkCError(rw) != want {
			t.Errorf("key name %.20q: got %d %q, want 400 and the name sentence", bad, rw.Code, walkCError(rw))
		}
	}
}

// ---- AI test calls ----

// Walk 5 S5: a viewer called POST /ai/test-config with the address of other
// services and read the answers.
func TestWalkC_AITestCallsAreAdminOnly(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		srv, h := walkCServer(t, role)
		bob := walkCLogin(t, srv, "bob")
		for _, path := range []string{"/api/v1/ai/test-config", "/api/v1/ai/test"} {
			rw := walkCCall(t, h, bob, http.MethodPost, path, map[string]string{"provider": "ollama", "ollama_url": "http://127.0.0.1:1"})
			if rw.Code != http.StatusForbidden {
				t.Errorf("%s as %s: got %d, want 403 (%s)", path, role, rw.Code, rw.Body.String())
			}
		}
	}
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/test", nil)
	if rw.Code == http.StatusForbidden || rw.Code == http.StatusUnauthorized {
		t.Errorf("an admin must get past the role check, got %d", rw.Code)
	}
}

func walkCTestConfig(t *testing.T, h http.Handler, token string, body map[string]string) (string, string) {
	t.Helper()
	rw := walkCCall(t, h, token, http.MethodPost, "/api/v1/ai/test-config", body)
	if rw.Code != http.StatusOK {
		t.Fatalf("test-config: %d %s", rw.Code, rw.Body.String())
	}
	var got struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	return got.Status, got.Message
}

func TestWalkC_AITestRefusesAddressesItNeverCalls(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")

	cases := []struct {
		body map[string]string
		want string
	}{
		{map[string]string{"provider": "ollama", "ollama_url": "http://169.254.169.254"},
			"Sharko does not call this address. Addresses that start with 169.254 (and fe80:) belong to the machine's own network setup, and no AI provider lives there."},
		{map[string]string{"provider": "custom-openai", "base_url": "http://[fe80::1]:8080", "model": "m"},
			"Sharko does not call this address. Addresses that start with 169.254 (and fe80:) belong to the machine's own network setup, and no AI provider lives there."},
		{map[string]string{"provider": "custom-openai", "base_url": "https://someone:hunter2@gateway.example.com", "model": "m"},
			"Take the user name and password out of the address. If the provider needs a key, put it in the API key field."},
		{map[string]string{"provider": "ollama", "ollama_url": "file:///etc/passwd"},
			"The address must start with http:// or https://."},
		{map[string]string{"provider": "custom-openai", "model": "m"},
			"Enter the address of the provider first."},
		{map[string]string{"provider": "openai", "model": "gpt-4o"},
			"Enter an API key first."},
		{map[string]string{"provider": "openai", "model": "gpt-4o", "api_key": "sk-abc def"},
			"The API key has a space or a line break in it. Paste the key again, on one line."},
		{map[string]string{"provider": "openai", "model": "gpt-4o", "api_key": strings.Repeat("k", 20000)},
			"The API key is too long to be a real key. Paste the key again."},
	}
	for _, tc := range cases {
		status, msg := walkCTestConfig(t, h, admin, tc.body)
		if status != "error" || msg != tc.want {
			t.Errorf("%v:\n got  %q %q\n want error %q", tc.body, status, msg, tc.want)
		}
		if strings.Contains(msg, "hunter2") {
			t.Error("the password from the address is in the answer")
		}
	}
}

// What an address answers is never passed on. The walker read ArgoCD's and
// the Kubernetes API's answers this way.
func TestWalkC_AITestDoesNotPassOnWhatAnAddressAnswers(t *testing.T) {
	const secretPage = "INTERNAL-PAGE-do-not-show <html>Client sent an HTTP request to an HTTPS server</html>"
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, secretPage)
	}))
	defer inner.Close()

	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	status, msg := walkCTestConfig(t, h, admin, map[string]string{"provider": "ollama", "ollama_url": inner.URL, "model": "llama3.2"})
	const want = "ollama returned 404: this address does not answer like an Ollama server, or the model is not there."
	if status != "error" || msg != want {
		t.Errorf("got %q %q, want error %q", status, msg, want)
	}
	if strings.Contains(msg, "INTERNAL-PAGE") || strings.Contains(msg, "HTTPS server") {
		t.Errorf("the answer of the address was passed on: %q", msg)
	}
}

// A self-hosted provider on a private or local address still works: that is
// where an Ollama lives.
func TestWalkC_AITestStillWorksForASelfHostedProvider(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"response":"AI connection successful"}`)
	}))
	defer ollama.Close()

	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/test-config",
		map[string]string{"provider": "ollama", "ollama_url": ollama.URL, "model": "llama3.2"})
	if !strings.Contains(rw.Body.String(), `"status":"ok"`) {
		t.Fatalf("a local Ollama must pass the test: %s", rw.Body.String())
	}
	// And it can be saved.
	rw = walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config",
		map[string]string{"provider": "ollama", "ollama_url": ollama.URL, "model": "llama3.2"})
	if rw.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rw.Code, rw.Body.String())
	}
	t.Cleanup(func() { srv.aiClient.SetConfig(ai.Config{}) })
}

func TestWalkC_AISaveRefusesAnAddressItNeverCalls(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config",
		map[string]string{"provider": "ollama", "ollama_url": "http://169.254.169.254", "model": "llama3.2"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (%s)", rw.Code, rw.Body.String())
	}
	if srv.aiClient.IsEnabled() {
		t.Error("the refused address was saved anyway")
	}
}

// Walk 7 finding 1: one click on the "Annotate values on generate" switch
// sent {"provider":"openai","annotate_on_seed":false} and the saved key and
// model were erased.
func TestWalkC_TheAnnotateSwitchKeepsTheSavedKeyAndModel(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	t.Cleanup(func() { srv.aiClient.SetConfig(ai.Config{}) })

	const key = "sk-TESTONLY-not-a-real-key-0000"
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config",
		map[string]any{"provider": "openai", "api_key": key, "model": "gpt-4o"})
	if rw.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rw.Code, rw.Body.String())
	}

	for _, next := range []bool{false, true} {
		rw = walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config",
			map[string]any{"provider": "openai", "annotate_on_seed": next})
		if rw.Code != http.StatusOK {
			t.Fatalf("switch: %d %s", rw.Code, rw.Body.String())
		}
		cfg := srv.aiClient.GetConfig()
		if cfg.APIKey != key {
			t.Fatalf("after the switch the saved key is %q — it was erased", cfg.APIKey)
		}
		if cfg.CloudModel != "gpt-4o" {
			t.Fatalf("after the switch the model is %q, want gpt-4o", cfg.CloudModel)
		}
		if srv.aiClient.AnnotateOnSeedEnabled() != next {
			t.Fatalf("the switch itself did not change to %v", next)
		}
	}

	// The page reads the true state back: provider, model, and "a key is saved".
	rw = walkCCall(t, h, admin, http.MethodGet, "/api/v1/ai/config", nil)
	var got aiConfigResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.HasAPIKey || got.CurrentProvider != "openai" || !got.AnnotateOnSeed {
		t.Errorf("config read back: %+v", got)
	}
	if strings.Contains(rw.Body.String(), key) {
		t.Error("the key itself is in the answer")
	}
	for _, p := range got.AvailableProviders {
		wantConfigured := p.ID == "openai"
		if p.Configured != wantConfigured {
			t.Errorf("provider %s configured = %v, want %v", p.ID, p.Configured, wantConfigured)
		}
		if p.ID != "openai" && p.Model != "" {
			t.Errorf("provider %s shows the model %q of another provider", p.ID, p.Model)
		}
		if p.ID == "openai" && p.Model != "gpt-4o" {
			t.Errorf("openai model = %q", p.Model)
		}
	}

	// A different provider gets nothing carried over.
	rw = walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config", map[string]any{"provider": "claude"})
	if rw.Code != http.StatusOK {
		t.Fatalf("switch provider: %d", rw.Code)
	}
	if cfg := srv.aiClient.GetConfig(); cfg.APIKey != "" {
		t.Error("the OpenAI key was carried over to another provider")
	}
	// Turning AI off removes the key.
	walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config", map[string]any{"provider": "openai", "api_key": key, "model": "gpt-4o"})
	walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/config", map[string]any{"provider": "none"})
	if cfg := srv.aiClient.GetConfig(); cfg.APIKey != "" {
		t.Error("turning AI off must remove the saved key")
	}
}

func TestWalkC_AIConfigWithNothingSetUpMarksNothingConfigured(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	srv.aiClient.SetConfig(ai.Config{})
	bob := walkCLogin(t, srv, "bob")
	rw := walkCCall(t, h, bob, http.MethodGet, "/api/v1/ai/config", nil)
	var got aiConfigResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, p := range got.AvailableProviders {
		ids[p.ID] = true
		if p.Configured || p.Model != "" {
			t.Errorf("nothing is set up, but %s says configured=%v model=%q", p.ID, p.Configured, p.Model)
		}
	}
	if !ids["custom-openai"] {
		t.Error("the list has no custom-openai, though the form offers it")
	}
	if got.HasAPIKey {
		t.Error("has_api_key is true with nothing set up")
	}
}

func TestWalkC_AITestsAreWrittenDownAsTests(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	walkCTestConfig(t, h, admin, map[string]string{"provider": "ollama", "ollama_url": "http://127.0.0.1:1", "model": "m"})
	walkCCall(t, h, admin, http.MethodPost, "/api/v1/ai/test", nil)

	seen := map[string]audit.Entry{}
	for _, e := range srv.auditLog.List(20) {
		seen[e.Event] = e
	}
	for _, old := range []string{"ai_created"} {
		if _, ok := seen[old]; ok {
			t.Errorf("an AI test is still written down as %q", old)
		}
	}
	tc, ok := seen["ai_config_tested"]
	if !ok {
		t.Fatalf("no ai_config_tested row; events: %v", seen)
	}
	if tc.Detail != "provider=ollama" || tc.Result != "failure" {
		t.Errorf("ai_config_tested row: detail %q result %q, want provider=ollama / failure", tc.Detail, tc.Result)
	}
	if _, ok := seen["ai_tested"]; !ok {
		t.Errorf("no ai_tested row; events: %v", seen)
	}
}

// ---- small findings ----

// Walk 5 L12: an operator could create and renew a key but never revoke it.
func TestWalkC_AnOperatorCanRevokeTheirOwnKeyOnly(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	admin := walkCLogin(t, srv, "root-admin")
	bob := walkCLogin(t, srv, "bob")
	walkCCreateKey(t, h, bob, "bobs-key", "viewer")
	walkCCreateKey(t, h, admin, "admins-key", "viewer")

	if rw := walkCCall(t, h, bob, http.MethodDelete, "/api/v1/tokens/admins-key", nil); rw.Code != http.StatusForbidden {
		t.Errorf("an operator revoking somebody else's key: got %d, want 403", rw.Code)
	}
	if rw := walkCCall(t, h, bob, http.MethodDelete, "/api/v1/tokens/no-such-key", nil); rw.Code != http.StatusForbidden {
		t.Errorf("an operator asking about a key that is not there: got %d, want the same 403", rw.Code)
	}
	if rw := walkCCall(t, h, bob, http.MethodDelete, "/api/v1/tokens/bobs-key", nil); rw.Code != http.StatusOK {
		t.Errorf("an operator revoking their own key: got %d %s, want 200", rw.Code, rw.Body.String())
	}
	if _, ok := srv.authStore.GetToken("bobs-key"); ok {
		t.Error("the key is still there")
	}
	if _, ok := srv.authStore.GetToken("admins-key"); !ok {
		t.Error("the admin's key is gone")
	}
}

func TestWalkC_AViewerCannotRevokeAnyKey(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	bob := walkCLogin(t, srv, "bob")
	walkCCreateKey(t, h, admin, "admins-key", "viewer")
	if rw := walkCCall(t, h, bob, http.MethodDelete, "/api/v1/tokens/admins-key", nil); rw.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rw.Code)
	}
}

// Walk 5 L10: a refused password reset said the caller had tried
// 'user.change-role'.
func TestWalkC_ARefusedPasswordResetNamesTheRightAction(t *testing.T) {
	srv, h := walkCServer(t, "operator")
	bob := walkCLogin(t, srv, "bob")
	rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/users/root-admin/reset-password", nil)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rw.Code)
	}
	const want = "action 'user.reset-password' requires role 'admin', you have 'operator'"
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// Walk 5 L14: the create-user answer showed role "".
func TestWalkC_CreateUserAnswerShowsTheRoleGiven(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/users", map[string]string{"username": "norole"})
	if rw.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rw.Code, rw.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(rw.Body.Bytes(), &got)
	if got["role"] != "viewer" {
		t.Errorf("role in the answer = %q, want viewer", got["role"])
	}
}

// Walk 5 L2: a 200-letter user name got the sentence about allowed letters.
func TestWalkC_ALongUserNameGetsItsOwnSentence(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	admin := walkCLogin(t, srv, "root-admin")
	rw := walkCCall(t, h, admin, http.MethodPost, "/api/v1/users", map[string]string{"username": strings.Repeat("a", 200), "role": "viewer"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rw.Code)
	}
	const want = "A user name can be at most 63 characters long."
	if got := walkCError(rw); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// Walk 5 L13: a password over 72 bytes gave HTTP 500 with the hashing
// library's words, and the same password again was accepted.
func TestWalkC_PasswordRules(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	bob := walkCLogin(t, srv, "bob")
	cases := []struct {
		newPass string
		want    string
	}{
		{strings.Repeat("p", 73), "The new password is too long. Use at most 72 characters."},
		{walkCPassword, "The new password is the same as the current one. Pick a different password."},
	}
	for _, tc := range cases {
		rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/auth/update-password", map[string]string{
			"current_password": walkCPassword, "new_password": tc.newPass,
		})
		if rw.Code != http.StatusBadRequest {
			t.Errorf("%.20q: got %d, want 400 (%s)", tc.newPass, rw.Code, rw.Body.String())
		}
		if got := walkCError(rw); got != tc.want {
			t.Errorf("error = %q, want %q", got, tc.want)
		}
		if strings.Contains(rw.Body.String(), "bcrypt") {
			t.Error("the hashing library's own words are in the answer")
		}
	}
	if !isValidSession(bob) {
		t.Error("a refused change must not end the login")
	}
}

// Walk 5 L11: the users list came back in a different order on each load.
func TestWalkC_ListsComeBackInNameOrder(t *testing.T) {
	srv, _ := walkCServer(t, "viewer")
	for _, n := range []string{"zed", "amy", "kim", "dan", "eve", "lou"} {
		if err := srv.authStore.AddUser(n, walkCPassword, "viewer"); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.authStore.CreateToken("key-"+n, "viewer", 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		users := srv.authStore.ListUsers()
		for j := 1; j < len(users); j++ {
			if users[j-1].Username > users[j].Username {
				t.Fatalf("users not in name order: %s before %s", users[j-1].Username, users[j].Username)
			}
		}
		tokens := srv.authStore.ListTokens()
		for j := 1; j < len(tokens); j++ {
			if tokens[j-1].Name > tokens[j].Name {
				t.Fatalf("keys not in name order: %s before %s", tokens[j-1].Name, tokens[j].Name)
			}
		}
	}
}

// Walk 7 finding 7: every assistant message was written down as "Agent
// created".
func TestWalkC_AChatMessageIsNotWrittenDownAsAgentCreated(t *testing.T) {
	srv, h := walkCServer(t, "viewer")
	bob := walkCLogin(t, srv, "bob")
	walkCCall(t, h, bob, http.MethodPost, "/api/v1/agent/chat", map[string]string{"message": "hello"})
	for _, e := range srv.auditLog.List(10) {
		if e.Event == "agent_created" {
			t.Fatal("a chat message is still written down as agent_created")
		}
		if e.Event == "ai_chat_message" {
			if e.Detail != "" {
				t.Errorf("the row carries detail %q; the message must not be kept", e.Detail)
			}
			return
		}
	}
	t.Fatal("no ai_chat_message row")
}
