// Command perf-baseline-compare is the V2-1.4 CI regression gate AND
// the baselines refresh tool.
//
// MODES (selected via -mode flag, default "compare"):
//
//  - compare (DEFAULT): reads captured timings + canonical baselines
//    YAML, computes p50/p95/p99 per (path, phase), and exits non-zero
//    if any phase's measured p99 regresses by more than the configured
//    threshold (default 20%) over the recorded baseline p99. This is
//    the per-PR gate invoked by .github/workflows/perf-regression.yml.
//
//  - refresh: reads captured timings, computes p50/p95/p99 per
//    (path, phase), and REWRITES the canonical
//    docs/site/operator/perf-baselines.yaml in place. The companion
//    Markdown file (perf-baselines.md) is human-curated prose and is
//    NOT touched — operators update it (or not) as a follow-on commit
//    when the YAML refresh PR lands. The CI gate reads only the YAML;
//    the Markdown is a courtesy human-facing view. Invoked by
//    .github/workflows/perf-baseline-refresh.yml (workflow_dispatch
//    only — NEVER auto on merge: drift would be invisible).
//
// COMPARE-MODE input (file paths via flags):
//
//  1. A captured timing JSON-Lines file (produced by the perf harness
//     emitting PhaseTimer lines — see tests/e2e/harness/timing.go).
//     Each line shape:
//       {"path":"...", "phase":"...", "duration_ms": <float>, "iteration": <int>, "ts_ms": <int>}
//
//  2. The canonical baselines YAML file (docs/site/operator/perf-baselines.yaml)
//     produced by the V2-1.2 baselines run + maintained via the
//     baseline-refresh workflow.
//
// COMPARE-MODE exit codes:
//
//	0 — no regressions over the threshold
//	1 — input/parse error (operator failure, e.g. bad path)
//	2 — at least one regression over the threshold (gate fires)
//
// REFRESH-MODE exit codes:
//
//	0 — files rewritten successfully
//	1 — input/parse/write error
//
// Phases that produced zero samples in the captured run are treated as
// SKIPPED, not REGRESSED — the skip-graceful policy of the harness (e.g.
// cluster_registration skips entirely when kind is absent) MUST flow
// through here, otherwise developer-laptop runs of the gate would fail
// spuriously. CI's perf-regression workflow provisions kind so the
// skip-graceful exit path never triggers in CI.
//
// Phases that are present in the baselines YAML but absent from the
// captured timings cause a WARNING printed to stderr but do NOT fail the
// gate — same reason. Phases present in the captured timings but missing
// from the baselines YAML are treated as new measurements that need a
// baselines refresh; they print a WARNING + are skipped (NOT a gate
// failure).
//
// Usage:
//
//	perf-baseline-compare \
//	    -timings _dist/perf-timings.jsonl \
//	    -baselines docs/site/operator/perf-baselines.yaml \
//	    -threshold 0.20
//
// All flags have sensible defaults so a bare invocation works when run
// from the repo root.
//
// Output is intentionally human-readable (the CI log is the primary
// consumer) — one summary line per phase + a final verdict block. The
// shape is:
//
//	[OK]      cluster_registration / argocd_secret_created   baseline=916.86ms measured=873.21ms delta=-4.76%
//	[REGRESS] addon_cycle           / enable_dry_run          baseline=0.38ms   measured=0.51ms   delta=+34.21% (threshold +20.00%)
//	[SKIP]    catalog_scan          / catalog_load            no samples captured
//
// V2-1.4 acceptance: a synthetic-regression PR is rejected. The triage
// flow when this gate fires is documented in the V2-4 runbook epic and
// the PR-label escape hatch `skip-perf-gate` is honoured by the
// perf-regression workflow (NOT by this binary — the binary always
// computes the verdict; the workflow decides whether to run it).
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// exit codes — see package comment for semantics.
const (
	exitOK         = 0
	exitInputError = 1
	exitRegressed  = 2
)

