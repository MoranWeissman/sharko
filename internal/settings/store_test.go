package settings

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// failGetConfigMaps installs a reactor on client that fails every "get
// configmaps" call with simulatedErr — used to exercise the read-error /
// cache-fallback paths in IsInlineCredentialsAllowed and IsAPITest
// (V2-cleanup-90.3 / review finding M4).
func failGetConfigMaps(client *fake.Clientset, simulatedErr error) {
	client.PrependReactor("get", "configmaps", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, simulatedErr
	})
}

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

// V2-cleanup-89.6 — allow_inline_credentials kill switch.

func TestGetAllowInlineCredentials_DefaultsToTrue(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	allow, err := store.GetAllowInlineCredentials(ctx)
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials: %v", err)
	}
	if !allow {
		t.Error("GetAllowInlineCredentials = false, want default true (today's behavior unchanged)")
	}
}

func TestSetAllowInlineCredentials_RoundTrip(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if err := store.SetAllowInlineCredentials(ctx, false); err != nil {
		t.Fatalf("SetAllowInlineCredentials(false): %v", err)
	}
	allow, err := store.GetAllowInlineCredentials(ctx)
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials: %v", err)
	}
	if allow {
		t.Error("GetAllowInlineCredentials = true after SetAllowInlineCredentials(false), want false")
	}

	// Flip back — the ConfigMap is updated, not recreated.
	if err := store.SetAllowInlineCredentials(ctx, true); err != nil {
		t.Fatalf("SetAllowInlineCredentials(true): %v", err)
	}
	allow, err = store.GetAllowInlineCredentials(ctx)
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials (after revert): %v", err)
	}
	if !allow {
		t.Error("GetAllowInlineCredentials (after revert) = false, want true")
	}
}

func TestIsInlineCredentialsAllowed(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if !store.IsInlineCredentialsAllowed(ctx) {
		t.Error("IsInlineCredentialsAllowed should be true by default")
	}

	if err := store.SetAllowInlineCredentials(ctx, false); err != nil {
		t.Fatalf("SetAllowInlineCredentials: %v", err)
	}
	if store.IsInlineCredentialsAllowed(ctx) {
		t.Error("IsInlineCredentialsAllowed should be false after SetAllowInlineCredentials(false)")
	}
}

func TestIsInlineCredentialsAllowed_NilStoreIsSafe(t *testing.T) {
	var store *Store
	if !store.IsInlineCredentialsAllowed(context.Background()) {
		t.Error("IsInlineCredentialsAllowed on a nil *Store must default to true, never panic or report false")
	}
}

// V2-cleanup-90.3 / review finding M4 — the kill switch must not silently
// fail open on a read error. These three tests pin the cache-fallback
// contract end to end.

func TestIsInlineCredentialsAllowed_ErrorAfterFalseWasRead_StaysFalse(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	// An admin turns the switch off, and it is successfully read back once
	// — this seeds the cache with false.
	if err := store.SetAllowInlineCredentials(ctx, false); err != nil {
		t.Fatalf("SetAllowInlineCredentials: %v", err)
	}
	if store.IsInlineCredentialsAllowed(ctx) {
		t.Fatal("expected IsInlineCredentialsAllowed to be false before injecting any read error")
	}

	// Now every subsequent ConfigMap read fails. The kill switch must keep
	// reporting false (serve from cache), never fail open to the static
	// default (true).
	failGetConfigMaps(client, errors.New("simulated API server outage"))

	for i := 0; i < 3; i++ {
		if store.IsInlineCredentialsAllowed(ctx) {
			t.Fatalf("iteration %d: IsInlineCredentialsAllowed must stay false (cached) on a read error after false was successfully read, got true", i)
		}
	}
}

func TestIsInlineCredentialsAllowed_ErrorBeforeAnyRead_DefaultsTrue(t *testing.T) {
	client := fake.NewSimpleClientset()
	failGetConfigMaps(client, errors.New("simulated API server outage"))
	store := NewStore(client, "sharko")
	ctx := context.Background()

	// No successful read has ever happened on this Store — the cache is
	// empty, so the static default (true, allowed) applies, exactly like
	// today's behavior.
	if !store.IsInlineCredentialsAllowed(ctx) {
		t.Error("expected the static default (true) when no successful read has ever happened, got false")
	}
}

func TestIsInlineCredentialsAllowed_RecoversAfterError(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if err := store.SetAllowInlineCredentials(ctx, false); err != nil {
		t.Fatalf("SetAllowInlineCredentials: %v", err)
	}

	var readErr error
	client.PrependReactor("get", "configmaps", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if readErr != nil {
			return true, nil, readErr
		}
		return false, nil, nil // fall through to the default reactor chain
	})

	readErr = errors.New("simulated API server outage")
	if store.IsInlineCredentialsAllowed(ctx) {
		t.Fatal("expected cached false to be served while reads are failing")
	}

	// The outage clears. An admin (or the next successful read) flips the
	// live value back to true — the wrapper must resume reading live,
	// not keep serving the stale cached false forever.
	readErr = nil
	if err := store.SetAllowInlineCredentials(ctx, true); err != nil {
		t.Fatalf("SetAllowInlineCredentials(true): %v", err)
	}
	if !store.IsInlineCredentialsAllowed(ctx) {
		t.Fatal("expected IsInlineCredentialsAllowed to resume live reads and report true once the outage clears")
	}

	// And a fresh outage after the recovery must now cache-fallback to the
	// newly recovered value (true), not the old stale false.
	readErr = errors.New("simulated API server outage, take two")
	if !store.IsInlineCredentialsAllowed(ctx) {
		t.Fatal("expected the cache to reflect the post-recovery value (true) during a second outage")
	}
}

// probe_mode gets the same cache-on-error treatment as
// allow_inline_credentials (V2-cleanup-90.3 M4 — "3-line symmetry" call).

func TestIsAPITest_ErrorAfterAPITestWasRead_StaysTrue(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if err := store.SetProbeMode(ctx, ProbeModeAPITest); err != nil {
		t.Fatalf("SetProbeMode: %v", err)
	}
	if !store.IsAPITest(ctx) {
		t.Fatal("expected IsAPITest to be true before injecting any read error")
	}

	failGetConfigMaps(client, errors.New("simulated API server outage"))

	if !store.IsAPITest(ctx) {
		t.Error("expected IsAPITest to stay true (cached) on a read error after api-test was successfully read, got false")
	}
}

func TestIsAPITest_ErrorBeforeAnyRead_DefaultsFalse(t *testing.T) {
	client := fake.NewSimpleClientset()
	failGetConfigMaps(client, errors.New("simulated API server outage"))
	store := NewStore(client, "sharko")
	ctx := context.Background()

	if store.IsAPITest(ctx) {
		t.Error("expected the static default (false / check-app) when no successful read has ever happened, got true")
	}
}
