// generator.go — the seeded demo-estate generator (S1).
//
// The default demo estate (5 clusters, 8 addons — seed.go's demoClusters /
// demoAddons / demoUnregisteredClusters, mock_git.go's seeded PRs) is
// hand-written and UNTOUCHED by this file: every constructor that takes a
// ScaleConfig checks cfg.IsDefault() first and falls back to the existing
// hand-written fixtures when it's true, so the maintainer's familiar
// prod-eu/prod-us/staging-eu/dev-us/perf-asia walkthrough never changes
// shape, and every existing demo test keeps passing unmodified.
//
// GenerateEstate only runs for a NON-default ScaleConfig (bigger, smaller,
// or differently-seeded than the default) — the --demo-scale=big preset and
// any explicit --demo-clusters/--demo-addons/--demo-seed flag combination.
// The only source of randomness is math/rand seeded from cfg.Seed, so the
// same config always reproduces the same estate.
package demo

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// ScaleConfig controls the size of a generated demo estate.
type ScaleConfig struct {
	Clusters int
	Addons   int
	Seed     int64
}

// Default/preset sizes (S2 flags + --demo-scale=big).
const (
	DefaultClusterCount = 5
	DefaultAddonCount   = 8
	DefaultSeed         = 0

	BigClusterCount = 50
	BigAddonCount   = 30
	// BigSeed is fixed (not time-based) so --demo-scale=big always renders
	// the identical estate — reproducible across restarts and machines.
	BigSeed = 20260801
)

// DefaultScaleConfig reproduces today's small, hand-written demo estate.
// BigScaleConfig is the --demo-scale=big preset.
var (
	DefaultScaleConfig = ScaleConfig{Clusters: DefaultClusterCount, Addons: DefaultAddonCount, Seed: DefaultSeed}
	BigScaleConfig     = ScaleConfig{Clusters: BigClusterCount, Addons: BigAddonCount, Seed: BigSeed}
)

// IsDefault reports whether cfg matches the small hand-written estate's
// size. Every generator-aware constructor in this package checks this
// FIRST and skips the generator entirely when true — see the package doc
// comment above.
func (cfg ScaleConfig) IsDefault() bool {
	return cfg.Clusters == DefaultClusterCount && cfg.Addons == DefaultAddonCount
}

// Internal generation knobs (S1 story spec).
const (
	minAddonsPerCluster = 10
	maxAddonsPerCluster = 18

	// degradedFraction is the per-cell probability an addon deployment
	// rolls Degraded or Progressing instead of Healthy. Applied per
	// (cluster, addon) cell independently, which spreads the unhealthy
	// cells across clusters rather than concentrating them.
	degradedFraction = 0.10

	// behindCatalogFraction is the per-cell probability a deployed addon
	// version is rendered older than the catalog's current version.
	behindCatalogFraction = 0.20

	disconnectedClusterCount = 2

	minUnregisteredCandidates = 1
	maxUnregisteredCandidates = 2

	targetOpenPRs   = 15
	targetMergedPRs = 20

	minHistoryEntries = 1
	maxHistoryEntries = 5
)

// generatedAddon is one addon drawn from the curated catalog for a
// generated estate, plus a synthesized "current catalog version" (the real
// curated catalog has no version field — every consumer resolves it from a
// live Helm repo, which a demo estate has none of).
type generatedAddon struct {
	Name           string
	Chart          string
	RepoURL        string
	Namespace      string
	Description    string
	CatalogVersion string
}

// deploymentInfo is the per-(cluster, addon) cell detail a generated
// estate needs beyond the deployed version already carried on Cluster.Addons.
type deploymentInfo struct {
	Health  string // "Healthy" | "Degraded" | "Progressing"
	Sync    string // "Synced" | "OutOfSync"
	History []generatedHistoryEntry
}

// generatedHistoryEntry mirrors one ArgoCD application history entry
// (models.AppHistoryEntry / mockHistoryEntry) — ascending IDs, earliest
// first, each with a fake short SHA revision.
type generatedHistoryEntry struct {
	ID              int
	DeployedAt      string
	DeployStartedAt string
	Revision        string
}

