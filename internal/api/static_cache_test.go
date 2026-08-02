package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// sprint-backlog-burndown-r1 Lane B — cache honesty for the embedded SPA.
//
// Bug: after a live image roll, an already-open tab may still hold the OLD
// index.html, which references hashed chunk files the NEW image no longer
// ships. If index.html gets cached and a missing /assets/* file quietly
// falls back to index.html (old SPA-catch-all-for-everything behavior), the
// browser gets HTML back where it expected JS/CSS — a MIME-type error that
// leaves the tab dead until a hard refresh. These tests pin the three-part
// fix in NewRouter's static file handler.

func staticTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte("<html>shell</html>")},
		"assets/index-abc123.js":  {Data: []byte("console.log('app')")},
		"assets/index-abc123.css": {Data: []byte("body{}")},
		"favicon.ico":             {Data: []byte("icon")},
	}
}

func TestStaticIndexHTML_NoCacheHeader(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, staticTestFS())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control for / (index.html) = %q, want %q", got, "no-cache")
	}
}

// TestStaticIndexHTMLDirect_NoCacheHeader covers a literal "/index.html"
// request. Go's http.FileServer always 301-redirects a request for a file
// literally named index.html to its containing directory ("/") — that's
// stdlib behavior, not something this fix changes or needs to fight. What
// matters here is that our handler stamps Cache-Control before handing off
// to fileServer, so even this redirect response carries no-cache (and the
// browser lands on "/", which is separately covered and also no-cache).
func TestStaticIndexHTMLDirect_NoCacheHeader(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, staticTestFS())

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301 (stdlib http.FileServer redirects a literal index.html to /)", w.Code)
	}
	if got := w.Header().Get("Location"); got != "./" {
		t.Errorf("Location = %q, want %q", got, "./")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control for /index.html = %q, want %q", got, "no-cache")
	}
}

func TestStaticExistingAsset_ImmutableCache(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, staticTestFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	want := "public, max-age=31536000, immutable"
	if got := w.Header().Get("Cache-Control"); got != want {
		t.Errorf("Cache-Control for existing asset = %q, want %q", got, want)
	}
}

// TestStaticMissingAsset_Returns404NotIndexHTML is the core regression
// guard for the rolled-over-tab bug: a request for a chunk file that no
// longer exists (because a new image shipped without it) must NOT get the
// SPA's index.html back — that's exactly what produces the browser's
// MIME-type error ("Expected a JavaScript module ... but the server
// responded with a MIME type of text/html").
func TestStaticMissingAsset_Returns404NotIndexHTML(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, staticTestFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/index-DOESNOTEXIST.js", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a missing /assets/* file", w.Code)
	}
	body := w.Body.String()
	if body == "<html>shell</html>" {
		t.Fatalf("missing asset fell back to index.html body — this is the rolled-over-tab bug")
	}
}

// TestStaticRoutePath_StillGetsSPAFallback pins that client-side routes
// (e.g. /clusters) still resolve to index.html — the SPA fallback must
// survive the cache-honesty fix, only the caching behavior changes.
func TestStaticRoutePath_StillGetsSPAFallback(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, staticTestFS())

	req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback for a client-side route)", w.Code)
	}
	if body := w.Body.String(); body != "<html>shell</html>" {
		t.Errorf("body = %q, want the index.html shell for SPA route fallback", body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control for SPA-fallback route = %q, want %q", got, "no-cache")
	}
}
