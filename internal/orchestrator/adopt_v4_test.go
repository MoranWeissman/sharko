package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// adoptV4Git returns a mock repo that already looks like a v4 repo (the
// engine pin), with no cluster registered yet — the ordinary starting point
// for adopting a cluster ArgoCD already knows about.
func adoptV4Git() *mockGitProvider {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	return git
}

func TestAdoptClusters_V4_HappyPath_WritesBothFilesAndSwapsOwnership(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}

	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		// Found + unclaimed: the ordinary "nobody has adopted this yet" case.
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com"},
	}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}
	cr := result.Results[0]
	if cr.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", cr.Status, cr.Error)
	}
	// An unclaimed connection is no longer a silent pass (task #150): the
	// preflight warns that nobody claims it and asks the human to confirm
	// nothing else manages it. The request acknowledges that warning by
	// its id, so the write goes ahead and the warning rides along.
	if len(cr.Warnings) != 1 || !strings.Contains(cr.Warnings[0].Message, "Nobody has claimed") {
		t.Errorf("expected exactly the confirm-nobody-manages warning to ride along, got %v", cr.Warnings)
	}

	// The two v4 files, same pair a takeover writes.
	connBytes, ok := git.files[V4ManagedClustersPath]
	if !ok {
		t.Fatalf("%s was not written; wrote: %v", V4ManagedClustersPath, keysOf(git.files))
	}
	spec, err := models.LoadManagedClusters(connBytes)
	if err != nil {
		t.Fatalf("fleet record does not parse: %v", err)
	}
	found := false
	for _, c := range spec.Clusters {
		if c.Name == "prod-eu" {
			found = true
			if len(c.Labels) != 0 {
				t.Errorf("adopt must not author addon labels on the v4 connection record, got %v", c.Labels)
			}
		}
	}
	if !found {
		t.Fatal("expected a prod-eu entry in managed-clusters.yaml")
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; !ok {
		t.Error("expected cluster-addons/prod-eu.yaml to be written")
	}
	// v3 registry must stay untouched.
	if _, ok := git.files["configuration/managed-clusters.yaml"]; ok {
		t.Error("v3 configuration/managed-clusters.yaml must not be written on a v4 repo")
	}

	// Auto-merge is on, so the ownership swap and the adopted annotation
	// both ran.
	if len(asm.takenOver) != 1 || asm.takenOver[0] != "prod-eu" {
		t.Errorf("expected TakeOverClusterSecret to be called for prod-eu, got %v", asm.takenOver)
	}
	if asm.annotations["prod-eu"][AnnotationAdopted] != "true" {
		t.Error("expected the adopted annotation to be set after merge")
	}
}

// TestAdoptClusters_V4_NotReady_FailsWithPreflightSummary pins the batch
// contract: a cluster the preflight blocks fails with the preflight's
// summary as its error, and does not touch git at all.
func TestAdoptClusters_V4_NotReady_FailsWithPreflightSummary(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}

	asm := newMockArgoSecretManager()
	// No secret detail seeded — GetClusterSecretDetail's default answer is
	// Found:false, which blocks the preflight ("there is nothing to take
	// over").

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" {
		t.Fatalf("expected failed, got %s", cr.Status)
	}
	if !strings.Contains(cr.Error, "not ready to take over yet") {
		t.Errorf("expected the preflight summary as the error, got: %q", cr.Error)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("a blocked adoption must not write anything")
	}
	if len(asm.takenOver) != 0 {
		t.Error("a blocked adoption must not swap ArgoCD ownership")
	}
}

// TestAdoptClusters_V4_WarningSurfacesButProceeds pins the other half of
// the contract: a cluster the preflight only WARNS about (not blocks)
// still adopts, and the warning rides along on the result. A rival
// ownership marker no longer qualifies (it blocks — see the test below);
// the acknowledgeable states are the soft ownership signal and the
// no-marker state.
func TestAdoptClusters_V4_WarningSurfacesButProceeds(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}

	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		// A soft ownership signal — a marker that MAY belong to an ArgoCD
		// application or a plain Helm release named "flux". Not proof, so
		// it warns rather than blocks.
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com",
			ForeignOwnerFound: true, ForeignOwnerConfidence: "soft", ForeignOwnerAppName: "flux"},
	}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "success" {
		t.Fatalf("expected success despite the warning, got %s (error: %s)", cr.Status, cr.Error)
	}
	if len(cr.Warnings) == 0 {
		t.Fatal("expected the foreign-owner warning to surface on the result")
	}
	msgs := []string{}
	for _, w := range cr.Warnings {
		msgs = append(msgs, w.Message)
	}
	joined := strings.Join(msgs, " | ")
	if !strings.Contains(joined, "flux") {
		t.Errorf("expected the warning to name the foreign owner, got: %q", joined)
	}
	// The warning was acknowledged by id, so the write went ahead.
	if _, ok := git.files[V4ManagedClustersPath]; !ok {
		t.Error("a warned-but-ready adoption must still write")
	}
}

