package prtracker

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
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

// EP3 dry run round S, S1 and S3: only a MERGED removal that keeps the
// connection hands it back; an open one only holds it (it may still be
// closed). Every unfinished remove or un-adopt, cleanup=all too, holds the
// connection so the sweep leaves the finish its credentials. Closed pull
// requests hold nothing.
func TestKeptConnections_OnlyRemoveAndUnadoptThatKeep(t *testing.T) {
	tracker, _ := newTestTracker(&mockGitProvider{})
	ctx := context.Background()
	now := time.Now()
	prs := []PRInfo{
		{PRID: 1, Cluster: "keep-git-open", Operation: OpRemoveCluster, LastStatus: "open", FollowUp: &ClusterFollowUp{Cleanup: "git", KeepConnection: true}},
		{PRID: 2, Cluster: "delete-all-merged", Operation: OpRemoveCluster, LastStatus: "merged", FollowUp: &ClusterFollowUp{Cleanup: "all"}},
		{PRID: 3, Cluster: "unadopt-merged", Operation: OpUnadoptCluster, LastStatus: "merged", FollowUp: &ClusterFollowUp{KeepConnection: true}},
		{PRID: 4, Cluster: "register-me", Operation: "register", LastStatus: "open", FollowUp: &ClusterFollowUp{KeepConnection: true}},
		{PRID: 5, Cluster: "keep-git-merged", Operation: OpRemoveCluster, LastStatus: "merged", FollowUp: &ClusterFollowUp{Cleanup: "git", KeepConnection: true}},
		{PRID: 6, Cluster: "keep-closed", Operation: OpRemoveCluster, LastStatus: "closed", FollowUp: &ClusterFollowUp{Cleanup: "git", KeepConnection: true}},
	}
	for _, pr := range prs {
		pr.CreatedAt = now
		if err := tracker.TrackPR(ctx, pr); err != nil {
			t.Fatalf("TrackPR %d: %v", pr.PRID, err)
		}
	}
	got, err := tracker.KeptConnections(ctx)
	if err != nil {
		t.Fatalf("KeptConnections: %v", err)
	}
	want := map[string]models.ConnectionHold{
		"keep-git-open":     {Hold: true},
		"delete-all-merged": {Hold: true},
		"unadopt-merged":    {Hold: true, ReleasePR: 3},
		"keep-git-merged":   {Hold: true, ReleasePR: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kept connections:\n got %v\nwant %v", got, want)
	}
}
