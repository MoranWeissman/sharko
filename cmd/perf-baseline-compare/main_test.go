// V2-1.4 — tests for the perf-baseline-compare comparator.
//
// Covers the five verdict statuses (OK / REGRESS / SKIP / NEW / MISSING)
// and the threshold boundary (exactly +20% must be OK; +20.01% must
// REGRESS). Tests are pure — no filesystem, no subprocess — so they
// stay fast and run in the normal `go test ./...` lane.

package main

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeBaselines builds a baselinesFile with one path + three phases so
// every status code can be exercised against a single input.
func fakeBaselines() *baselinesFile {
	return &baselinesFile{
		Environment: baselineEnv{
			Date:          "2026-05-26",
			SharkoVersion: "1.25.0-pre.0",
			SampleCount:   30,
		},
		Paths: map[string]baselinePath{
			"addon_cycle": {
				Phases: map[string]baselinePhase{
					"enable_dry_run": {N: 30, P99Ms: 1.000},
					"disable_dry_run": {N: 30, P99Ms: 2.000},
					"upgrade_global":  {N: 30, P99Ms: 0.500},
				},
			},
			"cluster_registration": {
				Phases: map[string]baselinePhase{
					"ui_submit": {N: 30, P99Ms: 100.000},
				},
			},
		},
	}
}

func TestCompare_OK_WithinThreshold(t *testing.T) {
	bf := fakeBaselines()
	// enable_dry_run baseline p99 = 1.0ms; +20% = 1.2ms. Measured 1.15ms
	// (under the bar) → OK.
	measured := map[string]map[string]measuredPhase{
		"addon_cycle": {
			"enable_dry_run":  {N: 5, P99Ms: 1.150},
			"disable_dry_run": {N: 5, P99Ms: 1.800},
			"upgrade_global":  {N: 5, P99Ms: 0.400},
		},
		"cluster_registration": {
			"ui_submit": {N: 5, P99Ms: 95.0},
		},
	}
	verdicts := compare(bf, measured, 0.20)
	for _, v := range verdicts {
		if v.Status != "OK" {
			t.Fatalf("expected all OK, got %+v", v)
		}
	}
	if len(verdicts) != 4 {
		t.Fatalf("expected 4 verdicts (one per phase), got %d", len(verdicts))
	}
}

func TestCompare_REGRESS_ExceedsThreshold(t *testing.T) {
	bf := fakeBaselines()
	// upgrade_global baseline p99 = 0.5ms; +20% = 0.6ms. Measured 0.7ms
	// → REGRESS.
	measured := map[string]map[string]measuredPhase{
		"addon_cycle": {
			"enable_dry_run":  {N: 5, P99Ms: 0.9}, // under baseline → OK
			"disable_dry_run": {N: 5, P99Ms: 1.5}, // under baseline → OK
			"upgrade_global":  {N: 5, P99Ms: 0.7}, // +40% → REGRESS
		},
		"cluster_registration": {
			"ui_submit": {N: 5, P99Ms: 90.0},
		},
	}
	verdicts := compare(bf, measured, 0.20)
	var regressed []verdict
	for _, v := range verdicts {
		if v.Status == "REGRESS" {
			regressed = append(regressed, v)
		}
	}
	if len(regressed) != 1 {
		t.Fatalf("expected 1 REGRESS, got %d (verdicts: %+v)", len(regressed), verdicts)
	}
	if regressed[0].Phase != "upgrade_global" {
		t.Fatalf("expected upgrade_global to regress, got %s", regressed[0].Phase)
	}
	if math.Abs(regressed[0].DeltaPct-40.0) > 0.001 {
		t.Fatalf("expected ~+40%% delta, got %+v", regressed[0].DeltaPct)
	}
}