// GeneratedEstate is the full output of GenerateEstate: everything the
// mock Git provider, the mock ArgoCD server, and the mock credentials
// provider need to present a self-consistent estate of the requested size.
type GeneratedEstate struct {
	Clusters             []Cluster
	UnregisteredClusters []Cluster
	Addons               []generatedAddon

	// Deployments[clusterName][addonName] holds the health/sync/history
	// detail for that cell. Cluster.Addons[addonName] (name -> deployed
	// version) already lives on the Cluster value; this map is keyed the
	// same way so the two are easy to join when building mock ArgoCD apps.
	Deployments map[string]map[string]deploymentInfo

	// TrackedPRs is the "dozens of PRs" feed (S1) — a mix of open and
	// recently-merged addon-enable/disable/upgrade PRs, in the shape the
	// PR tracker (internal/prtracker) stores and /api/v1/prs serves.
	TrackedPRs []prtracker.PRInfo

	// GitPRs is the minimal PR set the mock Git provider itself needs to
	// seed (internal/gitprovider.PullRequest) — just the pending
	// registration PRs for the unregistered candidate clusters, matching
	// the branch/title pattern the orchestrator's real registration flow
	// uses. This is a distinct, smaller list from TrackedPRs: MockGitProvider
	// is read directly by pending-registration detection, while TrackedPRs
	// feeds the PR tracker (/api/v1/prs) — two different consumers.
	GitPRs []gitprovider.PullRequest

	// --- State-coverage fields (maintainer scope addition, folded into S1) ---
	//
	// Everything below exists so every distinct state the UI can render
	// for a cluster or an addon application appears at least once in a
	// generated (non-default) estate, deterministically pinned rather than
	// left to the random per-cell fractions above. See showcase.go for
	// where each one is assigned and the package-level doc comment there
	// for the full enumeration.

	// ArgoOnlyClusters are clusters the mock ArgoCD server knows about
	// (with a real cluster entry + apps) that are deliberately NOT written
	// to any git file — the "discovered / not_in_git" case
	// (internal/service/cluster.go's notInGitClusters). SharkoOwned
	// controls whether the cluster's fake ArgoCD-secret carries the
	// app.kubernetes.io/managed-by=sharko label, which is the gate
	// between the plain "not_in_git" bucket (unlabeled) and the "Orphan
	// Registrations" bucket (labeled) — see
	// internal/api/clusters_orphans.go's resolveOrphanRegistrations.
	ArgoOnlyClusters []ArgoOnlyCluster

	// MissingClusterNames are registered (git-defined) clusters that are
	// deliberately EXCLUDED from the mock ArgoCD server's cluster/app
	// list entirely — internal/service/cluster.go's ConnectionStatus =
	// "missing" case (a cluster in managed-clusters.yaml whose ArgoCD
	// cluster Secret is gone). Doubles as the "git-defined but not owned"
	// case the maintainer's request called out.
	MissingClusterNames []string

	// ForeignSecretClusterNames are REGISTERED clusters (present in both
	// git and the mock ArgoCD server) whose fake ArgoCD-connection Secret
	// deliberately lacks the app.kubernetes.io/managed-by=sharko label —
	// the "takeover-eligible" case (models.Cluster.AlreadyManagedBySharko
	// = false while everything else about the cluster looks normal).
	ForeignSecretClusterNames []string

	// SelfManagedClusterNames are registered clusters whose git entry sets
	// connectionManagedBy: user (models.ManagedClusterEntry.ConnectionManagedBy)
	// — the self-managed-connection case, rendered in the UI as
	// "connection managed by you".
	SelfManagedClusterNames []string

	// ConnectivityCheckApps are connectivity-check-<cluster> ArgoCD
	// Applications to seed for disconnected clusters, covering every
	// internal/api/connectivity_status.go verdict (verified_check,
	// check_pending, check_failed — verified_argocd and "" fall out of
	// the ordinary ConnStatus/absent-app cases with no extra seeding).
	ConnectivityCheckApps []ConnectivityCheckApp

	// ObservationSeeds feed the fake observations.Store (S1 coverage
	// addition) so every persisted SharkoStatus value
	// (internal/observations/types.go's 5-state model) is reachable.
	ObservationSeeds []ObservationSeed
}

// ArgoOnlyCluster is one of GeneratedEstate.ArgoOnlyClusters — see that
// field's doc comment.
type ArgoOnlyCluster struct {
	Cluster     Cluster
	SharkoOwned bool
}

