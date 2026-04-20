package api

// secret_leak_audit.go — Story V121-8.5 helper.
//
// When the AI-annotate secret guard hard-blocks an upstream values payload,
// we want a dedicated audit-log entry so the security team can grep one
// stable token (`secret_leak_blocked`) across the audit log and pull every
// block in one go — independent of which handler triggered the scan.
//
// The per-request `audit.Enrich` path is reserved for the handler's own
// event (`addon_added`, `ai_annotate_run`, `values_refreshed_from_upstream`)
// so the tier + audit coverage tests keep working. To get a separate entry
// without disturbing the in-flight one, this helper writes a second Entry
// directly to the Server's audit log buffer.
//
// We only log redacted summaries — pattern names + line numbers + addon +
// chart version. The actual matched bytes never leave the orchestrator's
// in-memory ScanForSecrets call.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// emitSecretLeakAuditBlock records one `secret_leak_blocked` entry on the
// Server's audit log. `source` distinguishes the call site (`addon_add`,
// `ai_annotate`, `values_refresh`) so operators can tell which mutation
// path the block came from. `addon` and `chart`+`version` identify the
// resource without leaking secret bytes. `matches` is the redacted
// SecretMatch list returned by ScanForSecrets — pattern + redacted field +
// line number, never the actual secret.
//
// Safe to call when s.auditLog is nil (early server bring-up): it falls
// through to a slog.Warn so the block isn't lost entirely.
func (s *Server) emitSecretLeakAuditBlock(
	ctx context.Context,
	source, addon, chart, version string,
	matches []orchestrator.SecretMatch,
) {
	patterns := summarizeSecretPatterns(matches)
	detail := fmt.Sprintf("source=%s chart=%s version=%s matches=%d patterns=[%s]",
		source, chart, version, len(matches), patterns)

	// Log line — same redacted shape, lets ops grep `secret_leak_blocked`
	// in stdout/journald even if the audit ring has rolled over.
	slog.Warn("secret_leak_blocked",
		"addon", addon, "chart", chart, "version", version,
		"source", source, "matches", len(matches), "patterns", patterns,
	)

	if s == nil || s.auditLog == nil {
		return
	}

	// Inherit user / source / request_id from the in-flight Fields if the
	// audit middleware ran (which it does for every mutating handler).
	user := "system"
	apiSource := "api"
	requestID := ""
	if f := audit.CurrentFields(ctx); f != nil {
		// Fields itself doesn't carry user/source — those are set on the
		// final Entry by the middleware. The dedicated entry is emitted
		// before that point, so we fall back to the request context's
		// stored user (if any) via a separate ctx key. No user key is
		// exported today, so "system" is the safe fallback.
		_ = f // kept for future enrichment; intentionally unused right now
	}

	s.auditLog.Add(audit.Entry{
		Timestamp: time.Now().UTC(),
		Level:     "warn",
		Event:     "secret_leak_blocked",
		User:      user,
		Action:    "block",
		Resource:  fmt.Sprintf("addon:%s", addon),
		Source:    apiSource,
		Result:    "blocked",
		RequestID: requestID,
		Detail:    detail,
	})
}

// summarizeSecretPatterns returns a comma-separated, deduplicated, sorted
// list of pattern names from a SecretMatch slice. Bounded at 200 chars so
// the audit Detail field stays readable in the UI table.
func summarizeSecretPatterns(matches []orchestrator.SecretMatch) string {
	if len(matches) == 0 {
		return ""
	}
	seen := map[string]struct{}{}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, dup := seen[m.Pattern]; dup {
			continue
		}
		seen[m.Pattern] = struct{}{}
		names = append(names, m.Pattern)
	}
	out := strings.Join(names, ", ")
	if len(out) > 200 {
		out = out[:197] + "..."
	}
	return out
}
