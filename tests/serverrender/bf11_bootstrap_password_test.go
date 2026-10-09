package serverrender

// bf11_bootstrap_password_test.go — the bootstrap admin password must reach
// the Pod only through a Kubernetes Secret reference.
//
// # What was wrong
//
// An operator who set bootstrapAdmin.password got the plaintext written into
// the Deployment as an ordinary environment value:
//
//	- name: SHARKO_BOOTSTRAP_ADMIN_PASSWORD
//	  value: "<the password>"
//
// A Deployment is not a Secret. Anyone with read access to Deployments in the
// namespace could read it, and so could `helm get manifest`, `kubectl describe
// pod`, and any GitOps repository holding rendered manifests. The value was
// also already in the chart's own Secret in bcrypt form, so the plaintext in
// the Deployment bought nothing.
//
// # What the chart does now
//
// secret.yaml writes the plaintext into the chart's Secret under
// admin.bootstrapPassword, and deployment.yaml references that one key. The
// key is not admin.password: that holds a bcrypt hash, and the server needs
// the plaintext because internal/auth/store.go SeedBootstrapAdminFromEnv does
// the hashing itself.
//
// # The three paths, all pinned below
//
//	default          — no password set, no bootstrap environment entry at all
//	explicit value   — bootstrapAdmin.password, read from the chart's Secret
//	existing secret  — bootstrapAdmin.existingSecret, read from the operator's

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// bf11Sentinel is the synthetic password every guard here renders with. It is
// a made-up string chosen so that a hit anywhere is unambiguous.
const bf11Sentinel = "BF11-PLANTED-BOOTSTRAP-PASSWORD-9f3a2c"

// bootstrapPasswordKey is the key deployment.yaml references and secret.yaml
// writes. Written out here rather than read from the chart on purpose: a test
// that reads the name from the same place the chart does would still pass if
// both moved together and the Pod stopped starting.
const bootstrapPasswordKey = "admin.bootstrapPassword"

// theSecret is the chart's own Secret under the release name these tests use.
const theSecret = "Secret/sharko"

// theDeployment is the Sharko Deployment under the same release name.
const theDeployment = "Deployment/sharko"

// --------------------------------------------------------------------------
// positive control 1 — every carrier is detectable at all
// --------------------------------------------------------------------------

// TestTheSweepFindsAPlantedValueInEveryCarrier is the control the rest of this
// file rests on.
//
// The chart offers no values door into the Sharko container's command or args,
// and none into the Deployment's annotations or labels, so a planted value
// cannot be pushed through those carriers by rendering. That does not make
// them safe to leave unchecked — it makes them carriers whose DETECTABILITY
// has to be proved some other way. So this control hands the sweep a
// hand-built document with the sentinel in each carrier and pins the exact
// list of paths it comes back with.
//
// It is a list, not a count: a carrier that stops being found fails here by
// name, and a carrier nobody has added yet fails here as an unexpected extra.
func TestTheSweepFindsAPlantedValueInEveryCarrier(t *testing.T) {
	planted := map[string]any{
		"kind": "Deployment",
		"metadata": map[string]any{
			"name":        "sharko",
			"annotations": map[string]any{"example.com/note": "carried in " + bf11Sentinel},
			"labels":      map[string]any{"example.com/tag": bf11Sentinel},
		},
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{"example.com/pod-note": bf11Sentinel},
					"labels":      map[string]any{"example.com/pod-tag": bf11Sentinel},
				},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":    "sharko",
							"command": []any{"/bin/sh", "-c"},
							"args":    []any{"run --password=" + bf11Sentinel},
							"env": []any{
								map[string]any{"name": "SAFE", "value": "nothing here"},
								map[string]any{"name": "DERIVED", "value": "https://host/?t=" + bf11Sentinel},
							},
						},
					},
					"initContainers": []any{
						map[string]any{
							"name": "setup",
							"args": []any{bf11Sentinel},
						},
					},
				},
			},
		},
	}

	want := []string{
		"metadata.annotations.example.com/note",
		"metadata.labels.example.com/tag",
		"spec.template.metadata.annotations.example.com/pod-note",
		"spec.template.metadata.labels.example.com/pod-tag",
		"spec.template.spec.containers[0].args[0]",
		"spec.template.spec.containers[0].env[1].value",
		"spec.template.spec.initContainers[0].args[0]",
	}
	got := findText(planted, bf11Sentinel)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the sweep does not find a planted value in every carrier.\nwanted:\n  %s\ngot:\n  %s\n"+
			"A carrier missing from the second list is a carrier the guards below would report clean "+
			"whether it was clean or not.", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}

	// And the same sweep says nothing about a document that does not hold the
	// value — otherwise it would report every carrier as a hit always.
	clean := map[string]any{"kind": "Deployment", "metadata": map[string]any{"name": "sharko"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "sharko", "args": []any{"run"}}}}}}}
	if hits := findText(clean, bf11Sentinel); len(hits) != 0 {
		t.Fatalf("the sweep reports hits in a document that does not contain the value: %v", hits)
	}
}

