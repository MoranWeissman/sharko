package operator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

const (
	// DefaultGeneratorTickInterval is the safety-net cadence for re-syncing
	// ClusterAddons CRs from managed-clusters.yaml. Mirrors the
	// clusterreconciler's 30s tick (both read the same file).
	DefaultGeneratorTickInterval = 60 * time.Second

	// ManagedByLabelKey is the managed-by label key that identifies
	// Sharko-generated ClusterAddons CRs (vs. hand-authored).
	ManagedByLabelKey = "app.kubernetes.io/managed-by"

	// ManagedByLabelValue is the managed-by label value for Sharko-owned CRs.
	ManagedByLabelValue = "sharko"

	// DefaultBranch is the default git branch to read managed-clusters.yaml from.
	DefaultBranch = "main"
)

// GitReaderFunc is the signature for the git-file reader injected into the
// generator. It reads the managed-clusters.yaml file from the repository and
// returns the raw YAML body. The generator parses it via
// models.LoadManagedClusters — the same reader path the clusterreconciler uses.
type GitReaderFunc func(ctx context.Context, path, branch string) ([]byte, error)

// ClusterAddonsGenerator auto-generates one ClusterAddons CR per managed
// cluster from configuration/managed-clusters.yaml. It is a GENERATOR, not a
// controller-runtime reconciler — a simple ticker loop using the manager's
// client. It writes CR SPEC only (never .status — Story 1.3 owns that) and
// prunes CRs whose cluster no longer exists in managed-clusters.yaml, but ONLY
// if they carry the managed-by=sharko label (hand-written CRs are never
// touched).
//
// Idempotent + deterministic: same file → same CRs. A hand-edit to a generated
// CR's SPEC is corrected on next pass (drift discipline). Status is left
// untouched.
//
// This generator is Story 1.3b. The status controller (Story 1.3) watches
// ClusterAddons CRs and writes .status; no fight because they're different
// subresources.
type ClusterAddonsGenerator struct {
	client    client.Client
	gitReader GitReaderFunc
	namespace string
	branch    string
	path      string

	tickInterval time.Duration

	// startOnce ensures Start() is idempotent.
	startOnce sync.Once

	// stopOnce ensures Stop() is idempotent.
	stopOnce sync.Once

	// stopCh signals the goroutine to stop.
	stopCh chan struct{}

	// stoppedCh is closed when the goroutine exits.
	stoppedCh chan struct{}
}

// NewClusterAddonsGenerator creates a new generator. The gitReader func reads
// managed-clusters.yaml from the repository; the client is the
// controller-runtime client from mgr.GetClient(); namespace is the namespace to
// create ClusterAddons CRs in (typically the same as the operator's namespace).
func NewClusterAddonsGenerator(
	c client.Client,
	gitReader GitReaderFunc,
	namespace string,
) *ClusterAddonsGenerator {
	return &ClusterAddonsGenerator{
		client:       c,
		gitReader:    gitReader,
		namespace:    namespace,
		branch:       DefaultBranch,
		path:         "configuration/managed-clusters.yaml",
		tickInterval: DefaultGeneratorTickInterval,
		stopCh:       make(chan struct{}),
		stoppedCh:    make(chan struct{}),
	}
}

// Start begins the generator loop in a goroutine. It generates CRs once
// immediately, then ticks on the configured interval. Safe to call multiple
// times (idempotent via sync.Once).
func (g *ClusterAddonsGenerator) Start(ctx context.Context) {
	g.startOnce.Do(func() {
		go g.run(ctx)
	})
}

// Stop signals the generator to stop and waits for the goroutine to exit.
// Safe to call multiple times (idempotent via sync.Once).
func (g *ClusterAddonsGenerator) Stop() {
	g.stopOnce.Do(func() {
		close(g.stopCh)
		<-g.stoppedCh
	})
}

// run is the generator's main loop. Generates CRs once on start, then ticks.
func (g *ClusterAddonsGenerator) run(ctx context.Context) {
	defer close(g.stoppedCh)

	slog.Info("[clusteraddons-generator] starting",
		"namespace", g.namespace,
		"path", g.path,
		"branch", g.branch,
		"tick_interval", g.tickInterval,
	)

	// Generate once immediately.
	g.generateOnce(ctx)

	ticker := time.NewTicker(g.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("[clusteraddons-generator] context canceled, stopping")
			return
		case <-g.stopCh:
			slog.Info("[clusteraddons-generator] stop signal received, stopping")
			return
		case <-ticker.C:
			g.generateOnce(ctx)
		}
	}
}

