package serverrender

// Guards on the Ollama deployment charts/sharko renders when an operator
// turns the local AI on (ai.enabled + ai.provider=ollama + ai.ollama.deploy).
//
// The rule being pinned is one sentence: this deployment may not report
// itself ready without the model it was told to serve.
//
// Three separate ways the chart used to break that sentence, all of which
// these guards now catch:
//
//   - The download lived inside `{{- if .Values.ai.ollama.persistence }}`,
//     and persistence ships off. On the documented default install there
//     was no download step in the pod at all.
//   - The download script had no `set -e`, and the container's exit status
//     came from the shutdown line that followed. A download that failed
//     ended the container with status 0.
//   - Readiness was an HTTP GET of /api/tags. An Ollama holding no models
//     answers that with 200 and an empty list, so the pod went Ready with
//     nothing to serve.
//
// Everything here is asked of the RENDERED objects, never of the template
// text, for the reason render_test.go gives at the top of this package.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The model names below are deliberately nothing the chart would ever
// produce on its own. If a guard passed while reading a chart default,
// these names would not appear and the guard would fail — which is the
// point. A fixture that reads the same value its assertion reads proves
// nothing.
const (
	guardPrimaryModel = "guard-primary-model"
	guardAgentModel   = "guard-agent-model"
)

// ollamaCase is one supported combination of values. The list is built by
// nested loops in ollamaCases, never typed out, so a new dimension cannot
// be added to the chart and quietly skipped here.
type ollamaCase struct {
	persistence bool
	agentModel  bool
	gpu         bool
}

func (c ollamaCase) name() string {
	return fmt.Sprintf("persistence=%t/agentModel=%t/gpu=%t", c.persistence, c.agentModel, c.gpu)
}

// expectedPulls is the exact list of models this combination must download.
func (c ollamaCase) expectedPulls() []string {
	out := []string{guardPrimaryModel}
	if c.agentModel {
		out = append(out, guardAgentModel)
	}
	return out
}

func (c ollamaCase) helmArgs() []string {
	args := []string{
		"--set", "ai.enabled=true",
		"--set", "ai.provider=ollama",
		"--set", "ai.ollama.deploy=true",
		"--set", "ai.ollama.model=" + guardPrimaryModel,
		"--set", fmt.Sprintf("ai.ollama.persistence=%t", c.persistence),
		"--set", fmt.Sprintf("ai.ollama.gpu=%t", c.gpu),
	}
	agent := ""
	if c.agentModel {
		agent = guardAgentModel
	}
	return append(args, "--set", "ai.ollama.agentModel="+agent)
}

func ollamaCases() []ollamaCase {
	var out []ollamaCase
	for _, persistence := range []bool{true, false} {
		for _, agentModel := range []bool{true, false} {
			for _, gpu := range []bool{true, false} {
				out = append(out, ollamaCase{persistence: persistence, agentModel: agentModel, gpu: gpu})
			}
		}
	}
	return out
}

// ollamaCaseCount is the exact number of combinations, compared with !=.
// Two dimensions of two values plus a third of two is eight; if the chart
// grows a fourth switch and the loops above learn about it, this line has
// to be updated deliberately rather than drifting.
const ollamaCaseCount = 8

// --- the slice of a rendered object these guards read -----------------

type ollamaDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template *struct {
			Spec struct {
				Containers     []ollamaContainer `yaml:"containers"`
				InitContainers []ollamaContainer `yaml:"initContainers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type ollamaContainer struct {
	Name           string       `yaml:"name"`
	Image          string       `yaml:"image"`
	Args           []string     `yaml:"args"`
	ReadinessProbe *ollamaProbe `yaml:"readinessProbe"`
	LivenessProbe  *ollamaProbe `yaml:"livenessProbe"`
}

type ollamaProbe struct {
	HTTPGet *struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
	Exec *struct {
		Command []string `yaml:"command"`
	} `yaml:"exec"`
}

// renderOllamaCase renders the shipped chart for one combination.
//
// helm is required, not optional — same reasoning as renderServerChart in
// this package. A missing binary must never turn a guard on the chart an
// operator installs into a quiet pass.
func renderOllamaCase(t *testing.T, c ollamaCase) []ollamaDoc {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH, so the shipped chart cannot be rendered: %v", err)
	}
	root := repoRoot(t)
	args := append([]string{"template", "sharko", filepath.Join(root, "charts", "sharko")}, c.helmArgs()...)
	cmd := exec.Command(helm, args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed for %s: %v\n%s", c.name(), err, stderr.String())
	}

	var docs []ollamaDoc
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc ollamaDoc
		if decodeErr := decoder.Decode(&doc); decodeErr != nil {
			break
		}
		if doc.Kind == "" {
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		t.Fatalf("charts/sharko rendered no objects at all for %s:\n%s", c.name(), out)
	}
	return docs
}

// ollamaPod finds the workload whose main containers include one running
// an Ollama image. Named by what it runs, not by its object name, so a
// rename of the Deployment does not silently switch this guard off.
func ollamaPod(t *testing.T, docs []ollamaDoc, c ollamaCase) ollamaDoc {
	t.Helper()
	var found []ollamaDoc
	for _, doc := range docs {
		if doc.Spec.Template == nil {
			continue
		}
		for _, container := range doc.Spec.Template.Spec.Containers {
			if strings.Contains(container.Image, "ollama/ollama") {
				found = append(found, doc)
				break
			}
		}
	}
	if len(found) != 1 {
		var names []string
		for _, doc := range docs {
			names = append(names, doc.Kind+"/"+doc.Metadata.Name)
		}
		t.Fatalf("%s: expected exactly one workload running an Ollama image, found %d. "+
			"Either the AI values no longer render the Ollama deployment, or the image moved and "+
			"this guard is now checking nothing. Rendered objects: %v", c.name(), len(found), names)
	}
	return found[0]
}

var ollamaPullLine = regexp.MustCompile(`(?m)^[ \t]*ollama pull[ \t]+(.*)$`)

// pullTargets is the exact list of models the download script fetches, in
// the order it fetches them.
func pullTargets(script string) []string {
	var out []string
	for _, match := range ollamaPullLine.FindAllStringSubmatch(script, -1) {
		target := strings.TrimSpace(match[1])
		target = strings.Trim(target, `"'`)
		out = append(out, target)
	}
	return out
}

// TestTheModelIsFetchedOnEverySupportedInstall walks every supported
// combination of the Ollama values and insists the download step is there,
// fetching exactly the models that combination asked for.
//
// The persistence=false half of the matrix is the whole reason this test
// exists: persistence decides where a model is KEPT between restarts, and
// never decided whether one is fetched at all.
func TestTheModelIsFetchedOnEverySupportedInstall(t *testing.T) {
	cases := ollamaCases()
	if len(cases) != ollamaCaseCount {
		t.Fatalf("the value matrix produced %d combinations, not the %d this guard covers — "+
			"a dimension was added or removed without the count being reconsidered", len(cases), ollamaCaseCount)
	}

	for _, c := range cases {
		t.Run(c.name(), func(t *testing.T) {
			pod := ollamaPod(t, renderOllamaCase(t, c), c)
			inits := pod.Spec.Template.Spec.InitContainers
			if len(inits) == 0 {
				t.Fatalf("%s renders NO init container. Nothing downloads a model, so this Ollama "+
					"starts empty and every AI request against it fails. The download must not be "+
					"conditional on persistence.", c.name())
			}

			var scripts []string
			for _, init := range inits {
				scripts = append(scripts, strings.Join(init.Args, "\n"))
			}
			script := strings.Join(scripts, "\n")

			got := pullTargets(script)
			want := c.expectedPulls()
			if len(got) == 0 {
				t.Fatalf("%s has an init container but it downloads nothing:\n%s", c.name(), script)
			}
			gotSorted, wantSorted := append([]string{}, got...), append([]string{}, want...)
			sort.Strings(gotSorted)
			sort.Strings(wantSorted)
			if strings.Join(gotSorted, ",") != strings.Join(wantSorted, ",") {
				t.Errorf("%s downloads %v, expected exactly %v. A model missing from this list is a "+
					"model the pod cannot serve; a model that should no longer be here is a stale entry.",
					c.name(), got, want)
			}
		})
	}
}

