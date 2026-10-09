package remediation

// sentinel_leak_test.go — the permanent regression test for the two paths that
// were GUARANTEED to record a raw error, on every failure, forever.
//
// # What was wrong here
//
// act() writes two audit entries on failure: one when TerminateOperation fails
// and one when SyncApplication fails. Both used to be built like this:
//
//	Error:             err.Error(),
//	CredentialFailure: credsafe.Is(err),
//
// The audit sink only sanitised when CredentialFailure was true. The error here
// is an ArgoCD error, and nothing in Sharko marks ArgoCD errors — so
// credsafe.Is was false on 100% of reaching paths, the flag was false, and
// err.Error() was stored verbatim every single time. Not "could leak under some
// condition": leaked always.
//
// Sixteen other sites made the same call to credsafe.Is and were safe only
// because somebody upstream had remembered to mark. These two had nobody.
//
// # What this test is
//
// It plants a token-shaped sentinel in the ArgoCD error, runs the REAL act()
// through the real OnMerge path, and sweeps every audit entry that comes out —
// raw value, base64, hashes, fragments, and any mask whose width tracks the
// secret's length. It proves the sweep can find the sentinel before it asserts
// the sentinel is absent.
//
// There is no marked variant here on purpose. The whole point is that this path
// never had one.

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// remediationSentinel is unique to this file. It is shaped like a bearer token,
// because that is what an ArgoCD API error really can carry.
const remediationSentinel = "Bearer.REMEDIATIONSENTINEL.7q2v9x.deadbeef0123456789"

// argocdErrorWithSentinel is the shape the real ArgoCD client returns: an
// unmarked error whose text includes whatever the server said back.
func argocdErrorWithSentinel() error {
	return fmt.Errorf("argocd: unexpected status 401 from POST /api/v1/applications/keda-moran-test/operation: %w",
		errors.New("invalid session token "+remediationSentinel))
}

func remediationLeakForms(secret string) map[string]string {
	sum256 := sha256.Sum256([]byte(secret))
	sum1 := sha1.Sum([]byte(secret))
	sum5 := md5.Sum([]byte(secret))
	return map[string]string{
		"raw value":            secret,
		"base64":               base64.StdEncoding.EncodeToString([]byte(secret)),
		"base64 (url safe)":    base64.URLEncoding.EncodeToString([]byte(secret)),
		"base64 (raw, no pad)": base64.RawStdEncoding.EncodeToString([]byte(secret)),
		"sha256":               hex.EncodeToString(sum256[:]),
		"sha1":                 hex.EncodeToString(sum1[:]),
		"md5":                  hex.EncodeToString(sum5[:]),
		"fragment (first 12)":  secret[:12],
		"fragment (last 12)":   secret[len(secret)-12:],
		"fragment (middle)":    secret[16:32],
	}
}

