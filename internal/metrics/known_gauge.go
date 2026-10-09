package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// knownOnlyGauge is a gauge that publishes NOTHING until somebody has
// actually measured the thing it describes.
//
// A plain prometheus.Gauge starts life at zero and is exposed on every
// scrape from the moment it is registered. For a gauge that is only ever
// written once Sharko has read something — the org's approved catalog, say
// — that opening zero is not a measurement. It is the absence of one,
// dressed up as a fact. An operator graphing it sees a confident flat line
// at zero and concludes the catalog is empty, when the truth is that
// Sharko has not looked yet.
//
// Prometheus already has a well-understood shape for "no measurement":
// a labelled collector with no children emits no series at all, and a
// query against it returns no data. Ten of Sharko's metrics behave that
// way today purely because they happen to carry labels. This type gives
// the same honest behaviour to a gauge that carries none.
//
// Describe() always reports the Desc, so the metric is still a first-class
// member of the registry — the contract checks in this package see it, the
// documentation can describe it, and a consumer naming it is not reported
// as naming something that does not exist. Only Collect() is conditional.
type knownOnlyGauge struct {
	desc *prometheus.Desc

	mu    sync.RWMutex
	known bool
	value float64
}

// newKnownOnlyGauge builds an unregistered knownOnlyGauge from the same
// options a promauto gauge takes.
func newKnownOnlyGauge(opts prometheus.GaugeOpts) *knownOnlyGauge {
	return &knownOnlyGauge{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
			opts.Help,
			nil,
			opts.ConstLabels,
		),
	}
}

// mustRegisterKnownOnlyGauge is the promauto-equivalent constructor: build
// it and put it in the default registry, panicking on a duplicate exactly
// as promauto does.
func mustRegisterKnownOnlyGauge(opts prometheus.GaugeOpts) *knownOnlyGauge {
	g := newKnownOnlyGauge(opts)
	prometheus.MustRegister(g)
	return g
}

// Describe reports the Desc unconditionally — see the type comment.
func (g *knownOnlyGauge) Describe(ch chan<- *prometheus.Desc) { ch <- g.desc }

// Collect emits the series only once a real value has been recorded.
func (g *knownOnlyGauge) Collect(ch chan<- prometheus.Metric) {
	g.mu.RLock()
	known, value := g.known, g.value
	g.mu.RUnlock()
	if !known {
		return
	}
	ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, value)
}

// Set records a measured value. From the first call onwards the series is
// published on every scrape, including when the measured value is zero —
// a measured zero is a fact and belongs on the graph.
func (g *knownOnlyGauge) Set(v float64) {
	g.mu.Lock()
	g.known = true
	g.value = v
	g.mu.Unlock()
}

// forgetForTest drops the recorded value so the collector goes back to
// publishing nothing. Test-only; production code never calls it.
func (g *knownOnlyGauge) forgetForTest() {
	g.mu.Lock()
	g.known = false
	g.value = 0
	g.mu.Unlock()
}
