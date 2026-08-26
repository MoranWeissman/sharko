package envreg

// unknown_test.go — the startup rules, each one driven to a failure and
// back.
//
// Every test here is written so that deleting the thing it protects
// makes it fail. That is the point of the round: a guard nobody has
// watched break is a guard nobody has checked.
//
// The tests do NOT use t.Setenv and do NOT touch the real process
// environment. They hand CheckEnviron a slice, which is exactly the
// shape os.Environ returns, so a developer with SHARKO_ names exported
// in their shell cannot change the result — and neither can another
// test running beside this one.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// env builds an os.Environ-shaped slice, with the ordinary variables a
// real process carries so the tests exercise the "not our business"
// path too.
func env(pairs ...string) []string {
	return append([]string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/someone",
		"KUBERNETES_SERVICE_HOST=10.96.0.1",
		// The chart really sets these four, and they carry no SHARKO_
		// prefix. The rule must leave them alone — see the scope note in
		// unknown.go.
		"CONNECTION_SECRET_NAME=sharko-connections",
		"ARGOCD_NAMESPACE=argocd",
		"APP_ENVIRONMENTS=prod,staging",
		"GITOPS_ACTIONS_ENABLED=true",
	}, pairs...)
}

// ---------------------------------------------------------------------
// 1. an unknown setting
// ---------------------------------------------------------------------

func TestStartup_AnUnknownSettingStopsTheServer(t *testing.T) {
	err := CheckEnviron(env("SHARKO_ENTIRELY_MADE_UP_KNOB=yes"))
	if err == nil {
		t.Fatal("an unregistered SHARKO_ name was accepted. This is the whole rule: a setting Sharko " +
			"does not recognise is a setting it is not applying, and accepting it in silence leaves " +
			"the operator sure they changed something they did not.")
	}
	if !strings.Contains(err.Error(), "SHARKO_ENTIRELY_MADE_UP_KNOB") {
		t.Errorf("the error does not name the variable, so the operator cannot tell which one is wrong: %v", err)
	}
	if strings.Contains(err.Error(), "yes") {
		t.Errorf("the error repeats the VALUE. Errors name settings and never values — configuration "+
			"values reach logs and issue trackers, and a rule that only holds for the settings "+
			"somebody remembered to mark secret is not a rule: %v", err)
	}
}

func TestStartup_TheRuleIgnoresNamesThatAreNotSharkos(t *testing.T) {
	// The stated scope: SHARKO_ and nothing else. The chart sets four
	// real, read, unregistered names without that prefix, and they stay
	// unguarded this round on purpose.
	if err := CheckEnviron(env("SOMETHING_ELSE_ENTIRELY=1", "AI_PROVIDER=ollama")); err != nil {
		t.Errorf("the rule reached past the SHARKO_ prefix: %v", err)
	}
}

func TestStartup_AWholeCleanEnvironmentPasses(t *testing.T) {
	// Every registered production name at once, so the rule is proved to
	// accept the real product and not merely to reject things.
	var all []string
	for _, s := range Registry() {
		if s.Kind == TestHarness {
			continue
		}
		value := s.Default
		if value == "" {
			value = "x"
		}
		if s.Name == LegacyHTTPPort {
			continue // the alias is its own test, below
		}
		all = append(all, s.Name+"="+value)
	}
	if len(all) < 60 {
		t.Fatalf("only %d registered names were exercised — the registry read is broken, not the rule", len(all))
	}
	if err := CheckEnviron(env(all...)); err != nil {
		t.Errorf("the shipped registry does not accept its own settings: %v", err)
	}
}

// ---------------------------------------------------------------------
// 2. a misspelling of a real setting
// ---------------------------------------------------------------------

func TestStartup_AMisspellingIsRefusedAndTheRealNameIsOffered(t *testing.T) {
	err := CheckEnviron(env("SHARKO_LOG_LEVL=debug"))
	if err == nil {
		t.Fatal("SHARKO_LOG_LEVL was accepted. A one-letter slip is the exact case this rule exists " +
			"for: the server runs on the default log level and nothing says why.")
	}
	if !strings.Contains(err.Error(), "SHARKO_LOG_LEVEL") {
		t.Errorf("the error does not offer the real name. A name is not a value, so saying which "+
			"setting was probably meant costs nothing and saves the search: %v", err)
	}
	if strings.Contains(err.Error(), "debug") {
		t.Errorf("the error repeats the value: %v", err)
	}
}

