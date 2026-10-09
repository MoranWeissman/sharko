package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A command that logs must finish.
//
// `Execute` (root.go) installs the redaction wrapper as the process-wide slog
// default for every command. It used to build that wrapper around whatever
// handler was already the default — Go's stock one. That handler does not own
// a writer; it writes through the standard `log` package, and the `log`
// package asks slog for the current default handler again at write time. Once
// the wrapper was the default, the first log line a command emitted went round
// in a circle into a lock it already held, and the command never came back:
// `sharko validate-config` on a file that carries the old API group printed
// nothing at all and had to be killed. `sharko serve` escaped only because it
// swaps in a writer-backed handler moments later.
//
// The tests below run the REAL built binary with a deadline the test enforces
// itself, so a return of this defect is a named failure with a clear message
// instead of a suite that hangs until CI times out.
//
// What is asserted here is meaning and safety — that the command terminates,
// with the intended status, having printed the warning it owes the operator,
// and that a credential still cannot cross the redaction sink. Timestamps and
// the exact text layout are deliberately NOT asserted: the fix changes the
// non-serve log format to Go's standard `time=... level=... msg=...` text
// form, and that is accepted.

const (
	// cliDeadline is generous on purpose. A slow or loaded machine must not
	// be mistaken for a deadlock; a deadlock is forever, so 60s separates the
	// two cleanly.
	cliDeadline = 60 * time.Second

	// deprecatedAPIGroupFile is the smallest input that makes a non-serve
	// command log. schema.IsEnveloped recognises the old API group and warns
	// once; the unknown kind then fails validation, so the command exits 1.
	deprecatedAPIGroupFile = "apiVersion: sharko.io/v1\nkind: SharkoConfig\n"
)

// cliResult is one finished (or killed) run of the built binary.
type cliResult struct {
	stdout   string
	stderr   string
	exitCode int
	timedOut bool
}

// runCLI runs the built binary with a deadline the test owns.
//
// The deadline is an exec.CommandContext timeout plus an explicit wait delay,
// so a wedged child is killed rather than left behind. The result reports
// "timed out" separately from "exited with a status", because those two are
// completely different findings: the first is the deadlock this file exists to
// catch, the second is an ordinary wrong-behaviour failure.
func runCLI(t *testing.T, bin string, extraEnv map[string]string, args ...string) cliResult {
	t.Helper()
	return runCLIWithDeadline(t, cliDeadline, bin, extraEnv, args...)
}

// runCLIWithDeadline is runCLI with the deadline named by the caller.
func runCLIWithDeadline(t *testing.T, deadline time.Duration, bin string, extraEnv map[string]string, args ...string) cliResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 5 * time.Second

	env := os.Environ()
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	res.timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.exitCode = 0
	case errors.As(runErr, &exitErr):
		res.exitCode = exitErr.ExitCode()
	default:
		if !res.timedOut {
			t.Fatalf("running %s %v failed before it could produce a status: %v", filepath.Base(bin), args, runErr)
		}
	}
	return res
}

// requireTerminated fails with a message that names the deadlock explicitly,
// and separately from a wrong exit status.
func requireTerminated(t *testing.T, res cliResult, wantExit int, what string) {
	t.Helper()
	if res.timedOut {
		t.Fatalf("%s did not finish before its deadline — the CLI log handler chain is deadlocked. "+
			"newCLIHandler (cmd/sharko/root.go) must build its inner handler with its own writer, "+
			"never from slog.Default().Handler(). stdout so far: %q; stderr so far: %q",
			what, res.stdout, res.stderr)
	}
	if res.exitCode != wantExit {
		t.Fatalf("%s exited %d, want %d\nstdout: %s\nstderr: %s",
			what, res.exitCode, wantExit, res.stdout, res.stderr)
	}
}