// TestAdoptClusters_V4_RivalManagedByBlocks pins the task #150 flip at the
// v4 adopt door: a connection whose ownership marker names another tool is
// BLOCKED by the shared takeover preflight, so the adoption fails and
// nothing is written or relabelled.
func TestAdoptClusters_V4_RivalManagedByBlocks(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}

	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com", ManagedBy: "terraform"},
	}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" {
		t.Fatalf("expected failed — another tool owns the connection — got %s", cr.Status)
	}
	if !strings.Contains(cr.Error, "not ready to take over yet") {
		t.Errorf("expected the preflight summary as the error, got: %q", cr.Error)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("a blocked adoption must not write anything")
	}
	if len(asm.takenOver) != 0 {
		t.Error("a blocked adoption must not swap ArgoCD ownership")
	}
	// Every reason is listed, by id, not only the summary (finding 4).
	if len(cr.Blockers) != 1 || cr.Blockers[0].ID != takeover.FindingSecretOwner {
		t.Fatalf("blockers = %+v, want exactly the %s blocker", cr.Blockers, takeover.FindingSecretOwner)
	}
	if !strings.Contains(cr.Blockers[0].Message, "terraform") {
		t.Errorf("the blocker should say who owns it, got %q", cr.Blockers[0].Message)
	}
}

// TestAdoptClusters_V4_RefusesUntilEveryWarningIsAcknowledged pins call 5
// of the episode 3 fixes: a real adopt with a warning that was not
// acknowledged writes nothing and names the missing ids.
func TestAdoptClusters_V4_RefusesUntilEveryWarningIsAcknowledged(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}
	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com"},
	}
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	for _, acked := range [][]string{nil, {"some-other-id"}} {
		_, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
			Clusters:             []string{"prod-eu"},
			AcknowledgedFindings: acked,
		})
		var needsAck *AdoptNeedsAcknowledgementError
		if !errors.As(err, &needsAck) {
			t.Fatalf("acked=%v: want AdoptNeedsAcknowledgementError, got %v", acked, err)
		}
		if len(needsAck.Missing) != 1 || needsAck.Missing[0] != takeover.FindingSecretOwner {
			t.Errorf("acked=%v: missing = %v, want [%s]", acked, needsAck.Missing, takeover.FindingSecretOwner)
		}
		if len(needsAck.Results) != 1 || len(needsAck.Results[0].Warnings) != 1 ||
			needsAck.Results[0].Warnings[0].ID != takeover.FindingSecretOwner || needsAck.Results[0].Warnings[0].Title == "" {
			t.Errorf("acked=%v: results must carry the warning with id and title: %+v", acked, needsAck.Results)
		}
		want := "Sharko did not adopt anything. Read each warning and acknowledge it first — not acknowledged yet: secret-owner"
		if err.Error() != want {
			t.Errorf("error text = %q, want %q", err.Error(), want)
		}
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("an unacknowledged adoption must not write anything")
	}
	if len(asm.takenOver) != 0 {
		t.Error("an unacknowledged adoption must not swap ArgoCD ownership")
	}

	// A dry run needs no acknowledgement and shows the warning by id.
	res, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{Clusters: []string{"prod-eu"}, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(res.Results[0].Warnings) != 1 || res.Results[0].Warnings[0].ID != takeover.FindingSecretOwner {
		t.Errorf("dry run warnings = %+v", res.Results[0].Warnings)
	}
}

// TestAdoptSingleClusterV4_WarningThatAppearsLateStillStops covers the
// second check: a warning that was not there when AdoptClusters first
// looked still stops that cluster before anything is written.
func TestAdoptSingleClusterV4_WarningThatAppearsLateStillStops(t *testing.T) {
	git := adoptV4Git()
	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com"},
	}
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	cr := orch.adoptSingleClusterV4(context.Background(), "prod-eu", "https://prod-eu.example.com", AdoptClustersRequest{})
	if cr.Status != "failed" || len(cr.UnacknowledgedFindings) != 1 || cr.UnacknowledgedFindings[0] != takeover.FindingSecretOwner {
		t.Fatalf("got status %q, unacknowledged %v", cr.Status, cr.UnacknowledgedFindings)
	}
	want := "Not adopted: there is a warning you have not acknowledged yet (secret-owner). Read it, then adopt again and acknowledge it."
	if cr.Error != want {
		t.Errorf("error = %q, want %q", cr.Error, want)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("nothing may be written")
	}
}

