// Package takeover computes the preflight report Sharko shows before it
// takes over a cluster that ArgoCD already manages (v4 Wave 2, Epic 6,
// stories 6.1 and 6.2).
//
// The whole package is pure: it takes a snapshot of facts someone else
// gathered (the existing cluster Secret's owner and labels, the
// ApplicationSets in the argocd namespace, the Applications pointing at the
// cluster, the names Sharko already has registered) and turns them into a
// report. No K8s calls, no git calls, no clock, no randomness — so the same
// facts always produce the same report, and every sentence in it can be
// tested without a cluster.
//
// Two rules shape everything here:
//
//  1. Every finding says what it means and what to do about it, in words a
//     person who is not a developer can act on without going to look
//     something up. No jargon, no bare field names, no "see the docs".
//  2. The report NEVER causes anything to happen. It is a read. The
//     confirm gate lives in the takeover call, not here.
package takeover

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Status is how a single check came out.
type Status string

const (
	// StatusOK — nothing to do about this one.
	StatusOK Status = "ok"
	// StatusWarning — the takeover can go ahead, but the person has to
	// read this and decide. Warnings must be acknowledged explicitly.
	StatusWarning Status = "warning"
	// StatusBlocked — the takeover must not go ahead until this is fixed.
	// Re-running the preflight after fixing it turns this one green.
	StatusBlocked Status = "blocked"
)

// Finding IDs. Stable strings — the UI keys off them, and a re-run of the
// preflight after a fix must produce the SAME id with a better status.
const (
	FindingSecretOwner   = "secret-owner"
	FindingAppSetSafety  = "appset-deletion-safety"
	FindingApplications  = "cluster-applications"
	FindingNameCollision = "name-collision"
)