// buildSharko builds the real binary once and hands back its path. Every case
// in TestCLICommandsThatLogTerminate shares it.
func buildSharko(t *testing.T, dir string) string {
	t.Helper()

	bin := filepath.Join(dir, "sharko")

	// Built in a normal environment on purpose: HOME must stay as it is, or
	// Go's module cache moves and the build breaks.
	build := exec.Command("go", "build", "-o", bin, "./cmd/sharko")
	build.Dir = repoRootForCLITest(t)
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building the sharko binary failed: %v\n%s", err, out)
	}

	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("the sharko binary was not written to %s: %v", bin, err)
	}
	if info.Size() == 0 {
		t.Fatalf("the sharko binary at %s is empty — nothing was built, so no case below would prove anything", bin)
	}
	return bin
}

// repoRootForCLITest walks up from the test's working directory (cmd/sharko)
// to the directory holding go.mod, so `go build ./cmd/sharko` resolves.
func repoRootForCLITest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot read the working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %q — cannot locate the repository root to build from", dir)
		}
		dir = parent
	}
}

// cliEnv is the environment every case runs under: an isolated config
// directory and no route to a cluster.
func cliEnv(t *testing.T) map[string]string {
	t.Helper()
	// isolatedConfigDir (server_address_leak_test.go) points the CLI at a
	// throwaway directory and PROVES, by calling configDir() itself, that the
	// override took effect before any command runs. The same value is handed
	// to the child process below.
	return map[string]string{
		"SHARKO_CONFIG_DIR": isolatedConfigDir(t),
		"KUBECONFIG":        "/nonexistent",
	}
}

// logLineMarkers are the fragments Go's text handler puts on every record.
// Their ABSENCE is what the unchanged-paths cases assert.
var logLineMarkers = []string{"level=INFO", "level=WARN", "level=ERROR"}

func containsAnyLogLine(s string) string {
	for _, m := range logLineMarkers {
		if strings.Contains(s, m) {
			return m
		}
	}
	return ""
}

