package observations

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/verify"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRecordAndGetObservation(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	// Record a successful stage1 test.
	result := verify.Result{
		Success:    true,
		Stage:      "stage1",
		DurationMs: 123,
	}
	if err := store.RecordTestResult(ctx, "prod-eu", result); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	// Read it back.
	obs, err := store.GetObservation(ctx, "prod-eu")
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.LastTestOutcome != "success" {
		t.Errorf("LastTestOutcome = %q, want %q", obs.LastTestOutcome, "success")
	}
	if obs.LastTestStage != "stage1" {
		t.Errorf("LastTestStage = %q, want %q", obs.LastTestStage, "stage1")
	}
	if obs.LastTestDurationMs != 123 {
		t.Errorf("LastTestDurationMs = %d, want %d", obs.LastTestDurationMs, 123)
	}
}

func TestRecordFailedResult(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	result := verify.Result{
		Success:      false,
		Stage:        "stage1",
		ErrorCode:    verify.ERR_NETWORK,
		ErrorMessage: "connection refused",
		DurationMs:   50,
	}
	if err := store.RecordTestResult(ctx, "staging", result); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	obs, err := store.GetObservation(ctx, "staging")
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.LastTestOutcome != "failure" {
		t.Errorf("LastTestOutcome = %q, want %q", obs.LastTestOutcome, "failure")
	}
	if obs.LastTestErrorCode != "ERR_NETWORK" {
		t.Errorf("LastTestErrorCode = %q, want %q", obs.LastTestErrorCode, "ERR_NETWORK")
	}
	if obs.LastTestErrorMessage != "connection refused" {
		t.Errorf("LastTestErrorMessage = %q, want %q", obs.LastTestErrorMessage, "connection refused")
	}
}

func TestGetObservation_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	obs, err := store.GetObservation(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if obs != nil {
		t.Errorf("expected nil observation, got %+v", obs)
	}
}

func TestListObservations(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	// Record observations for two clusters.
	if err := store.RecordTestResult(ctx, "prod-eu", verify.Result{Success: true, Stage: "stage1", DurationMs: 100}); err != nil {
		t.Fatalf("RecordTestResult prod-eu: %v", err)
	}
	if err := store.RecordTestResult(ctx, "prod-us", verify.Result{Success: false, Stage: "stage2", ErrorCode: verify.ERR_AUTH, DurationMs: 200}); err != nil {
		t.Fatalf("RecordTestResult prod-us: %v", err)
	}

	all, err := store.ListObservations(ctx)
	if err != nil {
		t.Fatalf("ListObservations: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(all))
	}
	if all["prod-eu"] == nil {
		t.Error("missing observation for prod-eu")
	}
	if all["prod-us"] == nil {
		t.Error("missing observation for prod-us")
	}
	if all["prod-us"].LastTestErrorCode != "ERR_AUTH" {
		t.Errorf("prod-us ErrorCode = %q, want %q", all["prod-us"].LastTestErrorCode, "ERR_AUTH")
	}
}

func TestListObservations_Empty(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	all, err := store.ListObservations(ctx)
	if err != nil {
		t.Fatalf("ListObservations: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 observations, got %d", len(all))
	}
}

func TestRecordOverwritesPrevious(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "default")
	ctx := context.Background()

	// Record success, then failure for same cluster.
	if err := store.RecordTestResult(ctx, "prod-eu", verify.Result{Success: true, Stage: "stage1"}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}
	if err := store.RecordTestResult(ctx, "prod-eu", verify.Result{Success: false, Stage: "stage1", ErrorCode: verify.ERR_TLS}); err != nil {
		t.Fatalf("RecordTestResult: %v", err)
	}

	obs, err := store.GetObservation(ctx, "prod-eu")
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if obs.LastTestOutcome != "failure" {
		t.Errorf("LastTestOutcome = %q, want %q (should be overwritten)", obs.LastTestOutcome, "failure")
	}
}
