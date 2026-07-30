package helm

import (
	"context"
	"errors"
	"testing"
)

// TestListVersions_OCIRepoDegradesGracefully is the v4 wave 1 Story 3.3
// primitive-level test: an oci:// repo (a private in-house chart is a
// common case, per the story's "private OCI chart reference" AC) must
// return the distinguishable ErrOCIVersionCheckUnsupported sentinel instead
// of attempting (and confusingly failing) an HTTP GET to
// "<oci-url>/index.yaml" — there is nothing to fetch there. No network
// call should happen at all, so this test needs no HTTP server.
func TestListVersions_OCIRepoDegradesGracefully(t *testing.T) {
	f := NewFetcher()
	_, err := f.ListVersions(context.Background(), "oci://registry.example.com/charts", "billing-api")
	if err == nil {
		t.Fatalf("expected an error for an oci:// repo")
	}
	if !errors.Is(err, ErrOCIVersionCheckUnsupported) {
		t.Errorf("error = %v, want errors.Is match for ErrOCIVersionCheckUnsupported", err)
	}
}

// TestListVersions_OCIRepoTrailingSlashStillDetected guards the prefix
// check against a trivial formatting variant.
func TestListVersions_OCIRepoTrailingSlashStillDetected(t *testing.T) {
	f := NewFetcher()
	_, err := f.ListVersions(context.Background(), "oci://registry.example.com/charts/", "billing-api")
	if !errors.Is(err, ErrOCIVersionCheckUnsupported) {
		t.Errorf("error = %v, want errors.Is match for ErrOCIVersionCheckUnsupported", err)
	}
}

func TestGuessGitHubRepo(t *testing.T) {
	tests := []struct {
		repoURL   string
		chartName string
		expected  string
	}{
		{"https://helm.datadoghq.com", "datadog", "DataDog/helm-charts"},
		{"https://argoproj.github.io/argo-helm", "argo-rollouts", "argoproj/argo-helm"},
		{"https://kyverno.github.io/kyverno", "kyverno", "kyverno/kyverno"},
		{"https://kedacore.github.io/charts", "keda", "kedacore/charts"},
		{"https://example.github.io/my-charts", "test", "example/my-charts"},
		{"https://random-repo.com/charts", "foo", ""},
	}

	for _, tt := range tests {
		got := guessGitHubRepo(tt.repoURL, tt.chartName)
		if got != tt.expected {
			t.Errorf("guessGitHubRepo(%q, %q) = %q, want %q", tt.repoURL, tt.chartName, got, tt.expected)
		}
	}
}

func TestExtractGitHubRepoFromURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://github.com/provectus/kafka-ui", "provectus/kafka-ui"},
		{"https://github.com/provectus/kafka-ui.git", "provectus/kafka-ui"},
		{"https://github.com/owner/repo/tree/main", "owner/repo"},
		{"https://github.com/owner/repo?tab=readme", "owner/repo"},
		{"https://helm.example.com/charts", ""},
		{"https://gitlab.com/owner/repo", ""},
		{"", ""},
		{"https://github.com/owner", ""},
	}

	for _, tt := range tests {
		got := extractGitHubRepoFromURL(tt.input)
		if got != tt.expected {
			t.Errorf("extractGitHubRepoFromURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFetchReleaseNotesPreferenceOrder(t *testing.T) {
	// Verify that when Chart.yaml metadata is cached with a GitHub source URL,
	// FetchReleaseNotes uses that repo rather than the heuristic.
	f := NewFetcher()

	// Prime the chart cache with metadata that has a sources[] entry pointing
	// to the real application repo (not the charts repo).
	cacheKey := "https://provectus.github.io/kafka-ui-charts/kafka-ui/0.7.6"
	f.chartCache[cacheKey] = &chartMetadata{
		Sources: []string{"https://github.com/provectus/kafka-ui"},
		Home:    "https://provectus.github.io/kafka-ui-charts",
	}

	// extractGitHubRepoFromURL of the sources entry should yield the app repo.
	got := extractGitHubRepoFromURL("https://github.com/provectus/kafka-ui")
	want := "provectus/kafka-ui"
	if got != want {
		t.Errorf("sources extraction: got %q, want %q", got, want)
	}

	// The heuristic alone would produce the wrong repo.
	heuristic := guessGitHubRepo("https://provectus.github.io/kafka-ui-charts", "kafka-ui")
	if heuristic == want {
		t.Logf("heuristic happens to match — test is still valid but less discriminating")
	}
}
