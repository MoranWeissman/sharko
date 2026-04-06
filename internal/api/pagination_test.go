package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Pagination helper tests ---

func TestParsePagination_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	p := parsePagination(req)
	if p.Page != 1 {
		t.Errorf("expected page=1, got %d", p.Page)
	}
	if p.PerPage != 20 {
		t.Errorf("expected per_page=20, got %d", p.PerPage)
	}
}

func TestParsePagination_Custom(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?page=3&per_page=50", nil)
	p := parsePagination(req)
	if p.Page != 3 {
		t.Errorf("expected page=3, got %d", p.Page)
	}
	if p.PerPage != 50 {
		t.Errorf("expected per_page=50, got %d", p.PerPage)
	}
}

func TestParsePagination_ClampsPerPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?per_page=999", nil)
	p := parsePagination(req)
	if p.PerPage != 100 {
		t.Errorf("expected per_page clamped to 100, got %d", p.PerPage)
	}
}

func TestParsePagination_InvalidIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?page=abc&per_page=-5", nil)
	p := parsePagination(req)
	if p.Page != 1 {
		t.Errorf("expected page=1 for invalid input, got %d", p.Page)
	}
	// -5 clamped to 1
	if p.PerPage != 1 {
		t.Errorf("expected per_page=1 for -5, got %d", p.PerPage)
	}
}

func TestApplyPagination_FirstPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p := paginationParams{Page: 1, PerPage: 3}
	result := applyPagination(items, p)
	if len(result) != 3 || result[0] != 1 || result[2] != 3 {
		t.Errorf("unexpected first page result: %v", result)
	}
}

func TestApplyPagination_SecondPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	p := paginationParams{Page: 2, PerPage: 2}
	result := applyPagination(items, p)
	if len(result) != 2 || result[0] != 3 || result[1] != 4 {
		t.Errorf("unexpected second page result: %v", result)
	}
}

func TestApplyPagination_OutOfRange(t *testing.T) {
	items := []int{1, 2, 3}
	p := paginationParams{Page: 5, PerPage: 10}
	result := applyPagination(items, p)
	if len(result) != 0 {
		t.Errorf("expected empty slice for out-of-range page, got %v", result)
	}
}

func TestApplyPagination_LastPagePartial(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	p := paginationParams{Page: 2, PerPage: 3}
	result := applyPagination(items, p)
	if len(result) != 2 || result[0] != 4 || result[1] != 5 {
		t.Errorf("unexpected last partial page result: %v", result)
	}
}

func TestSetPaginationHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	p := paginationParams{Page: 2, PerPage: 10}
	setPaginationHeaders(w, 55, p)

	if got := w.Header().Get("X-Total-Count"); got != "55" {
		t.Errorf("expected X-Total-Count=55, got %q", got)
	}
	if got := w.Header().Get("X-Page"); got != "2" {
		t.Errorf("expected X-Page=2, got %q", got)
	}
	if got := w.Header().Get("X-Per-Page"); got != "10" {
		t.Errorf("expected X-Per-Page=10, got %q", got)
	}
}

// --- Notifications endpoint pagination test ---

func TestNotificationsEndpoint_PaginationHeaders(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?page=1&per_page=5", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Total-Count") == "" {
		t.Error("expected X-Total-Count header to be set")
	}
	if w.Header().Get("X-Page") != "1" {
		t.Errorf("expected X-Page=1, got %q", w.Header().Get("X-Page"))
	}
	if w.Header().Get("X-Per-Page") != "5" {
		t.Errorf("expected X-Per-Page=5, got %q", w.Header().Get("X-Per-Page"))
	}
}

// --- Rate limiter tests ---

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := newRateLimiter(5, 1*time.Minute)
	for i := 0; i < 5; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Errorf("expected allow on attempt %d", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := newRateLimiter(3, 1*time.Minute)
	for i := 0; i < 3; i++ {
		rl.Allow("1.2.3.4")
	}
	if rl.Allow("1.2.3.4") {
		t.Error("expected block after limit exceeded")
	}
}

func TestRateLimiter_SeparateKeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(1, 1*time.Minute)
	rl.Allow("1.1.1.1")
	if !rl.Allow("2.2.2.2") {
		t.Error("different IPs should have independent limits")
	}
}

func TestWriteRateLimiter_BlocksAfterLimit(t *testing.T) {
	srv := newTestServer()

	// Build a minimal handler with only the write rate limiter (limit=2).
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := writeRateLimiter(2, 1*time.Minute)(inner)

	doPost := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", strings.NewReader(`{}`))
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	_ = srv // used indirectly via newTestServer pattern

	if doPost() != http.StatusOK {
		t.Error("first request should succeed")
	}
	if doPost() != http.StatusOK {
		t.Error("second request should succeed")
	}
	if doPost() != http.StatusTooManyRequests {
		t.Error("third request should be rate limited (429)")
	}
}

func TestWriteRateLimiter_GetNotLimited(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := writeRateLimiter(1, 1*time.Minute)(inner)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET request %d should not be rate limited, got %d", i+1, w.Code)
		}
	}
}

func TestWriteRateLimiter_LoginExempt(t *testing.T) {
	// Login should bypass the write rate limiter (it has its own limiter)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := writeRateLimiter(1, 1*time.Minute)(inner)

	doLogin := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// Both calls should succeed (login is exempt from the write limiter)
	for i := 0; i < 3; i++ {
		if doLogin() != http.StatusOK {
			t.Errorf("login attempt %d should not be rate limited by write limiter", i+1)
		}
	}
}