// TestAFailedDownloadEndsTheSetupStep pins that nothing in the download
// script can hide a failure.
//
// The container's exit status is what Kubernetes reads. While the script
// ran without `set -e`, that status came from the shutdown line at the
// bottom, so a download that failed still looked like a clean run.
func TestAFailedDownloadEndsTheSetupStep(t *testing.T) {
	for _, c := range ollamaCases() {
		t.Run(c.name(), func(t *testing.T) {
			pod := ollamaPod(t, renderOllamaCase(t, c), c)
			inits := pod.Spec.Template.Spec.InitContainers
			if len(inits) == 0 {
				t.Fatalf("%s renders no init container, so there is no download script to check", c.name())
			}

			for _, init := range inits {
				script := strings.Join(init.Args, "\n")
				lines := strings.Split(script, "\n")

				var setEAt, firstPullAt, lastPullAt = -1, -1, -1
				for i, raw := range lines {
					line := strings.TrimSpace(raw)
					if line == "set -e" || line == "set -eu" || line == "set -ex" {
						if setEAt == -1 {
							setEAt = i
						}
					}
					if strings.HasPrefix(line, "ollama pull") {
						if firstPullAt == -1 {
							firstPullAt = i
						}
						lastPullAt = i
					}
				}

				if setEAt == -1 {
					t.Errorf("%s: the download script never turns on abort-on-error. A failed download "+
						"leaves the script running and the container exits with whatever the last line "+
						"returned:\n%s", c.name(), script)
				}
				if firstPullAt == -1 {
					t.Fatalf("%s: no download line found in the script, so this guard is checking "+
						"nothing:\n%s", c.name(), script)
				}
				if setEAt != -1 && setEAt > firstPullAt {
					t.Errorf("%s: abort-on-error is turned on at line %d, AFTER the first download at "+
						"line %d, so that download's failure is not caught:\n%s",
						c.name(), setEAt+1, firstPullAt+1, script)
				}

				for i, raw := range lines {
					line := strings.TrimSpace(raw)
					if !strings.HasPrefix(line, "ollama pull") {
						continue
					}
					for _, swallow := range []string{"||", "&", ";", "|"} {
						if strings.Contains(line, swallow) {
							t.Errorf("%s: the download on line %d is joined with %q, which can hide its "+
								"failure. A download must stand alone so its exit status is the script's: %s",
								c.name(), i+1, swallow, line)
						}
					}
				}

				// The shutdown of the background server must come after
				// every download. Put before one, it becomes the status
				// the container reports.
				for i, raw := range lines {
					line := strings.TrimSpace(raw)
					if strings.HasPrefix(line, "kill ") && i < lastPullAt {
						t.Errorf("%s: the background server is stopped on line %d, before the last "+
							"download on line %d. That stop's exit status can end up standing in for a "+
							"failed download:\n%s", c.name(), i+1, lastPullAt+1, script)
					}
				}
			}
		})
	}
}

