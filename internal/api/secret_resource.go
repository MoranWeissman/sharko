package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// secret_resource.go — "show me the actual Secret, as it is on the cluster
// right now, read-only" (S3, Managed Secrets page). Two endpoints, one for
// each kind of secret Sharko manages:
//
//   - GET /clusters/{name}/secret/resource
//     the cluster's ArgoCD connection Secret, read from the HUB's argocd
//     namespace (the same client + namespace the cluster reconciler uses).
//   - GET /clusters/{name}/addons/{addon}/secret/resource
//     one addon-values Secret, read from the REMOTE cluster it was pushed
//     to, over that cluster's own credentials.
//
// COST (S5) — READ THIS BEFORE "OPTIMISING" ANYTHING HERE.
//
// Each call is one live round trip to a cluster: fetch credentials, build a
// throwaway client, GET one Secret, throw the client away. That is fine
// because it happens ON A CLICK and only on a click. It must NEVER be:
//   - called while the Managed Secrets list renders (the list is built
//     entirely from data the server already has — see
//     system_managed_secrets.go's own header, which says the same thing),
//   - put on a timer, prefetched, or warmed,
//   - fanned out over the row set ("just load them all so the panel is
//     instant"). At 50 clusters x 10 addons that is 500 remote calls per
//     page view.
// If a future change needs many of these at once, that is a new design
// decision with a new shape (a batched, server-paced endpoint), not a loop
// around this handler.
//
// SECURITY (S4) — the whole point of this file.
//
// A Secret's VALUES never leave the server. Blanking is not a UI concern
// and is not done by the browser: newSecretResourceView below builds the
// response from a *corev1.Secret and copies ONLY key names out of
// .Data/.StringData, pairing each with the fixed blankedValue mask. The
// real bytes are never read, never measured, never hashed, never logged.
// The mask is a constant — it does not depend on the value in any way, so
// it cannot leak a length. ArgoCD's own resource view does exactly this
// (a Secret renders with its values replaced by asterisks), so this is a
// known-safe shape, not a new risk being invented.

// blankedValue is what every secret data key's value renders as. A fixed
// constant on purpose: anything derived from the real value — its length,
// a prefix, a hash, "(24 bytes)" — is a leak. Eight bullets, always,
// whatever the value was.
const blankedValue = "••••••••"

// annotationsThatEmbedTheObject lists annotation keys whose VALUE is a
// serialized copy of the object it is attached to — which, on a Secret,
// means the value carries the secret data too (base64'd, but that is
// encoding, not protection).
//
// kubectl writes last-applied-configuration on every `kubectl apply`, so
// this is not a theoretical case: any Secret a human applied by hand
// carries a full copy of itself, values included, in its own metadata.
// Showing annotations "as they are" without this gate would hand the
// browser every value this file exists to blank.
//
// The KEY is still shown (an operator should know the annotation is
// there); the value is replaced with the same mask the data keys get.
var annotationsThatEmbedTheObject = map[string]bool{
	"kubectl.kubernetes.io/last-applied-configuration": true,
}

// secretResourceKeyView is one data key of the live Secret. Value is
// ALWAYS blankedValue — the field exists so the response states plainly
// that the server blanked it, not so a future change has somewhere to put
// the real thing. There is no code path in this package that assigns
// anything else to it.
type secretResourceKeyView struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Path (P2-C2) is the secrets-store POINTER this key's value comes
	// from — a location, not a value; safe to show (the whole point of
	// this endpoint's own header comment: Sharko may describe the
	// delivery, never the secret). Populated ONLY on the addon-values
	// live-read response (handleGetAddonValuesSecretResource), which is
	// already behind the operator-gated secret.resource.read action — NEVER
	// on the list endpoint (buildAddonValuesSecretRows), which any logged-in
	// user can reach. Empty on the connection-secret live-read response,
	// which has no per-key store-pointer concept — a connection secret's
	// desired state lives at one FILE path, already carried on the row
	// (connectionSecretRow.ComparedPath), not per-key.
	Path string `json:"path,omitempty"`
}

