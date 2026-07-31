// Package demo — the v4 data-file-format demo repo (v4 Wave 1 Story 4.5).
//
// docs/design/2026-07-30-v4-data-file-format.md's worked example (§6) is
// the template: a handful of clusters, one addon pinned older with a
// known quirk on one cluster, another addon following the catalog
// default everywhere, and one in-house addon that only exists in the
// caller's own catalog/addons.yaml delta (never in the shipped curated
// catalog) — the "internal addon" case v4 Wave 1 Story 3.3 added.
//
// This file builds that layout using the SAME Go helpers a real Sharko
// server uses to write it (models.SaveClusterAddons,
// config.SaveAddonCatalog, models.SaveManagedClusters) rather than
// hand-typed YAML strings, so the fixture can never drift from what the
// real read/write path actually accepts.
//
// Deliberately additive: the v3 fixture files in mock_git.go
// (configuration/managed-clusters.yaml, configuration/addons-catalog.yaml,
// configuration/addons-*-values/) are left untouched. Several existing
// read paths (GetClusterComparison, the dashboard's addon-stats tile) are
// not yet v4-aware — that is a known backend gap tracked in the design
// doc's §8 "what this changes in the existing code" list, not something
// this UI/demo-fixture lane owns. Keeping both layouts side by side means
// the v4-aware reads (AddonService.GetVersionMatrix's v4 branch, the
// catalog/delta endpoints, EnableAddonV4/DisableAddonV4 and their
// dry-run previews) light up on real v4 data while every already-working
// v3-driven view (cluster comparison, the dashboard tile) keeps working
// exactly as it did before this story.
package demo