func TestCompare_ThresholdBoundary(t *testing.T) {
	bf := &baselinesFile{
		Paths: map[string]baselinePath{
			"p": {Phases: map[string]baselinePhase{"ph": {P99Ms: 100.0}}},
		},
	}
	cases := []struct {
		name     string
		measured float64
		want     string
	}{
		// Exactly +20% is the boundary; comparator uses strict `>`, so
		// +20.000% is OK and +20.001% trips REGRESS. This codifies the
		// boundary so a future refactor can't silently flip it.
		{"exact +20%", 120.0, "OK"},
		{"+19.9%", 119.9, "OK"},
		{"+20.001%", 120.001, "REGRESS"},
		{"-50% (improvement)", 50.0, "OK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			measured := map[string]map[string]measuredPhase{
				"p": {"ph": {N: 5, P99Ms: c.measured}},
			}
			verdicts := compare(bf, measured, 0.20)
			if len(verdicts) != 1 {
				t.Fatalf("want 1 verdict, got %d", len(verdicts))
			}
			if verdicts[0].Status != c.want {
				t.Fatalf("want %s, got %s (delta=%+v)", c.want, verdicts[0].Status, verdicts[0].DeltaPct)
			}
		})
	}
}

func TestCompare_SKIP_NoSamples(t *testing.T) {
	bf := fakeBaselines()
	// cluster_registration path exists but ui_submit captured 0 samples
	// (mirroring kind-absent skip-graceful behavior in the harness).
	measured := map[string]map[string]measuredPhase{
		"addon_cycle": {
			"enable_dry_run":  {N: 5, P99Ms: 0.9},
			"disable_dry_run": {N: 5, P99Ms: 1.5},
			"upgrade_global":  {N: 5, P99Ms: 0.4},
		},
		"cluster_registration": {
			"ui_submit": {N: 0}, // zero samples → SKIP, NOT REGRESS
		},
	}
	verdicts := compare(bf, measured, 0.20)
	var skipped int
	var regressed int
	for _, v := range verdicts {
		if v.Status == "SKIP" {
			skipped++
		}
		if v.Status == "REGRESS" {
			regressed++
		}
	}
	if skipped != 1 || regressed != 0 {
		t.Fatalf("expected SKIP=1 REGRESS=0, got SKIP=%d REGRESS=%d (verdicts: %+v)", skipped, regressed, verdicts)
	}
}

func TestCompare_MISSING_EntirePathAbsent(t *testing.T) {
	bf := fakeBaselines()
	// cluster_registration absent entirely from captures — all its
	// phases should report MISSING (developer ran perf harness in
	// in-process-only mode without kind).
	measured := map[string]map[string]measuredPhase{
		"addon_cycle": {
			"enable_dry_run":  {N: 5, P99Ms: 0.9},
			"disable_dry_run": {N: 5, P99Ms: 1.5},
			"upgrade_global":  {N: 5, P99Ms: 0.4},
		},
	}
	verdicts := compare(bf, measured, 0.20)
	var missing int
	var regressed int
	for _, v := range verdicts {
		if v.Status == "MISSING" {
			missing++
		}
		if v.Status == "REGRESS" {
			regressed++
		}
	}
	if missing != 1 || regressed != 0 {
		t.Fatalf("expected MISSING=1 REGRESS=0, got MISSING=%d REGRESS=%d", missing, regressed)
	}
}

func TestCompare_NEW_PhaseNotInBaselines(t *testing.T) {
	bf := fakeBaselines()
	// captures include a path that is NOT in the baselines yet (e.g. a
	// developer added a new phase to phases.go but didn't refresh the
	// baselines). The gate should NOT fire — it should warn and
	// continue.
	measured := map[string]map[string]measuredPhase{
		"addon_cycle": {
			"enable_dry_run":   {N: 5, P99Ms: 0.9},
			"disable_dry_run":  {N: 5, P99Ms: 1.5},
			"upgrade_global":   {N: 5, P99Ms: 0.4},
			"brand_new_phase":  {N: 5, P99Ms: 3.0},
		},
		"cluster_registration": {
			"ui_submit": {N: 5, P99Ms: 95.0},
		},
	}
	verdicts := compare(bf, measured, 0.20)
	var newCount int
	for _, v := range verdicts {
		if v.Status == "NEW" {
			newCount++
		}
		if v.Status == "REGRESS" {
			t.Fatalf("unexpected REGRESS verdict: %+v", v)
		}
	}
	if newCount != 1 {
		t.Fatalf("expected NEW=1, got NEW=%d (verdicts: %+v)", newCount, verdicts)
	}
}