// TestReadinessMeansTheModelIsThere pins the probe.
//
// An Ollama with zero models answers GET /api/tags with HTTP 200 and an
// empty list. A probe that reads only that status code calls such a pod
// Ready, and every AI request afterwards fails with nothing pointing at
// the cause.
func TestReadinessMeansTheModelIsThere(t *testing.T) {
	for _, c := range ollamaCases() {
		t.Run(c.name(), func(t *testing.T) {
			pod := ollamaPod(t, renderOllamaCase(t, c), c)

			var checked int
			for _, container := range pod.Spec.Template.Spec.Containers {
				if !strings.Contains(container.Image, "ollama/ollama") {
					continue
				}
				checked++
				probe := container.ReadinessProbe
				if probe == nil {
					t.Errorf("%s: container %q has no readiness probe at all, so Kubernetes sends it "+
						"traffic the moment the process starts", c.name(), container.Name)
					continue
				}
				if probe.HTTPGet != nil {
					t.Errorf("%s: container %q is ready as soon as an HTTP request to %q returns 200. "+
						"Ollama answers that with 200 while holding no models, so this reports Ready for "+
						"a pod that cannot serve anything.", c.name(), container.Name, probe.HTTPGet.Path)
					continue
				}
				if probe.Exec == nil || len(probe.Exec.Command) == 0 {
					t.Errorf("%s: container %q has a readiness probe that runs nothing", c.name(), container.Name)
					continue
				}
				// What the probe DOES with those models is not asked here.
				// Reading the command text can only ever show that the
				// names appear in it, and both names appear whether the
				// two checks are joined by "and" or by "or".
				// TestTheReadinessProbeRunsAndSaysNoWithoutEveryModel runs
				// the command instead and reads its exit status.
			}
			if checked == 0 {
				t.Fatalf("%s: no container running an Ollama image was found in the workload this guard "+
					"picked, so it checked nothing", c.name())
			}
		})
	}
}

// --- running the probe, rather than reading it ------------------------

// stubPresentEnv names the models the fake `ollama` reports as present.
// stubLogEnv names the file it records every call in, so a run where the
// fake was never reached can be told apart from a run where it answered.
const (
	stubPresentEnv = "SHARKO_GUARD_PRESENT_MODELS"
	stubLogEnv     = "SHARKO_GUARD_STUB_LOG"
)

// ollamaStubScript is a stand-in for the real `ollama` binary. It answers
// `ollama show <name>` with success only when <name> is in the present
// list, and refuses every other subcommand.
const ollamaStubScript = `#!/bin/sh
printf '%s\n' "$*" >> "$` + stubLogEnv + `"
if [ "$1" != "show" ]; then
  exit 1
fi
for wanted in $` + stubPresentEnv + `; do
  if [ "$wanted" = "$2" ]; then
    exit 0
  fi
done
exit 1
`

// writeOllamaStub puts the fake `ollama` in its own directory and returns
// that directory, for putting first on PATH.
func writeOllamaStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ollama")
	if err := os.WriteFile(path, []byte(ollamaStubScript), 0o755); err != nil {
		t.Fatalf("could not write the stand-in ollama: %v", err)
	}
	return dir
}

// runWithStub runs one command with the stand-in `ollama` first on PATH
// and the given models reported present. It returns whether the command
// succeeded and how many times the stand-in was called.
func runWithStub(t *testing.T, command []string, present []string) (ok bool, calls int) {
	t.Helper()
	if len(command) == 0 {
		t.Fatal("asked to run an empty command, so nothing would be exercised")
	}
	stubDir := writeOllamaStub(t)
	logPath := filepath.Join(t.TempDir(), "calls.log")

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		stubPresentEnv+"="+strings.Join(present, " "),
		stubLogEnv+"="+logPath,
	)
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		ok = true
	case errors.As(runErr, &exitErr):
		ok = false
	default:
		t.Fatalf("the readiness command could not be run at all (%v), so its answer was never "+
			"observed: %v", runErr, command)
	}

	body, readErr := os.ReadFile(logPath)
	if readErr == nil {
		for _, line := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(line) != "" {
				calls++
			}
		}
	}
	return ok, calls
}

// readinessRun is one behaviour to check: with these models present on
// this install, is the pod supposed to read as Ready?
type readinessRun struct {
	agentModel bool
	present    []string
	wantReady  bool
}

func (r readinessRun) name() string {
	shown := "none"
	if len(r.present) > 0 {
		shown = strings.Join(r.present, "+")
	}
	return fmt.Sprintf("agentModel=%t/present=%s", r.agentModel, shown)
}

