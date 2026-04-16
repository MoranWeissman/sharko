package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/advisories"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/models"
)

// mockAdvisorySource implements advisorySource for tests.
type mockAdvisorySource struct {
	data []advisories.Advisory
	err  error
}

func (m *mockAdvisorySource) Get(_ context.Context, _, _ string) ([]advisories.Advisory, error) {
	return m.data, m.err
}

// catalogYAML builds a minimal addons-catalog.yaml with one entry.
func catalogYAML(name, chart, repoURL, version string) []byte {
	return []byte(fmt.Sprintf(`applicationsets:
  - name: %s
    chart: %s
    repoURL: %s
    version: %s
`, name, chart, repoURL, version))
}

// helmIndexYAML builds a minimal Helm repo index.yaml with the given versions.
func helmIndexYAMLFor(chart string, versions []string) string {
	entries := ""
	for _, v := range versions {
		entries += fmt.Sprintf("  - version: %q\n    urls:\n    - \"https://example.com/%s-%s.tgz\"\n", v, chart, v)
	}
	return fmt.Sprintf("apiVersion: v1\nentries:\n  %s:\n%s", chart, entries)
}

// newTestUpgradeSvc creates an UpgradeService with a real helm.Fetcher (backed by httptest) and optional advisory mock.
func newTestUpgradeSvc(advSrc advisorySource) *UpgradeService {
	return &UpgradeService{
		parser:              config.NewParser(),
		fetcher:             helm.NewFetcher(),
		advisories:          advSrc,
		managedClustersPath: "configuration/managed-clusters.yaml",
	}
}

