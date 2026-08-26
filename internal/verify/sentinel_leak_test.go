package verify

// sentinel_leak_test.go — the positive-control leak test.
//
// A sweep that finds nothing proves nothing unless the same sweep can be shown
// to find something. So every test here plants a value that must never be
// shown to anybody, drives the real Stage 1 code path, and then sweeps what
// comes out — AND asserts, in the same run, that the sweep does find the
// sentinel where it is legitimately allowed to be (Result.diagnostic, the
// server-log-only field). If the sweep ever stops working, the control fails
// and the whole file goes red rather than passing empty.
//
// The forms swept for are the ones the standing rule names: the raw value, its
// base64, its hashes, fragments of it, and anything whose LENGTH tracks it. A
// mask whose width follows the secret leaks the secret's length, so a
// fixed-width check is not enough.

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// sentinelSecret is the value that must never appear on an outbound surface.
// It is shaped like something a backend really would put in an error: a bearer
// token fragment.
const sentinelSecret = "k8s-aws-v1.SHARKOSENTINELdeadbeef0123456789ABCDEF"

// leakForms returns every form of secret the standing rule bans, each with a
// name so a failure says WHICH form was found rather than just "a leak".
func leakForms(secret string) map[string]string {
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
		// Fragments. The distinctive middle is what a partial mask would
		// keep ("k8s-aws-v1.SHAR…CDEF" style), so both ends are swept.
		"fragment (first 12)": secret[:12],
		"fragment (last 12)":  secret[len(secret)-12:],
		"fragment (middle)":   secret[16:32],
	}
}

// findLeaks returns the names of every banned form present in text.
func findLeaks(text, secret string) []string {
	var found []string
	for name, form := range leakForms(secret) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// variableWidthMask matches a run of mask characters long enough to be
// carrying a length. A fixed short mask ("***") is fine; a run that tracks the
// secret's length is not, because the width IS the length.
var variableWidthMask = regexp.MustCompile(`[*x•●]{8,}`)

// findLengthLeaks reports mask runs whose width equals the secret's length (or
// the length of one of its fragments) — the length-derived form the rule bans
// alongside the value itself.
func findLengthLeaks(text, secret string) []string {
	var found []string
	for _, run := range variableWidthMask.FindAllString(text, -1) {
		if len(run) == len(secret) {
			found = append(found, fmt.Sprintf("a %d-character mask run, exactly the secret's length", len(run)))
		}
	}
	// A bare decimal equal to the secret's length, next to mask-ish words.
	lengthWord := regexp.MustCompile(`\b` + strconv.Itoa(len(secret)) + `\b`)
	if lengthWord.MatchString(text) &&
		(strings.Contains(strings.ToLower(text), "length") ||
			strings.Contains(strings.ToLower(text), "char") ||
			strings.Contains(strings.ToLower(text), "bytes")) {
		found = append(found, fmt.Sprintf("the secret's length (%d) stated in words", len(secret)))
	}
	return found
}

// stage1WithSentinelError drives the REAL Stage1 against a client whose API
// calls fail with an error carrying the sentinel — the shape a provider or a
// credential-minting transport really would produce.
//
// marked says whether the error is tagged as a credentials-backend failure.
// Both are exercised: the marked case is the one credsafe is for, the unmarked
// case is what Stage 1 actually sees today, and NEITHER may leak.
func stage1WithSentinelError(t *testing.T, verb string, marked bool) Result {
	t.Helper()

	raw := errors.New("Get \"https://cluster.example.invalid/version\": failed to refresh token: " + sentinelSecret)
	injected := raw
	if marked {
		injected = credsafe.Mark(raw)
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)
	client.PrependReactor(verb, "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, injected
	})
	// Discovery().ServerVersion() on the fake clientset does not go through
	// the reactor chain, so the secret verbs are where the sentinel is
	// injected — those are steps 3 to 5, the ones that carry a real payload.

	result := Stage1(t.Context(), client, "sharko-test")
	if result.Success {
		t.Fatalf("expected Stage1 to fail so there is a message to sweep (verb %q)", verb)
	}
	return result
}

// TestSentinel_NeverReachesTheSerializedResult is the main event: plant the
// sentinel, run Stage 1, marshal the Result exactly as the API does, and sweep
// every banned form out of the bytes that would go on the wire.
func TestSentinel_NeverReachesTheSerializedResult(t *testing.T) {
	for _, marked := range []bool{false, true} {
		name := "unmarked provider error"
		if marked {
			name = "credentials-marked error"
		}
		t.Run(name, func(t *testing.T) {
			for _, verb := range []string{"create", "get", "delete"} {
				result := stage1WithSentinelError(t, verb, marked)

				// THE POSITIVE CONTROL, run before the assertions that
				// expect nothing. The unmarked error's own words are on
				// Diagnostic() — the server-log-only field — so the sweep
				// must find the sentinel there. If it does not, the sweep is
				// broken and every "no leak" result below is worthless.
				if !marked {
					if got := findLeaks(result.Diagnostic(), sentinelSecret); len(got) == 0 {
						t.Fatalf("POSITIVE CONTROL FAILED on verb %q: the sweep could not find the sentinel even in Result.diagnostic, where it certainly is. The sweep proves nothing in this state.", verb)
					}
				}

				// The wire bytes. This is the whole outbound API surface for
				// this Result: error_message, every step detail, everything.
				wire, err := json.Marshal(result)
				if err != nil {
					t.Fatalf("marshalling result: %v", err)
				}
				body := string(wire)

				if leaks := findLeaks(body, sentinelSecret); len(leaks) > 0 {
					t.Errorf("verb %q: the serialized Result leaks the sentinel as %s\n  body: %s",
						verb, strings.Join(leaks, ", "), body)
				}
				if leaks := findLengthLeaks(body, sentinelSecret); len(leaks) > 0 {
					t.Errorf("verb %q: the serialized Result leaks the sentinel's LENGTH — %s\n  body: %s",
						verb, strings.Join(leaks, ", "), body)
				}

				// The unexported diagnostic field must not be reachable
				// through encoding/json. This is the structural claim, so it
				// is asserted rather than assumed.
				if strings.Contains(body, "diagnostic") {
					t.Errorf("verb %q: %q appeared in the marshalled Result — the diagnostic field is no longer unexported",
						verb, "diagnostic")
				}

				// And the message must still be the catalog sentence.
				if result.ErrorMessage != safeSentences[result.ErrorCode] {
					t.Errorf("verb %q: ErrorMessage is not the catalog sentence for %s: %q",
						verb, result.ErrorCode, result.ErrorMessage)
				}
			}
		})
	}
}