// secretResourceLabelView is one label or annotation, as a sorted list
// rather than a map so the panel renders in a stable order.
type secretResourceLabelView struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Blanked is true when Value is the mask rather than the real text —
	// only ever set for an annotation that embeds a copy of the object
	// (see annotationsThatEmbedTheObject).
	Blanked bool `json:"blanked,omitempty"`
}

// secretResourceView is the response body for both endpoints: the live
// Secret as ArgoCD would show it, with every value blanked.
type secretResourceView struct {
	Kind       string `json:"kind"`        // always "Secret"
	APIVersion string `json:"api_version"` // always "v1"
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	// SecretType is the Kubernetes Secret type ("Opaque",
	// "kubernetes.io/tls", ...) — metadata, never content.
	SecretType string `json:"secret_type,omitempty"`
	// CreatedAt is the object's creationTimestamp in RFC3339, "" when the
	// cluster did not set one. The UI turns this into an age.
	CreatedAt   string                    `json:"created_at,omitempty"`
	Labels      []secretResourceLabelView `json:"labels"`
	Annotations []secretResourceLabelView `json:"annotations"`
	DataKeys    []secretResourceKeyView   `json:"data_keys"`
	// ReadFrom is a plain sentence naming where this object was read from,
	// so the panel never leaves a reader guessing which cluster they are
	// looking at.
	ReadFrom string `json:"read_from"`
	// ValuesBlanked is always true. It is in the body so the contract is
	// visible to anyone reading the API, not only to anyone reading this
	// file.
	ValuesBlanked bool `json:"values_blanked"`
}

// newSecretResourceView is THE blanking point. Every response body both
// handlers below return is built here, from a *corev1.Secret, and this
// function never reads sec.Data[k] or sec.StringData[k] — only their key
// names. Nothing downstream of here has access to the live object, so
// there is no second place a value could escape from.
//
// keyPaths (P2-C2) is the per-key secrets-store pointer map (data key →
// provider path, e.g. models.AddonSecretRef.Keys) — nil for the connection
// endpoint, def.Keys for the addon-values endpoint. A key with no entry in
// keyPaths simply gets an empty Path, same as before this parameter
// existed.
func newSecretResourceView(sec *corev1.Secret, readFrom string, keyPaths map[string]string) secretResourceView {
	view := secretResourceView{
		Kind:          "Secret",
		APIVersion:    "v1",
		Name:          sec.Name,
		Namespace:     sec.Namespace,
		SecretType:    string(sec.Type),
		Labels:        []secretResourceLabelView{},
		Annotations:   []secretResourceLabelView{},
		DataKeys:      []secretResourceKeyView{},
		ReadFrom:      readFrom,
		ValuesBlanked: true,
	}
	if !sec.CreationTimestamp.IsZero() {
		view.CreatedAt = sec.CreationTimestamp.UTC().Format(time.RFC3339)
	}

	// Labels are shown in full and that is deliberate: on a cluster
	// connection Secret the addon labels ARE the useful content — they
	// decide which addons run on that cluster — and they are not secret.
	for _, k := range sortedKeys(sec.Labels) {
		view.Labels = append(view.Labels, secretResourceLabelView{Key: k, Value: sec.Labels[k]})
	}

	for _, k := range sortedKeys(sec.Annotations) {
		if annotationsThatEmbedTheObject[k] {
			view.Annotations = append(view.Annotations, secretResourceLabelView{
				Key: k, Value: blankedValue, Blanked: true,
			})
			continue
		}
		view.Annotations = append(view.Annotations, secretResourceLabelView{Key: k, Value: sec.Annotations[k]})
	}

	// Key NAMES only. Both maps are walked because StringData is a valid
	// place for a key name to appear on an object handed to us; the value
	// side of either map is never touched.
	names := make(map[string]struct{}, len(sec.Data)+len(sec.StringData))
	for k := range sec.Data {
		names[k] = struct{}{}
	}
	for k := range sec.StringData {
		names[k] = struct{}{}
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		view.DataKeys = append(view.DataKeys, secretResourceKeyView{Key: k, Value: blankedValue, Path: keyPaths[k]})
	}

	return view
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readFailureSentence maps a failed live read to ONE safe, pre-written
// sentence. Same choice addonValuesSecretCheckFailureSentence already
// makes for the reconciler's own errors, and for the same reason: the
// error we are handed can wrap text from a credentials provider SDK, and
// this project has already had a near-miss where provider error text was
// about to be rendered straight back to a browser. Categorise by WHICH
// STEP failed — never render the error's own text — and there is nothing
// for a misbehaving SDK to smuggle out.
//
// A failure never falls back to stale or invented content: the handler
// returns this sentence and no view at all.
func readFailureSentence(step string, cluster string, err error) (int, string) {
	switch {
	case apierrors.IsNotFound(err):
		return http.StatusNotFound, fmt.Sprintf("This secret does not exist on cluster %q right now.", cluster)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return http.StatusBadGateway, fmt.Sprintf("Sharko is not allowed to read this secret on cluster %q.", cluster)
	}
	switch step {
	case "credentials":
		return http.StatusBadGateway, fmt.Sprintf("Sharko couldn't get credentials for cluster %q.", cluster)
	case "connect":
		return http.StatusBadGateway, fmt.Sprintf("Sharko couldn't connect to cluster %q.", cluster)
	default:
		return http.StatusBadGateway, fmt.Sprintf("Sharko couldn't read this secret from cluster %q.", cluster)
	}
}

// logReadFailure records that a read failed, with the step that failed and
// the object it was for — and NOT the error's own text. Same reasoning as
// readFailureSentence: the wrapped error can carry provider SDK text, and
// a log line is a place that text would be kept. The step name plus the
// cluster/namespace/name is enough to find the matching entry in the
// credentials provider's or the cluster's own logs.
func logReadFailure(step, cluster, namespace, name string) {
	slog.Warn("[secret-resource] could not read the live secret",
		"step", step, "cluster", cluster, "namespace", namespace, "secret", name)
}

// remoteClientForCluster resolves a read-only Kubernetes client for one
// registered cluster: fetch its credentials, build a throwaway client,
// hand it back. The caller discards it after one Get — no persistent
// connection, the same connect/operate/disconnect shape
// internal/remoteclient has always used.
//
// Demo mode replaces this whole function via SetDemoRemoteClusterClient
// (there are no real clusters there, and a real dial against a fake
// kubeconfig would just hang until the 30s client timeout).
func (s *Server) remoteClientForCluster(ctx context.Context, cluster string) (kubernetes.Interface, string, error) {
	if fn := s.demoRemoteClusterClientFn; fn != nil {
		client, err := fn(ctx, cluster)
		return client, "connect", err
	}
	if s.credProvider() == nil {
		return nil, "credentials", fmt.Errorf("no credentials provider configured")
	}
	creds, err := s.fetchClusterCredentials(ctx, cluster)
	if err != nil {
		return nil, "credentials", err
	}
	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		return nil, "connect", err
	}
	return client, "", nil
}

