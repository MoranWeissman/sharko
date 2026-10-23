package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// EP3-DRYRUN-FIXES call 9: un-adopt changes the ArgoCD cluster Secret only
// after its pull request merges (same rule as call 1).

func TestUnadoptCluster_PRWaits_DoesNotTouchTheSecret(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: cluster-a\n    secretPath: clusters/a-kubeconfig\n    labels: {}\n")
	asm := newMockArgoSecretManager()
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}
	tracker := &stubPRTracker{}

	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetPRTracker(tracker)

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unadopt: %v", err)
	}
	if len(asm.unadopted) != 0 {
		t.Fatalf("the Secret was changed before the PR merged: %v", asm.unadopted)
	}
	if result.Status != "success" || result.Git == nil || result.Git.Merged {
		t.Fatalf("want success with an open PR, got %q %+v", result.Status, result.Git)
	}
	want := "Pull request opened, waiting for merge: " + result.Git.PRUrl +
		". Nothing has changed on cluster cluster-a yet — Sharko takes its marks off the ArgoCD cluster Secret after the pull request is merged."
	if result.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", result.Message, want)
	}
	calls := tracker.snapshot()
	if len(calls) != 1 || calls[0].Operation != "unadopt-cluster" || calls[0].FollowUp == nil ||
		!calls[0].FollowUp.KeepConnection || calls[0].FollowUp.CredentialKey != "clusters/a-kubeconfig" {
		t.Fatalf("the tracked PR must carry the un-adopt follow-up with the stored secretPath, got %+v", calls)
	}

	// After the merge, the hook finishes the job.
	if err := orch.FinishUnadoptAfterMerge(context.Background(), "cluster-a", *calls[0].FollowUp); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if len(asm.unadopted) != 1 || asm.unadopted[0] != "cluster-a" {
		t.Fatalf("after the merge the Secret must be un-adopted, got %v", asm.unadopted)
	}
}

func TestUnadoptCluster_GitFails_SaysNothingChanged(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")
	git.prErr = errors.New("the git host said no")
	asm := newMockArgoSecretManager()
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unadopt: %v", err)
	}
	if len(asm.unadopted) != 0 {
		t.Fatalf("a failed PR must change nothing on the Secret, got %v", asm.unadopted)
	}
	if result.Message != "Git commit failed during unadoption — nothing was changed on the cluster's ArgoCD connection" {
		t.Fatalf("message %q", result.Message)
	}
	// The old wording claimed the labels were already gone. Now untrue.
	if strings.Contains(result.Message, "have already been removed") {
		t.Fatalf("old wording is back: %q", result.Message)
	}
}