// ConnectivityCheckApp describes one connectivity-check-<cluster>
// Application to add to the mock ArgoCD server on behalf of a disconnected
// cluster. CreatedAt controls the check_pending -> check_failed escalation
// (internal/api/connectivity_status.go's checkPendingEscalation window).
type ConnectivityCheckApp struct {
	ClusterName    string
	Health         string
	Sync           string
	CreatedAt      string
	OperationPhase string
}

// ObservationSeed is one exemplar cluster's connectivity-test history, fed
// into the fake observations.Store via RecordTestResult. Stage is ""
// (never tested — ComputeStatus's default case), "stage1", or "stage2";
// Success false always yields StatusUnreachable regardless of stage.
type ObservationSeed struct {
	ClusterName string
	Stage       string
	Success     bool
}

var (
	// envTagToEnv maps a cluster-name env tag to the full word stored on
	// Cluster.Env.
	envTagToEnv = map[string]string{
		"prod":    "production",
		"staging": "staging",
		"dev":     "development",
	}

	// shortRegionToFull maps a cluster-name region tag to a realistic
	// cloud region string stored on Cluster.Region. Generic placeholders
	// only — no real account IDs or internal hostnames.
	shortRegionToFull = map[string]string{
		"eu":   "eu-west-1",
		"us":   "us-east-1",
		"asia": "ap-southeast-1",
	}

	generatedK8sVersions = []string{"1.27.16", "1.28.15", "1.29.10", "1.30.6", "1.31.2"}
)

// genClusterName is one generated cluster's name plus the metadata derived
// alongside it (env/region), so callers never need to re-parse the name.
type genClusterName struct {
	Name   string
	Env    string
	Region string
}

// generateClusterNames produces n realistic env+region cluster names —
// prod-eu-1..k, prod-us-*, staging-*, dev-* — deterministically from rng.
// Combos are shuffled once so which family gets more replicas varies with
// the seed, then assigned round-robin with a per-combo running index.
func generateClusterNames(n int, rng *rand.Rand) []genClusterName {
	type combo struct{ envTag, regionTag string }
	var combos []combo
	for _, e := range []string{"prod", "staging", "dev"} {
		for _, r := range []string{"eu", "us", "asia"} {
			combos = append(combos, combo{e, r})
		}
	}
	rng.Shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })

	counts := make(map[combo]int, len(combos))
	out := make([]genClusterName, 0, n)
	for i := 0; len(out) < n; i++ {
		c := combos[i%len(combos)]
		counts[c]++
		out = append(out, genClusterName{
			Name:   fmt.Sprintf("%s-%s-%d", c.envTag, c.regionTag, counts[c]),
			Env:    envTagToEnv[c.envTag],
			Region: shortRegionToFull[c.regionTag],
		})
	}
	return out
}

// generateUnregisteredClusterNames produces plausible names for clusters
// visible to the credentials provider but not yet registered — the
// disaster-recovery / sandbox flavor the hand-written fixture uses (dr-eu).
func generateUnregisteredClusterNames(n int, rng *rand.Rand) []genClusterName {
	type combo struct{ prefix, regionTag string }
	var combos []combo
	for _, p := range []string{"dr", "sandbox"} {
		for _, r := range []string{"eu", "us", "asia"} {
			combos = append(combos, combo{p, r})
		}
	}
	rng.Shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })

	counts := make(map[combo]int, len(combos))
	out := make([]genClusterName, 0, n)
	for i := 0; len(out) < n; i++ {
		c := combos[i%len(combos)]
		counts[c]++
		env := "disaster-recovery"
		if c.prefix == "sandbox" {
			env = "sandbox"
		}
		out = append(out, genClusterName{
			Name:   fmt.Sprintf("%s-%s-%d", c.prefix, c.regionTag, counts[c]),
			Env:    env,
			Region: shortRegionToFull[c.regionTag],
		})
	}
	return out
}

// selectAddons draws n addons from the real curated catalog
// (catalog/addons.yaml, ~45 entries) via a seeded shuffle, so a generated
// estate's addon names/charts/repos look real. A synthesized
// "current catalog version" is attached since the curated catalog itself
// carries no version (every real consumer resolves versions from a live
// Helm repo, which a demo estate has none of).
func selectAddons(n int, rng *rand.Rand) ([]generatedAddon, error) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("demo generator: loading curated catalog: %w", err)
	}
	entries := append([]catalog.CatalogEntry(nil), cat.Entries()...)
	if n > len(entries) {
		n = len(entries)
	}
	if n < 0 {
		n = 0
	}
	rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	selected := entries[:n]
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })

	out := make([]generatedAddon, 0, n)
	for _, e := range selected {
		ns := e.DefaultNamespace
		if ns == "" {
			ns = e.Name
		}
		out = append(out, generatedAddon{
			Name:           e.Name,
			Chart:          e.Chart,
			RepoURL:        e.Repo,
			Namespace:      ns,
			Description:    e.Description,
			CatalogVersion: generateVersion(rng),
		})
	}
	return out, nil
}

