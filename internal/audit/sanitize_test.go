package audit

// sanitize_test.go — the audit log stores no error's own words, from any
// source, whatever the caller does.
//
// These are unit tests on Add itself, which is where the safety lives. The
// end-to-end proofs (through the real provider, the real handlers, GET /audit
// and GET /audit/stream) are in internal/api/cred_error_sentinel_test.go and
// internal/clusterreconciler/cred_error_sentinel_test.go. The positive-control
// sentinel sweep is in sentinel_leak_test.go; the source-reading guards are in
// guard_test.go.
//
// WHAT THESE TESTS USED TO ASSERT, AND WHY IT CHANGED. The old suite pinned
// "a credentials failure gets the fixed sentence, and everything else keeps its
// own text" — TestAdd_UnrelatedErrorsKeepTheirText existed specifically to stop
// an over-correction from redacting git and Kubernetes errors. That contract is
// what the product-owner ruling overturned: an unmarked provider error must get
// the same protection as a marked credential error, because "unmarked" only
// ever meant "nobody remembered", never "safe". So no error text is stored now,
// and what a reader gets instead is a category plus a whole sentence — which
// TestAdd_TheUsefulCategorySurvives is here to keep honest.

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

// ── the headline ────────────────────────────────────────────────────────────

// TestAdd_EveryStoredErrorIsACatalogSentence is the whole story in one test.
//
// It drives Add with entries a caller might really produce — including the
// hostile ones — and asserts that in every case the stored Error is one of the
// catalog sentences and nothing else. There is no case in this table that
// produces raw text, and there is no case a caller could add that would,
// because Entry.Error is a SafeText and no package outside this one can build
// one.
func TestAdd_EveryStoredErrorIsACatalogSentence(t *testing.T) {
	catalog := map[string]bool{"": true}
	for _, s := range safeSentences {
		catalog[s] = true
	}

	for _, tc := range []struct {
		name  string
		entry Entry
	}{
		{"a marked credentials failure", Entry{
			Event:  "cluster_secret_create",
			Reason: Classify(credsafe.Mark(errors.New("AccessDenied resolving " + auditSentinel))),
		}},
		{"an addon secret-value failure", Entry{
			Event:  "addon_secret_sync",
			Reason: Classify(credsafe.MarkSecretValue(errors.New("AccessDenied resolving " + auditSentinel))),
		}},
		{"an UNMARKED provider error — the case that used to leak", Entry{
			Event:  "argocd_auto_remediation_failed",
			Reason: Classify(errors.New("argocd returned 500: " + auditSentinel)),
		}},
		{"a writer that said nothing at all", Entry{Event: "e", Result: "failure"}},
		{"a writer that invented a reason", Entry{Event: "e", Reason: Reason("totally-made-up")}},
		{"a writer that put the sentinel IN the reason", Entry{Event: "e", Reason: Reason(auditSentinel)}},
		{"a success entry", Entry{Event: "e", Result: "success"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(tc.entry)
			stored := log.List(0)[0]

			if !catalog[stored.Error.String()] {
				t.Errorf(`stored Error is not a catalog sentence: %q

Entry.Error may only ever hold one of the sentences in safeSentences. Anything
else means somebody found a way to write text into it.`, stored.Error.String())
			}
			if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
				t.Errorf("the stored entry printed with %%+v carries the sentinel:\n%+v", stored)
			}
			blob, err := json.Marshal(stored)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if strings.Contains(string(blob), auditSentinel) {
				t.Errorf("the serialized entry carries the sentinel: %s", blob)
			}
		})
	}
}

// ── break test 1: remove a provider marker ──────────────────────────────────

