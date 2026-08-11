package audit

// sanitize_test.go — the audit log stores no credentials-backend error text, and
// keeps nothing on a stored entry that says it once did.
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

// TestAdd_StoredCredentialEntryHasAllFourSafeProperties is the headline test for
// this package, and it asserts all four properties of a stored credential-failure
// entry in one place so none of them can be quietly dropped.
//
//  1. Error is EXACTLY the fixed safe sentence.
//  2. Detail is EMPTY. Not "the sentence again" — empty. One answer, one field.
//  3. The credential-failure flag is cleared, so a stored entry says nothing
//     about how it was classified.
//  4. Printed with %+v the entry carries no part of the underlying error.
//
// The %+v check matters because a field hidden by json:"-" would not show up in a
// JSON-only assertion. There is no error-typed field on Entry any more, so it
// should hold trivially — which is the point: if somebody adds one back, this
// fails.
func TestAdd_StoredCredentialEntryHasAllFourSafeProperties(t *testing.T) {
	credErr := credsafe.Mark(errors.New("AccessDenied while resolving " + auditSentinel))

	log := NewLog(10)
	log.Add(Entry{
		Event:  "cluster_secret_create",
		Action: "get_credentials",
		// The unsafe shape on purpose: a caller that puts text in both public
		// fields. Add must fix both.
		Error:             "AccessDenied while resolving " + auditSentinel,
		Detail:            "fetch failed: AccessDenied while resolving " + auditSentinel,
		CredentialFailure: credsafe.Is(credErr),
	})

	stored := log.List(0)[0]

	// 1. Error is the fixed safe sentence, exactly.
	if stored.Error != credsafe.Message {
		t.Errorf("stored Error = %q, want exactly the fixed safe sentence %q", stored.Error, credsafe.Message)
	}
	// 2. Detail is empty.
	if stored.Detail != "" {
		t.Errorf(`stored Detail = %q, want EMPTY.

A credential failure has one answer and it lives in Error. Repeating the same fixed sentence in Detail makes the row look like it carries two separate pieces of information when it carries one.`, stored.Detail)
	}
	// 3. The flag is cleared.
	if stored.CredentialFailure {
		t.Error("the stored entry still has CredentialFailure set — Add must clear it before storing, so a stored entry says nothing about how it was classified")
	}
	// 4. Nothing of the underlying error survives, even under %+v.
	if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
		t.Errorf("the stored entry printed with %%+v carries the sentinel:\n%+v", stored)
	}
}

// TestAdd_StoredEntryHoldsNoLiveError.
//
// There is no error-typed field on Entry any more, and this is the test that
// keeps it that way. It walks the stored struct by reflection, so a renamed or
// newly-added field holding a live error is caught — not only the one field a
// hand-written assertion would know about.
//
// It runs for a credentials entry, a plain one and an empty one, because a live
// error on a stored entry is a hazard regardless of what it says: the next person
// to print the entry with %+v or reflect over it gets whatever it holds, and
// json:"-" hides it from ONE reader, not from all of them.
func TestAdd_StoredEntryHoldsNoLiveError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry Entry
	}{
		{"a credentials-backend failure", Entry{
			Event: "e", Error: "cred " + auditSentinel, CredentialFailure: true,
		}},
		{"a plain git failure", Entry{
			Event: "e", Error: "git: reference not found",
		}},
		{"nothing special at all", Entry{Event: "e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(tc.entry)
			stored := log.List(0)[0]

			if stored.CredentialFailure {
				t.Error("CredentialFailure is still set on the stored entry — Add must clear it")
			}
			if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
				t.Errorf("the stored entry printed with %%+v carries the sentinel:\n%+v", stored)
			}
			v := reflect.ValueOf(stored)
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Kind() == reflect.Interface && !f.IsNil() {
					if e, ok := f.Interface().(error); ok {
						t.Errorf(`field %q holds a live error (%v).

audit.Entry must not carry an error. The classification runs at the credential boundary, where the typed error still exists, and only the ANSWER (a bool) travels here. An error on a stored entry is a leak waiting for the next %%+v.`, v.Type().Field(i).Name, e)
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
		Event:             "cluster_secret_reconcile",
		Action:            "git_read",
		Error:             gitErr.Error(),
		Detail:            "the managed-clusters file could not be read",
		CredentialFailure: credsafe.Is(gitErr),
	})

	stored := log.List(0)[0]
	if stored.Error != gitErr.Error() {
		t.Errorf("stored Error = %q, want the git error's own text %q — unrelated errors must not be redacted", stored.Error, gitErr.Error())
	}
	if stored.Detail != "the managed-clusters file could not be read" {
		t.Errorf("stored Detail = %q — an unrelated entry's detail must survive; only a CREDENTIAL failure's Detail is emptied", stored.Detail)
	}
}

// TestAdd_CredentialErrorWrappedByAnotherErrorIsStillCaught is the case that
// makes type-based classification necessary.
//
// A git or Kubernetes error that wraps a credentials error with %w is still
// recognised by credsafe.Is at the call site, so the entry still gets the safe
// sentence and an emptied Detail. A filter that matched on the error's WORDS
// would miss this the moment the wrapper reworded things.
func TestAdd_CredentialErrorWrappedByAnotherErrorIsStillCaught(t *testing.T) {
	inner := credsafe.Mark(errors.New("AccessDenied: " + auditSentinel))
	wrapped := fmt.Errorf("reading configuration/managed-clusters.yaml from git: %w",
		fmt.Errorf("resolving the cluster Secret: %w", inner))

	if !credsafe.Is(wrapped) {
		t.Fatal("the marker was lost through the %w chain, so the call site could not have classified this — check that every hop uses %w")
	}

	log := NewLog(10)
	// A caller that puts the wrapper's own text in both fields. The wrapper's
	// outer words are its own, but the entry is still a credentials failure, so
	// both fields get the credentials treatment.
	log.Add(Entry{
		Event:             "e",
		Error:             "reading configuration/managed-clusters.yaml from git: AccessDenied: " + auditSentinel,
		Detail:            "reading configuration/managed-clusters.yaml from git: AccessDenied: " + auditSentinel,
		CredentialFailure: credsafe.Is(wrapped),
	})

	stored := log.List(0)[0]
	if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
		t.Errorf("a git error WRAPPING a credentials error leaked the inner text:\n%+v", stored)
	}
	if stored.Error != credsafe.Message {
		t.Errorf("stored Error = %q, want the safe sentence — the marker must be found through the %%w chain", stored.Error)
	}
	if stored.Detail != "" {
		t.Errorf("stored Detail = %q, want empty", stored.Detail)
	}
}

