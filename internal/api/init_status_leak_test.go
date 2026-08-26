package api

// init_status_leak_test.go — the proof for B4.
//
// # What is being proved
//
// GET /api/v1/init/status and POST /api/v1/init used to hand out other
// people's error text: the Git library's, the ArgoCD server's, the Go HTTP
// transport's. On the Git side that is not merely untidy, it is a secret
// leaving the server. A Git repository URL is routinely written with the
// token inside it —
//
//	https://x-access-token:<token>@github.example/org/repo.git
//
// — and net/url's own parse error quotes in full the string it failed on. So
// one unreadable repository URL in the saved connection turned a read-only
// probe's 502 body into a copy of the repository's access token.
//
// The second carrier had nothing to do with an error at all: when the
// bootstrap Application was degraded, the probe pasted ArgoCD's
// spec.source.repoURL straight into the detail string. That is an ordinary
// 200 response. Nothing had to fail for the token to come out.
//
// # How it is proved
//
// A sentinel that looks like a real token is planted inside a tokenised
// repository URL — the shape of the actual threat — and pushed down each real
// path through the real handler. Then the response body and every captured log
// line are swept for the sentinel in every form a leak takes: raw, four base64
// spellings, three hashes, several substrings, and its length in a dozen
// labelled shapes.
//
// The sweep is proved to WORK before it is trusted: TestInitLeakSweep_FindsAPlantedSentinel
// plants each form and requires the finder to name it. A sweep that cannot find
// a planted secret proves nothing at all, and it is the easiest thing in this
// file to get quietly wrong.
//
// # Why the sentences are typed out as literals here
//
// Every assertion compares against a string written by hand in this file, not
// against the constant the handler uses. Comparing a handler's output with the
// constant the handler just assigned cannot fail — it is the same value on both
// sides — so it pins nothing. Typing the sentence twice is the point: a change
// to the shipped wording has to be made here too, deliberately.

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
)

// initLeakSentinel stands in for the access token inside a repository URL. It
// appears nowhere else in this repository, so finding it anywhere is proof of
// a leak rather than a coincidence.
const initLeakSentinel = "Q7XW-init-status-token-sentinel-4m9d2p-never-leaves-the-server-b3e8"

// initLeakRepoURL is the threat in its real shape: a repository URL with the
// token embedded in the userinfo section.
const initLeakRepoURL = "https://x-access-token:" + initLeakSentinel + "@github.example/sharko-org/addons.git"

// initLeakRepoURLSafe is what SafeRepoURL leaves of it — host and path, which
// is all anybody needed in order to recognise which repository is failing.
const initLeakRepoURLSafe = "https://github.example/sharko-org/addons.git"

// initLeakUnparseableRepoURL is the same URL with a DEL byte on the end, which
// is what makes net/url refuse it. That refusal is the production path where
// the token reached the 502 body: the error value embeds the whole string.
const initLeakUnparseableRepoURL = initLeakRepoURL + "\x7f"

// --- the sweep -------------------------------------------------------------

// initLeakForms is every shape the sentinel could come out wearing. Each one
// is a way a value has genuinely escaped from software before: re-encoded,
// hashed "for safety", truncated to a "harmless" fragment, or reported as a
// length.
func initLeakForms() map[string]string {
	return leakFormsFor(initLeakSentinel, initLeakRepoURL)
}