// TestBreak_RemovingTheProviderMarkerKeepsTheAuditSafe.
//
// THIS IS A DESIGNED PASS. It is not a break that failed to fire — it is the
// proof that safety survives the mutation the old design could not survive.
//
// Under the old design this was the whole bug: the sanitizer only acted when
// the call site had set CredentialFailure, the call site computed that with
// credsafe.Is, and credsafe.Is is false the moment an upstream author forgets
// to call Mark. Take the marker away and the raw text was stored.
//
// Now the SAME error, marked and unmarked, produces a catalog sentence either
// way. What changes is only which sentence: the marked one is recognisably
// about credentials, the unmarked one falls to the upstream category. That is
// accuracy lost, which is the honest cost — and never safety lost.
func TestBreak_RemovingTheProviderMarkerKeepsTheAuditSafe(t *testing.T) {
	raw := errors.New("AccessDenied while resolving " + auditSentinel)

	withMarker := Classify(credsafe.Mark(raw))
	withoutMarker := Classify(raw) // the mutation: the provider forgot to Mark

	if withMarker == withoutMarker {
		t.Fatalf(`THE MUTATION DID NOT LAND: marked and unmarked classify the same (%s).

This test proves nothing unless removing the marker really changes something.`, withMarker)
	}
	if withMarker != ReasonCredentials {
		t.Errorf("a marked error should classify as %s, got %s", ReasonCredentials, withMarker)
	}
	if withoutMarker != ReasonUpstream {
		t.Errorf("an unmarked error should classify as %s, got %s", ReasonUpstream, withoutMarker)
	}

	for _, reason := range []Reason{withMarker, withoutMarker} {
		log := NewLog(10)
		log.Add(Entry{Event: "cluster_secret_create", Result: "failure", Reason: reason})
		stored := log.List(0)[0]

		if strings.Contains(fmt.Sprintf("%+v", stored), auditSentinel) {
			t.Errorf("reason %s: the stored entry carries the sentinel:\n%+v", reason, stored)
		}
		if stored.Error.String() != safeSentences[reason] {
			t.Errorf("reason %s: stored Error = %q, want the catalog sentence", reason, stored.Error.String())
		}
	}
}

// ── break test 2: the old flag, set wrongly or omitted ──────────────────────

// TestBreak_TheOldFlagCannotBeSetAtAll.
//
// THIS IS A DESIGNED PASS, and it is the strongest form the ruling's second
// break test can take: the flag a caller used to get wrong does not exist.
//
// The ruling's wording was "set the old flag incorrectly, or omit it". The flag
// was removed rather than kept as a hint, so "set it incorrectly" is now a
// compile error and cannot be written. What CAN still be got wrong is the
// Reason, so the equivalent mutations are exercised here instead: omit it, and
// set it to the wrong value. Both stay safe, because Reason is an enum and the
// sentence is chosen by the sink.
func TestBreak_TheOldFlagCannotBeSetAtAll(t *testing.T) {
	// The type-level half: no field on Entry is a bool any more, so there is
	// nothing shaped like the old switch for a future author to reach for.
	entryType := reflect.TypeOf(Entry{})
	for i := 0; i < entryType.NumField(); i++ {
		f := entryType.Field(i)
		if f.Type.Kind() == reflect.Bool {
			t.Errorf(`audit.Entry has a bool field %q.

Audit safety must not be switchable from a call site. CredentialFailure was
exactly such a switch: sixteen of seventeen sites computed it from a live
error and any error nobody had marked defeated it. A new bool on this struct
is the same mistake wearing a different name.`, f.Name)
		}
		if strings.Contains(strings.ToLower(f.Name), "credentialfailure") {
			t.Errorf("audit.Entry still has a field named %q — the flag was removed on purpose", f.Name)
		}
	}

	// The behavioural half: the two mutations that remain possible.
	for _, tc := range []struct {
		name  string
		entry Entry
	}{
		{"the reason omitted entirely", Entry{Event: "e", Result: "failure"}},
		{"a flatly wrong reason", Entry{Event: "e", Result: "failure", Reason: ReasonNotFound}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(tc.entry)
			stored := log.List(0)[0]

			blob, err := json.Marshal(stored)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			// A wrong reason picks a wrong sentence. It cannot pick a
			// non-sentence, which is the only property that matters here.
			if stored.Error.String() != safeSentences[stored.Reason] {
				t.Errorf("stored Error %q is not the catalog sentence for reason %q: %s",
					stored.Error.String(), stored.Reason, blob)
			}
		})
	}
}

