package api

// connection_comparison.go — GET /clusters/{name}/connection-comparison.
//
// "Does this cluster's ArgoCD connection look the way Sharko means it to look,
// and how much of that can Sharko honestly check?"
//
// READ-ONLY. This handler performs no write of any kind: no Kubernetes write,
// no git write, no secrets-backend write, no repair. Every call it makes is a
// read (one git file read at a pinned commit, one Secret Get, at most one
// secrets-backend fetch), and the packages it calls into for the answer
// (internal/connectioncompare) have no client at all.
//
// WHY A NEW ENDPOINT INSTEAD OF WIDENING THE OLD ONE.
// GET /clusters/{name}/comparison already exists and returns an untyped
// map[string]interface{} (internal/api/clusters.go). Widening it would put new
// keys into a shape nothing type-checks, in front of callers that already read
// it. A separate typed response can be checked by the compiler and by the
// tests in this package, and the old endpoint keeps working exactly as it did.
//
// THE REQUEST IDENTIFIES A CLUSTER AND NOTHING ELSE.
// There is one input: the cluster name in the path. No body is read. No query
// parameter is read. There is deliberately no way to pass a candidate value, an
// expected manifest, a hash, a secrets-backend path, a destination override or
// a namespace — the namespace comes from the running reconciler. That is what
// stops this from becoming a way to guess at a value: a caller cannot ask "is
// the token X?", only "does the connection match the Git-defined connection?", and
// answer to that is the same no matter what else they put on the request.
// TestConnectionComparison_IsNotAGuessingOracle pins it.
//
// NEITHER SIDE OF A SENSITIVE FIELD EVER LEAVES.
// The credential blob is compared in memory. What comes back for it is the
// field path, one of same / different / missing / unexpected, and
// sensitive: true. No value, no base64 of it, no hash of it, no length of it,
// no prefix or suffix of it, and no mask whose length tracks it. The absence is
// structural — see connectioncompare.Difference.MarshalJSON.
//
// ROLE GATE. secret.resource.read (operator or higher), the same action the
// live connection-Secret read uses. This endpoint reads the live Secret too, so
// it belongs on the same side of the line. internal/authz/authz.go's own
// comment on that action says the open-to-viewers /comparison endpoint "is the
// wrong precedent to copy here" — this follows that.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// connectionComparisonTimeout caps the whole check. Three reads at worst (git,
// the hub Secret, the secrets backend), on a click.
const connectionComparisonTimeout = 20 * time.Second

// Fixed, safe failure sentences. Every one of them is a literal here: a
// provider error, a git error or a Kubernetes error is never passed through,
// because those can carry credential material in their text and this project
// has already had a near-miss on exactly that.
const (
	failNoReconciler     = "The part of Sharko that manages cluster connections is not running on this server, so it cannot check this connection."
	failNoGitConnection  = "Sharko is not connected to a Git repository right now, so it cannot see what this connection should look like."
	failNoHubClient      = "Sharko is not connected to its own cluster on this server, so it cannot read this connection."
	failGitRead          = "Sharko could not read this cluster's record from Git, so it cannot tell what the connection should look like. Check the Git connection and try again."
	failLiveRead         = "Sharko could not read this cluster's connection from its own cluster, so the check did not finish. Try again in a moment."
	failBackendRead      = "Sharko could not read this cluster's configured credentials source from the secrets backend, so it could not work out what the connection should look like. Check the secrets backend connection and try again."
	failNotManaged       = "This cluster has no entry in the Git-managed cluster list, so Sharko has nothing to compare its connection against."
	failCredsUnavailable = "Sharko could not read this cluster's configured credentials source, so the check did not finish."

	// The two sentences below used to be typed INLINE inside
	// internal/connectioncompare/compare.go, which is a file no catalog guard
	// and no inline-prose sweep read. So they shipped to a person's screen
	// from outside the contract that is supposed to own every sentence on this
	// surface. They live here now, with the other seven, for the same reason
	// limitReasonCommitUnknown does.

	// failAddonLabelsUnknown is byte-for-byte the sentence compare.go used to
	// type inline. Nothing about it was wrong; only its address was.
	failAddonLabelsUnknown = "Sharko could not read which addons should be on for this cluster, so it cannot tell whether this connection's labels are right. Check that the cluster's addon file is readable in Git, then check again."

	// failExpectedBuild is the one sentence in this story that CHANGED.
	//
	// It used to end "Check again in a moment." — character-for-character the
	// promise that was already removed from repairFailSecretGone. Here the
	// problem is not the timeframe, it is the advice: this failure is a JSON
	// marshalling failure inside argosecrets.BuildClusterSecret, over Sharko's
	// own struct. The same inputs produce the same failure every time, so
	// "check again in a moment" tells the reader to do something that cannot
	// possibly help, and sends them looking at their cluster and their Git
	// repository for a fault that is in neither.
	//
	// This does NOT contradict the deliberate exception kept for
	// repairFailWrite ("Try again in a moment.") — a failed WRITE genuinely
	// can succeed on a second attempt, so telling a person to retry is true
	// there. See TestConnectionSentences_RepairRefusalsAreExact, which pins
	// that exception on purpose.
	failExpectedBuild = "Sharko could not work out what this cluster's connection should look like, so there is nothing to compare against. This is a fault in Sharko itself — nothing on the cluster or in Git needs changing."

	// failCheckDidNotFinish is the last resort, for a typed reason that has no
	// sentence of its own. Unreachable while the exhaustiveness guard passes —
	// it exists so that if it ever IS reached, the reader gets a true sentence
	// rather than a check_failed answer with a blank explanation.
	failCheckDidNotFinish = "Sharko could not finish checking this connection."
)

