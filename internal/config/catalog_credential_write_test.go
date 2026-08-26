// catalog_credential_write_test.go — replaces catalog_credential_roundtrip_test.go.
//
// # What the old test said, and why it is gone
//
// The old file required a repository address carrying an operator's password
// to survive a read-write round trip byte for byte. It was written to stop a
// naive fix for a response leak from quietly deleting a credential out of the
// operator's Git repository, and that worry was real: the catalog structs are
// the file on disk, Sharko rewrites the WHOLE file on every ordinary change,
// and Sharko has no second copy of anything it deletes.
//
// What the old file got wrong was the thing it was protecting. It protected
// KEEPING a plaintext token in a file that is committed to Git — which the
// approved security contract does not allow, and cannot allow: Git is durable
// and replicated, so a token in a commit is also in every clone, every fork,
// every CI cache and every backup, and editing the file later does not take it
// back.
//
// So the answer is not "keep it carefully". It is "never write it in the
// first place". These tests require exactly that.
//
// # What is kept
//
// The data-loss worry is still real, so the byte-identical round trip is still
// proved here — for a CLEAN address, which is the only kind that may be
// written. If anything ever starts rewriting ordinary catalog content, these
// tests fail the same way the old ones would have.
package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// cleanRepoURL is an address with nothing in it that could hide a credential:
// no user information, no query string, no fragment.
const cleanRepoURL = "https://charts.example/org/charts"

// plantedSecret is the stand-in for an operator's stored password. It is
// written out in full so a reader can see exactly what must never be written.
const plantedSecret = "P4ss-w0rd-the-operator-actually-stored-9x2f"

// sweptSecret is the same text again, written independently. Nothing derives
// one from the other, so a fixture that stops carrying the secret cannot also
// silently stop the sweep from looking for it. TestSentinelsAgree keeps the two
// honest.
const sweptSecret = "P4ss-w0rd-the-operator-actually-stored-9x2f"

func TestSentinelsAgree(t *testing.T) {
	if plantedSecret != sweptSecret {
		t.Fatalf("the planted secret and the swept secret have drifted apart:\n  planted %q\n  swept   %q\nEvery clean-output assertion in this file is looking for the wrong text.", plantedSecret, sweptSecret)
	}
	if plantedSecret == "" {
		t.Fatal("the sentinel is empty — strings.Contains would answer true for everything and every assertion below would pass without proving anything")
	}
}

// carriers is every shape a credential can be written into an address, plus
// the two ordinary-looking shapes that are refused anyway.
//
// The classifier underneath is structural: it asks whether the address has
// somewhere for a credential to sit, never whether the text there looks
// secret. That is why "?ref=main" is in this list. It is not a bug and it is
// not an accident — it is the price of a rule that cannot be fooled by a shape
// nobody predicted, and it is pinned here so it stays a decision.
var carriers = []struct {
	name string
	url  string
	// intentional is true for a shape with no credential in it at all,
	// refused because the rule is structural.
	intentional bool
}{
	{name: "userinfo with a password", url: "https://git-user:" + plantedSecret + "@charts.example/org/charts"},
	{name: "userinfo without a password", url: "https://" + plantedSecret + "@charts.example/org/charts"},
	{name: "the x-access-token shape", url: "https://x-access-token:" + plantedSecret + "@charts.example/org/charts"},
	{name: "a query string", url: "https://charts.example/org/charts?access_token=" + plantedSecret},
	{name: "a fragment", url: "https://charts.example/org/charts#" + plantedSecret},
	{name: "an ordinary query string, refused on purpose", url: "https://charts.example/org/charts?ref=main", intentional: true},
	{name: "an empty forced query, refused on purpose", url: "https://charts.example/org/charts?", intentional: true},
}

func TestCarrierListIsNotEmpty(t *testing.T) {
	// A table that quietly became empty would make every test that ranges
	// over it pass without running a single case.
	const want = 7
	if len(carriers) != want {
		t.Fatalf("the carrier table has %d entries, want exactly %d — if a shape was added or removed on purpose, move this number with it", len(carriers), want)
	}
	var real, intentional int
	for _, c := range carriers {
		if c.intentional {
			intentional++
		} else {
			real++
		}
	}
	if real == 0 || intentional == 0 {
		t.Fatalf("the table must cover both kinds: %d shapes that really carry a credential, %d refused on the structural rule alone", real, intentional)
	}
}