// TestAdoptClusters_V4_DryRun_ShowsBothFilesAndWritesNothing.
func TestAdoptClusters_V4_DryRun_ShowsBothFilesAndWritesNothing(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}
	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		"prod-eu": {Found: true, Server: "https://prod-eu.example.com"},
	}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"prod-eu"},
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", cr.Status, cr.Error)
	}
	if cr.DryRun == nil {
		t.Fatal("expected a dry-run preview")
	}
	if len(cr.DryRun.FilesToWrite) != 2 {
		t.Fatalf("expected both v4 files in the preview, got %d: %+v", len(cr.DryRun.FilesToWrite), cr.DryRun.FilesToWrite)
	}
	sawConnections, sawAddons := false, false
	for _, p := range cr.DryRun.FilesToWrite {
		if p.Path == V4ManagedClustersPath {
			sawConnections = true
			if p.Action != "create" {
				t.Errorf("expected managed-clusters.yaml preview action=create, got %q", p.Action)
			}
			if !strings.Contains(p.Diff, "prod-eu") {
				t.Errorf("expected the diff to name prod-eu, got: %q", p.Diff)
			}
		}
		if p.Path == "cluster-addons/prod-eu.yaml" {
			sawAddons = true
		}
	}
	if !sawConnections || !sawAddons {
		t.Errorf("expected previews for both files, got %+v", cr.DryRun.FilesToWrite)
	}
	if !strings.Contains(cr.DryRun.PRTitle, "adopt cluster prod-eu") {
		t.Errorf("unexpected PR title: %q", cr.DryRun.PRTitle)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("a dry run must not write anything")
	}
	if len(asm.takenOver) != 0 {
		t.Error("a dry run must not swap ArgoCD ownership")
	}
}

// TestAdoptClusters_V4_Batch_OneBlockedOneSucceeds pins that the batch
// continues past a failed cluster.
func TestAdoptClusters_V4_Batch_OneBlockedOneSucceeds(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "ready-cluster", Server: "https://ready.example.com"},
		{Name: "blocked-cluster", Server: "https://blocked.example.com"},
	}

	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{
		"ready-cluster": {Found: true, Server: "https://ready.example.com"},
		// blocked-cluster: no detail seeded -> Found:false -> blocked.
	}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"ready-cluster", "blocked-cluster"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}
	byName := map[string]AdoptClusterResult{}
	for _, r := range result.Results {
		byName[r.Name] = r
	}
	if byName["ready-cluster"].Status != "success" {
		t.Errorf("ready-cluster: expected success, got %s", byName["ready-cluster"].Status)
	}
	if byName["blocked-cluster"].Status != "failed" {
		t.Errorf("blocked-cluster: expected failed, got %s", byName["blocked-cluster"].Status)
	}
}

// TestAdoptClusters_V4_NoInClusterInstall_FailsThatClusterOnly — no
// ArgoSecretManager wired means Sharko cannot run the preflight at all;
// that cluster fails, but the batch (and the function call) still returns
// no top-level error.
func TestAdoptClusters_V4_NoInClusterInstall_FailsThatClusterOnly(t *testing.T) {
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.com"},
	}
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	// No SetArgoSecretManager call.

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" {
		t.Fatalf("expected failed, got %s", cr.Status)
	}
	if !strings.Contains(cr.Error, "in-cluster install") {
		t.Errorf("expected an explanation naming the missing in-cluster install, got: %q", cr.Error)
	}
}

// TestAdoptClusters_V3Repo_UnaffectedByV4Adopt is the regression guard in
// the other direction: a v3 repo (no engine pin) must keep using the
// existing v3 adopt path untouched — same shape TestRegisterCluster_V3Repo_
// UnaffectedByV4Detection pins for RegisterCluster.
func TestAdoptClusters_V3Repo_UnaffectedByV4Adopt(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")
	asm := newMockArgoSecretManager()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters: []string{"cluster-a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Results[0].Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Results[0].Status, result.Results[0].Error)
	}
	if _, ok := git.files["configuration/managed-clusters.yaml"]; !ok {
		t.Error("expected the v3 registry to be written")
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("v4 managed-clusters.yaml must not be written on a v3 repo")
	}
	// GetClusterSecretDetail / TakeOverClusterSecret are v4-only primitives
	// — the v3 path uses GetManagedByLabel + Ensure/SetAnnotation instead.
	if len(asm.takenOver) != 0 {
		t.Error("the v3 adopt path must not call TakeOverClusterSecret")
	}
}