// newHelmServer starts a test HTTP server serving a Helm repo index for the given chart versions.
func newHelmServer(t *testing.T, chart string, versions []string) *httptest.Server {
	t.Helper()
	indexContent := helmIndexYAMLFor(chart, versions)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			w.Header().Set("Content-Type", "text/yaml")
			fmt.Fprint(w, indexContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- GetRecommendations integration-style tests ---

func TestGetRecommendationsCards(t *testing.T) {
	const chart = "my-chart"
	const addon = "my-addon"

	tests := []struct {
		name            string
		currentVersion  string
		availableVers   []string
		advData         []advisories.Advisory
		wantCards       int
		wantRecommended string
		// specific assertions
		checkCards func(t *testing.T, cards []models.RecommendationCard)
	}{
		{
			name:            "patch only — no cross-major available",
			currentVersion:  "1.2.3",
			availableVers:   []string{"1.2.5", "1.2.4", "1.2.3"},
			wantCards:       1,
			wantRecommended: "1.2.5",
		},
		{
			name:            "patch + in-major",
			currentVersion:  "1.2.3",
			availableVers:   []string{"1.5.0", "1.3.0", "1.2.5", "1.2.3"},
			wantCards:       2,
			wantRecommended: "1.2.5", // no security → patch first
		},
		{
			name:            "all three cards — patch, in-major, latest cross-major",
			currentVersion:  "1.2.3",
			availableVers:   []string{"2.0.0", "1.5.0", "1.2.5", "1.2.3"},
			wantCards:       3,
			wantRecommended: "1.2.5",
		},
		{
			name:           "security fix in patch — patch recommended",
			currentVersion: "1.2.3",
			availableVers:  []string{"2.0.0", "1.5.0", "1.2.5", "1.2.3"},
			advData: []advisories.Advisory{
				{Version: "1.2.5", ContainsSecurityFix: true, Summary: "CVE-2024-1234 fixed"},
			},
			wantCards:       3,
			wantRecommended: "1.2.5",
			checkCards: func(t *testing.T, cards []models.RecommendationCard) {
				t.Helper()
				for _, c := range cards {
					if c.Version == "1.2.5" && !c.HasSecurity {
						t.Error("expected patch card 1.2.5 to have HasSecurity=true")
					}
				}
			},
		},
		{
			name:           "security fix only in in-major — in-major recommended",
			currentVersion: "1.2.3",
			availableVers:  []string{"2.0.0", "1.5.0", "1.2.5", "1.2.3"},
			advData: []advisories.Advisory{
				{Version: "1.5.0", ContainsSecurityFix: true, Summary: "security patch"},
			},
			wantCards:       3,
			wantRecommended: "1.5.0",
		},
		{
			name:            "no upgrade available",
			currentVersion:  "1.2.3",
			availableVers:   []string{"1.2.3"},
			wantCards:       0,
			wantRecommended: "",
		},
		{
			name:            "latest stable same major — no cross-major card",
			currentVersion:  "1.2.3",
			availableVers:   []string{"1.5.0", "1.2.5", "1.2.3"},
			wantCards:       2, // patch + in-major only
			wantRecommended: "1.2.5",
		},
		{
			name:            "current is invalid semver — returns empty",
			currentVersion:  "not-semver",
			availableVers:   []string{"1.2.3"},
			wantCards:       0,
			wantRecommended: "",
		},
		{
			name:            "nil advisory source — cards built without security data",
			currentVersion:  "1.2.3",
			availableVers:   []string{"2.0.0", "1.3.0", "1.2.5", "1.2.3"},
			advData:         nil, // will use nil advisorySource
			wantCards:       3,
			wantRecommended: "1.2.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			helmSrv := newHelmServer(t, chart, tc.availableVers)

			var advSrc advisorySource
			if tc.advData != nil {
				advSrc = &mockAdvisorySource{data: tc.advData}
			}

			svc := newTestUpgradeSvc(advSrc)

			gp := &fakeGitProvider{
				files: map[string][]byte{
					"configuration/addons-catalog.yaml": catalogYAML(addon, chart, helmSrv.URL, tc.currentVersion),
				},
			}

			rec, err := svc.GetRecommendations(context.Background(), addon, gp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(rec.Cards) != tc.wantCards {
				t.Errorf("wantCards=%d got=%d\ncards=%+v", tc.wantCards, len(rec.Cards), rec.Cards)
			}

			if rec.Recommended != tc.wantRecommended {
				t.Errorf("wantRecommended=%q got=%q", tc.wantRecommended, rec.Recommended)
			}

			// Exactly one IsRecommended card when cards present
			if len(rec.Cards) > 0 {
				count := 0
				for _, c := range rec.Cards {
					if c.IsRecommended {
						count++
					}
				}
				if count != 1 {
					t.Errorf("expected exactly 1 IsRecommended card, got %d (cards=%+v)", count, rec.Cards)
				}
			}

			// Legacy fields should still be populated when cards are returned
			if len(rec.Cards) > 0 && tc.currentVersion != "not-semver" {
				anyLegacy := rec.NextPatch != "" || rec.NextMinor != "" || rec.LatestStable != ""
				if !anyLegacy {
					t.Error("expected at least one legacy field (NextPatch/NextMinor/LatestStable) to be set")
				}
			}

			if tc.checkCards != nil {
				tc.checkCards(t, rec.Cards)
			}
		})
	}
}

// --- buildCards unit tests ---

func TestBuildCardsInMajorSkippedWhenEqualsLatest(t *testing.T) {
	cur := semverParts{major: 1, minor: 2, patch: 3}
	// latest same as in-major — should not produce duplicate card
	cards, recommended := buildCards(cur, "1.2.5", "1.5.0", "1.5.0", map[string]advisories.Advisory{})
	if len(cards) != 2 {
		t.Errorf("expected 2 cards (patch + in-major deduped with latest), got %d: %+v", len(cards), cards)
	}
	_ = recommended
}

func TestBuildCardsLatestSameMajorSkipped(t *testing.T) {
	cur := semverParts{major: 1, minor: 2, patch: 3}
	// latestVer same major as current — no "Latest Stable" card
	cards, _ := buildCards(cur, "1.2.5", "1.5.0", "1.5.0", map[string]advisories.Advisory{})
	for _, c := range cards {
		if c.Label == "Latest Stable" {
			t.Error("expected no Latest Stable card when latest is same major as current")
		}
	}
}

func TestBuildCardsCrossMajorFlagged(t *testing.T) {
	cur := semverParts{major: 1, minor: 2, patch: 3}
	cards, _ := buildCards(cur, "", "", "2.0.0", map[string]advisories.Advisory{})
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	if !cards[0].CrossMajor {
		t.Error("expected CrossMajor=true for 2.0.0 when current is 1.x")
	}
	if !cards[0].HasBreaking {
		t.Error("expected HasBreaking=true for cross-major card")
	}
}

// --- pickRecommended unit tests ---

func TestPickRecommendedSecurityPatchWins(t *testing.T) {
	cards := []models.RecommendationCard{
		{Label: "Patch", Version: "1.2.5", HasSecurity: true},
		{Label: "Latest in 1.x", Version: "1.5.0", HasSecurity: true},
		{Label: "Latest Stable", Version: "2.0.0", CrossMajor: true},
	}
	idx := pickRecommended(cards)
	if idx != 0 {
		t.Errorf("expected patch card (idx 0), got %d", idx)
	}
}

func TestPickRecommendedInMajorSecurityWhenNoPatchCard(t *testing.T) {
	cards := []models.RecommendationCard{
		{Label: "Latest in 1.x", Version: "1.5.0", HasSecurity: true},
		{Label: "Latest Stable", Version: "2.0.0", CrossMajor: true},
	}
	idx := pickRecommended(cards)
	if idx != 0 {
		t.Errorf("expected in-major card (idx 0), got %d", idx)
	}
}

func TestPickRecommendedFirstCardWhenNoSecurity(t *testing.T) {
	cards := []models.RecommendationCard{
		{Label: "Patch", Version: "1.2.5"},
		{Label: "Latest in 1.x", Version: "1.5.0"},
		{Label: "Latest Stable", Version: "2.0.0", CrossMajor: true},
	}
	// No security on any card → first card (safest = patch)
	idx := pickRecommended(cards)
	if idx != 0 {
		t.Errorf("expected first card (Patch, idx 0), got %d", idx)
	}
}

func TestPickRecommendedFallsBackToFirstCard(t *testing.T) {
	cards := []models.RecommendationCard{
		{Label: "Patch", Version: "1.2.5"},
	}
	idx := pickRecommended(cards)
	if idx != 0 {
		t.Errorf("expected first card (idx 0), got %d", idx)
	}
}

func TestPickRecommendedEmpty(t *testing.T) {
	idx := pickRecommended(nil)
	if idx != -1 {
		t.Errorf("expected -1 for empty cards, got %d", idx)
	}
}
