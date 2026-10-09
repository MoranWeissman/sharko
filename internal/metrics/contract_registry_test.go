package metrics

// contract_registry_test.go — the authoritative answer to "which metric
// series does Sharko actually publish?".
//
// # Why Describe() and nothing else
//
// Three tempting sources are all wrong:
//
//   - A live /metrics scrape. A *Vec with no observed label combination
//     emits nothing at all — not even its HELP line. Scraping the running
//     pod returns 12 families out of 44, so a scrape-based check would
//     condemn 32 real metrics.
//   - A grep for string literals in internal/metrics. Twelve of the names
//     do not exist as literals anywhere: slo_registry.go assembles them at
//     runtime as "sharko_" + path + "_duration_seconds" from SLOPaths. A
//     literal grep misses all twelve and then reports the shipped alert
//     rules that use them as broken.
//   - A hand-maintained list in a test. That is what TestMetricsRegistered
//     was, and it asserted only that the number 25 had not changed.
//
// Describe() is the only source that reports a zero-child *Vec correctly
// and the only one that sees a runtime-assembled name. Both registries the
// /metrics handler composes (internal/api/metrics.go) are described here:
// the promauto default registry and metrics.SLORegistry().
//
// # How the metric TYPE is discovered
//
// A *prometheus.Desc carries the name, the help and the label names — but
// not the metric type, and Prometheus derives _bucket/_count/_sum series
// from histograms only. Rather than keep a second hand-written list of
// "which of these are histograms" (a list that rots the moment somebody
// adds one), the type is read back out of the registry itself:
// registering a probe collector that describes an already-registered Desc
// returns prometheus.AlreadyRegisteredError, whose ExistingCollector is
// the real collector. Its Go type says histogram, summary, counter or
// gauge. The probe is rejected by the registry, so nothing is polluted.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// metricNamePrefix scopes the whole contract to Sharko's own metrics.
// go_*, process_* and anything a different exporter publishes are not
// Sharko's to promise.
const metricNamePrefix = "sharko_"

// scrapeTimeLabels are attached by Prometheus or by the service-discovery
// config, not by the Go collector, so a query may match on them even
// though no Desc declares them. Keeping this list short and explicit
// matters: every name added here is a label the check stops verifying.
var scrapeTimeLabels = map[string]bool{
	"job":                true,
	"instance":           true,
	"namespace":          true,
	"pod":                true,
	"container":          true,
	"service":            true,
	"endpoint":           true,
	"prometheus":         true,
	"prometheus_replica": true,
}

// seriesSpec is one time-series name that Sharko really publishes,
// together with every label a query is allowed to match on it.
type seriesSpec struct {
	// family is the registered metric family this series comes from.
	// For a derived histogram series it is the histogram's own name.
	family string
	// labels holds the permitted label names.
	labels map[string]bool
	// derived is true for the _bucket/_count/_sum series Prometheus
	// synthesises from a histogram or summary.
	derived bool
}

// descProbe describes exactly one already-known Desc and collects
// nothing. Registering it against the registry that owns that Desc is
// refused with AlreadyRegisteredError, which is how the real collector —
// and therefore the real metric type — is recovered.
type descProbe struct{ d *prometheus.Desc }

func (p descProbe) Describe(ch chan<- *prometheus.Desc) { ch <- p.d }
func (p descProbe) Collect(chan<- prometheus.Metric)    {}

// describeCollector drains Describe() into a slice.
func describeCollector(c prometheus.Collector) []*prometheus.Desc {
	ch := make(chan *prometheus.Desc, 1024)
	go func() {
		c.Describe(ch)
		close(ch)
	}()
	var out []*prometheus.Desc
	for d := range ch {
		out = append(out, d)
	}
	return out
}