// leakFormsFor is initLeakForms generalised over WHICH secret is being swept
// for (B12/B13). The list of shapes is the same one, in one place — B13 needed
// the identical sweep pointed at a different sentinel and a different URL
// spelling (the token in the USERNAME position rather than the password one),
// and a second copy of this list is a second thing to forget to update.
//
// tokenisedURL may be empty when the caller only cares about the bare token.
func leakFormsFor(sentinel, tokenisedURL string) map[string]string {
	n := len(sentinel)
	sha256Sum := sha256.Sum256([]byte(sentinel))
	sha1Sum := sha1.Sum([]byte(sentinel))
	md5Sum := md5.Sum([]byte(sentinel))

	forms := map[string]string{
		"the token itself":     sentinel,
		"base64 (std)":         base64.StdEncoding.EncodeToString([]byte(sentinel)),
		"base64 (raw std)":     base64.RawStdEncoding.EncodeToString([]byte(sentinel)),
		"base64 (url)":         base64.URLEncoding.EncodeToString([]byte(sentinel)),
		"base64 (raw url)":     base64.RawURLEncoding.EncodeToString([]byte(sentinel)),
		"SHA-256 hex":          hex.EncodeToString(sha256Sum[:]),
		"SHA-256 base64":       base64.StdEncoding.EncodeToString(sha256Sum[:]),
		"SHA-1 hex":            hex.EncodeToString(sha1Sum[:]),
		"MD5 hex":              hex.EncodeToString(md5Sum[:]),
		"first 12 characters":  sentinel[:12],
		"first 24 characters":  sentinel[:24],
		"last 12 characters":   sentinel[n-12:],
		"middle 16 characters": sentinel[n/2-8 : n/2+8],
	}
	if tokenisedURL != "" {
		forms["the whole tokenised URL"] = tokenisedURL
	}
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf(`"size": %d`, n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("size=%d", n),
	} {
		forms["its byte length, written "+shape] = shape
	}
	for _, ch := range []string{"*", "•", "x", "●", "#"} {
		for _, l := range []int{n - 1, n, n + 1} {
			forms[fmt.Sprintf("a mask of %d %q", l, ch)] = strings.Repeat(ch, l)
		}
	}
	return forms
}

// findInitLeak returns the names of every form of the sentinel present in
// text. It is split out from the assertion so the positive control below can
// prove the finder actually finds things.
func findInitLeak(text string) []string {
	return findLeakIn(text, initLeakForms())
}