// TestTheSweepFindsAPlantedValueInTheRealRender is the second half of the
// control: the machinery above also works on what helm actually emits, not
// only on a map a test built.
//
// Two real values doors are used, one for the environment carrier and one for
// the container-argument carrier. ai.ollama.model reaches the Ollama
// container's args, which is the only argv in the chart any value can reach.
func TestTheSweepFindsAPlantedValueInTheRealRender(t *testing.T) {
	render := renderChart(t,
		"--set-string", "extraEnv[0].name=BF11_CONTROL",
		"--set-string", "extraEnv[0].value="+bf11Sentinel,
		"--set", "ai.enabled=true",
		"--set-string", "ai.provider=ollama",
		"--set", "ai.ollama.deploy=true",
		"--set-string", "ai.ollama.model="+bf11Sentinel,
	)

	envHits := findText(render.docsByName()[theDeployment], bf11Sentinel)
	if len(envHits) == 0 {
		t.Fatal("planted a value into the Deployment through extraEnv and the sweep did not find it. " +
			"Every absence this file asserts about the Deployment would be meaningless.")
	}

	var argvHit bool
	for _, hit := range render.sweep(bf11Sentinel) {
		if strings.Contains(hit, ".args[") || strings.Contains(hit, ".command[") {
			argvHit = true
		}
	}
	if !argvHit {
		t.Fatal("planted a value into a container's arguments through ai.ollama.model and the sweep " +
			"did not find it there. The container-argument carrier is not proved detectable in a real render.")
	}
}

// --------------------------------------------------------------------------
// the guard itself
// --------------------------------------------------------------------------

// TestAnInlineBootstrapPasswordIsNowhereInTheRenderButTheSecret is the BF11
// rule.
func TestAnInlineBootstrapPasswordIsNowhereInTheRenderButTheSecret(t *testing.T) {
	render := renderChart(t, "--set-string", "bootstrapAdmin.password="+bf11Sentinel)

	// Positive control on THIS render: the value is in the input, so the
	// sweep must find it in the Secret. Without this, a typo in the --set
	// flag would make every absence below true and meaningless.
	secretHits := findText(render.docsByName()[theSecret], bf11Sentinel)
	if len(secretHits) == 0 {
		t.Fatalf("the synthetic password is not in the rendered Secret, so it never entered this render "+
			"at all and the absences below prove nothing. Secret was:\n%v", render.docsByName()[theSecret])
	}
	wantSecretHits := []string{"stringData." + bootstrapPasswordKey}
	if strings.Join(secretHits, ",") != strings.Join(wantSecretHits, ",") {
		t.Errorf("the password is in the Secret at %v; the only place it should be is %v",
			secretHits, wantSecretHits)
	}

	// The rule: nowhere else in the whole render.
	if hits := render.sweep(bf11Sentinel, theSecret); len(hits) != 0 {
		t.Errorf("the bootstrap admin password appears outside the Secret, at:\n  %s\n"+
			"A Deployment, a ConfigMap and a Service are readable by anyone who can read those object "+
			"kinds in the namespace. The password belongs in the Secret and is referenced from there.",
			strings.Join(hits, "\n  "))
	}

	// Said again about the Deployment on its own, and about the raw text of
	// its document rather than the parsed tree, so a value hiding in a
	// comment or in whitespace the parser drops is still caught.
	deploymentText := documentText(t, render.Raw, "templates/deployment.yaml")
	if strings.Contains(deploymentText, bf11Sentinel) {
		t.Error("the bootstrap admin password appears in the raw text of the rendered Deployment")
	}
}

