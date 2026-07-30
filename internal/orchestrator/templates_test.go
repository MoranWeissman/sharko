package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBuildV4SeedFiles_EnginePinName_MatchesConstant is the v4 Wave 1 Story
// 4.2 successor to the old V124-14 / BUG-031 template-drift guard. There is
// no longer a separate template file for the engine pin's metadata.name to
// drift from — BuildV4SeedFiles generates it directly — but the same class
// of bug (init.go polls one app name, the seed writes another) is still
// possible if a future edit hardcodes a literal instead of
// BootstrapRootAppName. This test renders the real seed and asserts the
// `kind: Application` document's metadata.name equals BootstrapRootAppName,
// the same name WaitForSync polls in ArgoCD.
func TestBuildV4SeedFiles_EnginePinName_MatchesConstant(t *testing.T) {
	files := BuildV4SeedFiles(
		GitOpsConfig{BaseBranch: "main", RepoURL: "https://github.com/example/addons"},
		RepoPathsConfig{HostClusterName: "hub"},
	)

	raw, ok := files[BootstrapRootAppPath]
	if !ok {
		t.Fatalf("BuildV4SeedFiles emitted no file at BootstrapRootAppPath = %q", BootstrapRootAppPath)
	}

	type appMetaDoc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}

	var parsed appMetaDoc
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parsing engine pin YAML: %v", err)
	}
	if parsed.Kind != "Application" {
		t.Fatalf("engine pin kind = %q, want %q", parsed.Kind, "Application")
	}
	if parsed.Metadata.Name != BootstrapRootAppName {
		t.Errorf(
			"engine pin metadata.name = %q, want %q (BootstrapRootAppName).\n"+
				"This is the v4 successor to the V124-14 / BUG-031 drift guard: WaitForSync\n"+
				"polls ArgoCD for BootstrapRootAppName, so if this ever hardcodes a literal\n"+
				"instead of the constant, first-run init silently fails step 4.",
			parsed.Metadata.Name, BootstrapRootAppName,
		)
	}
}

// TestCollectBootstrapFiles_RootAppPath_MatchesConstant is the v4 successor
// to the V124-20 / BUG-045 drift guard. It runs CollectBootstrapFiles and
// asserts the returned commit-path map contains exactly one entry at
// BootstrapRootAppPath whose content is the ArgoCD engine-pin Application
// (kind: Application + the canonical BootstrapRootAppName) — the same path
// the API layer's pollPRMerge / isPRMerged / already-init checks
// (internal/api/init.go, init_status.go) probe on the base branch.
func TestCollectBootstrapFiles_RootAppPath_MatchesConstant(t *testing.T) {
	orch := New(
		nil, // gitMu — CollectBootstrapFiles does not lock
		nil, // credProvider — unused
		nil, // argocd — unused
		nil, // git — unused
		GitOpsConfig{BaseBranch: "main", RepoURL: "https://github.com/example/addons"},
		RepoPathsConfig{},
		nil, // templateFS — v4 bootstrap no longer reads from it
	)

	files, err := orch.CollectBootstrapFiles(context.Background())
	if err != nil {
		t.Fatalf("CollectBootstrapFiles: %v", err)
	}

	content, ok := files[BootstrapRootAppPath]
	if !ok {
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		t.Fatalf(
			"CollectBootstrapFiles emitted no file at BootstrapRootAppPath = %q.\n"+
				"Got commit paths: %v", BootstrapRootAppPath, paths,
		)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "kind: Application") {
		t.Errorf("file at BootstrapRootAppPath = %q does not contain `kind: Application`.\nContent head: %.200s",
			BootstrapRootAppPath, contentStr)
	}
	if !strings.Contains(contentStr, "name: "+BootstrapRootAppName) {
		t.Errorf("file at BootstrapRootAppPath = %q does not contain `name: %s`.\nContent head: %.200s",
			BootstrapRootAppPath, BootstrapRootAppName, contentStr)
	}
}

// TestCollectBootstrapFiles_ExactlyTheSeed pins Story 4.2's headline
// acceptance criterion: the bootstrap PR contains EXACTLY the empty data
// folders, the engine pin, and the README — nothing else. No AppProject
// file, no Chart.yaml, no addons-catalog.yaml seed, no per-addon values
// stubs (the whole templates/bootstrap/ tree the v3 path used to walk).
func TestCollectBootstrapFiles_ExactlyTheSeed(t *testing.T) {
	orch := New(nil, nil, nil, nil,
		GitOpsConfig{BaseBranch: "main", RepoURL: "https://github.com/example/addons"},
		RepoPathsConfig{HostClusterName: "hub"},
		nil,
	)

	files, err := orch.CollectBootstrapFiles(context.Background())
	if err != nil {
		t.Fatalf("CollectBootstrapFiles: %v", err)
	}

	want := map[string]bool{
		"clusters/.gitkeep":        true,
		"fleet/.gitkeep":           true,
		"values/global/.gitkeep":   true,
		"values/clusters/.gitkeep": true,
		"catalog/.gitkeep":         true,
		"engine/application.yaml":  true,
		"README.md":                true,
	}

	if len(files) != len(want) {
		got := make([]string, 0, len(files))
		for p := range files {
			got = append(got, p)
		}
		t.Fatalf("CollectBootstrapFiles returned %d files, want exactly %d.\nGot: %v", len(files), len(want), got)
	}
	for p := range files {
		if !want[p] {
			t.Errorf("unexpected file in bootstrap seed: %q — the seed must contain ONLY empty data folders, the engine pin, and the README (design doc §1)", p)
		}
	}
	for p := range want {
		if _, ok := files[p]; !ok {
			t.Errorf("missing expected seed file: %q", p)
		}
	}

	// The five .gitkeep placeholders must be empty — a git limitation
	// placeholder, never Sharko data (design doc §1).
	for p, content := range files {
		if strings.HasSuffix(p, ".gitkeep") && len(content) != 0 {
			t.Errorf("%q should be empty, got %d bytes", p, len(content))
		}
	}
}

// TestBuildEnginePin_NoUnresolvedPlaceholders guards against the v3 failure
// mode where a raw SHARKO_* token or unresolved {{ .Values... }} expression
// leaked into a committed file because a substitution pass was skipped.
// BuildV4SeedFiles resolves every value directly (no placeholder-token
// pass at all), so this should never happen — but if it ever does, the
// engine pin is meaningless when the API layer re-reads it post-merge to
// bootstrap ArgoCD.
func TestBuildEnginePin_NoUnresolvedPlaceholders(t *testing.T) {
	content := buildEnginePin(
		GitOpsConfig{RepoURL: "https://github.com/example/addons", BaseBranch: "main"},
		RepoPathsConfig{HostClusterName: "hub"},
	)
	for _, token := range []string{"SHARKO_", "{{ .Values", "{{.Values"} {
		if bytes.Contains(content, []byte(token)) {
			t.Errorf("engine pin content contains an unresolved placeholder token %q:\n%s", token, content)
		}
	}
}
