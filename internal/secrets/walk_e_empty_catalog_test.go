package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

type notFoundReader struct{}

func (notFoundReader) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	return nil, fmt.Errorf("get file content: path %q: %w", path, gitprovider.ErrFileNotFound)
}

type brokenReader struct{}

func (brokenReader) GetFileContent(_ context.Context, _, _ string) ([]byte, error) {
	return nil, errors.New("the Git host did not answer")
}

// Walk 1 L17 / walk 8 L07: a repo with no addon catalog yet is not a failure.
// It used to be recorded as an error every five minutes until the first addon
// was added.
func TestPlanPushes_NoCatalogYet_IsNotAnError(t *testing.T) {
	r := &Reconciler{baseBranch: "main"}
	plan, err := r.planPushes(context.Background(), notFoundReader{})
	if err != nil {
		t.Fatalf("a repo with no catalog yet must not be an error: %v", err)
	}
	if len(plan.work) != 0 {
		t.Fatalf("nothing should be pushed: %+v", plan.work)
	}
}

// A catalog that cannot be READ is still a failure.
func TestPlanPushes_CatalogUnreadable_IsStillAnError(t *testing.T) {
	r := &Reconciler{baseBranch: "main"}
	if _, err := r.planPushes(context.Background(), brokenReader{}); err == nil {
		t.Fatal("a Git host that does not answer must still be reported")
	}
}