// ── break test 3: an unmarked error carrying a sentinel secret ──────────────

// TestBreak_AnUnmarkedErrorCarryingASecretCannotReachTheEntry.
//
// THIS IS A DESIGNED PASS. The mutation the ruling asks for — pass an unmarked
// error containing a sentinel secret — cannot be expressed any more: there is
// no field on audit.Entry that takes an error, and no field that takes text.
//
// So the test does the nearest thing a caller CAN still do, three ways, and
// sweeps all three. The stronger structural claim (no error-typed field, no
// forgeable text field) is asserted in TestEntry_HasNoErrorTypedFieldAtAll and
// TestSafeText_CannotBeBuiltOutsideThisPackage.
func TestBreak_AnUnmarkedErrorCarryingASecretCannotReachTheEntry(t *testing.T) {
	unmarked := fmt.Errorf("listing argocd cluster secrets: %w",
		errors.New("Post \"https://argocd/api\": bearer "+auditSentinel))

	if credsafe.Is(unmarked) {
		t.Fatal("THE MUTATION DID NOT LAND: the error is marked, so this is not the unmarked case")
	}

	for _, tc := range []struct {
		name  string
		entry Entry
	}{
		{"classified honestly", Entry{Event: "e", Result: "failure", Reason: Classify(unmarked)}},
		{"classified as credentials, which it is not", Entry{Event: "e", Result: "failure", Reason: ReasonCredentials}},
		{"the writer put the error's words in Detail", Entry{
			Event: "e", Result: "failure", Reason: Classify(unmarked),
			Detail: "could not list: " + unmarked.Error(),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			ch, unsub := log.Subscribe()
			defer unsub()
			log.Add(tc.entry)

			stored := log.List(0)[0]
			streamed := <-ch

			for label, e := range map[string]Entry{"stored": stored, "streamed": streamed} {
				if strings.Contains(e.Error.String(), auditSentinel) {
					t.Errorf("%s Error carries the sentinel: %q", label, e.Error.String())
				}
				if e.Error.String() != safeSentences[e.Reason] {
					t.Errorf("%s Error is not the catalog sentence for %s: %q", label, e.Reason, e.Error.String())
				}
			}

			// The Detail case is the honest limit of the runtime guard and it
			// is stated rather than hidden: text a writer types into Detail
			// beside an error is caught at BUILD time by
			// TestNoAuditDetailIsBuiltFromAnError in guard_test.go, not here —
			// no error object travels with the entry, so the sink has nothing
			// to compare Detail against. What the sink DOES do unconditionally
			// is drop Detail for the two credential reasons.
			if stored.Reason.hidesDetail() && stored.Detail != "" {
				t.Errorf("Detail survived a %s entry: %q — the sink must drop it", stored.Reason, stored.Detail)
			}
		})
	}
}

// ── break test 5: the useful category survives ──────────────────────────────