func TestStartup_ASuggestionIsOnlyMadeWhenItIsNear(t *testing.T) {
	// A wrong suggestion is worse than none: it sends somebody to change
	// a setting that was never the one they meant.
	if got := closestRegisteredName("SHARKO_LOG_LEVL"); got != "SHARKO_LOG_LEVEL" {
		t.Errorf("closest to SHARKO_LOG_LEVL is %q, want SHARKO_LOG_LEVEL", got)
	}
	if got := closestRegisteredName("SHARKO_QQQQQQQQQQQQQQQQ"); got != "" {
		t.Errorf("a name near nothing was matched to %q — the threshold is not doing its job", got)
	}
}

// ---------------------------------------------------------------------
// 3. a deprecated alias: accepted, and warned about
// ---------------------------------------------------------------------

func TestStartup_ADeprecatedAliasIsAcceptedAndWarnedAbout(t *testing.T) {
	if err := CheckEnviron(env("SHARKO_PORT=9090")); err != nil {
		t.Fatalf("the deprecated alias was refused. It was a published name, so it has to keep "+
			"working: %v", err)
	}

	// And the warning really happens, naming both settings and neither
	// value. Driven through the same Lookup path startup uses.
	resetForTest()
	t.Setenv(LegacyHTTPPort, "9090")
	if _, err := ResolveHTTPPort(8080); err != nil {
		t.Fatalf("resolving the port through the alias failed: %v", err)
	}
	var buf bytes.Buffer
	WarnDeprecated(slog.New(slog.NewTextHandler(&buf, nil)))
	logged := buf.String()
	if !strings.Contains(logged, LegacyHTTPPort) || !strings.Contains(logged, HTTPPort) {
		t.Errorf("the deprecation warning does not name both settings: %q", logged)
	}
	if strings.Contains(logged, "9090") {
		t.Errorf("the deprecation warning carries the value. The Deprecation type holds two names and "+
			"nothing else precisely so this cannot happen: %q", logged)
	}
}

// ---------------------------------------------------------------------
// 4. an alias and its canonical setting, disagreeing
// ---------------------------------------------------------------------

func TestStartup_AnAliasAndItsCanonicalSettingDisagreeing(t *testing.T) {
	t.Setenv(HTTPPort, "8080")
	t.Setenv(LegacyHTTPPort, "9090")

	_, err := ResolveHTTPPort(1234)
	if err == nil {
		t.Fatal("two contradictory instructions were resolved by picking one. Guessing is how an " +
			"operator ends up certain they changed a port they did not.")
	}
	if !strings.Contains(err.Error(), HTTPPort) || !strings.Contains(err.Error(), LegacyHTTPPort) {
		t.Errorf("the conflict error does not name both settings: %v", err)
	}
	for _, value := range []string{"8080", "9090"} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("the conflict error repeats %s: %v", value, err)
		}
	}

	// And the same two names set to the SAME value is not a conflict.
	t.Setenv(LegacyHTTPPort, "8080")
	if port, sameErr := ResolveHTTPPort(1234); sameErr != nil || port != 8080 {
		t.Errorf("both names set to the same port gave (%d, %v), want (8080, nil) — the rule is "+
			"refusing agreement, not disagreement", port, sameErr)
	}
}

// ---------------------------------------------------------------------
// 5. a port that is not a port
// ---------------------------------------------------------------------