// handleGetConnectionSecretResource godoc
//
// @Summary Read the live cluster connection Secret, values blanked
// @Description Reads the named cluster's ArgoCD connection Secret from the hub's argocd namespace as it is right now, and returns it for display: kind, name, namespace, labels, annotations, age, secret type, and the data KEY NAMES with every value blanked server-side. The addon labels are shown in full — they are not secret and they are what decides which addons run on that cluster. Values never reach the browser: the response is built from key names only (see internal/api/secret_resource.go). One live read per call, on click only — never while a list renders, never on a timer, never fanned out.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} secretResourceView "The live Secret, values blanked"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "This secret does not exist right now"
// @Failure 502 {object} map[string]interface{} "Sharko could not read the secret"
// @Failure 503 {object} map[string]interface{} "Sharko has no Kubernetes client for its own cluster on this server"
// @Router /clusters/{name}/secret/resource [get]
func (s *Server) handleGetConnectionSecretResource(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "secret.resource.read") {
		return
	}

	cluster := r.PathValue("name")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	client, ns, ok := s.k8sClientAndNamespace()
	if !ok {
		writeError(w, http.StatusServiceUnavailable,
			"Sharko is not connected to its own cluster on this server, so it cannot read this secret.")
		return
	}

	// The connection Secret's name always equals the cluster's name — the
	// same deterministic fact buildConnectionSecretRows uses to fill in the
	// row this panel opened from.
	sec, err := client.CoreV1().Secrets(ns).Get(r.Context(), cluster, metav1.GetOptions{})
	if err != nil {
		logReadFailure("get", cluster, ns, cluster)
		status, msg := readFailureSentence("get", cluster, err)
		writeError(w, status, msg)
		return
	}

	// A connection secret has no per-key store pointer — its desired state
	// lives at one FILE path, already on the row (P2-C1's ComparedPath) —
	// so keyPaths is nil here (P2-C2).
	writeJSON(w, http.StatusOK, newSecretResourceView(sec,
		fmt.Sprintf("Sharko's own cluster, namespace %q", ns), nil))
}

