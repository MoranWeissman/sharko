package api

// connection_repair.go — POST /clusters/{name}/connection-repair.
//
// "Make this cluster's ArgoCD connection match what Sharko intends."
//
// This is the first endpoint in the connection-Secret feature that WRITES. Steps
// 1 and 2 were read-only. Everything below is shaped by that.
//
// WHY A NEW ENDPOINT AND NOT A WIDER /resync.
// POST /clusters/{name}/resync re-applies Sharko's own addon labels and nothing
// else. Its name, its behaviour, its cluster.resync action and its response shape
// are unchanged by this step — a test pins that. Teaching it to rewrite a whole
// connection would mean one endpoint whose blast radius depends on the cluster it
// was pointed at, which is exactly the kind of surprise a write endpoint must not
// have.
//
// THE REQUEST IDENTIFIES A CLUSTER AND THE COMMIT THE CALLER REVIEWED.
// The cluster name in the path and the reviewed commit in the query are the only
// inputs. No body is read. The commit is the one the caller reviewed from the
// check's response, not a value to write — it guards against racing a git move.
// There is deliberately no way to pass a candidate value, an expected manifest, a
// hash, a secrets-backend path, a destination override or a namespace — the
// namespace comes from the running reconciler. A caller cannot steer what gets
// written, only ask for the one thing this endpoint does.
//
// WHERE THE EXPECTED STATE COMES FROM, AND WHERE IT NEVER COMES FROM.
// Git for the desired labels and the cluster record; the secrets backend for the
// credential material. The read-only comparison check uses the stored-facts path
// that creates nothing; the repair uses the normal credentials route that creates
// exactly one short-lived token for the write (see ConnectionCredentialSpecForWrite
// in internal/clusterreconciler/repair_credentials.go). NEVER the live ArgoCD
// Secret. Building what you are about to write out of what is already there is the
// self-comparison trap step 2 exists to prevent, and here it would be written back
// to the cluster — a corrupted connection would be "repaired" into staying
// corrupted, and reported as fixed.
//
// THE POLICY IS READ, NOT RE-DECIDED.
// connectioncompare.Policy.RepairScope already answers what a repair may touch
// for each of the seven connection modes. This handler reads that answer and
// obeys it. It does not re-derive the question anywhere, and it asks the same
// CanReadStoredFactsIndependentOfArgoCDSecret the comparison asks — a review round
// on step 2 was spent fixing a second, wider question that let an ArgoCD fallback
// backend earn full scope.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// connectionRepairTimeout caps the whole repair. A handful of reads and one
// write, on a click.
const connectionRepairTimeout = 30 * time.Second

// Fixed, safe sentences. Every one is a literal here. A git error, a Kubernetes
// error or a credentials-backend error is never passed through — and on the
// credential path there is nothing to pass through anyway, because credsafe.Mark
// makes those errors say one fixed sentence.
const (
	repairFailNoReconciler = "The part of Sharko that manages cluster connections is not running on this server, so it cannot repair this connection."
	repairFailNoGit        = "Sharko is not connected to a git repository right now, so it cannot see what this connection should look like."
	repairFailNoHubClient  = "Sharko is not connected to its own cluster on this server, so it cannot change this connection."
	repairFailGitRead      = "Sharko could not read this cluster's record from git, so it does not know what the connection should be. Check the git connection and try again."
	repairFailNotManaged   = "This cluster has no entry in the git-managed cluster list, so Sharko has nothing to repair its connection against."
	repairFailBuild        = "Sharko could not work out what this cluster's connection should look like, so it changed nothing."
	repairFailWrite        = "Sharko could not write this cluster's connection. Nothing was changed. Try again in a moment."
	repairFailSecretGone   = "There is no ArgoCD connection for this cluster to repair. Sharko creates a missing connection on its own — check again in a moment."
	repairFailRaced        = "Something else changed this cluster's connection while Sharko was repairing it, so Sharko wrote nothing. Run the check again to see where it stands now."
	repairFailNotOwned     = "This cluster's ArgoCD connection is not Sharko's to change any more — its ownership marker names something else, or it has none at all. Sharko changed nothing."

	// The revision guard's three sentences (R3-4). They are separate because
	// they are genuinely different situations for the person reading them.
	repairFailRevisionUnknown = "Sharko cannot tell which commit your git branch is on right now, so it will not rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching. Re-run the connection check and try again."
	repairFailRevisionMissing = "This repair did not say which commit it was reviewed against. Run the connection check first, then repair from its result."
	repairFailRevisionMoved   = "Your git branch moved while you were looking at this connection, so what you reviewed is not what Sharko would write now. Sharko changed nothing. Run the connection check again and repair from the fresh result."
)

// connectionRepairScopeApplied is what the repair was allowed to touch.
type connectionRepairScopeApplied string

const (
	// repairScopeFullConnection — the whole connection was rewritten from git
	// plus the secrets backend.
	repairScopeFullConnection connectionRepairScopeApplied = "full_connection"
	// repairScopeAddonLabelsOnly — only Sharko's own addon labels were
	// re-applied. This is the same act POST /clusters/{name}/resync performs.
	repairScopeAddonLabelsOnly connectionRepairScopeApplied = "addon_labels_only"
)

