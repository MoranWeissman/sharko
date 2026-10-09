// Package ekslivescript proves the one hard rule scripts/eks-live-test.sh gained
// in BF18: the script will not install Sharko onto a real EKS cluster unless the
// operator names the exact image tag to install.
//
// It used to carry a default — SHARKO_IMAGE_TAG="${EKS_TEST_SHARKO_IMAGE_TAG:-v2.3.0}"
// — which flowed straight into `helm upgrade --install sharko ... --set
// image.tag=...`. The runbook never mentioned the variable, so following the
// documented steps stood up a cluster running an old published build nobody had
// chosen. The default is gone; hub-up and env-up now refuse to start without it.
//
// Nothing here talks to AWS, Kubernetes, a registry or the network. Every
// external command that could is replaced by a stub on a temporary PATH that
// records its own name and arguments to a log file and exits 0. Every value used
// is synthetic.
package ekslivescript

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// syntheticTag is the tag the tests hand the script. It is deliberately not a
// version number and does not exist in any registry.
const syntheticTag = "synthetic-not-a-real-tag-bf18"

// dangerousCommands is the vocabulary the inventory guard searches for: every
// command name that could reach AWS, Kubernetes, a container registry, the
// network, or other processes on the machine. Names on this list that appear
// anywhere in the script MUST be stubbed before any test runs it. The list is
// deliberately wider than what the script uses today, so a future edit that
// reaches for a new tool is caught rather than silently escaping the stubs.
var dangerousCommands = []string{
	"aws", "eksctl", "kubectl", "helm", "argocd", "curl", "gh",
	"docker", "podman", "buildah", "skopeo", "crane", "cosign",
	"wget", "nc", "ssh", "scp",
	"terraform", "az", "gcloud", "ecr", "sops", "vault",
	"aws-iam-authenticator", "oc", "kustomize", "kind", "minikube", "k9s",
	"pkill", "killall", "git",
}

// expectedShims is the LIST — not a count — of names the guard expects to find
// in the script right now. TestExternalCommandInventoryIsFullyAccountedFor fails
// if the script grows a new dangerous command (it would appear here and not be
// stubbed) AND if an entry here goes stale (the script no longer mentions it).
var expectedShims = []string{
	"argocd", "aws", "curl", "docker", "eksctl", "gh", "git",
	"helm", "kind", "kubectl", "pkill",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return root
}

func scriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "scripts", "eks-live-test.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("script not found at %s: %v", p, err)
	}
	return p
}

func runbookPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "docs", "site", "developer-guide", "eks-live-test-runbook.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("runbook not found at %s: %v", p, err)
	}
	return p
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// commandMentioned reports whether name appears in src as a whole word (not as
// part of a longer identifier). Conservative on purpose: a false positive costs
// one harmless stub, a false negative is a command that runs for real.
func commandMentioned(src, name string) bool {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(name) + `(?:[^A-Za-z0-9_-]|$)`)
	return re.MatchString(src)
}

// shimsRequiredBy walks the script and returns every dangerous command it
// mentions, sorted.
func shimsRequiredBy(src string) []string {
	var found []string
	for _, name := range dangerousCommands {
		if commandMentioned(src, name) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}

// shimDir builds a temporary directory holding one executable stub per name.
// Each stub appends "<name> <arg> <arg> ..." to $SHIM_LOG and exits 0, so the
// log is a faithful record of every dangerous command the script tried to run
// and with exactly which arguments.
//
// The curl stub additionally prints a registry-token JSON body, because
// ghcr_image_pullable() parses curl's output to decide whether an image is
// anonymously pullable. Without it the script would divert into the
// imagePullSecret branch and never reach the helm call this package needs to
// observe. It is still a stub: it opens no connection.
func shimDir(t *testing.T, names []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		body := "#!/bin/sh\n" +
			"printf '%s' \"" + name + "\" >> \"$SHIM_LOG\"\n" +
			"for a in \"$@\"; do printf ' %s' \"$a\" >> \"$SHIM_LOG\"; done\n" +
			"printf '\\n' >> \"$SHIM_LOG\"\n"
		if name == "curl" {
			body += "printf '{\"token\":\"stub-token\"}\\n'\n"
		}
		body += "exit 0\n"
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("writing stub %s: %v", name, err)
		}
	}
	return dir
}

