// v1.20 — recent-PRs filter tests.
//
// These tests pin the fix for the "Recent changes panel stays empty" bug. The
// original implementation called ListPullRequests(ctx, "merged"), but neither
// the GitHub provider nor the Azure DevOps provider accepts "merged" as a
// state — merged PRs live under state="closed" with Status="merged". The
// handler now asks for "closed" and filters by Status=="merged", and the
// filter itself is covered here with concrete Sharko-authored titles /
// branches.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

func TestRecentPRs_EmptyState_NoMatchingPRs(t *testing.T) {
	recentPRsStore.reset()
	srv := newTestServer()
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{
		prs: []gitprovider.PullRequest{
			// Merged but on an unrelated file — should be filtered out by
			// the addon/keyword check.
			{
				ID:           10,
				Title:        "sharko: register cluster prod-eu",
				SourceBranch: "sharko/register-cluster-prod-eu-abc123",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/10",
				Author:       "moran",
				ClosedAt:     "2026-04-10T10:00:00Z",
			},
			// Still open — must not surface on recent-changes (which is
			// "recently merged").
			{
				ID:           11,
				Title:        "sharko: update global values for cert-manager",
				SourceBranch: "sharko/update-global-values-for-cert-manager-def456",
				Status:       "open",
				URL:          "https://github.com/acme/infra/pull/11",
				Author:       "moran",
			},
		},
	})

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/addons/cert-manager/values/recent-prs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d. body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries    []map[string]interface{} `json:"entries"`
		ValuesFile string                   `json:"values_file"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected zero entries (no matching merged PRs), got %d: %+v", len(resp.Entries), resp.Entries)
	}
	if resp.ValuesFile == "" {
		t.Error("expected values_file in response even when entries are empty")
	}
}

func TestRecentPRs_GlobalValues_MatchesSharkoPR(t *testing.T) {
	recentPRsStore.reset()
	srv := newTestServer()
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{
		prs: []gitprovider.PullRequest{
			// Exact Sharko-generated PR for a global values edit — this is
			// the one pr:25 looked like in the maintainer's audit log.
			{
				ID:           25,
				Title:        "sharko: update global values for cert-manager",
				SourceBranch: "sharko/update-global-values-for-cert-manager-ab12cd34",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/25",
				Author:       "moran",
				ClosedAt:     "2026-04-16T14:23:00Z",
			},
			// Pull-upstream PR for the same addon — also matches (has
			// "upstream" + addon name).
			{
				ID:           22,
				Title:        "sharko: pull upstream defaults for cert-manager",
				SourceBranch: "sharko/pull-upstream-defaults-for-cert-manager-99ffaa11",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/22",
				Author:       "moran",
				ClosedAt:     "2026-04-15T10:00:00Z",
			},
			// Different addon — must not leak into cert-manager results.
			{
				ID:           23,
				Title:        "sharko: update global values for external-dns",
				SourceBranch: "sharko/update-global-values-for-external-dns-77ee22bb",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/23",
				Author:       "moran",
				ClosedAt:     "2026-04-15T12:00:00Z",
			},
		},
	})

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/addons/cert-manager/values/recent-prs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d. body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []struct {
			PRID  int    `json:"pr_id"`
			Title string `json:"title"`
		} `json:"entries"`
		ValuesFile string `json:"values_file"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 matching PRs (pr:25 + pr:22), got %d: %+v", len(resp.Entries), resp.Entries)
	}

	seen := map[int]bool{}
	for _, e := range resp.Entries {
		seen[e.PRID] = true
	}
	if !seen[25] {
		t.Error("expected pr:25 (update global values for cert-manager) in results")
	}
	if !seen[22] {
		t.Error("expected pr:22 (pull upstream defaults for cert-manager) in results")
	}
	if seen[23] {
		t.Error("pr:23 (external-dns) must not leak into cert-manager results")
	}
	if want := "configuration/addons-global-values/cert-manager.yaml"; resp.ValuesFile != want {
		t.Errorf("values_file = %q, want %q", resp.ValuesFile, want)
	}
}

