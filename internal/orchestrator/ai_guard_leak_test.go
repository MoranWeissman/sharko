// ai_guard_leak_test.go — the secret-leak refusal must never carry any
// part of the value it refused.
//
// The older test in ai_guard_test.go only ever fed values made entirely of
// letters and digits, which is exactly the shape that survives a substring
// mask cleanly. Every case here is a value that contains a character the
// assignment pattern's value class does not accept — a dot, a bang, a
// dollar, a space, an at-sign — which is where a substring-only mask stops
// and starts copying the rest of the line out verbatim.
//
// Every value below is synthetic. Nothing here is, or has ever been, a real
// credential.

package orchestrator

import (
	"strings"
	"testing"
)

// The one fragment the sweep itself is checked against. It is planted on
// purpose in TestSecretValueSweep_FindsPlantedSentinel: if the sweep cannot
// find this, no "nothing leaked" result from the sweep means anything.
const bf15Sentinel = "PLANTEDSENTINELBF15MUSTFIND"

// valueFragmentsIn reports every run of `n` or more characters taken from
// `value` that also shows up in `field`. An empty result is the only
// acceptable answer for a refusal message: it means not one readable piece
// of the value survived. The sweep is deliberately blunt — it does not know
// anything about how the mask works, so a new masking bug cannot hide from
// it by being a shape the sweep did not think of.
func valueFragmentsIn(field, value string, n int) []string {
	var found []string
	seen := map[string]struct{}{}
	for i := 0; i+n <= len(value); i++ {
		frag := value[i : i+n]
		if strings.TrimSpace(frag) == "" {
			continue
		}
		if !strings.Contains(field, frag) {
			continue
		}
		if _, dup := seen[frag]; dup {
			continue
		}
		seen[frag] = struct{}{}
		found = append(found, frag)
	}
	return found
}

// TestSecretValueSweep_FindsPlantedSentinel is the sweep's own positive
// control. A sweep that can only ever return "clean" proves nothing, so
// here it is handed a field string that really does carry the value and
// must say so. If this test ever passes with an empty result, every other
// "no fragment survived" assertion in this file is worthless.
func TestSecretValueSweep_FindsPlantedSentinel(t *testing.T) {
	value := bf15Sentinel + ".tail.part"
	leakyField := "password: ***." + "tail.part"

	// The tail really is in the leaky field, so the sweep must find it.
	if got := valueFragmentsIn(leakyField, value, 6); len(got) == 0 {
		t.Fatalf("the sweep found nothing in a field that demonstrably carries %q — the sweep is broken", value)
	}
	// And the whole sentinel, present verbatim, must be found too.
	if got := valueFragmentsIn("password: "+bf15Sentinel, value, 6); len(got) == 0 {
		t.Fatalf("the sweep missed the planted sentinel %q — the sweep is broken", bf15Sentinel)
	}
	// Control on the sweep itself: a clean field yields nothing.
	if got := valueFragmentsIn("password: ***", value, 6); len(got) != 0 {
		t.Fatalf("the sweep invented a leak in a clean field: %v", got)
	}
}

// bf15Case is one values.yaml line, the value it carries, and the exact
// Field the refusal is allowed to say about it.
type bf15Case struct {
	name string
	// line is the values.yaml line handed to the scanner.
	line string
	// value is the complete secret value on that line. Not one run of six
	// characters from this may appear in the refusal.
	value string
	// wantField is the exact Field text the refusal must carry.
	wantField string
}

func bf15Cases() []bf15Case {
	return []bf15Case{
		{
			name:      "control - value is only letters and digits",
			line:      "password: SYNTHETICVALUE0000ABCDEF",
			value:     "SYNTHETICVALUE0000ABCDEF",
			wantField: "password: ***",
		},
		{
			name:      "value contains a dot",
			line:      "password: SYNTHETICVALUE0001." + bf15Sentinel + ".more",
			value:     "SYNTHETICVALUE0001." + bf15Sentinel + ".more",
			wantField: "password: ***",
		},
		{
			name:      "value contains a bang",
			line:      "password: SYNTHETICVALUE0002!" + bf15Sentinel,
			value:     "SYNTHETICVALUE0002!" + bf15Sentinel,
			wantField: "password: ***",
		},
		{
			name:      "value contains a dollar",
			line:      "password: SYNTHETICVALUE0003$" + bf15Sentinel,
			value:     "SYNTHETICVALUE0003$" + bf15Sentinel,
			wantField: "password: ***",
		},
		{
			name:      "value contains whitespace",
			line:      "password: SYNTHETICVALUE0004 " + bf15Sentinel,
			value:     "SYNTHETICVALUE0004 " + bf15Sentinel,
			wantField: "password: ***",
		},
		{
			name:      "value contains an at sign and a slash",
			line:      "apiKey: SYNTHETICVALUE0005@" + bf15Sentinel + "/deeper",
			value:     "SYNTHETICVALUE0005@" + bf15Sentinel + "/deeper",
			wantField: "apiKey: ***",
		},
		{
			name:      "value contains a comma and a semicolon",
			line:      "apiToken: SYNTHETICVALUE0006," + bf15Sentinel + ";next",
			value:     "SYNTHETICVALUE0006," + bf15Sentinel + ";next",
			wantField: "apiToken: ***",
		},
		{
			name:      "quoted value with a dot outside the quotes-aware class",
			line:      `password: "SYNTHETICVALUE0007.` + bf15Sentinel + `"`,
			value:     `"SYNTHETICVALUE0007.` + bf15Sentinel + `"`,
			wantField: "password: ***",
		},
		{
			name:      "equals separator with a dotted value",
			line:      "credential = SYNTHETICVALUE0008." + bf15Sentinel,
			value:     "SYNTHETICVALUE0008." + bf15Sentinel,
			wantField: "credential: ***",
		},
		{
			name:      "indented and inside a comment",
			line:      "    # password: SYNTHETICVALUE0009." + bf15Sentinel,
			value:     "SYNTHETICVALUE0009." + bf15Sentinel,
			wantField: "password: ***",
		},
		{
			name:      "yaml list item",
			line:      "  - password: SYNTHETICVALUE0010." + bf15Sentinel,
			value:     "SYNTHETICVALUE0010." + bf15Sentinel,
			wantField: "password: ***",
		},
		{
			name:      "dotted key path",
			line:      "global.postgresql.auth.password: SYNTHETICVALUE0011." + bf15Sentinel,
			value:     "SYNTHETICVALUE0011." + bf15Sentinel,
			wantField: "global.postgresql.auth.password: ***",
		},
	}
}

