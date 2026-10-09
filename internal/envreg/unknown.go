package envreg

// unknown.go — the rule that stops Sharko starting with a SHARKO_ setting
// it does not know.
//
// # What used to happen
//
// Nothing. A misspelled setting was accepted in silence. An operator who
// wrote SHARKO_LOG_LEVL got a server on the default log level and no hint
// that anything was wrong, and the same was true of every one of the
// sixty-odd names in the registry. The setting looked applied because
// nothing said otherwise.
//
// # Why this could not be done until now
//
// Kubernetes gives every Pod an environment variable for each Service in
// the namespace, named after that Service. Sharko's own Service is
// normally called sharko, so Sharko's Pod was handed seven SHARKO_ names
// nobody set. A plain "unknown name stops the server" rule would have
// stopped an ordinary install from booting.
//
// The commit before this one removed that blocker for the official
// install path: the chart sets enableServiceLinks: false, so those names
// are simply not there. Operators who write their own manifests still
// have them, which is what the second half of this file is about.
//
// # What is scoped in, and what is deliberately not
//
// The rule covers the SHARKO_ prefix and nothing else. The shipped chart
// also sets real, read names that do NOT carry that prefix —
// CONNECTION_SECRET_NAME, ARGOCD_NAMESPACE, APP_ENVIRONMENTS,
// GITOPS_ACTIONS_ENABLED and the AI_* family. Those are unguarded, on
// purpose: the registry only knows SHARKO_ names, so a rule over the
// whole environment would reject PATH, HOME and every variable the
// container runtime sets. Bringing the unprefixed names under a registry
// is its own piece of work, and pretending this rule covers them would
// be worse than saying plainly that it does not.
//
// There is no escape hatch — no SHARKO_ALLOW_UNKNOWN_SETTINGS, no skip
// list. A switch that turns the rule off is a switch that gets left on,
// and it would have to be a registered setting itself, which is its own
// joke. The way past this rule is to register the name.

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// SettingPrefix is the prefix that makes a name Sharko's business.
const SettingPrefix = "SHARKO_"

// ---------------------------------------------------------------------
// what Kubernetes writes, recognised by shape rather than by a list
// ---------------------------------------------------------------------

// The names kubelet derives for a Service, in the old Docker-link format
// (kubernetes.io/docs/concepts/services-networking/service, "Environment
// variables"). For a Service named sharko on port 80 that is:
//
//	SHARKO_SERVICE_HOST=10.96.35.88
//	SHARKO_SERVICE_PORT=80
//	SHARKO_SERVICE_PORT_HTTP=80
//	SHARKO_PORT=tcp://10.96.35.88:80
//	SHARKO_PORT_80_TCP=tcp://10.96.35.88:80
//	SHARKO_PORT_80_TCP_ADDR=10.96.35.88
//	SHARKO_PORT_80_TCP_PORT=80
//	SHARKO_PORT_80_TCP_PROTO=tcp
//
// SHARKO_PORT is not in the table below because it is a registered
// deprecated alias and never reaches this rule — httpport.go owns it.
//
// A LIST OF NAMES was rejected in the previous commit and is still
// rejected: the names come from the Service name, so a list would be
// wrong for anyone installing under a different release name. This is
// not that. It is kubelet's naming SHAPE, which does not change, and it
// only ever matches when the Service really is called sharko — install
// under the name foo and kubelet writes FOO_SERVICE_HOST, which this
// rule never sees because it does not start with SHARKO_.
//
// Both halves have to match. The name shape alone would let
// SHARKO_SERVICE_PORT=nonsense through; requiring the value kubelet
// really writes means a person who typed one of these names by hand
// still gets told.
var serviceLinkShapes = []struct {
	name  *regexp.Regexp
	value *regexp.Regexp
	what  string
}{
	{regexp.MustCompile(`^SHARKO_SERVICE_HOST$`), hostValueRe, "the Service's cluster IP"},
	{regexp.MustCompile(`^SHARKO_SERVICE_PORT(?:_[A-Z0-9]+)*$`), digitsValueRe, "a Service port number"},
	{regexp.MustCompile(`^SHARKO_PORT_[0-9]+_(?:TCP|UDP|SCTP)$`), serviceLinkValueRe, "a Service port URI"},
	{regexp.MustCompile(`^SHARKO_PORT_[0-9]+_(?:TCP|UDP|SCTP)_ADDR$`), hostValueRe, "a Service port address"},
	{regexp.MustCompile(`^SHARKO_PORT_[0-9]+_(?:TCP|UDP|SCTP)_PORT$`), digitsValueRe, "a Service port number"},
	{regexp.MustCompile(`^SHARKO_PORT_[0-9]+_(?:TCP|UDP|SCTP)_PROTO$`), protoValueRe, "a Service port protocol"},
}

var (
	// hostValueRe matches an IPv4 or IPv6 address — kubelet writes the
	// Service's cluster IP, never a name.
	hostValueRe = regexp.MustCompile(`^[0-9a-fA-F.:]+$`)
	// digitsValueRe matches a bare port number.
	digitsValueRe = regexp.MustCompile(`^[0-9]+$`)
	// protoValueRe matches the three protocols a Service port can have.
	protoValueRe = regexp.MustCompile(`^(?:tcp|udp|sctp)$`)
)

