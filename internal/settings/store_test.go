package settings

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestGetProbeMode_DefaultsToCheckApp(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode: %v", err)
	}
	if mode != ProbeModeCheckApp {
		t.Errorf("GetProbeMode = %q, want default %q", mode, ProbeModeCheckApp)
	}
}

func TestSetProbeMode_RoundTrip(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if err := store.SetProbeMode(ctx, ProbeModeAPITest); err != nil {
		t.Fatalf("SetProbeMode: %v", err)
	}

	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode: %v", err)
	}
	if mode != ProbeModeAPITest {
		t.Errorf("GetProbeMode = %q, want %q", mode, ProbeModeAPITest)
	}

	// Flip back — the ConfigMap is updated, not recreated.
	if err := store.SetProbeMode(ctx, ProbeModeCheckApp); err != nil {
		t.Fatalf("SetProbeMode (revert): %v", err)
	}
	mode, err = store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode (after revert): %v", err)
	}
	if mode != ProbeModeCheckApp {
		t.Errorf("GetProbeMode (after revert) = %q, want %q", mode, ProbeModeCheckApp)
	}
}

func TestSetProbeMode_RejectsUnrecognizedValue(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	err := store.SetProbeMode(ctx, "bogus-mode")
	if err == nil {
		t.Fatal("expected an error for an unrecognized probe_mode value, got nil")
	}
	if _, ok := err.(*InvalidProbeModeError); !ok {
		t.Errorf("expected *InvalidProbeModeError, got %T: %v", err, err)
	}

	// The invalid write must not have persisted anything — the value stays default.
	mode, getErr := store.GetProbeMode(ctx)
	if getErr != nil {
		t.Fatalf("GetProbeMode: %v", getErr)
	}
	if mode != ProbeModeCheckApp {
		t.Errorf("GetProbeMode after rejected write = %q, want default %q", mode, ProbeModeCheckApp)
	}
}

func TestIsAPITest(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if store.IsAPITest(ctx) {
		t.Error("IsAPITest should be false by default (check-app)")
	}

	if err := store.SetProbeMode(ctx, ProbeModeAPITest); err != nil {
		t.Fatalf("SetProbeMode: %v", err)
	}
	if !store.IsAPITest(ctx) {
		t.Error("IsAPITest should be true after SetProbeMode(api-test)")
	}
}

func TestIsAPITest_NilStoreIsSafe(t *testing.T) {
	var store *Store
	if store.IsAPITest(context.Background()) {
		t.Error("IsAPITest on a nil *Store must default to false (check-app), never panic or report true")
	}
}
