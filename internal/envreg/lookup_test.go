package envreg

// lookup_test.go — the resolver, the conflict error and the deprecation
// warning.
//
// Zero deprecated aliases ship today, on purpose. That is exactly why
// these tests install a temporary registry: the alias path is the part
// nobody exercises by accident, so if it is only checked the day someone
// adds an alias, it will be wrong that day.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// withSettings swaps the registry for the duration of a test.
func withSettings(t *testing.T, replacement []Setting) {
	t.Helper()
	original := settings
	settings = replacement
	resetForTest()
	t.Cleanup(func() {
		settings = original
		resetForTest()
	})
}

func aliasFixture() []Setting {
	return []Setting{
		{
			Name: "SHARKO_PORT", Kind: Production,
			Summary: "TCP port the server listens on.", Default: "8080",
			ReaderFile: readerServe,
		},
		{
			Name: "SHARKO_OLD_PORT", Kind: DeprecatedAlias, AliasOf: "SHARKO_PORT",
			Summary:    "Former name for SHARKO_PORT.",
			ReaderFile: readerServe,
		},
	}
}

func TestLookup_CanonicalOnly_NoWarning(t *testing.T) {
	withSettings(t, aliasFixture())
	t.Setenv("SHARKO_PORT", "9090")

	value, present, err := Lookup("SHARKO_PORT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "9090" || !present {
		t.Errorf("got (%q, %v), want (\"9090\", true)", value, present)
	}
	if got := PendingDeprecations(); len(got) != 0 {
		t.Errorf("canonical-only use warned about %v — nothing is deprecated here", got)
	}
}

func TestLookup_AliasOnly_UsesItAndWarns(t *testing.T) {
	withSettings(t, aliasFixture())
	t.Setenv("SHARKO_OLD_PORT", "9090")

	value, present, err := Lookup("SHARKO_PORT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "9090" || !present {
		t.Errorf("got (%q, %v), want (\"9090\", true)", value, present)
	}
	want := []Deprecation{{Alias: "SHARKO_OLD_PORT", Canonical: "SHARKO_PORT"}}
	if got := PendingDeprecations(); len(got) != 1 || got[0] != want[0] {
		t.Errorf("deprecations = %v, want %v", got, want)
	}
}

func TestLookup_BothSetAndEqual_UsesItAndWarns(t *testing.T) {
	withSettings(t, aliasFixture())
	t.Setenv("SHARKO_PORT", "9090")
	t.Setenv("SHARKO_OLD_PORT", "9090")

	value, present, err := Lookup("SHARKO_PORT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "9090" || !present {
		t.Errorf("got (%q, %v), want (\"9090\", true)", value, present)
	}
	if got := PendingDeprecations(); len(got) != 1 {
		t.Errorf("deprecations = %v, want exactly one", got)
	}
}

func TestLookup_BothSetAndDifferent_FailsAndRepeatsNoValues(t *testing.T) {
	withSettings(t, aliasFixture())
	const canonicalValue = "9090"
	const aliasValue = "7070"
	t.Setenv("SHARKO_PORT", canonicalValue)
	t.Setenv("SHARKO_OLD_PORT", aliasValue)

	_, _, err := Lookup("SHARKO_PORT")
	if err == nil {
		t.Fatalf("two contradictory values resolved without an error — one of them was silently preferred")
	}
	msg := err.Error()
	for _, name := range []string{"SHARKO_PORT", "SHARKO_OLD_PORT"} {
		if !strings.Contains(msg, name) {
			t.Errorf("the conflict error does not name %s: %q", name, msg)
		}
	}
	for _, value := range []string{canonicalValue, aliasValue} {
		if strings.Contains(msg, value) {
			t.Errorf("the conflict error repeats the value %q — an error that prints configuration "+
				"values leaks a credential the day a secret-bearing name is deprecated: %q", value, msg)
		}
	}

	// Startup must refuse to boot on it.
	if err := Validate(); err == nil {
		t.Errorf("Validate() accepted a conflicting configuration — startup would continue on a contradiction")
	}
}

func TestLookup_NeitherSet_ReturnsTheRegisteredDefault(t *testing.T) {
	withSettings(t, aliasFixture())

	value, present, err := Lookup("SHARKO_PORT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Errorf("present = true with nothing set")
	}
	if value != "8080" {
		t.Errorf("value = %q, want the registered default 8080", value)
	}
}