// runResult is what one stubbed script run produced.
type runResult struct {
	exitCode  int
	stdout    string
	stderr    string
	logExists bool
	log       string
}

func (r runResult) output() string { return r.stdout + r.stderr }

// runScript runs one subcommand of the script against stubs on a temporary PATH.
// The environment is built from scratch — nothing from the developer's shell
// leaks in, so no real AWS profile or kubeconfig can be picked up.
func runScript(t *testing.T, imageTagEnv *string, args ...string) runResult {
	t.Helper()

	src := readFile(t, scriptPath(t))
	work := t.TempDir()
	logPath := filepath.Join(work, "shim.log")
	shims := shimDir(t, shimsRequiredBy(src))

	env := []string{
		"PATH=" + shims + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"SHIM_LOG=" + logPath,
		"HOME=" + work,
		"TMPDIR=" + work,
		// Everything the guards other than the image-tag guard need, so the
		// image tag is the ONLY thing that differs between the positive control
		// and the absence run.
		"SHARKO_EKS_TEST_ACCOUNT_ID=000000000000",
		"SHARKO_GITOPS_REPO_URL=https://example.invalid/synthetic/repo",
		"SHARKO_GITHUB_TOKEN=synthetic-token-not-a-credential",
		"EKS_TEST_KUBECONFIG=" + filepath.Join(work, "spoke.kubeconfig"),
		"EKS_TEST_HUB_KUBECONFIG=" + filepath.Join(work, "hub.kubeconfig"),
	}
	if imageTagEnv != nil {
		env = append(env, "EKS_TEST_SHARKO_IMAGE_TAG="+*imageTagEnv)
	}

	cmd := exec.Command("/bin/bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Env = env
	cmd.Dir = repoRoot(t)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	// Any prompt this reaches must not block the test.
	cmd.Stdin = strings.NewReader("")
	runErr := cmd.Run()

	res := runResult{stdout: out.String(), stderr: errb.String()}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			res.exitCode = ee.ExitCode()
		} else {
			t.Fatalf("running script: %v", runErr)
		}
	}
	if b, err := os.ReadFile(logPath); err == nil {
		res.logExists = true
		res.log = string(b)
	}
	return res
}

func strptr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// The positive control comes first. If it does not hold, every "nothing ran"
// assertion below is vacuous — the log could be empty because the harness never
// works, not because the guard stopped anything.
// ---------------------------------------------------------------------------

// TestPositiveControlStubbedCommandsDoRun proves the harness can produce a
// non-empty log: with an image tag supplied and nothing else changed, both
// subcommands get past the guards and call a stubbed command.
func TestPositiveControlStubbedCommandsDoRun(t *testing.T) {
	for _, sub := range []string{"hub-up", "env-up"} {
		t.Run(sub, func(t *testing.T) {
			res := runScript(t, strptr(syntheticTag), sub, "--yes")
			if !res.logExists || len(res.log) == 0 {
				t.Fatalf("POSITIVE CONTROL FAILED for %q: the stub log is %s. "+
					"Every absence assertion in this package would be vacuous.\n"+
					"stdout:\n%s\nstderr:\n%s",
					sub, map[bool]string{true: "empty", false: "missing"}[res.logExists],
					res.stdout, res.stderr)
			}
			if !strings.Contains(res.log, "aws ") {
				t.Fatalf("POSITIVE CONTROL FAILED for %q: the stub log has no aws call.\nlog:\n%s", sub, res.log)
			}
		})
	}
}

