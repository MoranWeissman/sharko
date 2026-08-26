package envreg

// bf11_secret_values_not_echoed_test.go — the diagnostics carrier.
//
// BF11 took the bootstrap admin password out of the Deployment and put it
// behind a Secret reference. That closes the carrier an operator can read with
// kubectl. It says nothing about the OTHER place a setting's value can come
// back out: the things Sharko itself prints about its own environment at
// startup — the "that is not a setting I know" refusal, the deprecation
// warnings, the unused-setting warnings.
//
// Those all take a setting NAME and are supposed to print only the name. This
// test plants a sentinel in the value of every setting the registry marks
// Secret and reads everything those functions produce.
//
// # What BF12-4 changed here, and why it had to
//
// The old version of this file was a guard that could not fail.
//
// It drove the four entry points on a HAPPY environment: every producer
// returned nil, nothing was logged, and the text it then searched was very
// nearly empty. A search for a sentinel in an empty string always says "not
// found", so the test reported clean having read nothing at all. Its
// positive control was worse — it appended the sentinel to its own haystack
// and then checked the haystack contained the sentinel, which is true for
// every possible input. It proved strings.Contains works.
//
// Both halves are replaced. The run below makes every producer that can echo
// a value ACTUALLY PRODUCE: an unknown setting name so the refusals fire, a
// deprecated alias in use so the deprecation warning fires, a test-harness
// setting so the unused-setting warning fires. Each one is asserted non-empty
// before anything is claimed about it, so a producer that falls silent is a
// failure and not a pass.
//
// And the control is planted into the REAL producer, on the SAME path, in the
// SAME run: the unknown setting's NAME carries a second sentinel. These
// functions are supposed to print names, so that sentinel MUST come back —
// and it comes back through exactly the collection code that is then asked
// whether the value sentinel came back too. If the collection ever stops
// reading the producers, the control goes missing and the test fails before
// it can report the value clean.
//
// # What this proves and what it does not
//
// It proves that these entry points print a setting's NAME and never its
// VALUE. It does not prove that no code anywhere in Sharko ever prints one —
// that is a much larger claim, and other tests own their own surfaces. Said
// plainly here so nobody reads a green result as more than it is.

import (
	"bytes"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
)

// bf11SecretSentinel is planted in the VALUE of every Secret setting. It must
// never come back out.
const bf11SecretSentinel = "BF11-PLANTED-SECRET-VALUE-7c04ae"

// bf11NameSentinel is planted in the NAME of an unknown setting. It MUST come
// back out — that is what proves the reader is reading the producers.
//
// It is a legal environment variable name part, because it has to survive
// being exported for real.
const bf11NameSentinel = "BF11PLANTEDNAMEPART7C04AE"

// bf11UnknownSetting is a SHARKO_ name the registry has never heard of, with
// the name sentinel in it. Nothing in the registry is near enough for the
// "did you mean" suggestion to fire, which keeps the refusal text stable.
const bf11UnknownSetting = "SHARKO_" + bf11NameSentinel

// bf11DeprecatedAlias and bf11HarnessSetting are the two registered names that
// make the logging producers say something. They are looked up in the registry
// below rather than trusted, so this file fails loudly if either stops being
// what it is here for.
const (
	bf11DeprecatedAlias = "SHARKO_PORT"
	bf11HarnessSetting  = "SHARKO_E2E_IMAGE_TAG"
)

// diagnosticRun is what one drive of the startup diagnostics produced, kept
// per producer so a silent one can be named.
type diagnosticRun struct {
	parts map[string]string
	order []string
}

func (r diagnosticRun) all() string {
	var b strings.Builder
	for _, name := range r.order {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(r.parts[name])
		b.WriteString("\n")
	}
	return b.String()
}

func (r *diagnosticRun) add(name, text string) {
	if r.parts == nil {
		r.parts = map[string]string{}
	}
	r.parts[name] = text
	r.order = append(r.order, name)
}