// generateVersion synthesizes a plausible-looking semver string.
func generateVersion(rng *rand.Rand) string {
	major := 1 + rng.Intn(2)
	minor := rng.Intn(40)
	patch := rng.Intn(12)
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// olderVersion synthesizes a version that reads as older than v — used for
// the behind-catalog fraction of cells. Not real semver arithmetic, just
// "clearly older" for display purposes.
func olderVersion(v string, rng *rand.Rand) string {
	var major, minor, patch int
	if _, err := fmt.Sscanf(v, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return v
	}
	switch {
	case patch > 0 && rng.Intn(2) == 0:
		patch -= 1 + rng.Intn(patch)
	case minor > 0:
		step := minor
		if step > 3 {
			step = 3
		}
		minor -= 1 + rng.Intn(step)
		patch = rng.Intn(10)
	case patch > 0:
		patch--
	}
	if minor < 0 {
		minor = 0
	}
	if patch < 0 {
		patch = 0
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

const hexDigits = "0123456789abcdef"

// randomShortSHA returns a fake 7-character short SHA, matching ArgoCD's
// convention for a Git revision reference.
func randomShortSHA(rng *rand.Rand) string {
	b := make([]byte, 7)
	for i := range b {
		b[i] = hexDigits[rng.Intn(len(hexDigits))]
	}
	return string(b)
}

// generateHistory produces 1-5 ascending-ID ArgoCD history entries spread
// over the past days, oldest first — the earliest entry is what
// buildRecentSyncs (internal/service/observability.go) treats as the
// install; every later one reads as an update.
func generateHistory(rng *rand.Rand, now time.Time) []generatedHistoryEntry {
	count := minHistoryEntries + rng.Intn(maxHistoryEntries-minHistoryEntries+1)
	spanDays := 10 + rng.Intn(110)
	entries := make([]generatedHistoryEntry, count)
	for i := 0; i < count; i++ {
		frac := float64(count-i) / float64(count)
		daysAgo := int(float64(spanDays) * frac)
		deployedAt := now.
			Add(-time.Duration(daysAgo) * 24 * time.Hour).
			Add(-time.Duration(rng.Intn(720)) * time.Minute)
		startedAt := deployedAt.Add(-time.Duration(1+rng.Intn(20)) * time.Minute)
		entries[i] = generatedHistoryEntry{
			ID:              i + 1,
			DeployedAt:      deployedAt.UTC().Format(time.RFC3339),
			DeployStartedAt: startedAt.UTC().Format(time.RFC3339),
			Revision:        randomShortSHA(rng),
		}
	}
	return entries
}

// GenerateEstate builds a full demo estate of the requested size. Callers
// MUST check cfg.IsDefault() first — this function does not special-case
// the default size and is only ever invoked for a non-default ScaleConfig.
func GenerateEstate(cfg ScaleConfig) (*GeneratedEstate, error) {
	rng := rand.New(rand.NewSource(cfg.Seed))

	addons, err := selectAddons(cfg.Addons, rng)
	if err != nil {
		return nil, err
	}

	names := generateClusterNames(cfg.Clusters, rng)

	discCount := disconnectedClusterCount
	if discCount > len(names) {
		discCount = len(names)
	}
	disconnectedIdx := make(map[int]bool, discCount)
	for _, idx := range rng.Perm(len(names))[:discCount] {
		disconnectedIdx[idx] = true
	}

	now := time.Now()
	clusters := make([]Cluster, 0, len(names))
	deployments := make(map[string]map[string]deploymentInfo, len(names))

	for i, n := range names {
		connStatus := "Successful"
		if disconnectedIdx[i] {
			connStatus = "Failed"
		}

		addonCount := minAddonsPerCluster
		if maxAddonsPerCluster > minAddonsPerCluster {
			addonCount += rng.Intn(maxAddonsPerCluster - minAddonsPerCluster + 1)
		}
		if addonCount > len(addons) {
			addonCount = len(addons)
		}
		order := rng.Perm(len(addons))[:addonCount]
		sort.Ints(order)

		clusterAddons := make(map[string]string, addonCount)
		cellInfo := make(map[string]deploymentInfo, addonCount)
		for _, ai := range order {
			a := addons[ai]
			version := a.CatalogVersion
			behind := rng.Float64() < behindCatalogFraction
			if behind {
				version = olderVersion(a.CatalogVersion, rng)
			}

			health := "Healthy"
			sync := "Synced"
			if behind {
				sync = "OutOfSync"
			}
			if rng.Float64() < degradedFraction {
				if rng.Intn(2) == 0 {
					health = "Degraded"
					sync = "OutOfSync"
				} else {
					health = "Progressing"
				}
			}

			clusterAddons[a.Name] = version
			cellInfo[a.Name] = deploymentInfo{
				Health:  health,
				Sync:    sync,
				History: generateHistory(rng, now),
			}
		}

		clusters = append(clusters, Cluster{
			Name:       n.Name,
			Server:     fmt.Sprintf("https://k8s.%s.demo.internal", n.Name),
			Region:     n.Region,
			Env:        n.Env,
			K8sVersion: generatedK8sVersions[rng.Intn(len(generatedK8sVersions))],
			ConnStatus: connStatus,
			Addons:     clusterAddons,
		})
		deployments[n.Name] = cellInfo
	}

	unregisteredCount := minUnregisteredCandidates
	if maxUnregisteredCandidates > minUnregisteredCandidates {
		unregisteredCount += rng.Intn(maxUnregisteredCandidates - minUnregisteredCandidates + 1)
	}
	unregisteredNames := generateUnregisteredClusterNames(unregisteredCount, rng)
	unregistered := make([]Cluster, 0, unregisteredCount)
	gitPRs := make([]gitprovider.PullRequest, 0, unregisteredCount)
	nextGitPRID := 1
	for _, n := range unregisteredNames {
		unregistered = append(unregistered, Cluster{
			Name:       n.Name,
			Server:     fmt.Sprintf("https://k8s.%s.demo.internal", n.Name),
			Region:     n.Region,
			Env:        n.Env,
			K8sVersion: generatedK8sVersions[rng.Intn(len(generatedK8sVersions))],
			ConnStatus: "Successful",
		})
		id := nextGitPRID
		nextGitPRID++
		createdAt := now.Add(-time.Duration(rng.Intn(5*24)) * time.Hour).UTC().Format(time.RFC3339)
		gitPRs = append(gitPRs, gitprovider.PullRequest{
			ID:           id,
			Title:        "sharko: register cluster " + n.Name,
			Description:  "Automated cluster registration by Sharko",
			Author:       "sharko-bot",
			Status:       "open",
			SourceBranch: "sharko/register-" + n.Name,
			TargetBranch: "main",
			URL:          fmt.Sprintf("https://github.com/demo/sharko-addons/pull/%d", id),
			CreatedAt:    createdAt,
			UpdatedAt:    createdAt,
		})
	}

	estate := &GeneratedEstate{
		Clusters:             clusters,
		UnregisteredClusters: unregistered,
		Addons:               addons,
		Deployments:          deployments,
		GitPRs:               gitPRs,
	}

	// Pin one exemplar of every enumerated cluster/app state (maintainer
	// scope addition) BEFORE building the PR feed, so the PR-flow
	// exemplars (an open enable/disable/upgrade PR) can see the
	// post-pinning addon membership and stay semantically honest (an
	// "enable" PR targets an addon NOT on the cluster; "disable"/"upgrade"
	// target one that IS).
	applyStateCoverage(estate, rng, now)

	estate.TrackedPRs = generateTrackedPRs(estate, rng, now)

	return estate, nil
}

// prTitleAndBranch renders the title/branch-verb pair for one PR operation,
// shared by generateTrackedPRs' pinned exemplars and its random fill.
func prTitleAndBranch(op, addonName, clusterName string) (title, branchVerb string) {
	switch op {
	case prtracker.OpAddonEnable:
		return fmt.Sprintf("sharko: enable %s on %s", addonName, clusterName), "enable"
	case prtracker.OpAddonDisable:
		return fmt.Sprintf("sharko: disable %s on %s", addonName, clusterName), "disable"
	default:
		return fmt.Sprintf("sharko: upgrade %s on %s", addonName, clusterName), "upgrade"
	}
}

// generateTrackedPRs builds the "dozens of PRs" feed (S1): a seeded mix of
// open and recently-merged addon-enable/disable/upgrade PRs, each attached
// to a real generated cluster+addon pair, in the shape the PR tracker
// (internal/prtracker) stores.
//
// Every PR (pinned exemplars AND the random fill) respects addon
// membership: "enable" only ever targets an addon NOT currently on the
// cluster (an addon that hasn't landed yet — the ghost-pending-card case
// the Marketplace/catalog UI renders for an open enable PR); "disable" and
// "upgrade" only ever target one that IS on the cluster. The first three
// open PRs are pinned exemplars (maintainer coverage addition) so an open
// PR of EVERY operation type is guaranteed rather than left to chance.
func generateTrackedPRs(estate *GeneratedEstate, rng *rand.Rand, now time.Time) []prtracker.PRInfo {
	clusters := estate.Clusters
	addons := estate.Addons
	if len(clusters) == 0 || len(addons) == 0 {
		return nil
	}

	nextID := 100

	build := func(status string, minAgeDays, maxAgeDays int, c Cluster, a generatedAddon, op string) prtracker.PRInfo {
		title, branchVerb := prTitleAndBranch(op, a.Name, c.Name)
		ageDays := minAgeDays
		if maxAgeDays > minAgeDays {
			ageDays += rng.Intn(maxAgeDays - minAgeDays + 1)
		}
		createdAt := now.
			Add(-time.Duration(ageDays) * 24 * time.Hour).
			Add(-time.Duration(rng.Intn(1440)) * time.Minute)

		id := nextID
		nextID++
		return prtracker.PRInfo{
			PRID:       id,
			PRUrl:      fmt.Sprintf("https://github.com/demo/sharko-addons/pull/%d", id),
			PRBranch:   fmt.Sprintf("sharko/%s-%s-%s", branchVerb, a.Name, c.Name),
			PRTitle:    title,
			PRBase:     "main",
			Cluster:    c.Name,
			Addon:      a.Name,
			Operation:  op,
			User:       "sharko-bot",
			Source:     "ui",
			CreatedAt:  createdAt,
			LastStatus: status,
			LastPolled: now,
		}
	}

	// pickForOp finds a (cluster, addon) pair matching op's membership
	// requirement, scanning a handful of random draws before giving up (in
	// which case the caller falls back to whatever op the pair supports).
	pickForOp := func(op string) (Cluster, generatedAddon, string) {
		for attempt := 0; attempt < 20; attempt++ {
			c := clusters[rng.Intn(len(clusters))]
			a := addons[rng.Intn(len(addons))]
			_, enabled := c.Addons[a.Name]
			switch {
			case op == prtracker.OpAddonEnable && !enabled:
				return c, a, op
			case op != prtracker.OpAddonEnable && enabled:
				return c, a, op
			}
		}
		// Fall back: whatever the last draw actually supports.
		c := clusters[rng.Intn(len(clusters))]
		a := addons[rng.Intn(len(addons))]
		if _, enabled := c.Addons[a.Name]; enabled {
			ops := []string{prtracker.OpAddonDisable, prtracker.OpAddonUpgrade}
			return c, a, ops[rng.Intn(len(ops))]
		}
		return c, a, prtracker.OpAddonEnable
	}

	prs := make([]prtracker.PRInfo, 0, targetOpenPRs+targetMergedPRs)

	// Pinned exemplars — guarantee an open PR of every operation type
	// exists, not left to the random fill below.
	for _, op := range []string{prtracker.OpAddonEnable, prtracker.OpAddonDisable, prtracker.OpAddonUpgrade} {
		c, a, resolvedOp := pickForOp(op)
		prs = append(prs, build("open", 0, 3, c, a, resolvedOp))
	}

	ops := []string{prtracker.OpAddonEnable, prtracker.OpAddonDisable, prtracker.OpAddonUpgrade}
	for i := len(prs); i < targetOpenPRs; i++ {
		c, a, op := pickForOp(ops[rng.Intn(len(ops))])
		prs = append(prs, build("open", 0, 6, c, a, op))
	}
	for i := 0; i < targetMergedPRs; i++ {
		c, a, op := pickForOp(ops[rng.Intn(len(ops))])
		prs = append(prs, build("merged", 7, 29, c, a, op))
	}
	return prs
}
