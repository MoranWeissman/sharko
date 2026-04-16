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
		{
			// Current 0.20.4: has 1.x and 2.x available → expect next-major (1.5.2) + latest stable (2.3.0) = 2 cards
			name:            "next-major card — 0.x current with 1.x and 2.x available",
			currentVersion:  "0.20.4",
			availableVers:   []string{"2.3.0", "2.0.0", "1.5.2", "1.0.0", "0.20.4", "0.18.0"},
			wantCards:       2,
			wantRecommended: "1.5.2", // first card (no security → pick first)
			checkCards: func(t *testing.T, cards []models.RecommendationCard) {
				t.Helper()
				if len(cards) < 2 {
					return
				}
				if cards[0].Label != "Latest in 1.x" || cards[0].Version != "1.5.2" {
					t.Errorf("expected first card Latest in 1.x/1.5.2, got %+v", cards[0])
				}
				if cards[1].Label != "Latest Stable" || cards[1].Version != "2.3.0" {
					t.Errorf("expected second card Latest Stable/2.3.0, got %+v", cards[1])
				}
			},
		},
		{
			// Current 1.3.0: next-major is 2.3.0, which is also latest stable → no duplicate
			name:            "next-major same as latest stable — no duplicate card",
			currentVersion:  "1.3.0",
			availableVers:   []string{"2.3.0", "1.5.2", "1.3.5", "1.3.0"},
			wantCards:       3, // Patch, Latest in 1.x, Latest Stable (next-major==latest, no dup)
			wantRecommended: "1.3.5",
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
	cards, recommended := buildCards(cur, "1.2.5", "1.5.0", "", "1.5.0", map[string]advisories.Advisory{})
	if len(cards) != 2 {
		t.Errorf("expected 2 cards (patch + in-major deduped with latest), got %d: %+v", len(cards), cards)
	}
	_ = recommended
}

func TestBuildCardsLatestSameMajorSkipped(t *testing.T) {
	cur := semverParts{major: 1, minor: 2, patch: 3}
	// latestVer same major as current — no "Latest Stable" card
	cards, _ := buildCards(cur, "1.2.5", "1.5.0", "", "1.5.0", map[string]advisories.Advisory{})
	for _, c := range cards {
		if c.Label == "Latest Stable" {
			t.Error("expected no Latest Stable card when latest is same major as current")
		}
	}
}

func TestBuildCardsCrossMajorFlagged(t *testing.T) {
	cur := semverParts{major: 1, minor: 2, patch: 3}
	cards, _ := buildCards(cur, "", "", "", "2.0.0", map[string]advisories.Advisory{})
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

// --- buildCards next-major tests ---

func TestBuildCardsNextMajor_On0x_NoInMajor(t *testing.T) {
	// Current 0.20.4, no patch, no in-major, nextMajor=1.5.2, latest=2.3.0 → 2 cards
	cur := semverParts{major: 0, minor: 20, patch: 4}
	cards, _ := buildCards(cur, "", "", "1.5.2", "2.3.0", map[string]advisories.Advisory{})
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d: %+v", len(cards), cards)
	}
	if cards[0].Label != "Latest in 1.x" || cards[0].Version != "1.5.2" {
		t.Errorf("expected first card to be Latest in 1.x / 1.5.2, got %+v", cards[0])
	}
	if cards[1].Label != "Latest Stable" || cards[1].Version != "2.3.0" {
		t.Errorf("expected second card to be Latest Stable / 2.3.0, got %+v", cards[1])
	}
}

func TestBuildCardsNextMajor_On0x_WithInMajor(t *testing.T) {
	// Current 0.18.0, no patch, in-major=0.20.4, nextMajor=1.5.2, latest=2.3.0 → 3 cards
	cur := semverParts{major: 0, minor: 18, patch: 0}
	cards, _ := buildCards(cur, "", "0.20.4", "1.5.2", "2.3.0", map[string]advisories.Advisory{})
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %d: %+v", len(cards), cards)
	}
	labels := []string{cards[0].Label, cards[1].Label, cards[2].Label}
	expected := []string{"Latest in 0.x", "Latest in 1.x", "Latest Stable"}
	for i, want := range expected {
		if labels[i] != want {
			t.Errorf("card[%d] label: want %q got %q", i, want, labels[i])
		}
	}
}

func TestBuildCardsNextMajor_SameAsLatest_NoDuplicate(t *testing.T) {
	// Current 1.3.0, patch=1.3.5, in-major=1.5.2, nextMajor=2.3.0, latest=2.3.0
	// nextMajor == latest → should NOT show duplicate; expect 3 cards: Patch, Latest in 1.x, Latest Stable
	cur := semverParts{major: 1, minor: 3, patch: 0}
	cards, _ := buildCards(cur, "1.3.5", "1.5.2", "2.3.0", "2.3.0", map[string]advisories.Advisory{})
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards (no duplicate for nextMajor==latest), got %d: %+v", len(cards), cards)
	}
	// Verify no two cards have the same version
	seen := make(map[string]bool)
	for _, c := range cards {
		if seen[c.Version] {
			t.Errorf("duplicate version %q in cards", c.Version)
		}
		seen[c.Version] = true
	}
	// Latest Stable should show for 2.3.0
	if cards[2].Label != "Latest Stable" || cards[2].Version != "2.3.0" {
		t.Errorf("expected third card Latest Stable/2.3.0, got %+v", cards[2])
	}
}

func TestBuildCardsNextMajor_On2x_NoNextMajor(t *testing.T) {
	// Current 2.0.0, latest=2.3.0, no nextMajor → 1 card (in-major only, same major as latest)
	cur := semverParts{major: 2, minor: 0, patch: 0}
	cards, _ := buildCards(cur, "", "2.3.0", "", "2.3.0", map[string]advisories.Advisory{})
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d: %+v", len(cards), cards)
	}
	if cards[0].Label != "Latest in 2.x" {
		t.Errorf("expected Latest in 2.x, got %q", cards[0].Label)
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