// parseDesc pulls the fully-qualified name and the label names out of a
// Desc. Desc keeps both unexported and exposes them only through
// String(), which is a stable, documented format:
//
//	Desc{fqName: "x", help: "y", constLabels: {a="b"}, variableLabels: {p,q}}
//
// A parse failure is fatal on purpose. Returning an empty label set on a
// malformed string would silently turn every label check into a pass,
// which is precisely the shape of failure this whole round is about.
func parseDesc(t *testing.T, d *prometheus.Desc) (string, map[string]bool) {
	t.Helper()
	s := d.String()

	const namePrefix = `Desc{fqName: `
	if !strings.HasPrefix(s, namePrefix) {
		t.Fatalf("Desc.String() format changed, the contract check cannot read it: %s", s)
	}
	quoted, err := strconv.QuotedPrefix(s[len(namePrefix):])
	if err != nil {
		t.Fatalf("Desc.String() fqName is not a quoted string: %s", s)
	}
	name, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("Desc.String() fqName does not unquote: %s", s)
	}

	labels := map[string]bool{}

	const constMarker = `constLabels: {`
	const varMarker = `variableLabels: {`
	ci := strings.Index(s, constMarker)
	vi := strings.LastIndex(s, varMarker)
	if ci < 0 || vi < 0 || vi < ci {
		t.Fatalf("Desc.String() format changed, the contract check cannot read it: %s", s)
	}
	constPart := strings.TrimSuffix(strings.TrimSpace(s[ci+len(constMarker):vi]), varMarker)
	constPart = strings.TrimSpace(constPart)
	constPart = strings.TrimSuffix(constPart, ",")
	constPart = strings.TrimSuffix(constPart, "}")
	for _, kv := range strings.Split(constPart, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		if eq := strings.Index(kv, "="); eq > 0 {
			labels[kv[:eq]] = true
		}
	}

	varPart := strings.TrimSuffix(s[vi+len(varMarker):], "}}")
	for _, l := range strings.Split(varPart, ",") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// A constrained label is printed as c(name).
		l = strings.TrimSuffix(strings.TrimPrefix(l, "c("), ")")
		labels[l] = true
	}
	return name, labels
}

// collectorGoTypeFor returns the Go type name of the collector that owns
// desc inside reg. See the file comment for why this works.
func collectorGoTypeFor(reg prometheus.Registerer, desc *prometheus.Desc) (string, error) {
	probe := descProbe{d: desc}
	err := reg.Register(probe)
	if err == nil {
		// It was not registered after all — undo, and say so loudly
		// rather than guessing a type.
		reg.Unregister(probe)
		return "", fmt.Errorf("%s: probe registered cleanly, so the desc was not in this registry", desc)
	}
	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		return fmt.Sprintf("%T", already.ExistingCollector), nil
	}
	return "", fmt.Errorf("%s: unexpected register result: %w", desc, err)
}

// authoritativeSeries returns every sharko_ time-series name the product
// can publish, mapped to the labels a query may match on it.
func authoritativeSeries(t *testing.T) map[string]seriesSpec {
	t.Helper()

	// Both registries the /metrics handler composes
	// (internal/api/metrics.go metricsHandler).
	defaultReg, ok := prometheus.DefaultRegisterer.(prometheus.Collector)
	if !ok {
		t.Fatalf("prometheus.DefaultRegisterer is not a Collector (%T) — the contract check cannot describe it", prometheus.DefaultRegisterer)
	}
	sources := []struct {
		name string
		reg  prometheus.Registerer
		col  prometheus.Collector
	}{
		{"default registry", prometheus.DefaultRegisterer, defaultReg},
		{"SLO registry", SLORegistry(), SLORegistry()},
	}

	out := map[string]seriesSpec{}
	families := 0
	for _, src := range sources {
		for _, d := range describeCollector(src.col) {
			name, labels := parseDesc(t, d)
			if !strings.HasPrefix(name, metricNamePrefix) {
				continue
			}
			if _, dup := out[name]; dup {
				t.Fatalf("metric %s described twice — the contract check would compare against an ambiguous label set", name)
			}
			families++

			goType, err := collectorGoTypeFor(src.reg, d)
			if err != nil {
				t.Fatalf("%s: cannot determine the metric type: %v", src.name, err)
			}
			lower := strings.ToLower(goType)
			isHistogram := strings.Contains(lower, "histogram")
			isSummary := strings.Contains(lower, "summary")

			base := seriesSpec{family: name, labels: copyLabels(labels)}
			if isSummary {
				base.labels["quantile"] = true
			}
			out[name] = base

			if isHistogram || isSummary {
				// Prometheus derives these from the family itself.
				bucketLabels := copyLabels(labels)
				if isHistogram {
					bucketLabels["le"] = true
					out[name+"_bucket"] = seriesSpec{family: name, labels: bucketLabels, derived: true}
				}
				out[name+"_count"] = seriesSpec{family: name, labels: copyLabels(labels), derived: true}
				out[name+"_sum"] = seriesSpec{family: name, labels: copyLabels(labels), derived: true}
			}
		}
	}

	// A collapsed registry would make every consumer look broken (or,
	// worse in the label direction, make nothing look broken). Refuse to
	// run the contract on an obviously empty answer.
	if families < 40 {
		t.Fatalf("only %d sharko_ metric families described — the registry walk is broken, not the consumers", families)
	}
	return out
}