// TestAdd_TheUsefulCategorySurvives is the anti-over-correction guard, and it
// is the direct replacement for TestAdd_UnrelatedErrorsKeepTheirText.
//
// A record that says only "an error occurred" is a different failure, not a
// fix. So this walks the real reasons and asserts that each produces a distinct
// sentence naming a distinct category — and that the entry still carries the
// four structured fields a reader needs to know WHAT failed, which is where the
// specificity that used to live in the raw error text has gone.
func TestAdd_TheUsefulCategorySurvives(t *testing.T) {
	seen := map[string]Reason{}
	for reason, sentence := range safeSentences {
		if other, dup := seen[sentence]; dup {
			t.Errorf("%s and %s share a sentence — two categories a reader cannot tell apart: %q",
				reason, other, sentence)
		}
		seen[sentence] = reason
		if len(strings.Fields(sentence)) < 8 {
			t.Errorf("the %s sentence is too short to say anything useful: %q", reason, sentence)
		}
	}

	// And the structured half, on a real entry.
	log := NewLog(10)
	log.Add(Entry{
		Level:    "error",
		Event:    "argocd_auto_remediation_failed",
		Action:   "terminate_operation",
		Resource: "app:cert-manager-prod-eu",
		Source:   "remediation",
		Result:   "failure",
		Changes:  ChangesNone,
		Reason:   ReasonUpstream,
		Detail:   "failed to terminate stale sync for cert-manager-prod-eu after PR #41 merged",
	})
	stored := log.List(0)[0]

	for name, got := range map[string]string{
		"Event":    stored.Event,
		"Action":   stored.Action,
		"Resource": stored.Resource,
		"Result":   stored.Result,
		"Reason":   string(stored.Reason),
		"Detail":   stored.Detail,
		"Error":    stored.Error.String(),
	} {
		if got == "" {
			t.Errorf(`%s is empty on a stored failure entry.

A reader must still learn which operation failed, on what, and what sort of
failure it was. Removing the raw error text is only correct while these
survive.`, name)
		}
	}
	if stored.Changes != ChangesNone {
		t.Errorf("Changes = %q, want it to survive", stored.Changes)
	}
}

// ── idempotency ─────────────────────────────────────────────────────────────

// TestSanitize_IsIdempotentWithoutComparingAnyText.
//
// Sanitising an already-safe entry must not degrade it. This is normally the
// hard part, because the obvious way to tell "already safe" from "raw" is to
// look at the text — which is banned on a credential path and would stop
// working the day a sentence was reworded.
//
// The design sidesteps the question entirely: Error is a pure function of
// Reason (sentenceFor is a map lookup) and sanitize does not read the incoming
// Error at all. So there is no state to detect. Running it twice runs the same
// lookup twice, and this test pins that for every reason including the invalid
// ones.
func TestSanitize_IsIdempotentWithoutComparingAnyText(t *testing.T) {
	reasons := []Reason{"", Reason("invented"), Reason(auditSentinel)}
	for r := range safeSentences {
		reasons = append(reasons, r)
	}

	for _, r := range reasons {
		base := Entry{
			ID: "fixed", Event: "e", Result: "failure", Reason: r,
			Detail: "a detail worth keeping",
		}
		once := sanitize(base)
		twice := sanitize(once)
		thrice := sanitize(twice)

		if !reflect.DeepEqual(once, twice) || !reflect.DeepEqual(twice, thrice) {
			t.Errorf(`sanitize is not idempotent for reason %q:
  once:   %+v
  twice:  %+v
  thrice: %+v`, r, once, twice, thrice)
		}
		// The specific degradation the ruling names: a second pass must not
		// turn a real category into the empty or fallback one.
		if r.Valid() && twice.Reason != r {
			t.Errorf("a second pass changed reason %q into %q — the useful category was destroyed by double sanitisation", r, twice.Reason)
		}
		if r.Valid() && twice.Error.String() != safeSentences[r] {
			t.Errorf("a second pass changed the %q sentence to %q", r, twice.Error.String())
		}
	}
}

// TestAdd_DiscardsAJSONForgedSafeText.
//
// SafeText has an UnmarshalJSON so a stored entry can be decoded back. That is
// the one way a value of the type can be produced outside this package — so
// this checks it is not a hole. sanitize rebuilds Error from Reason
// unconditionally and never reads the incoming value, so a forged one is gone
// the moment it reaches Add.
func TestAdd_DiscardsAJSONForgedSafeText(t *testing.T) {
	var forged SafeText
	if err := json.Unmarshal([]byte(`"AccessDenied: `+auditSentinel+`"`), &forged); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if !strings.Contains(forged.String(), auditSentinel) {
		t.Fatal("THE MUTATION DID NOT LAND: the forged SafeText does not carry the sentinel, so this test sweeps nothing")
	}

	log := NewLog(10)
	log.Add(Entry{Event: "e", Result: "failure", Error: forged, Reason: ReasonUpstream})
	stored := log.List(0)[0]

	if strings.Contains(stored.Error.String(), auditSentinel) {
		t.Errorf("a JSON-forged SafeText survived Add: %q", stored.Error.String())
	}
	if stored.Error.String() != safeSentences[ReasonUpstream] {
		t.Errorf("stored Error = %q, want the catalog sentence for %s", stored.Error.String(), ReasonUpstream)
	}
}