// readinessRuns builds every combination by walking the models each
// install requires and taking every subset of them as the "present" set.
// Nothing here is typed out by hand.
func readinessRuns() []readinessRun {
	var out []readinessRun
	for _, agentModel := range []bool{false, true} {
		required := ollamaCase{agentModel: agentModel}.expectedPulls()
		for mask := 0; mask < 1<<len(required); mask++ {
			var present []string
			for i, model := range required {
				if mask&(1<<i) != 0 {
					present = append(present, model)
				}
			}
			out = append(out, readinessRun{
				agentModel: agentModel,
				present:    present,
				wantReady:  len(present) == len(required),
			})
		}
	}
	return out
}

// readinessRunCount is one subset per required model set: two for the
// install with only the primary model, four for the install that also
// asks for an agent model.
const readinessRunCount = 6

// TestTheStandInOllamaAnswersBothWays proves the fake used below actually
// distinguishes a present model from a missing one. Without this, a fake
// that failed on everything would make every "not Ready" case pass for
// entirely the wrong reason.
func TestTheStandInOllamaAnswersBothWays(t *testing.T) {
	present, calls := runWithStub(t, []string{"/bin/sh", "-c", "ollama show " + guardPrimaryModel},
		[]string{guardPrimaryModel})
	if !present {
		t.Errorf("the stand-in ollama refused a model it was told is present, so every check below "+
			"would fail for the wrong reason (%d call(s) recorded)", calls)
	}
	if calls == 0 {
		t.Fatal("the stand-in ollama was never called, so it is not the binary being reached")
	}

	absent, _ := runWithStub(t, []string{"/bin/sh", "-c", "ollama show " + guardAgentModel},
		[]string{guardPrimaryModel})
	if absent {
		t.Error("the stand-in ollama accepted a model it was told is missing, so every check below " +
			"would pass for the wrong reason")
	}
}

// TestTheReadinessProbeRunsAndSaysNoWithoutEveryModel takes the readiness
// command the chart renders and RUNS it, against a stand-in `ollama` that
// reports which models are there.
//
// Reading the command text cannot tell the two joins apart: whether the
// two model checks are joined by "and" or by "or", both model names are
// in the string either way. With "or" the pod reports Ready while holding
// only one of the two models it was told to serve, and every request that
// needs the other one fails. The only way to see the difference is to run
// the command and read what it returns.
func TestTheReadinessProbeRunsAndSaysNoWithoutEveryModel(t *testing.T) {
	runs := readinessRuns()
	if len(runs) != readinessRunCount {
		t.Fatalf("the behaviour matrix produced %d combinations, not the %d this guard covers — "+
			"the models an install requires changed without this count being reconsidered",
			len(runs), readinessRunCount)
	}

	commands := map[bool][]string{}
	for _, agentModel := range []bool{false, true} {
		c := ollamaCase{agentModel: agentModel}
		pod := ollamaPod(t, renderOllamaCase(t, c), c)

		var found int
		for _, container := range pod.Spec.Template.Spec.Containers {
			if !strings.Contains(container.Image, "ollama/ollama") {
				continue
			}
			found++
			probe := container.ReadinessProbe
			if probe == nil || probe.Exec == nil || len(probe.Exec.Command) == 0 {
				t.Fatalf("%s: container %q has no readiness command to run, so this guard would "+
					"check nothing", c.name(), container.Name)
			}
			commands[agentModel] = probe.Exec.Command
		}
		if found != 1 {
			t.Fatalf("%s: expected exactly one container running an Ollama image, found %d",
				c.name(), found)
		}
	}
	if len(commands) != 2 {
		t.Fatalf("only %d readiness command(s) were collected, expected 2 — one install shape was "+
			"never rendered", len(commands))
	}

	var exercised int
	for _, run := range runs {
		t.Run(run.name(), func(t *testing.T) {
			command := commands[run.agentModel]
			ready, calls := runWithStub(t, command, run.present)
			if calls == 0 {
				t.Fatalf("the readiness command never reached the stand-in ollama, so its answer "+
					"says nothing about the models: %v", command)
			}
			exercised++

			switch {
			case run.wantReady && !ready:
				t.Errorf("every model this install needs is there, yet the readiness command "+
					"refuses, so the pod would never take traffic. Present: %v. Command: %v",
					run.present, command)
			case !run.wantReady && ready:
				t.Errorf("the readiness command succeeds while only %v of the models this install "+
					"needs are there. The pod would be sent traffic it cannot answer — every check "+
					"must have to pass, not just one of them. Command: %v", run.present, command)
			}
		})
	}
	if exercised != readinessRunCount {
		t.Fatalf("only %d of %d combinations actually ran the readiness command",
			exercised, readinessRunCount)
	}
}

