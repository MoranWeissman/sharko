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

// TestSwaggerCSPAllowsInlineScript verifies that /swagger/-prefixed paths receive
// a relaxed CSP whose script-src permits 'unsafe-inline' (the swagger-ui-dist page
// bootstraps via an inline <script>), while normal API paths keep the strict
// script-src 'self' with no inline. Both branches must still carry the other
// security headers unchanged (V2-cleanup-46).
func TestSwaggerCSPAllowsInlineScript(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	// scriptSrc extracts the script-src directive value from a full CSP string.
	scriptSrc := func(csp string) string {
		for _, d := range strings.Split(csp, ";") {
			d = strings.TrimSpace(d)
			if strings.HasPrefix(d, "script-src ") {
				return d
			}
		}
		return ""
	}

	t.Run("swagger path allows inline script", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/swagger/index.html", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		csp := w.Header().Get("Content-Security-Policy")
		sd := scriptSrc(csp)
		if !strings.Contains(sd, "'unsafe-inline'") {
			t.Errorf("swagger CSP script-src should include 'unsafe-inline'; got: %s", sd)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options: got %q, want %q", got, "nosniff")
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options: got %q, want %q", got, "DENY")
		}
	})

	t.Run("api path keeps strict script-src self", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		csp := w.Header().Get("Content-Security-Policy")
		sd := scriptSrc(csp)
		if sd != "script-src 'self'" {
			t.Errorf("api CSP script-src should be exactly \"script-src 'self'\"; got: %q", sd)
		}
		if strings.Contains(sd, "'unsafe-inline'") {
			t.Errorf("api CSP script-src must NOT include 'unsafe-inline'; got: %s", sd)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options: got %q, want %q", got, "nosniff")
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options: got %q, want %q", got, "DENY")
		}
	})
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