// ── the stream is covered too ───────────────────────────────────────────────

// TestAdd_SanitizesTheSTREAMEDCopyToo.
//
// This is the surface a read-side fix would have missed. Add fans the same
// value out to every SSE subscriber; the stream handler json.Marshals it raw.
// Because Add sanitizes BEFORE both the append and the fan-out, one fix covers
// List and the stream, and it cannot be bypassed by the stream.
func TestAdd_SanitizesTheSTREAMEDCopyToo(t *testing.T) {
	log := NewLog(10)
	ch, unsub := log.Subscribe()
	defer unsub()

	log.Add(Entry{
		Event:  "e",
		Reason: ReasonCredentials,
		Detail: "AccessDenied while resolving " + auditSentinel,
	})

	streamed := <-ch
	blob, err := json.Marshal(streamed)
	if err != nil {
		t.Fatalf("marshalling the streamed entry: %v", err)
	}
	if strings.Contains(string(blob), auditSentinel) {
		t.Errorf("the STREAMED entry carries the sentinel: %s", blob)
	}
	if strings.Contains(fmt.Sprintf("%+v", streamed), auditSentinel) {
		t.Errorf("the STREAMED entry printed with %%+v carries the sentinel:\n%+v", streamed)
	}
	if streamed.Error.String() != credsafe.Message {
		t.Errorf("the streamed entry's Error = %q, want the fixed safe sentence", streamed.Error.String())
	}
	if streamed.Detail != "" {
		t.Errorf("the streamed entry's Detail = %q, want empty — a credentials entry has one answer and it lives in Error", streamed.Detail)
	}
}

// TestAdd_CredentialsDetailIsDroppedOnEveryEntry is the second structural hole
// the ruling names, pinned.
//
// The old sanitize cleared Detail only INSIDE the `if entry.CredentialFailure`
// branch, so a Detail writer that set no flag shipped its text verbatim. The
// clearing is now driven by the sink's own reading of a validated enum, so it
// happens for every credentials entry regardless of what else the writer did or
// did not set.
func TestAdd_CredentialsDetailIsDroppedOnEveryEntry(t *testing.T) {
	for _, r := range []Reason{ReasonCredentials, ReasonSecretValue} {
		log := NewLog(10)
		log.Add(Entry{Event: "e", Reason: r, Detail: "anything at all " + auditSentinel})
		if got := log.List(0)[0].Detail; got != "" {
			t.Errorf("reason %s: Detail = %q, want empty", r, got)
		}
	}
	// And NOT dropped for the others — over-correction is its own failure.
	log := NewLog(10)
	log.Add(Entry{Event: "e", Reason: ReasonUpstream, Detail: "re-sync failed after PR #41 merged"})
	if got := log.List(0)[0].Detail; got != "re-sync failed after PR #41 merged" {
		t.Errorf("Detail = %q — a non-credentials entry's detail must survive", got)
	}
}

// ── structural guards ───────────────────────────────────────────────────────

// TestEntry_HasNoErrorTypedFieldAtAll is the structural guard, kept from the
// previous design and still exactly right.
//
// An audit entry must not carry an error, hidden by json:"-" or otherwise:
// json:"-" hides a field from ONE reader while leaving it there for the next
// person who prints the entry with %+v or reflects over it. Classification runs
// at the boundary where the typed error is still alive, and only the CATEGORY
// travels here.
func TestEntry_HasNoErrorTypedFieldAtAll(t *testing.T) {
	errType := reflect.TypeOf((*error)(nil)).Elem()
	entryType := reflect.TypeOf(Entry{})

	for i := 0; i < entryType.NumField(); i++ {
		f := entryType.Field(i)
		if f.Type == errType || f.Type.Implements(errType) ||
			(f.Type.Kind() == reflect.Ptr && f.Type.Implements(errType)) {
			t.Errorf(`audit.Entry has an error-typed field %q (%s).

An audit entry must not carry an error. Classify it at the boundary, where the
typed error is still alive, and send the CATEGORY here. An error on this struct
is a leak waiting for the next %%+v.`, f.Name, f.Type)
		}
	}
}

