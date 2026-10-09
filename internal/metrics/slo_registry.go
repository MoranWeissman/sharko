package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// SLO-surface metric family — one histogram (per-phase duration) + two
// counters (total + errors) per SLO path. Mirrors the V2-3 sprint plan
// metric naming (sharko_<path>_duration_seconds / _total / _errors_total)
// and the V2-1.2 phase IDs verbatim.
type sloFamily struct {
	duration *prometheus.HistogramVec // labels: phase
	total    *prometheus.CounterVec   // labels: code
	errors   *prometheus.CounterVec   // labels: code
}

// sloRegistry holds the custom Prometheus registry that exposes the four
// SLO surfaces. A separate registry (NOT the default global) keeps the
// SLO surfaces independently testable and lets us serve them alongside
// the legacy default-registry metrics through a MultiGatherer in
// internal/api/metrics.go.
//
// Construction is idempotent + lazy: the first call to sloRegistryOnce()
// builds the registry and the four families; subsequent calls return the
// same instances. Test code that needs to reset between runs can call
// ResetSLORegistryForTest.
type sloRegistryState struct {
	reg      *prometheus.Registry
	families map[string]*sloFamily
}

var (
	sloState     *sloRegistryState
	sloStateOnce sync.Once
	sloStateMu   sync.RWMutex
)

func ensureSLORegistry() *sloRegistryState {
	sloStateOnce.Do(func() {
		st := buildSLORegistry()
		sloStateMu.Lock()
		sloState = st
		sloStateMu.Unlock()
	})
	sloStateMu.RLock()
	defer sloStateMu.RUnlock()
	return sloState
}

func buildSLORegistry() *sloRegistryState {
	reg := prometheus.NewRegistry()
	families := make(map[string]*sloFamily, len(SLOPaths))

	for _, path := range SLOPaths {
		fam := &sloFamily{
			duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "sharko_" + path + "_duration_seconds",
				Help:    "Duration of the " + path + " SLO surface, partitioned by phase.",
				Buckets: bucketsForPath(path),
			}, []string{"phase"}),
			total: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "sharko_" + path + "_total",
				Help: "Total invocations of the " + path + " SLO surface, partitioned by code (HTTP status or domain code; empty for paths without a natural code).",
			}, []string{"code"}),
			errors: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "sharko_" + path + "_errors_total",
				Help: "Error invocations of the " + path + " SLO surface, partitioned by code (HTTP status or domain code; empty for paths without a natural code).",
			}, []string{"code"}),
		}
		reg.MustRegister(fam.duration, fam.total, fam.errors)
		families[path] = fam
	}

	return &sloRegistryState{reg: reg, families: families}
}

// SLORegistry returns the package-private prometheus.Registry that holds
// the SLO-surface metric families. The /metrics HTTP handler in
// internal/api/metrics.go composes this registry with the
// prometheus.DefaultGatherer via prometheus.Gatherers so a single scrape
// returns BOTH the legacy promauto-registered metrics AND the V2-3 SLO
// surfaces.
func SLORegistry() *prometheus.Registry {
	return ensureSLORegistry().reg
}

// familyFor returns the metric family for path, or nil if path is not a
// known SLO path. Callers MUST treat nil as a no-op so an instrumentation
// typo never panics in a hot handler path.
func familyFor(path string) *sloFamily {
	return ensureSLORegistry().families[path]
}

// ResetSLORegistryForTest replaces the SLO registry with a fresh one.
// This is for unit-test isolation only — production code MUST NOT call
// it. Concurrent calls during a test serialise; see slo_test.go.
func ResetSLORegistryForTest() {
	sloStateMu.Lock()
	defer sloStateMu.Unlock()
	sloState = buildSLORegistry()
	// Reset the once so future ensureSLORegistry calls treat the
	// freshly built state as the canonical one. Because we re-built
	// already, we just keep sloStateOnce as-is (sync.Once fires once
	// per process). The Lock above guarantees ordering with readers.
}