// connectionFailureSentences is the ONE map from a typed check failure to the
// words a person reads.
//
// # Why this map exists at all
//
// The product owner's ruling: "presentation structure must follow typed facts,
// never equality between human sentences." Before this map, the sentence WAS
// the fact — connectioncompare.Result carried the whole paragraph and the
// connection page switched on it, so a copy edit silently changed which branch
// of the page ran and which failed step it named.
//
// Now the fact travels typed and the words are looked up here, once, at the
// edge. Routing keys on the fact; a sentence can be rewritten freely.
//
// # It is exhaustive in both directions, and that is checked
//
// TestConnectionFailure_EveryTypedReasonHasASentence walks
// connectioncompare.CheckFailures() and fails BY NAME on a reason with no
// sentence AND on a sentence whose reason no longer exists. Neither direction
// is a count.
var connectionFailureSentences = map[connectioncompare.CheckFailure]string{
	connectioncompare.CheckFailureNoReconciler:           failNoReconciler,
	connectioncompare.CheckFailureNoGitConnection:        failNoGitConnection,
	connectioncompare.CheckFailureNoHubClient:            failNoHubClient,
	connectioncompare.CheckFailureGitRead:                failGitRead,
	connectioncompare.CheckFailureLiveRead:               failLiveRead,
	connectioncompare.CheckFailureBackendRead:            failBackendRead,
	connectioncompare.CheckFailureNotManaged:             failNotManaged,
	connectioncompare.CheckFailureCredentialsUnavailable: failCredsUnavailable,
	connectioncompare.CheckFailureAddonLabelsUnknown:     failAddonLabelsUnknown,
	connectioncompare.CheckFailureExpectedBuild:          failExpectedBuild,
}

// connectionFailureSentence is what a person is shown for a typed check
// failure. The empty reason means nothing failed, so it has no words.
//
// An UNDECLARED reason falls back to failCheckDidNotFinish rather than to an
// empty string: a check_failed answer with no explanation at all would be the
// worst of both — a page that says something went wrong and refuses to say
// what. It cannot happen (the guard above fails first), and if it ever does
// the reader still gets a sentence that is TRUE, just narrow.
func connectionFailureSentence(f connectioncompare.CheckFailure) string {
	if f == connectioncompare.CheckFailureNone {
		return ""
	}
	if sentence, ok := connectionFailureSentences[f]; ok {
		return sentence
	}
	return failCheckDidNotFinish
}

// limitReasonCommitUnknown is the R3-8 repair withdrawal sentence.
//
// IT USED TO BE TYPED INLINE, in the middle of compareClusterConnection. A
// sentence that is not a constant is invisible to the message catalog and to
// the guard that keeps the catalog complete, so it would have shipped to a
// person's screen from outside the contract that is supposed to own it — and
// a copy of it is already hand-typed in a test fixture. The value is
// byte-for-byte what the handler assigned before.
const limitReasonCommitUnknown = "Sharko cannot tell which commit your Git branch is on, so it will not offer to rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching."

