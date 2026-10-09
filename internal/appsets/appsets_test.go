package appsets

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// parseYAML is the fixture helper: it turns an ApplicationSet document into
// the same map[string]interface{} shape the dynamic client hands us, so the
// tests exercise the real Parse path.
func parseYAML(t *testing.T, doc string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return out
}

func TestParse_DefaultSyncPolicyIsNotDeletionSafe(t *testing.T) {
	info := Parse(parseYAML(t, `
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: addons
  namespace: argocd
spec:
  generators:
    - clusters:
        selector:
          matchLabels:
            addons.sharko.dev/datadog: enabled
  template:
    metadata:
      name: '{{.name}}-datadog'
`))

	if info.Name != "addons" || info.Namespace != "argocd" {
		t.Fatalf("metadata not read: %+v", info)
	}
	if info.DeletionSafe() {
		t.Error("an ApplicationSet with no syncPolicy must NOT be treated as deletion-safe — the default deletes")
	}
	if !info.HasClusterGenerator {
		t.Error("cluster generator not detected")
	}
	if !info.SelectsLabelKey("addons.sharko.dev/datadog") {
		t.Errorf("selector key not collected: %+v", info.ClusterSelectorLabels)
	}
	if got := info.ClusterSelectorMatchLabels["addons.sharko.dev/datadog"]; got != "enabled" {
		t.Errorf("matchLabels value = %q, want %q", got, "enabled")
	}
}

func TestParse_PreserveResourcesOnDeletionIsSafe(t *testing.T) {
	info := Parse(parseYAML(t, `
metadata: {name: safe-one}
spec:
  syncPolicy:
    preserveResourcesOnDeletion: true
  generators:
    - clusters: {}
`))
	if !info.DeletionSafe() {
		t.Error("preserveResourcesOnDeletion: true must be deletion-safe")
	}
	if info.WhySafe() == "" {
		t.Error("a safe AppSet must be able to say why")
	}
}

func TestParse_ApplicationsSyncModes(t *testing.T) {
	cases := []struct {
		mode string
		safe bool
	}{
		{ApplicationsSyncCreateOnly, true},
		{ApplicationsSyncCreateUpdate, true},
		{ApplicationsSyncCreateDelete, false},
		{ApplicationsSyncSync, false},
		{"", false},
	}
	for _, tc := range cases {
		info := ApplicationSetInfo{ApplicationsSync: tc.mode}
		if got := info.DeletionSafe(); got != tc.safe {
			t.Errorf("applicationsSync=%q DeletionSafe()=%v, want %v", tc.mode, got, tc.safe)
		}
		if !tc.safe && info.WhyUnsafe() == "" {
			t.Errorf("applicationsSync=%q must explain why it is unsafe", tc.mode)
		}
	}
}

func TestParse_MatrixGeneratorSelectorsAreFound(t *testing.T) {
	info := Parse(parseYAML(t, `
metadata: {name: matrixed}
spec:
  generators:
    - matrix:
        generators:
          - clusters:
              selector:
                matchLabels:
                  env: prod
                matchExpressions:
                  - key: tier
                    operator: In
                    values: [gold]
          - git:
              repoURL: https://example.org/fleet
`))
	if !info.HasClusterGenerator {
		t.Fatal("cluster generator nested in a matrix was not found")
	}
	if !info.SelectsLabelKey("env") {
		t.Errorf("matchLabels key not collected: %+v", info.ClusterSelectorLabels)
	}
	if !info.SelectsLabelKey("tier") {
		t.Errorf("matchExpressions key not collected: %+v", info.ClusterSelectorLabels)
	}
	// matchExpressions contributes a key but no single required value.
	if _, ok := info.ClusterSelectorMatchLabels["tier"]; ok {
		t.Error("a matchExpressions key must not appear in the matchLabels map — it has no single value")
	}
}

// TestParse_NeverReadsTemplate is the enforcement of the promise in the
// package comment: whatever is in spec.template, nothing from it reaches
// the parsed result.
func TestParse_NeverReadsTemplate(t *testing.T) {
	info := Parse(parseYAML(t, `
metadata: {name: templated}
spec:
  syncPolicy:
    applicationsSync: create-only
  generators:
    - clusters: {}
  template:
    metadata:
      name: '{{.name}}-secret-sauce'
      labels:
        proprietary: very
    spec:
      source:
        repoURL: https://internal.example.org/private
`))
	if info.SelectsLabelKey("proprietary") {
		t.Error("a label from spec.template leaked into the selector set")
	}
	if len(info.ClusterSelectorMatchLabels) != 0 {
		t.Errorf("spec.template contributed to matchLabels: %+v", info.ClusterSelectorMatchLabels)
	}
}

func TestParse_MalformedDocumentDoesNotPanicAndIsNotSafe(t *testing.T) {
	for name, doc := range map[string]string{
		"spec is a string":       "metadata: {name: bad}\nspec: nonsense\n",
		"generators is a string": "metadata: {name: bad}\nspec:\n  generators: nope\n",
		"selector is a list":     "metadata: {name: bad}\nspec:\n  generators:\n    - clusters:\n        selector: [a, b]\n",
		"syncPolicy wrong type":  "metadata: {name: bad}\nspec:\n  syncPolicy: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			info := Parse(parseYAML(t, doc))
			if info.DeletionSafe() {
				t.Error("an ApplicationSet Sharko cannot fully read must never be reported as deletion-safe")
			}
		})
	}
	if got := Parse(nil); got.Name != "" {
		t.Errorf("Parse(nil) must return the zero value, got %+v", got)
	}
}

func TestNotDeletionSafeAndSelectingLabelKeys(t *testing.T) {
	all := []ApplicationSetInfo{
		{Name: "safe", PreserveResourcesOnDeletion: true, ClusterSelectorLabels: []string{"env"}},
		{Name: "risky", ClusterSelectorLabels: []string{"env", "team"}},
		{Name: "unrelated", ApplicationsSync: ApplicationsSyncCreateOnly, ClusterSelectorLabels: []string{"other"}},
	}

	unsafe := NotDeletionSafe(all)
	if len(unsafe) != 1 || unsafe[0].Name != "risky" {
		t.Errorf("NotDeletionSafe = %v, want just [risky]", Names(unsafe))
	}

	selecting := SelectingLabelKeys(all, []string{"env"})
	if got := Names(selecting); len(got) != 2 || got[0] != "safe" || got[1] != "risky" {
		t.Errorf("SelectingLabelKeys(env) = %v, want [safe risky]", got)
	}
	if got := Names(SelectingLabelKeys(all, []string{"nobody-uses-this"})); len(got) != 0 {
		t.Errorf("SelectingLabelKeys on an unused key = %v, want empty", got)
	}
}

func TestNewDynamicReader_NilClientIsNil(t *testing.T) {
	if r := NewDynamicReader(nil, "argocd"); r != nil {
		t.Error("NewDynamicReader(nil) must return nil so callers can nil-check once")
	}
}
