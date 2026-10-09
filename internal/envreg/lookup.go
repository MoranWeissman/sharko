package envreg

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Deprecation records that a deprecated alias was used. It carries the
// two NAMES and nothing else — no value, not even a redacted one. A
// warning that prints "SHARKO_OLD=hunter2 is deprecated" leaks a
// credential into every log aggregator the moment somebody deprecates a
// secret-bearing name, and the honest fix is for the value never to be
// in the record at all.
type Deprecation struct {
	Alias     string
	Canonical string
}

// Lookup resolves one registered setting, following any deprecated alias
// that points at it.
//
// Rules, exactly:
//
//   - canonical set, alias unset            → the canonical value, no warning
//   - alias set, canonical unset            → the alias value, one deprecation
//   - both set to the SAME value            → that value, one deprecation
//   - both set to DIFFERENT values          → error; nothing is guessed
//   - neither set                           → the registered Default, present=false
//
// with one thing sitting in front of all five: an alias holding a value
// Kubernetes wrote rather than an operator is not configuration at all
// and is skipped before any of the rules above see it. See
// isServiceLinkValue.
//
// The conflict error names both settings and repeats neither value.
// Startup turns it into a boot failure, which is the point: silently
// preferring one of two contradictory instructions is how an operator
// ends up sure they changed something they did not.
//
// An unregistered name is an error. There is no "read anything" mode —
// that would put the registry back to being a description of the code
// instead of the thing that reads the environment.
func Lookup(name string) (value string, present bool, err error) {
	setting, ok := Get(name)
	if !ok {
		return "", false, fmt.Errorf("%s is not a registered configuration setting", name)
	}
	if setting.Kind == DeprecatedAlias {
		return "", false, fmt.Errorf("%s is a deprecated alias; look up %s instead", name, setting.AliasOf)
	}

	canonicalValue, canonicalSet := os.LookupEnv(name)

	for _, alias := range aliasesOf(name) {
		aliasValue, aliasSet := os.LookupEnv(alias.Name)
		canonicalValue, canonicalSet, _, err = resolveAliasPair(
			name, canonicalValue, canonicalSet,
			alias.Name, aliasValue, aliasSet)
		if err != nil {
			return "", false, err
		}
	}

	if !canonicalSet {
		return setting.Default, false, nil
	}
	return canonicalValue, true, nil
}

// resolveAliasPair applies the alias rules to ONE canonical/alias pair,
// and says which of the two names the winning value came from.
//
// This is the single implementation of those rules. Lookup drives it
// from the registry's alias table, for every setting. ResolveHTTPPort
// drives it from two reads it makes by name, because it has to tell the
// operator WHICH variable carried the bad value — and because a name
// that only ever appears in a data table is a name none of this
// package's guards can see. Two callers, one rule: writing the rule
// twice is how one copy quietly stops matching the other.
//
// source is the environment variable the returned value came from, and
// is empty when neither was set.
func resolveAliasPair(
	canonicalName, canonicalValue string, canonicalSet bool,
	aliasName, aliasValue string, aliasSet bool,
) (value string, set bool, source string, err error) {
	source = ""
	if canonicalSet {
		source = canonicalName
	}
	if !aliasSet {
		return canonicalValue, canonicalSet, source, nil
	}
	// Kubernetes wrote this one, not an operator. Not a value, not a
	// conflict, and emphatically not a deprecation to warn about —
	// warning would tell every ordinary install to stop setting a
	// variable it never set.
	if isServiceLinkValue(aliasValue) {
		return canonicalValue, canonicalSet, source, nil
	}
	if canonicalSet && aliasValue != canonicalValue {
		return "", false, "", fmt.Errorf(
			"configuration conflict: %s and its deprecated alias %s are both set, to different values; set only %s",
			canonicalName, aliasName, canonicalName)
	}
	recordDeprecation(Deprecation{Alias: aliasName, Canonical: canonicalName})
	if canonicalSet {
		return canonicalValue, true, canonicalName, nil
	}
	return aliasValue, true, aliasName, nil
}

// serviceLinkValueRe matches the value Kubernetes injects for a Service
// port when service links are on.
//
// kubelet gives every Pod a Docker-link-compatible set of variables named
// after each Service in the namespace. For a Service named `sharko` on
// port 80 that is, among others:
//
//	SHARKO_SERVICE_HOST=10.96.35.88
//	SHARKO_SERVICE_PORT=80
//	SHARKO_PORT=tcp://10.96.35.88:80
//	SHARKO_PORT_80_TCP=tcp://10.96.35.88:80
//
// (kubernetes.io/docs/concepts/services-networking/service, "Environment
// variables"). Sharko's own Service is called sharko in an ordinary
// install, so SHARKO_PORT lands in Sharko's own Pod holding a URI.
//
// The three schemes are the three protocols a Service port can have —
// TCP, UDP and SCTP. Nothing an operator would ever type into a port
// setting looks like this, which is what makes ignoring it safe.
var serviceLinkValueRe = regexp.MustCompile(`^(?:tcp|udp|sctp)://[^\s/]+:[0-9]+$`)

