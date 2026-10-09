//go:build e2e

// V2-1.2 — in-house quantile + grouping helper that consumes the JSON
// lines emitted by PhaseTimer and yields p50/p95/p99 per (path, phase).
//
// Why in-house: per the V2-1 sprint plan's "no third-party deps
// unless absolutely required" stance, a ~30-line linear-interpolation
// quantile implementation is preferable to pulling montanaflynn/stats
// into go.mod for a single computation.
package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
)

// PhaseStats holds the computed quantile baseline for one (path,
// phase) pair across N iterations.
type PhaseStats struct {
	Path  string
	Phase string
	N     int
	P50Ms float64
	P95Ms float64
	P99Ms float64
	MinMs float64
	MaxMs float64
}

// PathStats groups PhaseStats by path in the order PhasesForPath
// returns them. Used by the docs generator + (eventually) the V2-1.4
// CI gate to render per-path tables.
type PathStats struct {
	Path   string
	Phases []PhaseStats
}

// ComputeBaselines parses JSON-Lines timing emissions from r and
// returns one PathStats per critical path in the order AllPaths
// returns them. Lines that do not parse, lines whose path/phase is
// not in the locked set (phases.go), and lines with zero or negative
// duration are silently dropped — the input may include unrelated
// log lines (the perf runner shares stderr with the harness's
// general slog output) and best-effort parsing keeps the helper
// robust to that.
//
// Returns an error only on read failure.
func ComputeBaselines(r io.Reader) ([]PathStats, error) {
	if r == nil {
		return nil, errors.New("ComputeBaselines: nil reader")
	}
	// Group durations by (path, phase). Order doesn't matter — we'll
	// re-walk AllPaths to produce a stable output order.
	durations := make(map[string]map[string][]float64)

	scanner := bufio.NewScanner(r)
	// 1 MiB per line — plenty of headroom for the tiny timingLine JSON.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || !strings.HasPrefix(raw, "{") {
			continue
		}
		var line timingLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			continue
		}
		if line.Path == "" || line.Phase == "" || line.DurationMs < 0 {
			continue
		}
		// Verify the path/phase pair is in the locked set so we don't
		// surface accidental emissions in the baselines table.
		valid := false
		for _, ph := range PhasesForPath(line.Path) {
			if ph == line.Phase {
				valid = true
				break
			}
		}
		if !valid {
			continue
		}
		if durations[line.Path] == nil {
			durations[line.Path] = make(map[string][]float64)
		}
		durations[line.Path][line.Phase] = append(
			durations[line.Path][line.Phase],
			line.DurationMs,
		)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	out := make([]PathStats, 0, len(AllPaths()))
	for _, path := range AllPaths() {
		phases := PhasesForPath(path)
		ps := PathStats{Path: path, Phases: make([]PhaseStats, 0, len(phases))}
		for _, ph := range phases {
			samples := durations[path][ph]
			if len(samples) == 0 {
				ps.Phases = append(ps.Phases, PhaseStats{Path: path, Phase: ph})
				continue
			}
			sort.Float64s(samples)
			ps.Phases = append(ps.Phases, PhaseStats{
				Path:  path,
				Phase: ph,
				N:     len(samples),
				P50Ms: quantile(samples, 0.50),
				P95Ms: quantile(samples, 0.95),
				P99Ms: quantile(samples, 0.99),
				MinMs: samples[0],
				MaxMs: samples[len(samples)-1],
			})
		}
		out = append(out, ps)
	}
	return out, nil
}

// quantile returns the q-th quantile of a SORTED slice using linear
// interpolation between adjacent ranks (Type-7 in Hyndman + Fan's
// taxonomy — the default in NumPy, R, and most stats packages).
//
// Returns 0 for empty input. Clamps q to [0,1].
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	// Type-7 rank: (n-1) * q
	pos := float64(n-1) * q
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