// --- v3: MarshalAddonCatalog ------------------------------------------------

func TestMarshalAddonCatalog_CleanURLRoundTripsByteIdentically(t *testing.T) {
	original, err := MarshalAddonCatalog("addon-catalog", []models.AddonCatalogEntry{{
		Name:      "keda",
		RepoURL:   cleanRepoURL,
		Chart:     "keda",
		Version:   "2.13.0",
		Namespace: "keda",
		AdditionalSources: []models.AddonSource{{
			RepoURL: cleanRepoURL,
			Chart:   "keda-extras",
			Version: "1.0.0",
		}},
	}})
	if err != nil {
		t.Fatalf("building the starting file: %v", err)
	}
	if !strings.Contains(string(original), cleanRepoURL) {
		t.Fatal("the starting file does not carry the address at all — this test would prove nothing")
	}

	entries, err := NewParser().ParseAddonsCatalog(original)
	if err != nil {
		t.Fatalf("reading the catalog back: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("read back %d entries, want 1", len(entries))
	}
	if entries[0].RepoURL != cleanRepoURL {
		t.Errorf("the parsed entry's repoURL is %q, want the operator's value unchanged %q", entries[0].RepoURL, cleanRepoURL)
	}
	if len(entries[0].AdditionalSources) != 1 || entries[0].AdditionalSources[0].RepoURL != cleanRepoURL {
		t.Errorf("the extra source's repoURL did not survive the read: %+v", entries[0].AdditionalSources)
	}

	written, err := MarshalAddonCatalog("addon-catalog", entries)
	if err != nil {
		t.Fatalf("writing the catalog back: %v", err)
	}
	if string(written) != string(original) {
		t.Errorf(`the catalog file changed on a read-write round trip.

Ordinary catalog content must survive untouched. Sharko rewrites the whole file
on every change and has no second copy of what it drops.

before:
%s
after:
%s`, original, written)
	}
}

func TestMarshalAddonCatalog_RefusesEveryCarrier(t *testing.T) {
	for _, c := range carriers {
		t.Run(c.name, func(t *testing.T) {
			entries := []models.AddonCatalogEntry{{
				Name: "keda", RepoURL: c.url, Chart: "keda", Version: "2.13.0",
			}}

			out, err := MarshalAddonCatalog("addon-catalog", entries)
			assertRefused(t, out, err, AddonCatalogFilename, "spec.applicationsets[keda].repoURL", c.intentional)
		})
	}
}

func TestMarshalAddonCatalog_RefusesACarrierOnAnExtraSource(t *testing.T) {
	entries := []models.AddonCatalogEntry{{
		Name: "keda", RepoURL: cleanRepoURL, Chart: "keda", Version: "2.13.0",
		AdditionalSources: []models.AddonSource{
			{RepoURL: cleanRepoURL, Chart: "a"},
			{RepoURL: "https://git-user:" + plantedSecret + "@charts.example/org/charts", Chart: "b"},
		},
	}}
	out, err := MarshalAddonCatalog("addon-catalog", entries)
	assertRefused(t, out, err, AddonCatalogFilename, "spec.applicationsets[keda].additionalSources[1].repoURL", false)
}

// --- v4: SaveAddonCatalog ---------------------------------------------------

func TestSaveAddonCatalog_CleanURLRoundTripsByteIdentically(t *testing.T) {
	spec := AddonCatalogSpec{Addons: map[string]AddonCatalogEntry{
		"keda": {
			RepoURL:   cleanRepoURL,
			Chart:     "keda",
			Version:   "2.13.0",
			Namespace: "keda",
			AdditionalSources: []models.AddonSource{{
				RepoURL: cleanRepoURL,
				Chart:   "keda-extras",
				Version: "1.0.0",
			}},
		},
	}}

	original, err := SaveAddonCatalog(spec)
	if err != nil {
		t.Fatalf("building the starting file: %v", err)
	}
	if !strings.Contains(string(original), cleanRepoURL) {
		t.Fatal("the starting file does not carry the address at all — this test would prove nothing")
	}

	readBack, err := LoadAddonCatalog(original)
	if err != nil {
		t.Fatalf("reading catalog.yaml back: %v", err)
	}
	entry, ok := readBack.Addons["keda"]
	if !ok {
		t.Fatal("the addon is missing after the read")
	}
	if entry.RepoURL != cleanRepoURL {
		t.Errorf("the parsed entry's repoURL is %q, want %q", entry.RepoURL, cleanRepoURL)
	}

	written, err := SaveAddonCatalog(readBack)
	if err != nil {
		t.Fatalf("writing catalog.yaml back: %v", err)
	}
	if string(written) != string(original) {
		t.Errorf("catalog.yaml changed on a read-write round trip.\n\nbefore:\n%s\nafter:\n%s", original, written)
	}
}

func TestSaveAddonCatalog_RefusesEveryCarrier(t *testing.T) {
	for _, c := range carriers {
		t.Run(c.name, func(t *testing.T) {
			spec := AddonCatalogSpec{Addons: map[string]AddonCatalogEntry{
				"keda": {RepoURL: c.url, Chart: "keda", Version: "2.13.0"},
			}}
			out, err := SaveAddonCatalog(spec)
			assertRefused(t, out, err, AddonCatalogPath, "addons.keda.repoURL", c.intentional)
		})
	}
}

func TestSaveAddonCatalog_RefusesACarrierOnAnExtraSource(t *testing.T) {
	spec := AddonCatalogSpec{Addons: map[string]AddonCatalogEntry{
		"keda": {
			RepoURL: cleanRepoURL, Chart: "keda", Version: "2.13.0",
			AdditionalSources: []models.AddonSource{
				{RepoURL: cleanRepoURL, Chart: "a"},
				{RepoURL: "https://charts.example/org/charts#" + plantedSecret, Chart: "b"},
			},
		},
	}}
	out, err := SaveAddonCatalog(spec)
	assertRefused(t, out, err, AddonCatalogPath, "addons.keda.additionalSources[1].repoURL", false)
}

// TestSaveAddonCatalog_OneBadEntryStopsTheWholeFile is the "never silently
// rewrite" proof at the writer. A catalog that already carries such an address
// in Git must not be rewritten without it, and must not be rewritten with the
// entry quietly dropped — either would be Sharko editing an operator's
// repository behind their back. So the whole write is refused, the file on
// disk stays exactly as it is, and the operator is told which field to fix.
func TestSaveAddonCatalog_OneBadEntryStopsTheWholeFile(t *testing.T) {
	spec := AddonCatalogSpec{Addons: map[string]AddonCatalogEntry{
		"keda":         {RepoURL: cleanRepoURL, Chart: "keda", Version: "2.13.0"},
		"cert-manager": {RepoURL: "https://git-user:" + plantedSecret + "@charts.example/org/charts", Chart: "cert-manager", Version: "1.14.5"},
	}}
	out, err := SaveAddonCatalog(spec)
	assertRefused(t, out, err, AddonCatalogPath, "addons.cert-manager.repoURL", false)
}

// --- the shared assertion ---------------------------------------------------

// assertRefused is the whole contract in one place: the write produced no
// bytes at all, the refusal is recognised BY TYPE, it names the file and the
// field, it states the rule, and nothing anywhere in it is derived from the
// value that was refused.
func assertRefused(t *testing.T, out []byte, err error, wantFile, wantField string, intentional bool) {
	t.Helper()

	if err == nil {
		t.Fatalf("the write was allowed. A catalog file carrying this address would now be committed to Git:\n%s", out)
	}
	if len(out) != 0 {
		t.Errorf("the write was refused but still handed back %d bytes — a caller that ignores the error would commit them", len(out))
	}

	var typed *credsafe.UnsupportedRepoURLError
	if !errors.As(err, &typed) {
		t.Fatalf("the refusal is not a *credsafe.UnsupportedRepoURLError, so a caller has to read the English to know what happened: %v", err)
	}
	if !errors.Is(err, credsafe.ErrRepoURLUnsupported) {
		t.Error("errors.Is against the sentinel says no — a caller that only wants to know the reason cannot")
	}
	if typed.File != wantFile {
		t.Errorf("the refusal names file %q, want %q", typed.File, wantFile)
	}
	if typed.Field != wantField {
		t.Errorf("the refusal names field %q, want %q", typed.Field, wantField)
	}

	msg := err.Error()
	if !strings.Contains(msg, credsafe.UnsupportedRepoURLMessage) {
		t.Errorf("the refusal does not state the rule. it said:\n  %s", msg)
	}
	// The message states the rule and never claims a credential was found,
	// because the test underneath cannot know that. ("credential-free" is
	// fine — that is the rule, not an accusation.)
	claim := strings.ReplaceAll(strings.ToLower(msg), "credential-free", "")
	for _, banned := range []string{"credential", "token", "password", "secret", "detected", "found"} {
		if strings.Contains(claim, banned) {
			t.Errorf("the refusal says %q, which claims something Sharko cannot know — the rule is about the SHAPE of the address. it said:\n  %s", banned, msg)
		}
	}
	if strings.Contains(msg, sweptSecret) {
		t.Errorf("the refusal repeats the value it refused")
	}
	if intentional {
		// Nothing more to check: there was no secret in this address.
		return
	}
}

// TestRefusalsCarryNothingFromTheValue plants the secret, proves the sweep can
// find it, and only then requires it to be absent from everything the refusal
// produces.
//
// The order matters. A sweep that cannot find a secret that IS there proves
// nothing at all about one it did not find.
func TestRefusalsCarryNothingFromTheValue(t *testing.T) {
	bad := "https://git-user:" + plantedSecret + "@charts.example/org/charts"

	// Positive control, first: the sweep works.
	if !strings.Contains(bad, sweptSecret) {
		t.Fatal("the sweep cannot find the secret in the address it was planted in — every absence check below would pass for the wrong reason")
	}

	v3out, v3err := MarshalAddonCatalog("addon-catalog", []models.AddonCatalogEntry{{
		Name: "keda", RepoURL: bad, Chart: "keda", Version: "2.13.0",
	}})
	v4out, v4err := SaveAddonCatalog(AddonCatalogSpec{Addons: map[string]AddonCatalogEntry{
		"keda": {RepoURL: bad, Chart: "keda", Version: "2.13.0"},
	}})

	surfaces := map[string]string{
		"the v3 catalog bytes":  string(v3out),
		"the v3 refusal text":   errText(v3err),
		"the v4 catalog bytes":  string(v4out),
		"the v4 refusal text":   errText(v4err),
		"the shared rule text":  credsafe.UnsupportedRepoURLMessage,
		"the sentinel err text": credsafe.ErrRepoURLUnsupported.Error(),
	}
	if len(surfaces) == 0 {
		t.Fatal("no surfaces to sweep")
	}
	for what, s := range surfaces {
		if strings.Contains(s, sweptSecret) {
			t.Errorf("%s carries the planted secret", what)
		}
		// Not the value, not a piece of it, not its length, not a mask.
		if strings.Contains(s, sweptSecret[:8]) {
			t.Errorf("%s carries the first eight characters of the planted secret", what)
		}
		if strings.Contains(s, "git-user") {
			t.Errorf("%s carries the user half of the address", what)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestTheSentenceIsPinned holds the exact words an operator reads. It is
// written out here in full, on purpose: a test that only checks the constant
// is not empty, or compares the constant against itself, would let the
// sentence be reworded into something wrong without a single failure.
func TestTheSentenceIsPinned(t *testing.T) {
	const want = "Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL."
	if credsafe.UnsupportedRepoURLMessage != want {
		t.Errorf("the sentence an operator reads has changed.\n  now:  %s\n  want: %s\n\nIf the wording is meant to move, move it here too and check it still states the RULE rather than claiming a credential was found.", credsafe.UnsupportedRepoURLMessage, want)
	}
}