// TestTheOllamaImageStaysPinned keeps BF2's pin in place.
//
// ":latest" means two installs a month apart run different software with
// no record of the change.
func TestTheOllamaImageStaysPinned(t *testing.T) {
	for _, c := range ollamaCases() {
		t.Run(c.name(), func(t *testing.T) {
			pod := ollamaPod(t, renderOllamaCase(t, c), c)
			all := append(append([]ollamaContainer{}, pod.Spec.Template.Spec.Containers...),
				pod.Spec.Template.Spec.InitContainers...)

			var checked int
			for _, container := range all {
				if !strings.Contains(container.Image, "ollama/ollama") {
					continue
				}
				checked++
				_, tag, found := strings.Cut(container.Image, ":")
				if !found || tag == "" {
					t.Errorf("%s: container %q runs %q with no version after the colon",
						c.name(), container.Name, container.Image)
					continue
				}
				if tag == "latest" {
					t.Errorf("%s: container %q runs %q. A moving tag means two installs a month apart "+
						"run different software with no record of the change.",
						c.name(), container.Name, container.Image)
				}
			}
			if checked < 2 {
				t.Fatalf("%s: only %d container(s) running an Ollama image were checked — expected the "+
					"download step and the server itself, so this guard is not seeing what it thinks it is",
					c.name(), checked)
			}
		})
	}
}

// TestTheDefaultModelIsFetchedToo covers the install where the operator
// names no model at all and takes the chart's own default.
//
// The other guards set a sentinel model name so they cannot pass by
// reading a chart default. This one deliberately does not, so the plain
// documented install is covered as well — it asserts only that SOME model
// is fetched and that the readiness probe asks after that same model,
// never what the default's name happens to be.
func TestTheDefaultModelIsFetchedToo(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH: %v", err)
	}
	root := repoRoot(t)
	cmd := exec.Command(helm, "template", "sharko", filepath.Join(root, "charts", "sharko"),
		"--set", "ai.enabled=true", "--set", "ai.provider=ollama", "--set", "ai.ollama.deploy=true")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr.String())
	}

	var docs []ollamaDoc
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc ollamaDoc
		if decodeErr := decoder.Decode(&doc); decodeErr != nil {
			break
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		t.Fatalf("the chart rendered no objects at all:\n%s", out)
	}

	pod := ollamaPod(t, docs, ollamaCase{})
	inits := pod.Spec.Template.Spec.InitContainers
	if len(inits) == 0 {
		t.Fatal("the plain documented install renders no init container, so no model is ever " +
			"downloaded and the pod starts empty")
	}

	targets := pullTargets(strings.Join(inits[0].Args, "\n"))
	if len(targets) != 1 {
		t.Fatalf("the plain install downloads %d model(s), expected exactly 1: %v", len(targets), targets)
	}
	if targets[0] == "" {
		t.Fatal("the plain install's download names no model at all")
	}

	var probeSeen bool
	for _, container := range pod.Spec.Template.Spec.Containers {
		if !strings.Contains(container.Image, "ollama/ollama") {
			continue
		}
		probeSeen = true
		probe := container.ReadinessProbe
		if probe == nil || probe.Exec == nil {
			t.Errorf("the plain install's readiness probe for %q does not run a check of its own",
				container.Name)
			continue
		}
		if !strings.Contains(strings.Join(probe.Exec.Command, " "), targets[0]) {
			t.Errorf("the plain install downloads %q but its readiness probe never mentions it: %v",
				targets[0], probe.Exec.Command)
		}
	}
	if !probeSeen {
		t.Fatal("no container running an Ollama image was found, so this guard checked nothing")
	}
}
