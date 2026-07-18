package demo

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestBootstrapRootAppSeeded verifies the mock ArgoCD app list includes
// the bootstrap root app so ProbeBootstrapApp returns healthy (Story LW-F gap #1).
func TestBootstrapRootAppSeeded(t *testing.T) {
	srv, err := NewMockArgocdServer()
	if err != nil {
		t.Fatalf("starting mock argocd: %v", err)
	}
	defer srv.Close()

	srv.mu.RLock()
	apps := srv.apps
	srv.mu.RUnlock()

	var found *mockApp
	for i := range apps {
		if apps[i].Metadata.Name == orchestrator.BootstrapRootAppName {
			found = &apps[i]
			break
		}
	}

	if found == nil {
		t.Fatalf("bootstrap root app %q not found in mock apps (count=%d)",
			orchestrator.BootstrapRootAppName, len(apps))
	}

	// Assert Synced + Healthy so ProbeBootstrapApp would return healthy
	if found.Status.Sync.Status != "Synced" {
		t.Errorf("bootstrap app sync status = %q, want Synced", found.Status.Sync.Status)
	}
	if found.Status.Health.Status != "Healthy" {
		t.Errorf("bootstrap app health status = %q, want Healthy", found.Status.Health.Status)
	}

	// Sanity: the app should be on the in-cluster destination
	if found.Spec.Destination.Server != "https://kubernetes.default.svc" {
		t.Errorf("bootstrap app destination server = %q, want in-cluster",
			found.Spec.Destination.Server)
	}
}

// TestPendingRegistrationPRSeeded verifies the mock Git provider includes
// an open PR matching a pending cluster registration (Story LW-F gap #2, updated LW-18).
func TestPendingRegistrationPRSeeded(t *testing.T) {
	p := NewMockGitProvider()

	prs, err := p.ListPullRequests(context.Background(), "open")
	if err != nil {
		t.Fatalf("listing open PRs: %v", err)
	}

	// The dr-eu cluster from demoUnregisteredClusters should have a matching PR
	var foundDrEu bool
	for _, pr := range prs {
		// Title must follow the pattern "sharko: register cluster dr-eu"
		if pr.Title == "sharko: register cluster dr-eu" && pr.Status == "open" {
			foundDrEu = true
			// Sanity: PR 42 is the second seeded PR (LW-18: removed perf-asia phantom PR)
			if pr.ID != 42 {
				t.Errorf("dr-eu pending registration PR ID = %d, want 42", pr.ID)
			}
			break
		}
	}

	if !foundDrEu {
		var titles []string
		for _, pr := range prs {
			titles = append(titles, pr.Title)
		}
		t.Fatalf("pending registration PR for dr-eu not found; open PR titles: %v", titles)
	}
}

// TestPendingRegistrationMatchesUnregisteredCluster verifies the seeded pending
// registration PR corresponds to a cluster in demoUnregisteredClusters.
func TestPendingRegistrationMatchesUnregisteredCluster(t *testing.T) {
	// Find dr-eu in demoUnregisteredClusters
	var foundCluster bool
	for _, c := range demoUnregisteredClusters {
		if c.Name == "dr-eu" {
			foundCluster = true
			break
		}
	}

	if !foundCluster {
		t.Fatal("dr-eu not found in demoUnregisteredClusters; the seeded PR is orphaned")
	}

	// The PR should be seeded (tested above); this confirms the mapping
	p := NewMockGitProvider()
	prs, _ := p.ListPullRequests(context.Background(), "open")

	var foundPR bool
	for _, pr := range prs {
		if pr.Title == "sharko: register cluster dr-eu" {
			foundPR = true
			break
		}
	}

	if !foundPR {
		t.Fatal("pending PR for dr-eu not seeded even though dr-eu is in demoUnregisteredClusters")
	}
}

// TestPerfAsiaNotPending verifies perf-asia does NOT have a pending registration PR
// (LW-18 Part 1: perf-asia is the disconnected in-git cluster, not a pending registration).
func TestPerfAsiaNotPending(t *testing.T) {
	p := NewMockGitProvider()

	prs, err := p.ListPullRequests(context.Background(), "open")
	if err != nil {
		t.Fatalf("listing open PRs: %v", err)
	}

	for _, pr := range prs {
		if pr.Title == "sharko: register cluster perf-asia" {
			t.Fatalf("found phantom perf-asia pending PR (ID=%d); should not exist per LW-18", pr.ID)
		}
	}
}