func TestCLICommandsThatLogTerminate(t *testing.T) {
	bin := buildSharko(t, t.TempDir())

	// The one command that emits a log line offline: validate-config on a
	// file carrying the old API group. Before the fix this printed nothing at
	// all and had to be killed.
	t.Run("DeprecatedAPIGroupConfigFinishes", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "old-api-group.yaml")
		if err := os.WriteFile(cfg, []byte(deprecatedAPIGroupFile), 0o600); err != nil {
			t.Fatalf("writing the test config failed: %v", err)
		}

		res := runCLI(t, bin, cliEnv(t), "validate-config", cfg)
		requireTerminated(t, res, 1, "sharko validate-config on a file with the old API group")

		// The warning the operator is owed, asserted by what it says rather
		// than how it is laid out.
		for _, want := range []string{"deprecated_api_group", "sharko.io/v1", "sharko.dev/v1"} {
			if !strings.Contains(res.stderr, want) {
				t.Errorf("the deprecation warning does not mention %q\nstderr: %s", want, res.stderr)
			}
		}
		// And the validation verdict still reaches the operator.
		if !strings.Contains(res.stdout, "failed validation") {
			t.Errorf("the validation verdict is missing from stdout\nstdout: %s", res.stdout)
		}
	})

	// A second run of the same command over a DIRECTORY holding two files
	// with the old API group. This walks a different code path (directory
	// collection, two files validated in one process) and proves the fix
	// holds for a run that reads more than one deprecated file.
	//
	// Note for whoever reads this next: the warning itself is emitted through
	// a sync.Once in internal/schema/envelope.go, so it appears ONCE no matter
	// how many deprecated files a single run reads. This case does not claim
	// otherwise — what it proves is that the multi-file run terminates and
	// still warns.
	t.Run("TwoDeprecatedAPIGroupConfigsFinish", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"first.yaml", "second.yaml"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(deprecatedAPIGroupFile), 0o600); err != nil {
				t.Fatalf("writing %s failed: %v", name, err)
			}
		}

		res := runCLI(t, bin, cliEnv(t), "validate-config", dir)
		requireTerminated(t, res, 1, "sharko validate-config over a directory with two old-API-group files")

		if !strings.Contains(res.stderr, "deprecated_api_group") {
			t.Errorf("the deprecation warning is missing from the two-file run\nstderr: %s", res.stderr)
		}
		if !strings.Contains(res.stdout, "2 file(s) failed validation") {
			t.Errorf("both files should have failed validation\nstdout: %s", res.stdout)
		}
	})

	// The paths that emit no log line must be exactly as they were.
	t.Run("HelpIsUnchanged", func(t *testing.T) {
		for _, args := range [][]string{
			{"--help"},
			{"version", "--help"},
			{"validate-catalog", "--help"},
		} {
			name := strings.Join(args, " ")
			res := runCLI(t, bin, cliEnv(t), args...)
			requireTerminated(t, res, 0, "sharko "+name)
			if strings.TrimSpace(res.stdout) == "" {
				t.Errorf("sharko %s printed nothing", name)
			}
			if marker := containsAnyLogLine(res.stderr); marker != "" {
				t.Errorf("sharko %s emitted a log line (%s) it never used to\nstderr: %s", name, marker, res.stderr)
			}
		}
		// The top-level help still lists the commands.
		res := runCLI(t, bin, cliEnv(t), "--help")
		for _, want := range []string{"Available Commands:", "validate-config", "validate-catalog"} {
			if !strings.Contains(res.stdout, want) {
				t.Errorf("top-level help no longer contains %q\nstdout: %s", want, res.stdout)
			}
		}
	})

	t.Run("SuccessfulValidationIsUnchanged", func(t *testing.T) {
		catalog := filepath.Join(repoRootForCLITest(t), "catalog", "addons.yaml")
		if _, err := os.Stat(catalog); err != nil {
			t.Fatalf("the curated catalog is missing at %s: %v — this case would otherwise pass without checking anything", catalog, err)
		}

		res := runCLI(t, bin, cliEnv(t), "validate-catalog", catalog)
		requireTerminated(t, res, 0, "sharko validate-catalog on the curated catalog")
		if !strings.Contains(res.stdout, "OK") {
			t.Errorf("the success line is missing\nstdout: %s", res.stdout)
		}
		if marker := containsAnyLogLine(res.stderr); marker != "" {
			t.Errorf("a clean validate-catalog run emitted a log line (%s) it never used to\nstderr: %s", marker, res.stderr)
		}
	})
}

// ---------------------------------------------------------------------------
// In-process probes, run in a child process.
//
// There is no second non-serve command that logs offline. Every log line in
// the tree comes from internal/*, and the only one a non-serve command can
// reach without a cluster or a server is the deprecated-API-group warning
// above. Rather than invent a command that does not exist, the second proof is
// taken by driving the shipping handler chain directly, at levels the binary
// case never reaches, and by planting a credential in it.
//
// Those probes re-execute THIS test binary rather than running in the test
// process, and that is not ceremony. A wedged chain holds the standard `log`
// package's lock for good; anything that later reads or sets that package's
// output — including a test cleanup trying to put things back — blocks on it
// too, and the whole suite hangs until the 10-minute panic. Putting each probe
// in its own process means a wedge is one killed child and one named failure.
// ---------------------------------------------------------------------------

// probeEnvVar tells a re-executed copy of this test binary that it is the
// child, and which probe to run.
const probeEnvVar = "SHARKO_CLI_LOG_PROBE"

const (
	probeLevels        = "levels"
	probeRedactControl = "redact-control"
	probeRedact        = "redact"
)

const (
	probeMsg  = "cli log chain probe"
	probeDone = "CLI-LOG-PROBE-DONE"

	// plantedCredential is synthetic. It exists only to be looked for.
	plantedCredential    = "sharko_planted_credential_for_the_redaction_probe"
	redactionPlaceholder = "[REDACTED]"
)

// probeDeadline bounds a child probe. A log call that has not come back in
// this long is not slow, it is stuck.
const probeDeadline = 25 * time.Second

