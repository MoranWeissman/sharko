package helm

import "testing"

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
