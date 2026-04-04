package demo

import (
	"context"
	"fmt"
	"sync"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// MockGitProvider implements gitprovider.GitProvider entirely in memory.
// All write operations are accepted and stored in-process — no real Git calls made.
type MockGitProvider struct {
	mu      sync.RWMutex
	files   map[string][]byte    // path → content
	branches map[string]bool     // branch name → exists
	prs     []gitprovider.PullRequest
	nextPRID int
}

// NewMockGitProvider creates a new in-memory git provider with pre-seeded content.
func NewMockGitProvider() *MockGitProvider {
	p := &MockGitProvider{
		files:    make(map[string][]byte),
		branches: map[string]bool{"main": true},
		nextPRID: 43, // start after the 2 pre-seeded PRs
	}
	p.seedFiles()
	p.seedPRs()
	return p
}

func (p *MockGitProvider) seedFiles() {
	// cluster-addons.yaml — lists all clusters with addon enable labels
	p.files["configuration/cluster-addons.yaml"] = []byte(clusterAddonsYAML)

	// addons-catalog.yaml — the addon catalog (applicationsets format)
	p.files["configuration/addons-catalog.yaml"] = []byte(addonsCatalogYAML)

	// Global values stubs
	p.files["configuration/addons-global-values/cert-manager.yaml"] = []byte(`replicaCount: 1
resources:
  requests:
    cpu: 100m
    memory: 128Mi
`)
	p.files["configuration/addons-global-values/metrics-server.yaml"] = []byte(`replicaCount: 1
args:
  - --kubelet-insecure-tls
`)
	p.files["configuration/addons-global-values/kube-prometheus-stack.yaml"] = []byte(`grafana:
  enabled: true
alertmanager:
  enabled: true
`)
	p.files["configuration/addons-global-values/datadog.yaml"] = []byte(`datadog:
  clusterName: "demo"
  collectEvents: true
`)

	// Bootstrap root-app (marks repo as initialised)
	p.files["bootstrap/root-app.yaml"] = []byte(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-root
  namespace: argocd
spec:
  project: sharko
  source:
    repoURL: https://github.com/demo/sharko-addons
    targetRevision: HEAD
    path: bootstrap
`)

	// Per-cluster values
	p.files["configuration/addons-clusters-values/prod-eu/cert-manager.yaml"] = []byte(`global:
  leaderElection:
    namespace: cert-manager
`)
	p.files["configuration/addons-clusters-values/staging-eu/cert-manager.yaml"] = []byte(`global:
  leaderElection:
    namespace: cert-manager
`)
}

func (p *MockGitProvider) seedPRs() {
	p.prs = []gitprovider.PullRequest{
		{
			ID:           41,
			Title:        "sharko: upgrade cert-manager 1.13.6 → 1.14.4 on staging-eu",
			Description:  "Automated upgrade by Sharko",
			Author:       "sharko-bot",
			Status:       "open",
			SourceBranch: "sharko/upgrade-cert-manager-staging-eu",
			TargetBranch: "main",
			URL:          "https://github.com/demo/sharko-addons/pull/41",
			CreatedAt:    "2025-01-18T09:00:00Z",
			UpdatedAt:    "2025-01-18T09:00:00Z",
		},
		{
			ID:           42,
			Title:        "sharko: register cluster perf-asia",
			Description:  "Automated cluster registration by Sharko",
			Author:       "sharko-bot",
			Status:       "open",
			SourceBranch: "sharko/register-perf-asia",
			TargetBranch: "main",
			URL:          "https://github.com/demo/sharko-addons/pull/42",
			CreatedAt:    "2025-01-19T14:22:00Z",
			UpdatedAt:    "2025-01-19T14:22:00Z",
		},
	}
}

// GetFileContent returns the content of a file at the given path and ref.
func (p *MockGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if content, ok := p.files[path]; ok {
		return content, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

// ListDirectory returns the names of items under a directory path.
func (p *MockGitProvider) ListDirectory(_ context.Context, dirPath, _ string) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	prefix := dirPath
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	seen := make(map[string]bool)
	var entries []string
	for path := range p.files {
		if dirPath == "" || path == dirPath || (len(path) > len(prefix) && path[:len(prefix)] == prefix) {
			// Extract the immediate child name
			rest := path[len(prefix):]
			if idx := indexOf(rest, '/'); idx >= 0 {
				rest = rest[:idx]
			}
			if !seen[rest] {
				seen[rest] = true
				entries = append(entries, rest)
			}
		}
	}
	return entries, nil
}

// ListPullRequests returns pull requests filtered by state.
func (p *MockGitProvider) ListPullRequests(_ context.Context, state string) ([]gitprovider.PullRequest, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []gitprovider.PullRequest
	for _, pr := range p.prs {
		if state == "" || state == "all" || pr.Status == state {
			result = append(result, pr)
		}
	}
	return result, nil
}

// TestConnection always succeeds for the demo provider.
func (p *MockGitProvider) TestConnection(_ context.Context) error {
	return nil
}

// CreateBranch creates an in-memory branch.
func (p *MockGitProvider) CreateBranch(_ context.Context, branchName, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.branches[branchName] = true
	return nil
}

// CreateOrUpdateFile upserts a file in the in-memory store.
func (p *MockGitProvider) CreateOrUpdateFile(_ context.Context, path string, content []byte, branch, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[path] = content
	return nil
}

// DeleteFile removes a file from the in-memory store.
func (p *MockGitProvider) DeleteFile(_ context.Context, path, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, path)
	return nil
}

// CreatePullRequest creates a mock PR and returns it with a demo URL.
func (p *MockGitProvider) CreatePullRequest(_ context.Context, title, body, head, base string) (*gitprovider.PullRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := p.nextPRID
	p.nextPRID++

	pr := gitprovider.PullRequest{
		ID:           id,
		Title:        title,
		Description:  body,
		Author:       "sharko-bot",
		Status:       "open",
		SourceBranch: head,
		TargetBranch: base,
		URL:          fmt.Sprintf("https://github.com/demo/sharko-addons/pull/%d", id),
		CreatedAt:    "2025-01-20T10:00:00Z",
		UpdatedAt:    "2025-01-20T10:00:00Z",
	}
	p.prs = append(p.prs, pr)
	return &pr, nil
}

// MergePullRequest marks a PR as merged (no-op success).
func (p *MockGitProvider) MergePullRequest(_ context.Context, prNumber int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.prs {
		if p.prs[i].ID == prNumber {
			p.prs[i].Status = "merged"
		}
	}
	return nil
}

// DeleteBranch removes a branch from the in-memory store.
func (p *MockGitProvider) DeleteBranch(_ context.Context, branchName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.branches, branchName)
	return nil
}

// indexOf returns the index of sep in s, or -1.
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}

// clusterAddonsYAML is the fake configuration/cluster-addons.yaml.
// Each cluster has addon labels: addonName: enabled|disabled.
const clusterAddonsYAML = `clusters:
  - name: prod-eu
    region: eu-west-1
    labels:
      env: production
      region: eu-west-1
      cert-manager: enabled
      metrics-server: enabled
      kube-prometheus-stack: enabled
      external-dns: enabled
      istio-base: enabled

  - name: prod-us
    region: us-east-1
    labels:
      env: production
      region: us-east-1
      cert-manager: enabled
      metrics-server: enabled
      kube-prometheus-stack: enabled
      external-dns: enabled

  - name: staging-eu
    region: eu-west-1
    labels:
      env: staging
      region: eu-west-1
      cert-manager: enabled
      cert-manager-version: "1.13.6"
      metrics-server: enabled
      metrics-server-version: "3.11.0"
      kube-prometheus-stack: enabled
      kube-prometheus-stack-version: "57.2.0"
      datadog: enabled

  - name: dev-us
    region: us-west-2
    labels:
      env: development
      region: us-west-2
      cert-manager: enabled
      cert-manager-version: "1.13.6"
      metrics-server: enabled
      vault: enabled

  - name: perf-asia
    region: ap-southeast-1
    labels:
      env: performance
      region: ap-southeast-1
      cert-manager: enabled
      cert-manager-version: "1.12.9"
      metrics-server: enabled
      metrics-server-version: "3.10.0"
      kube-prometheus-stack: enabled
      kube-prometheus-stack-version: "55.5.0"
`

// addonsCatalogYAML is the fake configuration/addons-catalog.yaml in applicationsets format.
const addonsCatalogYAML = `applicationsets:
  - appName: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: "1.14.4"
    namespace: cert-manager

  - appName: metrics-server
    chart: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server/
    version: "3.12.1"
    namespace: kube-system

  - appName: datadog
    chart: datadog
    repoURL: https://helm.datadoghq.com
    version: "3.69.0"
    namespace: datadog

  - appName: external-dns
    chart: external-dns
    repoURL: https://kubernetes-sigs.github.io/external-dns/
    version: "1.14.4"
    namespace: external-dns

  - appName: istio-base
    chart: base
    repoURL: https://istio-release.storage.googleapis.com/charts
    version: "1.21.1"
    namespace: istio-system

  - appName: kube-prometheus-stack
    chart: kube-prometheus-stack
    repoURL: https://prometheus-community.github.io/helm-charts
    version: "58.2.1"
    namespace: monitoring

  - appName: logging-operator
    chart: logging-operator
    repoURL: https://kube-logging.github.io/helm-charts
    version: "4.6.0"
    namespace: logging

  - appName: vault
    chart: vault
    repoURL: https://helm.releases.hashicorp.com
    version: "0.28.0"
    namespace: vault
`
