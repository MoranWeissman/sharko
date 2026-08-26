// Package serverrender holds render-level guards on the Sharko server
// chart (charts/sharko) — the chart an operator actually installs.
//
// It asks its questions of the RENDERED Kubernetes objects, never of the
// template text. A grep for `enableServiceLinks: false` over
// deployment.yaml would pass while the line sat inside a container, or
// inside an `{{- if }}` nobody's values reach, or commented out in a
// Go-template comment. The only thing that settles it is what helm
// actually emits, parsed as YAML and read as a Pod specification.
//
// # Why the chart turns service links off
//
// kubelet gives every Pod environment variables named after each Service
// in the namespace, in the old Docker-link format
// (kubernetes.io/docs/concepts/services-networking/service). For a
// Service named sharko on port 80 that includes:
//
//	SHARKO_SERVICE_HOST=10.96.35.88
//	SHARKO_SERVICE_PORT=80
//	SHARKO_PORT=tcp://10.96.35.88:80
//	SHARKO_PORT_80_TCP=tcp://10.96.35.88:80
//	SHARKO_PORT_80_TCP_PROTO=tcp
//	SHARKO_PORT_80_TCP_PORT=80
//	SHARKO_PORT_80_TCP_ADDR=10.96.35.88
//
// So Sharko's own Pod is handed roughly ten SHARKO_* variables no
// operator ever set, one of which — SHARKO_PORT — is the name Sharko's
// own listen-port setting used to have. The field defaults to true, so
// the chart has to say false explicitly.
//
// A list of Kubernetes' variable names to skip was considered and
// rejected: the names are derived from the Service name, so the list
// would be wrong the moment somebody installs under a different release
// name. Turning the injection off is the only fix that does not depend
// on guessing what the names will be.
package serverrender

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoRoot resolves the repository root from this test file's location,
// so the test works whatever directory `go test` was invoked from.
// Mirrors tests/enginerender and tests/bootstraprender.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <root>/tests/serverrender/render_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// renderServerChart runs `helm template` over charts/sharko and returns
// every rendered document.
//
// helm is required, not optional. This follows
// internal/metrics/contract_consumers_test.go rather than the older
// skip-if-missing convention in tests/enginerender: a missing helm binary
// must not turn a guard on the shipped chart into a silent pass, and this
// chart is the one an operator installs verbatim.
func renderServerChart(t *testing.T, extraArgs ...string) []k8sObject {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH, so the shipped chart cannot be rendered: %v", err)
	}

	root := repoRoot(t)
	args := append([]string{"template", "sharko", filepath.Join(root, "charts", "sharko")}, extraArgs...)
	cmd := exec.Command(helm, args...)
	cmd.Dir = root
	// Only stdout. helm writes kubeconfig-permission warnings to stderr,
	// and folding those into the YAML makes the render look unparseable.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr.String())
	}

	var objects []k8sObject
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var obj k8sObject
		decodeErr := decoder.Decode(&obj)
		if decodeErr != nil {
			break
		}
		if obj.Kind == "" {
			continue // an empty document between --- separators
		}
		objects = append(objects, obj)
	}
	if len(objects) == 0 {
		t.Fatalf("charts/sharko rendered no objects at all:\n%s", out)
	}
	return objects
}

// The shapes below are deliberately partial: only the fields these
// guards read. Everything else in the render is somebody else's test.

type k8sObject struct {
	Kind     string   `yaml:"kind"`
	Metadata metadata `yaml:"metadata"`
	Spec     objSpec  `yaml:"spec"`

	// Secret payloads. Only the KEYS are read — a key of a Secret a
	// container pulls in with envFrom is an environment variable name,
	// and SHARKO_ENCRYPTION_KEY reaches the server no other way. The
	// values are never looked at.
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
}

type metadata struct {
	Name string `yaml:"name"`
}

type objSpec struct {
	// Deployment / StatefulSet / DaemonSet / Job all carry the Pod
	// template here.
	Template *podTemplate `yaml:"template"`
	// CronJob nests one level deeper.
	JobTemplate *struct {
		Spec struct {
			Template *podTemplate `yaml:"template"`
		} `yaml:"spec"`
	} `yaml:"jobTemplate"`
}

type podTemplate struct {
	Spec podSpec `yaml:"spec"`
}

type podSpec struct {
	// A pointer so "absent" and "explicitly false" are different
	// answers. Absent means the Kubernetes default of true applies,
	// which is the bug.
	EnableServiceLinks *bool       `yaml:"enableServiceLinks"`
	Containers         []container `yaml:"containers"`
	InitContainers     []container `yaml:"initContainers"`
	Volumes            []struct{}  `yaml:"-"`
}

type container struct {
	Name    string        `yaml:"name"`
	Image   string        `yaml:"image"`
	Env     []envVar      `yaml:"env"`
	EnvFrom []envFromItem `yaml:"envFrom"`
}

