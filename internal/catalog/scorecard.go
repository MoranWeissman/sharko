package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ScorecardEndpoint is the public OpenSSF Scorecard API base URL. Overridable
// via a field on Scheduler for tests; the zero value uses the production URL.
const ScorecardEndpoint = "https://api.scorecard.dev/projects"

// ScorecardMetrics is the subset of the metrics package the scheduler depends
// on. It is an interface (not a direct import of internal/metrics) to keep the
// catalog package self-contained and to let tests observe the calls without
// importing Prometheus.
type ScorecardMetrics interface {
	IncRefreshTotal(status string, delta int)
	SetLastRefreshTimestamp(ts time.Time)
}

// NoopScorecardMetrics is a drop-in for places that do not wire metrics
// (tests, local dev without Prometheus). All methods are safe to call.
type NoopScorecardMetrics struct{}

func (NoopScorecardMetrics) IncRefreshTotal(string, int)      {}
func (NoopScorecardMetrics) SetLastRefreshTimestamp(time.Time) {}

// Scheduler runs the daily Scorecard refresh. It calls api.scorecard.dev for
// every catalog entry whose source_url points at github.com, parses the
// aggregate score, and updates the in-memory catalog.
//
// The scheduler is started once at server boot and stopped on shutdown. It
// holds a pointer to the Catalog; Catalog.UpdateScore is safe to call from
// this goroutine because the other Catalog read paths only read the struct
// fields and do not race with scalar writes of the score (Go's word-size
// writes are atomic for scalars; a lock would add ceremony without meaningful
// benefit here). If we grow past word-size state this is the first place to
// add a mutex.
type Scheduler struct {
	cat         *Catalog
	client      *http.Client
	endpoint    string // overridable for tests
	interval    time.Duration
	firstTick   time.Time
	metrics     ScorecardMetrics
	runFirstRun bool // when true, fire one refresh immediately on Start (used by tests)

	mu      sync.Mutex
	stopCh  chan struct{}
	running bool
}

// NewScheduler builds a Scheduler that will refresh at each 04:00 UTC wall
// clock tick (per design §4.6). Passing metrics=nil installs the no-op
// implementation.
func NewScheduler(cat *Catalog, metrics ScorecardMetrics) *Scheduler {
	if metrics == nil {
		metrics = NoopScorecardMetrics{}
	}
	return &Scheduler{
		cat:      cat,
		client:   &http.Client{Timeout: 10 * time.Second},
		endpoint: ScorecardEndpoint,
		interval: 24 * time.Hour,
		metrics:  metrics,
	}
}

// WithEndpoint overrides the Scorecard base URL. Tests use this to point at
// an httptest.Server.
func (s *Scheduler) WithEndpoint(url string) *Scheduler {
	s.endpoint = url
	return s
}

// WithInterval overrides the between-tick interval. Tests use sub-second
// intervals; production uses the 24h default.
func (s *Scheduler) WithInterval(d time.Duration) *Scheduler {
	s.interval = d
	return s
}

// WithHTTPClient overrides the HTTP client.
func (s *Scheduler) WithHTTPClient(c *http.Client) *Scheduler {
	if c != nil {
		s.client = c
	}
	return s
}

// WithFirstRunImmediate tells Start to kick off one refresh before the normal
// daily cadence starts ticking. Handy for tests; production code can also set
// this if operators want a score refresh on pod start.
func (s *Scheduler) WithFirstRunImmediate(enable bool) *Scheduler {
	s.runFirstRun = enable
	return s
}

// Start launches the background goroutine. Safe to call once; subsequent
// calls are no-ops until Stop is called.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	go s.loop(ctx)
}

// Stop signals the goroutine to exit. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	close(s.stopCh)
	s.running = false
}

func (s *Scheduler) loop(ctx context.Context) {
	if s.runFirstRun {
		s.Refresh(ctx)
	}
	// Initial delay until the next 04:00 UTC tick. In tests callers set a
	// small interval and the 04:00 alignment is irrelevant; in production
	// this centers the refresh at a quiet hour.
	delay := s.nextTickDelay(time.Now().UTC())
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-timer.C:
			s.Refresh(ctx)
			// Reset for the next interval. Using interval directly keeps it
			// simple and easy to test; production aligns at 04:00 on boot
			// and drifts by the refresh duration (seconds) after that.
			timer.Reset(s.interval)
		}
	}
}

// nextTickDelay computes the time-until the next 04:00 UTC boundary relative
// to now. Exposed for tests.
func (s *Scheduler) nextTickDelay(now time.Time) time.Duration {
	if s.interval < time.Hour {
		// Short intervals skip the wall-clock alignment.
		return s.interval
	}
	target := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.UTC)
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return target.Sub(now)
}

// Refresh runs one pass over every entry whose source_url is a github.com URL
// and updates the in-memory score. Errors are non-fatal per design §4.6; the
// entry keeps its last-known score.
func (s *Scheduler) Refresh(ctx context.Context) {
	if s.cat == nil {
		return
	}
	success := 0
	errs := 0
	today := time.Now().UTC().Format("2006-01-02")
	for _, e := range s.cat.Entries() {
		owner, repo, ok := parseGitHubURL(e.SourceURL)
		if !ok {
			continue
		}
		score, err := s.fetchScore(ctx, owner, repo)
		if err != nil {
			errs++
			slog.Warn("catalog: scorecard refresh failed",
				"addon", e.Name, "owner", owner, "repo", repo, "err", err)
			continue
		}
		s.cat.UpdateScore(e.Name, score, today)
		success++
	}
	if success > 0 {
		s.metrics.IncRefreshTotal("success", success)
	}
	if errs > 0 {
		s.metrics.IncRefreshTotal("error", errs)
	}
	s.metrics.SetLastRefreshTimestamp(time.Now())
}

// scorecardResponse is the slice of api.scorecard.dev we care about. The full
// response has a `checks` array and `metadata` blobs; we only read `score`.
type scorecardResponse struct {
	Score float64 `json:"score"`
}

// fetchScore calls api.scorecard.dev/projects/github.com/<owner>/<repo>.
// Returns (score, nil) on success. 404 is treated as a benign "no data yet"
// and returns (0, err) so the caller skips the entry.
func (s *Scheduler) fetchScore(ctx context.Context, owner, repo string) (float64, error) {
	url := fmt.Sprintf("%s/github.com/%s/%s", strings.TrimRight(s.endpoint, "/"), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a bit of body for diagnostics but cap it.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("scorecard http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload scorecardResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("scorecard decode: %w", err)
	}
	if payload.Score < 0 || payload.Score > 10 {
		return 0, fmt.Errorf("scorecard returned out-of-range score: %v", payload.Score)
	}
	return payload.Score, nil
}

// parseGitHubURL extracts owner + repo from common github.com URL shapes.
// Returns ok=false for non-GitHub URLs or malformed inputs.
func parseGitHubURL(raw string) (owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	// Strip the protocol.
	for _, p := range []string{"https://", "http://"} {
		raw = strings.TrimPrefix(raw, p)
	}
	raw = strings.TrimPrefix(raw, "www.")
	if !strings.HasPrefix(raw, "github.com/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "github.com/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	// Trim anchor / query / path tails.
	if idx := strings.IndexAny(repo, "?#"); idx != -1 {
		repo = repo[:idx]
	}
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