// findLeakIn is findInitLeak with the form list passed in, so B13's sweep uses
// the same matcher over its own sentinel instead of a second copy of it.
func findLeakIn(text string, forms map[string]string) []string {
	var found []string
	for name, form := range forms {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// assertNoInitLeak fails, naming each form, when text carries the sentinel.
func assertNoInitLeak(t *testing.T, where, text string) {
	t.Helper()
	for _, name := range findInitLeak(text) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestInitLeakSweep_FindsAPlantedSentinel is the positive control, and it runs
// before anything trusts the sweep.
//
// Every other test in this file asserts an ABSENCE, and an absence is what a
// broken sweep reports too. So each form is planted in turn and the finder is
// required to name it. If this test ever goes green while finding nothing, the
// sweep is decoration and the whole file is worthless.
func TestInitLeakSweep_FindsAPlantedSentinel(t *testing.T) {
	forms := initLeakForms()
	if len(forms) < 20 {
		t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
	}
	for name, form := range forms {
		planted := "some ordinary looking log line " + form + " and some more text"
		found := findInitLeak(planted)
		if len(found) == 0 {
			t.Errorf("the sweep did NOT find a planted %s (%q).\n\nA sweep that cannot find a secret somebody put there proves nothing about the ones it says are absent.", name, form)
		}
	}

	// And it says nothing about text that is genuinely clean, so a green run
	// elsewhere is a real result rather than a sweep that fires on everything.
	if found := findInitLeak("Sharko has no usable Git connection. Open Settings."); len(found) != 0 {
		t.Errorf("the sweep fired on clean text, naming %v — every other assertion in this file would be noise", found)
	}
}

// --- 1. the Git side: the 502 body -----------------------------------------

// serverWithUnreadableRepoURL builds a REAL server whose active connection
// holds a repository URL net/url refuses. No fake, no seam: the connection is
// saved through the real store and the handler builds its Git provider through
// the real service, which fails the way it fails in production.
func serverWithUnreadableRepoURL(t *testing.T, repoURL, argocdToken string) *Server {
	t.Helper()
	store := config.NewFileStore(t.TempDir() + "/init-leak-config.yaml")
	if err := store.SaveConnection(models.Connection{
		Name: "leak-test",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitea,
			RepoURL:  repoURL,
			Owner:    "sharko-org",
			Repo:     "addons",
			Token:    "a-token-that-is-not-empty",
		},
		// The ArgoCD half is filled in so POST /init gets PAST its ArgoCD
		// check and reaches the Git one this test is about. Leaving the
		// server URL blank would send the service off to auto-discover it,
		// which dials the network and takes twelve seconds to give up — and
		// the request would 502 on the ArgoCD branch, so the Git line under
		// test would never run at all. An empty argocdToken is how the
		// ArgoCD-side test asks for that branch on purpose, without the dial.
		Argocd: models.ArgocdConfig{
			ServerURL: "https://argocd.example",
			Token:     argocdToken,
		},
	}); err != nil {
		t.Fatalf("saving the test connection: %v", err)
	}
	if err := store.SetActiveConnection("leak-test"); err != nil {
		t.Fatalf("activating the test connection: %v", err)
	}

	srv := newTestServer()
	srv.connSvc = service.NewConnectionService(store)
	return srv
}

// TestInitStatusLeak_GitConnectionFailure_NeverShowsTheRepositoryToken drives
// the real GET /api/v1/init/status against a connection whose repository URL
// cannot be read, and proves the token inside that URL reaches neither the
// response body nor any log line.
func TestInitStatusLeak_GitConnectionFailure_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "an-argocd-token")

	// Sanity first: the fixture must really fail, and the underlying error
	// must really carry the token. Without this half every assertion below
	// would pass while proving nothing.
	_, buildErr := srv.connSvc.GetActiveGitProvider()
	if buildErr == nil {
		t.Fatal("the fixture must FAIL to build a Git provider — there is nothing to prove otherwise")
	}
	if !strings.Contains(buildErr.Error(), initLeakSentinel) {
		t.Fatalf(`the underlying error does NOT carry the token, so this test proves nothing.

got: %v`, buildErr)
	}

	router := NewRouter(srv, nil)
	var body string
	logs := captureSlog(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/init/status", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d (body %s)", w.Code, w.Body.String())
		}
		body = w.Body.String()
	})

	assertNoInitLeak(t, "the GET /init/status 502 body", body)
	assertNoInitLeak(t, "the log output for GET /init/status", logs)

	// The old prefix is gone as well as the token. Leaving the prefix and
	// dropping only the error would be a change nobody could see.
	if strings.Contains(body, "err.Error") || strings.Contains(body, "gitea provider:") || strings.Contains(body, "net/url") {
		t.Errorf("the 502 body still carries a fragment of the underlying failure: %s", body)
	}

	// And it still says something an operator can act on. A fix that returned
	// an empty message would pass every sweep above and help nobody.
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding the 502 body: %v (body %s)", err, body)
	}
	got, _ := decoded["error"].(string)
	const wantGitSentence = "Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token."
	if got != wantGitSentence {
		t.Errorf("the 502 message is\n  %q\nwant exactly\n  %q", got, wantGitSentence)
	}
}

// TestInitLeak_PostInitGitConnectionFailure_NeverShowsTheRepositoryToken is the
// same proof for POST /api/v1/init, which reads the same connection through the
// same service and had the same line.
func TestInitLeak_PostInitGitConnectionFailure_NeverShowsTheRepositoryToken(t *testing.T) {
	srv := serverWithUnreadableRepoURL(t, initLeakUnparseableRepoURL, "an-argocd-token")
	router := NewRouter(srv, nil)

	var body string
	logs := captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/init", nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d (body %s)", w.Code, w.Body.String())
		}
		body = w.Body.String()
	})

	assertNoInitLeak(t, "the POST /init 502 body", body)
	assertNoInitLeak(t, "the log output for POST /init", logs)

	// It must be the GIT refusal that came back. If the ArgoCD check refused
	// first, this test would be green without the line it is named after ever
	// having run — which is exactly what happened on the first attempt.
	const wantGitSentence = "Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token."
	if !strings.Contains(body, wantGitSentence) {
		t.Fatalf("POST /init did not refuse on the Git connection — this test proves nothing about the Git line.\n\nbody: %s", body)
	}
}