// timingLine mirrors the JSON shape emitted by tests/e2e/harness/timing.go.
// Duplicated here (rather than imported) because that package is gated
// behind `//go:build e2e` so importing it from a regular binary fails.
// The contract is small and stable — adding fields to that struct is
// additive (encoding/json skips unknown JSON fields when unmarshalling
// into a stricter struct, and PhaseTimer emits a strict superset).
type timingLine struct {
	Path       string  `json:"path"`
	Phase      string  `json:"phase"`
	DurationMs float64 `json:"duration_ms"`
	// Iteration + Ts intentionally omitted — this binary does not need
	// them for the p50/p95/p99 computation. Keeping the struct narrow
	// avoids accidental dependency on timestamp ordering.
}

// baselinesFile is the on-disk shape of perf-baselines.yaml.
//
// Field tags match the YAML structure documented in that file's header
// comment; gopkg.in/yaml.v3 is the canonical Sharko YAML parser already
// in go.mod (used by every internal/models/*.go), so this introduces
// zero new module deps.
type baselinesFile struct {
	Environment baselineEnv               `yaml:"environment"`
	Paths       map[string]baselinePath   `yaml:"paths"`
}

type baselineEnv struct {
	Date          string `yaml:"date"`
	SharkoVersion string `yaml:"sharko_version"`
	Runner        string `yaml:"runner"`
	SampleCount   int    `yaml:"sample_count"`
}

type baselinePath struct {
	Phases map[string]baselinePhase `yaml:"phases"`
}

type baselinePhase struct {
	N     int     `yaml:"n"`
	P50Ms float64 `yaml:"p50_ms"`
	P95Ms float64 `yaml:"p95_ms"`
	P99Ms float64 `yaml:"p99_ms"`
	MinMs float64 `yaml:"min_ms"`
	MaxMs float64 `yaml:"max_ms"`
}

// measuredPhase is the per-phase p50/p95/p99 computed from the captured
// timings file. Mirrors baselinePhase but without the on-disk struct
// tags so the comparator can build it dynamically.
type measuredPhase struct {
	N     int
	P50Ms float64
	P95Ms float64
	P99Ms float64
	MinMs float64
	MaxMs float64
}

// verdict is the per-phase result of the gate. The CI log iterates a
// slice of these and prints one line each.
type verdict struct {
	Path        string
	Phase       string
	Status      string // OK, REGRESS, SKIP, NEW, MISSING
	BaselineP99 float64
	MeasuredP99 float64
	DeltaPct    float64 // (measured - baseline) / baseline * 100; 0 when SKIP/NEW/MISSING
	Note        string
}

func main() {
	var (
		mode          = flag.String("mode", "compare", "Mode: compare (default — PR gate) or refresh (rewrite baselines YAML)")
		timingsPath   = flag.String("timings", "_dist/perf-timings.jsonl", "Path to the captured PhaseTimer JSON-Lines file")
		baselinesPath = flag.String("baselines", "docs/site/operator/perf-baselines.yaml", "Path to the canonical baselines YAML file")
		threshold     = flag.Float64("threshold", 0.20, "Regression threshold as a fraction (0.20 = 20%); any phase whose measured p99 exceeds baseline p99 by more than this fires the gate")
		quiet         = flag.Bool("quiet", false, "Suppress OK + SKIP lines; only REGRESS + NEW + MISSING are printed")
		sharkoVersion = flag.String("sharko-version", "", "Sharko version string written into the YAML environment block (refresh mode only)")
		runnerLabel   = flag.String("runner", "", "Runner description written into the YAML environment block (refresh mode only)")
	)
	flag.Parse()

	switch *mode {
	case "compare":
		if err := run(*timingsPath, *baselinesPath, *threshold, *quiet); err != nil {
			var gate *gateFailedError
			if errors.As(err, &gate) {
				fmt.Fprintln(os.Stderr, gate.Error())
				os.Exit(exitRegressed)
			}
			fmt.Fprintln(os.Stderr, "perf-baseline-compare: "+err.Error())
			os.Exit(exitInputError)
		}
		os.Exit(exitOK)
	case "refresh":
		if err := refresh(*timingsPath, *baselinesPath, "", *sharkoVersion, *runnerLabel); err != nil {
			fmt.Fprintln(os.Stderr, "perf-baseline-compare refresh: "+err.Error())
			os.Exit(exitInputError)
		}
		os.Exit(exitOK)
	default:
		fmt.Fprintf(os.Stderr, "perf-baseline-compare: unknown mode %q (want compare|refresh)\n", *mode)
		os.Exit(exitInputError)
	}
}

