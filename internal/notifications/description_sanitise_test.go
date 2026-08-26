package notifications

// description_sanitise_test.go — proof that a caller cannot get its own words
// onto a notification's description except where that was decided in the open.
//
// # What was wrong
//
// sanitizeNotification rebuilt the description only `if isConnectionCode(...)`
// — one code family out of two. Every other family, including any added
// tomorrow, kept whatever its producer typed. The standing ruling is "the
// caller must not be able to disable safety", and a rule that covers only the
// families somebody remembered is a rule the next caller disables by existing.
//
// The rebuild covered every family after that fix EXCEPT three addon codes,
// which were listed as "producer-owned" and kept whatever prose their producer
// typed. And nothing covered Title, ID or Type on ANY code — Title being the
// line a person actually reads.
//
// There is no exemption of any kind now. Every field a person can read is
// rendered by the server from the code and a set of checked identifiers
// (render.go), on every record, with no way for a caller to opt out.
//
// # Why the sweep proves itself first
//
// A leak sweep that reports nothing is indistinguishable from a leak sweep
// that looks at nothing, and this release round has already produced several of
// the second kind. So every sweep below is first pointed at a record that has
// NOT been sanitised and must FIND the sentinel there. Only then is its silence
// on the sanitised record worth anything.

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// leakSentinel is deliberately unlike anything Sharko writes, so a hit is
// never a coincidence. Stand-in for what a raw provider error would carry.
const leakSentinel = "SENTINEL-a9f2c7-do-not-ship-this-anywhere"

// sweepForSentinel returns every form of the sentinel found in text: the raw
// value, its base64 encoding, and any length-derived form.
//
// The last two are not paranoia. A "safe" record that carries base64 of the
// original, or its length, still leaks — a length alone narrows a secret, and
// this project's standing rule bans raw value, base64, hash, length, partial
// and variable-width mask alike.
func sweepForSentinel(text string) []string {
	var found []string
	if strings.Contains(text, leakSentinel) {
		found = append(found, "raw value")
	}
	if strings.Contains(text, base64.StdEncoding.EncodeToString([]byte(leakSentinel))) {
		found = append(found, "base64 of the value")
	}
	if strings.Contains(text, base64.RawURLEncoding.EncodeToString([]byte(leakSentinel))) {
		found = append(found, "url-safe base64 of the value")
	}
	// A length-derived form: the number itself, and the two shapes Sharko's
	// own code would most plausibly write it in.
	n := strconv.Itoa(len(leakSentinel))
	for _, shape := range []string{n + " characters", n + " bytes", "(" + n + ")", "length " + n} {
		if strings.Contains(text, shape) {
			found = append(found, "a length-derived form ("+shape+")")
		}
	}
	// A distinctive fragment, in case something truncated rather than dropped.
	if strings.Contains(text, "a9f2c7") {
		found = append(found, "a fragment of the value")
	}
	return found
}

// TestSanitise_SweepFindsTheSentinelWhenNothingIsSanitised is the POSITIVE
// CONTROL, and it runs first on purpose.
//
// It asserts the sweep FINDS the sentinel in an unsanitised record. Without
// this, every "the sentinel is absent" assertion below could be passing
// because the sweep is broken rather than because the code is safe.
func TestSanitise_SweepFindsTheSentinelWhenNothingIsSanitised(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"the raw value", "something went wrong: " + leakSentinel},
		{"base64 of the value", "payload " + base64.StdEncoding.EncodeToString([]byte(leakSentinel))},
		{"url-safe base64", "payload " + base64.RawURLEncoding.EncodeToString([]byte(leakSentinel))},
		{"a length-derived form", fmt.Sprintf("the secret is %d characters", len(leakSentinel))},
		{"a fragment", "trimmed: SENTINEL-a9f2c7…"},
	}
	for _, tc := range cases {
		if found := sweepForSentinel(tc.text); len(found) == 0 {
			t.Errorf("the sweep did NOT find %s in %q — its silence on a sanitised record would prove nothing", tc.name, tc.text)
		}
	}
	// And it is not a sweep that fires on everything.
	if found := sweepForSentinel("Sharko could not reach ArgoCD. Check the connection."); len(found) != 0 {
		t.Errorf("the sweep fired on an ordinary sentence (%v) — it would fail on safe records and prove nothing", found)
	}
}