// connectionRepairView is the response body. Typed, not a map.
//
// Nothing here can carry credential material. The value-bearing fields are the
// cluster name, field PATHS, counts, a commit SHA, a branch name, and the fixed
// sentences in this file.
type connectionRepairView struct {
	// Cluster is the cluster this answer is about.
	Cluster string `json:"cluster"`

	// Repaired is true when Sharko actually changed something. False with a
	// successful response means the connection already matched what Sharko
	// intends — worth saying plainly rather than implying a fix happened.
	Repaired bool `json:"repaired"`

	// ScopeApplied is what this repair was allowed to touch: full_connection or
	// addon_labels_only. It comes from the mode policy, never from the request.
	ScopeApplied string `json:"scope_applied"`

	// FieldsRepaired are the owned field paths that changed, sorted. PATHS
	// only — data.config appears by name when the credential blob was
	// rewritten, and its contents appear nowhere in this response or anywhere
	// else.
	FieldsRepaired []string `json:"fields_repaired"`

	// LabelDiff reports the addon-label changes for a labels-only repair, the
	// same shape POST /resync reports.
	LabelDiff *models.ClusterResyncLabelDiff `json:"label_diff,omitempty"`

	// PreservedForeignLabels and PreservedForeignDataKeys count what the repair
	// deliberately left alone. Counts, not names: naming somebody else's keys
	// would be reporting on their data.
	PreservedForeignLabels   int `json:"preserved_foreign_labels"`
	PreservedForeignDataKeys int `json:"preserved_foreign_data_keys"`

	// Branch is the configured git branch. Sharko never discovers a default
	// branch.
	Branch string `json:"branch"`

	// RepairedAtCommit is the commit this repair was built from and verified
	// against. Always set on a successful repair — a repair with an unknown
	// commit is refused rather than performed (see the revision guard).
	RepairedAtCommit string `json:"repaired_at_commit,omitempty"`

	// RepairedAt is when the repair ran, RFC3339.
	RepairedAt string `json:"repaired_at"`

	// Message is one plain sentence about what happened.
	Message string `json:"message"`

	// Comparison is the FRESH comparison re-run after the repair, so the caller
	// sees what the repair achieved rather than being told "done" and having to
	// ask. Same sanitized shape the read-only endpoint returns: a sensitive
	// field carries a path, a status and sensitive true, with no expected value
	// and no live value present at all.
	Comparison connectionComparisonView `json:"comparison"`

	// SelfHealUnchanged is always true. Repair does not read the self-heal
	// setting as a precondition, does not write it, and does not depend on it.
	// It is in the body so the promise is visible to anyone reading the API.
	SelfHealUnchanged bool `json:"self_heal_unchanged"`

	// ValuesNeverReturned is always true, same promise the comparison makes.
	ValuesNeverReturned bool `json:"values_never_returned"`
}

