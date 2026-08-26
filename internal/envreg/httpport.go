package envreg

// httpport.go — the one place the server's listen port is worked out.
//
// # What was wrong
//
// The port used to be read in cmd/sharko/serve.go like this:
//
//	if envPort := os.Getenv("SHARKO_PORT"); envPort != "" {
//		fmt.Sscanf(envPort, "%d", &port)
//	}
//
// with the error thrown away. fmt.Sscanf stops at the first character it
// cannot use and reports success for what it managed to read, so "80x"
// was silently accepted as 80. And a value with no leading digit at all
// left `port` untouched, so the server quietly listened on the default
// while the operator was sure they had changed it.
//
// # The other half of the problem
//
// SHARKO_PORT is not only Sharko's own setting. Kubernetes injects a
// variable of that exact name into Sharko's Pod, holding a URI like
// tcp://10.96.35.88:80 — see isServiceLinkValue in lookup.go. So the
// name cannot simply be tightened: doing that alone would turn an
// ordinary install's startup into a boot failure over a variable no
// operator ever set.
//
// # What happens now
//
// SHARKO_HTTP_PORT is the setting. SHARKO_PORT still works and warns.
// A value Kubernetes wrote is ignored without a word. Anything else that
// is not a whole number in range stops the server, naming the variable
// and never repeating what was in it.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// HTTPPort is the setting that says which TCP port the server
	// listens on.
	HTTPPort = "SHARKO_HTTP_PORT"

	// LegacyHTTPPort is the name SHARKO_HTTP_PORT replaced. It is
	// registered as a deprecated alias, so it still works and warns —
	// except when it holds a value Kubernetes wrote, which is ignored.
	LegacyHTTPPort = "SHARKO_PORT"

	// MinHTTPPort and MaxHTTPPort bound a TCP port number. Port 0 is
	// excluded on purpose: to the kernel it means "pick any free port",
	// which is never what an operator writing a config file wants, and
	// the resulting server would be listening somewhere nobody can
	// predict.
	MinHTTPPort = 1
	MaxHTTPPort = 65535
)

// ResolveHTTPPort works out which TCP port the server should listen on.
//
// fallback is what to use when the operator has said nothing — in
// practice the --port flag, which carries its own default.
//
// The rules, all of them:
//
//   - SHARKO_HTTP_PORT set to a whole number in 1..65535 → that port.
//   - SHARKO_HTTP_PORT unset or empty, SHARKO_PORT likewise → fallback.
//   - SHARKO_PORT holding a URI Kubernetes injected → ignored in
//     silence. Kubernetes wrote it; no operator did.
//   - SHARKO_PORT holding a whole number → that port, plus a one-line
//     deprecation warning at startup naming both settings.
//   - SHARKO_PORT holding anything else → the server does not start.
//   - both set to the same number → that port, with the deprecation
//     warning.
//   - both set to different numbers → the server does not start. The
//     two instructions contradict each other and guessing which one the
//     operator meant is how somebody ends up certain they changed a
//     port they did not.
//
// The error never contains the offending value, only the name of the
// setting that carried it. Configuration values reach logs and issue
// trackers, and a rule that holds only for the settings somebody
// remembered to mark secret is not a rule.
func ResolveHTTPPort(fallback int) (int, error) {
	canonicalValue, canonicalSet := os.LookupEnv(HTTPPort)
	legacyValue, legacySet := os.LookupEnv(LegacyHTTPPort)

	value, set, source, err := resolveAliasPair(
		HTTPPort, canonicalValue, canonicalSet,
		LegacyHTTPPort, legacyValue, legacySet)
	if err != nil {
		return 0, err
	}
	// Not set by anyone. An empty string is absence, not nonsense: an
	// unset Helm value renders as "", and the old code ignored it too.
	if !set || strings.TrimSpace(value) == "" {
		return fallback, nil
	}

	// strconv.ParseInt with an explicit base of 10, not fmt.Sscanf. It
	// refuses trailing rubbish instead of stopping at it, which is the
	// whole defect: "80x" is an error here, where Sscanf made it 80.
	// A leading 0 does not make it octal either.
	port, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	if parseErr != nil || port < MinHTTPPort || port > MaxHTTPPort {
		return 0, fmt.Errorf(
			"%s is not a usable TCP port: it must be a whole number between %d and %d. "+
				"Sharko has not started. Fix the value and start it again",
			source, MinHTTPPort, MaxHTTPPort)
	}
	return int(port), nil
}