// Finding is one check's result, in plain language.
type Finding struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`

	// Detail is the fact: what we actually found.
	Detail string `json:"detail"`
	// WhatItMeans explains the consequence in ordinary words.
	WhatItMeans string `json:"what_it_means"`
	// WhatToDo is the action. On an ok finding it says there is nothing
	// to do, so the field is never empty and the UI never has a hole.
	WhatToDo string `json:"what_to_do"`

	// ApplicationSets / Applications carry the names behind the sentence
	// so the UI can list them without re-parsing Detail.
	ApplicationSets []string `json:"application_sets,omitempty"`
	Applications    []string `json:"applications,omitempty"`
}

// Report is the whole preflight result.
type Report struct {
	Cluster string `json:"cluster"`

	// Ready is true when nothing is blocked. A ready report can still
	// carry warnings — see NeedsAcknowledgement.
	Ready bool `json:"ready"`
	// NeedsAcknowledgement is true when at least one finding is a
	// warning. The takeover call refuses unless the caller says, in so
	// many words, that they have read the warnings.
	NeedsAcknowledgement bool `json:"needs_acknowledgement"`

	// Summary is the one-sentence version, for the top of the page.
	Summary string `json:"summary"`

	Findings []Finding `json:"findings"`

	// Server is the cluster's API address as ArgoCD currently records it.
	// The takeover keeps this EXACTLY as it is.
	Server string `json:"server,omitempty"`

	// LegacyLabels are the labels already on the cluster's Secret that
	// are not Sharko's. Takeover carries every one of them over by
	// default — this is the list a person is shown before they confirm.
	LegacyLabels map[string]string `json:"legacy_labels,omitempty"`

	// LegacyLabelsSelectedBy maps each legacy label key to the
	// ApplicationSets that select on it, so the "who would notice if this
	// went away" question is answerable straight from the report.
	LegacyLabelsSelectedBy map[string][]string `json:"legacy_labels_selected_by,omitempty"`
}

// Inputs is the snapshot the report is computed from. Everything is
// already-gathered data; nothing here is fetched.
type Inputs struct {
	// Cluster is the cluster name as ArgoCD knows it.
	Cluster string

	// SecretFound is false when there is no cluster Secret with that name
	// in the argocd namespace at all.
	SecretFound bool
	// Server is the cluster's API address from the existing Secret.
	Server string
	// SecretLabels / SecretAnnotations are the live metadata of the
	// existing Secret.
	SecretLabels      map[string]string
	SecretAnnotations map[string]string
	// ManagedBy is the app.kubernetes.io/managed-by label value ("" when
	// nobody claims it).
	ManagedBy string
	// ForeignOwnerAppName / ForeignOwnerConfidence / ForeignOwnerFound
	// mirror what ArgoCD's own tracking markers say about who renders
	// this Secret from git. Confidence is "hard" or "soft".
	ForeignOwnerAppName    string
	ForeignOwnerConfidence string
	ForeignOwnerFound      bool

	// ApplicationSets is every ApplicationSet in the argocd namespace.
	ApplicationSets []appsets.ApplicationSetInfo
	// ApplicationSetsError is non-empty when the ApplicationSets could
	// not be read at all (no permission, no in-cluster client). The
	// deletion-safety check then reports a warning instead of pretending
	// everything is fine.
	ApplicationSetsError string

	// Applications is every ArgoCD Application whose destination is this
	// cluster.
	Applications []string

	// RegisteredClusters is every cluster name Sharko already has in its
	// own fleet record.
	RegisteredClusters []string
	// RegisteredClustersError is non-empty when that list could not be read
	// at all. The name check then blocks instead of reporting the name as
	// free — "I could not look" and "I looked and there is nothing" are two
	// different answers, and only one of them is safe to act on.
	RegisteredClustersError string
	// AlreadyTakenOver is true when THIS cluster is the one Sharko
	// already manages under that name — a re-run after a successful
	// takeover, not a collision with a different cluster.
	AlreadyTakenOver bool
}

// SharkoOwnedLabelKey reports whether a label key on a cluster Secret is
// one Sharko itself writes. Everything else on the Secret came from
// somewhere else and is what this package calls a legacy label.
//
// The v4 addon vocabulary is namespaced ("addons.sharko.dev/<addon>"), so
// unlike the v3 world there is no guessing here: an unqualified key such as
// "env" or "region" on a brownfield Secret is somebody else's, full stop.
func SharkoOwnedLabelKey(key string) bool {
	switch key {
	case models.LabelConnectivityCheck, models.LabelConnectivityCheckLegacy:
		return true
	case "app.kubernetes.io/managed-by":
		return true
	}
	return models.IsV4AddonLabelKey(key)
}

// ArgoCDSecretTypeLabel is ArgoCD's own marker that makes a Secret count as
// a cluster. It belongs to neither side and must survive everything —
// dropping it makes ArgoCD forget the cluster exists.
const ArgoCDSecretTypeLabel = "argocd.argoproj.io/secret-type"

// LegacyLabels splits a live label map down to the keys that came from
// whoever managed this cluster before Sharko. ArgoCD's own secret-type
// marker is excluded: it is not the old owner's, it is ArgoCD's, and it is
// never a candidate for dropping.
func LegacyLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		if k == ArgoCDSecretTypeLabel || SharkoOwnedLabelKey(k) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SortedKeys is the deterministic key order used everywhere a label map is
// turned into a sentence or a list.
func SortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Preflight runs the four checks and returns the report.
func Preflight(in Inputs) Report {
	legacy := LegacyLabels(in.SecretLabels)

	r := Report{
		Cluster:      in.Cluster,
		Server:       in.Server,
		LegacyLabels: legacy,
	}

	r.Findings = append(r.Findings,
		checkSecretOwner(in),
		checkApplicationSetSafety(in),
		checkApplications(in),
		checkNameCollision(in),
	)

	if len(legacy) > 0 {
		selectedBy := map[string][]string{}
		for _, key := range SortedKeys(legacy) {
			var names []string
			for _, as := range in.ApplicationSets {
				if as.SelectsLabelKey(key) {
					names = append(names, as.Name)
				}
			}
			if len(names) > 0 {
				sort.Strings(names)
				selectedBy[key] = names
			}
		}
		if len(selectedBy) > 0 {
			r.LegacyLabelsSelectedBy = selectedBy
		}
	}

	blocked, warnings := 0, 0
	for _, f := range r.Findings {
		switch f.Status {
		case StatusBlocked:
			blocked++
		case StatusWarning:
			warnings++
		}
	}
	r.Ready = blocked == 0
	r.NeedsAcknowledgement = warnings > 0
	r.Summary = summarize(in.Cluster, blocked, warnings)
	return r
}

func summarize(cluster string, blocked, warnings int) string {
	switch {
	case blocked > 0 && warnings > 0:
		return fmt.Sprintf(
			"%s is not ready to take over yet: %s to fix first, and %s to read before you go ahead.",
			cluster, plural(blocked, "thing", "things"), plural(warnings, "thing", "things"))
	case blocked > 0:
		return fmt.Sprintf("%s is not ready to take over yet: %s to fix first.",
			cluster, plural(blocked, "thing", "things"))
	case warnings > 0:
		return fmt.Sprintf(
			"%s can be taken over, but there %s %s to read and agree to first.",
			cluster, isAre(warnings), plural(warnings, "thing", "things"))
	default:
		return fmt.Sprintf("%s is ready to take over — every check came back clean.", cluster)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ─────────────────────────────────────────────────────────────────────────
// Check (a) — who owns the existing cluster Secret
// ─────────────────────────────────────────────────────────────────────────

func checkSecretOwner(in Inputs) Finding {
	f := Finding{ID: FindingSecretOwner, Title: "Who owns this cluster's connection today"}

	if !in.SecretFound {
		f.Status = StatusBlocked
		f.Detail = fmt.Sprintf("ArgoCD has no connection saved for %q.", in.Cluster)
		f.WhatItMeans = "There is nothing to take over. Taking over means becoming the owner of a connection that already exists — this cluster does not have one."
		f.WhatToDo = "If this cluster is new to ArgoCD, register it with Sharko instead of taking it over. If you expected it to be here, check you have the cluster name exactly right."
		return f
	}

	legacy := LegacyLabels(in.SecretLabels)

	switch {
	case in.ManagedBy == "sharko":
		f.Status = StatusOK
		f.Detail = fmt.Sprintf("Sharko already owns the connection for %q.", in.Cluster)
		f.WhatItMeans = "This cluster has already been taken over. Running the takeover again changes nothing."
		f.WhatToDo = "Nothing to do."
		return f

	case in.ManagedBy != "":
		// Another tool's ownership marker BLOCKS — it does not warn. That
		// tool can write the connection again and undo the takeover, so
		// there is no acknowledgment that makes going ahead safe. The path
		// through is: stop the old manager, remove its marker (the marker
		// does not come off by itself), re-run this check.
		f.Status = StatusBlocked
		f.Detail = fmt.Sprintf("The connection is marked as owned by %q.", in.ManagedBy)
		f.WhatItMeans = fmt.Sprintf(
			"Something called %q wrote this connection and can write it again. If it does, it puts its own connection back — and that undoes the takeover.",
			in.ManagedBy)
		f.WhatToDo = fmt.Sprintf(
			"Stop or retire whatever %q is — or stop it from managing this cluster's connection. Then remove its ownership marker from the connection: the marker does not come off by itself when the tool is turned off, and this check stays red while it is there. Once both are done, run this check again. It goes green, or drops to a warning asking you to confirm nobody manages this connection — and you approve the takeover from there.",
			in.ManagedBy)
		return f

	case in.ForeignOwnerFound && in.ForeignOwnerConfidence == "hard":
		// A hard tracking match BLOCKS too: the named ArgoCD application
		// really does render this exact Secret from Git, so its next sync
		// would put its own version back no matter what anyone acknowledged.
		f.Status = StatusBlocked
		f.Detail = fmt.Sprintf("The connection is being deployed by the ArgoCD application %q.", in.ForeignOwnerAppName)
		f.WhatItMeans = fmt.Sprintf(
			"ArgoCD application %q builds this connection from a file in Git. If the takeover went ahead, the next time that application syncs it would put its own version back and the two would fight over the cluster.",
			in.ForeignOwnerAppName)
		f.WhatToDo = fmt.Sprintf(
			"Remove this cluster's connection from whatever %q deploys — or stop that application syncing it — then run this check again.",
			in.ForeignOwnerAppName)
		return f

	case in.ForeignOwnerFound:
		f.Status = StatusWarning
		f.Detail = fmt.Sprintf("The connection carries a marker naming %q.", in.ForeignOwnerAppName)
		f.WhatItMeans = fmt.Sprintf(
			"Something called %q may have created this connection — either an ArgoCD application or a plain Helm install. The marker is not proof, but if that thing runs again it could put its own version back.",
			in.ForeignOwnerAppName)
		f.WhatToDo = fmt.Sprintf(
			"Have a look at what %q is. If it still deploys this cluster's connection, stop it from doing so first. If the marker is just left over from an old install, acknowledge this warning and continue.",
			in.ForeignOwnerAppName)
		return f
	}

	// No ownership marker at all. This is a WARNING, not a clean pass:
	// Sharko can only see markers, and "no marker" is not proof that
	// nothing manages this connection — a tool that leaves no marker could
	// still own it. The human confirms and acknowledges; Sharko never
	// guesses.
	f.Status = StatusWarning
	if len(legacy) > 0 {
		f.Detail = fmt.Sprintf(
			"Nobody has claimed the connection for %q. It carries %s that Sharko will keep: %s.",
			in.Cluster, plural(len(legacy), "label", "labels"), strings.Join(SortedKeys(legacy), ", "))
		f.WhatItMeans = "Nothing has put its name on this connection, which usually means nothing manages it. But Sharko can only see the markers tools leave behind, so this is not proof — a tool that leaves no marker could still be managing it. The labels already on it stay exactly as they are."
	} else {
		f.Detail = fmt.Sprintf("Nobody has claimed the connection for %q.", in.Cluster)
		f.WhatItMeans = "Nothing has put its name on this connection, which usually means nothing manages it. But Sharko can only see the markers tools leave behind, so this is not proof — a tool that leaves no marker could still be managing it."
	}
	f.WhatToDo = "Check for yourself that nothing else manages this cluster's connection — Terraform, a script, another team's tooling. When you are sure, acknowledge this warning and approve the takeover."
	return f
}

// ─────────────────────────────────────────────────────────────────────────
// Check (b) — which ApplicationSets are NOT deletion-safe
// ─────────────────────────────────────────────────────────────────────────

func checkApplicationSetSafety(in Inputs) Finding {
	f := Finding{ID: FindingAppSetSafety, Title: "What happens if this cluster stops matching an ApplicationSet"}

	if in.ApplicationSetsError != "" {
		f.Status = StatusWarning
		f.Detail = "Sharko could not read the ApplicationSets in the argocd namespace: " + in.ApplicationSetsError
		f.WhatItMeans = "Sharko cannot tell you whether any of them would delete running workloads if this cluster's labels change. It is not saying they would — it is saying it does not know."
		f.WhatToDo = "Give Sharko permission to read ApplicationSets (the Sharko chart asks for it, so re-installing or upgrading the chart is usually enough), then run this check again. Or check the ApplicationSets yourself before continuing."
		return f
	}

	if len(in.ApplicationSets) == 0 {
		f.Status = StatusOK
		f.Detail = "There are no ApplicationSets in the argocd namespace."
		f.WhatItMeans = "Nothing is set up to add or remove Applications when a cluster's labels change, so changing labels on this cluster cannot delete anything."
		f.WhatToDo = "Nothing to do."
		return f
	}

	unsafe := appsets.NotDeletionSafe(in.ApplicationSets)
	if len(unsafe) == 0 {
		f.Status = StatusOK
		f.Detail = fmt.Sprintf("All %d ApplicationSets are set up to leave running workloads alone.", len(in.ApplicationSets))
		f.WhatItMeans = "Even if this cluster stops matching one of them, whatever it installed keeps running."
		f.WhatToDo = "Nothing to do."
		return f
	}

	names := appsets.Names(unsafe)
	f.Status = StatusWarning
	f.ApplicationSets = names
	f.Detail = fmt.Sprintf("%s would delete what it installed if this cluster stopped matching it: %s.",
		plural(len(unsafe), "ApplicationSet", "ApplicationSets"), strings.Join(names, ", "))

	var reasons []string
	for _, a := range unsafe {
		reasons = append(reasons, fmt.Sprintf("%s — %s", a.Name, a.WhyUnsafe()))
	}
	f.WhatItMeans = "Taking over does not change any labels by itself, so nothing is deleted today. But these are the ones to be careful with later, when you drop the old labels: " +
		strings.Join(reasons, "; ") + "."
	f.WhatToDo = "For each one, either set it to keep its resources when an Application goes away, or set it to never delete Applications — and do that before you drop this cluster's old labels. If you would rather keep them as they are, that is fine: acknowledge this warning, and take extra care at the drop-labels step."
	return f
}

// ─────────────────────────────────────────────────────────────────────────
// Check (c) — the full list of Applications targeting the cluster
// ─────────────────────────────────────────────────────────────────────────

func checkApplications(in Inputs) Finding {
	f := Finding{ID: FindingApplications, Title: "What is running on this cluster right now"}

	apps := append([]string(nil), in.Applications...)
	sort.Strings(apps)
	f.Applications = apps

	if len(apps) == 0 {
		f.Status = StatusOK
		f.Detail = "No ArgoCD Applications are pointed at this cluster."
		f.WhatItMeans = "Nothing is being deployed here through ArgoCD today, so there is nothing that could be disturbed."
		f.WhatToDo = "Nothing to do."
		return f
	}

	f.Status = StatusOK
	f.Detail = fmt.Sprintf("%s deployed to this cluster: %s.",
		plural(len(apps), "ArgoCD Application is", "ArgoCD Applications are"), strings.Join(apps, ", "))
	f.WhatItMeans = "This is the full list of what would be affected if this cluster ever stopped matching the ApplicationSets that created them. The takeover itself does not touch any of them."
	f.WhatToDo = "Read the list and make sure you recognise everything on it. If something here is business-critical, do the takeover during a quiet window."
	return f
}

// ─────────────────────────────────────────────────────────────────────────
// Check (d) — name collision with Sharko's registered clusters
// ─────────────────────────────────────────────────────────────────────────

func checkNameCollision(in Inputs) Finding {
	f := Finding{ID: FindingNameCollision, Title: "Whether the name is already taken"}

	// Could not read Sharko's own list of clusters. This blocks rather than
	// warns: the thing on the other side of this check is stamping Sharko's
	// ownership onto a live ArgoCD connection, and if the name turns out to
	// belong to a DIFFERENT cluster Sharko already has, that cluster's
	// settings start being applied to this one. Guessing "the name is free"
	// is not an option a person would accept if they were asked.
	if in.RegisteredClustersError != "" {
		f.Status = StatusBlocked
		f.Detail = "Sharko could not read its own list of clusters: " + in.RegisteredClustersError
		f.WhatItMeans = fmt.Sprintf(
			"Sharko cannot tell you whether it already has a cluster called %q. It is not saying the name is taken — it is saying it does not know, and taking over on a name that turns out to belong to a different cluster would start applying that cluster's settings to this one.",
			in.Cluster)
		f.WhatToDo = "Check Sharko's Git connection is working, then run this check again. It goes green as soon as the list can be read."
		return f
	}

	registered := false
	for _, name := range in.RegisteredClusters {
		if name == in.Cluster {
			registered = true
			break
		}
	}

	if !registered {
		f.Status = StatusOK
		f.Detail = fmt.Sprintf("Sharko has no cluster called %q yet.", in.Cluster)
		f.WhatItMeans = "The name is free, so this cluster can keep the name it already has."
		f.WhatToDo = "Nothing to do."
		return f
	}

	if in.AlreadyTakenOver {
		f.Status = StatusOK
		f.Detail = fmt.Sprintf("Sharko already has %q — and it is this same cluster.", in.Cluster)
		f.WhatItMeans = "This cluster has been taken over already. Running it again will not create a duplicate."
		f.WhatToDo = "Nothing to do."
		return f
	}

	f.Status = StatusBlocked
	f.Detail = fmt.Sprintf("Sharko already has a different cluster called %q.", in.Cluster)
	f.WhatItMeans = "Two clusters cannot share a name. If this went ahead, Sharko's settings for the existing one would start being applied to this one."
	f.WhatToDo = fmt.Sprintf(
		"Rename one of them so the names do not clash. If the %q Sharko already has is not in use any more, remove it first — then run this check again and it will go green.",
		in.Cluster)
	return f
}