// TestInitLeak_PostInitArgocdConnectionFailure_SaysSharkosOwnSentence is the
// other half of POST /init: the ArgoCD check, which sat on the same shape and
// is now the same kind of fixed sentence.
func TestInitLeak_PostInitArgocdConnectionFailure_SaysSharkosOwnSentence(t *testing.T) {
	// A readable repo URL and NO ArgoCD token: the ArgoCD check refuses first.
	srv := serverWithUnreadableRepoURL(t, "https://gitea.example/sharko-org/addons.git", "")
	router := NewRouter(srv, nil)

	var body string
	logs := captureSlog(t, func() {
		req := withRole(httptest.NewRequest(http.MethodPost, "/api/v1/init", nil), "admin")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d (body %s)", w.Code, w.Body.String())
		}
		body = w.Body.String()
	})

	assertNoInitLeak(t, "the POST /init 502 body for a broken ArgoCD connection", body)
	assertNoInitLeak(t, "the log output for a broken ArgoCD connection", logs)

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding the 502 body: %v (body %s)", err, body)
	}
	got, _ := decoded["error"].(string)
	const wantArgocdSentence = "Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token."
	if got != wantArgocdSentence {
		t.Errorf("the 502 message is\n  %q\nwant exactly\n  %q", got, wantArgocdSentence)
	}
	// The words of the underlying failure are gone, prefix and all.
	if strings.Contains(body, "ARGOCD_TOKEN") || strings.Contains(body, "SHARKO_DEV_MODE") {
		t.Errorf("the 502 body still carries the underlying error's own words: %s", body)
	}
}

// --- 2. the ArgoCD side: the probe's three answers --------------------------

// TestInitStatusLeak_ArgocdListFailure_NeverShowsTheTransportsWords covers the
// catch-all branch — the one reached when Sharko does not know what went wrong,
// and therefore cannot know what the error text contains.
func TestInitStatusLeak_ArgocdListFailure_NeverShowsTheTransportsWords(t *testing.T) {
	// The error carries a tokenised repository URL, which is exactly what a
	// Git-over-HTTPS failure surfacing through an ArgoCD call looks like.
	ac := &initFakeArgocd{listErr: fmt.Errorf(
		"Get %q: x509: certificate signed by unknown authority", initLeakRepoURL)}

	var body InitStatusResponse
	logs := captureSlog(t, func() {
		body = initStatusBody(t, initializedRepoGit(), ac)
	})

	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for a failed ArgoCD read", string(raw))
	assertNoInitLeak(t, "the log output for a failed ArgoCD read", logs)

	if body.State != RepoStateUnknown {
		t.Errorf("state = %q, want %q — a failed read must stay the honest 'could not check'", body.State, RepoStateUnknown)
	}
	const wantUnknown = "Sharko could not reach ArgoCD to check the bootstrap application, so it does not know whether the bootstrap is healthy. Check that the ArgoCD server address in Settings is right and that Sharko can reach it."
	if body.Detail != wantUnknown {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, wantUnknown)
	}
}