// secretSettingNames is every setting the registry marks Secret, sorted.
func secretSettingNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, s := range Registry() {
		if s.Secret {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// runStartupDiagnostics drives every entry point that takes a setting value
// and produces text about it, in an environment built to make each of them
// speak.
func runStartupDiagnostics(t *testing.T, secretNames []string) diagnosticRun {
	t.Helper()

	// The values under test.
	for _, name := range secretNames {
		t.Setenv(name, bf11SecretSentinel)
	}
	// An unknown name carrying the name sentinel, with a secret-shaped value
	// on it too — this is the single pair that both fires the refusals and
	// carries both sentinels through them.
	t.Setenv(bf11UnknownSetting, bf11SecretSentinel)
	// A registered test-harness setting, so the unused-setting warning fires.
	t.Setenv(bf11HarnessSetting, bf11SecretSentinel)
	// A deprecated alias in use, so the deprecation warning fires. Its value
	// must be a usable port: this one is resolved for real.
	t.Setenv(bf11DeprecatedAlias, "9123")

	// Both once-guards are process-wide, so clear them or the warnings may
	// already have been consumed by another test in this package.
	resetForTest()
	resetUnknownForTest()

	var run diagnosticRun
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if err := Validate(); err != nil {
		run.add("Validate", err.Error())
	} else {
		run.add("Validate", "")
	}
	if err := ValidateEnvironment(); err != nil {
		run.add("ValidateEnvironment", err.Error())
	} else {
		run.add("ValidateEnvironment", "")
	}
	if err := CheckEnviron(os.Environ()); err != nil {
		run.add("CheckEnviron", err.Error())
	} else {
		run.add("CheckEnviron", "")
	}
	if err := CheckSetting(bf11UnknownSetting, bf11SecretSentinel); err != nil {
		run.add("CheckSetting", err.Error())
	} else {
		run.add("CheckSetting", "")
	}
	// CheckSetting over the real Secret names too. These are registered, so
	// they return nil — recorded anyway so the run shows every call made.
	var registered []string
	for _, name := range secretNames {
		if err := CheckSetting(name, bf11SecretSentinel); err != nil {
			registered = append(registered, err.Error())
		}
	}
	run.add("CheckSetting over the registered Secret settings", strings.Join(registered, "\n"))

	WarnDeprecated(logger)
	WarnUnusedSettings(logger)
	run.add("logged", buf.String())

	return run
}

func TestNoDiagnosticEchoesASecretSettingValue(t *testing.T) {
	secretNames := secretSettingNames(t)

	// Not vacuous, and not stale. Zero secret settings would make everything
	// below true and empty; the named one is the setting BF11 is about, and
	// if it stops being marked Secret this test says so instead of quietly
	// covering one fewer thing.
	if len(secretNames) == 0 {
		t.Fatal("no setting in the registry is marked Secret, so this test planted nothing and read nothing")
	}
	var haveBootstrap bool
	for _, n := range secretNames {
		if n == "SHARKO_BOOTSTRAP_ADMIN_PASSWORD" {
			haveBootstrap = true
		}
	}
	if !haveBootstrap {
		t.Fatalf("SHARKO_BOOTSTRAP_ADMIN_PASSWORD is no longer marked Secret in the registry. The "+
			"settings this test covers are %v.", secretNames)
	}

	// The two registered names this run leans on must still be what they are
	// here for, or the producers they drive stay silent and the run below
	// would read less than it thinks.
	if s, ok := Get(bf11DeprecatedAlias); !ok || s.Kind != DeprecatedAlias {
		t.Fatalf("%s is no longer a deprecated alias in the registry, so the deprecation warning "+
			"cannot be made to fire and this test would stop reading it", bf11DeprecatedAlias)
	}
	if s, ok := Get(bf11HarnessSetting); !ok || s.Kind != TestHarness {
		t.Fatalf("%s is no longer a test-harness setting in the registry, so the unused-setting "+
			"warning cannot be made to fire and this test would stop reading it", bf11HarnessSetting)
	}
	if _, registered := Get(bf11UnknownSetting); registered {
		t.Fatalf("%s is now a registered setting, so it no longer drives the unknown-name refusals "+
			"this test reads. Pick another name the registry has never heard of.", bf11UnknownSetting)
	}

	run := runStartupDiagnostics(t, secretNames)
	all := run.all()

	// ------------------------------------------------------------------
	// Refuses to pass vacuously: every producer that can echo a value has
	// to have produced something to read.
	// ------------------------------------------------------------------
	for _, mustSpeak := range []string{"ValidateEnvironment", "CheckEnviron", "CheckSetting", "logged"} {
		if strings.TrimSpace(run.parts[mustSpeak]) == "" {
			t.Fatalf("%s produced nothing, so this test examined no diagnostic output from it and a "+
				"clean result below would mean nothing. What the whole run produced:\n%s", mustSpeak, all)
		}
	}
	// And the two logged warnings specifically — "logged" being non-empty is
	// not the same as both warnings having fired.
	for _, mustBeLogged := range []string{
		"deprecated configuration setting is set",
		"configuration setting is set but the server never reads it",
	} {
		if !strings.Contains(run.parts["logged"], mustBeLogged) {
			t.Fatalf("the startup warnings did not include %q, so that producer was never exercised "+
				"and this test is not reading it. What was logged:\n%s", mustBeLogged, run.parts["logged"])
		}
	}

	// ------------------------------------------------------------------
	// Positive control, planted into the REAL producers and read back
	// through the SAME collection the clean claim below rests on.
	//
	// These functions are supposed to print a setting's name. The unknown
	// setting's name carries bf11NameSentinel, so it MUST come back out of
	// each refusal. If it does not, the reader is not reading the producers
	// and no absence it reports means anything.
	// ------------------------------------------------------------------
	for _, mustCarryTheName := range []string{"ValidateEnvironment", "CheckEnviron", "CheckSetting"} {
		if !strings.Contains(run.parts[mustCarryTheName], bf11NameSentinel) {
			t.Fatalf("positive control failed: %s does not name the unknown setting it was given, so "+
				"this reader is not seeing what these functions produce and cannot claim anything is "+
				"absent from it. It produced:\n%s", mustCarryTheName, run.parts[mustCarryTheName])
		}
	}
	if !strings.Contains(all, bf11NameSentinel) {
		t.Fatalf("positive control failed: the joined text does not carry the planted name, so the "+
			"search below is running over something other than what the producers wrote:\n%s", all)
	}

	// ------------------------------------------------------------------
	// The claim.
	// ------------------------------------------------------------------
	if strings.Contains(all, bf11SecretSentinel) {
		t.Errorf("a startup diagnostic echoed the value of a setting the registry marks Secret. "+
			"What was produced:\n%s", all)
	}
	// No partial either — half a credential is still a credential.
	for size := 8; size < len(bf11SecretSentinel); size += 4 {
		if strings.Contains(all, bf11SecretSentinel[:size]) {
			t.Errorf("a startup diagnostic echoed the first %d characters of a secret setting's value", size)
		}
	}
}