// TestScanForSecrets_CompleteValueIsDiscarded is the acceptance test: for
// every line above the guard must fire, the refusal must read exactly as
// expected, and not one six-character run of the value may survive.
func TestScanForSecrets_CompleteValueIsDiscarded(t *testing.T) {
	for _, tc := range bf15Cases() {
		t.Run(tc.name, func(t *testing.T) {
			matches := ScanForSecrets([]byte(tc.line))

			// No vacuous pass. A line the guard never classified cannot
			// be counted as a clean result — it is a hole in the guard.
			if len(matches) == 0 {
				t.Fatalf("the guard did not fire at all on %q — this case proves nothing about masking; fix the input or the pattern list", tc.line)
			}

			for _, m := range matches {
				if m.Field != tc.wantField {
					t.Errorf("Field = %q, want exactly %q", m.Field, tc.wantField)
				}
				if frags := valueFragmentsIn(m.Field, tc.value, 6); len(frags) > 0 {
					t.Errorf("the refusal still carries part of the value: Field=%q leaked fragments=%v", m.Field, frags)
				}
				if strings.Contains(m.Field, bf15Sentinel) {
					t.Errorf("the refusal carries the planted sentinel verbatim: Field=%q", m.Field)
				}
			}
		})
	}
}

// TestScanForSecrets_NoFieldNameWhenTheLineDoesNotParse covers the lines
// that have no plain `key: value` shape to read a field name off. The
// refusal falls back to the fixed generic text and says nothing about the
// line's contents.
func TestScanForSecrets_NoFieldNameWhenTheLineDoesNotParse(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "PEM begin marker", line: "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{name: "PEM begin marker indented", line: "    -----BEGIN RSA PRIVATE KEY-----"},
		{
			name: "the key position itself is a known secret",
			line: "AKIAIOSFODNN7EXAMPLE: somethingelse",
		},
		{
			name: "words before the key, so the key does not parse",
			line: "some stray words password: SYNTHETICVALUE0012." + bf15Sentinel,
		},
		{
			name: "no separator at all",
			line: "xoxb-1234567890-" + bf15Sentinel,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := ScanForSecrets([]byte(tc.line))
			if len(matches) == 0 {
				t.Fatalf("the guard did not fire at all on %q — this case proves nothing", tc.line)
			}
			for _, m := range matches {
				if m.Field != SecretFieldUnavailable {
					t.Errorf("Field = %q, want the fixed generic text %q", m.Field, SecretFieldUnavailable)
				}
			}
		})
	}
}

// TestScanForSecrets_PEMBlockLeaksNothing walks a whole multi-line PEM
// block, the shape most likely to smuggle bytes out a line at a time.
func TestScanForSecrets_PEMBlockLeaksNothing(t *testing.T) {
	body := "MIIBOgIBAAJBAK" + bf15Sentinel + "0123456789abcdefghij"
	values := "tls:\n" +
		"  key: |\n" +
		"    -----BEGIN RSA PRIVATE KEY-----\n" +
		"    " + body + "\n" +
		"    -----END RSA PRIVATE KEY-----\n"

	matches := ScanForSecrets([]byte(values))
	if len(matches) == 0 {
		t.Fatal("the guard did not fire on a PEM block at all — this case proves nothing")
	}
	for _, m := range matches {
		if strings.Contains(m.Field, bf15Sentinel) {
			t.Errorf("a PEM refusal carries the planted sentinel: Field=%q", m.Field)
		}
		if frags := valueFragmentsIn(m.Field, body, 6); len(frags) > 0 {
			t.Errorf("a PEM refusal carries part of the key body: Field=%q fragments=%v", m.Field, frags)
		}
	}
}

// TestScanForSecrets_MaskWidthIsFixed proves the marker never grows with
// the value. A mask whose width tracks the secret's length is itself a
// disclosure, so two values of very different lengths under the same key
// must produce byte-identical refusals.
func TestScanForSecrets_MaskWidthIsFixed(t *testing.T) {
	shortOne := ScanForSecrets([]byte("password: SYNTHETICVALUE0013AB"))
	longOne := ScanForSecrets([]byte("password: " + strings.Repeat("SYNTHETICVALUE0013AB", 12)))

	if len(shortOne) == 0 || len(longOne) == 0 {
		t.Fatal("the guard did not fire on both inputs — this case proves nothing")
	}
	if shortOne[0].Field != longOne[0].Field {
		t.Errorf("the refusal text changes with the value's length: %q vs %q",
			shortOne[0].Field, longOne[0].Field)
	}
}