// TestMissingImageTagStopsBeforeAnyDangerousCommand is the absence proof, and
// the test that a restored default at scripts/eks-live-test.sh:103 breaks.
func TestMissingImageTagStopsBeforeAnyDangerousCommand(t *testing.T) {
	cases := []struct {
		name string
		tag  *string
	}{
		{"unset", nil},
		{"empty", strptr("")},
	}
	for _, sub := range []string{"hub-up", "env-up"} {
		for _, tc := range cases {
			t.Run(sub+"/"+tc.name, func(t *testing.T) {
				// Control first, in this same subtest, with the SAME harness.
				ctrl := runScript(t, strptr(syntheticTag), sub, "--yes")
				if !ctrl.logExists || len(ctrl.log) == 0 {
					t.Fatalf("control run produced no stub activity — the absence check below would prove nothing")
				}

				res := runScript(t, tc.tag, sub, "--yes")
				if res.exitCode == 0 {
					t.Fatalf("%s with the image tag %s exited 0; it must refuse.\nstdout:\n%s\nstderr:\n%s",
						sub, tc.name, res.stdout, res.stderr)
				}
				if res.logExists && len(res.log) > 0 {
					t.Fatalf("%s with the image tag %s ran stubbed commands before refusing:\n%s",
						sub, tc.name, res.log)
				}
				if !strings.Contains(res.output(), "EKS_TEST_SHARKO_IMAGE_TAG") {
					t.Fatalf("%s refusal does not name EKS_TEST_SHARKO_IMAGE_TAG.\nstdout:\n%s\nstderr:\n%s",
						sub, res.stdout, res.stderr)
				}
				if !strings.Contains(res.output(), "<candidate-image-tag>") {
					t.Fatalf("%s refusal does not tell the caller to name the candidate image tag.\nstderr:\n%s",
						sub, res.stderr)
				}
			})
		}
	}
}