type envFromItem struct {
	SecretRef struct {
		Name string `yaml:"name"`
	} `yaml:"secretRef"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// sharkoImageRepo is the image a Pod running Sharko's own server uses.
// A Pod is "a Sharko Pod" when one of its containers runs this image —
// that is the property that decides whether the SHARKO_* injection
// matters, not the object's name or its labels.
const sharkoImageRepo = "ghcr.io/moranweissman/sharko"

// podSpecs returns every Pod specification in the render, with a label
// naming where it came from so a failure says which object is wrong.
func podSpecs(objects []k8sObject) map[string]podSpec {
	out := map[string]podSpec{}
	for _, obj := range objects {
		switch {
		case obj.Spec.Template != nil:
			out[obj.Kind+"/"+obj.Metadata.Name] = obj.Spec.Template.Spec
		case obj.Spec.JobTemplate != nil && obj.Spec.JobTemplate.Spec.Template != nil:
			out[obj.Kind+"/"+obj.Metadata.Name] = obj.Spec.JobTemplate.Spec.Template.Spec
		}
	}
	return out
}

func (p podSpec) runsSharko() bool {
	for _, c := range append(append([]container{}, p.Containers...), p.InitContainers...) {
		if strings.Contains(c.Image, sharkoImageRepo) {
			return true
		}
	}
	return false
}

// TestEveryPodRunningSharkoDisablesServiceLinks is the guard.
//
// It is written over every Pod in the render rather than over the one
// Deployment on purpose: the next workload somebody adds that runs the
// Sharko image is caught by this test on the day it is added, without
// anybody remembering this rule exists.
func TestEveryPodRunningSharkoDisablesServiceLinks(t *testing.T) {
	// ai.enabled + ollama.deploy brings a SECOND Pod into the render
	// that does NOT run Sharko, so the rule is exercised in both
	// directions in one pass.
	objects := renderServerChart(t,
		"--set", "ai.enabled=true",
		"--set", "ai.provider=ollama",
		"--set", "ai.ollama.deploy=true",
	)

	pods := podSpecs(objects)
	if len(pods) < 2 {
		t.Fatalf("only %d Pod specification(s) in the render — expected at least the Sharko "+
			"Deployment and the Ollama Deployment, so this guard is not seeing what it thinks it is: %v",
			len(pods), keys(pods))
	}

	var sharkoPods int
	for where, spec := range pods {
		if !spec.runsSharko() {
			continue
		}
		sharkoPods++
		if spec.EnableServiceLinks == nil {
			t.Errorf("%s runs Sharko but its Pod specification does not set enableServiceLinks. "+
				"The field defaults to TRUE, so Kubernetes injects a SHARKO_* variable for every "+
				"Service in the namespace — including SHARKO_PORT=tcp://<ip>:<port>, the name "+
				"Sharko's own listen-port setting used to have. Set enableServiceLinks: false in "+
				"the Pod spec (a sibling of containers, not a container field).", where)
			continue
		}
		if *spec.EnableServiceLinks {
			t.Errorf("%s runs Sharko and sets enableServiceLinks: true, which is the thing this "+
				"rule exists to prevent.", where)
		}
	}

	if sharkoPods == 0 {
		t.Fatal("no Pod in the render runs the Sharko image. Either the image repository moved and " +
			"sharkoImageRepo is stale, or the guard is now checking nothing at all.")
	}
}

// TestTheGuardCanTellTheTwoPodsApart proves the rule above is looking at
// the image and not at anything that happens to be true of every Pod.
//
// Without this, a bug that made runsSharko always return false would
// leave TestEveryPodRunningSharkoDisablesServiceLinks reporting ok
// forever — it would find nothing to check and say nothing about it.
// (The sharkoPods == 0 fatal covers that specific shape; this covers the
// opposite one, where everything looks like Sharko.)
func TestTheGuardCanTellTheTwoPodsApart(t *testing.T) {
	objects := renderServerChart(t,
		"--set", "ai.enabled=true",
		"--set", "ai.provider=ollama",
		"--set", "ai.ollama.deploy=true",
	)

	var sharko, notSharko []string
	for where, spec := range podSpecs(objects) {
		if spec.runsSharko() {
			sharko = append(sharko, where)
			continue
		}
		notSharko = append(notSharko, where)
	}
	if len(sharko) == 0 {
		t.Error("no Pod was identified as running Sharko")
	}
	if len(notSharko) == 0 {
		t.Error("every Pod in the render was identified as running Sharko, including the Ollama " +
			"Deployment. The image check is not discriminating, so the rule would pass whatever the chart said.")
	}
}

// TestTheChartSetsTheCanonicalPortSetting pins the name the chart hands
// the server.
//
// The chart used to set SHARKO_PORT. That name is now a deprecated alias,
// so a chart still setting it would make every ordinary install log a
// deprecation warning about a variable the operator never touched.
func TestTheChartSetsTheCanonicalPortSetting(t *testing.T) {
	objects := renderServerChart(t)

	var found bool
	for where, spec := range podSpecs(objects) {
		if !spec.runsSharko() {
			continue
		}
		for _, c := range spec.Containers {
			for _, e := range c.Env {
				if e.Name == "SHARKO_PORT" {
					t.Errorf("%s container %q sets SHARKO_PORT, which is now a deprecated alias. "+
						"Set SHARKO_HTTP_PORT instead — otherwise every install warns about a "+
						"setting its operator never chose.", where, c.Name)
				}
				if e.Name != "SHARKO_HTTP_PORT" {
					continue
				}
				found = true
				if e.Value != "8080" {
					t.Errorf("%s container %q sets SHARKO_HTTP_PORT to %q; the container port the "+
						"chart declares is 8080, so the two have drifted apart", where, c.Name, e.Value)
				}
			}
		}
	}
	if !found {
		t.Error("no container in the render sets SHARKO_HTTP_PORT — the chart no longer tells the " +
			"server which port to listen on")
	}
}

func keys(m map[string]podSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
