package cmstore

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	client := fake.NewSimpleClientset()
	return NewStore(client, "test-ns", "test-state")
}

func TestReadModifyWrite_CreatesOnFirstWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["foo"] = "bar"
		return nil
	})
	if err != nil {
		t.Fatalf("ReadModifyWrite: %v", err)
	}

	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("expected foo=bar, got %v", got["foo"])
	}
}

func TestReadModifyWrite_UpdatesExisting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// First write
	err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["counter"] = float64(1)
		return nil
	})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Second write (update)
	err = s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["counter"] = float64(2)
		data["new_key"] = "hello"
		return nil
	})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["counter"] != float64(2) {
		t.Errorf("expected counter=2, got %v", got["counter"])
	}
	if got["new_key"] != "hello" {
		t.Errorf("expected new_key=hello, got %v", got["new_key"])
	}
}

func TestReadModifyWrite_ConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Initialize counter
	err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["counter"] = float64(0)
		return nil
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// 10 goroutines each increment counter by 1
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
				cur, _ := data["counter"].(float64)
				data["counter"] = cur + 1
				return nil
			}); err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["counter"] != float64(10) {
		t.Errorf("expected counter=10, got %v", got["counter"])
	}
}

func TestReadModifyWrite_VersionField(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["key"] = "value"
		return nil
	})
	if err != nil {
		t.Fatalf("ReadModifyWrite: %v", err)
	}

	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	v, ok := got["version"]
	if !ok {
		t.Fatal("version field missing")
	}
	if v != float64(1) {
		t.Errorf("expected version=1, got %v", v)
	}
}

func TestRead_EmptyStore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestSizeWarning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Capture slog output
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	origLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(origLogger)

	// Write data larger than 800KB
	bigValue := strings.Repeat("x", 850*1024)

	err := s.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data["big"] = bigValue
		return nil
	})
	if err != nil {
		t.Fatalf("ReadModifyWrite: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "configmap approaching size limit") {
		t.Errorf("expected size warning in log, got: %s", logOutput)
	}

	// Verify data was still written correctly
	got, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	raw, _ := json.Marshal(got)
	if len(raw) <= sizeWarningBytes {
		t.Errorf("expected data > 800KB, got %d bytes", len(raw))
	}
}
