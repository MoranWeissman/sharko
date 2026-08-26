package remediation

// argocd_write_audit_leak_test.go — BF6's proof on the surface this package
// owns: the audit record, and the background log line beside it.
//
// # What is being proved
//
// Auto-remediation makes the same two ArgoCD writes the restart-sync button
// does, and when one fails it writes an audit entry and a log line. The audit
// log is persisted and served over the API; the log is shipped to whatever
// collector the operator runs. Both are wider than a single 502.
//
// The error those two receive used to carry ArgoCD's response payload whole,
// and ArgoCD quotes the repository it was working on inside that payload —
// token and all.
//
// # How it is proved
//
// The error is not faked. A real HTTP server answers like a broken ArgoCD,
// with the token inside a repository address in all four carrier shapes, and
// a REAL argocd.Client is driven against it. The error that comes back is what
// the remediator is given, and then the captured audit entries and the
// captured log are swept.
//
// The sweep is proved to work first, on this file's own sentinel, before any
// absence here is believed.

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
)

// argoWriteLeakSentinel appears nowhere else in this repository.
const argoWriteLeakSentinel = "V2HD-remediation-token-sentinel-3w8j5r-never-leaves-the-server-e9c4"

// argoWriteLeakCarriers are the FOUR slots a credential is carried in inside
// a URL.
func argoWriteLeakCarriers() []struct{ name, url string } {
	return []struct{ name, url string }{
		{"password slot", "https://x-access-token:" + argoWriteLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"username slot", "https://" + argoWriteLeakSentinel + "@github.example/sharko-org/addons.git"},
		{"query parameter", "https://github.example/sharko-org/addons.git?access_token=" + argoWriteLeakSentinel},
		{"fragment", "https://github.example/sharko-org/addons.git#" + argoWriteLeakSentinel},
	}
}