func TestStartup_APortThatIsNotAPortStopsTheServer(t *testing.T) {
	for _, bad := range []string{"80x", "0", "65536", "-1", "http", "8080 8081"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(HTTPPort, bad)
			port, err := ResolveHTTPPort(8080)
			if err == nil {
				t.Fatalf("%q was accepted as a port and resolved to %d. fmt.Sscanf used to stop at the "+
					"first character it could not use and call that success, which is how 80x became 80.", bad, port)
			}
			if !strings.Contains(err.Error(), HTTPPort) {
				t.Errorf("the error does not name the setting that carried the bad value: %v", err)
			}
			if strings.Contains(err.Error(), bad) {
				t.Errorf("the error repeats the value: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------
// 6. a Pod that still has Kubernetes service links turned on
// ---------------------------------------------------------------------

// serviceLinkEnvironment is what kubelet writes into a Pod whose
// namespace holds a Service called sharko on port 80, with a named port
// "http". The chart turns this off; an operator running their own
// manifests still gets it, and that Pod must still start.
var serviceLinkEnvironment = []string{
	"SHARKO_SERVICE_HOST=10.96.35.88",
	"SHARKO_SERVICE_PORT=80",
	"SHARKO_SERVICE_PORT_HTTP=80",
	"SHARKO_PORT=tcp://10.96.35.88:80",
	"SHARKO_PORT_80_TCP=tcp://10.96.35.88:80",
	"SHARKO_PORT_80_TCP_ADDR=10.96.35.88",
	"SHARKO_PORT_80_TCP_PORT=80",
	"SHARKO_PORT_80_TCP_PROTO=tcp",
}

func TestStartup_APodWithServiceLinksStillOnStartsAnyway(t *testing.T) {
	if err := CheckEnviron(env(serviceLinkEnvironment...)); err != nil {
		t.Fatalf("a Pod with service links still enabled would not start: %v\n\n"+
			"Every one of those names is written by kubelet, not by an operator. Refusing them turns "+
			"a tightening of Sharko's own settings into a boot failure for anyone who writes their "+
			"own manifests.", err)
	}

	// And the port really is left alone rather than read as something.
	t.Setenv("SHARKO_PORT", "tcp://10.96.35.88:80")
	port, err := ResolveHTTPPort(8080)
	if err != nil {
		t.Fatalf("the injected SHARKO_PORT stopped the server: %v", err)
	}
	if port != 8080 {
		t.Errorf("the injected SHARKO_PORT was read as port %d — kubelet wrote it, no operator did", port)
	}
}

// TestServiceLinkShapesAreRecognisedByShapeNotByAList is the reason the
// rule above is safe to have.
//
// A list of names was rejected in the commit before this one, because
// the names come from the Service name and the list would be wrong for
// anyone installing under a different release name. This checks that
// what replaced it really is a shape: it accepts kubelet's naming with
// values kubelet writes, and refuses the same names holding anything
// else.
func TestServiceLinkShapesAreRecognisedByShapeNotByAList(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
		why         string
	}{
		{"SHARKO_SERVICE_HOST", "10.96.35.88", true, "the cluster IP kubelet writes"},
		{"SHARKO_SERVICE_PORT", "80", true, "a port number"},
		{"SHARKO_SERVICE_PORT_HTTP", "80", true, "a named port"},
		{"SHARKO_PORT_443_TCP", "tcp://10.96.35.88:443", true, "another port, same shape"},
		{"SHARKO_PORT_53_UDP_PROTO", "udp", true, "UDP is a Service protocol too"},
		{"SHARKO_PORT_9000_SCTP_ADDR", "10.96.35.88", true, "so is SCTP"},

		{"SHARKO_SERVICE_HOST", "please-log-me-in", false, "a person typed this; kubelet writes an IP"},
		{"SHARKO_SERVICE_PORT", "eighty", false, "a person typed this; kubelet writes digits"},
		{"SHARKO_PORT_80_TCP_PROTO", "carrier-pigeon", false, "not one of the three Service protocols"},
		{"SHARKO_SERVICE_ACCOUNT", "sharko", false, "not a shape kubelet produces at all"},
		{"SHARKO_PORTAL", "tcp://10.96.35.88:80", false, "a value that looks right on a name that is not"},
	} {
		if got := isServiceLinkInjection(tc.name, tc.value); got != tc.want {
			t.Errorf("%s: got %v, want %v (%s)", tc.name, got, tc.want, tc.why)
		}
	}

	// The shape only ever fires on a Service literally named sharko.
	// Install under any other release name and kubelet writes FOO_*,
	// which never reaches this rule at all.
	if err := CheckEnviron(env("FOO_SERVICE_HOST=10.96.35.88", "FOO_PORT=tcp://10.96.35.88:80")); err != nil {
		t.Errorf("names from a Service called foo reached the rule: %v", err)
	}
}

// ---------------------------------------------------------------------
// test-harness settings: warned about, never a refusal
// ---------------------------------------------------------------------

func TestStartup_ATestHarnessSettingWarnsAndDoesNotStopTheServer(t *testing.T) {
	resetUnknownForTest()

	err := CheckEnviron(env(
		"SHARKO_E2E_IMAGE_TAG=pr-1234",
		"SHARKO_E2E_GITEA_TOKEN=a-real-looking-token",
	))
	if err != nil {
		t.Fatalf("a registered test-harness name stopped the server: %v\n\n"+
			"These are spelled correctly and have real readers under tests/. Refusing them punishes a "+
			"contributor whose rig is exported in the same shell, over a name that is not a mistake.", err)
	}

	var buf bytes.Buffer
	WarnUnusedSettings(slog.New(slog.NewTextHandler(&buf, nil)))
	logged := buf.String()
	for _, name := range []string{"SHARKO_E2E_IMAGE_TAG", "SHARKO_E2E_GITEA_TOKEN"} {
		if !strings.Contains(logged, name) {
			t.Errorf("%s was set and the server never reads it, and nothing said so: %q", name, logged)
		}
	}
	if strings.Contains(logged, "a-real-looking-token") {
		t.Errorf("the warning carries the value, and one of these settings is an API token: %q", logged)
	}
}

func TestStartup_InternalSettingsAreAcceptedInSilence(t *testing.T) {
	resetUnknownForTest()

	// SHARKO_E2E_GIT_HOSTS_ALLOWLIST is read by production code and is
	// set on the live playground. Warning about it would be wrong.
	if err := CheckEnviron(env("SHARKO_DEV_MODE=true", "SHARKO_E2E_GIT_HOSTS_ALLOWLIST=gitfake.sharko.svc")); err != nil {
		t.Fatalf("an internal setting stopped the server: %v", err)
	}
	var buf bytes.Buffer
	WarnUnusedSettings(slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("an internal setting was warned about. Production code reads it, so it is used: %q", buf.String())
	}
}

// ---------------------------------------------------------------------
// the rule reports everything, and reads the real environment
// ---------------------------------------------------------------------

func TestStartup_EveryUnknownNameIsReportedAtOnce(t *testing.T) {
	err := CheckEnviron(env(
		"SHARKO_MADE_UP_ONE=a",
		"SHARKO_MADE_UP_TWO=b",
		"SHARKO_MADE_UP_THREE=c",
	))
	if err == nil {
		t.Fatal("three unknown names were accepted")
	}
	for _, name := range []string{"SHARKO_MADE_UP_ONE", "SHARKO_MADE_UP_TWO", "SHARKO_MADE_UP_THREE"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s is missing from the error. Reporting one at a time makes an operator restart "+
				"once per mistake: %v", name, err)
		}
	}
}

// TestValidateEnvironment_ReadsTheProcessEnvironment pins the wiring
// between the pure rule and the one call startup makes. Without it,
// ValidateEnvironment could be handed an empty slice forever and every
// test above would still pass.
func TestValidateEnvironment_ReadsTheProcessEnvironment(t *testing.T) {
	original := environFn
	t.Cleanup(func() { environFn = original })

	environFn = func() []string { return env("SHARKO_NOT_A_REAL_SETTING=1") }
	if err := ValidateEnvironment(); err == nil {
		t.Fatal("ValidateEnvironment accepted an environment CheckEnviron refuses — the two have " +
			"come apart, and the one startup calls is the one that is wrong")
	}

	environFn = func() []string { return env("SHARKO_LOG_LEVEL=debug") }
	if err := ValidateEnvironment(); err != nil {
		t.Fatalf("ValidateEnvironment refused a clean environment: %v", err)
	}
}
