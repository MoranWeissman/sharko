package api

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

func TestEmitSecretLeakAuditBlock_WritesGrepableEntry(t *testing.T) {
	srv := &Server{auditLog: audit.NewLog(10)}
	matches := []orchestrator.SecretMatch{
		{Pattern: "AWS access key", Field: "key: ***", Line: 12},
		{Pattern: "GitHub classic PAT", Field: "token: ***", Line: 18},
		// Duplicate pattern — should be deduped in the summary.
		{Pattern: "AWS access key", Field: "extra: ***", Line: 22},
	}

	srv.emitSecretLeakAuditBlock(context.Background(),
		"addon_add", "cert-manager", "cert-manager", "1.14.5", matches)

	entries := srv.auditLog.List(0)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Event != "secret_leak_blocked" {
		t.Errorf("Event = %q, want secret_leak_blocked", e.Event)
	}
	if e.Resource != "addon:cert-manager" {
		t.Errorf("Resource = %q, want addon:cert-manager", e.Resource)
	}
	if e.Result != "blocked" {
		t.Errorf("Result = %q, want blocked", e.Result)
	}
	if !strings.Contains(e.Detail, "source=addon_add") {
		t.Errorf("Detail missing source: %q", e.Detail)
	}
	if !strings.Contains(e.Detail, "matches=3") {
		t.Errorf("Detail missing match count: %q", e.Detail)
	}
	if !strings.Contains(e.Detail, "AWS access key") || !strings.Contains(e.Detail, "GitHub classic PAT") {
		t.Errorf("Detail missing pattern names: %q", e.Detail)
	}
	// Dedup check — "AWS access key" should appear exactly once in the
	// summary even though it matched twice.
	if strings.Count(e.Detail, "AWS access key") != 1 {
		t.Errorf("AWS pattern appears %d times in detail, want 1: %q",
			strings.Count(e.Detail, "AWS access key"), e.Detail)
	}
}

func TestEmitSecretLeakAuditBlock_NilLogIsNoop(t *testing.T) {
	srv := &Server{}
	// Must not panic.
	srv.emitSecretLeakAuditBlock(context.Background(),
		"ai_annotate", "x", "y", "1.0.0",
		[]orchestrator.SecretMatch{{Pattern: "JWT token", Field: "tok: ***"}},
	)
}

func TestSummarizeSecretPatterns_DedupAndCap(t *testing.T) {
	if got := summarizeSecretPatterns(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	matches := []orchestrator.SecretMatch{
		{Pattern: "PEM private key"}, {Pattern: "JWT token"}, {Pattern: "JWT token"},
	}
	out := summarizeSecretPatterns(matches)
	if out != "PEM private key, JWT token" {
		t.Errorf("got %q, want %q", out, "PEM private key, JWT token")
	}
}