// isServiceLinkInjection says whether Kubernetes wrote this pair rather
// than a person.
func isServiceLinkInjection(name, value string) bool {
	for _, shape := range serviceLinkShapes {
		if shape.name.MatchString(name) && shape.value.MatchString(value) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// the rule
// ---------------------------------------------------------------------

// CheckSetting decides one name/value pair.
//
// It returns nil for a name that is not Sharko's business at all, for a
// registered name of any kind, and for a variable Kubernetes injected.
// It returns an error for a SHARKO_ name the registry has never heard
// of.
//
// The error names the variable and NEVER its value. A configuration
// value reaches logs, terminals and issue trackers, and a rule that
// holds only for the settings somebody remembered to mark secret is not
// a rule. When a registered name is close enough to be a plausible
// typo, the error says which — a name is not a value.
func CheckSetting(name, value string) error {
	if !strings.HasPrefix(name, SettingPrefix) {
		return nil
	}
	if _, registered := Get(name); registered {
		return nil
	}
	if isServiceLinkInjection(name, value) {
		return nil
	}
	if suggestion := closestRegisteredName(name); suggestion != "" {
		return fmt.Errorf(
			"%s is not a Sharko setting. Did you mean %s? Sharko has not started: a setting it does "+
				"not recognise is a setting it is not applying, and starting anyway would leave you "+
				"sure you had changed something you had not. Correct the name or remove it, then "+
				"start Sharko again",
			name, suggestion)
	}
	return fmt.Errorf(
		"%s is not a Sharko setting. Sharko has not started: a setting it does not recognise is a "+
			"setting it is not applying, and starting anyway would leave you sure you had changed "+
			"something you had not. Remove it, then start Sharko again",
		name)
}

// CheckEnviron applies the rule to a whole environment, in the shape
// os.Environ returns: one "NAME=value" string per variable.
//
// Every unknown name is reported, not just the first. An operator
// fixing a chart's extraEnv block should not have to restart six times
// to be told about six names.
//
// It also records every registered TestHarness name it finds, for
// WarnUnusedSettings to report once the logger exists. See that
// function for why those are a warning and not a refusal.
func CheckEnviron(environ []string) error {
	var unknown []string
	var harness []string

	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			// os.Environ does not produce these. A caller passing its
			// own slice might; a name with no value is still a name.
			name, value = entry, ""
		}
		if !strings.HasPrefix(name, SettingPrefix) {
			continue
		}
		if s, registered := Get(name); registered {
			if s.Kind == TestHarness {
				harness = append(harness, name)
			}
			continue
		}
		if err := CheckSetting(name, value); err != nil {
			unknown = append(unknown, err.Error())
		}
	}

	recordTestHarnessSettings(harness)

	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	if len(unknown) == 1 {
		return fmt.Errorf("%s", unknown[0])
	}
	return fmt.Errorf("%d settings are not Sharko settings:\n\n  %s\n\nSharko has not started",
		len(unknown), strings.Join(unknown, "\n  "))
}

// ValidateEnvironment applies the rule to this process's environment.
//
// It is called at startup, immediately after Validate and before
// anything reads a value, builds a client or opens a store.
func ValidateEnvironment() error {
	return CheckEnviron(environFn())
}

// environFn is the seam the tests drive. Package level rather than
// per-instance because this package has no instances: it is a registry
// and a set of functions over the one process environment. Tests that
// swap it must not run in parallel with each other, and the helper in
// the test file is what enforces that.
var environFn = os.Environ

// ---------------------------------------------------------------------
// did you mean
// ---------------------------------------------------------------------

// closestRegisteredName returns the registered name nearest to an
// unknown one, or "" when nothing is near enough to be worth saying.
//
// The threshold is deliberately tight. A suggestion that is wrong is
// worse than no suggestion: it sends somebody to change a setting that
// was never the one they meant.
func closestRegisteredName(name string) string {
	const maxDistance = 3

	best := ""
	bestDistance := maxDistance + 1
	for _, s := range Registry() { // sorted, so a tie resolves the same way every run
		d := editDistance(name, s.Name)
		if d < bestDistance {
			best, bestDistance = s.Name, d
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

// editDistance is Levenshtein distance, two rows at a time.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// ---------------------------------------------------------------------
// test-harness settings: a warning, and the reasoning for it
// ---------------------------------------------------------------------

var (
	harnessMu       sync.Mutex
	harnessSettings = map[string]bool{}
	harnessWarnOnce sync.Once
)

func recordTestHarnessSettings(names []string) {
	if len(names) == 0 {
		return
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	for _, n := range names {
		harnessSettings[n] = true
	}
}

// PendingTestHarnessSettings returns the TestHarness names found set in
// the environment, sorted.
func PendingTestHarnessSettings() []string {
	harnessMu.Lock()
	defer harnessMu.Unlock()
	out := make([]string, 0, len(harnessSettings))
	for n := range harnessSettings {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// WarnUnusedSettings says one line per registered TestHarness setting
// found in the environment, once for the life of the process. Each line
// names the setting and carries no value attribute at all — one of them
// is an API token.
//
// # Why a warning and not a refusal, and not silence
//
// A TestHarness name is a REGISTERED name with a real reader under
// tests/. It is not a misspelling, so the rule this file exists for —
// stop silently accepting a setting Sharko does not know — does not
// apply to it. Refusing to start would punish a contributor who has the
// e2e rig exported in the shell they also run the server from, over a
// name that is spelled correctly and does nothing.
//
// Silence is wrong for the opposite reason: the server never reads any
// of them, so an operator who set one on a real installation believing
// it configured something would go on believing it. Saying so once, by
// name, costs nothing and is the whole point of the ruling.
func WarnUnusedSettings(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	harnessWarnOnce.Do(func() {
		for _, name := range PendingTestHarnessSettings() {
			logger.Warn("configuration setting is set but the server never reads it",
				"setting", name,
				"read_by", "the end-to-end test harness only")
		}
	})
}

// resetUnknownForTest clears the recorded harness settings and the once.
// Test-only.
func resetUnknownForTest() {
	harnessMu.Lock()
	harnessSettings = map[string]bool{}
	harnessMu.Unlock()
	harnessWarnOnce = sync.Once{}
}