// TestSentinel_MarkedErrorSaysOnlyTheFixedSentence checks the credsafe
// contract end to end: a marked error's words are gone even from the
// server-log-only field.
func TestSentinel_MarkedErrorSaysOnlyTheFixedSentence(t *testing.T) {
	result := stage1WithSentinelError(t, "create", true)

	if leaks := findLeaks(result.Diagnostic(), sentinelSecret); len(leaks) > 0 {
		t.Errorf("a credentials-marked error leaked the sentinel into the log field as %s: %q",
			strings.Join(leaks, ", "), result.Diagnostic())
	}
	if result.Diagnostic() != credsafe.Message {
		t.Errorf("a credentials-marked error should log the one fixed sentence, got %q", result.Diagnostic())
	}
}

// TestSentinel_StepDetailsCarryOnlyCatalogSentences sweeps the step list on
// its own. Steps are serialized twice per cluster-test response, and the six
// Detail values were raw error text before this story.
func TestSentinel_StepDetailsCarryOnlyCatalogSentences(t *testing.T) {
	result := stage1WithSentinelError(t, "create", false)

	sawFailedStep := false
	for _, step := range result.Steps {
		if step.Status != "fail" {
			continue
		}
		sawFailedStep = true
		if leaks := findLeaks(step.Detail, sentinelSecret); len(leaks) > 0 {
			t.Errorf("step %q leaks the sentinel as %s: %q",
				step.Name, strings.Join(leaks, ", "), step.Detail)
		}
		if step.Detail != safeSentences[result.ErrorCode] {
			t.Errorf("step %q detail is not the catalog sentence for %s: %q",
				step.Name, result.ErrorCode, step.Detail)
		}
	}
	if !sawFailedStep {
		t.Fatal("no failed step in the result — this test swept nothing")
	}
}

// ── the catalog is complete, checked as a LIST ──────────────────────────

// allDeclaredErrorCodes parses the ErrorCode const block in errors.go and
// returns every code declared there.
//
// It reads the SOURCE rather than taking a hand-written list, so a code added
// tomorrow is covered without anybody remembering to come here — and it
// returns the names, so a failure says WHICH code is missing a sentence
// instead of "expected 10, got 9".
func allDeclaredErrorCodes(t *testing.T) []ErrorCode {
	t.Helper()
	path := filepath.Join(repoRootForCredsafeGuard(t), "internal", "verify", "errors.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing errors.go: %v", err)
	}

	var codes []ErrorCode
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "ErrorCode" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			unquoted, unqErr := strconv.Unquote(lit.Value)
			if unqErr == nil {
				codes = append(codes, ErrorCode(unquoted))
			}
		}
		return true
	})

	if len(codes) == 0 {
		t.Fatal("parsed no ErrorCode constants out of internal/verify/errors.go — this check covers nothing in that state")
	}
	return codes
}

// TestSafeSentences_EveryDeclaredErrorCodeHasOne fails BY NAME on any declared
// code with no catalog sentence.
func TestSafeSentences_EveryDeclaredErrorCodeHasOne(t *testing.T) {
	for _, code := range allDeclaredErrorCodes(t) {
		sentence, ok := safeSentences[code]
		if !ok {
			t.Errorf("%s is declared in errors.go but has no sentence in safeSentences — it would render as the ERR_UNKNOWN fallback, silently", code)
			continue
		}
		if strings.TrimSpace(sentence) == "" {
			t.Errorf("%s has an empty sentence", code)
		}
	}

	// And nothing in the catalog that is not a declared code — a stale entry
	// is dead prose nobody will ever read.
	declared := map[ErrorCode]bool{}
	for _, code := range allDeclaredErrorCodes(t) {
		declared[code] = true
	}
	for code := range safeSentences {
		if !declared[code] {
			t.Errorf("safeSentences has an entry for %s, which is not a declared ErrorCode — dead prose", code)
		}
	}
}

// TestSafeSentences_CarryNoRawErrorVocabulary pins that the catalog is written
// in a person's words, not a machine's. A sentence that quoted client-go
// vocabulary would mean somebody had pasted an error message in here.
func TestSafeSentences_CarryNoRawErrorVocabulary(t *testing.T) {
	banned := []string{
		"dial tcp", "x509:", "connection refused", "no such host",
		"deadline exceeded", "%!", "0x", "err:", "error:",
	}
	for code, sentence := range safeSentences {
		lower := strings.ToLower(sentence)
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("the %s sentence contains machine vocabulary %q — catalog sentences are written for a person: %q",
					code, phrase, sentence)
			}
		}
	}
}