// handleRepairConnection godoc
//
// @Summary Repair a cluster's ArgoCD connection to match what Sharko intends
// @Description Rewrites the parts of this cluster's ArgoCD connection Secret that Sharko owns so they match the configured git branch plus, where one exists, the cluster's configured credentials source held outside the connection. For an EKS cluster that source is the cluster's own details rather than a reusable sign-in credential, and the write creates exactly one short-lived sign-in token from it — the read-only check creates none. What it is allowed to touch depends on the kind of connection: a connection Sharko owns whose credentials it can read independently gets the whole connection rewritten; a connection Sharko is only a guest on gets its addon labels re-applied and nothing else; a connection another tool owns is refused. The configured credentials source is re-read from the secrets backend and never read back out of the connection being repaired. The update happens in place — never a delete and recreate — and Sharko re-checks who owns the connection immediately before writing, refusing if anything else has taken it over. Foreign labels, foreign annotations, unrelated data keys and labels a takeover carried over all survive untouched. Git is never written. The self-heal setting is neither read nor changed. Requires the commit the caller reviewed; if the branch has moved since, the repair is refused so what was reviewed is what gets written. The response carries a fresh comparison so the caller can see what the repair achieved. No value is ever returned: a sensitive field comes back with its path, one of same/different/missing/unexpected, and sensitive true. The request identifies a cluster and the commit that was reviewed — no caller-supplied value, manifest or hash of one is ever accepted.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param reviewed_commit query string true "The commit the caller reviewed, from the connection check's compared_commit"
// @Success 200 {object} connectionRepairView "The repair result plus a fresh comparison"
// @Failure 400 {object} map[string]interface{} "Cluster name or reviewed commit is missing"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires admin role"
// @Failure 404 {object} map[string]interface{} "This cluster is not in the git-managed cluster list"
// @Failure 409 {object} map[string]interface{} "The git branch moved since the caller reviewed it, or the connection changed underneath — nothing was written"
// @Failure 422 {object} map[string]interface{} "This kind of connection cannot be repaired, or is not Sharko's to change"
// @Failure 502 {object} map[string]interface{} "Sharko could not read what the connection should be, so it changed nothing"
// @Failure 503 {object} map[string]interface{} "Sharko is missing something it needs to run the repair"
// @Router /clusters/{name}/connection-repair [post]
func (s *Server) handleRepairConnection(w http.ResponseWriter, r *http.Request) {
	// THE AUTHZ ACTION.
	//
	// cluster.connection.repair, a NEW action at admin level, with its own
	// ActionRequirements entry (internal/authz/authz.go).
	//
	// Not cluster.resync (operator): that action covers re-applying addon
	// labels, which cannot break a cluster's connection. This endpoint can
	// rewrite the credential material ArgoCD signs in with, so a mistake here
	// takes a cluster offline — that is the blast radius of cluster.takeover and
	// cluster.remove, both admin. Widening cluster.resync to cover both acts
	// would have silently handed every operator the bigger one.
	if !authz.RequireWithResponse(w, r, "cluster.connection.repair") {
		return
	}

	cluster := r.PathValue("name")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	// Audit the attempt up front, so a repair that fails anywhere below still
	// answers "who asked Sharko to change this connection". The middleware
	// emits one entry per request and reads these fields after the handler
	// returns; the outcome is added to Detail as the handler learns it.
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_connection_repair_requested",
		Resource: fmt.Sprintf("cluster:%s", cluster),
		Detail:   "asked Sharko to repair this cluster's ArgoCD connection",
	})

	if s.clusterRecon == nil {
		s.finishRepairAudit(r, cluster, "refused: no reconciler on this server")
		writeError(w, http.StatusServiceUnavailable, repairFailNoReconciler)
		return
	}
	if s.clusterRecon.GitProviderForRead() == nil {
		s.finishRepairAudit(r, cluster, "refused: no git connection")
		writeError(w, http.StatusServiceUnavailable, repairFailNoGit)
		return
	}
	client, ns, ok := s.k8sClientAndNamespace()
	if !ok {
		s.finishRepairAudit(r, cluster, "refused: no in-cluster Kubernetes client")
		writeError(w, http.StatusServiceUnavailable, repairFailNoHubClient)
		return
	}

	// THE REVIEWED COMMIT (R3-4).
	//
	// A repair must write what the caller actually reviewed. The comparison
	// reports the commit it read everything at; the repair requires it back, and
	// refuses when it is absent — there is no "just repair whatever is there
	// now" mode, because that is how somebody repairs a connection to match a
	// commit they never looked at.
	reviewedCommit := r.URL.Query().Get("reviewed_commit")
	if reviewedCommit == "" {
		s.finishRepairAudit(r, cluster, "refused: no reviewed commit supplied")
		writeError(w, http.StatusBadRequest, repairFailRevisionMissing)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectionRepairTimeout)
	defer cancel()

	branch := s.clusterRecon.Branch()

	// FIRST RESOLVE. Pin every read below to one commit, and refuse right away
	// when the provider cannot name one.
	//
	// An unknown revision is NOT a match. Sharko does not guess a commit, does
	// not fall back to reading the branch head twice and hoping, and does not
	// treat empty-equals-empty as agreement. A git provider without the optional
	// BranchRevisioner capability simply cannot support this write, and saying so
	// is better than writing blind. The read-only comparison still works for such
	// a provider — it just reports no commit and no full repair.
	revisionAtStart := s.clusterRecon.ResolveComparedRevision(ctx)
	if revisionAtStart == "" {
		s.finishRepairAudit(r, cluster, "refused: git cannot report the branch commit")
		writeError(w, http.StatusConflict, repairFailRevisionUnknown)
		return
	}
	if revisionAtStart != reviewedCommit {
		s.finishRepairAudit(r, cluster, "refused: the branch moved since the caller reviewed it")
		writeError(w, http.StatusConflict, repairFailRevisionMoved)
		return
	}

	// The desired state, read at the pinned commit.
	desired, desiredErr := s.clusterRecon.DesiredConnectionStateAt(ctx, cluster, revisionAtStart)
	if desiredErr != nil {
		slog.Warn("[connection-repair] could not read the desired state from git",
			"cluster", cluster, "branch", branch, "path", desired.ComparedPath)
		s.finishRepairAudit(r, cluster, "failed: could not read the desired state from git")
		writeError(w, http.StatusBadGateway, repairFailGitRead)
		return
	}
	if !desired.Found {
		s.finishRepairAudit(r, cluster, "refused: cluster is not in the git-managed list")
		writeError(w, http.StatusNotFound, repairFailNotManaged)
		return
	}

	// R3-8: The repair endpoint must independently refuse when addon labels are
	// unknown, checked BEFORE any write. It does not rely on the comparison having
	// been called first; it reads AddonLabelsKnown itself. On a v4 repo whose
	// cluster-addons/<name>.yaml could not be read or parsed, proceeding with an
	// empty desired label set would let the delete loop strip every addon key git
	// "no longer declares" — a git hiccup or a YAML typo would undeploy the
	// cluster's addons. The reconciler and the check pass both already refuse in
	// this state; the repair endpoint must too.
	if !desired.AddonLabelsKnown {
		s.finishRepairAudit(r, cluster, "refused: addon labels are unknown — cannot repair without knowing which addons should run here")
		writeError(w, http.StatusUnprocessableEntity, "Sharko cannot repair this cluster's connection right now because it could not read which addons should run on this cluster (v4 repos: cluster-addons file missing or unparseable). A repair without that information would strip addon labels. Wait for the file to be available, then try again.")
		return
	}

	// The live Secret, for classifying the mode only. It is NEVER used to build
	// what gets written.
	live, getErr := client.CoreV1().Secrets(ns).Get(ctx, cluster, metav1.GetOptions{})
	liveFound := true
	switch {
	case apierrors.IsNotFound(getErr):
		live, liveFound = nil, false
	case getErr != nil:
		slog.Warn("[connection-repair] could not read the live connection secret",
			"cluster", cluster, "namespace", ns)
		s.finishRepairAudit(r, cluster, "failed: could not read the live connection")
		writeError(w, http.StatusBadGateway, repairFailWrite)
		return
	}

	// ONE ANSWER, ASKED ONCE, USED TWICE — the same rule the comparison follows.
	backendCanProvideStoredFacts := s.credsRouter().CanReadStoredFactsIndependentOfArgoCDSecret()
	policy := connectioncompare.Classify(connectioncompare.ClassifyInput{
		CredsSource:                  desired.Entry.CredsSource,
		ConnectionManagedBy:          desired.Entry.ConnectionManagedBy,
		BackendCanProvideStoredFacts: backendCanProvideStoredFacts,
		LiveSecretFound:              liveFound,
		LiveManagedBy:                liveManagedBy(live),
		LiveAdopted:                  live != nil && argosecrets.IsAdopted(live.Annotations),
	})

	// OBEY THE POLICY. It already decided what a repair may touch for this mode;
	// this handler reads that and does not re-decide it.
	switch policy.RepairScope {
	case connectioncompare.RepairScopeNone:
		s.finishRepairAudit(r, cluster, "refused: this connection is owned by another tool")
		writeError(w, http.StatusUnprocessableEntity, policy.LimitReason)
		return

	case connectioncompare.RepairScopeAddonLabelsOnly:
		s.repairAddonLabelsOnly(ctx, w, r, cluster, ns, branch, revisionAtStart, policy, desired)
		return

	case connectioncompare.RepairScopeFullConnection:
		s.repairFullConnection(ctx, w, r, cluster, ns, branch, revisionAtStart, policy, desired,
			backendCanProvideStoredFacts)
		return

	default:
		// A repair scope this handler has never heard of gets the narrow answer,
		// not the trusting one.
		s.finishRepairAudit(r, cluster, "refused: unknown repair scope")
		writeError(w, http.StatusUnprocessableEntity, repairFailBuild)
		return
	}
}

