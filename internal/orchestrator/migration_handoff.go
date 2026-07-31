// Package orchestrator — the v3 → v4 RUNTIME handoff (v4 Wave 2 review
// findings B-1 and H-2).
//
// migration_v3v4.go converts the repository. This file makes the running
// fleet survive that conversion. They are two halves of one operation, and
// before this file existed only the first half was implemented — which is
// why merging a migration PR on a live fleet uninstalled every addon.
//
// ─── what actually goes wrong ────────────────────────────────────────────
//
// A v3 repo's ApplicationSets do not live in the repo. They were rendered
// from templates/bootstrap/templates/addons-appset.yaml into the argocd
// namespace at bootstrap time, and they keep running there long after the
// repo has been rewritten. Each one has a matrix generator with two arms:
//
//	arm 1 — clusters, selected on a bare `<addon>: enabled` Secret label
//	arm 2 — git,      reading configuration/addons-clusters-values/<cluster>.yaml
//
// The migration PR destroys BOTH. It deletes the per-cluster values files
// (arm 2 has nothing to read), and once it merges the cluster reconciler
// rewrites the bare addon labels into `addons.sharko.dev/<addon>` (arm 1
// matches nothing). A matrix generator producing an empty result makes the
// ApplicationSet controller delete every Application it generated — and
// the v3 template stamps `resources-finalizer.argocd.argoproj.io` on each
// generated Application and sets no preserveResourcesOnDeletion, so those
// deletions cascade straight into the live workloads.
//
// Separately: nothing on the migration path ever applied the new
// engine.yaml to ArgoCD. Only the first-run init flow does
// (init.go's BootstrapArgoCD). So even a fleet that somehow survived the
// first problem would simply freeze, with no ApplicationSets left to
// manage it.
//
// ─── the sequence, and why it is this order ──────────────────────────────
//
// PREPARE — before the pull request is opened, while the old generators
// still produce their normal results:
//
//  1. For every old ApplicationSet whose cluster generator selects a bare
//     catalog-addon label: set preserveResourcesOnDeletion, and take the
//     ArgoCD resources finalizer off its Application template.
//  2. Take that same finalizer off the LIVE Applications it generated, so
//     the safety of the transition does not depend on the ApplicationSet
//     controller reconciling the template change before the PR merges.
//
// After PREPARE the old ApplicationSets are inert: they can lose their
// generators, they can be deleted outright, and either way not one
// running workload is touched.
//
// COMPLETE — after the pull request merges:
//
//  3. Delete the old ApplicationSets.
//  4. THEN apply engine.yaml to ArgoCD, which brings up the
//     engine's own ApplicationSets and regenerates the Applications.
//
// Step 3 must come before step 4, and the reason is a name collision. The
// v3 template names each generated Application `<addon>-<cluster>`
// (templates/bootstrap/templates/addons-appset.yaml, metadata.name
// `{{ $appset.name }}-{{ .name }}`). The engine chart names them exactly
// the same (charts/sharko-engine/templates/appset.yaml, metadata.name
// `{{ $name }}-{{ .name }}`). Two ApplicationSets cannot both own an
// Application of the same name — the second one to arrive cannot adopt an
// object another controller already owns. So the old owner has to be gone
// first.
//
// That identical naming is also what makes the handoff seamless rather
// than merely survivable. ArgoCD tracks which resources belong to an
// Application by stamping the APPLICATION NAME on them (the
// `app.kubernetes.io/instance` label, or the `argocd.argoproj.io/
// tracking-id` annotation when the install is configured that way). Same
// Application name means the same stamp, so when the engine's
// ApplicationSet creates `<addon>-<cluster>`, the workloads already
// running under that name are recognised as its own and adopted in place:
// no reinstall, no duplicate, no prune. Both templates also default the
// destination namespace the same way (`namespace` from the catalog entry,
// falling back to the addon name), so the tracking stamp matches on
// namespace too.
//
// The one thing that legitimately changes is the AppProject: v3 made one
// project per addon, named after the addon; the engine uses a single
// `sharko-addons` project. A project change does not affect resource
// tracking and does not prune anything.
//
// ─── when it refuses ─────────────────────────────────────────────────────
//
// A repo-only migration on a live fleet is exactly the trap above, so
// PREPARE refuses the whole migration when it cannot do its job: no
// ApplicationSet access wired, or a patch that failed. Nothing is written
// to git, the person gets a plain-words reason, and the fleet keeps
// running.
//
// The one case it skips silently is a fleet with nothing in it: a v3 repo
// whose cluster registry holds no clusters has no generated Applications
// to strand, so there is nothing to hand over. That is detected from the
// repo itself and needs no cluster access at all.
package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/config"
)

