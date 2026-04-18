package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		in          string
		wantOwner   string
		wantRepo    string
		wantOK      bool
	}{
		{"https://github.com/cert-manager/cert-manager", "cert-manager", "cert-manager", true},
		{"http://github.com/kyverno/kyverno.git", "kyverno", "kyverno", true},
		{"https://github.com/grafana/grafana/tree/main", "grafana", "grafana", true},
		{"https://gitlab.com/foo/bar", "", "", false},
		{"", "", "", false},
		{"https://github.com/solo-project", "", "", false},
	}
	for _, tc := range cases {
		gotOwner, gotRepo, gotOK := parseGitHubURL(tc.in)
		if gotOwner != tc.wantOwner || gotRepo != tc.wantRepo || gotOK != tc.wantOK {
			t.Errorf("parseGitHubURL(%q) = (%q, %q, %v); want (%q, %q, %v)",
				tc.in, gotOwner, gotRepo, gotOK, tc.wantOwner, tc.wantRepo, tc.wantOK)
		}
	}
}

// countingMetrics records the counter deltas so the refresh-job tests can
// assert outcome counts without importing Prometheus.
type countingMetrics struct {
	success    int64
	errors     int64
	lastRefresh atomic.Int64
}

func (m *countingMetrics) IncRefreshTotal(status string, delta int) {
	switch status {
	case "success":
		atomic.AddInt64(&m.success, int64(delta))
	case "error":
		atomic.AddInt64(&m.errors, int64(delta))
	}
}
func (m *countingMetrics) SetLastRefreshTimestamp(ts time.Time) {
	m.lastRefresh.Store(ts.Unix())
}

func TestScheduler_Refresh_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// URL shape: /github.com/<owner>/<repo>
		if !strings.HasPrefix(r.URL.Path, "/github.com/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"score": 8.4, "checks": []}`))
	}))
	defer srv.Close()

	y := `
addons:
  - name: cert-manager
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    source_url: https://github.com/cert-manager/cert-manager
    security_score: unknown
  - name: no-source-url
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	metrics := &countingMetrics{}
	sched := NewScheduler(cat, metrics).WithEndpoint(srv.URL).WithInterval(24 * time.Hour)
	sched.Refresh(context.Background())

	if atomic.LoadInt64(&metrics.success) != 1 {
		t.Errorf("want 1 success, got %d", metrics.success)
	}
	if atomic.LoadInt64(&metrics.errors) != 0 {
		t.Errorf("want 0 errors, got %d", metrics.errors)
	}
	if metrics.lastRefresh.Load() == 0 {
		t.Errorf("lastRefresh timestamp should be set")
	}

	e, _ := cat.Get("cert-manager")
	if !e.SecurityScore.Known || e.SecurityScore.Value != 8.4 {
		t.Errorf("expected score 8.4, got %+v", e.SecurityScore)
	}
	if e.SecurityTier != "Strong" {
		t.Errorf("tier after score 8.4: got %q, want Strong", e.SecurityTier)
	}
	if e.SecurityScoreUpdated == "" {
		t.Errorf("expected security_score_updated to be set")
	}
}

func TestScheduler_Refresh_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream kaput", http.StatusInternalServerError)
	}))
	defer srv.Close()

	y := `
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
    source_url: https://github.com/foo/foo
    security_score: 6.0
`
	cat, err := LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	metrics := &countingMetrics{}
	sched := NewScheduler(cat, metrics).WithEndpoint(srv.URL)
	sched.Refresh(context.Background())

	if atomic.LoadInt64(&metrics.errors) != 1 {
		t.Errorf("want 1 error, got %d", metrics.errors)
	}
	// The pre-existing score must be retained when the upstream fails.
	e, _ := cat.Get("foo")
	if !e.SecurityScore.Known || e.SecurityScore.Value != 6.0 {
		t.Errorf("expected retained 6.0, got %+v", e.SecurityScore)
	}
}

func TestScheduler_NextTickDelay(t *testing.T) {
	cat, err := LoadBytes([]byte(`
addons:
  - name: foo
    description: x
    chart: x
    repo: https://x
    default_namespace: x
    maintainers: [m]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	sched := NewScheduler(cat, nil)
	// At 03:00 UTC the next 04:00 UTC is 1h away.
	now := time.Date(2026, 4, 17, 3, 0, 0, 0, time.UTC)
	if got := sched.nextTickDelay(now); got != time.Hour {
		t.Errorf("nextTickDelay at 03:00 UTC: got %v, want 1h", got)
	}
	// At 05:00 UTC the next 04:00 is 23h away.
	now = time.Date(2026, 4, 17, 5, 0, 0, 0, time.UTC)
	if got := sched.nextTickDelay(now); got != 23*time.Hour {
		t.Errorf("nextTickDelay at 05:00 UTC: got %v, want 23h", got)
	}
	// Short intervals bypass wall-clock alignment.
	sched.WithInterval(200 * time.Millisecond)
	if got := sched.nextTickDelay(now); got != 200*time.Millisecond {
		t.Errorf("short interval should bypass alignment: got %v", got)
	}
}