func copyLabels(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+2)
	for k := range in {
		out[k] = true
	}
	return out
}

// TestAuthoritativeSeries_IsBuiltFromTheRegistryNotFromLiterals is the
// guard on the guard. It proves the three properties the rest of the
// contract check leans on, so a future change that quietly breaks the
// registry walk fails here with a readable message instead of turning
// every downstream comparison into a silent pass.
func TestAuthoritativeSeries_IsBuiltFromTheRegistryNotFromLiterals(t *testing.T) {
	series := authoritativeSeries(t)

	// 1. The runtime-assembled SLO names are present. These are the
	//    twelve names that exist as no string literal anywhere.
	for _, path := range SLOPaths {
		for _, suffix := range []string{"_duration_seconds", "_total", "_errors_total"} {
			name := "sharko_" + path + suffix
			if _, ok := series[name]; !ok {
				t.Errorf("SLO series %s is missing — the registry walk is not seeing metrics.SLORegistry()", name)
			}
		}
	}

	// 2. Histogram-derived series are synthesised, with le on _bucket.
	//    sharko_api_request_duration_seconds is a HistogramVec that has
	//    never been observed in this test binary, so it is exactly the
	//    case a live scrape would miss.
	const hist = "sharko_api_request_duration_seconds"
	if _, ok := series[hist]; !ok {
		t.Fatalf("%s missing from the authoritative set", hist)
	}
	bucket, ok := series[hist+"_bucket"]
	if !ok {
		t.Fatalf("%s_bucket missing — histogram-derived series are not being synthesised", hist)
	}
	if !bucket.labels["le"] {
		t.Errorf("%s_bucket does not permit the le label", hist)
	}
	for _, suffix := range []string{"_count", "_sum"} {
		if _, ok := series[hist+suffix]; !ok {
			t.Errorf("%s%s missing — histogram-derived series are not being synthesised", hist, suffix)
		}
	}

	// 3. Variable labels are captured, and a label the metric does not
	//    have is genuinely absent. This is the property that catches
	//    sharko_catalog_source_entries{verified="false"}.
	entries, ok := series["sharko_catalog_source_entries"]
	if !ok {
		t.Fatalf("sharko_catalog_source_entries missing from the authoritative set")
	}
	if !entries.labels["url"] {
		t.Errorf("sharko_catalog_source_entries should carry the url label; got %v", sortedLabels(entries.labels))
	}
	if entries.labels["verified"] {
		t.Errorf("sharko_catalog_source_entries must NOT carry a verified label — the label check would be worthless")
	}

	// 4. A counter does not get histogram-derived series invented for it.
	if _, ok := series["sharko_api_requests_total_bucket"]; ok {
		t.Errorf("sharko_api_requests_total is a counter; _bucket must not be synthesised for it")
	}
}

func sortedLabels(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
