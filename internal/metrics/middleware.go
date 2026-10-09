package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
//
// The wrapper must transparently expose the optional interfaces that the
// underlying writer implements (Flusher, Hijacker, CloseNotifier).
// Otherwise handlers that rely on a type assertion — most importantly
// Server-Sent Events handlers like /api/v1/audit/stream which do
// `w.(http.Flusher)` — will see the assertion fail and fall back to a
// 500 "streaming not supported" response. WebSocket upgrade paths rely
// on http.Hijacker the same way.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// Flush forwards to the wrapped writer when it implements http.Flusher.
// Required for Server-Sent Events / streaming responses (e.g. /audit/stream).
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped writer when it implements http.Hijacker.
// Required for WebSocket upgrades and any handler that needs to take over
// the underlying TCP connection.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported")
}

// CloseNotify forwards to the wrapped writer when it implements
// http.CloseNotifier. The interface is deprecated in favour of
// Request.Context().Done(), but some libraries still rely on the
// type-assertion shape.
//
//nolint:staticcheck // CloseNotifier is deprecated but downstream code still uses it
func (rw *responseWriter) CloseNotify() <-chan bool {
	if cn, ok := rw.ResponseWriter.(http.CloseNotifier); ok {
		return cn.CloseNotify()
	}
	closed := make(chan bool, 1)
	return closed
}

// Middleware returns an HTTP middleware that records request count and duration
// metrics for every request. It normalizes URL paths to prevent cardinality
// explosion from dynamic segments (e.g. cluster names, addon names).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics endpoint itself to avoid self-referential noise.
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		RecordHTTPRequest(r.Method, r.URL.Path, rw.statusCode, duration)
	})
}

// NormalizePath replaces dynamic path segments with placeholders to keep
// metric cardinality bounded. Known resource names (clusters, addons, etc.)
// have their next segment replaced with a placeholder like {name} or {id}.
func NormalizePath(path string) string {
	parts := strings.Split(path, "/")

	// Known resource positions: the segment immediately after these words is a
	// dynamic identifier.
	resources := map[string]string{
		"clusters":    "{name}",
		"addons":      "{name}",
		"connections": "{name}",
		"users":       "{name}",
		"tokens":      "{id}",
		"prs":         "{id}",
		"operations":  "{id}",
		"docs":        "{slug}",
	}

	for i := range parts {
		if i > 0 {
			if placeholder, ok := resources[parts[i-1]]; ok {
				parts[i] = placeholder
			}
		}
	}
	return strings.Join(parts, "/")
}