// repairFullConnection rewrites the whole connection from git plus the secrets
// backend.
func (s *Server) repairFullConnection(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	cluster, ns, branch, reviewedCommit string,
	policy connectioncompare.Policy,
	desired clusterreconciler.DesiredConnectionState,
	backendCanProvideStoredFacts bool,
) {
	// The credential material, re-fetched from the BACKEND. Never from the live
	// Secret — that is the trap this whole feature exists to avoid, and here the
	// result would be written back.
	//
	// The predicate is the per-cluster half of the same question the policy was
	// given, handed the SAME backend answer.
	if !desired.Entry.ExpectedCredentialsRebuildableWithoutLiveSecret(backendCanProvideStoredFacts) {
		// The policy said full repair and the record disagrees. The narrower
		// answer wins.
		s.finishRepairAudit(r, cluster, "refused: no independent copy of the sign-in details")
		writeError(w, http.StatusUnprocessableEntity, repairFailBuild)
		return
	}

	// THE ONE ROUTE A WRITE TAKES TO CREDENTIALS (R3-14).
	//
	// ConnectionCredentialSpecForWrite is what the periodic reconcile pass calls
	// too, so a repair and a normal write hand argosecrets.buildSecretConfig the
	// SAME evidence and therefore produce the SAME connection shape. For a stored
	// EKS payload that means minting exactly one sign-in token, the same as the
	// pass — because the thing being written has to let ArgoCD sign in
	// afterwards.
	//
	// This used to be storedSpecForRepair, the read-only no-mint route the
	// COMPARISON uses. For a stored EKS payload that route returns the server and
	// the CA bundle and no token, and buildSecretConfig picks the shape by
	// precedence (cert pair > token > exec) — so with no token and no cert pair
	// it fell through to the execProviderConfig (argocd-k8s-auth) shape while
	// every normal write for that same cluster produced the bearerToken shape.
	// Clicking repair silently changed HOW ArgoCD signs in. If argocd-k8s-auth
	// is not usable from that ArgoCD's environment, the repair broke the
	// connection it was asked to fix, and the fresh check afterwards compared
	// against the wrong shape and called it correct.
	//
	// storedSpecForRepair is still the comparison's read, on both of its call
	// sites, and it stays no-mint. A read-only check must create nothing. The two
	// paths need DIFFERENT reads — see repair_credentials.go for the whole story.
	spec, credErr := s.clusterRecon.ConnectionCredentialSpecForWrite(desired.Entry)
	if credErr != nil {
		// NOTHING IS WRITTEN. The error keeps its type and its credsafe marker;
		// a marked error already SAYS the one fixed safe sentence, so nothing
		// here reads its words and nothing builds a sentence from it. There is
		// deliberately no fallback to a spec with no credential in it — that is
		// exactly how a missing token became a changed sign-in method instead of
		// a refusal.
		slog.Warn("[connection-repair] could not read this cluster's sign-in details for the write",
			"cluster", cluster)
		s.auditRepairCredentialFailure(r, cluster, credErr)
		writeError(w, http.StatusBadGateway, credsafe.Sentence(credErr))
		return
	}

	// The label set a write would stamp: the addon labels git declares, plus the
	// derived connectivity-check label under the same live setting the writer
	// reads.
	specLabels := make(map[string]string, len(desired.AddonLabels)+2)
	for k, v := range desired.AddonLabels {
		specLabels[k] = v
	}
	models.ApplyConnectivityCheckLabel(specLabels, s.clusterRecon.ConnectivityCheckEnabled(ctx))

	spec.Name = cluster
	spec.Labels = specLabels
	// Provenance is the reconciler's to stamp at write time; the builder must not
	// bake it in.
	spec.Annotations = nil

	// THE CANONICAL BUILDER, and the only thing that builds a connection Secret.
	built, buildErr := argosecrets.BuildClusterSecret(spec, ns)
	if buildErr != nil {
		slog.Warn("[connection-repair] could not build the expected connection", "cluster", cluster)
		s.finishRepairAudit(r, cluster, "failed: could not build the expected connection")
		writeError(w, http.StatusBadGateway, repairFailBuild)
		return
	}

	// SECOND RESOLVE, immediately before the write (R3-4).
	//
	// Everything above took time: a git read, a backend read, a build. The branch
	// may have moved in that window, and what the caller reviewed would no longer
	// be what gets written. So the commit is resolved again here and must still
	// be the reviewed one. An unknown revision is not a match.
	//
	// This sits right next to the ownership recheck inside the write primitive —
	// the two guards that make the gap between "we checked" and "we wrote" as
	// small as the code can make it.
	revisionNow := s.clusterRecon.ResolveComparedRevision(ctx)
	if revisionNow == "" {
		s.finishRepairAudit(r, cluster, "refused: git could not report the branch commit at write time")
		writeError(w, http.StatusConflict, repairFailRevisionUnknown)
		return
	}
	if revisionNow != reviewedCommit {
		s.finishRepairAudit(r, cluster, "refused: the branch moved while Sharko was preparing the repair")
		writeError(w, http.StatusConflict, repairFailRevisionMoved)
		return
	}

	// THE WRITE. The ownership recheck happens inside this call, against the
	// object it is about to update, with nothing but in-memory work between.
	result, repairErr := s.clusterRecon.RepairOwnedConnectionSecret(ctx, built, revisionNow)
	if repairErr != nil {
		status, message, detail := repairRefusal(repairErr)
		s.finishRepairAudit(r, cluster, detail)
		writeError(w, status, message)
		return
	}

	if result.Changed {
		// An event only for a repair that actually changed something — a no-op
		// is not news, and an event per no-op would train people to ignore them.
		s.clusterRecon.EmitConnectionRepairEvent(cluster, len(result.FieldsWritten))
	}

	view := connectionRepairView{
		Cluster:                  cluster,
		Repaired:                 result.Changed,
		ScopeApplied:             string(repairScopeFullConnection),
		FieldsRepaired:           result.FieldsWritten,
		PreservedForeignLabels:   result.PreservedForeignLabels,
		PreservedForeignDataKeys: result.PreservedForeignDataKeys,
		Branch:                   branch,
		RepairedAtCommit:         revisionNow,
		RepairedAt:               time.Now().UTC().Format(time.RFC3339),
		SelfHealUnchanged:        true,
		ValuesNeverReturned:      true,
	}
	if result.Changed {
		view.Message = fmt.Sprintf("Sharko rewrote %d part(s) of this cluster's connection to match git and this cluster's configured credentials source. Foreign labels, other data keys and annotations were left alone. The self-heal setting was not changed.", len(result.FieldsWritten))
	} else {
		view.Message = "This cluster's connection already matched what Sharko intends, so nothing was changed. The self-heal setting was not changed."
	}

	// RE-RUN THE COMPARISON and return the fresh result, so the caller sees what
	// the repair achieved instead of being told "done" and having to ask.
	view.Comparison = s.freshComparisonAfterRepair(ctx, cluster, ns, branch, revisionNow, desired, backendCanProvideStoredFacts)

	s.finishRepairAudit(r, cluster, repairOutcomeDetail(result.Changed, len(result.FieldsWritten), view.Comparison.Status))
	writeJSON(w, http.StatusOK, view)
}