// gateFailedError signals "the gate fires" — caller maps this to exit 2.
// Anything else is exit 1 (input/parse failure).
type gateFailedError struct {
	count int
}

func (g *gateFailedError) Error() string {
	if g.count == 1 {
		return "PERF REGRESSION: 1 phase exceeded the regression threshold"
	}
	return fmt.Sprintf("PERF REGRESSION: %d phases exceeded the regression threshold", g.count)
}

func run(timingsPath, baselinesPath string, threshold float64, quiet bool) error {
	if threshold < 0 {
		return fmt.Errorf("threshold must be >= 0, got %v", threshold)
	}

	baselines, err := loadBaselines(baselinesPath)
	if err != nil {
		return fmt.Errorf("load baselines %q: %w", baselinesPath, err)
	}

	measured, err := loadMeasured(timingsPath)
	if err != nil {
		return fmt.Errorf("load timings %q: %w", timingsPath, err)
	}

	verdicts := compare(baselines, measured, threshold)
	printReport(verdicts, threshold, quiet)

	regressedCount := 0
	for _, v := range verdicts {
		if v.Status == "REGRESS" {
			regressedCount++
		}
	}
	if regressedCount > 0 {
		return &gateFailedError{count: regressedCount}
	}
	return nil
}

// loadBaselines reads + parses the YAML baselines file.
func loadBaselines(path string) (*baselinesFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bf baselinesFile
	if err := yaml.Unmarshal(raw, &bf); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(bf.Paths) == 0 {
		return nil, errors.New("no paths in baselines file")
	}
	return &bf, nil
}

// loadMeasured reads the JSON-Lines timings file and computes
// p50/p95/p99 per (path, phase).
//
// Lines that don't parse, lines without a path/phase, and lines with
// non-positive duration are silently dropped — same robustness contract
// as harness.ComputeBaselines.
func loadMeasured(path string) (map[string]map[string]measuredPhase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// (path -> phase -> []durations) bucketing; quantiles are computed
	// after the full file is read so the sort can run once per bucket.
	buckets := make(map[string]map[string][]float64)
	scanner := bufio.NewScanner(f)
	// 1 MiB per line is plenty — timingLine JSON is < 200 bytes.
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
		if buckets[line.Path] == nil {
			buckets[line.Path] = make(map[string][]float64)
		}
		buckets[line.Path][line.Phase] = append(buckets[line.Path][line.Phase], line.DurationMs)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan timings: %w", err)
	}

	out := make(map[string]map[string]measuredPhase, len(buckets))
	for p, byPhase := range buckets {
		out[p] = make(map[string]measuredPhase, len(byPhase))
		for ph, samples := range byPhase {
			if len(samples) == 0 {
				out[p][ph] = measuredPhase{}
				continue
			}
			sort.Float64s(samples)
			out[p][ph] = measuredPhase{
				N:     len(samples),
				P50Ms: quantile(samples, 0.50),
				P95Ms: quantile(samples, 0.95),
				P99Ms: quantile(samples, 0.99),
				MinMs: samples[0],
				MaxMs: samples[len(samples)-1],
			}
		}
	}
	return out, nil
}

