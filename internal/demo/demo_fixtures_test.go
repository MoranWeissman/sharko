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
// an open PR matching a pending cluster registration (Story LW-F gap #2).
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
			// Sanity: PR 43 is the third seeded PR
			if pr.ID != 43 {
				t.Errorf("dr-eu pending registration PR ID = %d, want 43", pr.ID)
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