// TestAdd_StoredEntryHoldsNoLiveError is the value-level companion: it walks a
// stored entry by reflection, so a renamed or newly-added field holding a live
// error is caught even if the type-level check above were somehow satisfied.
func TestAdd_StoredEntryHoldsNoLiveError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry Entry
	}{
		{"a credentials failure", Entry{Event: "e", Reason: ReasonCredentials}},
		{"a plain git failure", Entry{Event: "e", Reason: ReasonUpstream}},
		{"nothing special at all", Entry{Event: "e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(tc.entry)
			stored := log.List(0)[0]

			v := reflect.ValueOf(stored)
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Kind() == reflect.Interface && !f.IsNil() {
					if e, ok := f.Interface().(error); ok {
						t.Errorf("field %q holds a live error (%v)", v.Type().Field(i).Name, e)
					}
				}
			}
		})
	}
}

// TestEntry_ReasonSerializesAsItself pins that the category reaches a reader.
// It is the field that replaced the raw text, so a reader that cannot see it
// has been left with less than before.
func TestEntry_ReasonSerializesAsItself(t *testing.T) {
	log := NewLog(10)
	log.Add(Entry{Event: "e", Result: "failure", Reason: ReasonPermission})
	blob, err := json.Marshal(log.List(0)[0])
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(blob), `"reason":"permission_denied"`) {
		t.Errorf("the reason is not on the wire: %s", blob)
	}
	if !strings.Contains(string(blob), `"error":"`) {
		t.Errorf("the sentence is not on the wire under the key readers already use: %s", blob)
	}
}

// TestEntry_SuccessCarriesNoErrorKey keeps the wire shape the old
// `omitempty` string had, so a reader that treats the presence of "error" as
// "this failed" is not broken by the type change.
func TestEntry_SuccessCarriesNoErrorKey(t *testing.T) {
	log := NewLog(10)
	log.Add(Entry{Event: "e", Result: "success"})
	blob, err := json.Marshal(log.List(0)[0])
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(blob), `"error"`) {
		t.Errorf("a success entry carries an \"error\" key: %s", blob)
	}
}

// ── the classifier ──────────────────────────────────────────────────────────

// TestClassify_UnmarkedGetsTheSameProtectionAsMarked is the ruling's central
// requirement, stated as a test.
//
// Both errors carry the sentinel. Neither reaches a stored entry. The marked
// one is additionally recognised as a credentials failure, which is the only
// difference marking makes now.
func TestClassify_UnmarkedGetsTheSameProtectionAsMarked(t *testing.T) {
	raw := errors.New("STS AssumeRole failed with " + auditSentinel)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"marked", credsafe.Mark(raw)},
		{"unmarked", raw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := NewLog(10)
			log.Add(Entry{Event: "e", Result: "failure", Reason: Classify(tc.err)})
			stored := log.List(0)[0]

			blob, err := json.Marshal(stored)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if strings.Contains(string(blob), auditSentinel) {
				t.Errorf("the %s error leaked into the audit record: %s", tc.name, blob)
			}
			if !stored.Reason.Valid() {
				t.Errorf("the %s error produced no usable category (%q)", tc.name, stored.Reason)
			}
		})
	}
}

// TestClassify_NilIsNotAFailure — an entry that is not about a failure gets no
// reason and therefore no sentence.
func TestClassify_NilIsNotAFailure(t *testing.T) {
	if got := Classify(nil); got != "" {
		t.Errorf("Classify(nil) = %q, want empty", got)
	}
	if got := sentenceFor(""); !got.IsZero() {
		t.Errorf("sentenceFor(\"\") = %q, want the zero SafeText", got.String())
	}
}
