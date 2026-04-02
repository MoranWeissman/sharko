package providers

import (
	"strings"
	"testing"
)

func TestNew_EmptyType(t *testing.T) {
	_, err := New(Config{Type: ""})
	if err == nil {
		t.Fatal("expected error for empty type, got nil")
	}
	if !strings.Contains(err.Error(), "SHARKO_PROVIDER_TYPE") {
		t.Errorf("expected error to mention SHARKO_PROVIDER_TYPE, got: %v", err)
	}
}

func TestNew_UnknownType(t *testing.T) {
	_, err := New(Config{Type: "vault"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("expected error to mention unknown provider type, got: %v", err)
	}
}

func TestNew_K8sSecrets(t *testing.T) {
	// This will fail without a valid kubeconfig or in-cluster config,
	// but it verifies the factory routes to the correct constructor.
	_, err := New(Config{Type: "k8s-secrets"})
	if err == nil {
		// If it succeeds (e.g., valid kubeconfig exists), that's fine too.
		return
	}
	// The error should be about K8s config, not about unknown provider type.
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to K8s provider, got: %v", err)
	}
}

func TestNew_KubernetesAlias(t *testing.T) {
	_, err := New(Config{Type: "kubernetes"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to K8s provider for 'kubernetes' alias, got: %v", err)
	}
}

func TestNew_AWSSM(t *testing.T) {
	// This will likely fail without AWS credentials, but verifies factory routing.
	_, err := New(Config{Type: "aws-sm", Region: "us-east-1"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to AWS provider, got: %v", err)
	}
}

func TestNew_AWSSecretsManagerAlias(t *testing.T) {
	_, err := New(Config{Type: "aws-secrets-manager", Region: "us-east-1"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to AWS provider for alias, got: %v", err)
	}
}
