package audit

// sentinel_leak_test.go — the positive-control leak sweep for the audit sink.
//
// A sweep that finds nothing proves nothing unless the same sweep can be shown
// to find something. So every test here plants a value that must never be shown
// to anybody, drives the REAL Add path, and sweeps what comes out — AND asserts,
// in the same run, that the sweep does find the sentinel where it certainly is.
// If the sweep ever stops working, the control fails and the file goes red
// rather than passing empty.
//
// The forms swept for are the ones the standing rule names: the raw value, its
// base64, its hashes, fragments of it, and anything whose LENGTH tracks it. A
// mask whose width follows the secret leaks the secret's length, so a
// fixed-width check is not enough.
//
// This file is the audit-sink twin of internal/verify/sentinel_leak_test.go and
// deliberately reuses its shape.

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

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// leakSentinel is the value that must never reach a stored or streamed audit
// entry. It is shaped like something a backend really would put in an error.
const leakSentinel = "k8s-aws-v1.AUDITSENTINELdeadbeef0123456789ABCDEF"

// leakForms returns every form of secret the standing rule bans, each named so
// a failure says WHICH form was found rather than just "a leak".
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
		"fragment (first 12)":  secret[:12],
		"fragment (last 12)":   secret[len(secret)-12:],
		"fragment (middle)":    secret[16:32],
	}
}

func findLeaks(text, secret string) []string {
	var found []string
	for name, form := range leakForms(secret) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// variableWidthMask matches a run of mask characters long enough to be carrying
// a length. A fixed short mask ("***") is fine; a run that tracks the secret's
// length is not, because the width IS the length.
var variableWidthMask = regexp.MustCompile(`[*x•●]{8,}`)

func findLengthLeaks(text, secret string) []string {
	var found []string
	for _, run := range variableWidthMask.FindAllString(text, -1) {
		if len(run) == len(secret) {
			found = append(found, fmt.Sprintf("a %d-character mask run, exactly the secret's length", len(run)))
		}
	}
	lengthWord := regexp.MustCompile(`\b` + strconv.Itoa(len(secret)) + `\b`)
	if lengthWord.MatchString(text) &&
		(strings.Contains(strings.ToLower(text), "length") ||
			strings.Contains(strings.ToLower(text), "char") ||
			strings.Contains(strings.ToLower(text), "bytes")) {
		found = append(found, fmt.Sprintf("the secret's length (%d) stated in words", len(secret)))
	}
	return found
}

// TestSentinelSweep_FindsTheSentinelWhereItIs is the positive control on its
// own, run first so a broken sweep fails here with a plain message rather than
// making every other test in this file pass empty.
func TestSentinelSweep_FindsTheSentinelWhereItIs(t *testing.T) {
	raw := errors.New("Post \"https://argocd/api/v1/session\": bearer " + leakSentinel)

	if got := findLeaks(raw.Error(), leakSentinel); len(got) == 0 {
		t.Fatal(`POSITIVE CONTROL FAILED: the sweep cannot find the sentinel in the raw error text, where it certainly is.

Every "no leak" assertion in this file is worthless in this state.`)
	}
	// And it finds the derived forms, not only the raw one.
	for _, form := range []string{"base64", "sha256", "fragment (middle)"} {
		blob := leakForms(leakSentinel)[form]
		if len(findLeaks("prefix "+blob+" suffix", leakSentinel)) == 0 {
			t.Errorf("POSITIVE CONTROL FAILED: the sweep does not detect the %q form", form)
		}
	}
	// And the length-derived form.
	mask := strings.Repeat("*", len(leakSentinel))
	if len(findLengthLeaks("token: "+mask, leakSentinel)) == 0 {
		t.Error("POSITIVE CONTROL FAILED: the sweep does not detect a mask whose width tracks the secret's length")
	}
}

// TestSentinel_NeverReachesAStoredOrStreamedEntry is the main event: plant the
// sentinel in an error, classify it the way a real call site does, drive the
// real Add, and sweep both the stored entry and the streamed copy.
//
// The marked and unmarked cases are both exercised, because the whole ruling is
// that they must come out the same.
func TestSentinel_NeverReachesAStoredOrStreamedEntry(t *testing.T) {
	raw := errors.New("Get \"https://cluster.example.invalid/version\": failed to refresh token: " + leakSentinel)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"an unmarked provider error — the remediation case", raw},
		{"a credentials-marked error", credsafe.Mark(raw)},
		{"a secret-value-marked error", credsafe.MarkSecretValue(raw)},
		{"a git error WRAPPING a credentials error", fmt.Errorf("reading managed-clusters.yaml: %w", credsafe.Mark(raw))},
		{"a Kubernetes-shaped wrapper around an unmarked error", fmt.Errorf("listing secrets in namespace %q: %w", "argocd", raw)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The control, per case: the sentinel really is reachable from
			// this error before Sharko does anything with it. A marked error
			// says only the safe sentence, so the control reads through
			// credsafe.Cause — the one sanctioned way to see the real words.
			source := tc.err.Error()
			if credsafe.Is(tc.err) {
				source = credsafe.Cause(tc.err).Error()
			}
			if len(findLeaks(source, leakSentinel)) == 0 {
				t.Fatalf("POSITIVE CONTROL FAILED for %q: the sentinel is not reachable in the source error, so this case sweeps nothing", tc.name)
			}

			log := NewLog(10)
			ch, unsub := log.Subscribe()
			defer unsub()

			log.Add(Entry{
				Level:    "error",
				Event:    "cluster_secret_reconcile",
				Action:   "get_credentials",
				Resource: "cluster:prod-eu",
				Source:   "reconciler",
				Result:   "failure",
				Reason:   Classify(tc.err),
			})

			stored := log.List(0)[0]
			streamed := <-ch

			for label, e := range map[string]Entry{"stored": stored, "streamed": streamed} {
				wire, err := json.Marshal(e)
				if err != nil {
					t.Fatalf("marshalling the %s entry: %v", label, err)
				}
				body := string(wire)

				if leaks := findLeaks(body, leakSentinel); len(leaks) > 0 {
					t.Errorf("the %s entry leaks the sentinel as %s\n  body: %s",
						label, strings.Join(leaks, ", "), body)
				}
				if leaks := findLengthLeaks(body, leakSentinel); len(leaks) > 0 {
					t.Errorf("the %s entry leaks the sentinel's LENGTH — %s\n  body: %s",
						label, strings.Join(leaks, ", "), body)
				}
				// %+v reaches fields json:"-" would hide.
				printed := fmt.Sprintf("%+v", e)
				if leaks := findLeaks(printed, leakSentinel); len(leaks) > 0 {
					t.Errorf("the %s entry printed with %%+v leaks the sentinel as %s:\n%s",
						label, strings.Join(leaks, ", "), printed)
				}
				// And the sentence is from the catalog, not from anywhere else.
				if e.Error.String() != safeSentences[e.Reason] {
					t.Errorf("the %s entry's Error is not the catalog sentence for %s: %q",
						label, e.Reason, e.Error.String())
				}
			}
		})
	}
}