func TestLookup_UnregisteredNameIsAnError(t *testing.T) {
	if _, _, err := Lookup("SHARKO_NEVER_REGISTERED"); err == nil {
		t.Fatalf("Lookup accepted an unregistered name — the registry would be a description of the code, not the thing that reads it")
	}
}

// TestWarnDeprecated_RecordCarriesNoValue is break test B-h. Asserting
// only the message would pass a warning whose ATTRIBUTES carry the value,
// so this reads the whole rendered record.
func TestWarnDeprecated_RecordCarriesNoValue(t *testing.T) {
	withSettings(t, aliasFixture())
	const secretish = "hunter2-not-in-any-log"
	t.Setenv("SHARKO_OLD_PORT", secretish)

	if _, _, err := Lookup("SHARKO_PORT"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	WarnDeprecated(logger)

	raw := buf.String()
	if strings.Contains(raw, secretish) {
		t.Fatalf("the deprecation record contains the configured VALUE:\n%s", raw)
	}

	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one warning record, got %d:\n%s", len(lines), raw)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("the warning is not a parseable record: %v\n%s", err, raw)
	}

	// The WHOLE record, key by key. slog adds time, level and msg.
	wantKeys := map[string]bool{"time": true, "level": true, "msg": true, "setting": true, "use_instead": true}
	for key, value := range record {
		if !wantKeys[key] {
			t.Errorf("the deprecation record carries an unexpected attribute %q = %v — a value "+
				"smuggled into an attribute is still a leak", key, value)
		}
	}
	for key := range wantKeys {
		if _, ok := record[key]; !ok {
			t.Errorf("the deprecation record is missing %q", key)
		}
	}
	if record["setting"] != "SHARKO_OLD_PORT" {
		t.Errorf("setting = %v, want SHARKO_OLD_PORT — the warning must name the setting", record["setting"])
	}
	if record["use_instead"] != "SHARKO_PORT" {
		t.Errorf("use_instead = %v, want SHARKO_PORT", record["use_instead"])
	}
}

// TestWarnDeprecated_EmitsOnce holds the "once at startup, from a single
// call" part of the contract.
func TestWarnDeprecated_EmitsOnce(t *testing.T) {
	withSettings(t, aliasFixture())
	t.Setenv("SHARKO_OLD_PORT", "9090")
	if _, _, err := Lookup("SHARKO_PORT"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	WarnDeprecated(logger)
	WarnDeprecated(logger)
	WarnDeprecated(logger)

	if got := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1; got != 1 {
		t.Errorf("got %d warning records from three calls, want 1:\n%s", got, buf.String())
	}
}

// TestValidate_RefusesAnAliasWhoseCanonicalIsNotResolverRouted is the
// structural guard that ships with the alias kind. Zero aliases exist
// today; this is what stops the first one being quietly false.
func TestValidate_RefusesAnAliasWhoseCanonicalIsNotResolverRouted(t *testing.T) {
	cases := []struct {
		name    string
		fixture []Setting
	}{
		{
			name: "canonical is not registered at all",
			fixture: []Setting{
				{Name: "SHARKO_OLD_PORT", Kind: DeprecatedAlias, AliasOf: "SHARKO_PORT",
					Summary: "Former name.", ReaderFile: readerServe},
			},
		},
		{
			name: "canonical is registered but is not a production setting",
			fixture: []Setting{
				{Name: "SHARKO_DEV_MODE", Kind: Internal,
					Summary: "Development switch.", ReaderFile: readerTieredGit},
				{Name: "SHARKO_OLD_DEV_MODE", Kind: DeprecatedAlias, AliasOf: "SHARKO_DEV_MODE",
					Summary: "Former name.", ReaderFile: readerTieredGit},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withSettings(t, tc.fixture)
			if err := Validate(); err == nil {
				t.Errorf("Validate() accepted it — an alias the resolver cannot honour would ship as a working promise")
			}
		})
	}
}

func TestValidate_RefusesADuplicateName(t *testing.T) {
	withSettings(t, []Setting{
		{Name: "SHARKO_PORT", Kind: Production, Summary: "One.", ReaderFile: readerServe},
		{Name: "SHARKO_PORT", Kind: Production, Summary: "Two.", ReaderFile: readerServe},
	})
	if err := Validate(); err == nil {
		t.Errorf("Validate() accepted the same name registered twice")
	}
}

func TestValidate_ShippedRegistryIsClean(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("the shipped registry does not validate: %v", err)
	}
}