// connectionComparisonDifference is one field that did not match.
//
// Expected and Live are pointers so a sensitive field can omit them entirely.
// This mirrors connectioncompare.Difference, which is where the omission is
// actually enforced; the type is repeated here only so swagger documents the
// wire shape.
type connectionComparisonDifference struct {
	// Path is the field, e.g. "data.server" or
	// "metadata.labels[addons.sharko.dev/datadog]".
	Path string `json:"path"`
	// Status is same, different, missing, or unexpected.
	Status string `json:"status"`
	// Sensitive marks a field holding sign-in details. When true there is no
	// expected value and no live value in this object at all.
	Sensitive bool `json:"sensitive,omitempty"`
	// Expected is what Sharko means the field to be. Absent on a sensitive
	// field, always.
	Expected *string `json:"expected,omitempty"`
	// Live is what the connection carries. Absent on a sensitive field,
	// always.
	Live *string `json:"live,omitempty"`
}

// connectionComparisonNotChecked is one field inside the nominal scope Sharko
// deliberately did not check, and the plain reason why.
type connectionComparisonNotChecked struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// connectionComparisonView is the response body.
//
// Nothing here can carry credential material. The value-bearing fields are the
// cluster name, the Secret type, label values, the API server address, data key
// names, and the fixed sentences in this file — and label values are already
// returned in full by the live connection-Secret read for the reasons that
// endpoint documents (Kubernetes caps a label value at 63 characters of a
// restricted character set, and the addon labels are the useful content).
type connectionComparisonView struct {
	// Cluster is the cluster this answer is about.
	Cluster string `json:"cluster"`

	// Status is synced, out_of_sync, missing, check_failed, ownership_conflict
	// or limited. synced means every field inside the scope was checked
	// successfully AND matched; it is only ever possible at full scope.
	// check_failed means Sharko could not finish, and is never turned into
	// out_of_sync or synced.
	Status string `json:"status"`

	// Scope is how much of the connection was checked: full, limited,
	// addon_labels_only, or ownership_conflict.
	Scope string `json:"scope"`

	// OwnershipMode is which of the seven kinds of connection this is.
	OwnershipMode string `json:"ownership_mode"`

	// LimitReason explains a scope narrower than full, in plain words. Empty
	// at full scope.
	LimitReason string `json:"limit_reason,omitempty"`

	// FailureReason explains a check_failed answer. Empty otherwise.
	FailureReason string `json:"failure_reason,omitempty"`

	// CheckedAt is when this check ran, RFC3339.
	CheckedAt string `json:"checked_at"`

	// Branch is the configured git branch. Sharko does not discover a default
	// branch.
	Branch string `json:"branch"`

	// ComparedCommit is the commit every file in this check was read at.
	// Empty when the git provider cannot report a commit — an empty value
	// says "Sharko does not know", never a guessed or stale commit.
	ComparedCommit string `json:"compared_commit,omitempty"`

	// ComparedPath is the git file the desired state was read from.
	ComparedPath string `json:"compared_path,omitempty"`

	// CredentialSourceType is where this cluster's sign-in details are kept:
	// inline-kubeconfig, secret-kubeconfig, eks-token, or empty when the
	// record predates the field. It is a KIND, never a path into a store and
	// never a value.
	CredentialSourceType string `json:"credential_source_type,omitempty"`

	// Differences are the fields that did not match, in a fixed order.
	Differences []connectionComparisonDifference `json:"differences"`

	// NotChecked are fields inside the nominal scope Sharko deliberately did
	// not check, each with a reason.
	NotChecked []connectionComparisonNotChecked `json:"not_checked"`

	// CheckedFieldCount is how many fields were compared. A count of FIELDS —
	// not of bytes, characters or anything else measured off a value.
	CheckedFieldCount int `json:"checked_field_count"`

	// RepairAvailable says whether a repair could be offered for this kind of
	// connection. This endpoint never repairs anything.
	RepairAvailable bool `json:"repair_available"`

	// RepairScope is exactly what a repair would be allowed to touch: none,
	// addon_labels_only, or full_connection.
	RepairScope string `json:"repair_scope"`

	// ValuesNeverReturned is always true. It is in the body so the promise is
	// visible to anyone reading the API, not only to anyone reading this file.
	ValuesNeverReturned bool `json:"values_never_returned"`

	// policy is the classification this check ran under, kept whole as a join
	// for the reconciliation endpoint. UNEXPORTED, like the two provenance
	// fields below: encoding/json never touches it, so the comparison
	// endpoint's wire shape is unchanged.
	//
	// It exists because the exported fields are a lossy summary, and two
	// places on the connection page promise Sharko will create a missing
	// Secret. OwnershipMode alone cannot decide that promise: Classify hands
	// out backend_stored_credentials and eks_token BOTH when the secrets
	// backend can be read and when it cannot, and the two situations deserve
	// opposite answers. RepairScope cannot decide it either — the comparison
	// sets it to none on a missing Secret, which is correct for a repair
	// offer and useless as a capability. The Policy carries the real answer,
	// so both promises are keyed on the same one thing.
	//
	// A zero Policy classifies as unknown_source, which promises nothing.
	// That is the right value for the early check_failed returns, which never
	// reach a Classify call.
	policy connectioncompare.Policy

	// failure is WHY a check_failed answer could not finish, as a typed fact.
	// UNEXPORTED, like policy: encoding/json never touches it, so this
	// endpoint's wire shape is unchanged.
	//
	// FailureReason above is DERIVED FROM THIS AND NEVER AUTHORED. finishView
	// is the only place in the server that assigns it, and it assigns it from
	// connectionFailureSentence(failure), so the words and the fact cannot
	// disagree. That note deliberately does NOT live on FailureReason's own doc
	// comment: swaggo copies an exported field's doc comment into the published
	// API description, and naming Sharko's internal functions there tells an API
	// consumer about machinery it cannot see.
	//
	// THIS IS WHAT THE PAGE ROUTES ON, and FailureReason is what it shows.
	// They used to be one field — the whole paragraph — and the connection
	// page's condition builder switched on it with case values that were
	// sentences a person reads. Rewording one of those sentences would have
	// dropped the page into its default branch and named the wrong failed
	// step, with nothing to notice: no compiler error, no failing test.
	//
	// The product owner's ruling is the reason both exist now: "presentation
	// structure must follow typed facts, never equality between human
	// sentences." connection_sentence_routing_test.go fails on any new
	// comparison against a sentence constant, so the old shape cannot come
	// back quietly.
	failure connectioncompare.CheckFailure

	// liveAppliedRevision and liveWrittenAt are joins for the reconciliation
	// endpoint (connection_reconciliation.go): the live Secret's
	// sharko.dev/revision and sharko.dev/written-at provenance annotations,
	// read during the one Secret Get this check already performs. They are
	// UNEXPORTED on purpose — encoding/json never serialises them, so the
	// comparison endpoint's wire shape stays byte-for-byte what it was.
	// Empty when the Secret is missing or was never stamped; an empty value
	// means "Sharko does not know", never a guessed one.
	liveAppliedRevision string
	liveWrittenAt       string

	// liveSecretFound and liveOwnershipMarker are the two live facts about
	// WHO the connection Secret says it belongs to, kept from the same Secret
	// Get this check already performs. UNEXPORTED like the joins above —
	// encoding/json never touches them, so this endpoint's wire shape is
	// unchanged.
	//
	// They are here because the ownership MODE is not the same fact as the
	// ownership MARKER, and the page was stating the mode as though it were
	// the marker. Classify's foreign-owner rule needs a marker that is
	// non-empty AND not Sharko's, so a Secret carrying NO managed-by label at
	// all falls through to the ordinary Sharko-managed path — and the page
	// then rendered "Sharko owns this connection Secret." as a passed check
	// about a Secret that says no such thing, promised an automatic label
	// re-apply nothing performs (the reconciler lists only marked Secrets and
	// refuses unmarked ones as Adopt territory), and would offer a repair
	// argosecrets.Manager.RepairOwnedConnection refuses on the same marker.
	//
	// liveOwnershipMarker is a LABEL VALUE from Sharko's own vocabulary — the
	// same string the ownership model is built on. It is never rendered; only
	// liveOwnershipMarkerRefusesWrite reads it.
	liveSecretFound     bool
	liveOwnershipMarker string
}

