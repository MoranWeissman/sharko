package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/changelog"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// hookCatcher stands in for the PR tracker: it keeps the functions that
// wirePRTrackerHooks hands it, so the test can call them.
type hookCatcher struct {
	onMerge    func(prtracker.PRInfo)
	onComplete func(prtracker.PRInfo, string)
	finish     prtracker.FinishFn
}

func (h *hookCatcher) SetOnMergeFn(fn func(prtracker.PRInfo))            { h.onMerge = fn }
func (h *hookCatcher) SetOnCompleteFn(fn func(prtracker.PRInfo, string)) { h.onComplete = fn }
func (h *hookCatcher) SetFinishFn(fn prtracker.FinishFn)                 { h.finish = fn }

// hostSpy stands in for the API server and records which method each hook
// calls.
type hostSpy struct {
	mu          sync.Mutex
	invalidated int
	handoffOps  []string
	handoffCh   chan struct{}
	store       *changelog.Store
	finishArgs  []string
	finishErr   error
}

func (h *hostSpy) InvalidateReadCache() { h.mu.Lock(); h.invalidated++; h.mu.Unlock() }
func (h *hostSpy) CompleteMigrationHandoffOnMerge(_ context.Context, op string) {
	h.mu.Lock()
	h.handoffOps = append(h.handoffOps, op)
	h.mu.Unlock()
	close(h.handoffCh)
}
func (h *hostSpy) ChangeLogStore() *changelog.Store { return h.store }
func (h *hostSpy) FinishTrackedPR(_ context.Context, pr prtracker.PRInfo, status string) error {
	h.mu.Lock()
	h.finishArgs = append(h.finishArgs, pr.Operation+"/"+status)
	h.mu.Unlock()
	return h.finishErr
}

func newWiredSpy(t *testing.T) (*hookCatcher, *hostSpy) {
	t.Helper()
	catcher := &hookCatcher{}
	spy := &hostSpy{handoffCh: make(chan struct{}), store: changelog.NewStore(10, nil), finishErr: errors.New("finish failed")}
	wirePRTrackerHooks(catcher, spy, nil, nil, nil)
	if catcher.onMerge == nil || catcher.onComplete == nil || catcher.finish == nil {
		t.Fatalf("a hook was not set: merge=%v complete=%v finish=%v", catcher.onMerge != nil, catcher.onComplete != nil, catcher.finish != nil)
	}
	return catcher, spy
}

// The merge hook clears the read cache and starts the migration hand-off
// with the PR's own operation.
func TestWirePRTrackerHooks_MergeHook(t *testing.T) {
	catcher, spy := newWiredSpy(t)
	catcher.onMerge(prtracker.PRInfo{Operation: "migrate-v3-v4"})
	select {
	case <-spy.handoffCh:
	case <-time.After(2 * time.Second):
		t.Fatal("the merge hook never called CompleteMigrationHandoffOnMerge")
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.invalidated != 1 {
		t.Errorf("InvalidateReadCache calls = %d, want 1", spy.invalidated)
	}
	if len(spy.handoffOps) != 1 || spy.handoffOps[0] != "migrate-v3-v4" {
		t.Errorf("hand-off operations = %v, want [migrate-v3-v4]", spy.handoffOps)
	}
	if len(spy.finishArgs) != 0 {
		t.Errorf("the merge hook must not run the finish step; got %v", spy.finishArgs)
	}
}

// The complete hook records the change in the Server's change log, with the
// terminal status it was given.
func TestWirePRTrackerHooks_CompleteHook(t *testing.T) {
	catcher, spy := newWiredSpy(t)
	catcher.onComplete(prtracker.PRInfo{Operation: prtracker.OpRemoveCluster, Cluster: "c1", PRID: 7, PRUrl: "https://x/pull/7"}, "closed")
	got := spy.store.List("c1")
	if len(got) != 1 {
		t.Fatalf("change log has %d entries for c1, want 1", len(got))
	}
	if got[0].Status != "closed" || got[0].PRID != 7 || got[0].Cluster != "c1" {
		t.Errorf("unexpected change log entry: %+v", got[0])
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.invalidated != 0 || len(spy.finishArgs) != 0 {
		t.Errorf("the complete hook must only write the change log; invalidated=%d finish=%v", spy.invalidated, spy.finishArgs)
	}
}

// The finish hook is the Server's FinishTrackedPR: same PR, same status,
// and its error comes back so the tracker tries again.
func TestWirePRTrackerHooks_FinishHook(t *testing.T) {
	catcher, spy := newWiredSpy(t)
	err := catcher.finish(context.Background(), prtracker.PRInfo{Operation: prtracker.OpAdoptCluster}, "merged")
	if err == nil || err.Error() != "finish failed" {
		t.Fatalf("finish hook returned %v, want the Server's error", err)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.finishArgs) != 1 || spy.finishArgs[0] != prtracker.OpAdoptCluster+"/merged" {
		t.Errorf("FinishTrackedPR calls = %v", spy.finishArgs)
	}
	if spy.invalidated != 0 {
		t.Errorf("the finish hook must not touch the read cache")
	}
}

// The cluster reconciler's waiting-marks pass is wired to the Server's
// FinishWaitingClusterMarks. That field sits inside the serve closure, so
// this reads serve.go (same approach as TestServeWiresSelfHealFn).
func TestServeWiresWaitingMarksFn(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "serve.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing serve.go: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "WaitingMarksFn" {
			return true
		}
		sel, ok := kv.Value.(*ast.SelectorExpr)
		if !ok {
			t.Errorf("WaitingMarksFn is not a plain method value")
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "srv" && sel.Sel.Name == "FinishWaitingClusterMarks" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("WaitingMarksFn is not set to srv.FinishWaitingClusterMarks in serve.go")
	}
}

// And the tracker hooks must come from wirePRTrackerHooks, called with the
// real Server.
func TestServeCallsWirePRTrackerHooks(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "serve.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing serve.go: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "wirePRTrackerHooks" && len(c.Args) == 5 {
			a0, _ := c.Args[0].(*ast.Ident)
			a1, _ := c.Args[1].(*ast.Ident)
			found = a0 != nil && a0.Name == "prTracker" && a1 != nil && a1.Name == "srv"
		}
		return true
	})
	if !found {
		t.Fatal("serve.go does not call wirePRTrackerHooks(prTracker, srv, ...)")
	}
}