// RuntimeHandoff values for MigrateRequest.RuntimeHandoff.
const (
	// RuntimeHandoffAuto is the default (empty) mode: prepare the handoff
	// when the repo has clusters, skip it when it has none.
	RuntimeHandoffAuto = ""
	// RuntimeHandoffSkip is the explicit escape hatch. It means "I know
	// this repo has nothing running; migrate the files and do not touch
	// ArgoCD". Use it for a repo Sharko never bootstrapped into a cluster,
	// or one whose ArgoCD is already gone.
	RuntimeHandoffSkip = "skip"
)

// Handoff states reported on MigrationStatusResult and MigrateResult.
const (
	// HandoffStateNotNeeded — this repo has no running fleet to hand over.
	HandoffStateNotNeeded = "not_needed"
	// HandoffStatePrepared — the old ApplicationSets have been made safe;
	// the second half runs when the pull request merges.
	HandoffStatePrepared = "prepared"
	// HandoffStatePending — the repo is on v4 but the old ApplicationSets
	// are still there, or the engine has not been applied yet.
	HandoffStatePending = "pending"
	// HandoffStateComplete — the old ApplicationSets are gone and the
	// engine is running.
	HandoffStateComplete = "complete"
	// HandoffStateSkipped — the caller asked for the files only.
	HandoffStateSkipped = "skipped"
)

// RuntimeHandoffReport is the plain-words account of what the handoff did,
// carried on the migrate response and on migration status.
type RuntimeHandoffReport struct {
	// State is one of the HandoffState* constants above.
	State string `json:"state"`
	// Message is one sentence a person can read as-is.
	Message string `json:"message"`
	// ApplicationSets are the old ApplicationSets this handoff prepared
	// (or, after the second half, retired).
	ApplicationSets []string `json:"application_sets,omitempty"`
	// ReleasedApplications are the live Applications whose
	// delete-everything marker was removed, so their workloads outlive the
	// transition.
	ReleasedApplications []string `json:"released_applications,omitempty"`
	// EngineApplied reports whether engine.yaml has been
	// handed to ArgoCD.
	EngineApplied bool `json:"engine_applied"`
}

// SetApplicationSetManager wires in the ApplicationSet read+write surface
// the runtime handoff needs. nil (or skipping the call) means "no
// ApplicationSet access", which makes the migration refuse on any repo
// with a live fleet — see the package comment.
func (o *Orchestrator) SetApplicationSetManager(m appsets.Manager) {
	o.appSets = m
}

