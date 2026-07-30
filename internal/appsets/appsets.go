// Package appsets is Sharko's READ-ONLY view of the ArgoCD ApplicationSets
// living in the argocd namespace (v4 Wave 2, Epic 6 — brownfield takeover).
//
// Why this package exists at all: taking over a cluster ArgoCD already
// manages means swapping the owner of that cluster's Secret. If some
// ApplicationSet selects that cluster on a label and Sharko changes the
// labels, the AppSet can stop matching — and an AppSet that stops matching
// a cluster normally DELETES the Applications it generated for it, which in
// turn prunes the workloads. Before anyone confirms a takeover we have to
// be able to say, in plain words, which ApplicationSets would delete things
// and which would not.
//
// The read is deliberately narrow. We look at:
//
//   - metadata.name / metadata.namespace
//   - spec.syncPolicy.preserveResourcesOnDeletion (bool)
//   - spec.syncPolicy.applicationsSync (string)
//   - the label selectors on any cluster generator
//     (spec.generators[].clusters.selector, including generators nested
//     inside matrix / merge / union generators)
//
// We NEVER read spec.template. That block is the user's own Application
// template — free-form, frequently full of Go templating, and none of
// Sharko's business. Reading it would turn "Sharko needs list access to
// ApplicationSets" into "Sharko reads your deployment templates", which is
// a much bigger promise than this feature needs. The parser below has no
// code path that touches it.
//
// RBAC: this package is the ONLY reason the Sharko chart asks for
// get/list/watch on applicationsets.argoproj.io. That is exactly one new
// permission, and it is read-only.
package appsets

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// GroupVersionResource is the ApplicationSet CRD coordinate. ArgoCD has
// shipped ApplicationSets under argoproj.io/v1alpha1 since 2.3; there is no
// other served version to fall back to.
var GroupVersionResource = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applicationsets",
}

// Sync-policy values ArgoCD accepts for spec.syncPolicy.applicationsSync.
// "sync" (or an empty field) is the default and is the one that deletes.
const (
	ApplicationsSyncCreateOnly   = "create-only"
	ApplicationsSyncCreateUpdate = "create-update"
	ApplicationsSyncCreateDelete = "create-delete"
	ApplicationsSyncSync         = "sync"
)