// TestExactlyOnePendingRegistration verifies the demo seeds exactly 1 pending registration
// (dr-eu only; sandbox-us removed, perf-asia is in-git not pending).
func TestExactlyOnePendingRegistration(t *testing.T) {
	p := NewMockGitProvider()

	prs, err := p.ListPullRequests(context.Background(), "open")
	if err != nil {
		t.Fatalf("listing open PRs: %v", err)
	}

	var pendingRegistrations []string
	for _, pr := range prs {
		if pr.Title == "sharko: register cluster dr-eu" {
			pendingRegistrations = append(pendingRegistrations, "dr-eu")
		} else if pr.Title == "sharko: register cluster perf-asia" {
			pendingRegistrations = append(pendingRegistrations, "perf-asia")
		} else if pr.Title == "sharko: register cluster sandbox-us" {
			pendingRegistrations = append(pendingRegistrations, "sandbox-us")
		}
	}

	if len(pendingRegistrations) != 1 {
		t.Fatalf("expected exactly 1 pending registration (dr-eu), got %d: %v",
			len(pendingRegistrations), pendingRegistrations)
	}

	if pendingRegistrations[0] != "dr-eu" {
		t.Fatalf("expected pending registration for dr-eu, got %s", pendingRegistrations[0])
	}
}

// TestPerfAsiaInGit verifies perf-asia is seeded as an in-git (managed) cluster
// with Failed connection status.
func TestPerfAsiaInGit(t *testing.T) {
	var found *Cluster
	for i := range demoClusters {
		if demoClusters[i].Name == "perf-asia" {
			found = &demoClusters[i]
			break
		}
	}

	if found == nil {
		t.Fatal("perf-asia not found in demoClusters (should be the disconnected in-git cluster)")
	}

	if found.ConnStatus != "Failed" {
		t.Errorf("perf-asia ConnStatus = %q, want Failed (disconnected cluster)", found.ConnStatus)
	}
}

// TestMockAppNamesMatchServiceMatcher verifies the mock ArgoCD app names follow
// the {addon}-{cluster} pattern that the real cluster service expects (LW-19).
// Before this fix, the mock used the REVERSED {cluster}-{addon} order, causing
// every addon on the cluster-detail page to show "Missing from ArgoCD".
func TestMockAppNamesMatchServiceMatcher(t *testing.T) {
	srv, err := NewMockArgocdServer()
	if err != nil {
		t.Fatalf("starting mock argocd: %v", err)
	}
	defer srv.Close()

	srv.mu.RLock()
	apps := srv.apps
	srv.mu.RUnlock()

	// Find a representative addon app (e.g., cert-manager on prod-eu)
	// The expected name is {addon}-{cluster}, e.g., "cert-manager-prod-eu"
	var foundCertManagerProdEu *mockApp
	for i := range apps {
		if apps[i].Metadata.Name == "cert-manager-prod-eu" {
			foundCertManagerProdEu = &apps[i]
			break
		}
	}

	if foundCertManagerProdEu == nil {
		// List all app names to help diagnose
		var names []string
		for _, app := range apps {
			names = append(names, app.Metadata.Name)
		}
		t.Fatalf("cert-manager-prod-eu not found; app names: %v\nExpected {addon}-{cluster} format, got something else", names)
	}

	// Sanity: the app should target the prod-eu cluster and the cert-manager chart
	if foundCertManagerProdEu.Spec.Source.Chart != "cert-manager" {
		t.Errorf("cert-manager-prod-eu chart = %q, want cert-manager",
			foundCertManagerProdEu.Spec.Source.Chart)
	}

	// The destination server should match prod-eu's server URL
	var prodEuServer string
	for _, c := range demoClusters {
		if c.Name == "prod-eu" {
			prodEuServer = c.Server
			break
		}
	}
	if foundCertManagerProdEu.Spec.Destination.Server != prodEuServer {
		t.Errorf("cert-manager-prod-eu destination server = %q, want %q (prod-eu)",
			foundCertManagerProdEu.Spec.Destination.Server, prodEuServer)
	}
}
