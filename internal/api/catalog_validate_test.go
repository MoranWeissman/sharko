package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/helm"
)

// catalog_validate_test.go — V121-4.1 unit tests for the /catalog/validate
// endpoint. We exercise the no-network branches (input validation, error
// classification, cache hit reshape) directly. The upstream Helm fetch is left
// to integration tests since it crosses a network boundary.

func TestValidateRepoURL_AcceptsValid(t *testing.T) {
	for _, in := range []string{
		"https://charts.jetstack.io",
		"http://example.test/charts",
		"https://example.test:8443/path",
	} {
		if err := validateRepoURL(in); err != nil {
			t.Errorf("validateRepoURL(%q) = %v, want nil", in, err)
		}
	}
}

func TestValidateRepoURL_RejectsInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"not a url",
		"ftp://example.test",
		"file:///etc/passwd",
		"https://",
		"http://",
	} {
		if err := validateRepoURL(in); err == nil {
			t.Errorf("validateRepoURL(%q) = nil, want error", in)
		}
	}
}

func TestClassifyValidateError_AllBranches(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   validateErrorCode
		substr string
	}{
		{
			name:   "context deadline → timeout",
			err:    context.DeadlineExceeded,
			want:   validateErrTimeout,
			substr: "timed out",
		},
		{
			name:   "wrapped deadline still classifies as timeout",
			err:    wrapErr(context.DeadlineExceeded),
			want:   validateErrTimeout,
			substr: "timed out",
		},
		{
			name:   "chart missing → chart_not_found",
			err:    errors.New(`chart "ghost" not found in repo`),
			want:   validateErrChartNotFound,
			substr: "is not present",
		},
		{
			name:   "yaml parse failure → index_parse_error",
			err:    errors.New("parsing index: yaml: line 3: invalid scalar"),
			want:   validateErrIndexParseError,
			substr: "malformed",
		},
		{
			name:   "non-200 status → repo_unreachable",
			err:    errors.New("fetching index returned status 404"),
			want:   validateErrRepoUnreachable,
			substr: "could not fetch",
		},
		{
			name:   "network error → repo_unreachable",
			err:    errors.New("fetching index: dial tcp: lookup example.test: no such host"),
			want:   validateErrRepoUnreachable,
			substr: "could not fetch",
		},
		{
			name:   "io error → repo_unreachable",
			err:    errors.New("reading index: unexpected EOF"),
			want:   validateErrRepoUnreachable,
			substr: "could not fetch",
		},
		{
			name: "unknown error falls back to repo_unreachable",
			err:  errors.New("something weird"),
			want: validateErrRepoUnreachable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := classifyValidateError(c.err, "https://example.test", "demo")
			if out.Valid {
				t.Fatal("Valid=true, want false")
			}
			if out.ErrorCode != c.want {
				t.Errorf("ErrorCode = %q, want %q", out.ErrorCode, c.want)
			}
			if c.substr != "" && !strings.Contains(out.Message, c.substr) {
				t.Errorf("Message %q does not contain %q", out.Message, c.substr)
			}
			if out.Repo != "https://example.test" || out.Chart != "demo" {
				t.Errorf("repo/chart not echoed: got repo=%q chart=%q", out.Repo, out.Chart)
			}
		})
	}
}

func TestHandleValidateCatalogChart_RejectsMissingParams(t *testing.T) {
	srv := &Server{}
	cases := []string{
		"/api/v1/catalog/validate",
		"/api/v1/catalog/validate?repo=",
		"/api/v1/catalog/validate?chart=cert-manager",
		"/api/v1/catalog/validate?repo=https://example.test",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rw := httptest.NewRecorder()
			srv.handleValidateCatalogChart(rw, req)
			if rw.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rw.Code)
			}
		})
	}
}

func TestHandleValidateCatalogChart_RejectsBadURL(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/catalog/validate?repo=ftp://nope&chart=cert-manager",
		nil,
	)
	rw := httptest.NewRecorder()
	srv.handleValidateCatalogChart(rw, req)

	// Bad URL is structured failure (200 + invalid_input), not 400 — keeps
	// the UI's switch table consistent with all other failure modes.
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body catalogValidateResponse
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Valid {
		t.Fatal("Valid=true, want false")
	}
	if body.ErrorCode != validateErrInvalidInput {
		t.Errorf("ErrorCode = %q, want %q", body.ErrorCode, validateErrInvalidInput)
	}
}

func TestHandleValidateCatalogChart_CacheHitServesWithoutFetcher(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	// Pre-populate the cache with a versions response — handler should serve
	// directly without touching the network. Trailing slash is stripped to
	// match the handler's normalisation.
	cached := buildVersionsResponse("cert-manager", "cert-manager", "https://charts.jetstack.io",
		toChartVersionsForTest([]string{"1.20.2", "1.20.1", "1.20.0-rc.1"}),
	)
	storeCachedVersions("validate|https://charts.jetstack.io|cert-manager", cached)

	srv := &Server{}
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/catalog/validate?repo=https://charts.jetstack.io/&chart=cert-manager",
		nil,
	)
	rw := httptest.NewRecorder()
	srv.handleValidateCatalogChart(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body catalogValidateResponse
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Valid {
		t.Fatal("Valid=false, want true (cache hit)")
	}
	if body.Chart != "cert-manager" {
		t.Errorf("chart = %q, want cert-manager", body.Chart)
	}
	if body.Repo != "https://charts.jetstack.io" {
		t.Errorf("repo = %q, want trailing-slash stripped", body.Repo)
	}
	if body.LatestStable == "" || body.LatestStable == "1.20.0-rc.1" {
		t.Errorf("latest_stable = %q, want a stable version", body.LatestStable)
	}
	if len(body.Versions) != 3 {
		t.Errorf("versions length = %d, want 3", len(body.Versions))
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// wrapErr returns an error that wraps inner so errors.Is still walks through
// to the sentinel. Mirrors what context.WithTimeout produces in the handler.
func wrapErr(inner error) error {
	return &timeoutWrapper{inner: inner}
}

type timeoutWrapper struct{ inner error }

func (w *timeoutWrapper) Error() string { return "fetching index: " + w.inner.Error() }
func (w *timeoutWrapper) Unwrap() error { return w.inner }

// toChartVersionsForTest builds helm.ChartVersion entries with just a Version
// set — enough for buildVersionsResponse to sort and pick a latest_stable.
func toChartVersionsForTest(versions []string) []helm.ChartVersion {
	out := make([]helm.ChartVersion, len(versions))
	for i, v := range versions {
		out[i] = helm.ChartVersion{Version: v}
	}
	return out
}