// TestRefusalEchoesNothingTheCallerSupplied checks the refusal does not read
// back values the caller handed the script. Every value the caller can set is
// supplied here as a distinctive marker, and none of them may appear in the
// refusal.
func TestRefusalEchoesNothingTheCallerSupplied(t *testing.T) {
	src := readFile(t, scriptPath(t))
	work := t.TempDir()
	logPath := filepath.Join(work, "shim.log")
	shims := shimDir(t, shimsRequiredBy(src))

	markers := map[string]string{
		"SHARKO_EKS_TEST_ACCOUNT_ID": "111111111111",
		"SHARKO_GITOPS_REPO_URL":     "https://example.invalid/MARKERREPO",
		"SHARKO_GITHUB_TOKEN":        "MARKERTOKEN",
		"AWS_PROFILE":                "MARKERPROFILE",
		"EKS_TEST_SHARKO_IMAGE_REPO": "example.invalid/MARKERIMAGEREPO",
	}
	env := []string{
		"PATH=" + shims + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"SHIM_LOG=" + logPath,
		"HOME=" + work,
		"TMPDIR=" + work,
	}
	for k, v := range markers {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command("/bin/bash", scriptPath(t), "hub-up", "--yes")
	cmd.Env = env
	cmd.Dir = repoRoot(t)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errb, strings.NewReader("")
	_ = cmd.Run()

	combined := out.String() + errb.String()
	if !strings.Contains(combined, "EKS_TEST_SHARKO_IMAGE_TAG is not set") {
		t.Fatalf("expected the image-tag refusal, got:\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	}
	for k, v := range markers {
		if strings.Contains(combined, v) {
			t.Errorf("the refusal echoed the caller-supplied %s (%q) back:\n%s", k, v, combined)
		}
	}
}

// TestSyntheticTagReachesHelmSetImageTag proves the value the operator names is
// the value that lands on the cluster.
//
// It sources the script and calls hub_install_sharko directly rather than
// driving hub-up end to end: reaching the install through hub-up would mean
// stubbing an EKS cluster create, an IAM role, Pod Identity associations and an
// ArgoCD install convincingly enough for each step's output parsing to survive,
// which would test the stubs more than the script. Sourcing with no arguments
// runs the script's dispatcher on its default "help" path, which touches no
// stubbed command; SHIM_LOG is pointed at the real log only afterwards.
func TestSyntheticTagReachesHelmSetImageTag(t *testing.T) {
	src := readFile(t, scriptPath(t))
	work := t.TempDir()
	logPath := filepath.Join(work, "shim.log")
	sinkPath := filepath.Join(work, "sink.log")
	shims := shimDir(t, shimsRequiredBy(src))

	// No `set -u` here: the script does not run under it, and imposing it would
	// test the driver rather than the script.
	driver := fmt.Sprintf(`
SHIM_LOG=%q
export SHIM_LOG
. %q >/dev/null 2>&1
SHIM_LOG=%q
export SHIM_LOG
hub_install_sharko
`, sinkPath, scriptPath(t), logPath)

	cmd := exec.Command("/bin/bash", "-c", driver)
	cmd.Env = []string{
		"PATH=" + shims + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + work,
		"TMPDIR=" + work,
		"EKS_TEST_SHARKO_IMAGE_TAG=" + syntheticTag,
		"EKS_TEST_HUB_KUBECONFIG=" + filepath.Join(work, "hub.kubeconfig"),
	}
	cmd.Dir = repoRoot(t)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errb, strings.NewReader("")
	_ = cmd.Run()

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("no stub log written — hub_install_sharko never reached a stubbed command.\nstdout:\n%s\nstderr:\n%s",
			out.String(), errb.String())
	}
	log := string(logBytes)

	want := "--set image.tag=" + syntheticTag
	var helmLine string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "helm ") {
			helmLine = line
			break
		}
	}
	if helmLine == "" {
		t.Fatalf("no helm invocation in the stub log:\n%s\nstdout:\n%s\nstderr:\n%s", log, out.String(), errb.String())
	}
	if !strings.Contains(helmLine, want) {
		t.Fatalf("the helm invocation does not carry %q:\n%s", want, helmLine)
	}
}

// TestHelpWorksWithNoEnvironmentAtAll — the guard must not be reachable ahead of
// --help, and the help text must not render a bare trailing colon now that the
// image tag has no default.
func TestHelpWorksWithNoEnvironmentAtAll(t *testing.T) {
	cases := [][]string{
		{"hub-up", "--help"},
		{"env-up", "--help"},
		{"help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := exec.Command("/bin/bash", append([]string{scriptPath(t)}, args...)...)
			// The whole environment, deliberately: one variable.
			cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
			cmd.Dir = repoRoot(t)
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errb, strings.NewReader("")
			if err := cmd.Run(); err != nil {
				t.Fatalf("help exited non-zero (%v)\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
			}
			text := out.String()
			if !strings.Contains(text, "EKS_TEST_SHARKO_IMAGE_TAG") {
				t.Errorf("help does not mention EKS_TEST_SHARKO_IMAGE_TAG:\n%s", text)
			}
			if !strings.Contains(text, "<candidate-image-tag>") {
				t.Errorf("help does not show the <candidate-image-tag> placeholder:\n%s", text)
			}
			for _, line := range strings.Split(text, "\n") {
				if strings.HasSuffix(strings.TrimRight(line, " "), "/sharko:") {
					t.Errorf("help renders an image reference with nothing after the colon: %q", line)
				}
			}
		})
	}
}

// activeOldVersionDefault matches a shell parameter default that pins an old
// Sharko release, e.g. ${VAR:-v2.3.0}. It deliberately does NOT match prose
// about old versions — SECURITY.md's statements and this package's own comments
// are history, not behaviour.
var activeOldVersionDefault = regexp.MustCompile(`:-\s*v?[23]\.[0-9]+\.[0-9]+`)

