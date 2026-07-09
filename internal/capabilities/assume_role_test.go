package capabilities

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestAssumeRoleChecker(fn assumeRoleFn) *AssumeRoleChecker {
	return &AssumeRoleChecker{assumeRoleFn: fn, timeout: time.Second}
}

// TestAssumeRoleChecker_Success asserts a successful attempt returns nil and
// forwards the exact roleARN/region the caller supplied.
func TestAssumeRoleChecker_Success(t *testing.T) {
	var gotRole, gotRegion string
	checker := newTestAssumeRoleChecker(func(_ context.Context, roleARN, region string) error {
		gotRole, gotRegion = roleARN, region
		return nil
	})

	err := checker.Check(context.Background(), "arn:aws:iam::123456789012:role/example", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRole != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("roleARN forwarded = %q, want the input role", gotRole)
	}
	if gotRegion != "us-east-1" {
		t.Errorf("region forwarded = %q, want %q", gotRegion, "us-east-1")
	}
}

// TestAssumeRoleChecker_Failure asserts a failed attempt surfaces the
// underlying error unchanged — the doctor's fix message is built from it.
func TestAssumeRoleChecker_Failure(t *testing.T) {
	wantErr := errors.New("AccessDenied: not authorized to perform sts:AssumeRole")
	checker := newTestAssumeRoleChecker(func(_ context.Context, _, _ string) error {
		return wantErr
	})

	err := checker.Check(context.Background(), "arn:aws:iam::123456789012:role/example", "")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

// TestAssumeRoleChecker_TimeoutBound asserts a hung AssumeRole call is
// bounded by the checker's timeout, not left to hang the caller forever —
// mirrors the AWSDetector timeout-bound contract for the identity check.
func TestAssumeRoleChecker_TimeoutBound(t *testing.T) {
	checker := &AssumeRoleChecker{
		timeout: 20 * time.Millisecond,
		assumeRoleFn: func(ctx context.Context, _, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	start := time.Now()
	err := checker.Check(context.Background(), "arn:aws:iam::123456789012:role/example", "us-east-1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed > time.Second {
		t.Errorf("Check took %v, want it bounded by the checker's short timeout", elapsed)
	}
}

// TestAssumeRoleChecker_DefaultTimeoutFallback asserts a zero-value timeout
// field falls back to defaultAssumeRoleTimeout instead of firing
// immediately (context.WithTimeout with 0 would cancel right away).
func TestAssumeRoleChecker_DefaultTimeoutFallback(t *testing.T) {
	called := false
	checker := &AssumeRoleChecker{
		assumeRoleFn: func(_ context.Context, _, _ string) error {
			called = true
			return nil
		},
	}

	if err := checker.Check(context.Background(), "arn:aws:iam::123456789012:role/example", "us-east-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("assumeRoleFn was never invoked — zero timeout must fall back to a real bound, not cancel instantly")
	}
}

// TestNewAssumeRoleChecker_WiresDefaults asserts the production constructor
// wires a non-nil assumeRoleFn and a positive timeout.
func TestNewAssumeRoleChecker_WiresDefaults(t *testing.T) {
	checker := NewAssumeRoleChecker()
	if checker.assumeRoleFn == nil {
		t.Fatal("assumeRoleFn is nil, want defaultAssumeRole wired")
	}
	if checker.timeout != defaultAssumeRoleTimeout {
		t.Errorf("timeout = %v, want %v", checker.timeout, defaultAssumeRoleTimeout)
	}
}