// liveOwnershipMarkerRefusesWrite reports the EXACT condition
// argosecrets.Manager.RepairOwnedConnection refuses a write on: a live
// connection Secret is there and its app.kubernetes.io/managed-by marker is
// not Sharko's own.
//
// It is false when there is no live Secret at all — there is nothing to
// refuse, and a missing Secret is its own state with its own answers.
//
// This does not weaken that gate and does not add a second one. The gate
// stays exactly as strict; this is the page asking the gate's own question so
// it cannot claim, offer or promise something the gate will shut.
func (v connectionComparisonView) liveOwnershipMarkerRefusesWrite() bool {
	return v.liveSecretFound && v.liveOwnershipMarker != argosecrets.ManagedByValue
}

// handleGetConnectionComparison godoc
//
// @Summary Compare a cluster's ArgoCD connection with the Git-defined connection
// @Description Read-only. Works out what the named cluster's ArgoCD connection Secret should look like — the connection Git defines, with credential values resolved, where one exists, from the cluster's configured credentials source held outside the connection — and compares it with the connection that is actually there. For an EKS cluster that source is the cluster's own details rather than a reusable sign-in credential, and the check creates no sign-in tokens. Writes nothing. The answer says how much of the connection could honestly be checked: a cluster whose sign-in details only exist inside the connection itself, or whose record does not say where they are kept, is reported with a narrower scope rather than being compared against itself. Sign-in details are compared in memory and neither side is ever returned: a sensitive field comes back with its path, one of same/different/missing/unexpected, and sensitive true, with no expected value and no live value present at all. The request identifies a cluster and nothing else — no candidate value, no expected manifest, no hash, no backend path, no namespace.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} connectionComparisonView "The comparison result"
// @Failure 400 {object} map[string]interface{} "Cluster name is required"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "This cluster is not in the Git-managed cluster list"
// @Failure 503 {object} map[string]interface{} "Sharko is missing something it needs to run the check"
// @Router /clusters/{name}/connection-comparison [get]
func (s *Server) handleGetConnectionComparison(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "secret.resource.read") {
		return
	}

	cluster := r.PathValue("name")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	view, refusal := s.compareClusterConnection(r.Context(), cluster)
	if refusal != nil {
		writeError(w, refusal.httpStatus, refusal.sentence)
		return
	}

	// The human check and the background loop (connection_credential_check.go)
	// feed the SAME per-cluster store, so the fleet page never lags a check a
	// person just ran. This is a store update, not a write to any cluster.
	s.connCredChecks.record(cluster, view)

	s.auditSecretResourceRead(r, fmt.Sprintf("cluster:%s", cluster),
		"compared the cluster connection with the Git-defined connection", auditResultFor(connectioncompare.Status(view.Status)))
	writeJSON(w, http.StatusOK, view)
}