// repairAddonLabelsOnly re-applies just Sharko's addon labels using PRE-RESOLVED
// pinned labels from the VERIFIED reviewed commit, which is the whole of what a
// repair may do on a guest connection.
//
// R3-9 fix: this function now calls the NEW SAFE PRIMITIVE wrapper
// (Reconciler.RepairAddonLabelsWithPinnedDesired) which routes to
// argosecrets.Manager.RepairAddonLabelsWithOwnershipCheck. That primitive accepts
// pinned labels (not re-reading git) and performs the ownership recheck immediately
// before writing (not trusting the classification from earlier in the request).
//
// The OLD UNSAFE PATH (RepairAddonLabelsOnly → ResyncClusterLabels) is kept for
// POST /clusters/{name}/resync, where re-reading the latest git state IS the
// point and there is no reviewed commit.
//
// What it reports comes from the write and nothing else. The primitive returns
// the label paths it really changed — additions, value changes and removals,
// sorted — plus the counts of foreign material it left alone, and those go
// straight into the response. Nothing here recomputes them from the desired set.
func (s *Server) repairAddonLabelsOnly(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	cluster, ns, branch, reviewedCommit string,
	policy connectioncompare.Policy,
	desired clusterreconciler.DesiredConnectionState,
) {
	// R3-9 rule 1: the repair does NO further git reads after the reviewed commit
	// is verified. The desired addon labels were computed once from the pinned
	// commit; they are passed into the writer, not re-resolved.
	//
	// R3-9 rule 7 test guard: resolve again here, verify it still matches reviewed,
	// and refuse if not. This is the ONLY place the labels-only path resolves git
	// after the handler's top-level verification, and it does so to REFUSE when the
	// branch moved — not to read new labels from it.
	revisionNow := s.clusterRecon.ResolveComparedRevision(ctx)
	if revisionNow == "" {
		s.finishRepairAudit(r, cluster, "refused: git could not report the branch commit at write time")
		writeError(w, http.StatusConflict, repairFailRevisionUnknown)
		return
	}
	if revisionNow != reviewedCommit {
		s.finishRepairAudit(r, cluster, "refused: the branch moved while Sharko was preparing the repair")
		writeError(w, http.StatusConflict, repairFailRevisionMoved)
		return
	}

	// R3-9 rule 2: the writer is given the pinned desired labels (from the reviewed
	// commit) and the expected ownership mode (classified once, at the top of the
	// handler). It re-reads the Secret, confirms ownership matches, and only then
	// Updates. The re-read and the Update have nothing but in-memory map work
	// between them.
	//
	// expectedOwned: derived from the classification. Sharko-owned means this is a
	// connection Sharko created (has managed-by=sharko label) OR adopted (has the
	// adopted annotation). Guest means neither — we're only applying labels to
	// somebody else's connection.
	//
	// The key insight: labels-only scope means we're a guest. Everything that
	// reached labels-only scope is either self-managed, adopted, or something
	// Sharko doesn't fully own. For the ownership check in the primitive, owned
	// means "has one of Sharko's markers": managed-by label OR adopted annotation.
	// So we reverse-engineer: if the policy gave us labels-only scope AND we have
	// a live Secret (we must have one to repair labels), then this must be either
	// self-managed (no markers) or adopted (adopted marker). The primitive needs to
	// know whether Sharko's marker should be present.
	//
	// HOWEVER, the classification already told us whether the live Secret has these
	// markers in ClassifyInput. Rather than re-reading the Secret here just to set
	// this flag, we use the fact that labels-only scope on a FOUND Secret means
	// one of three things:
	//   1. Self-managed record → no markers expected → expectedOwned = false
	//   2. Adopted Secret → adopted marker expected → expectedOwned = true
	//   3. Inline kubeconfig → managed-by expected → expectedOwned = true
	//   4. Unknown source → could be either → need to check the live Secret
	//
	// Looking at the modes that reach labels-only scope (mode.go:287-335):
	// - ModeSelfManaged (connectionManagedBy = user) → no markers
	// - ModeAdopted (live has adopted annotation) → adopted marker
	// - ModeInlineKubeconfig (credsSource = inline-kubeconfig) → managed-by
	// - ModeUnknownSource (empty or unrecognized) → managed-by
	//
	// The safe, simple rule: labels-only scope in the repair endpoint means Sharko
	// either created this Secret (inline/unknown → managed-by label) or adopted it
	// (adopted → annotation). Self-managed is the ONLY labels-only case where
	// Sharko holds no marker. So:
	expectedOwned := policy.Mode != connectioncompare.ModeSelfManaged

	outcome, found, err := s.clusterRecon.RepairAddonLabelsWithPinnedDesired(
		ctx, cluster, desired.AddonLabels, revisionNow, expectedOwned,
	)

	// ONE MAPPING FOR ONE SITUATION, ON BOTH PATHS.
	//
	// repairRefusal is the full path's mapper and it is now this path's mapper
	// too, because the primitive can return the same refusals: an ownership
	// change, and a Kubernetes version conflict when something else wrote the
	// Secret in the window (ErrRepairSecretChangedUnderneath → 409).
	//
	// This branch used to recognise only ErrRepairOwnershipChanged and send
	// everything else to a 502, so a conflict was a bad gateway here and a 409
	// on the full path — one cause, two answers, depending on which scope the
	// cluster happened to fall into. A caller cannot write sane retry logic
	// against that.
	if err != nil {
		status, message, detail := repairRefusal(err)
		// The error's text is not passed to the caller — repairRefusal returns
		// one of this file's fixed sentences. It is logged, because a
		// Kubernetes error here is a Kubernetes error: the credential read on
		// this path never happens at all, so there is nothing credential-shaped
		// to leak.
		slog.Warn("[connection-repair] the labels-only repair did not complete",
			"cluster", cluster, "error", err)
		s.finishRepairAudit(r, cluster, detail)
		writeError(w, status, message)
		return
	}

	// Not found is not an error for labels-only: a missing connection is the
	// reconciler's job to create on its normal pass, and doing it here would
	// conflate "repair" with "provision".
	if !found {
		s.finishRepairAudit(r, cluster, "no-op: the ArgoCD connection Secret does not exist yet")
		writeError(w, http.StatusNotFound, "This cluster's ArgoCD connection Secret does not exist yet. Wait for the reconciler to create it on its next pass, then repair if needed.")
		return
	}

	// WHAT CHANGED IS REPORTED, NOT WORKED OUT AGAIN HERE.
	//
	// The primitive diffs the label map it is about to write against the one it
	// read, and that same diff is what decides whether it writes at all. So its
	// answer and its decision cannot disagree, and this handler's job is to
	// repeat it — not to reconstruct it.
	//
	// It used to reconstruct it, from a bare bool plus the desired label set, and
	// every part of that was visibly wrong to whoever read the response: change
	// one label out of twenty and it listed all twenty; a repair that only
	// REMOVED a label reported zero fields for a write that really happened; the
	// order came out of a map so it was different every call; and the preserved
	// counts were never set, so they always read zero even on a connection full
	// of somebody else's labels.
	//
	// LabelDiff stays nil on this path. POST /resync reports its own
	// Added/Removed/Changed diff because it uses the older primitive that
	// computes one; FieldsWritten is the same information in the shape both
	// repair scopes share.

	view := connectionRepairView{
		Cluster:                  cluster,
		Repaired:                 outcome.Changed,
		ScopeApplied:             string(repairScopeAddonLabelsOnly),
		FieldsRepaired:           outcome.FieldsWritten,
		PreservedForeignLabels:   outcome.PreservedForeignLabels,
		PreservedForeignDataKeys: outcome.PreservedForeignDataKeys,
		Branch:                   branch,
		RepairedAtCommit:         revisionNow,
		RepairedAt:               time.Now().UTC().Format(time.RFC3339),
		SelfHealUnchanged:        true,
		ValuesNeverReturned:      true,
	}

	if outcome.Changed {
		view.Message = fmt.Sprintf("Sharko re-applied this cluster's addon labels to match git — %d label(s) changed. Sharko never read or changed this connection's sign-in details. The self-heal setting was not changed.",
			len(outcome.FieldsWritten))
		// The LABELS event, not the connection one. EmitConnectionRepairEvent's
		// message says Sharko rewrote the connection's sign-in details from the
		// configured credentials source, and this path never reads them — the
		// event text is what an operator acts on, so it has to be true.
		s.clusterRecon.EmitAddonLabelsRepairEvent(cluster, len(outcome.FieldsWritten))
	} else {
		view.Message = "This cluster's addon labels already matched git, so nothing was changed. Sharko never read or changed this connection's sign-in details. The self-heal setting was not changed."
	}

	// A guest connection's fresh comparison is built with no expected credential
	// spec at all — Sharko holds no expectation about that connection's details,
	// which is the whole reason the repair was labels-only.
	view.Comparison = s.freshComparisonAfterRepair(ctx, cluster, ns, branch, revisionNow, desired, false)

	s.finishRepairAudit(r, cluster, repairOutcomeDetail(outcome.Changed, len(outcome.FieldsWritten), view.Comparison.Status))
	writeJSON(w, http.StatusOK, view)
}

