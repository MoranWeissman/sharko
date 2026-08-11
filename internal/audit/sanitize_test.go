package audit

// sanitize_test.go — the audit log stores no credentials-backend error text, and
// keeps no live error hanging off a stored entry.
//
// These are unit tests on Add itself, which is where the fix lives. The
// end-to-end proofs (through the real provider, the real handlers, GET /audit and
// GET /audit/stream) are in internal/api/cred_error_sentinel_test.go and
// internal/clusterreconciler/cred_error_sentinel_test.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// auditSentinel is unique to this file.
const auditSentinel = "MK2D-audit-sentinel-7t4r1w-never-stored-a9e3"

// TestAdd_CredentialErrorTextIsReplacedInBothPublicFields covers Error AND
// Detail. Detail is just as public as Error — handlers set it via Enrich, GET
// /audit returns it, and the SSE stream marshals it — and it was the field the
// first pass of this fix missed.
func TestAdd_CredentialErrorTextIsReplacedInBothPublicFields(t *testing.T) {
	credErr := credsafe.Mark(errors.New("AccessDenied while resolving " + auditSentinel))

	log := NewLog(10)
	log.Add(Entry{
		Event:  "cluster_secret_create",
		Action: "get_credentials",
		Error:  credErr.Error(),
		Detail: "fetch failed: " + credErr.Error(),
		Cause:  credErr,
	})

	stored := log.List(0)[0]
	if strings.Contains(stored.Error, auditSentinel) {
		t.Errorf("the stored Error field still carries the credentials error text: %q", stored.Error)
	}
	if strings.Contains(stored.Detail, auditSentinel) {
		t.Errorf("the stored Detail field still carries the credentials error text: %q", stored.Detail)
	}
	if stored.Error != credsafe.Message {
		t.Errorf("stored Error = %q, want %q", stored.Error, credsafe.Message)
	}
	if stored.Detail != credsafe.Message {
		t.Errorf("stored Detail = %q, want %q", stored.Detail, credsafe.Message)
	}
}

// TestAdd_ClearsTheTypedCauseAlways.
//
// The cause is cleared whether the entry was credentials-related or not. A live
// error on a stored entry is a hazard regardless of what it says: the next person
// to print the entry with %+v, or to reflect over it, gets whatever it holds.
// json:"-" hides it from ONE reader, not from all of them — which is why this
// asserts with %+v and reflection rather than JSON.
func TestAdd_ClearsTheTypedCauseAlways(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"a credentials-backend error", credsafe.Mark(errors.New("cred " + auditSentinel))},
		{"a plain git error", errors.New("git: reference not found")},
		{"no cause at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(Entry{Event: "e", Error: "some text", Cause: tc.cause})
			stored := log.List(0)[0]

			if stored.Cause != nil {
				t.Errorf("Cause = %v, want nil — Add must clear it before storing", stored.Cause)
			}
			if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
				t.Errorf("the stored entry printed with %%+v carries the sentinel:\n%+v", stored)
			}
			// Reflection, so a renamed or newly-added error-typed field is caught
			// too rather than only the field this test knows about.
			v := reflect.ValueOf(stored)
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Kind() == reflect.Interface && !f.IsNil() {
					if e, ok := f.Interface().(error); ok {
						t.Errorf("field %q still holds a live error (%v)", v.Type().Field(i).Name, e)
					}
				}
			}
		})
	}
}

// TestAdd_UnrelatedErrorsKeepTheirText is the guard against over-correction.
//
// Without it, a fix that blanket-redacted every Error and Detail would pass every
// other test here and quietly gut the audit trail. Git and Kubernetes errors are
// a different risk and they keep their words.
func TestAdd_UnrelatedErrorsKeepTheirText(t *testing.T) {
	gitErr := errors.New("git: reference not found: refs/heads/main")

	log := NewLog(10)
	log.Add(Entry{
		Event:  "cluster_secret_reconcile",
		Action: "git_read",
		Error:  gitErr.Error(),
		Detail: "the managed-clusters file could not be read",
		Cause:  gitErr,
	})

	stored := log.List(0)[0]
	if stored.Error != gitErr.Error() {
		t.Errorf("stored Error = %q, want the git error's own text %q — unrelated errors must not be redacted", stored.Error, gitErr.Error())
	}
	if stored.Detail != "the managed-clusters file could not be read" {
		t.Errorf("stored Detail = %q — an unrelated entry's detail must survive", stored.Detail)
	}
}

// TestAdd_CredentialErrorWrappedByAnotherErrorIsStillCaught is the case that
// makes type-based classification necessary.
//
// A git or Kubernetes error that wraps a credentials error with %w still gets the
// safe sentence, because the marker travels with the cause and errors.Is finds it
// through every hop. A filter that matched on the error's WORDS would miss this
// the moment the wrapper reworded things.
func TestAdd_CredentialErrorWrappedByAnotherErrorIsStillCaught(t *testing.T) {
	inner := credsafe.Mark(errors.New("AccessDenied: " + auditSentinel))
	wrapped := fmt.Errorf("reading configuration/managed-clusters.yaml from git: %w",
		fmt.Errorf("resolving the cluster Secret: %w", inner))

	log := NewLog(10)
	log.Add(Entry{Event: "e", Error: wrapped.Error(), Detail: wrapped.Error(), Cause: wrapped})

	stored := log.List(0)[0]
	if strings.Contains(stored.Error, auditSentinel) || strings.Contains(stored.Detail, auditSentinel) {
		t.Errorf("a git error WRAPPING a credentials error leaked the inner text.\n\nError: %q\nDetail: %q", stored.Error, stored.Detail)
	}
	if stored.Error != credsafe.Message {
		t.Errorf("stored Error = %q, want the safe sentence — the marker must be found through the %%w chain", stored.Error)
	}
}

// TestAdd_SanitizesTheSTREAMEDCopyToo.
//
// This is the surface a read-side fix would have missed. Add fans the same value
// out to every SSE subscriber; the stream handler json.Marshals it raw. Because
// Add sanitizes BEFORE both the append and the fan-out, one fix covers List and
// the stream, and it cannot be bypassed by the stream.
func TestAdd_SanitizesTheSTREAMEDCopyToo(t *testing.T) {
	credErr := credsafe.Mark(errors.New("AccessDenied while resolving " + auditSentinel))

	log := NewLog(10)
	ch, unsub := log.Subscribe()
	defer unsub()

	log.Add(Entry{Event: "e", Error: credErr.Error(), Detail: credErr.Error(), Cause: credErr})

	streamed := <-ch
	blob, err := json.Marshal(streamed)
	if err != nil {
		t.Fatalf("marshalling the streamed entry: %v", err)
	}
	if strings.Contains(string(blob), auditSentinel) {
		t.Errorf("the STREAMED entry carries the credentials error text: %s", blob)
	}
	if strings.Contains(fmt.Sprintf("%+v", streamed), auditSentinel) {
		t.Errorf("the STREAMED entry printed with %%+v carries the credentials error text:\n%+v", streamed)
	}
	if streamed.Cause != nil {
		t.Errorf("the streamed entry still carries a live cause (%v)", streamed.Cause)
	}
}
