package advisories

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArtifactHubSourceRepoLookupAndFetch(t *testing.T) {
	repoSearchCalled := 0
	packageFetched := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repositories/search":
			repoSearchCalled++
			json.NewEncoder(w).Encode(ahRepoSearchResult{
				Repositories: []ahRepository{{Name: "my-repo"}},
			})

		case r.URL.Path == "/api/v1/packages/helm/my-repo/nginx":
			packageFetched++
			json.NewEncoder(w).Encode(ahPackageSummary{
				Name: "nginx",
				AvailableVersions: []ahVersionEntry{
					{Version: "1.2.3", ContainsSecurityUpdates: true},
					{Version: "1.2.2", ContainsSecurityUpdates: false},
					{Version: "1.2.1-beta", Prerelease: true, ContainsSecurityUpdates: false},
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Patch ArtifactHub base URL to point at test server.
	origBase := artifactHubBaseURL
	setArtifactHubBaseURL(srv.URL + "/api/v1")
	defer setArtifactHubBaseURL(origBase)

	src := newArtifactHubSource(&http.Client{Timeout: 2 * time.Second})
	ctx := context.Background()

	advisories, err := src.Get(ctx, "https://charts.example.com", "nginx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repoSearchCalled != 1 {
		t.Errorf("expected 1 repo search call, got %d", repoSearchCalled)
	}
	if packageFetched != 1 {
		t.Errorf("expected 1 package fetch, got %d", packageFetched)
	}
	// Prerelease should be excluded
	if len(advisories) != 2 {
		t.Fatalf("expected 2 advisories (prerelease excluded), got %d", len(advisories))
	}
	if !advisories[0].ContainsSecurityFix {
		t.Errorf("expected first advisory to have ContainsSecurityFix=true")
	}
	if advisories[1].ContainsSecurityFix {
		t.Errorf("expected second advisory to have ContainsSecurityFix=false")
	}
}

func TestArtifactHubSourceRepoNameCached(t *testing.T) {
	repoSearchCalled := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repositories/search":
			repoSearchCalled++
			json.NewEncoder(w).Encode(ahRepoSearchResult{
				Repositories: []ahRepository{{Name: "cached-repo"}},
			})
		case r.URL.Path == "/api/v1/packages/helm/cached-repo/chart":
			json.NewEncoder(w).Encode(ahPackageSummary{Name: "chart"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origBase := artifactHubBaseURL
	setArtifactHubBaseURL(srv.URL + "/api/v1")
	defer setArtifactHubBaseURL(origBase)

	src := newArtifactHubSource(&http.Client{Timeout: 2 * time.Second})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		src.Get(ctx, "https://charts.example.com", "chart") //nolint
	}
	if repoSearchCalled != 1 {
		t.Errorf("expected repo search to be called once (cached), got %d", repoSearchCalled)
	}
}

func TestArtifactHubSourceRepoNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repositories/search" {
			json.NewEncoder(w).Encode(ahRepoSearchResult{Repositories: nil})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	origBase := artifactHubBaseURL
	setArtifactHubBaseURL(srv.URL + "/api/v1")
	defer setArtifactHubBaseURL(origBase)

	src := newArtifactHubSource(&http.Client{Timeout: 2 * time.Second})
	_, err := src.Get(context.Background(), "https://unknown.example.com", "chart")
	if err == nil {
		t.Error("expected error when repo not found on ArtifactHub")
	}
}

func TestArtifactHubSourceServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	origBase := artifactHubBaseURL
	setArtifactHubBaseURL(srv.URL + "/api/v1")
	defer setArtifactHubBaseURL(origBase)

	src := newArtifactHubSource(&http.Client{Timeout: 2 * time.Second})
	_, err := src.Get(context.Background(), "https://charts.example.com", "chart")
	if err == nil {
		t.Error("expected error on server error response")
	}
}

func TestReleaseNotesSourceKeywordDetection(t *testing.T) {
	indexYAML := `
apiVersion: v1
entries:
  nginx:
  - version: "1.2.3"
    annotations:
      artifacthub.io/changes: "Fix CVE-2024-1234 affecting ingress handler"
  - version: "1.2.2"
    annotations:
      artifacthub.io/changes: "Add new dashboard; breaking change in API"
  - version: "1.2.1"
    annotations:
      artifacthub.io/changes: "Minor performance improvements"
  - version: "1.2.0"
`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		w.Write([]byte(indexYAML))
	}))
	defer srv.Close()

	src := newReleaseNotesSource(&http.Client{Timeout: 2 * time.Second})
	advs, err := src.Get(context.Background(), srv.URL, "nginx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(advs) != 4 {
		t.Fatalf("expected 4 advisories, got %d", len(advs))
	}

	// Find by version
	byVer := make(map[string]Advisory)
	for _, a := range advs {
		byVer[a.Version] = a
	}

	if !byVer["1.2.3"].ContainsSecurityFix {
		t.Error("expected 1.2.3 to have ContainsSecurityFix=true (CVE keyword)")
	}
	if !byVer["1.2.2"].ContainsBreaking {
		t.Error("expected 1.2.2 to have ContainsBreaking=true (breaking keyword)")
	}
	if byVer["1.2.1"].ContainsSecurityFix || byVer["1.2.1"].ContainsBreaking {
		t.Error("expected 1.2.1 to have no flags (clean release notes)")
	}
	if byVer["1.2.0"].ContainsSecurityFix || byVer["1.2.0"].ContainsBreaking {
		t.Error("expected 1.2.0 to have no flags (no annotations)")
	}
}

func TestReleaseNotesSourceChartNotInIndex(t *testing.T) {
	indexYAML := `
apiVersion: v1
entries:
  other-chart:
  - version: "1.0.0"
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(indexYAML))
	}))
	defer srv.Close()

	src := newReleaseNotesSource(&http.Client{Timeout: 2 * time.Second})
	advs, err := src.Get(context.Background(), srv.URL, "missing-chart")
	if err != nil {
		t.Fatalf("unexpected error for missing chart: %v", err)
	}
	if len(advs) != 0 {
		t.Errorf("expected empty advisories for missing chart, got %d", len(advs))
	}
}