// handleGetAddonValuesSecretResource godoc
//
// @Summary Read the live addon values Secret on a cluster, values blanked
// @Description Reads one addon's values Secret from the remote cluster it was pushed to, as it is right now, and returns it for display: kind, name, namespace, labels, annotations, age, secret type, and the data KEY NAMES with every value blanked server-side. Values never reach the browser: the response is built from key names only (see internal/api/secret_resource.go). One live read per call, on click only — never while a list renders, never on a timer, never fanned out.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Success 200 {object} secretResourceView "The live Secret, values blanked"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "No values secret is defined for this addon, or it does not exist on the cluster right now"
// @Failure 502 {object} map[string]interface{} "Sharko could not read the secret"
// @Router /clusters/{name}/addons/{addon}/secret/resource [get]
func (s *Server) handleGetAddonValuesSecretResource(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "secret.resource.read") {
		return
	}

	cluster := r.PathValue("name")
	addon := r.PathValue("addon")
	if cluster == "" || addon == "" {
		writeError(w, http.StatusBadRequest, "cluster name and addon name are required")
		return
	}

	// Where the Secret lives comes from the same registered definition the
	// row itself was built from (buildAddonValuesSecretRows) — one source
	// of truth for "which Secret is this row about".
	s.addonSecretDefsMu.RLock()
	def, defined := s.addonSecretDefs[addon]
	s.addonSecretDefsMu.RUnlock()
	if !defined || def.SecretName == "" || def.Namespace == "" {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("Sharko has no values secret defined for addon %q, so there is nothing to show.", addon))
		return
	}

	client, step, err := s.remoteClientForCluster(r.Context(), cluster)
	if err != nil {
		logReadFailure(step, cluster, def.Namespace, def.SecretName)
		status, msg := readFailureSentence(step, cluster, err)
		writeError(w, status, msg)
		return
	}

	sec, err := client.CoreV1().Secrets(def.Namespace).Get(r.Context(), def.SecretName, metav1.GetOptions{})
	if err != nil {
		logReadFailure("get", cluster, def.Namespace, def.SecretName)
		status, msg := readFailureSentence("get", cluster, err)
		writeError(w, status, msg)
		return
	}

	// P2-C2: the per-key store pointer list — key name -> provider path.
	// This is the ONE place it ever ships: this endpoint is already
	// operator-gated (secret.resource.read, checked at the top of this
	// handler), unlike the list endpoint any logged-in user can reach.
	writeJSON(w, http.StatusOK, newSecretResourceView(sec,
		fmt.Sprintf("cluster %q, namespace %q", cluster, def.Namespace), def.Keys))
}

// secretResourceViewHasNoValue is a belt-and-braces assertion used by the
// tests in secret_resource_test.go: it reports the first field of a built
// view that carries anything other than the fixed mask where a value would
// be. Kept in non-test code on purpose — a reviewer reading this file can
// see exactly what "no value escapes" is being checked against, and it
// cannot drift away from the view type it guards.
func secretResourceViewHasNoValue(view secretResourceView) (field string, ok bool) {
	for _, k := range view.DataKeys {
		if k.Value != blankedValue {
			return "data_keys[" + k.Key + "]", false
		}
	}
	for _, a := range view.Annotations {
		if annotationsThatEmbedTheObject[a.Key] && a.Value != blankedValue {
			return "annotations[" + a.Key + "]", false
		}
	}
	if !view.ValuesBlanked {
		return "values_blanked", false
	}
	return "", true
}

// containsAny is a small helper for the tests' "does this response body
// contain any of these secret values" sweep. Declared here so the check
// lives next to the thing it guards.
func containsAny(haystack string, needles []string) (string, bool) {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return n, true
		}
	}
	return "", false
}