// connectionComparisonRefusal is a whole-check refusal: the comparison could
// not even start (a missing server-side dependency) or the cluster is not in
// the git-managed list. The sentence is always one of the fixed literals at
// the top of this file — never provider or Kubernetes error text.
type connectionComparisonRefusal struct {
	httpStatus int
	sentence   string
	// notManaged marks the "no entry in git" refusal (a 404 for the
	// endpoint; a silent skip for the background loop — a cluster that
	// left the list has no row to annotate).
	notManaged bool
}

// compareClusterConnection is the read-only comparison core for ONE cluster.
//
// TWO CALLERS, ONE ANSWER. The GET /clusters/{name}/connection-comparison
// handler above and the background credential-check loop
// (connection_credential_check.go) both call this exact method, so a button
// check and a background check can never disagree about a cluster. Everything
// HTTP-specific (the authz gate, status codes, the per-request audit entry)
// stays in the handler; everything cadence-specific stays in the loop.
//
// READ-ONLY, INHERITED BY BOTH CALLERS. Every call this makes is a read (one
// git file read at a pinned commit, one Secret Get, at most one read-only
// secrets-backend fetch). It never calls the write path's
// clusterreconciler.ConnectionCredentialSpecForWrite — that builder can mint
// an EKS sign-in token; the read here goes through
// StoredFactsIndependentOfArgoCDSecret, which cannot (see
// expectedConnectionSpec below).
func (s *Server) compareClusterConnection(parent context.Context, cluster string) (connectionComparisonView, *connectionComparisonRefusal) {
	if s.clusterRecon == nil {
		return connectionComparisonView{}, &connectionComparisonRefusal{httpStatus: http.StatusServiceUnavailable, sentence: failNoReconciler}
	}
	if s.clusterRecon.GitProviderForRead() == nil {
		return connectionComparisonView{}, &connectionComparisonRefusal{httpStatus: http.StatusServiceUnavailable, sentence: failNoGitConnection}
	}
	client, ns, ok := s.k8sClientAndNamespace()
	if !ok {
		return connectionComparisonView{}, &connectionComparisonRefusal{httpStatus: http.StatusServiceUnavailable, sentence: failNoHubClient}
	}

	ctx, cancel := context.WithTimeout(parent, connectionComparisonTimeout)
	defer cancel()

	branch := s.clusterRecon.Branch()

	// Pin everything to one commit. A commit landing between two file reads
	// would give a mixed picture of two different desired states, and a
	// comparison must not report a difference it invented itself. An empty
	// revision means the git provider cannot report one — the reads then use
	// the configured branch and the response reports no commit, which is
	// honest about what Sharko knows. No default-branch discovery happens
	// anywhere on this path.
	revision := s.clusterRecon.ResolveComparedRevision(ctx)
	readRef := branch
	if revision != "" {
		readRef = revision
	}

	view := connectionComparisonView{
		Cluster:             cluster,
		CheckedAt:           time.Now().UTC().Format(time.RFC3339),
		Branch:              branch,
		ComparedCommit:      revision,
		Differences:         []connectionComparisonDifference{},
		NotChecked:          []connectionComparisonNotChecked{},
		ValuesNeverReturned: true,
	}

	desired, desiredErr := s.clusterRecon.DesiredConnectionStateAt(ctx, cluster, readRef)
	view.ComparedPath = desired.ComparedPath
	if desiredErr != nil {
		// The git error's own text is not passed through — it can wrap
		// provider SDK text. The step and the cluster are logged instead,
		// which is enough to find the matching provider log entry.
		slog.Warn("[connection-comparison] could not read the desired state from git",
			"cluster", cluster, "branch", branch, "path", desired.ComparedPath)
		return finishView(view, connectioncompare.Compare(connectioncompare.Request{
			ClusterName:  cluster,
			Namespace:    ns,
			CheckFailure: connectioncompare.CheckFailureGitRead,
		})), nil
	}
	if !desired.Found {
		return connectionComparisonView{}, &connectionComparisonRefusal{httpStatus: http.StatusNotFound, sentence: failNotManaged, notManaged: true}
	}
	view.CredentialSourceType = desired.Entry.CredsSource

	// The live Secret. A missing one is an ANSWER (status missing); a failed
	// read is a check_failed and is never softened into a difference.
	live, getErr := client.CoreV1().Secrets(ns).Get(ctx, cluster, metav1.GetOptions{})
	liveFound := true
	switch {
	case apierrors.IsNotFound(getErr):
		live, liveFound = nil, false
	case getErr != nil:
		slog.Warn("[connection-comparison] could not read the live connection secret",
			"cluster", cluster, "namespace", ns)
		return finishView(view, connectioncompare.Compare(connectioncompare.Request{
			ClusterName:  cluster,
			Namespace:    ns,
			CheckFailure: connectioncompare.CheckFailureLiveRead,
		})), nil
	}
	if liveFound && live != nil {
		// Provenance joins for the reconciliation endpoint — same Get, no
		// extra read, never serialised on this endpoint (unexported fields).
		view.liveAppliedRevision = live.Annotations[clusterreconciler.AnnotationRevision]
		view.liveWrittenAt = live.Annotations[clusterreconciler.AnnotationWrittenAt]
	}

	// ONE ANSWER, ASKED ONCE, USED TWICE.
	//
	// "Can this backend tell Sharko what the connection should look like without
	// reading the live ArgoCD Secret?" The router owns that answer, because the
	// router is what performs the read — so the policy below and the read further
	// down cannot disagree about whether the credential half was checkable.
	//
	// This used to be `s.credProvider() != nil`, which is a WIDER question: an
	// ArgoCD fallback provider is a configured provider, and so is a backend with
	// no read-only stored-facts capability. Both of those made Classify hand a
	// secret-kubeconfig cluster full scope and a full-connection repair, while the
	// router then refused the read — so the response carried status limited next
	// to scope full, and step 3 would have inherited a full-repair offer it must
	// not have. TestConnectionComparison_ScopeNeverWiderThanTheAnswer pins that
	// combination as a failure.
	backendCanProvideStoredFacts := s.credsRouter().CanReadStoredFactsIndependentOfArgoCDSecret()
	liveMarker := liveManagedBy(live)
	policy := connectioncompare.Classify(connectioncompare.ClassifyInput{
		CredsSource:                  desired.Entry.CredsSource,
		ConnectionManagedBy:          desired.Entry.ConnectionManagedBy,
		BackendCanProvideStoredFacts: backendCanProvideStoredFacts,
		LiveSecretFound:              liveFound,
		LiveManagedBy:                liveMarker,
		LiveAdopted:                  live != nil && argosecrets.IsAdopted(live.Annotations),
	})
	// Carried whole to the reconciliation endpoint. See the policy field's
	// comment on connectionComparisonView: two creation promises on that page
	// key off it, and neither can be decided from the exported summary.
	view.policy = policy
	// The SAME two values Classify was just given, kept for the page. Assigned
	// from the one local so the classification and the page can never be
	// looking at different readings of the same Secret.
	view.liveSecretFound = liveFound
	view.liveOwnershipMarker = liveMarker

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

	// The expected credential spec, rebuilt WITHOUT the live Secret and
	// WITHOUT creating any credential.
	//
	// Only asked for when the mode says an independent rebuild is possible.
	// The predicate is models.ExpectedCredentialsRebuildableWithoutLiveSecret
	// — NOT CredentialsResolvable, which counts reading the live Secret back
	// as a valid route and would make this compare the Secret with itself. The
	// read itself goes through StoredFactsIndependentOfArgoCDSecret, which is a
	// read-only capability with no path to the EKS token mint and no fallback
	// to the live Secret, so neither a wrong answer from the predicate nor a
	// future edit here can produce a self-comparison or a minted credential.
	//
	// It is handed the SAME backendCanProvideStoredFacts the policy above was
	// given, from the same router call — that is the whole fix. Two different
	// answers here is exactly how the policy and the enforcement drifted apart.
	if desired.Entry.ExpectedCredentialsRebuildableWithoutLiveSecret(backendCanProvideStoredFacts) {
		spec, credFailure := s.expectedConnectionSpec(desired.Entry)
		if credFailure != connectioncompare.CheckFailureNone {
			req.CheckFailure = credFailure
		} else {
			req.ExpectedSpec = spec
		}
	}

	result := connectioncompare.Compare(req)

	// R3-8: when the git provider cannot report a commit (revision is empty),
	// the comparison may still run and report what it could check, but the
	// repair offer must be withdrawn. Sharko only rewrites a connection when it
	// can name the exact commit it is matching — otherwise a repair's
	// git-revision guard (R3-4) would always refuse, and offering the button
	// would mislead the user.
	if revision == "" && result.RepairAvailable {
		result.RepairAvailable = false
		result.RepairScope = connectioncompare.RepairScopeNone
		result.LimitReason = limitReasonCommitUnknown
	}

	return finishView(view, result), nil
}

