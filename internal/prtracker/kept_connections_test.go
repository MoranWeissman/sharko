package prtracker

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// EP3-DRYRUN-FIXES call 1: the after-merge choices of a remove or un-adopt
// travel on the tracked PR, survive the ConfigMap round trip, and name the
// clusters whose connection the orphan sweep must not delete.

func TestTrackPR_FollowUpSurvivesTheRoundTrip(t *testing.T) {
	tracker, _ := newTestTracker(&mockGitProvider{})
	ctx := context.Background()
	want := &ClusterFollowUp{
		Cleanup:           "git",
		KeepConnection:    true,
		CredentialKey:     "clusters/prod-eu",
		CredentialsSource: "aws-sm",
		CredentialRoleARN: "example-role",
	}
	if err := tracker.TrackPR(ctx, PRInfo{PRID: 7, Cluster: "prod-eu", Operation: OpRemoveCluster, CreatedAt: time.Now(), LastStatus: "open", FollowUp: want}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}
	got, err := tracker.GetPR(ctx, 7)
	if err != nil || got == nil {
		t.Fatalf("GetPR: %v %v", got, err)
	}
	if !reflect.DeepEqual(got.FollowUp, want) {
		t.Fatalf("follow-up after the round trip:\n got %+v\nwant %+v", got.FollowUp, want)
	}
}

func TestKeptConnections_OnlyRemoveAndUnadoptThatKeep(t *testing.T) {
	tracker, _ := newTestTracker(&mockGitProvider{})
	ctx := context.Background()
	now := time.Now()
	prs := []PRInfo{
		{PRID: 1, Cluster: "keep-git", Operation: OpRemoveCluster, FollowUp: &ClusterFollowUp{Cleanup: "git", KeepConnection: true}},
		{PRID: 2, Cluster: "delete-all", Operation: OpRemoveCluster, FollowUp: &ClusterFollowUp{Cleanup: "all"}},
		{PRID: 3, Cluster: "unadopt-me", Operation: OpUnadoptCluster, FollowUp: &ClusterFollowUp{KeepConnection: true}},
		{PRID: 4, Cluster: "register-me", Operation: "register", FollowUp: &ClusterFollowUp{KeepConnection: true}},
		{PRID: 5, Cluster: "old-remove", Operation: OpRemoveCluster},
	}
	for _, pr := range prs {
		pr.CreatedAt, pr.LastStatus = now, "open"
		if err := tracker.TrackPR(ctx, pr); err != nil {
			t.Fatalf("TrackPR %d: %v", pr.PRID, err)
		}
	}
	got, err := tracker.KeptConnections(ctx)
	if err != nil {
		t.Fatalf("KeptConnections: %v", err)
	}
	want := map[string]bool{"keep-git": true, "unadopt-me": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kept connections: got %v want %v", got, want)
	}
}
