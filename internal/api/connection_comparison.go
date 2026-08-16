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
// the token X?", only "does the connection match what Sharko intends?", and the
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
	failNoGitConnection  = "Sharko is not connected to a git repository right now, so it cannot see what this connection should look like."
	failNoHubClient      = "Sharko is not connected to its own cluster on this server, so it cannot read this connection."
	failGitRead          = "Sharko could not read this cluster's record from git, so it cannot tell what the connection should look like. Check the git connection and try again."
	failLiveRead         = "Sharko could not read this cluster's connection from its own cluster, so the check did not finish. Try again in a moment."
	failBackendRead      = "Sharko could not read this cluster's configured credentials source from the secrets backend, so it could not work out what the connection should look like. Check the secrets backend connection and try again."
	failNotManaged       = "This cluster has no entry in the git-managed cluster list, so Sharko has nothing to compare its connection against."
	failCredsUnavailable = "Sharko could not read this cluster's configured credentials source, so the check did not finish."
)

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
}

// handleGetConnectionComparison godoc
//
// @Summary Compare a cluster's ArgoCD connection with what Sharko intends
// @Description Read-only. Works out what the named cluster's ArgoCD connection Secret should look like — from git plus, where one exists, the cluster's configured credentials source held outside the connection — and compares it with the connection that is actually there. For an EKS cluster that source is the cluster's own details rather than a reusable sign-in credential, and the check creates no sign-in tokens. Writes nothing. The answer says how much of the connection could honestly be checked: a cluster whose sign-in details only exist inside the connection itself, or whose record does not say where they are kept, is reported with a narrower scope rather than being compared against itself. Sign-in details are compared in memory and neither side is ever returned: a sensitive field comes back with its path, one of same/different/missing/unexpected, and sensitive true, with no expected value and no live value present at all. The request identifies a cluster and nothing else — no candidate value, no expected manifest, no hash, no backend path, no namespace.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} connectionComparisonView "The comparison result"
// @Failure 400 {object} map[string]interface{} "Cluster name is required"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "This cluster is not in the git-managed cluster list"
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
		"compared the cluster connection with what Sharko intends", auditResultFor(connectioncompare.Status(view.Status)))
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
			CheckFailure: failGitRead,
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
			CheckFailure: failLiveRead,
		})), nil
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
		if credFailure != "" {
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
		result.LimitReason = "Sharko cannot tell which commit your git branch is on, so it will not offer to rewrite this connection. Sharko only makes this change when it can name the exact commit it is matching."
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
// It returns a fixed safe sentence rather than an error on failure: the
// backend's error can carry credential material in its text, so it is logged
// on the server side (without its text) and one of two pre-written sentences
// goes out.
func (s *Server) expectedConnectionSpec(entry models.ManagedClusterEntry) (*argosecrets.ClusterSecretSpec, string) {
	router := s.credsRouter()
	if router == nil {
		return nil, failCredsUnavailable
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
			return nil, ""
		}
		// The backend error's own text is never passed through or logged — a
		// provider error can carry credential material in its message.
		slog.Warn("[connection-comparison] could not read this cluster's configured credentials source",
			"cluster", entry.Name)
		return nil, failBackendRead
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
	}, ""
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
	view.FailureReason = res.FailureReason
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