// prepareRuntimeHandoff is the pre-merge half. It runs inside
// MigrateV3ToV4, after the file map is built and validated but BEFORE the
// branch exists, so a refusal here leaves the repo untouched.
//
// clusterCount and addonNames come from the build, so the decision is made
// against the same repo contents the pull request was computed from.
func (o *Orchestrator) prepareRuntimeHandoff(ctx context.Context, mode string, clusterCount int, addonNames []string) (*RuntimeHandoffReport, error) {
	if mode == RuntimeHandoffSkip {
		return &RuntimeHandoffReport{
			State: HandoffStateSkipped,
			Message: "You asked to migrate the files only, so Sharko left ArgoCD alone. " +
				"If anything IS still running from the old setup, its ApplicationSets will lose " +
				"their inputs when this pull request merges and will remove what they installed.",
		}, nil
	}

	// An empty fleet is the one case that genuinely needs nothing. No
	// clusters in the v3 registry means no cluster ever matched an old
	// ApplicationSet's generator, so there are no generated Applications
	// to strand. Read straight off the repo — no cluster access needed.
	if clusterCount == 0 {
		return &RuntimeHandoffReport{
			State:   HandoffStateNotNeeded,
			Message: "This repo has no clusters registered, so there is nothing running for the migration to disturb.",
		}, nil
	}

	if o.appSets == nil {
		return nil, fmt.Errorf(
			"refusing to migrate: this repo has %s registered, and the ApplicationSets that keep their addons running "+
				"live in ArgoCD, not in the repo. This pull request would take away everything those ApplicationSets read, "+
				"and they would remove every addon they installed. Sharko cannot reach ArgoCD from here to make them safe first. "+
				"Connect Sharko to the ArgoCD that runs this fleet and try again. If nothing is actually running any more, "+
				"send runtime_handoff: \"skip\" to migrate the files only. Nothing was written",
			plainCount(clusterCount, "cluster", "clusters"))
	}

	all, err := o.appSets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"refusing to migrate: Sharko could not read the ApplicationSets that keep this fleet's addons running, "+
				"so it cannot make them safe before this pull request takes away what they read (%w). Nothing was written", err)
	}

	legacy := legacyAddonAppSets(all, addonNames)
	if len(legacy) == 0 {
		return &RuntimeHandoffReport{
			State: HandoffStateNotNeeded,
			Message: "Nothing in ArgoCD picks clusters by an addon label from this repo's addon list, " +
				"so this pull request cannot strand anything.",
		}, nil
	}

	report := &RuntimeHandoffReport{State: HandoffStatePrepared}
	for _, as := range legacy {
		if err := o.appSets.Preserve(ctx, as.Name); err != nil {
			return nil, fmt.Errorf(
				"refusing to migrate: Sharko could not make the ApplicationSet %q safe to retire, and merging this "+
					"pull request while it is still able to delete things would remove the addons it installed (%w). "+
					"Nothing was written", as.Name, err)
		}
		released, err := o.appSets.ReleaseGeneratedApplications(ctx, as.Name)
		if err != nil {
			return nil, fmt.Errorf(
				"refusing to migrate: Sharko could not take the delete-everything marker off the Applications that %q "+
					"created, so merging this pull request could still remove what they installed (%w). Nothing was written",
				as.Name, err)
		}
		report.ApplicationSets = append(report.ApplicationSets, as.Name)
		report.ReleasedApplications = append(report.ReleasedApplications, released...)
	}
	sort.Strings(report.ApplicationSets)
	sort.Strings(report.ReleasedApplications)

	report.Message = fmt.Sprintf(
		"%s from the old setup %s been set to leave running workloads alone, so merging this pull request cannot remove "+
			"anything. Once it merges, Sharko retires %s and starts the new engine in their place.",
		plainCount(len(report.ApplicationSets), "ApplicationSet", "ApplicationSets"),
		wasWereCount(len(report.ApplicationSets)),
		pluralIt(len(report.ApplicationSets)))
	return report, nil
}