// TestSanitise_CallerWordsNeverSurviveOnAnyCode drives the REAL path —
// Store.Add, then Store.List — and sweeps what a reader would actually get.
//
// EVERY declared code, with no skips. The version of this test that shipped
// before skipped three codes because they were exempt, so the three that most
// needed proving were the three it did not look at.
func TestSanitise_CallerWordsNeverSurviveOnAnyCode(t *testing.T) {
	for _, code := range DeclaredCodes() {
		t.Run(code.String(), func(t *testing.T) {
			s := NewStore(10, nil)
			// A caller doing everything it can to get its own words through:
			// the description, the title, the id, and the type — all four of
			// which are persisted and all four of which a reader can see.
			s.Add(Notification{
				ID:          "leak-probe-" + leakSentinel,
				Code:        code,
				Type:        NotificationType(leakSentinel),
				Title:       "the provider said: " + leakSentinel,
				Description: "the provider said: " + leakSentinel,
				Reason:      ReasonUnreachable,
			})
			got := s.List()
			if len(got) != 1 {
				t.Fatalf("the probe was not stored (%d records) — this case proved nothing", len(got))
			}
			for _, surface := range []struct{ name, text string }{
				{"description", got[0].Description},
				{"title", got[0].Title},
				{"id", got[0].ID},
				{"type", string(got[0].Type)},
			} {
				if found := sweepForSentinel(surface.text); len(found) > 0 {
					t.Errorf("a caller's own words reached the stored %s as %v:\n  %q", surface.name, found, surface.text)
				}
			}
			// And the reader is not left with a blank where the explanation
			// should be.
			if got[0].Description == "" {
				t.Error("the description was emptied rather than rebuilt — the reader is told something happened and not what")
			}
			if got[0].Title == "" {
				t.Error("the title was emptied rather than rebuilt — the bell would show a blank line")
			}
		})
	}
}

// TestSanitise_AnUndeclaredCodeAlsoRebuilds pins the DEFAULT.
//
// Store.Add refuses an undeclared code outright, so this drives
// sanitizeNotification itself — the function every future call path will go
// through. A brand-new code family added tomorrow lands here before anybody
// writes a template for it, and it must land on the safe side.
func TestSanitise_AnUndeclaredCodeAlsoRebuilds(t *testing.T) {
	invented := Code("a_family_nobody_has_added_yet")
	if invented.IsDeclared() {
		t.Fatal("the invented code is actually declared — pick another or this proves nothing")
	}
	if _, hasTemplate := messageTemplates[invented]; hasTemplate {
		t.Fatal("the invented code has a template — pick another or this proves nothing")
	}

	n := Notification{
		Code:        invented,
		Title:       "raw backend error: " + leakSentinel,
		Description: "raw backend error: " + leakSentinel,
		Reason:      Reason(leakSentinel), // an undeclared reason, the other way in
	}
	sanitizeNotification(&n)

	if found := sweepForSentinel(n.Description); len(found) > 0 {
		t.Errorf("an undeclared code kept the caller's description as %v:\n  %q", found, n.Description)
	}
	if found := sweepForSentinel(n.Title); len(found) > 0 {
		t.Errorf("an undeclared code kept the caller's title as %v:\n  %q", found, n.Title)
	}
	if found := sweepForSentinel(string(n.Reason)); len(found) > 0 {
		t.Errorf("an undeclared reason survived sanitisation as %v: %q", found, n.Reason)
	}
	if n.Reason != ReasonUnspecified {
		t.Errorf("Reason = %q, want %q — an undeclared reason must be replaced, not passed through", n.Reason, ReasonUnspecified)
	}
	if n.Title != TitleUnclassified {
		t.Errorf("Title = %q, want the generic safe title %q", n.Title, TitleUnclassified)
	}
}