func findRemediationLeaks(text string) []string {
	var found []string
	for name, form := range remediationLeakForms(remediationSentinel) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

var remediationMaskRun = regexp.MustCompile(`[*x•●]{8,}`)

func findRemediationLengthLeaks(text string) []string {
	var found []string
	for _, run := range remediationMaskRun.FindAllString(text, -1) {
		if len(run) == len(remediationSentinel) {
			found = append(found, fmt.Sprintf("a %d-character mask run, exactly the secret's length", len(run)))
		}
	}
	lengthWord := regexp.MustCompile(`\b` + strconv.Itoa(len(remediationSentinel)) + `\b`)
	if lengthWord.MatchString(text) &&
		(strings.Contains(strings.ToLower(text), "length") ||
			strings.Contains(strings.ToLower(text), "char") ||
			strings.Contains(strings.ToLower(text), "bytes")) {
		found = append(found, fmt.Sprintf("the secret's length (%d) stated in words", len(remediationSentinel)))
	}
	return found
}

// TestRemediationSentinel_SweepFindsItWhereItIs is the positive control, run
// first so a broken sweep fails here rather than making the real test pass
// empty.
func TestRemediationSentinel_SweepFindsItWhereItIs(t *testing.T) {
	err := argocdErrorWithSentinel()
	if got := findRemediationLeaks(err.Error()); len(got) == 0 {
		t.Fatal(`POSITIVE CONTROL FAILED: the sweep cannot find the sentinel in the ArgoCD error's own text, where it certainly is.

Every "no leak" assertion in this file is worthless in this state.`)
	}
	for _, form := range []string{"base64", "sha256", "fragment (middle)"} {
		if len(findRemediationLeaks("x "+remediationLeakForms(remediationSentinel)[form]+" y")) == 0 {
			t.Errorf("POSITIVE CONTROL FAILED: the sweep does not detect the %q form", form)
		}
	}
	if len(findRemediationLengthLeaks("token: "+strings.Repeat("*", len(remediationSentinel)))) == 0 {
		t.Error("POSITIVE CONTROL FAILED: the sweep does not detect a mask whose width tracks the secret's length")
	}
	// And the premise: this error really is unmarked. If a future change marks
	// ArgoCD errors, this file must be revisited rather than silently passing
	// for a different reason.
	if credsafe.Is(err) {
		t.Fatal("the ArgoCD error is now credentials-marked — this file tests the UNMARKED path and no longer does so")
	}
}

// runRemediationFailure drives the real OnMerge → act path with an ArgoCD
// client that fails the given call, and returns every audit entry that reached
// the sink, stored the way the API really stores them.
func runRemediationFailure(t *testing.T, failTerminate, failSync bool) []audit.Entry {
	t.Helper()
	now := time.Now()
	fa := &fakeArgo{apps: []models.ArgocdApplication{liveKedaApp(now)}}
	if failTerminate {
		fa.errTerminate = argocdErrorWithSentinel()
	}
	if failSync {
		fa.errSync = argocdErrorWithSentinel()
	}
	r, ac := makeRemediator(fa, func() time.Time { return now })
	r.OnMerge(makePR("keda", "moran-test", 41))

	handed := ac.all()
	if len(handed) == 0 {
		t.Fatal("no audit entry was written, so this test swept nothing")
	}

	// Through the real sink, which is where the safety lives.
	log := audit.NewLog(50)
	for _, e := range handed {
		log.Add(e)
	}
	stored := log.List(0)
	if len(stored) == 0 {
		t.Fatal("nothing was stored, so this test swept nothing")
	}
	return stored
}

// TestRemediationSentinel_TerminateFailureRecordsNoErrorText is the first of
// the two paths the ruling named.
func TestRemediationSentinel_TerminateFailureRecordsNoErrorText(t *testing.T) {
	assertRemediationEntriesAreClean(t, runRemediationFailure(t, true, false), "terminate_operation")
}

// TestRemediationSentinel_SyncFailureRecordsNoErrorText is the second.
func TestRemediationSentinel_SyncFailureRecordsNoErrorText(t *testing.T) {
	assertRemediationEntriesAreClean(t, runRemediationFailure(t, false, true), "sync_application")
}

func assertRemediationEntriesAreClean(t *testing.T, stored []audit.Entry, wantAction string) {
	t.Helper()

	blob, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshalling the stored entries: %v", err)
	}
	body := string(blob)

	if leaks := findRemediationLeaks(body); len(leaks) > 0 {
		t.Errorf("the stored audit entries leak the sentinel as %s\n  body: %s",
			strings.Join(leaks, ", "), body)
	}
	if leaks := findRemediationLengthLeaks(body); len(leaks) > 0 {
		t.Errorf("the stored audit entries leak the sentinel's LENGTH — %s\n  body: %s",
			strings.Join(leaks, ", "), body)
	}
	// %+v reaches anything json:"-" would hide.
	printed := fmt.Sprintf("%+v", stored)
	if leaks := findRemediationLeaks(printed); len(leaks) > 0 {
		t.Errorf("the stored entries printed with %%+v leak the sentinel as %s:\n%s",
			strings.Join(leaks, ", "), printed)
	}

	// And the useful category survived: the failure entry still says what
	// operation failed, on what, and what kind of failure it was.
	var found bool
	for _, e := range stored {
		if e.Action != wantAction || e.Result != "failure" {
			continue
		}
		found = true
		if e.Event != "argocd_auto_remediation_failed" {
			t.Errorf("event = %q, want argocd_auto_remediation_failed", e.Event)
		}
		if e.Resource == "" {
			t.Error("the failure entry names no resource")
		}
		if !e.Reason.Valid() {
			t.Errorf("the failure entry has no usable reason (%q) — the category must survive", e.Reason)
		}
		if e.Error.IsZero() {
			t.Error("the failure entry carries no sentence at all — over-correction is its own failure")
		}
		if e.Detail == "" {
			t.Error("the failure entry lost its Sharko-authored detail, which names the app and the PR")
		}
	}
	if !found {
		t.Fatalf("no stored failure entry with action %q — this test asserted nothing", wantAction)
	}
}
