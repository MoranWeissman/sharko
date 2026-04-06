package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersPresent(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}
	for _, tt := range tests {
		got := w.Header().Get(tt.header)
		if got != tt.want {
			t.Errorf("header %s: got %q, want %q", tt.header, got, tt.want)
		}
	}

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header is missing")
	}
	for _, directive := range []string{"default-src", "script-src", "style-src", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing directive %q; got: %s", directive, csp)
		}
	}
}

func TestHSTSPresentOverHTTPS(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("Strict-Transport-Security header should be set when X-Forwarded-Proto is https")
	}
	if !strings.Contains(hsts, "max-age=") {
		t.Errorf("HSTS header missing max-age directive; got: %s", hsts)
	}
	if !strings.Contains(hsts, "includeSubDomains") {
		t.Errorf("HSTS header missing includeSubDomains; got: %s", hsts)
	}
}

func TestHSTSAbsentOverPlainHTTP(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	// No TLS, no X-Forwarded-Proto header — plain HTTP
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts != "" {
		t.Errorf("Strict-Transport-Security should not be set on plain HTTP; got: %s", hsts)
	}
}