// quantile is the Type-7 linear-interpolation quantile (same shape as
// harness.quantile). Duplicated rather than imported because the
// harness package is build-tagged.
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
	pos := float64(n-1) * q
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// compare walks the baselines + measured maps and yields one verdict per
// (path, phase). Order is stable: alphabetical by path, then by phase.
//
// Status rules:
//   - SKIP    — phase in baselines but no samples captured (kind absent etc.)
//   - MISSING — phase in baselines but path entirely absent from captures
//                (functionally same as SKIP but flagged distinctly so
//                the operator can spot a workflow misconfig vs an
//                expected skip-graceful)
//   - OK      — measured p99 within threshold of baseline p99
//   - REGRESS — measured p99 exceeds baseline p99 by more than threshold
//   - NEW     — phase present in captures but missing from baselines
//                (does NOT fail the gate; needs baseline refresh)
func compare(bf *baselinesFile, measured map[string]map[string]measuredPhase, threshold float64) []verdict {
	var verdicts []verdict

	// 1) Walk every (path, phase) in the baselines — these are the
	//    SLO-bearing surfaces, so missing measurements get flagged.
	bPaths := sortedKeys(bf.Paths)
	for _, p := range bPaths {
		phases := bf.Paths[p].Phases
		phaseNames := sortedKeys(phases)
		measuredPath, hasPath := measured[p]
		for _, ph := range phaseNames {
			bp := phases[ph]
			if !hasPath {
				verdicts = append(verdicts, verdict{
					Path:        p,
					Phase:       ph,
					Status:      "MISSING",
					BaselineP99: bp.P99Ms,
					Note:        "no samples captured for this path (the harness may have skipped it — check the upstream run)",
				})
				continue
			}
			mp, ok := measuredPath[ph]
			if !ok || mp.N == 0 {
				verdicts = append(verdicts, verdict{
					Path:        p,
					Phase:       ph,
					Status:      "SKIP",
					BaselineP99: bp.P99Ms,
					Note:        "no samples captured for this phase",
				})
				continue
			}
			deltaPct := 0.0
			if bp.P99Ms > 0 {
				deltaPct = (mp.P99Ms - bp.P99Ms) / bp.P99Ms * 100.0
			}
			status := "OK"
			// Threshold is fractional (0.20 = 20%). DeltaPct is already
			// in percent space, so compare against threshold * 100.
			if mp.P99Ms > bp.P99Ms*(1.0+threshold) {
				status = "REGRESS"
			}
			verdicts = append(verdicts, verdict{
				Path:        p,
				Phase:       ph,
				Status:      status,
				BaselineP99: bp.P99Ms,
				MeasuredP99: mp.P99Ms,
				DeltaPct:    deltaPct,
			})
		}
	}

	// 2) Walk every (path, phase) in the captures that isn't in the
	//    baselines — these are NEW measurements that need a refresh
	//    PR before the gate can cover them.
	measuredPaths := sortedKeys(measured)
	for _, p := range measuredPaths {
		bPath, hasBaselinePath := bf.Paths[p]
		measuredPhases := sortedKeys(measured[p])
		for _, ph := range measuredPhases {
			if hasBaselinePath {
				if _, ok := bPath.Phases[ph]; ok {
					continue // already covered above
				}
			}
			mp := measured[p][ph]
			if mp.N == 0 {
				continue
			}
			verdicts = append(verdicts, verdict{
				Path:        p,
				Phase:       ph,
				Status:      "NEW",
				MeasuredP99: mp.P99Ms,
				Note:        "phase not in baselines — run the perf-baseline-refresh workflow to record it",
			})
		}
	}

	return verdicts
}

