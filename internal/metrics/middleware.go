package metrics

import (
	"net/http"
	"strings"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
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