// TestInitStatusLeak_ArgocdTokenInvalid_SaysSharkosOwnSentence covers the 401
// branch. The old code returned err.Error() here and a comment claimed the
// error "already carries the full actionable message" — which was only ever
// true of the sentinel itself, never of the value that actually arrives, since
// every client is free to wrap it with whatever its transport produced.
func TestInitStatusLeak_ArgocdTokenInvalid_SaysSharkosOwnSentence(t *testing.T) {
	ac := &initFakeArgocd{listErr: fmt.Errorf(
		"listing applications from %s: %w", initLeakRepoURL, argocd.ErrTokenInvalid)}

	var body InitStatusResponse
	logs := captureSlog(t, func() {
		body = initStatusBody(t, initializedRepoGit(), ac)
	})

	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for a rejected ArgoCD token", string(raw))
	assertNoInitLeak(t, "the log output for a rejected ArgoCD token", logs)

	if body.State != RepoStateAuthFailed {
		t.Errorf("state = %q, want %q", body.State, RepoStateAuthFailed)
	}
	const wantAuthFailed = "ArgoCD rejected Sharko's token (the token is not valid, or it has expired). Create a new ArgoCD token and save it in Settings."
	if body.Detail != wantAuthFailed {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, wantAuthFailed)
	}
}

// TestInitStatusLeak_ArgocdForbidden_KeepsThePermissionSentence pins the 403
// answer as a literal too, so the three answers are pinned as three literals in
// one file and cannot drift into each other unnoticed.
func TestInitStatusLeak_ArgocdForbidden_KeepsThePermissionSentence(t *testing.T) {
	ac := &initFakeArgocd{listErr: fmt.Errorf(
		"listing applications from %s: %w", initLeakRepoURL, argocd.ErrPermissionDenied)}

	body := initStatusBody(t, initializedRepoGit(), ac)
	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for an ArgoCD permission refusal", string(raw))

	if body.State != RepoStateForbidden {
		t.Errorf("state = %q, want %q", body.State, RepoStateForbidden)
	}
	const wantForbidden = "ArgoCD rejected Sharko's token (permission denied) — the token needs permission to read applications. Check your ArgoCD RBAC: the account needs role:admin (or at least applications:get)."
	if body.Detail != wantForbidden {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, wantForbidden)
	}
}

// TestInitStatusLeak_ThreeRefusalsStayThreeAnswers is the honesty half of B4.
//
// Making the messages safe must not make them the same. A 403 means widen the
// token's ArgoCD permissions; a 401 means the token is dead and needs
// replacing; "could not check" means go and look at whether ArgoCD is
// reachable at all. Collapsing any two of them into one vague sentence would
// trade a leak for a false report, and the operator would be sent to the wrong
// screen.
func TestInitStatusLeak_ThreeRefusalsStayThreeAnswers(t *testing.T) {
	forbidden := initStatusBody(t, initializedRepoGit(), &initFakeArgocd{
		listErr: fmt.Errorf("listing applications: %w", argocd.ErrPermissionDenied)})
	authFailed := initStatusBody(t, initializedRepoGit(), &initFakeArgocd{
		listErr: fmt.Errorf("listing applications: %w", argocd.ErrTokenInvalid)})
	cannotTell := initStatusBody(t, initializedRepoGit(), &initFakeArgocd{
		listErr: fmt.Errorf("dial tcp 10.0.0.1:443: connect: connection refused")})

	answers := map[string]InitStatusResponse{
		"a permission refusal (403)":    forbidden,
		"a rejected token (401)":        authFailed,
		"could not reach ArgoCD at all": cannotTell,
	}
	seenState := map[string]string{}
	seenDetail := map[string]string{}
	for name, got := range answers {
		if got.Detail == "" {
			t.Errorf("%s came back with no explanation at all — an empty answer passes every leak sweep and helps nobody", name)
		}
		if other, dup := seenState[got.State]; dup {
			t.Errorf("%s reports state %q, the same as %s — these need different fixes and must stay different answers", name, got.State, other)
		}
		seenState[got.State] = name
		if other, dup := seenDetail[got.Detail]; dup {
			t.Errorf("%s says the same sentence as %s:\n  %q\nThese need different fixes: widen the token's ArgoCD permissions, replace a dead token, or check whether ArgoCD is reachable.", name, other, got.Detail)
		}
		seenDetail[got.Detail] = name
	}
}