// isServiceLinkValue says whether a value was written by Kubernetes'
// service-link injection rather than by a person.
//
// The chart turns service links off (charts/sharko/templates/
// deployment.yaml, enableServiceLinks: false), so on the official install
// path these variables are not there at all. This is the second line:
// operators who maintain their own manifests, and older Pods created
// before the chart change, still have them.
func isServiceLinkValue(v string) bool {
	return serviceLinkValueRe.MatchString(v)
}

func aliasesOf(canonical string) []Setting {
	var out []Setting
	for _, s := range settings {
		if s.Kind == DeprecatedAlias && s.AliasOf == canonical {
			out = append(out, s)
		}
	}
	sortByName(out)
	return out
}

// ---------------------------------------------------------------------
// startup
// ---------------------------------------------------------------------

var (
	deprecationMu   sync.Mutex
	deprecationsHit = map[Deprecation]bool{}
	warnOnce        sync.Once
)

func recordDeprecation(d Deprecation) {
	deprecationMu.Lock()
	defer deprecationMu.Unlock()
	deprecationsHit[d] = true
}

// Validate is called once at startup, before anything else reads the
// environment. It fails the boot on two things:
//
//   - a registry that contradicts itself (a duplicate name, an alias
//     pointing at a setting that is not a registered Production setting,
//     an entry with no summary or no reader);
//   - a canonical setting and its deprecated alias set to different
//     values.
//
// It also resolves every alias in use, so WarnDeprecated has something to
// say without any caller having to remember to collect it.
//
// It does NOT look at the environment for names it has never heard of.
// That is ValidateEnvironment, in unknown.go, and it is a separate call
// on purpose: this one is a question about the registry and gives the
// same answer whatever is exported, which is what lets the guards in
// registry_test.go run it without the developer's shell deciding the
// result. Startup makes both calls, one after the other.
func Validate() error {
	seen := map[string]bool{}
	for _, s := range settings {
		if seen[s.Name] {
			return fmt.Errorf("configuration registry is broken: %s is registered twice", s.Name)
		}
		seen[s.Name] = true

		if !strings.HasPrefix(s.Name, "SHARKO_") {
			return fmt.Errorf("configuration registry is broken: %s is not a SHARKO_ name", s.Name)
		}
		if strings.TrimSpace(s.Summary) == "" {
			return fmt.Errorf("configuration registry is broken: %s has no summary", s.Name)
		}
		if strings.TrimSpace(s.ReaderFile) == "" {
			return fmt.Errorf("configuration registry is broken: %s names no reader", s.Name)
		}
		switch s.Kind {
		case Production, TestHarness, Internal:
			if s.AliasOf != "" {
				return fmt.Errorf("configuration registry is broken: %s is %s but names an alias target", s.Name, s.Kind)
			}
		case DeprecatedAlias:
			// The structural guard on the alias kind: an alias whose
			// canonical setting is not itself a resolver-routed
			// Production setting is a promise the resolver cannot keep.
			target, ok := Get(s.AliasOf)
			if !ok {
				return fmt.Errorf("configuration registry is broken: %s aliases %s, which is not registered", s.Name, s.AliasOf)
			}
			if target.Kind != Production {
				return fmt.Errorf("configuration registry is broken: %s aliases %s, which is %s and not a production setting", s.Name, s.AliasOf, target.Kind)
			}
		default:
			return fmt.Errorf("configuration registry is broken: %s has unknown kind %q", s.Name, s.Kind)
		}
	}

	for _, s := range settings {
		if s.Kind == DeprecatedAlias {
			continue
		}
		if _, _, err := Lookup(s.Name); err != nil {
			return err
		}
	}
	return nil
}

// PendingDeprecations returns the deprecated aliases found in use, sorted.
func PendingDeprecations() []Deprecation {
	deprecationMu.Lock()
	defer deprecationMu.Unlock()
	out := make([]Deprecation, 0, len(deprecationsHit))
	for d := range deprecationsHit {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// WarnDeprecated emits one warning per deprecated alias in use, once for
// the life of the process. Each record names the setting and the name to
// use instead, and carries no value attribute at all.
func WarnDeprecated(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	warnOnce.Do(func() {
		for _, d := range PendingDeprecations() {
			logger.Warn("deprecated configuration setting is set",
				"setting", d.Alias,
				"use_instead", d.Canonical)
		}
	})
}

// resetForTest clears the recorded deprecations and the once. Test-only.
func resetForTest() {
	deprecationMu.Lock()
	deprecationsHit = map[Deprecation]bool{}
	deprecationMu.Unlock()
	warnOnce = sync.Once{}
}