// freshComparisonAfterRepair re-runs the read-only comparison and returns it in
// the same sanitized wire shape the comparison endpoint uses.
//
// It re-reads the live Secret (that is the point — it must see what the repair
// actually left behind) and re-classifies from scratch. It never invents an
// answer: if anything it needs cannot be read, the comparison comes back
// check_failed, exactly as it would through its own endpoint. check_failed is
// never turned into synced or out_of_sync here or anywhere else.
func (s *Server) freshComparisonAfterRepair(
	ctx context.Context, cluster, ns, branch, revision string,
	desired clusterreconciler.DesiredConnectionState,
	backendCanProvideStoredFacts bool,
) connectionComparisonView {
	view := connectionComparisonView{
		Cluster:              cluster,
		CheckedAt:            time.Now().UTC().Format(time.RFC3339),
		Branch:               branch,
		ComparedCommit:       revision,
		ComparedPath:         desired.ComparedPath,
		CredentialSourceType: desired.Entry.CredsSource,
		Differences:          []connectionComparisonDifference{},
		NotChecked:           []connectionComparisonNotChecked{},
		ValuesNeverReturned:  true,
	}

	client, _, ok := s.k8sClientAndNamespace()
	if !ok {
		return finishView(view, connectioncompare.Compare(connectioncompare.Request{
			ClusterName:  cluster,
			Namespace:    ns,
			CheckFailure: failNoHubClient,
		}))
	}

	live, getErr := client.CoreV1().Secrets(ns).Get(ctx, cluster, metav1.GetOptions{})
	liveFound := true
	switch {
	case apierrors.IsNotFound(getErr):
		live, liveFound = nil, false
	case getErr != nil:
		return finishView(view, connectioncompare.Compare(connectioncompare.Request{
			ClusterName:  cluster,
			Namespace:    ns,
			CheckFailure: failLiveRead,
		}))
	}

	policy := connectioncompare.Classify(connectioncompare.ClassifyInput{
		CredsSource:                  desired.Entry.CredsSource,
		ConnectionManagedBy:          desired.Entry.ConnectionManagedBy,
		BackendCanProvideStoredFacts: backendCanProvideStoredFacts,
		LiveSecretFound:              liveFound,
		LiveManagedBy:                liveManagedBy(live),
		LiveAdopted:                  live != nil && argosecrets.IsAdopted(live.Annotations),
	})

	req := connectioncompare.Request{
		ClusterName:         cluster,
		Namespace:           ns,
		Policy:              policy,
		Live:                live,
		LiveFound:           liveFound,
		DesiredAddonLabels:  desired.AddonLabels,
		AddonLabelsKnown:    desired.AddonLabelsKnown,
		ConnectivityCheckOn: s.clusterRecon.ConnectivityCheckEnabled(ctx),
	}
	if desired.Entry.ExpectedCredentialsRebuildableWithoutLiveSecret(backendCanProvideStoredFacts) {
		spec, credErr := s.storedSpecForRepair(desired.Entry)
		if credErr != nil {
			req.CheckFailure = failBackendRead
		} else {
			req.ExpectedSpec = spec
		}
	}
	return finishView(view, connectioncompare.Compare(req))
}

