package verify

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestStage1_Success(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success, got error: %s (%s)", result.ErrorMessage, result.ErrorCode)
	}
	if result.Stage != "stage1" {
		t.Errorf("expected stage 'stage1', got %q", result.Stage)
	}
	if result.DurationMs < 0 {
		t.Errorf("expected non-negative duration, got %d", result.DurationMs)
	}
}

func TestStage1_NamespaceCreated(t *testing.T) {
	// No pre-existing namespace — Stage1 should create it.
	client := fake.NewSimpleClientset()

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success after namespace creation, got error: %s (%s)", result.ErrorMessage, result.ErrorCode)
	}

	// Verify the namespace was actually created.
	_, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace should exist after Stage1: %v", err)
	}
}

func TestStage1_RBACError(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	// Make secret creation return a Forbidden error.
	client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is Forbidden: user cannot create secrets in namespace sharko-test")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure for RBAC error")
	}
	if result.ErrorCode != ERR_RBAC {
		t.Errorf("expected ERR_RBAC, got %s", result.ErrorCode)
	}
}

func TestStage1_AuthError(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Unauthorized: token expired")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure for auth error")
	}
	if result.ErrorCode != ERR_AUTH {
		t.Errorf("expected ERR_AUTH, got %s", result.ErrorCode)
	}
}

func TestStage1_NetworkError(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("dial tcp 10.0.0.1:443: connection refused")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure for network error")
	}
	if result.ErrorCode != ERR_NETWORK {
		t.Errorf("expected ERR_NETWORK, got %s", result.ErrorCode)
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCode
	}{
		{"connection refused", "dial tcp 10.0.0.1:443: connection refused", ERR_NETWORK},
		{"no such host", "dial tcp: lookup cluster.example.com: no such host", ERR_NETWORK},
		{"x509 cert", "x509: certificate signed by unknown authority", ERR_TLS},
		{"certificate keyword", "tls: bad certificate", ERR_TLS},
		{"unauthorized", "Unauthorized", ERR_AUTH},
		{"token expired", "token expired", ERR_AUTH},
		{"forbidden", "Forbidden: user cannot list pods", ERR_RBAC},
		{"forbidden lowercase", "forbidden: access denied", ERR_RBAC},
		{"sts get token", "failed to GetToken for cluster", ERR_AWS_STS},
		{"no identity provider", "no identity provider found", ERR_AWS_STS},
		{"assume role", "AssumeRole failed", ERR_AWS_ASSUME},
		{"not authorized to assume", "not authorized to assume role", ERR_AWS_ASSUME},
		{"throttling", "throttling: rate exceeded", ERR_QUOTA},
		{"too many requests", "Too many requests", ERR_QUOTA},
		{"admission webhook", "admission webhook denied the request", ERR_NAMESPACE},
		{"namespace keyword", "namespace sharko-test not found", ERR_NAMESPACE},
		{"timeout", "context deadline exceeded: timeout", ERR_TIMEOUT},
		{"deadline exceeded", "deadline exceeded", ERR_TIMEOUT},
		{"unknown error", "something completely unexpected", ERR_UNKNOWN},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(errors.New(tc.errMsg))
			if got != tc.expected {
				t.Errorf("ClassifyError(%q) = %s, want %s", tc.errMsg, got, tc.expected)
			}
		})
	}
}