// --- 3. the leak that was not an error at all -------------------------------

// TestInitStatusLeak_DegradedBootstrapRepoURL_IsStrippedOfItsToken is the find
// this story did not set out to make.
//
// When the bootstrap Application is degraded, the probe names the repository so
// the alert says WHICH repo is failing. It did that by pasting ArgoCD's
// spec.source.repoURL in whole. Nothing has to fail for this path to run — it
// is a 200 response about an unhealthy app — and if that URL was registered
// with the token inside it, the token went out with it.
func TestInitStatusLeak_DegradedBootstrapRepoURL_IsStrippedOfItsToken(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{{
		Name:          orchestrator.BootstrapRootAppName,
		SyncStatus:    "OutOfSync",
		HealthStatus:  "Degraded",
		SourceRepoURL: initLeakRepoURL,
	}}}

	body := initStatusBody(t, initializedRepoGit(), ac)
	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for a degraded bootstrap application", string(raw))

	want := fmt.Sprintf("argocd app %q sync=OutOfSync health=Degraded repo=%s",
		orchestrator.BootstrapRootAppName, initLeakRepoURLSafe)
	if body.Detail != want {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, want)
	}
	// The repository is still named. Dropping it entirely would have been the
	// lazy fix and would have taken the alert's usefulness with it.
	if !strings.Contains(body.Detail, "github.example/sharko-org/addons.git") {
		t.Errorf("the detail no longer names the repository, so the alert cannot say which repo is failing: %q", body.Detail)
	}
}

// TestInitStatusLeak_UnreadableRepoURL_IsNotNamedAtAll: when SafeRepoURL cannot
// take a URL apart, the repository is not mentioned. Falling back to the
// original string would be the whole leak again, wearing a helper's name.
func TestInitStatusLeak_UnreadableRepoURL_IsNotNamedAtAll(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{{
		Name:          orchestrator.BootstrapRootAppName,
		SyncStatus:    "OutOfSync",
		HealthStatus:  "Degraded",
		SourceRepoURL: initLeakUnparseableRepoURL,
	}}}

	body := initStatusBody(t, initializedRepoGit(), ac)
	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for a degraded app with an unreadable repo URL", string(raw))

	want := fmt.Sprintf("argocd app %q sync=OutOfSync health=Degraded", orchestrator.BootstrapRootAppName)
	if body.Detail != want {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q — an unreadable URL must leave no trailing artifact", body.Detail, want)
	}
}

// TestInitStatusLeak_UnknownArgocdStatusIsNotEchoed: the sync and health values
// are ArgoCD's words arriving over the wire, and they are pasted into a
// sentence a person reads. Only the values ArgoCD is known to report come
// through; anything else is named as unrecognised rather than repeated.
func TestInitStatusLeak_UnknownArgocdStatusIsNotEchoed(t *testing.T) {
	ac := &initFakeArgocd{listApps: []models.ArgocdApplication{{
		Name:         orchestrator.BootstrapRootAppName,
		SyncStatus:   "OutOfSync",
		HealthStatus: "Degraded " + initLeakSentinel,
	}}}

	body := initStatusBody(t, initializedRepoGit(), ac)
	raw, _ := json.Marshal(body)
	assertNoInitLeak(t, "the init-status body for an unrecognised ArgoCD health value", string(raw))

	want := fmt.Sprintf("argocd app %q sync=OutOfSync health=unrecognised", orchestrator.BootstrapRootAppName)
	if body.Detail != want {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, want)
	}
}