func TestQuantile(t *testing.T) {
	// Type-7 linear interpolation reference values:
	//   p50 of [1..9] = 5
	//   p99 of [1..100] (1-indexed) ≈ 99.01
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if got := quantile(sorted, 0.5); got != 5.0 {
		t.Fatalf("p50: want 5.0, got %v", got)
	}
	// Edge cases
	if got := quantile(nil, 0.5); got != 0 {
		t.Fatalf("empty: want 0, got %v", got)
	}
	if got := quantile([]float64{42}, 0.99); got != 42 {
		t.Fatalf("single: want 42, got %v", got)
	}
	// p100 returns max
	if got := quantile(sorted, 1.0); got != 9 {
		t.Fatalf("p100: want 9, got %v", got)
	}
	// p0 returns min
	if got := quantile(sorted, 0.0); got != 1 {
		t.Fatalf("p0: want 1, got %v", got)
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int{"z": 1, "a": 1, "m": 1}
	got := sortedKeys(m)
	want := []string{"a", "m", "z"}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("not sorted: %v", got)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestGateFailedError(t *testing.T) {
	one := &gateFailedError{count: 1}
	if !strings.Contains(one.Error(), "1 phase") {
		t.Fatalf("singular message wrong: %s", one.Error())
	}
	many := &gateFailedError{count: 3}
	if !strings.Contains(many.Error(), "3 phases") {
		t.Fatalf("plural message wrong: %s", many.Error())
	}
}

// ---------------------------------------------------------------------------
// REFRESH MODE — round-trip tests.
//
// refresh() reads a captured-timings JSONL file + existing baselines
// YAML, rewrites the YAML with the new per-phase quantiles, and
// preserves phases that captured zero samples. These tests exercise
// that behavior end-to-end against a tempdir + tiny fixtures.
// ---------------------------------------------------------------------------

func TestRefresh_UpdatesAndPreserves(t *testing.T) {
	// Stub the clock for deterministic Date field.
	originalNow := timeNow
	timeNow = func() time.Time {
		return time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	t.Cleanup(func() { timeNow = originalNow })

	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "baselines.yaml")
	jsonlPath := filepath.Join(tmp, "timings.jsonl")

	// Seed an existing baselines YAML with two paths, three phases.
	// One phase (cluster_registration/ui_submit) will get captures;
	// another (cluster_registration/argocd_secret_created) will have
	// zero captures — the existing baseline numbers must be preserved.
	seedYAML := `# leading comment block preserved verbatim
environment:
  date: "2026-05-26"
  sharko_version: "1.0.0"
  runner: "dev workstation"
  sample_count: 30
paths:
  cluster_registration:
    phases:
      ui_submit:
        n: 30
        p50_ms: 20.0
        p95_ms: 50.0
        p99_ms: 100.0
        min_ms: 10.0
        max_ms: 200.0
      argocd_secret_created:
        n: 30
        p50_ms: 400.0
        p95_ms: 700.0
        p99_ms: 900.0
        min_ms: 380.0
        max_ms: 950.0
  addon_cycle:
    phases:
      enable_dry_run:
        n: 30
        p50_ms: 0.25
        p95_ms: 0.33
        p99_ms: 0.38
        min_ms: 0.22
        max_ms: 0.40
`
	if err := os.WriteFile(yamlPath, []byte(seedYAML), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}

	// Captures: only ui_submit + enable_dry_run; argocd_secret_created
	// has nothing → must be preserved.
	jsonl := strings.Join([]string{
		`{"path":"cluster_registration","phase":"ui_submit","duration_ms":42.0}`,
		`{"path":"cluster_registration","phase":"ui_submit","duration_ms":48.0}`,
		`{"path":"cluster_registration","phase":"ui_submit","duration_ms":51.0}`,
		`{"path":"addon_cycle","phase":"enable_dry_run","duration_ms":0.5}`,
		`{"path":"addon_cycle","phase":"enable_dry_run","duration_ms":0.6}`,
		`{"path":"addon_cycle","phase":"enable_dry_run","duration_ms":0.7}`,
	}, "\n") + "\n"
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	if err := refresh(jsonlPath, yamlPath, "", "1.25.0-pre.X", "ci runner"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Re-read the refreshed file and validate.
	got, err := loadBaselines(yamlPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got.Environment.Date != "2099-01-02" {
		t.Fatalf("date: want 2099-01-02, got %q", got.Environment.Date)
	}
	if got.Environment.SharkoVersion != "1.25.0-pre.X" {
		t.Fatalf("version: want 1.25.0-pre.X, got %q", got.Environment.SharkoVersion)
	}
	if got.Environment.Runner != "ci runner" {
		t.Fatalf("runner: want 'ci runner', got %q", got.Environment.Runner)
	}

	ui := got.Paths["cluster_registration"].Phases["ui_submit"]
	if ui.N != 3 {
		t.Fatalf("ui_submit N: want 3, got %d", ui.N)
	}
	if ui.P50Ms != 48.0 {
		t.Fatalf("ui_submit p50: want 48.0 (middle of 42/48/51), got %v", ui.P50Ms)
	}

	// PRESERVED: argocd_secret_created had zero captures → baseline
	// numbers must equal the seed values.
	sec := got.Paths["cluster_registration"].Phases["argocd_secret_created"]
	if sec.N != 30 || sec.P99Ms != 900.0 {
		t.Fatalf("argocd_secret_created should be preserved (N=30, p99=900), got %+v", sec)
	}

	// Confirm leading comment block survived.
	raw, _ := os.ReadFile(yamlPath)
	if !strings.HasPrefix(string(raw), "# leading comment block preserved verbatim") {
		t.Fatalf("comment header not preserved; first chars: %q", string(raw[:80]))
	}
}

func TestRefresh_NoSamplesPreservesEverything(t *testing.T) {
	// Stub the clock.
	originalNow := timeNow
	timeNow = func() time.Time { return time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = originalNow })

	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "baselines.yaml")
	jsonlPath := filepath.Join(tmp, "timings.jsonl")

	// Same seed as above.
	seedYAML := `# header
environment:
  date: "2026-05-26"
  sharko_version: "1.0.0"
  runner: "dev workstation"
  sample_count: 30
paths:
  addon_cycle:
    phases:
      enable_dry_run:
        n: 30
        p50_ms: 0.25
        p95_ms: 0.33
        p99_ms: 0.38
        min_ms: 0.22
        max_ms: 0.40
`
	if err := os.WriteFile(yamlPath, []byte(seedYAML), 0o644); err != nil {
		t.Fatalf("seed yaml: %v", err)
	}
	// Empty timings file (kind not available, every path skipped).
	if err := os.WriteFile(jsonlPath, []byte{}, 0o644); err != nil {
		t.Fatalf("seed jsonl: %v", err)
	}

	if err := refresh(jsonlPath, yamlPath, "", "", ""); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := loadBaselines(yamlPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Existing baseline values must survive an empty-captures refresh.
	enable := got.Paths["addon_cycle"].Phases["enable_dry_run"]
	if enable.P99Ms != 0.38 {
		t.Fatalf("enable_dry_run p99 must be preserved at 0.38, got %v", enable.P99Ms)
	}
	// When flags are blank, existing version + runner survive.
	if got.Environment.SharkoVersion != "1.0.0" {
		t.Fatalf("version should be preserved, got %q", got.Environment.SharkoVersion)
	}
	if got.Environment.Runner != "dev workstation" {
		t.Fatalf("runner should be preserved, got %q", got.Environment.Runner)
	}
}

func TestExtractYAMLHeader(t *testing.T) {
	// Comments + blanks at top, then a key.
	input := []byte("# one\n# two\n\n# three\nenvironment:\n  date: x\n")
	got := extractYAMLHeader(input)
	want := "# one\n# two\n\n# three\n"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestPick(t *testing.T) {
	if pick("a", "b") != "a" {
		t.Fatal("non-empty flag should win")
	}
	if pick("", "b") != "b" {
		t.Fatal("blank flag should fall back")
	}
	if pick("", "") != "" {
		t.Fatal("both blank should yield blank")
	}
}
