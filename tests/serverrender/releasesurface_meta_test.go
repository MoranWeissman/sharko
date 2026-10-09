package serverrender

// releasesurface_meta_test.go — guard on the published GitHub release title
// and body warning, so the download page says it is a technical preview.
//
// Story marker: RELEASESURFACE-META-PLAN-2026-08-27
//
// # Why this guard exists
//
// Pushing a v4.0.0 tag automatically publishes a GitHub release if CI passes.
// Before this guard, GoReleaser would title that page simply "v4.0.0" with no
// indication of technical-preview status. Operators reading the download page
// need to know the release state without having to read into the body or hunt
// for external documentation.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPublishedReleaseTitleCarriesTechnicalPreviewLabel asserts that the
// GoReleaser configuration produces a GitHub release title that explicitly
// names the technical-preview status.
func TestPublishedReleaseTitleCarriesTechnicalPreviewLabel(t *testing.T) {
	cfg := loadGoReleaserConfig(t)

	release, ok := cfg["release"].(map[string]any)
	if !ok {
		t.Fatal("the release block is absent or not a map, so the title cannot be checked")
	}

	nameTemplate, ok := release["name_template"].(string)
	if !ok {
		t.Fatal("name_template is absent or not a string, so the published release title is unconfigured")
	}

	// The exact template string that must be present.
	want := "{{ .Tag }} — technical preview"
	if nameTemplate != want {
		t.Errorf("the published release title template is %q, but must be %q so the download page "+
			"says it is a technical preview", nameTemplate, want)
	}
}

// TestPublishedReleaseBodyCarriesTheDurableWarning asserts that the release
// body opens with the four-sentence warning that stays truthful before and
// after the tag is pushed.
func TestPublishedReleaseBodyCarriesTheDurableWarning(t *testing.T) {
	cfg := loadGoReleaserConfig(t)

	release, ok := cfg["release"].(map[string]any)
	if !ok {
		t.Fatal("the release block is absent or not a map, so the body warning cannot be checked")
	}

	header, ok := release["header"].(string)
	if !ok {
		t.Fatal("header is absent or not a string, so the release body is unconfigured")
	}

	// The four sentences that must appear, matched after whitespace and
	// backtick normalization so formatting cannot defeat the guard.
	warningSentences := []string{
		"Sharko {{ .Tag }} is a technical preview.",
		"Do not use Sharko in production.",
		"Install only published v4.0.1-or-later artifacts.",
		"v3.0.0 and earlier remain retired and unsupported.",
	}

	normalized := normalizeForMatch(header)
	for _, sentence := range warningSentences {
		normalizedSentence := normalizeForMatch(sentence)
		if !strings.Contains(normalized, normalizedSentence) {
			t.Errorf("the release body header does not contain the required sentence %q\n  normalized header: %s",
				sentence, normalized)
		}
	}
}

// TestPrereleaseDetectionRemainsAutomatic asserts that the prerelease field is
// still set to "auto", so GoReleaser can detect pre-release tags without manual
// workflow changes.
func TestPrereleaseDetectionRemainsAutomatic(t *testing.T) {
	cfg := loadGoReleaserConfig(t)

	release, ok := cfg["release"].(map[string]any)
	if !ok {
		t.Fatal("the release block is absent or not a map, so prerelease cannot be checked")
	}

	prereleaseRaw := release["prerelease"]
	if prereleaseRaw == nil {
		t.Fatal("prerelease is absent")
	}

	// The YAML parser may give us a string or a boolean depending on the value.
	// We want exactly the string "auto".
	switch v := prereleaseRaw.(type) {
	case string:
		if v != "auto" {
			t.Errorf("prerelease is %q, but must be \"auto\" for semver pre-release detection", v)
		}
	case bool:
		t.Errorf("prerelease is a boolean (%v), but must be the string \"auto\" for semver pre-release detection", v)
	default:
		t.Errorf("prerelease has unexpected type %T with value %v, but must be the string \"auto\"", v, v)
	}
}

// TestArgocdTestedRangeLineAppearsAfterTheWarning asserts that the
// CI-verified ArgoCD compatibility line is still present and appears after the
// warning, not before it.
func TestArgocdTestedRangeLineAppearsAfterTheWarning(t *testing.T) {
	cfg := loadGoReleaserConfig(t)

	release, ok := cfg["release"].(map[string]any)
	if !ok {
		t.Fatal("the release block is absent or not a map")
	}

	header, ok := release["header"].(string)
	if !ok {
		t.Fatal("header is absent or not a string")
	}

	// The ArgoCD line is the envOrDefault call. It must still be present.
	argocdLine := `{{ envOrDefault "SHARKO_TESTED_ARGOCD_LINE" "" }}`
	if !strings.Contains(header, argocdLine) {
		t.Fatal("the header no longer contains the ArgoCD tested-range line, so the release body " +
			"will not carry the CI-verified compatibility information")
	}

	// The warning must appear before the ArgoCD line. Find the position of the
	// first warning sentence and the ArgoCD line.
	warningPos := strings.Index(header, "Sharko {{ .Tag }}")
	argocdPos := strings.Index(header, argocdLine)

	if warningPos == -1 {
		t.Fatal("the warning does not appear in the header at all")
	}
	if argocdPos == -1 {
		t.Fatal("the ArgoCD line does not appear in the header at all")
	}
	if argocdPos < warningPos {
		t.Error("the ArgoCD tested-range line appears BEFORE the warning, but it must appear AFTER")
	}
}

// loadGoReleaserConfig reads and parses .goreleaser.yaml from the repository
// root. Fails the test if the file cannot be read or parsed.
func loadGoReleaserConfig(t *testing.T) map[string]any {
	t.Helper()

	root := repoRoot(t)
	path := filepath.Join(root, ".goreleaser.yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read .goreleaser.yaml: %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("cannot parse .goreleaser.yaml: %v", err)
	}

	if len(cfg) == 0 {
		t.Fatal(".goreleaser.yaml parsed as empty, so nothing below is checking anything")
	}

	return cfg
}

// normalizeForMatch collapses runs of whitespace to single spaces and strips
// backticks, so block-scalar wrapping or backtick formatting in the YAML
// cannot defeat the sentence matching.
func normalizeForMatch(s string) string {
	// Strip backticks first.
	s = strings.ReplaceAll(s, "`", "")

	// Collapse whitespace runs (spaces, tabs, newlines) to single spaces.
	ws := regexp.MustCompile(`\s+`)
	s = ws.ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}