// TestInitStatusLeak_NoArgocdConnection_SaysSoPlainly pins the fourth fixed
// sentence — the one for "there is nothing configured to ask", which must stay
// apart from "the thing that is configured did not answer".
func TestInitStatusLeak_NoArgocdConnection_SaysSoPlainly(t *testing.T) {
	body := initStatusBody(t, initializedRepoGit(), nil)
	if body.State != RepoStateUnknown {
		t.Errorf("state = %q, want %q", body.State, RepoStateUnknown)
	}
	const wantNoClient = "No ArgoCD connection is configured, so Sharko did not check the bootstrap application."
	if body.Detail != wantNoClient {
		t.Errorf("detail is\n  %q\nwant exactly\n  %q", body.Detail, wantNoClient)
	}
	const wantUnknown = "Sharko could not reach ArgoCD to check the bootstrap application, so it does not know whether the bootstrap is healthy. Check that the ArgoCD server address in Settings is right and that Sharko can reach it."
	if body.Detail == wantUnknown {
		t.Error(`"no ArgoCD is configured" and "ArgoCD did not answer" must stay two sentences — they send an operator to two different screens`)
	}
}

// --- 4. SafeRepoURL itself --------------------------------------------------

// TestSafeRepoURL covers the helper directly, including the shape url.URL's own
// Redacted() gets wrong: a URL whose USERNAME is the token, with no password at
// all. Redacted() hands that username straight back, which is why this strips
// the userinfo section whole instead.
func TestSafeRepoURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"user and token", "https://x-access-token:" + initLeakSentinel + "@github.example/org/repo.git", "https://github.example/org/repo.git"},
		{"token as the username, no password", "https://" + initLeakSentinel + "@github.example/org/repo.git", "https://github.example/org/repo.git"},
		{"a plain URL is left alone", "https://github.example/org/repo.git", "https://github.example/org/repo.git"},
		{"a port survives", "https://gitea.example:3000/org/repo.git", "https://gitea.example:3000/org/repo.git"},
		{"ssh with a user", "ssh://git@github.example/org/repo.git", "ssh://github.example/org/repo.git"},
		{"a token in the query goes too", "https://github.example/org/repo.git?access_token=" + initLeakSentinel, "https://github.example/org/repo.git"},
		{"a token in the fragment goes too", "https://github.example/org/repo.git#" + initLeakSentinel, "https://github.example/org/repo.git"},
		{"empty in, empty out", "", ""},
		{"unparseable means say nothing", initLeakUnparseableRepoURL, ""},
		{"scp-style means say nothing", "git@github.example:org/repo.git", ""},
		// Inverted by B12. This used to want "". The rule it pinned — no
		// scheme and no host means say nothing — was also blanking the
		// repository column for an operator whose catalog entry is written
		// without a scheme, and a blank reads like "there is no repository
		// here" rather than "Sharko declined to say".
		//
		// A credential gets into a URL through the userinfo section, which
		// ends at "@", or through a query or fragment, which start at "?" and
		// "#". A string with none of those three characters has nowhere to
		// hide one, so there is nothing for the old rule to protect. The two
		// lines below are the same case: one looks more like a hostname to a
		// human, but nothing in either can carry a token.
		{"scheme-less address now identifies itself", "charts.example/org/repo", "charts.example/org/repo"},
		{"and so does a string that is not a URL at all — it has nothing to hide", "not-a-url-at-all", "not-a-url-at-all"},
		// Changed by BF12. These used to want "". A scheme-less address is
		// now read the same way as one with a scheme, so the credential is
		// removed and the host and path are shown — the same answer these
		// two shapes get when they are written with an "https://" in front.
		// The sentinel check below is what proves nothing leaked.
		{"scheme-less with a token in the userinfo is stripped", initLeakSentinel + "@charts.example/org/repo", "charts.example/org/repo"},
		{"scheme-less with a token in the query is stripped", "charts.example/org/repo?access_token=" + initLeakSentinel, "charts.example/org/repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := credsafe.SafeRepoURL(tc.in)
			if got != tc.want {
				t.Errorf("SafeRepoURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, initLeakSentinel) {
				t.Errorf("SafeRepoURL(%q) still carries the token: %q", tc.in, got)
			}
		})
	}
}