// storedSpecForRepair reads what the secrets BACKEND has stored for this cluster
// and builds the credential half of the EXPECTED Secret from it — the side a
// COMPARISON compares against.
//
// Its name says repair for historical reasons; the repair does not call it any
// more (R3-14). Its two callers are both checks: expectedConnectionSpec's twin in
// connection_comparison.go, and freshComparisonAfterRepair below — the re-check
// that runs after a write.
//
// # Why a check reads differently from a write, and why that is right
//
// A CHECK must create nothing while answering "what should this look like?". For
// a stored EKS payload the backend holds metadata and not a credential, so the
// honest answer is "there is no credential on the expected side", and the check
// reports the credential fields as not checked rather than minting a real
// short-lived sign-in token to compare against and throw away. A read with a
// blast radius is not a read.
//
// A WRITE asks a different question — "what do I need to WRITE?" — and a write
// needs credentials that actually work, so it mints. Its route is
// clusterreconciler.ConnectionCredentialSpecForWrite, which both writers share.
//
// Sharing one read between the two would either make the check mint or make the
// repair write a spec with no credential in it. The second is what happened: the
// repair took this route, got no token for an EKS payload, and the builder's
// precedence fell through to the exec shape — changing how ArgoCD signs in.
//
// So this one CANNOT MINT, and it never reads the live Secret. It goes through
// ClusterCredsRouter.StoredFactsIndependentOfArgoCDSecret, which only ever calls
// the read-only StoredConnectionFacts capability — there is no branch here or in
// the router that reaches GetCredentials. Do not widen it.
//
// The returned error is whatever the router gave back. It is NOT inspected by
// text, ever: a credentials-backend error is marked, so its Error() is one fixed
// safe sentence and there are no words in it to match on. Classification is by
// type through credsafe.
func (s *Server) storedSpecForRepair(entry models.ManagedClusterEntry) (*argosecrets.ClusterSecretSpec, error) {
	router := s.credsRouter()
	if router == nil {
		return nil, credsafe.Mark(errors.New("no credentials backend is configured"))
	}
	facts, err := router.StoredFactsIndependentOfArgoCDSecret(entry.CredentialLookupKey(), entry.CredsSource)
	if err != nil {
		return nil, err
	}
	return &argosecrets.ClusterSecretSpec{
		Name:     entry.Name,
		Server:   facts.Server,
		Region:   entry.Region,
		RoleARN:  entry.RoleARN,
		Token:    facts.Token,
		CertData: base64.StdEncoding.EncodeToString(facts.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(facts.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(facts.CAData),
	}, nil
}

// repairRefusal maps a write refusal to its status, its safe sentence, and the
// audit detail. By error TYPE, never by reading any error's words.
func repairRefusal(err error) (status int, message, auditDetail string) {
	switch {
	case errors.Is(err, argosecrets.ErrRepairOwnershipChanged):
		return http.StatusUnprocessableEntity, repairFailNotOwned, "refused: the connection is not Sharko's to change"
	case errors.Is(err, argosecrets.ErrRepairSecretMissing):
		return http.StatusNotFound, repairFailSecretGone, "refused: there is no connection to repair"
	case errors.Is(err, argosecrets.ErrRepairSecretChangedUnderneath):
		return http.StatusConflict, repairFailRaced, "refused: the connection changed while Sharko was repairing it"
	case errors.Is(err, clusterreconciler.ErrRepairNoClient):
		return http.StatusServiceUnavailable, repairFailNoHubClient, "refused: no in-cluster Kubernetes client"
	default:
		return http.StatusBadGateway, repairFailWrite, "failed: the write did not complete"
	}
}

// repairOutcomeDetail builds the audit detail for a completed repair. Counts and
// a status word — never a field value.
func repairOutcomeDetail(changed bool, fields int, comparisonStatus string) string {
	if !changed {
		return fmt.Sprintf("nothing needed changing; the fresh check says %s", comparisonStatus)
	}
	return fmt.Sprintf("repaired %d owned field(s); the fresh check says %s", fields, comparisonStatus)
}

// finishRepairAudit records the outcome onto the in-flight audit entry.
//
// The detail is always one of this file's own fixed phrases plus counts — never
// an error's text, and never anything read off a Secret.
func (s *Server) finishRepairAudit(r *http.Request, cluster, detail string) {
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_connection_repair",
		Resource: fmt.Sprintf("cluster:%s", cluster),
		Detail:   detail,
	})
}

// auditRepairCredentialFailure records a credential-backend failure.
//
// It writes its OWN entry rather than only enriching, because the flag that makes
// the audit log sanitize the text lives on audit.Entry and is set here, where the
// typed error is still alive. credsafe.Is is a type check — it never reads the
// error's words — and Log.Add then replaces Error with the fixed safe sentence,
// empties Detail, and clears the flag before anything is stored or streamed.
func (s *Server) auditRepairCredentialFailure(r *http.Request, cluster string, err error) {
	// Keep the request-scoped entry honest too, with no error text in it.
	s.finishRepairAudit(r, cluster, "failed: could not read the configured credentials source")

	if s == nil || s.auditLog == nil {
		return
	}
	user := r.Header.Get("X-Sharko-User")
	if user == "" {
		user = "anonymous"
	}
	s.auditLog.Add(audit.Entry{
		Timestamp:         time.Now().UTC(),
		Level:             "warn",
		Event:             "cluster_connection_repair",
		User:              user,
		Action:            "repair_connection",
		Resource:          fmt.Sprintf("cluster:%s", cluster),
		Source:            detectSource(r),
		Result:            "failure",
		Error:             err.Error(),
		CredentialFailure: credsafe.Is(err),
	})
}
