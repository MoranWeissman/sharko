package api

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3-DRYRUN-FIXES join, design call 4: two hooks run when a pull request
// is done. Story A's FinishClusterFollowUpOnMerge finishes a remove or an
// un-adopt; story B's FinishAdoptOnPRComplete finishes an adopt. Each must
// leave the other's pull requests alone. The Secret below is set up so that
// the operation is the only thing stopping each hook: it carries Sharko's
// label (what A's remove would strip) and an adopt waiting mark that names
// the very pull request sent (what B's adopt would act on).
//
// A's hook has two guards: the early operation check and the switch at
// its end. Break-tested by removing B's check (fails), and by removing A's
// early check and letting its switch treat an adopt like a remove (fails).
func TestMergeHooks_EachIgnoresTheOtherOperation(t *testing.T) {
	const prID = 41
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	srv, k8s := followUpTestServer(t, newTakeoverFakeGP(), argo.URL, argocdAddedSecret(map[string]string{
		argosecrets.AnnotationAdoptPendingPR: strconv.Itoa(prID),
	}))
	before := getAddedSecret(t, k8s)

	// An adopt pull request, merged, sent to A's hook. It carries a
	// follow-up asking to drop the connection, so only the operation check
	// keeps A's hook from acting.
	if err := srv.FinishClusterFollowUpOnMerge(context.Background(), prtracker.PRInfo{
		PRID: prID, Cluster: addedClusterName, Operation: prtracker.OpAdoptCluster,
		FollowUp: &prtracker.ClusterFollowUp{Cleanup: "all"},
	}); err != nil {
		t.Fatalf("the remove hook returned an error for an adopt pull request: %v", err)
	}

	// A remove pull request, merged, sent to B's hook.
	got, _ := srv.FinishAdoptOnPRComplete(context.Background(), prtracker.PRInfo{
		PRID: prID, Cluster: addedClusterName, Operation: prtracker.OpRemoveCluster,
		FollowUp: &prtracker.ClusterFollowUp{Cleanup: "all"},
	}, "merged")
	if got != adoptFinishIgnored {
		t.Fatalf("the adopt hook acted on a remove pull request: %q", got)
	}

	after := getAddedSecret(t, k8s)
	if !reflect.DeepEqual(before.Labels, after.Labels) {
		t.Fatalf("labels changed: before %v, after %v", before.Labels, after.Labels)
	}
	if !reflect.DeepEqual(before.Annotations, after.Annotations) {
		t.Fatalf("annotations changed: before %v, after %v", before.Annotations, after.Annotations)
	}
	if n := atomic.LoadInt32(argo.deleteCalls); n != 0 {
		t.Fatalf("the remove hook deleted the cluster from ArgoCD for an adopt pull request (%d DELETE calls)", n)
	}
}