import (
	"fmt"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// v4CatalogDeltaAddon is one entry this fixture writes into the demo's
// catalog/addons.yaml delta, alongside the (cluster, pin) combinations
// that reference it from clusters/*.yaml.
type v4ClusterAddonPin struct {
	name     string
	version  string // "" means "no per-cluster pin — follow the catalog default"
	settings *models.ClusterAddonsAddonSettings
}

// buildV4DemoFiles renders the v4-format demo repo layout (design doc
// §1, §2) for the same five clusters the v3 fixture already seeds
// (prod-eu, prod-us, staging-eu, dev-us, perf-asia — see
// clusterAddonsYAML in mock_git.go). Every addon used here already
// exists in the real shipped/curated catalog (catalog/addons.yaml at
// the repo root, embedded at build time) EXCEPT datadog, which is not
// curated (it ships from a real public Helm repo but isn't part of
// Sharko's open-source curated set) — that gap is exactly what makes it
// the "one internal addon" the merged-catalog view (GET
// /api/v1/catalog/delta/addons) is meant to show: same shape, same
// version field, same everything as a curated addon, just sourced by
// the caller's own delta instead of the shipped set (design doc §2.3).
//
// Returns path -> rendered file bytes, ready to merge into
// MockGitProvider.files.
func buildV4DemoFiles() (map[string][]byte, error) {
	files := make(map[string][]byte)

	// ---- catalog/addons.yaml (AddonCatalog) ----------------------
	//
	// Fleet-wide "this is the version we run" entries for every curated
	// addon the demo clusters use, plus datadog as a full in-house
	// entry (repoURL/chart/version/namespace all required — none of
	// them come from a shipped catalog entry, because there isn't one).
	deltaSpec := config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager":          {Version: "1.14.5"},
			"metrics-server":        {Version: "3.12.1"},
			"kube-prometheus-stack": {Version: "58.2.1"},
			"external-dns":          {Version: "1.14.4"},
			"vault":                 {Version: "0.28.0"},
			"datadog": {
				RepoURL:   "https://helm.datadoghq.com",
				Chart:     "datadog",
				Version:   "3.69.0",
				Namespace: "datadog",
			},
		},
	}
	deltaBytes, err := config.SaveAddonCatalog(deltaSpec)
	if err != nil {
		return nil, fmt.Errorf("rendering demo %s: %w", config.AddonCatalogPath, err)
	}
	files[config.AddonCatalogPath] = deltaBytes

	// ---- clusters/<name>.yaml (one ClusterAddons per cluster) ----
	//
	// prod-eu carries the design doc's worked example verbatim (§6):
	// cert-manager pinned to an older version with the webhook
	// caBundle ignoreDifferences quirk, metrics-server following the
	// catalog default. The other four clusters mirror the pins already
	// present in the v3 fixture's labels (clusterAddonsYAML) so the two
	// layouts tell the same story about the same fleet.
	clusterAddons := map[string][]v4ClusterAddonPin{
		"prod-eu": {
			{name: "cert-manager", version: "1.12.0", settings: &models.ClusterAddonsAddonSettings{
				IgnoreDifferences: []map[string]interface{}{
					{
						"group": "admissionregistration.k8s.io",
						"kind":  "ValidatingWebhookConfiguration",
						"jsonPointers": []interface{}{
							"/webhooks/0/clientConfig/caBundle",
						},
					},
				},
			}},
			{name: "metrics-server"},
			{name: "kube-prometheus-stack"},
			{name: "external-dns"},
		},
		"prod-us": {
			{name: "cert-manager"},
			{name: "metrics-server"},
			{name: "kube-prometheus-stack"},
			{name: "external-dns"},
		},
		"staging-eu": {
			{name: "cert-manager", version: "1.13.6"},
			{name: "metrics-server", version: "3.11.0"},
			{name: "kube-prometheus-stack", version: "57.2.0"},
			{name: "datadog"},
		},
		"dev-us": {
			{name: "cert-manager", version: "1.13.6"},
			{name: "metrics-server"},
			{name: "vault"},
		},
		"perf-asia": {
			{name: "cert-manager", version: "1.12.9"},
			{name: "metrics-server", version: "3.10.0"},
			{name: "kube-prometheus-stack", version: "55.5.0"},
		},
	}

	// Deterministic iteration order (map range order is random in Go) —
	// keeps the fixture's own construction reproducible across runs,
	// even though the end result (a map keyed by cluster name) doesn't
	// care about insertion order itself.
	for _, clusterName := range []string{"prod-eu", "prod-us", "staging-eu", "dev-us", "perf-asia"} {
		pins := clusterAddons[clusterName]
		addons := make(map[string]models.ClusterAddonsAddon, len(pins))
		for _, p := range pins {
			addons[p.name] = models.ClusterAddonsAddon{
				Enabled:  true,
				Version:  p.version,
				Settings: p.settings,
			}
		}
		spec := models.ClusterAddonsSpec{Cluster: clusterName, Addons: addons}
		body, err := models.SaveClusterAddons(spec)
		if err != nil {
			return nil, fmt.Errorf("rendering demo clusters/%s.yaml: %w", clusterName, err)
		}
		files[orchestrator.V4ClustersDir+"/"+clusterName+".yaml"] = body
	}

	// ---- managed-clusters.yaml (ManagedClusters, v4 path) ------------
	//
	// Same five clusters as the v3 configuration/managed-clusters.yaml,
	// minus the addon on/off labels — v4 no longer authors those here
	// (design doc §2.4); they're derived from clusters/*.yaml instead.
	connSpec := models.ManagedClustersSpec{
		Clusters: []models.ManagedClusterEntry{
			{Name: "prod-eu", Region: "eu-west-1", SecretPath: "k8s-prod-eu", CredsSource: "secret-kubeconfig"},
			{Name: "prod-us", Region: "us-east-1", SecretPath: "k8s-prod-us", CredsSource: "secret-kubeconfig"},
			{Name: "staging-eu", Region: "eu-west-1", SecretPath: "k8s-staging-eu", CredsSource: "secret-kubeconfig"},
			{Name: "dev-us", Region: "us-west-2", SecretPath: "k8s-dev-us", CredsSource: "secret-kubeconfig"},
			{Name: "perf-asia", Region: "ap-southeast-1", SecretPath: "k8s-perf-asia", CredsSource: "secret-kubeconfig"},
		},
	}
	connBytes, err := models.SaveManagedClusters(connSpec)
	if err != nil {
		return nil, fmt.Errorf("rendering demo %s: %w", orchestrator.V4ManagedClustersPath, err)
	}
	files[orchestrator.V4ManagedClustersPath] = connBytes

	// ---- values/ (plain Helm values, no envelope — design doc §2.2) --
	files[orchestrator.V4GlobalValuesDir+"/cert-manager.yaml"] = []byte(`installCRDs: true
replicaCount: 2
resources:
  requests:
    cpu: 10m
    memory: 32Mi
`)
	files[orchestrator.V4GlobalValuesDir+"/metrics-server.yaml"] = []byte(`args:
  - --kubelet-insecure-tls
`)
	files[orchestrator.V4ClusterValuesDir+"/prod-eu/cert-manager.yaml"] = []byte(`replicaCount: 3
`)

	return files, nil
}