// TestTheInlinePasswordReachesThePodThroughTheSecretKey pins the wiring: the
// right variable, from the right Secret, at the right key, and that key is
// actually in the Secret the reference names.
func TestTheInlinePasswordReachesThePodThroughTheSecretKey(t *testing.T) {
	render := renderChart(t, "--set-string", "bootstrapAdmin.password="+bf11Sentinel)

	name, key, literal := bootstrapEnvWiring(t, render)
	if literal != "" {
		t.Fatalf("SHARKO_BOOTSTRAP_ADMIN_PASSWORD is still written as a literal value in the Deployment")
	}
	if name != "sharko" {
		t.Errorf("the bootstrap password is read from Secret %q; on the inline path it must come from the "+
			"chart's own Secret, %q", name, "sharko")
	}
	if key != bootstrapPasswordKey {
		t.Errorf("the bootstrap password is read from key %q, wanted %q", key, bootstrapPasswordKey)
	}
	if key == "admin.password" {
		t.Error("the reference points at admin.password, which holds a bcrypt HASH. The server hashes " +
			"what it is given, so it would seed the account with the text of a hash and no operator " +
			"password would ever work.")
	}

	// The key has to exist, or the Pod does not start. This is the regression
	// that a Secret reference introduces and a literal value never could.
	secretKeys := secretKeysOf(t, render, "Secret/"+name)
	if !secretKeys[key] {
		t.Errorf("the Deployment reads key %q from Secret %q, but that Secret only has keys %v. "+
			"A container referencing a key that is not in the Secret never starts.",
			key, name, bf11SortedKeys(secretKeys))
	}
}

// TestTheExistingSecretPathIsUnchanged keeps the door that was already right.
func TestTheExistingSecretPathIsUnchanged(t *testing.T) {
	render := renderChart(t,
		"--set-string", "bootstrapAdmin.existingSecret.name=operator-owned",
		"--set-string", "bootstrapAdmin.existingSecret.key=bootstrap-password",
	)
	name, key, literal := bootstrapEnvWiring(t, render)
	if literal != "" {
		t.Fatal("the existing-secret path writes a literal value")
	}
	if name != "operator-owned" || key != "bootstrap-password" {
		t.Errorf("the existing-secret path reads %q/%q, wanted operator-owned/bootstrap-password", name, key)
	}
	// The chart must not copy anything of its own into that Secret, and must
	// not write the bootstrap key into its own Secret on this path.
	if secretKeysOf(t, render, theSecret)[bootstrapPasswordKey] {
		t.Errorf("the chart wrote %s into its own Secret on the existing-secret path. Nothing reads it "+
			"there, and it is a copy of an operator's credential the chart was never given.",
			bootstrapPasswordKey)
	}
}

// TestTheDefaultChartHasNoBootstrapPasswordEntry pins the third path. With no
// password set the chart generates one into the Secret and the Deployment
// carries no bootstrap entry at all.
func TestTheDefaultChartHasNoBootstrapPasswordEntry(t *testing.T) {
	render := renderChart(t)
	name, key, literal := bootstrapEnvWiring(t, render)
	if name != "" || key != "" || literal != "" {
		t.Errorf("the default chart sets SHARKO_BOOTSTRAP_ADMIN_PASSWORD (secret %q key %q literal %q); "+
			"with no operator password there is nothing to seed from", name, key, literal)
	}
	if secretKeysOf(t, render, theSecret)[bootstrapPasswordKey] {
		t.Errorf("the default chart writes %s into the Secret with no operator password set", bootstrapPasswordKey)
	}
}

// --------------------------------------------------------------------------
// small readers
// --------------------------------------------------------------------------

