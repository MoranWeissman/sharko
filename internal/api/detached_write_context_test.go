package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

type detachKey struct{}

// Walk 9, finding 1: closing the tab while the page said "Adopting..." cut the
// steps of an adopt in two. The context an adopt, un-adopt or take over runs on
// must survive the request being cancelled, keep what the request carries, and
// still end by itself.
func TestDetachedWriteContext_SurvivesCancel_KeepsValues_HasDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), detachKey{}, "kept"))
	ctx, done := detachedWriteContext(parent)
	defer done()

	cancel() // the person closes the tab
	if err := ctx.Err(); err != nil {
		t.Fatalf("the work was cut off with the request: %v", err)
	}
	if ctx.Value(detachKey{}) != "kept" {
		t.Fatal("what the request carries (user, audit slot, logger) was lost")
	}
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > joinWriteTimeout || time.Until(dl) < joinWriteTimeout-10*time.Second {
		t.Fatalf("deadline = %v (ok=%v), want about %v from now so a stuck Git host cannot hold the work for ever", dl, ok, joinWriteTimeout)
	}
}

// The handlers must actually use it. A handler that goes back to r.Context()
// brings the half-done adopt back, and nothing else would notice until someone
// closed a tab again.
func TestJoinHandlers_RunOnTheDetachedContext(t *testing.T) {
	cases := []struct {
		file    string
		wantUse int
		mustNot []string
	}{
		{"clusters_adopt.go", 2, []string{"orch.AdoptClusters(r.Context()", "orch.UnadoptCluster(r.Context()"}},
		{"clusters_takeover.go", 1, []string{"s.GitProviderForTier(r.Context(), r, audit.Tier1)"}},
	}
	for _, c := range cases {
		src, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(src), "detachedWriteContext(r.Context())"); got != c.wantUse {
			t.Errorf("%s: detachedWriteContext used %d times, want %d", c.file, got, c.wantUse)
		}
		for _, bad := range c.mustNot {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s: %q puts the work back on the request context", c.file, bad)
			}
		}
	}
}