// argoWriteLeakForms is every shape the sentinel could come out wearing.
func argoWriteLeakForms(tokenisedURL string) map[string]string {
	s := argoWriteLeakSentinel
	n := len(s)
	sha256Sum := sha256.Sum256([]byte(s))
	sha1Sum := sha1.Sum([]byte(s))
	md5Sum := md5.Sum([]byte(s))

	forms := map[string]string{
		"the token itself":     s,
		"base64 (std)":         base64.StdEncoding.EncodeToString([]byte(s)),
		"base64 (raw std)":     base64.RawStdEncoding.EncodeToString([]byte(s)),
		"base64 (url)":         base64.URLEncoding.EncodeToString([]byte(s)),
		"base64 (raw url)":     base64.RawURLEncoding.EncodeToString([]byte(s)),
		"SHA-256 hex":          hex.EncodeToString(sha256Sum[:]),
		"SHA-256 base64":       base64.StdEncoding.EncodeToString(sha256Sum[:]),
		"SHA-1 hex":            hex.EncodeToString(sha1Sum[:]),
		"MD5 hex":              hex.EncodeToString(md5Sum[:]),
		"first 12 characters":  s[:12],
		"first 24 characters":  s[:24],
		"last 12 characters":   s[n-12:],
		"middle 16 characters": s[n/2-8 : n/2+8],
	}
	if tokenisedURL != "" {
		forms["the whole tokenised URL"] = tokenisedURL
	}
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"size":%d`, n),
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

func findArgoWriteLeak(text, tokenisedURL string) []string {
	var found []string
	for name, form := range argoWriteLeakForms(tokenisedURL) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

func assertNoArgoWriteLeak(t *testing.T, where, text, tokenisedURL string) {
	t.Helper()
	for _, name := range findArgoWriteLeak(text, tokenisedURL) {
		t.Errorf("%s carries %s of the repository token.\n\nthe text was:\n%s", where, name, text)
	}
}

// TestArgoWriteLeakSweep_FindsAPlantedSentinel is the POSITIVE CONTROL.
// Nothing below may be believed before it passes.
func TestArgoWriteLeakSweep_FindsAPlantedSentinel(t *testing.T) {
	for _, carrier := range argoWriteLeakCarriers() {
		t.Run(carrier.name, func(t *testing.T) {
			forms := argoWriteLeakForms(carrier.url)
			if len(forms) < 20 {
				t.Fatalf("the sweep only looks for %d forms — it has been hollowed out", len(forms))
			}
			for name, form := range forms {
				planted := "an ordinary looking audit detail " + form + " and some more text"
				if found := findArgoWriteLeak(planted, carrier.url); len(found) == 0 {
					t.Errorf("the sweep did NOT find a planted %s (%q).\n\nA sweep that cannot find a secret somebody put there proves nothing about the ones it says are absent.", name, form)
				}
			}
			if found := findArgoWriteLeak("terminated stale sync for keda-moran-test", carrier.url); len(found) != 0 {
				t.Errorf("the sweep fired on clean text, naming %v — every other assertion here would be noise", found)
			}
		})
	}
}

// realArgocdWriteError drives a REAL argocd.Client against a server answering
// like a broken ArgoCD, and returns the error the client hands back. Nothing
// about it is faked, so what the remediator sees below is what it sees in
// production.
func realArgocdWriteError(t *testing.T, payload string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(ts.Close)

	err := argocd.NewClient(ts.URL, "an-argocd-token", false).
		TerminateOperation(context.Background(), "keda-moran-test")
	if err == nil {
		t.Fatal("the terminate succeeded against a server answering 500 — there is no error to carry anything")
	}
	return err
}

// captureArgoWriteSlog installs the SAME handler chain `sharko serve`
// installs, so the sweep runs against the pipeline that actually ships.
func captureArgoWriteSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(logging.NewHandler(&buf, slog.LevelDebug)))
	fn()
	return buf.String()
}

// TestRemediation_AuditAndLogNeverCarryArgocdsReply is the proof.
func TestRemediation_AuditAndLogNeverCarryArgocdsReply(t *testing.T) {
	for _, carrier := range argoWriteLeakCarriers() {
		t.Run(carrier.name, func(t *testing.T) {
			payload := fmt.Sprintf(
				`{"error":"rpc error: code = Unknown desc = failed to list refs for %s: authentication required","code":2,`+
					`"message":"repository %s is not accessible"}`,
				carrier.url, carrier.url)
			if !strings.Contains(payload, argoWriteLeakSentinel) {
				t.Fatalf("the simulated ArgoCD payload does not carry the token — this test would prove nothing")
			}

			terminateErr := realArgocdWriteError(t, payload)

			mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
			fa := &fakeArgo{
				apps:         []models.ArgocdApplication{liveKedaApp(mergeBase)},
				canSync:      argocd.CapabilityAllowed,
				errTerminate: terminateErr,
			}
			rem, ac := makeRemediator(fa, func() time.Time { return mergeBase })

			pr := makePR("keda", "moran-test", 77)
			pr.LastPolled = mergeBase

			logs := captureArgoWriteSlog(t, func() { rem.OnMerge(pr) })

			entries := ac.all()
			if len(entries) == 0 {
				t.Fatal("remediation recorded nothing at all, so the audit sweep swept nothing")
			}
			recorded, marshalErr := json.Marshal(entries)
			if marshalErr != nil {
				t.Fatalf("marshalling audit entries: %v", marshalErr)
			}

			assertNoArgoWriteLeak(t, "the remediation audit records", string(recorded), carrier.url)
			assertNoArgoWriteLeak(t, "the remediation log output", logs, carrier.url)

			// And the record is still useful. An absence on its own is also
			// what a short circuit produces.
			var sawFailure bool
			for _, e := range entries {
				if e.Event == "argocd_auto_remediation_failed" && e.Action == "terminate_operation" {
					sawFailure = true
					if !strings.Contains(e.Detail, "keda-moran-test") {
						t.Errorf("the audit record does not name the application: %q", e.Detail)
					}
					if e.Reason == "" {
						t.Errorf("the audit record carries no reason, so nothing classified the failure")
					}
				}
			}
			if !sawFailure {
				t.Fatalf("no terminate-failure record was written, so this test never reached the branch it is about. entries: %v", entries)
			}
			if !strings.Contains(logs, "remediation: terminate operation failed") {
				t.Errorf("the failure was not logged, so an operator has nothing to go on.\n\nlog output:\n%s", logs)
			}
		})
	}
}

// TestRemediation_BenignTerminateIsDecidedByType replaces the copy of
// isBenignTerminateError this package used to hold.
//
// The real client is driven against a real 400 whose wording has nothing in
// common with the phrase the old matcher searched for. If the decision still
// depended on ArgoCD's prose, remediation would stop instead of carrying on.
func TestRemediation_BenignTerminateIsDecidedByType(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantSynced bool
	}{
		{
			name:       "a 400 worded nothing like the old phrase",
			status:     http.StatusBadRequest,
			body:       `{"error":"there is currently nothing running that could be cancelled","code":3}`,
			wantSynced: true,
		},
		{
			name:       "a 500 that happens to contain the old phrase",
			status:     http.StatusInternalServerError,
			body:       `{"error":"internal error while checking whether no operation is in progress","code":2}`,
			wantSynced: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			terminateErr := argocd.NewClient(ts.URL, "tok", false).
				TerminateOperation(context.Background(), "keda-moran-test")
			if terminateErr == nil {
				t.Fatal("the terminate succeeded against a failing server")
			}

			mergeBase := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
			fa := &fakeArgo{
				apps:         []models.ArgocdApplication{liveKedaApp(mergeBase)},
				canSync:      argocd.CapabilityAllowed,
				errTerminate: terminateErr,
			}
			rem, _ := makeRemediator(fa, func() time.Time { return mergeBase })
			pr := makePR("keda", "moran-test", 78)
			pr.LastPolled = mergeBase
			rem.OnMerge(pr)

			gotSynced := len(fa.synced) == 1
			if gotSynced != tc.wantSynced {
				t.Errorf("re-sync happened = %v, want %v — the benign/not-benign decision went the wrong way", gotSynced, tc.wantSynced)
			}
		})
	}
}