// generateOnce reads managed-clusters.yaml, generates/updates CRs for each
// cluster, and prunes CRs for clusters that no longer exist.
func (g *ClusterAddonsGenerator) generateOnce(ctx context.Context) {
	// Step 0: precondition check — no git reader means no work.
	if g.gitReader == nil {
		slog.Warn("[clusteraddons-generator] no git reader configured, skipping generate")
		return
	}

	// Step 1: read managed-clusters.yaml from git.
	body, err := g.gitReader(ctx, g.path, g.branch)
	if err != nil {
		// ErrFileNotFound is not exceptional — a freshly-bootstrapped repo has
		// zero clusters. Treat it as "empty desired state" and prune all
		// sharko-owned CRs.
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			slog.Debug("[clusteraddons-generator] managed-clusters.yaml not found, treating as empty")
			g.pruneOrphans(ctx, nil)
			return
		}
		slog.Error("[clusteraddons-generator] failed to read managed-clusters.yaml",
			"path", g.path,
			"branch", g.branch,
			"error", err,
		)
		return
	}

	// Step 2: parse via models.LoadManagedClusters (envelope-aware + validated).
	spec, err := models.LoadManagedClusters(body)
	if err != nil {
		slog.Error("[clusteraddons-generator] failed to parse managed-clusters.yaml",
			"path", g.path,
			"error", err,
		)
		return
	}

	// Step 3: build a set of desired cluster names for prune diff later.
	desiredNames := make(map[string]struct{}, len(spec.Clusters))
	for _, cluster := range spec.Clusters {
		desiredNames[cluster.Name] = struct{}{}
	}

	// Step 4: create/update CRs for each cluster.
	for _, cluster := range spec.Clusters {
		if err := g.ensureClusterAddonsCR(ctx, cluster); err != nil {
			// Per-cluster error isolation: log and continue to next.
			slog.Error("[clusteraddons-generator] failed to ensure CR",
				"cluster", cluster.Name,
				"error", err,
			)
		}
	}

	// Step 5: prune orphaned CRs (sharko-owned CRs whose cluster is gone).
	g.pruneOrphans(ctx, desiredNames)
}

// ensureClusterAddonsCR creates or updates a ClusterAddons CR for the given
// cluster. It writes SPEC only (never status).
func (g *ClusterAddonsGenerator) ensureClusterAddonsCR(ctx context.Context, cluster models.ManagedClusterEntry) error {
	// CR name = cluster name.
	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: g.namespace,
		},
	}

	// Use CreateOrUpdate to idempotently converge the CR.
	result, err := controllerutil.CreateOrUpdate(ctx, g.client, cr, func() error {
		// Set the managed-by label.
		if cr.Labels == nil {
			cr.Labels = make(map[string]string)
		}
		cr.Labels[ManagedByLabelKey] = ManagedByLabelValue

		// Set spec.cluster.
		cr.Spec.Cluster = cluster.Name

		// Map the cluster's labels (addon assignments) to spec.addons.
		cr.Spec.Addons = mapLabelsToAddonAssignments(cluster.Labels)

		return nil
	})

	if err != nil {
		return fmt.Errorf("CreateOrUpdate: %w", err)
	}

	if result != controllerutil.OperationResultNone {
		slog.Info("[clusteraddons-generator] CR ensured",
			"cluster", cluster.Name,
			"operation", result,
		)
	}

	return nil
}

// pruneOrphans deletes ClusterAddons CRs that have the managed-by=sharko label
// but whose cluster no longer exists in managed-clusters.yaml. desiredNames is
// the set of cluster names that SHOULD exist; a nil map means "empty desired
// state" (prune all sharko-owned CRs).
func (g *ClusterAddonsGenerator) pruneOrphans(ctx context.Context, desiredNames map[string]struct{}) {
	// List all ClusterAddons in our namespace with the managed-by label.
	var list v1alpha1.ClusterAddonsList
	listOpts := []client.ListOption{
		client.InNamespace(g.namespace),
		client.MatchingLabels{ManagedByLabelKey: ManagedByLabelValue},
	}

	if err := g.client.List(ctx, &list, listOpts...); err != nil {
		slog.Error("[clusteraddons-generator] failed to list CRs for pruning",
			"namespace", g.namespace,
			"error", err,
		)
		return
	}

	for _, cr := range list.Items {
		// If desiredNames is nil, prune all (empty desired state).
		// Otherwise, prune if this CR's cluster is not in the desired set.
		if desiredNames != nil {
			if _, exists := desiredNames[cr.Spec.Cluster]; exists {
				continue // keep it
			}
		}

		// Prune this CR.
		slog.Info("[clusteraddons-generator] pruning orphan CR",
			"cluster", cr.Spec.Cluster,
			"cr_name", cr.Name,
		)

		if err := g.client.Delete(ctx, &cr); err != nil {
			// Per-CR error isolation: log and continue.
			slog.Error("[clusteraddons-generator] failed to delete orphan CR",
				"cluster", cr.Spec.Cluster,
				"cr_name", cr.Name,
				"error", err,
			)
		}
	}
}

// mapLabelsToAddonAssignments converts the cluster's labels
// (addon-name→"enabled"/"disabled" map) to a list of AddonAssignment structs.
func mapLabelsToAddonAssignments(labels models.ClusterLabels) []v1alpha1.AddonAssignment {
	if len(labels) == 0 {
		return nil
	}

	assignments := make([]v1alpha1.AddonAssignment, 0, len(labels))
	for addonName, state := range labels {
		enabled := (state == "enabled")
		assignments = append(assignments, v1alpha1.AddonAssignment{
			Name:    addonName,
			Enabled: &enabled,
			// Version is omitted — per-cluster version overrides are not stored
			// in managed-clusters.yaml labels; they come from the values file
			// (and are not part of the ClusterAddons CR's SPEC layer — that's
			// the reconciler's job to read from git).
		})
	}

	return assignments
}