// sortedKeys is a tiny generic helper that returns the keys of a map
// in deterministic alphabetical order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printReport writes the human-readable verdict block to stdout.
//
// Format:
//
//	perf-baseline-compare (threshold +20.00% on p99)
//	[OK]      <path> / <phase>   baseline=<x>ms measured=<y>ms delta=<+/-z%>
//	[REGRESS] <path> / <phase>   baseline=<x>ms measured=<y>ms delta=<+z%> (exceeds threshold)
//	[SKIP]    <path> / <phase>   no samples captured
//	[NEW]     <path> / <phase>   measured=<y>ms (not in baselines)
//	[MISSING] <path> / <phase>   baseline=<x>ms (entire path absent from captures)
//	==========================================================================
//	OK=N REGRESS=N SKIP=N NEW=N MISSING=N
func printReport(verdicts []verdict, threshold float64, quiet bool) {
	fmt.Printf("perf-baseline-compare (threshold +%.2f%% on p99)\n", threshold*100.0)

	counts := map[string]int{}
	maxPath, maxPhase := 0, 0
	for _, v := range verdicts {
		if len(v.Path) > maxPath {
			maxPath = len(v.Path)
		}
		if len(v.Phase) > maxPhase {
			maxPhase = len(v.Phase)
		}
	}
	for _, v := range verdicts {
		counts[v.Status]++
		if quiet && (v.Status == "OK" || v.Status == "SKIP") {
			continue
		}
		switch v.Status {
		case "OK":
			fmt.Printf("[OK]      %-*s / %-*s baseline=%8.3fms measured=%8.3fms delta=%+6.2f%%\n",
				maxPath, v.Path, maxPhase, v.Phase, v.BaselineP99, v.MeasuredP99, v.DeltaPct)
		case "REGRESS":
			fmt.Printf("[REGRESS] %-*s / %-*s baseline=%8.3fms measured=%8.3fms delta=%+6.2f%% (exceeds threshold +%.2f%%)\n",
				maxPath, v.Path, maxPhase, v.Phase, v.BaselineP99, v.MeasuredP99, v.DeltaPct, threshold*100.0)
		case "SKIP":
			fmt.Printf("[SKIP]    %-*s / %-*s baseline=%8.3fms (no samples captured)\n",
				maxPath, v.Path, maxPhase, v.Phase, v.BaselineP99)
		case "NEW":
			fmt.Printf("[NEW]     %-*s / %-*s measured=%8.3fms (not in baselines — refresh required)\n",
				maxPath, v.Path, maxPhase, v.Phase, v.MeasuredP99)
		case "MISSING":
			fmt.Printf("[MISSING] %-*s / %-*s baseline=%8.3fms (entire path absent from captures)\n",
				maxPath, v.Path, maxPhase, v.Phase, v.BaselineP99)
		}
	}
	fmt.Println(strings.Repeat("=", 74))
	fmt.Printf("OK=%d REGRESS=%d SKIP=%d NEW=%d MISSING=%d\n",
		counts["OK"], counts["REGRESS"], counts["SKIP"], counts["NEW"], counts["MISSING"])
}

// Silence linter on the io import if reshuffled in the future.
var _ = io.EOF

// ---------------------------------------------------------------------------
// REFRESH MODE — regenerates perf-baselines.yaml from a captured
// timings file. Invoked by the perf-baseline-refresh workflow.
//
// The companion Markdown file (perf-baselines.md) is human-curated
// prose and is NOT touched by this mode — operators update it (or not)
// as a follow-on commit when the YAML refresh PR lands. The CI gate
// reads only the YAML; the Markdown is a courtesy human-facing view.
// This separation keeps the refresh's blast radius small + obvious
// (one machine-readable file, no fragile Markdown surgery).
// ---------------------------------------------------------------------------