// bootstrapEnvWiring returns how SHARKO_BOOTSTRAP_ADMIN_PASSWORD reaches the
// Sharko container: the Secret name and key it is read from, or the literal
// value if the Deployment writes one. All three empty means the variable is
// not set at all.
func bootstrapEnvWiring(t *testing.T, render renderResult) (secretName, secretKey, literal string) {
	t.Helper()
	deployment, ok := render.docsByName()[theDeployment]
	if !ok {
		t.Fatalf("no %s in the render; this guard is not looking at the Sharko Deployment", theDeployment)
	}
	spec, _ := deployment["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("the Sharko Deployment has no containers")
	}
	var found int
	for _, raw := range containers {
		c, _ := raw.(map[string]any)
		env, _ := c["env"].([]any)
		for _, rawEnv := range env {
			e, _ := rawEnv.(map[string]any)
			if n, _ := e["name"].(string); n != "SHARKO_BOOTSTRAP_ADMIN_PASSWORD" {
				continue
			}
			found++
			if v, ok := e["value"].(string); ok {
				literal = v
			}
			valueFrom, _ := e["valueFrom"].(map[string]any)
			ref, _ := valueFrom["secretKeyRef"].(map[string]any)
			secretName, _ = ref["name"].(string)
			secretKey, _ = ref["key"].(string)
		}
	}
	if found > 1 {
		t.Fatalf("SHARKO_BOOTSTRAP_ADMIN_PASSWORD is set %d times in the Pod; only the last one takes "+
			"effect, so the chart is saying two different things", found)
	}
	return secretName, secretKey, literal
}