// TestNoActiveOldReleaseDefaultRemains sweeps the script and its runbook.
func TestNoActiveOldReleaseDefaultRemains(t *testing.T) {
	// Positive control: the matcher must find the exact defect this story removed.
	sample := `SHARKO_IMAGE_TAG="${EKS_TEST_SHARKO_IMAGE_TAG:-v2.3.0}"`
	if !activeOldVersionDefault.MatchString(sample) {
		t.Fatalf("POSITIVE CONTROL FAILED: the matcher does not recognise %q, so a clean sweep proves nothing", sample)
	}

	for _, p := range []string{scriptPath(t), runbookPath(t)} {
		src := readFile(t, p)
		for i, line := range strings.Split(src, "\n") {
			if activeOldVersionDefault.MatchString(line) {
				t.Errorf("%s:%d still pins an old release as a default: %s", p, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestRunbookDocumentsTheImageTag — acceptance items 3 and 4.
func TestRunbookDocumentsTheImageTag(t *testing.T) {
	src := readFile(t, runbookPath(t))

	if !strings.Contains(src, "export EKS_TEST_SHARKO_IMAGE_TAG=<candidate-image-tag>") {
		t.Errorf("the runbook does not carry the export with the placeholder")
	}
	if !strings.Contains(src, "never builds or\npublishes an image") &&
		!strings.Contains(src, "never builds or publishes an image") {
		t.Errorf("the runbook does not say the script never builds or publishes an image")
	}
	if !strings.Contains(src, "has to be in the\nregistry already") &&
		!strings.Contains(src, "has to be in the registry already") {
		t.Errorf("the runbook does not say the image must already exist in the registry")
	}
	if strings.Contains(src, "Set the three env vars") {
		t.Errorf("the runbook still says three env vars; env-up now needs four")
	}
	// No release number may stand in for the placeholder.
	realVersion := regexp.MustCompile(`EKS_TEST_SHARKO_IMAGE_TAG\s*=\s*v?[0-9]+\.[0-9]+\.[0-9]+`)
	if m := realVersion.FindString(src); m != "" {
		t.Errorf("the runbook names a concrete release instead of the placeholder: %s", m)
	}
}

// TestExternalCommandInventoryIsFullyAccountedFor is the guard on the guard. It
// fails if the script grows a dangerous command no stub covers, and it fails if
// an entry in expectedShims goes stale because the script stopped using it.
func TestExternalCommandInventoryIsFullyAccountedFor(t *testing.T) {
	src := readFile(t, scriptPath(t))

	// Positive control: the walk must actually find things.
	if !commandMentioned(src, "aws") {
		t.Fatalf("POSITIVE CONTROL FAILED: the walk cannot even find `aws` in the script")
	}
	// And it must not match a name embedded in a longer identifier.
	if commandMentioned("kubectl_wrapper_helmish", "helm") {
		t.Fatalf("POSITIVE CONTROL FAILED: the walk matches inside identifiers, so its results mean nothing")
	}

	found := shimsRequiredBy(src)
	want := append([]string(nil), expectedShims...)
	sort.Strings(want)

	foundSet := map[string]bool{}
	for _, n := range found {
		foundSet[n] = true
	}
	wantSet := map[string]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range found {
		if !wantSet[n] {
			t.Errorf("the script now uses %q, which no stub covers — add it to expectedShims and re-check every absence proof in this package", n)
		}
	}
	for _, n := range want {
		if !foundSet[n] {
			t.Errorf("expectedShims lists %q but the script no longer mentions it — the list has gone stale", n)
		}
	}

	// Every name we build a stub for must end up executable on the stub PATH.
	dir := shimDir(t, found)
	for _, n := range found {
		info, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("stub %q was not created: %v", n, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("stub %q is not executable (mode %v) — it would silently fall through to the real binary", n, info.Mode())
		}
	}
}