// TestSentinel_SurvivesTheWholeRingAndTheFilteredReads sweeps the other read
// paths, so a leak cannot hide behind List(limit) or ListFiltered.
func TestSentinel_SurvivesTheWholeRingAndTheFilteredReads(t *testing.T) {
	raw := errors.New("AccessDenied: " + leakSentinel)
	log := NewLog(10)
	for i := 0; i < 5; i++ {
		log.Add(Entry{
			Event: "e", User: "sharko", Result: "failure",
			Resource: "cluster:prod-eu",
			Reason:   Classify(raw),
			Detail:   fmt.Sprintf("attempt %d", i),
		})
	}

	for name, entries := range map[string][]Entry{
		"List(0)":       log.List(0),
		"List(2)":       log.List(2),
		"ListFiltered":  log.ListFiltered(AuditFilter{Result: "failure"}),
		"ListFiltered2": log.ListFiltered(AuditFilter{Cluster: "prod-eu"}),
	} {
		if len(entries) == 0 {
			t.Fatalf("%s returned nothing — this read swept nothing", name)
		}
		wire, err := json.Marshal(entries)
		if err != nil {
			t.Fatalf("marshalling %s: %v", name, err)
		}
		if leaks := findLeaks(string(wire), leakSentinel); len(leaks) > 0 {
			t.Errorf("%s leaks the sentinel as %s: %s", name, strings.Join(leaks, ", "), wire)
		}
	}
}