// runProbeChild re-executes this test binary in the named probe mode, under a
// deadline the parent owns.
func runProbeChild(t *testing.T, testName, mode string) cliResult {
	t.Helper()
	return runCLIWithDeadline(t, probeDeadline, os.Args[0],
		map[string]string{probeEnvVar: mode},
		"-test.run=^"+testName+"$", "-test.count=1")
}

// requireProbeRan refuses a child whose probe never reached its last line.
// Without this a child that exited early would look like a pass.
func requireProbeRan(t *testing.T, res cliResult, what string) {
	t.Helper()
	if !strings.Contains(res.stdout, probeDone) {
		t.Fatalf("%s never printed its completion marker — it proved nothing\nstdout: %s\nstderr: %s",
			what, res.stdout, res.stderr)
	}
}

// TestCLILogChainReturnsAtEveryLevel drives the SHIPPING handler chain — the
// same newCLIHandler call Execute makes — at Info, Warn and Error.
func TestCLILogChainReturnsAtEveryLevel(t *testing.T) {
	if os.Getenv(probeEnvVar) == probeLevels {
		slog.SetDefault(slog.New(newCLIHandler()))
		slog.Info(probeMsg, "level_under_test", "info")
		slog.Warn(probeMsg, "level_under_test", "warn")
		slog.Error(probeMsg, "level_under_test", "error")
		fmt.Println(probeDone)
		return
	}

	res := runProbeChild(t, "TestCLILogChainReturnsAtEveryLevel", probeLevels)
	requireTerminated(t, res, 0, "the probe that logs at every level through the CLI handler chain")
	requireProbeRan(t, res, "the every-level probe")

	if got := strings.Count(res.stderr, probeMsg); got != 3 {
		t.Fatalf("the probe wrote %d lines, want exactly 3 (one per level)\nstderr: %s", got, res.stderr)
	}
	for _, want := range logLineMarkers {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the probe output has no %s line\nstderr: %s", want, res.stderr)
		}
	}
}

// TestCLILogChainStillRedacts proves the fix did not move the redaction
// boundary: a credential handed to the shipping CLI chain still comes out as
// the placeholder, never as itself.
//
// The positive control runs FIRST, with redaction removed. If the probe cannot
// see a planted credential when nothing is stopping it, the probe is broken and
// the real case below would pass without proving anything — so the control
// failing is fatal.
func TestCLILogChainStillRedacts(t *testing.T) {
	switch os.Getenv(probeEnvVar) {
	case probeRedactControl:
		// Same writer, same record, nothing in the way.
		slog.New(slog.NewTextHandler(os.Stderr, nil)).
			Info("redaction probe control", "token", plantedCredential)
		fmt.Println(probeDone)
		return
	case probeRedact:
		slog.SetDefault(slog.New(newCLIHandler()))
		slog.Info("redaction probe", "token", plantedCredential)
		fmt.Println(probeDone)
		return
	}

	control := runProbeChild(t, "TestCLILogChainStillRedacts", probeRedactControl)
	requireTerminated(t, control, 0, "the un-redacted positive control")
	requireProbeRan(t, control, "the un-redacted positive control")
	if !strings.Contains(control.stderr, plantedCredential) {
		t.Fatalf("the positive control did not see the planted credential — the probe is broken, "+
			"so the case below would pass without proving anything\nstderr: %s", control.stderr)
	}

	got := runProbeChild(t, "TestCLILogChainStillRedacts", probeRedact)
	requireTerminated(t, got, 0, "the planted credential sent through the shipping CLI handler chain")
	requireProbeRan(t, got, "the redaction probe")
	if strings.TrimSpace(got.stderr) == "" {
		t.Fatal("the shipping chain wrote nothing — nothing was proven, so this is a failure, not an empty pass")
	}
	if strings.Contains(got.stderr, plantedCredential) {
		t.Fatalf("the planted credential crossed the redaction sink\nstderr: %s", got.stderr)
	}
	if !strings.Contains(got.stderr, redactionPlaceholder) {
		t.Fatalf("the redaction placeholder %q is missing — the credential was neither kept nor redacted\nstderr: %s",
			redactionPlaceholder, got.stderr)
	}
}
