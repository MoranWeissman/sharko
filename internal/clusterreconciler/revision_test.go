package clusterreconciler

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// revisionAwareGit wraps fakeGit and additionally implements
// gitprovider.BranchRevisioner, returning a fixed SHA — so tests can prove
// the compared-revision path lights up when the active provider supports
// it, distinct from fakeGit's own "provider that cannot say" case (used
// elsewhere in this package's test suite without modification).
type revisionAwareGit struct {
	*fakeGit
	sha string
}

func (g *revisionAwareGit) GetBranchHeadSHA(_ context.Context, _ string) (string, error) {
	return g.sha, nil
}

const testRevisionSHA1 = "1111111111111111111111111111111111aaaa"
const testRevisionSHA2 = "2222222222222222222222222222222222bbbb"

// TestPollOnce_StampsComparedRevisionAndPath proves the pass-level facts
// (P2-C1): when the active provider implements BranchRevisioner, every
// cluster record from that pass carries the branch head SHA it read and
// the exact managed-clusters path it read it from.
func TestPollOnce_StampsComparedRevisionAndPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &revisionAwareGit{
		fakeGit: &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: envelopedManagedClusters("spoke-eu")}},
		sha:     testRevisionSHA1,
	}
	client := fake.NewSimpleClientset()
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-eu": {Server: "https://spoke-eu.example.com", CAData: []byte("ca"), Token: "tk"},
	}}

	r := newReconcilerForTest(t, gp, client, vault, &auditCollector{}, nil)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("spoke-eu")
	if !ok {
		t.Fatal("expected a reconcile record for spoke-eu")
	}
	if rec.ComparedRevision != testRevisionSHA1 {
		t.Errorf("ComparedRevision = %q, want %q", rec.ComparedRevision, testRevisionSHA1)
	}
	if rec.ComparedPath != DefaultManagedClustersPath {
		t.Errorf("ComparedPath = %q, want %q", rec.ComparedPath, DefaultManagedClustersPath)
	}
}

// TestFetchComparedRevision_ProviderWithoutBranchRevisioner proves the
// honest-silence half of P2-C1: a provider that does not implement
// BranchRevisioner (fakeGit, used unmodified everywhere else in this
// package) never gets a guessed or stale SHA — ComparedRevision is empty,
// ComparedPath is still reported (the path is known independent of the
// revision call).
func TestFetchComparedRevision_ProviderWithoutBranchRevisioner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-eu": {Server: "https://spoke-eu.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	r := newReconcilerForTest(t, nil, client, vault, &auditCollector{}, envelopedManagedClusters("spoke-eu"))
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("spoke-eu")
	if !ok {
		t.Fatal("expected a reconcile record for spoke-eu")
	}
	if rec.ComparedRevision != "" {
		t.Errorf("ComparedRevision = %q, want empty — fakeGit does not implement BranchRevisioner", rec.ComparedRevision)
	}
	if rec.ComparedPath != DefaultManagedClustersPath {
		t.Errorf("ComparedPath = %q, want %q", rec.ComparedPath, DefaultManagedClustersPath)
	}
}