// secretKeysOf returns the key names of a rendered Secret. Only the keys —
// the values are never read, and never printed by a failure here.
func secretKeysOf(t *testing.T, render renderResult, which string) map[string]bool {
	t.Helper()
	doc, ok := render.docsByName()[which]
	if !ok {
		t.Fatalf("no %s in the render", which)
	}
	out := map[string]bool{}
	for _, section := range []string{"data", "stringData"} {
		m, _ := doc[section].(map[string]any)
		for k := range m {
			out[k] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s has no keys at all, so a question about which keys it holds cannot be answered", which)
	}
	return out
}

func bf11SortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// documentText returns the raw text of the one rendered document that came
// from the named template, using the "# Source: ..." header helm writes.
func documentText(t *testing.T, raw, sourceSuffix string) string {
	t.Helper()
	var matching []string
	for _, doc := range strings.Split(raw, "\n---\n") {
		for _, line := range strings.Split(doc, "\n") {
			if strings.HasPrefix(line, "# Source: ") && strings.HasSuffix(strings.TrimSpace(line), sourceSuffix) {
				matching = append(matching, doc)
				break
			}
		}
	}
	if len(matching) != 1 {
		t.Fatalf("expected exactly one rendered document from %s, found %d — this reader is not looking "+
			"at what it thinks it is", sourceSuffix, len(matching))
	}
	return matching[0]
}

// --------------------------------------------------------------------------
// the branch no render can reach
// --------------------------------------------------------------------------
//
// secret.yaml's first branch preserves an existing in-cluster Secret's data on
// upgrade. It only runs when `lookup` finds a Secret, and `helm template`
// never talks to a cluster, so that branch is invisible to every other guard
// in this file. It is also the branch where the Deployment's Secret reference
// is most likely to dangle: it is written to leave the existing credential
// alone, and leaving the bootstrap key alone means not writing it at all.
//
// # What BF12-4 changed here
//
// The old guard COUNTED. It looked for two occurrences of the literal that
// writes the key and required exactly 2. Two writes in the wrong branch, or
// two inside the same branch, still counts 2 — and a reviewer demonstrated
// exactly that: deleting the preserve-branch write and adding a second copy
// inside the inline branch under `{{- if false }}` left the count at 2, the
// chart rendering, and this package green, while three real upgrade sequences
// would leave the Pod pointing at a key the Secret does not carry.
//
// Two guards replace it. The first reads WHICH branch each write is in. The
// second stops reasoning about template text altogether and drives real
// renders through a faithful stand-in for `lookup`.

// bootstrapKeyWrite is the literal line secret.yaml uses to write the
// plaintext bootstrap password.
const bootstrapKeyWrite = `{{ include "sharko.bootstrapPasswordKey" . }}: {{ $operatorInlinePassword | quote }}`

// TestEveryBranchThatWritesTheKeyIsTheBranchItShouldBeIn reads the branch
// structure of secret.yaml rather than counting occurrences.
//
// For each write of the bootstrap key it records the chain of template
// conditions it sits inside, innermost last, and compares the whole set
// against what is written down here. A write that moves to another branch
// changes its chain and fails; a second write in a branch that already has one
// is an extra chain and fails; a missing one is a missing chain and fails.
func TestEveryBranchThatWritesTheKeyIsTheBranchItShouldBeIn(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "charts", "sharko", "templates", "secret.yaml"))
	if err != nil {
		t.Fatalf("reading secret.yaml: %v", err)
	}

	chains := branchChainsOf(string(body), bootstrapKeyWrite)

	// The Deployment renders the reference on exactly one condition: an
	// inline password and no existing-Secret name. Two branches of
	// secret.yaml can be entered while that is true — the upgrade branch
	// that preserves an existing Secret, and the first-install inline branch
	// — so the key has to be written in each of those two, and nowhere else.
	want := []string{
		// the upgrade / preserve branch
		"if and $existingSecret (index ($existingSecret.data | default dict) \"admin.password\") > " +
			"if and (not $useExistingSecret) (ne $operatorInlinePassword \"\")",
		// the first-install inline branch
		"else if ne $operatorInlinePassword \"\"",
	}
	sort.Strings(want)
	got := append([]string{}, chains...)
	sort.Strings(got)

	if len(got) == 0 {
		t.Fatal("secret.yaml never writes the bootstrap password key, so the Deployment's reference " +
			"dangles on every path and no Pod with an inline password starts")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the bootstrap password key is not written in the branches it has to be written in.\n"+
			"expected these branch chains:\n  %s\nfound:\n  %s\n"+
			"Both listed branches can run while the Deployment is rendering the reference, and a "+
			"reference to a missing key stops the Pod from starting.",
			strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

// TestTheBranchReaderCanTellTwoWritesInOneBranchFromOneInEach is the positive
// control on the reader above, and it is the exact shape the old counting
// guard could not tell apart.
func TestTheBranchReaderCanTellTwoWritesInOneBranchFromOneInEach(t *testing.T) {
	const marker = "WRITE"
	oneInEach := strings.Join([]string{
		`{{- if $a }}`,
		`  WRITE`,
		`{{- else if $b }}`,
		`  WRITE`,
		`{{- end }}`,
	}, "\n")
	bothInOne := strings.Join([]string{
		`{{- if $a }}`,
		`  WRITE`,
		`  {{- if false }}`,
		`  WRITE`,
		`  {{- end }}`,
		`{{- else if $b }}`,
		`{{- end }}`,
	}, "\n")

	if got := branchChainsOf(oneInEach, marker); strings.Join(got, " | ") != "if $a | else if $b" {
		t.Fatalf("the reader cannot see one write in each of two branches: %v", got)
	}
	if got := branchChainsOf(bothInOne, marker); strings.Join(got, " | ") != "if $a | if $a > if false" {
		t.Fatalf("the reader cannot tell two writes inside one branch from one write in each: %v", got)
	}
	// And the count is identical in both, which is why counting was not
	// enough.
	if strings.Count(oneInEach, marker) != strings.Count(bothInOne, marker) {
		t.Fatal("the two shapes no longer have the same number of writes, so this control no longer " +
			"demonstrates what the old guard could not see")
	}
}

// branchChainsOf returns, for every line containing needle, the chain of
// enclosing template conditions, innermost last, joined with " > ".
func branchChainsOf(body, needle string) []string {
	var stack []string
	var out []string
	blocks := regexp.MustCompile(`\{\{-?\s*(if|with|range|else if|else|end)\b([^}]*?)\s*-?\}\}`)
	for _, line := range strings.Split(body, "\n") {
		opens := blocks.FindAllStringSubmatch(line, -1)
		// An "else"/"else if" on this line replaces the innermost frame
		// BEFORE the line's own content is read, and "end" closes it.
		for _, m := range opens {
			keyword, subject := m[1], strings.TrimSpace(m[2])
			label := keyword
			if subject != "" {
				label = keyword + " " + subject
			}
			switch keyword {
			case "if", "with", "range":
				stack = append(stack, label)
			case "else", "else if":
				if len(stack) > 0 {
					stack[len(stack)-1] = label
				}
			case "end":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
		if strings.Contains(line, needle) {
			out = append(out, strings.Join(stack, " > "))
		}
	}
	return out
}

// --------------------------------------------------------------------------
// the same branch, proved by rendering instead of by reading
// --------------------------------------------------------------------------

// simScenario is one real install-or-upgrade sequence, expressed as what
// `lookup` would have found and what the operator has set.
type simScenario struct {
	name string
	// existing is the data a Secret already in the cluster carries, or nil
	// when there is no Secret yet.
	existing map[string]string
	// sets are the --set-string arguments for the operator's values.
	sets []string
}

// bootstrapSimScenarios are the nine sequences an operator can actually put a
// cluster through. They are the reviewer's nine, kept by name.
func bootstrapSimScenarios() []simScenario {
	const pw = bf11Sentinel
	return []simScenario{
		{"A fresh install with an inline password", nil,
			[]string{"bootstrapAdmin.password=" + pw}},
		{"B upgrade after A, the Secret already has admin.password, inline still set",
			map[string]string{"admin.password": "YmNyeXB0"},
			[]string{"bootstrapAdmin.password=" + pw}},
		{"C1 fresh install, password auto-generated", nil, nil},
		{"C2 upgrade after C1, the operator adds an inline password",
			map[string]string{"admin.password": "YmNyeXB0", "admin.initialPassword": "cGxhaW4="},
			[]string{"bootstrapAdmin.password=" + pw}},
		{"D1 fresh install with an existing Secret named", nil,
			[]string{"bootstrapAdmin.existingSecret.name=operator-secret"}},
		{"D2 that install switched to inline before Sharko wrote admin.password", nil,
			[]string{"bootstrapAdmin.password=" + pw}},
		{"E that install switched to inline after Sharko wrote admin.password",
			map[string]string{"admin.password": "YmNyeXB0"},
			[]string{"bootstrapAdmin.password=" + pw}},
		{"F an inline install switched to an existing Secret",
			map[string]string{"admin.password": "YmNyeXB0"},
			[]string{"bootstrapAdmin.existingSecret.name=operator-secret"}},
		{"G inline and existingSecret both set",
			map[string]string{"admin.password": "YmNyeXB0"},
			[]string{"bootstrapAdmin.password=" + pw, "bootstrapAdmin.existingSecret.name=operator-secret"}},
	}
}

// TestTheDeploymentReferenceAndTheSecretKeyMoveTogetherOnEveryRealSequence is
// the behavioural guard.
//
// `helm template` cannot reach the preserve branch, because that branch is
// gated on `lookup` and there is no cluster. So the chart is COPIED to a
// scratch directory outside the repository and the one `lookup` line is
// replaced with a values-driven stand-in. Nothing else is changed. Every
// scenario is then a real render of the real templates, and the check is made
// on the SAME render: does the Deployment reference the key, and does the
// Secret write it?
//
// This replaces reasoning about template text with a measurement.
func TestTheDeploymentReferenceAndTheSecretKeyMoveTogetherOnEveryRealSequence(t *testing.T) {
	chart := copyChartWithLookupStandIn(t, false)

	scenarios := bootstrapSimScenarios()
	if len(scenarios) == 0 {
		t.Fatal("no sequence was rendered, so this guard proves nothing")
	}
	for _, s := range scenarios {
		s := s
		t.Run(s.name, func(t *testing.T) {
			refs, hasKey := renderSim(t, chart, s)
			if refs != hasKey {
				t.Errorf("the Deployment references the bootstrap password key (%v) and the Secret "+
					"writes it (%v). One without the other means the Pod either cannot start or "+
					"cannot be given the password the operator set.", refs, hasKey)
			}
		})
	}
}

// TestTheSimulationCanSeeADanglingReference is the positive control on the
// simulation, planted into the SAME code path.
//
// The preserve-branch write is deleted from a second scratch copy — the exact
// regression the guard above exists to catch — and the sequences that go
// through that branch must come back with a reference and no key. If they do
// not, the simulation is blind and a green result above means nothing.
func TestTheSimulationCanSeeADanglingReference(t *testing.T) {
	chart := copyChartWithLookupStandIn(t, true)

	var dangling []string
	for _, s := range bootstrapSimScenarios() {
		refs, hasKey := renderSim(t, chart, s)
		if refs && !hasKey {
			dangling = append(dangling, s.name)
		}
	}
	sort.Strings(dangling)
	want := []string{
		"B upgrade after A, the Secret already has admin.password, inline still set",
		"C2 upgrade after C1, the operator adds an inline password",
		"E that install switched to inline after Sharko wrote admin.password",
	}
	if strings.Join(dangling, "\n") != strings.Join(want, "\n") {
		t.Fatalf("with the preserve-branch write deleted, the sequences that dangle are:\n  %s\n"+
			"expected exactly:\n  %s\n"+
			"The simulation is not seeing the failure it is supposed to see, so a clean run of "+
			"TestTheDeploymentReferenceAndTheSecretKeyMoveTogetherOnEveryRealSequence proves nothing.",
			strings.Join(dangling, "\n  "), strings.Join(want, "\n  "))
	}
}

// copyChartWithLookupStandIn copies charts/sharko into a scratch directory and
// replaces the one `lookup` call with a values-driven stand-in, so the branch
// no render can reach becomes reachable.
//
// deletePreserveWrite is the positive control: it also removes the
// preserve-branch write of the bootstrap key.
func copyChartWithLookupStandIn(t *testing.T, deletePreserveWrite bool) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "charts", "sharko")
	dst := filepath.Join(t.TempDir(), "sharko")

	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copying the chart to a scratch directory: %v", err)
	}

	secretPath := filepath.Join(dst, "templates", "secret.yaml")
	body, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("reading the copied secret.yaml: %v", err)
	}
	text := string(body)

	const lookupLine = `{{- $existingSecret := (lookup "v1" "Secret" .Release.Namespace $secretName) }}`
	const standIn = `{{- $existingSecret := .Values.simExisting }}`
	if strings.Count(text, lookupLine) != 1 {
		t.Fatalf("secret.yaml no longer contains exactly one lookup line to stand in for, so this "+
			"simulation is not rendering the branch it thinks it is. Looked for:\n%s", lookupLine)
	}
	text = strings.Replace(text, lookupLine, standIn, 1)

	if deletePreserveWrite {
		chains := branchChainsOf(text, bootstrapKeyWrite)
		if len(chains) != 2 {
			t.Fatalf("the scratch copy has %d writes of the bootstrap key, expected 2 — the control "+
				"below would then delete the wrong one", len(chains))
		}
		// Delete the FIRST one, which is the preserve branch.
		i := strings.Index(text, bootstrapKeyWrite)
		lineStart := strings.LastIndex(text[:i], "\n") + 1
		lineEnd := strings.Index(text[i:], "\n") + i + 1
		text = text[:lineStart] + text[lineEnd:]
		if strings.Count(text, bootstrapKeyWrite) != 1 {
			t.Fatalf("deleting the preserve-branch write left %d writes, expected 1",
				strings.Count(text, bootstrapKeyWrite))
		}
	}

	if err := os.WriteFile(secretPath, []byte(text), 0o600); err != nil {
		t.Fatalf("writing the scratch secret.yaml: %v", err)
	}
	return dst
}

