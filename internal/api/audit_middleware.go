package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
)

// auditMiddleware emits an audit.Entry for every mutating API request.
// GETs, HEADs, and OPTIONS are skipped (read-only). The middleware ALWAYS
// emits — handlers enrich the entry via audit.Enrich(ctx, ...) before
// returning. Handlers that previously called s.auditLog.Add directly should
// migrate to audit.Enrich so only one entry is emitted per request.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only audit mutating methods on API paths.
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Skip paths that emit their own fine-grained audit entries inside the handler.
		// login/logout emit login, login_failed, logout with exact semantics the middleware
		// cannot reproduce (e.g. login_failed on 401 before handler responds).
		// webhooks emit webhook_received with signature context.
		path := r.URL.Path
		if path == "/api/v1/auth/login" || path == "/api/v1/auth/logout" ||
			path == "/api/v1/webhooks/git" || path == "/api/v1/auth/hash" ||
			path == "/api/v1/login" /* V124-6.1: dead-route stub, no audit needed */ {
			next.ServeHTTP(w, r)
			return
		}

		// Attach an enrichment slot to the context so handlers can fill in
		// semantic fields (Event, Resource, Detail) before returning.
		ctx, fields := audit.WithEnrichment(r.Context())
		r = r.WithContext(ctx)

		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "anonymous"
		}

		source := detectSource(r)
		result := resultFromStatus(rec.statusCode)
		level := levelFromStatus(rec.statusCode)

		event := fields.Event
		if event == "" {
			event = deriveEventName(r.Method, r.URL.Path)
		}

		// Resolve attribution tier and mode. Handlers that use the tier-aware
		// Git resolver (GitProviderForTier) stamp both fields before returning.
		// Handlers that use the legacy GetActiveGitProvider path — or that
		// never touch Git at all — leave them empty. To keep the audit log
		// readable (so the Attribution column doesn't render as a sea of
		// dashes on legitimate mutations), we fall back here:
		//   • Tier is looked up from HandlerTier via the matched route pattern.
		//   • AttributionMode is inferred from the tier + whether a user is
		//     authenticated:
		//       - unknown user or webhook/auth tier → service
		//       - otherwise                         → co_author
		//     per_user can only be reached via GitProviderForTier and is
		//     therefore always explicitly stamped.
		tier := fields.Tier
		if tier == "" {
			tier = tierFromPattern(r.Pattern)
		}
		mode := fields.AttributionMode
		if mode == "" {
			mode = inferAttributionMode(tier, user)
		}

		s.auditLog.Add(audit.Entry{
			Level:           level,
			Event:           event,
			User:            user,
			Action:          methodToAction(r.Method),
			Resource:        fields.Resource,
			Detail:          fields.Detail,
			Source:          source,
			Result:          result,
			DurationMs:      duration.Milliseconds(),
			Tier:            tier,
			AttributionMode: mode,
		})
	})
}

// detectSource infers the origin of a request from the User-Agent and auth headers.
func detectSource(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	uaLower := strings.ToLower(ua)

	if strings.Contains(uaLower, "sharko-cli") || strings.Contains(ua, "Sharko-CLI") {
		return "cli"
	}
	if strings.Contains(uaLower, "mozilla") || strings.Contains(uaLower, "webkit") ||
		strings.Contains(uaLower, "chrome") || strings.Contains(uaLower, "safari") {
		return "ui"
	}

	// Bearer token that starts with sharko_ prefix → API key (programmatic access).
	if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
		token := strings.TrimPrefix(authHdr, "Bearer ")
		if strings.HasPrefix(token, "sharko_") {
			return "api"
		}
	}

	return "api"
}

// resultFromStatus maps HTTP status codes to audit result strings.
func resultFromStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "success"
	case code == 207:
		return "partial"
	case code >= 400 && code < 500:
		return "rejected"
	default:
		return "failure"
	}
}

// levelFromStatus maps HTTP status codes to audit level strings.
func levelFromStatus(code int) string {
	if code >= 500 {
		return "error"
	}
	if code >= 400 {
		return "warn"
	}
	return "info"
}

// methodToAction converts an HTTP method to a short action word.
func methodToAction(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// patternToTier is populated lazily by handleWithTier — every route
// registration goes through that wrapper so we can resolve the tier for a
// request whose handler never called audit.Enrich with Tier/AttributionMode
// (the legacy-path handlers). The wrapper uses runtime reflection to look
// up the handler's short name and cross-references HandlerTier.
var patternToTier = map[string]audit.Tier{}
var patternToTierMu sync.RWMutex

// rememberTierForPattern stamps a (pattern, tier) pair so auditMiddleware can
// fall back to it when a handler doesn't set the Tier field on the audit
// entry. Idempotent and safe to call concurrently.
func rememberTierForPattern(pattern string, tier audit.Tier) {
	if pattern == "" || tier == "" {
		return
	}
	patternToTierMu.Lock()
	patternToTier[pattern] = tier
	patternToTierMu.Unlock()
}

// tierFromPattern returns the HandlerTier for the given ServeMux pattern
// (e.g. "POST /api/v1/clusters"). Returns "" if unknown.
func tierFromPattern(pattern string) audit.Tier {
	if pattern == "" {
		return ""
	}
	patternToTierMu.RLock()
	t := patternToTier[pattern]
	patternToTierMu.RUnlock()
	return t
}

// inferAttributionMode derives an attribution mode for an audit entry when the
// handler didn't stamp one. See auditMiddleware for the rules.
func inferAttributionMode(tier audit.Tier, user string) audit.AttributionMode {
	switch tier {
	case audit.TierAuth, audit.TierWebhook, audit.TierPersonal:
		// These tiers never write Git as the caller; the entry is informational.
		// Render as service so the column is non-empty and the tooltip still
		// carries the right message ("no human author on the commit").
		return audit.AttributionService
	case audit.Tier1, audit.Tier2:
		// Legacy-path Tier 1/2 handlers use the service token with a
		// Co-authored-by trailer when a user is authenticated, else plain
		// service token.
		if user == "" || user == "anonymous" || user == "system" {
			return audit.AttributionService
		}
		return audit.AttributionCoAuthor
	}
	return ""
}

// deriveEventName builds a best-effort event name from the method and path.
// This is a fallback — handlers should always call audit.Enrich with an explicit Event.
func deriveEventName(method, path string) string {
	// Trim /api/v1/ prefix.
	p := strings.TrimPrefix(path, "/api/v1/")
	// Replace path parameters like {name} segments with their values — we just take
	// the first path component as the resource type.
	parts := strings.SplitN(p, "/", 2)
	resource := parts[0]
	// Replace hyphens for cleaner event names.
	resource = strings.ReplaceAll(resource, "-", "_")

	switch method {
	case http.MethodPost:
		return resource + "_created"
	case http.MethodPut:
		return resource + "_updated"
	case http.MethodPatch:
		return resource + "_patched"
	case http.MethodDelete:
		return resource + "_deleted"
	default:
		return resource + "_" + strings.ToLower(method)
	}
}