// refresh reads the captured timings, computes per-phase quantiles, then
// rewrites the canonical YAML file in place.
//
// Behaviour:
//
//  - The existing baselines YAML is loaded first to preserve the
//    runner label + sharko version when the corresponding flags are
//    blank (so a local dry-run produces a sensible file). The leading
//    comment block in the YAML is preserved verbatim.
//
//  - The phase set in the YAML is the LOCKED canonical list (per
//    tests/e2e/harness/phases.go). Phases captured but not present in
//    the YAML are dropped silently — a NEW phase needs a code change
//    in phases.go and a corresponding initial entry in the YAML BEFORE
//    the refresh picks it up. This guards against a perf run on a
//    branch that added a phase silently introducing it.
//
//  - When a phase from the YAML has zero captures (e.g. the kind-backed
//    cluster_registration path skipped because the runner didn't have
//    docker), the EXISTING baseline numbers are preserved — refresh
//    must never wipe a baseline due to a skip-graceful condition.
func refresh(timingsPath, baselinesPath, _ string, sharkoVersion, runnerLabel string) error {
	measured, err := loadMeasured(timingsPath)
	if err != nil {
		return fmt.Errorf("load timings %q: %w", timingsPath, err)
	}

	existing, err := loadBaselines(baselinesPath)
	if err != nil {
		return fmt.Errorf("load existing baselines %q: %w", baselinesPath, err)
	}

	fresh := &baselinesFile{
		Environment: baselineEnv{
			Date:          today(),
			SharkoVersion: pick(sharkoVersion, existing.Environment.SharkoVersion),
			Runner:        pick(runnerLabel, existing.Environment.Runner),
			SampleCount:   existing.Environment.SampleCount,
		},
		Paths: make(map[string]baselinePath, len(existing.Paths)),
	}
	preserved := 0
	updated := 0
	for pathName, oldPath := range existing.Paths {
		newPhases := make(map[string]baselinePhase, len(oldPath.Phases))
		for phaseName, oldPhase := range oldPath.Phases {
			mp, ok := measured[pathName][phaseName]
			if !ok || mp.N == 0 {
				newPhases[phaseName] = oldPhase
				preserved++
				continue
			}
			newPhases[phaseName] = baselinePhase{
				N:     mp.N,
				P50Ms: mp.P50Ms,
				P95Ms: mp.P95Ms,
				P99Ms: mp.P99Ms,
				MinMs: mp.MinMs,
				MaxMs: mp.MaxMs,
			}
			updated++
		}
		fresh.Paths[pathName] = baselinePath{Phases: newPhases}
	}

	if err := writeBaselinesYAML(baselinesPath, fresh); err != nil {
		return fmt.Errorf("write yaml %q: %w", baselinesPath, err)
	}

	fmt.Printf("perf-baseline-compare refresh: wrote %s (updated=%d preserved=%d)\n",
		baselinesPath, updated, preserved)
	return nil
}

// pick returns flag if non-empty, otherwise fallback. Keeps the refresh
// CLI ergonomic: omit a flag and the existing value is retained.
func pick(flag, fallback string) string {
	if flag != "" {
		return flag
	}
	return fallback
}

// today returns YYYY-MM-DD. Wrapped so tests can stub via timeNow.
func today() string {
	return timeNow().Format("2006-01-02")
}

// timeNow is the clock source for today(). Overridable in tests for
// deterministic refresh-mode output.
var timeNow = time.Now

// writeBaselinesYAML rewrites the YAML file. The leading comment block
// is preserved (it's load-bearing — documents the locked schema +
// refresh discipline); only the data below it is replaced.
//
// Implementation: read the existing file, find the first non-comment
// non-blank line (the start of the data), preserve everything above
// it, then append the freshly-marshalled YAML body.
func writeBaselinesYAML(path string, bf *baselinesFile) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	header := extractYAMLHeader(existing)

	// Use an Encoder so we can pin indent=2 (yaml.v3 defaults to 4),
	// matching the hand-authored shape of the V2-1.4 seed YAML.
	var buf strings.Builder
	enc := yaml.NewEncoder(&yamlWriter{out: &buf})
	enc.SetIndent(2)
	if err := enc.Encode(bf); err != nil {
		_ = enc.Close()
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close yaml encoder: %w", err)
	}

	combined := []byte(header + buf.String())
	return os.WriteFile(path, combined, 0o644)
}

// yamlWriter is a tiny io.Writer adapter so the YAML encoder can write
// to a strings.Builder.
type yamlWriter struct{ out *strings.Builder }

func (w *yamlWriter) Write(p []byte) (int, error) {
	return w.out.Write(p)
}

// extractYAMLHeader returns the leading comment + blank-line block of
// the YAML file (everything before the first key). The returned string
// ends with a newline so the appended marshalled body starts on a fresh
// line.
func extractYAMLHeader(raw []byte) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			b.WriteString(line)
			continue
		}
		break
	}
	out := b.String()
	if out == "" || !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