// TestTwoRevisions_AppliedStaysBehindComparedAcrossAChecksOnlyTick is the
// core two-revision proof (P2-C1's generation/observedGeneration pair):
//
//  1. First pollOnce (the git provider is at SHA1) CREATES the cluster
//     secret — a real write — so AppliedRevision becomes SHA1, matching
//     ComparedRevision.
//  2. The git provider moves to SHA2 (a new commit merged) but the
//     cluster's desired addon labels did not actually change, so the
//     SECOND pollOnce finds the secret already in sync and writes
//     NOTHING.
//
// AppliedRevision must stay at SHA1 (the commit the secret's content was
// actually built from) while ComparedRevision moves to SHA2 (the commit
// Sharko just read) — proving the two facts are tracked independently, not
// as one value that silently follows every check.
func TestTwoRevisions_AppliedStaysBehindComparedAcrossAChecksOnlyTick(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	files := map[string][]byte{DefaultManagedClustersPath: envelopedManagedClusters("spoke-eu")}
	gp := &revisionAwareGit{fakeGit: &fakeGit{files: files}, sha: testRevisionSHA1}
	client := fake.NewSimpleClientset()
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-eu": {Server: "https://spoke-eu.example.com", CAData: []byte("ca"), Token: "tk"},
	}}

	r := newReconcilerForTest(t, gp, client, vault, &auditCollector{}, nil)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("spoke-eu")
	if !ok || rec.Outcome != OutcomeSucceeded {
		t.Fatalf("expected a succeeded create on the first pass, got %+v (ok=%v)", rec, ok)
	}
	if rec.ComparedRevision != testRevisionSHA1 || rec.AppliedRevision != testRevisionSHA1 {
		t.Fatalf("after create: compared=%q applied=%q, want both %q", rec.ComparedRevision, rec.AppliedRevision, testRevisionSHA1)
	}

	// Git moves to SHA2; the secret is already correctly labeled, so this
	// pass's per-cluster work finds nothing to write.
	gp.sha = testRevisionSHA2
	r.pollOnce(ctx)

	rec, ok = r.LastReconcile("spoke-eu")
	if !ok {
		t.Fatal("expected a reconcile record for spoke-eu after the second pass")
	}
	if rec.ComparedRevision != testRevisionSHA2 {
		t.Errorf("ComparedRevision after the no-op tick = %q, want %q (this pass's own read)", rec.ComparedRevision, testRevisionSHA2)
	}
	if rec.AppliedRevision != testRevisionSHA1 {
		t.Errorf("AppliedRevision after the no-op tick = %q, want %q (unchanged — nothing was written this pass)", rec.AppliedRevision, testRevisionSHA1)
	}
}

// TestCreateOne_StampsProvenanceAnnotations_NeverDataDerived pins the exact
// annotation set a real write stamps (P2-C5) and proves, structurally, that
// none of it can carry secret content: connectionProvenanceAnnotations
// only ever accepts a file path, a commit SHA, and a timestamp — there is
// no parameter through which a Secret's Data/StringData could reach an
// annotation.
func TestCreateOne_StampsProvenanceAnnotations_NeverDataDerived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	gp := &revisionAwareGit{
		fakeGit: &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: envelopedManagedClusters("spoke-eu")}},
		sha:     testRevisionSHA1,
	}
	client := fake.NewSimpleClientset()
	const bearerToken = "sk-live-should-never-appear-in-an-annotation"
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-eu": {Server: "https://spoke-eu.example.com", CAData: []byte("ca"), Token: bearerToken},
	}}

	r := newReconcilerForTest(t, gp, client, vault, &auditCollector{}, nil)
	r.pollOnce(ctx)

	secret := getSecret(t, client, "spoke-eu")

	wantAnnotations := map[string]string{
		AnnotationSourceFile: DefaultManagedClustersPath,
		AnnotationRevision:   testRevisionSHA1,
	}
	for k, want := range wantAnnotations {
		if got := secret.Annotations[k]; got != want {
			t.Errorf("annotation %q = %q, want %q", k, got, want)
		}
	}
	if _, ok := secret.Annotations[AnnotationWrittenAt]; !ok {
		t.Error("expected sharko.dev/written-at to be stamped")
	}
	// Exact set: exactly the three provenance keys, nothing else — a
	// growing annotation set here would be the first sign of a leak.
	if len(secret.Annotations) != 3 {
		t.Fatalf("annotations = %v, want exactly 3 keys (source-file, revision, written-at)", secret.Annotations)
	}
	for k, v := range secret.Annotations {
		if strings.Contains(v, bearerToken) {
			t.Fatalf("annotation %q leaked the credential: %q", k, v)
		}
	}
}
