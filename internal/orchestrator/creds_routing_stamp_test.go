package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-60.4 — registration stamps the effective creds source onto the
// managed-clusters.yaml record so later credential fetches (Test / Diagnose
// / secrets / addon ops) can route per cluster: inline-registered clusters
// read via the ArgoCD provider under ANY backend connection; backend
// sources keep the backend route. Records written before the field existed
// carry no credsSource (the router treats them as unknown and heals via
// the backend-first-then-ArgoCD-read fallback).

func TestRegisterCluster_Inline_StampsCredsSourceOnGitRecord(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-inline",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q (%s)", result.Status, result.Error)
	}

	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "credsSource: inline-kubeconfig") {
		t.Fatalf("managed-clusters.yaml must record credsSource: inline-kubeconfig, got:\n%s", mc)
	}
}

func TestRegisterCluster_Backend_StampsCredsSourceOnGitRecord(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://prod-eu.example.com"},
	}}
	orch := New(nil, creds, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:        "prod-eu",
		CredsSource: CredsSourceEKSToken,
		Addons:      map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q (%s)", result.Status, result.Error)
	}

	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "credsSource: eks-token") {
		t.Fatalf("managed-clusters.yaml must record credsSource: eks-token, got:\n%s", mc)
	}
}