// ApplicationSetInfo is everything Sharko knows about one ApplicationSet.
// Every field comes from a well-defined ArgoCD API field; nothing here is
// derived from a user template.
type ApplicationSetInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`

	// PreserveResourcesOnDeletion mirrors
	// spec.syncPolicy.preserveResourcesOnDeletion. true means: when this
	// AppSet deletes a generated Application, the workloads that
	// Application deployed are left running.
	PreserveResourcesOnDeletion bool `json:"preserve_resources_on_deletion"`

	// ApplicationsSync mirrors spec.syncPolicy.applicationsSync. Empty
	// means the field was not set, which ArgoCD treats as "sync" — the
	// mode that does delete Applications.
	ApplicationsSync string `json:"applications_sync,omitempty"`

	// ClusterSelectorLabels is every label key any cluster generator in
	// this AppSet selects on, sorted and de-duplicated. Both matchLabels
	// keys and matchExpressions keys land here.
	ClusterSelectorLabels []string `json:"cluster_selector_labels,omitempty"`

	// ClusterSelectorMatchLabels is the flattened matchLabels map across
	// every cluster generator (key -> required value). matchExpressions
	// contribute their key to ClusterSelectorLabels but not here — an
	// expression has no single required value.
	ClusterSelectorMatchLabels map[string]string `json:"cluster_selector_match_labels,omitempty"`

	// HasClusterGenerator is true when at least one cluster generator was
	// found. An AppSet with no cluster generator cannot react to a cluster
	// Secret's labels at all, which is a useful thing to be able to say.
	HasClusterGenerator bool `json:"has_cluster_generator"`
}

// DeletionSafe reports whether this ApplicationSet leaves workloads alone
// when a cluster stops matching it.
//
// Two independent settings make it safe, and either one is enough:
//
//   - preserveResourcesOnDeletion: true — the Application may go away, but
//     the things it deployed stay running.
//   - applicationsSync: create-only or create-update — the AppSet
//     controller is not allowed to delete Applications at all, so nothing
//     downstream gets pruned either.
//
// Anything else (the default "sync", or an explicit "create-delete") means
// losing the match deletes the Application and prunes its workloads.
func (i ApplicationSetInfo) DeletionSafe() bool {
	if i.PreserveResourcesOnDeletion {
		return true
	}
	switch i.ApplicationsSync {
	case ApplicationsSyncCreateOnly, ApplicationsSyncCreateUpdate:
		return true
	}
	return false
}

// SelectsLabelKey reports whether any cluster generator in this
// ApplicationSet selects on the given label key. Used by the label-drop
// story: dropping a label an AppSet still selects on is exactly the move
// that makes a cluster fall out of that AppSet.
func (i ApplicationSetInfo) SelectsLabelKey(key string) bool {
	for _, k := range i.ClusterSelectorLabels {
		if k == key {
			return true
		}
	}
	return false
}

// WhySafe / WhyUnsafe render the reason in words a non-developer can act
// on. Kept next to DeletionSafe so the sentence can never drift from the
// condition it describes.
func (i ApplicationSetInfo) WhySafe() string {
	if i.PreserveResourcesOnDeletion {
		return "it is set to keep the things it deployed even when its Application goes away"
	}
	switch i.ApplicationsSync {
	case ApplicationsSyncCreateOnly:
		return "it is set to only ever create Applications, never change or delete them"
	case ApplicationsSyncCreateUpdate:
		return "it is set to create and update Applications, but never delete them"
	}
	return ""
}

// WhyUnsafe explains, in one clause, what this AppSet will do if the
// cluster stops matching it.
func (i ApplicationSetInfo) WhyUnsafe() string {
	if i.ApplicationsSync == ApplicationsSyncCreateDelete {
		return "it is allowed to delete the Applications it created"
	}
	return "it uses the default behaviour, which deletes the Application and everything that Application installed"
}

// Reader is the read-only ApplicationSet source. Declared as an interface
// so the API layer can be handed a fake in tests and a nil when Sharko is
// not running in-cluster.
type Reader interface {
	List(ctx context.Context) ([]ApplicationSetInfo, error)
}

// DynamicReader lists ApplicationSets through the K8s dynamic client. It
// only ever calls List — no watch, no write.
type DynamicReader struct {
	client    dynamic.Interface
	namespace string
}

// NewDynamicReader returns a Reader over the ApplicationSets in namespace
// (normally "argocd"). Returns nil when client is nil so callers can wire
// it unconditionally and nil-check once.
func NewDynamicReader(client dynamic.Interface, namespace string) *DynamicReader {
	if client == nil {
		return nil
	}
	if namespace == "" {
		namespace = "argocd"
	}
	return &DynamicReader{client: client, namespace: namespace}
}

// List returns every ApplicationSet in the reader's namespace, parsed down
// to the narrow field set above. Results are sorted by name so a preflight
// report re-run reads identically.
func (r *DynamicReader) List(ctx context.Context) ([]ApplicationSetInfo, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("no ApplicationSet reader wired")
	}
	list, err := r.client.Resource(GroupVersionResource).Namespace(r.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing ApplicationSets in namespace %q: %w", r.namespace, err)
	}
	out := make([]ApplicationSetInfo, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, Parse(list.Items[i].Object))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// Parse extracts the narrow field set from one ApplicationSet object.
// Exported and pure so the whole "what does this AppSet do on deletion"
// judgement is testable against fixture YAML with no cluster in sight.
//
// Every lookup below is defensive: a field of the wrong type, or a missing
// block, yields the zero value rather than a panic. An ApplicationSet we
// cannot fully understand is reported as NOT deletion-safe, which is the
// direction that makes a person look before they leap.
func Parse(obj map[string]interface{}) ApplicationSetInfo {
	info := ApplicationSetInfo{}
	if obj == nil {
		return info
	}

	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		info.Name, _ = meta["name"].(string)
		info.Namespace, _ = meta["namespace"].(string)
	}

	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return info
	}

	if syncPolicy, ok := spec["syncPolicy"].(map[string]interface{}); ok {
		info.PreserveResourcesOnDeletion, _ = syncPolicy["preserveResourcesOnDeletion"].(bool)
		info.ApplicationsSync, _ = syncPolicy["applicationsSync"].(string)
	}

	// spec.template is deliberately never read — see the package comment.
	generators, _ := spec["generators"].([]interface{})
	keys := map[string]struct{}{}
	matchLabels := map[string]string{}
	collectClusterSelectors(generators, keys, matchLabels, &info.HasClusterGenerator, 0)

	if len(keys) > 0 {
		info.ClusterSelectorLabels = make([]string, 0, len(keys))
		for k := range keys {
			info.ClusterSelectorLabels = append(info.ClusterSelectorLabels, k)
		}
		sort.Strings(info.ClusterSelectorLabels)
	}
	if len(matchLabels) > 0 {
		info.ClusterSelectorMatchLabels = matchLabels
	}
	return info
}

// maxGeneratorDepth caps the matrix/merge/union recursion. ArgoCD itself
// only allows one level of nesting today; the cap exists so a hand-crafted
// or future deeply-nested document can never spin this walk.
const maxGeneratorDepth = 8

// collectClusterSelectors walks a generators list, recording the label keys
// (and matchLabels values) of every cluster generator it finds — including
// the ones nested inside matrix, merge and union generators.
func collectClusterSelectors(generators []interface{}, keys map[string]struct{}, matchLabels map[string]string, found *bool, depth int) {
	if depth > maxGeneratorDepth {
		return
	}
	for _, raw := range generators {
		gen, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		if clusters, ok := gen["clusters"].(map[string]interface{}); ok {
			*found = true
			collectSelector(clusters["selector"], keys, matchLabels)
		}

		// Nested generator containers. Each holds its own "generators"
		// list with the same shape, so the walk is the same function.
		for _, nested := range []string{"matrix", "merge", "union"} {
			container, ok := gen[nested].(map[string]interface{})
			if !ok {
				continue
			}
			inner, _ := container["generators"].([]interface{})
			collectClusterSelectors(inner, keys, matchLabels, found, depth+1)
		}
	}
}

// collectSelector reads a metav1.LabelSelector-shaped block: matchLabels
// (key -> value) and matchExpressions (a list of objects with a "key").
func collectSelector(raw interface{}, keys map[string]struct{}, matchLabels map[string]string) {
	selector, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if ml, ok := selector["matchLabels"].(map[string]interface{}); ok {
		for k, v := range ml {
			keys[k] = struct{}{}
			if s, ok := v.(string); ok {
				matchLabels[k] = s
			}
		}
	}
	if exprs, ok := selector["matchExpressions"].([]interface{}); ok {
		for _, rawExpr := range exprs {
			expr, ok := rawExpr.(map[string]interface{})
			if !ok {
				continue
			}
			if k, ok := expr["key"].(string); ok && k != "" {
				keys[k] = struct{}{}
			}
		}
	}
}

// NotDeletionSafe filters a list down to the ApplicationSets that would
// delete Applications (and prune their workloads) if a cluster stopped
// matching them. Order is preserved.
func NotDeletionSafe(all []ApplicationSetInfo) []ApplicationSetInfo {
	var out []ApplicationSetInfo
	for _, a := range all {
		if !a.DeletionSafe() {
			out = append(out, a)
		}
	}
	return out
}

// SelectingLabelKeys filters a list down to the ApplicationSets whose
// cluster generators select on ANY of the given label keys.
func SelectingLabelKeys(all []ApplicationSetInfo, labelKeys []string) []ApplicationSetInfo {
	var out []ApplicationSetInfo
	for _, a := range all {
		for _, key := range labelKeys {
			if a.SelectsLabelKey(key) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// Names returns just the names, for embedding in a report.
func Names(all []ApplicationSetInfo) []string {
	out := make([]string, 0, len(all))
	for _, a := range all {
		out = append(out, a.Name)
	}
	return out
}