// TestAdd_SanitizesTheSTREAMEDCopyToo.
//
// This is the surface a read-side fix would have missed. Add fans the same value
// out to every SSE subscriber; the stream handler json.Marshals it raw. Because
// Add sanitizes BEFORE both the append and the fan-out, one fix covers List and
// the stream, and it cannot be bypassed by the stream.
func TestAdd_SanitizesTheSTREAMEDCopyToo(t *testing.T) {
	log := NewLog(10)
	ch, unsub := log.Subscribe()
	defer unsub()

	log.Add(Entry{
		Event:             "e",
		Error:             "AccessDenied while resolving " + auditSentinel,
		Detail:            "AccessDenied while resolving " + auditSentinel,
		CredentialFailure: true,
	})

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
	if streamed.Error != credsafe.Message {
		t.Errorf("the streamed entry's Error = %q, want the fixed safe sentence", streamed.Error)
	}
	if streamed.Detail != "" {
		t.Errorf("the streamed entry's Detail = %q, want empty", streamed.Detail)
	}
	if streamed.CredentialFailure {
		t.Error("the streamed entry still has CredentialFailure set — Add must clear it before the fan-out, not only before the append")
	}
}

// TestEntry_CredentialFailureNeverSerializes: the flag is a hop from the call
// site to Add and nothing else. It must not appear in any API response.
func TestEntry_CredentialFailureNeverSerializes(t *testing.T) {
	blob, err := json.Marshal(Entry{Event: "e", CredentialFailure: true})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	for _, spelling := range []string{"CredentialFailure", "credential_failure", "credentialFailure"} {
		if strings.Contains(string(blob), spelling) {
			t.Errorf("the credential-failure flag serializes as %q — it is an internal hop, not an API field: %s", spelling, blob)
		}
	}
}

// TestEntry_HasNoErrorTypedFieldAtAll is the structural guard, and it is
// deliberately stronger than the reflection walks above.
//
// Those walks look at a stored VALUE, so they only catch a field somebody
// actually filled. This looks at the TYPE, so a raw error field added back to
// audit.Entry fails immediately — before any call site starts filling it, and
// whether or not it is hidden behind json:"-".
//
// That matters because json:"-" is exactly how the first version of this fix
// carried a live error, and json:"-" hides a field from ONE reader while leaving
// it there for the next person who prints the entry with %+v or reflects over it.
// The classification belongs at the credential boundary, where the typed error
// still exists; what travels here is a bool.
func TestEntry_HasNoErrorTypedFieldAtAll(t *testing.T) {
	errType := reflect.TypeOf((*error)(nil)).Elem()
	entryType := reflect.TypeOf(Entry{})

	for i := 0; i < entryType.NumField(); i++ {
		f := entryType.Field(i)
		if f.Type == errType || f.Type.Implements(errType) ||
			(f.Type.Kind() == reflect.Ptr && f.Type.Implements(errType)) {
			t.Errorf(`audit.Entry has an error-typed field %q (%s).

An audit entry must not carry an error, hidden by json:"-" or otherwise. Run credsafe.Is at the credential boundary — where the typed error is still alive — and send the ANSWER here as a bool. An error on this struct is a leak waiting for the next %%+v.`, f.Name, f.Type)
		}
	}
}