// CompleteRuntimeHandoff is the post-merge half, and it is IDEMPOTENT:
// running it twice, or on a repo that never needed it, is a clean no-op.
// That matters because it is driven from the pull-request merge callback,
// which can fire again after a restart, and it is also exposed as an
// endpoint a person can press when the callback never arrived.
//
// Two steps, in this order and for the reason in the package comment:
// retire the old ApplicationSets, then hand engine.yaml to
// ArgoCD.
func (o *Orchestrator) CompleteRuntimeHandoff(ctx context.Context) (*RuntimeHandoffReport, error) {
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return nil, err
	}
	if !v4 {
		return &RuntimeHandoffReport{
			State:   HandoffStatePending,
			Message: "The migration pull request has not merged yet, so there is nothing to finish.",
		}, nil
	}

	report := &RuntimeHandoffReport{State: HandoffStateComplete}

	// Step 1 — retire the old ApplicationSets. Identified the same way
	// prepare identified them, against the addon names the repo now has:
	// the merged catalog is the same set of addons either side of the
	// migration, which is exactly what assertAddonSetUnchanged guarantees.
	if o.appSets != nil {
		addonNames, err := o.mergedAddonNames(ctx)
		if err != nil {
			return nil, err
		}
		all, listErr := o.appSets.List(ctx)
		if listErr != nil {
			return nil, fmt.Errorf(
				"could not read the ApplicationSets in ArgoCD, so Sharko stopped before retiring the old ones: %w", listErr)
		}
		for _, as := range legacyAddonAppSets(all, addonNames) {
			// Never delete one that is still able to prune. Either prepare
			// never ran for it (a hand-merged PR, a fleet Sharko has only
			// just been pointed at) or the patch was undone since — and
			// deleting it in that state is the workload-removing move this
			// whole file exists to prevent.
			if !as.DeletionSafe() {
				report.State = HandoffStatePending
				report.Message = fmt.Sprintf(
					"The ApplicationSet %q from the old setup is still set to remove what it installed, so Sharko has "+
						"left it alone rather than risk taking your addons down. Set preserveResourcesOnDeletion: true on "+
						"it, then run this again.", as.Name)
				return report, nil
			}
			if err := o.appSets.Delete(ctx, as.Name); err != nil {
				return nil, fmt.Errorf("removing the old ApplicationSet %q: %w", as.Name, err)
			}
			report.ApplicationSets = append(report.ApplicationSets, as.Name)
		}
		sort.Strings(report.ApplicationSets)
	}

	// Step 2 — hand the engine pin to ArgoCD. This is the step that was
	// missing entirely (review finding H-2): only the first-run init flow
	// ever called it, so a migrated repo had a perfectly good
	// engine.yaml that nothing had applied.
	if o.argocd == nil {
		report.State = HandoffStatePending
		report.Message = "The old ApplicationSets are retired, but Sharko has no ArgoCD connection to start the new engine with. " +
			"Connect ArgoCD and run this again — or apply engine.yaml by hand."
		return report, nil
	}
	pin, err := o.ReadRootAppTemplate(ctx)
	if err != nil {
		return nil, err
	}
	if err := o.BootstrapArgoCD(ctx, pin); err != nil {
		return nil, fmt.Errorf("starting the new engine from %s: %w", EnginePinPath, err)
	}
	report.EngineApplied = true
	report.Message = "The old setup is retired and the new engine is running. Your addons keep the same names, " +
		"so ArgoCD picked up what was already installed rather than reinstalling it."
	return report, nil
}

// mergedAddonNames returns every addon name the v4 repo knows about:
// the shipped curated catalog plus the repo's own catalog.yaml
// delta. These are the label keys the OLD ApplicationSets selected on.
func (o *Orchestrator) mergedAddonNames(ctx context.Context) ([]string, error) {
	names := map[string]bool{}
	if o.curated != nil {
		for _, e := range o.curated.Entries() {
			if e.Name != "" {
				names[e.Name] = true
			}
		}
	}
	if body, ok := o.readFileIfExists(ctx, config.AddonCatalogPath); ok && len(strings.TrimSpace(string(body))) > 0 {
		delta, err := config.LoadAddonCatalog(body)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
		}
		for name := range delta.Addons {
			if name != "" {
				names[name] = true
			}
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// legacyAddonAppSets picks the ApplicationSets the migration would strand:
// the ones whose cluster generator selects on a BARE addon name.
//
// This is the same identification the takeover preflight uses
// (appsets.SelectingLabelKeys), pointed at the addon names rather than at
// a takeover's carried labels. It cannot mistake an engine ApplicationSet
// for an old one: the engine selects `addons.sharko.dev/<addon>`, which is
// a different label key from the bare `<addon>` the v3 template used.
func legacyAddonAppSets(all []appsets.ApplicationSetInfo, addonNames []string) []appsets.ApplicationSetInfo {
	if len(addonNames) == 0 {
		return nil
	}
	return appsets.SelectingLabelKeys(all, addonNames)
}

// ─── small wording helpers ───────────────────────────────────────────────

func plainCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func wasWereCount(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

func pluralIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