func TestRecentPRs_ClusterOverrides_RequiresClusterInTitleOrBranch(t *testing.T) {
	recentPRsStore.reset()
	srv := newTestServer()
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{
		prs: []gitprovider.PullRequest{
			// Cluster-scoped PR — title + branch both carry the cluster
			// name, so the per-cluster endpoint surfaces it.
			{
				ID:           31,
				Title:        "sharko: update cert-manager overrides on cluster dev-eu",
				SourceBranch: "sharko/update-cert-manager-overrides-on-cluster-dev-eu-aa00bb11",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/31",
				Author:       "moran",
				ClosedAt:     "2026-04-17T09:00:00Z",
			},
			// Global-values PR for the same addon — must NOT surface in
			// the cluster endpoint because the cluster name isn't in the
			// title or branch.
			{
				ID:           25,
				Title:        "sharko: update global values for cert-manager",
				SourceBranch: "sharko/update-global-values-for-cert-manager-ab12cd34",
				Status:       "merged",
				URL:          "https://github.com/acme/infra/pull/25",
				Author:       "moran",
				ClosedAt:     "2026-04-16T14:23:00Z",
			},
		},
	})

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/dev-eu/addons/cert-manager/values/recent-prs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d. body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []struct {
			PRID int `json:"pr_id"`
		} `json:"entries"`
		ValuesFile string `json:"values_file"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("expected exactly 1 cluster-scoped PR (pr:31), got %d: %+v", len(resp.Entries), resp.Entries)
	}
	if resp.Entries[0].PRID != 31 {
		t.Errorf("expected pr:31, got pr:%d", resp.Entries[0].PRID)
	}
	if want := "configuration/addons-clusters-values/dev-eu.yaml"; resp.ValuesFile != want {
		t.Errorf("values_file = %q, want %q", resp.ValuesFile, want)
	}
}

// Unit-level regression on the matcher alone — complements the handler tests.
func TestMatchesValuesPR(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		branch  string
		addon   string
		cluster string
		want    bool
	}{
		{
			name:   "global values edit for addon matches",
			title:  "sharko: update global values for cert-manager",
			branch: "sharko/update-global-values-for-cert-manager-abc123",
			addon:  "cert-manager",
			want:   true,
		},
		{
			name:   "pull upstream for addon matches",
			title:  "sharko: pull upstream defaults for cert-manager",
			branch: "sharko/pull-upstream-defaults-for-cert-manager-def456",
			addon:  "cert-manager",
			want:   true,
		},
		{
			name:   "different addon does not match",
			title:  "sharko: update global values for external-dns",
			branch: "sharko/update-global-values-for-external-dns-xyz789",
			addon:  "cert-manager",
			want:   false,
		},
		{
			name:    "cluster-scoped PR matches when cluster name present",
			title:   "sharko: update cert-manager overrides on cluster dev-eu",
			branch:  "sharko/update-cert-manager-overrides-on-cluster-dev-eu-aa00bb11",
			addon:   "cert-manager",
			cluster: "dev-eu",
			want:    true,
		},
		{
			name:    "global PR rejected when cluster filter is set",
			title:   "sharko: update global values for cert-manager",
			branch:  "sharko/update-global-values-for-cert-manager-abc123",
			addon:   "cert-manager",
			cluster: "dev-eu",
			want:    false,
		},
		{
			name:   "unrelated cluster-registration PR does not match addon",
			title:  "sharko: register cluster prod-eu",
			branch: "sharko/register-cluster-prod-eu-123abc",
			addon:  "cert-manager",
			want:   false,
		},
		{
			name:   "case-insensitive addon match",
			title:  "sharko: update global values for Cert-Manager",
			branch: "sharko/update-global-values-for-cert-manager-abc",
			addon:  "cert-manager",
			want:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesValuesPR(tc.title, tc.branch, tc.addon, tc.cluster)
			if got != tc.want {
				t.Errorf("matchesValuesPR(%q, %q, %q, %q) = %v, want %v",
					tc.title, tc.branch, tc.addon, tc.cluster, got, tc.want)
			}
		})
	}
}