// liveManagedBy reads the ownership label off the live Secret, nil-safe.
func liveManagedBy(live *corev1.Secret) string {
	if live == nil {
		return ""
	}
	return live.Labels[argosecrets.LabelManagedBy]
}

// auditResultFor maps a comparison status to the audit log's result word. A
// check that could not finish is a failure; everything else answered.
func auditResultFor(status connectioncompare.Status) string {
	if status == connectioncompare.StatusCheckFailed {
		return "failure"
	}
	return "success"
}

// expectedConnectionSpec rebuilds the credential half of the expected Secret
// from what the secrets backend has STORED — never from the live Secret, and
// never by creating a credential.
//
// THE READ CANNOT MINT. It goes through
// ClusterCredsRouter.StoredFactsIndependentOfArgoCDSecret, which only ever
// calls the read-only StoredConnectionFacts capability. There is no branch
// here, and none in the router, that reaches GetCredentials — so for an EKS
// cluster no STS sign-in token is created. That matters twice over: creating a
// real credential is a real risk, and the mode policy has already decided the
// credential blob is not comparable for that cluster, so the token would be
// created and thrown away. When the stored payload is EKS metadata, the facts
// come back with CredentialMintedPerFetch set and no credential at all, and the
// spec is built without one — the comparison then reports data.config as a field
// it did not check, which is the honest answer.
//
// It returns a TYPED reason rather than an error on failure: the backend's
// error can carry credential material in its text, so it is logged on the
// server side (without its text) and the caller turns the reason into one of
// two pre-written sentences.
func (s *Server) expectedConnectionSpec(entry models.ManagedClusterEntry) (*argosecrets.ClusterSecretSpec, connectioncompare.CheckFailure) {
	router := s.credsRouter()
	if router == nil {
		return nil, connectioncompare.CheckFailureCredentialsUnavailable
	}
	facts, err := router.StoredFactsIndependentOfArgoCDSecret(entry.CredentialLookupKey(), entry.CredsSource)
	if err != nil {
		if errors.Is(err, providers.ErrNoIndependentCredentialSource) {
			// The mode said a rebuild was possible and the router disagreed —
			// the router wins, because it knows what the backend actually is.
			// Not a failure: the comparison simply runs without the credential
			// half, at a narrower scope.
			slog.Info("[connection-comparison] no independent copy of this cluster's sign-in details — checking the rest of the connection only",
				"cluster", entry.Name)
			return nil, connectioncompare.CheckFailureNone
		}
		// The backend error's own text is never passed through or logged — a
		// provider error can carry credential material in its message.
		slog.Warn("[connection-comparison] could not read this cluster's configured credentials source",
			"cluster", entry.Name)
		return nil, connectioncompare.CheckFailureBackendRead
	}

	// The role ARN follows the write path's precedence: the cluster's own
	// entry, else the connection-level default the reconciler was given.
	roleARN := entry.RoleARN

	return &argosecrets.ClusterSecretSpec{
		Name:     entry.Name,
		Server:   facts.Server,
		Region:   entry.Region,
		RoleARN:  roleARN,
		Token:    facts.Token,
		CertData: base64.StdEncoding.EncodeToString(facts.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(facts.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(facts.CAData),
	}, connectioncompare.CheckFailureNone
}

// finishView copies a comparison result onto the response view.
//
// It is the one place the wire shape is filled in, and it copies field by field
// rather than embedding connectioncompare.Difference, so the JSON contract of
// this endpoint is written down in this package and cannot change underneath it
// when the internal type changes. Sensitive fields are copied WITHOUT either
// side — the internal type would drop them anyway when it marshalled, and doing
// it here as well means neither layer relies on the other for the rule.
func finishView(view connectionComparisonView, res connectioncompare.Result) connectionComparisonView {
	view.Status = string(res.Status)
	view.Scope = string(res.Scope)
	view.OwnershipMode = string(res.Mode)
	view.LimitReason = res.LimitReason
	// THE ONE PLACE the failure sentence is produced, and it is produced FROM
	// the typed reason. Everything that routes reads view.failure; everything
	// that displays reads view.FailureReason; neither can drift from the other
	// because there is only one assignment and it derives one from the other.
	view.failure = res.Failure
	view.FailureReason = connectionFailureSentence(res.Failure)
	view.CheckedFieldCount = res.CheckedFieldCount
	view.RepairAvailable = res.RepairAvailable
	view.RepairScope = string(res.RepairScope)

	view.Differences = make([]connectionComparisonDifference, 0, len(res.Differences))
	for _, d := range res.Differences {
		out := connectionComparisonDifference{
			Path:      d.Path,
			Status:    string(d.Status),
			Sensitive: d.Sensitive,
		}
		if !d.Sensitive {
			out.Expected = d.Expected
			out.Live = d.Live
		}
		view.Differences = append(view.Differences, out)
	}

	view.NotChecked = make([]connectionComparisonNotChecked, 0, len(res.NotChecked))
	for _, n := range res.NotChecked {
		view.NotChecked = append(view.NotChecked, connectionComparisonNotChecked{Path: n.Path, Reason: n.Reason})
	}
	return view
}
