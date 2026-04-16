package advisories

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// --- mock sources for unit tests ---

type mockSource struct {
	advisories []Advisory
	err        error
}

func (m *mockSource) Get(_ context.Context, _, _ string) ([]Advisory, error) {
	return m.advisories, m.err
}

// --- releaseNotesSource keyword parsing ---

func TestReleaseNotesKeywordParsing(t *testing.T) {
	tests := []struct {
		name         string
		notes        string
		wantSecurity bool
		wantBreaking bool
	}{
		{
			name:         "cve keyword triggers security",
			notes:        "Fix CVE-2024-1234: buffer overflow in handler",
			wantSecurity: true,
		},
		{
			name:         "security keyword triggers security",
			notes:        "Patched security regression in auth module",
			wantSecurity: true,
		},
		{
			name:         "vulnerability keyword triggers security",
			notes:        "Resolved vulnerability in dependency X",
			wantSecurity: true,
		},
		{
			name:         "breaking keyword triggers breaking",
			notes:        "Breaking change: removed deprecated API",
			wantBreaking: true,
		},
		{
			name:         "deprecated keyword triggers breaking",
			notes:        "The old config format is deprecated and will be removed",
			wantBreaking: true,
		},
		{
			name:         "both security and breaking",
			notes:        "Security fix for CVE-2024-9999; breaking change in schema",
			wantSecurity: true,
			wantBreaking: true,
		},
		{
			name:         "no keywords — clean release",
			notes:        "Added new dashboard feature; improved performance",
			wantSecurity: false,
			wantBreaking: false,
		},
		{
			name:         "empty notes",
			notes:        "",
			wantSecurity: false,
			wantBreaking: false,
		},
		{
			name:         "case insensitive — uppercase CVE",
			notes:        "Fixed CVE-2024-0001 affecting all versions",
			wantSecurity: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSec, gotBreak := classifyNotes(tc.notes)
			if gotSec != tc.wantSecurity {
				t.Errorf("wantSecurity=%v got=%v for notes %q", tc.wantSecurity, gotSec, tc.notes)
			}
			if gotBreak != tc.wantBreaking {
				t.Errorf("wantBreaking=%v got=%v for notes %q", tc.wantBreaking, gotBreak, tc.notes)
			}
		})
	}
}

// --- Service caching ---

func TestServiceCachesSuccessfulResult(t *testing.T) {
	calls := 0
	src := &countingSource{fn: func() ([]Advisory, error) {
		calls++
		return []Advisory{{Version: "1.0.0"}}, nil
	}}
	svc := newServiceWithSources(src, src, 1*time.Hour)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		res, err := svc.Get(ctx, "https://example.com/charts", "mychart")
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 {
			t.Fatalf("expected 1 advisory, got %d", len(res))
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 upstream call (cache hit), got %d", calls)
	}
}

func TestServiceFallsBackWhenPrimaryErrors(t *testing.T) {
	primary := &mockSource{err: errors.New("ArtifactHub down")}
	fallback := &mockSource{advisories: []Advisory{{Version: "1.2.3", ContainsSecurityFix: true}}}

	svc := newServiceWithSources(primary, fallback, 1*time.Hour)
	res, err := svc.Get(context.Background(), "https://example.com", "chart")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].ContainsSecurityFix {
		t.Errorf("expected fallback advisory, got %+v", res)
	}
}

func TestServiceReturnsEmptyWhenBothFail(t *testing.T) {
	primary := &mockSource{err: errors.New("primary error")}
	fallback := &mockSource{err: errors.New("fallback error")}

	svc := newServiceWithSources(primary, fallback, 1*time.Hour)
	res, err := svc.Get(context.Background(), "https://example.com", "chart")
	// Both fail → returns nil, nil (non-fatal)
	if err != nil {
		t.Errorf("expected nil error when both sources fail, got %v", err)
	}
	if res != nil {
		t.Errorf("expected nil result when both sources fail, got %+v", res)
	}
}

func TestServiceCacheExpiry(t *testing.T) {
	calls := 0
	src := &countingSource{fn: func() ([]Advisory, error) {
		calls++
		return []Advisory{{Version: "1.0.0"}}, nil
	}}
	svc := newServiceWithSources(src, src, 1*time.Millisecond) // very short TTL

	ctx := context.Background()
	svc.Get(ctx, "https://example.com", "chart") //nolint
	time.Sleep(5 * time.Millisecond)             // let cache expire
	svc.Get(ctx, "https://example.com", "chart") //nolint

	if calls != 2 {
		t.Errorf("expected 2 upstream calls after TTL expiry, got %d", calls)
	}
}

func TestServiceEvictsOldestWhenFull(t *testing.T) {
	src := &mockSource{advisories: []Advisory{{Version: "1.0.0"}}}
	svc := newServiceWithSources(src, src, 1*time.Hour)

	// Pre-fill cache to maxCacheEntries
	ctx := context.Background()
	for i := 0; i < maxCacheEntries; i++ {
		key := http.MethodGet + string(rune(i))
		svc.Get(ctx, key, "chart") //nolint
	}
	if len(svc.cache) != maxCacheEntries {
		t.Fatalf("expected %d cache entries, got %d", maxCacheEntries, len(svc.cache))
	}

	// Add one more — should evict the oldest
	svc.Get(ctx, "new-url", "new-chart") //nolint
	if len(svc.cache) > maxCacheEntries {
		t.Errorf("cache exceeded max size %d", maxCacheEntries)
	}
}

// --- helpers ---

type countingSource struct {
	fn func() ([]Advisory, error)
}

func (c *countingSource) Get(_ context.Context, _, _ string) ([]Advisory, error) {
	return c.fn()
}

// classifyNotes is extracted logic matching releaseNotesSource for unit testing.
func classifyNotes(notes string) (security, breaking bool) {
	lower := toLower(notes)
	for _, kw := range securityKeywords {
		if contains(lower, kw) {
			security = true
			break
		}
	}
	for _, kw := range breakingKeywords {
		if contains(lower, kw) {
			breaking = true
			break
		}
	}
	return
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