// renderSim renders the scratch chart for one scenario and says whether the
// Deployment references the bootstrap key and whether the Secret writes it —
// read out of the SAME render.
func renderSim(t *testing.T, chart string, s simScenario) (refs, hasKey bool) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on PATH, so the chart cannot be rendered: %v", err)
	}

	args := []string{"template", "sharko", chart}
	if s.existing == nil {
		args = append(args, "--set", "simExisting=null")
	} else {
		keys := make([]string, 0, len(s.existing))
		for k := range s.existing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// The key names carry dots, so they are escaped for --set.
			escaped := strings.ReplaceAll(k, ".", `\.`)
			args = append(args, "--set-string", "simExisting.data."+escaped+"="+s.existing[k])
		}
	}
	for _, set := range s.sets {
		args = append(args, "--set-string", set)
	}

	cmd := exec.Command(helm, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		t.Fatalf("the scratch chart did not render for %s: %v\n%s", s.name, runErr, errBuf.String())
	}

	rendered := out.String()
	deployment := documentText(t, rendered, "templates/deployment.yaml")
	secret := documentText(t, rendered, "templates/secret.yaml")

	refs = strings.Contains(deployment, `key: `+bootstrapPasswordKey) ||
		strings.Contains(deployment, `key: "`+bootstrapPasswordKey+`"`)
	hasKey = strings.Contains(secret, bootstrapPasswordKey+":")
	return refs, hasKey
}
