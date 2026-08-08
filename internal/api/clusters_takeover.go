package api

// clusters_takeover.go — brownfield takeover (v4 Wave 2, Epic 6).
//
// Four endpoints, in the order an operator meets them:
//
//	GET  /clusters/{name}/takeover/preflight            — story 6.1 + 6.2
//	POST /clusters/{name}/takeover                      — story 6.3
//	POST /clusters/{name}/takeover/legacy-labels/drop   — story 6.4
//	GET  /clusters/{name}/unregister/consequences       — story 6.5
//
// The promise that shapes every one of them: nothing is deleted, moved or
// relabelled until somebody says yes in so many words. The two reads write
// nothing at all and can be run as often as you like. The two writes both
// refuse outright without an explicit confirmation in the request body, and
// both check the state again themselves rather than trusting a preflight
// the caller may have run an hour ago.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// TakeoverRequest is the body of POST /clusters/{name}/takeover.
type TakeoverRequest struct {
	// Yes is the explicit confirmation. Without it nothing happens.
	Yes bool `json:"yes"`
	// DryRun returns the plan and writes nothing. A dry run does not need
	// a confirmation.
	DryRun bool `json:"dry_run,omitempty"`
	// AcknowledgedFindings names the warnings the caller has read, by the
	// finding id the preflight gave each one (for example
	// "appset-deletion-safety"). It is separate from Yes on purpose: "I
	// want to do this" and "I have read the things that could go wrong"
	// are two different statements, and folding them into one makes the
	// second one free.
	//
	// It is a list of ids rather than a single "yes I read them" flag
	// because the checks are re-run on this very call. A flag would
	// silently cover a warning that appeared in the seconds since the
	// caller looked — someone else pointing an ApplicationSet at this
	// cluster, say. An id can only cover a warning that was actually on
	// the screen. Any warning in the fresh report whose id is not in this
	// list comes back as a 409 naming it.
	AcknowledgedFindings []string `json:"acknowledged_findings,omitempty"`
	// PreserveLegacyLabels carries the previous owner's labels over to
	// Sharko's connection. Defaults to true — omit it and the labels are
	// kept. Set it to false only if you already know nothing selects on
	// them.
	PreserveLegacyLabels *bool `json:"preserve_legacy_labels,omitempty"`
	// Region is optional and only recorded on the fleet entry.
	Region string `json:"region,omitempty"`
	// AutoMerge overrides the connection's auto-merge default for this
	// pull request only.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// TakeoverResponse is what a takeover call returns.
type TakeoverResponse struct {
	Cluster string `json:"cluster"`
	// Status is "success", "partial" or "planned" (a dry run).
	Status string `json:"status"`
	// Server is the cluster's API address — unchanged by the takeover,
	// echoed back so the caller can see that for themselves.
	Server string `json:"server,omitempty"`
	// PreservedLabels are the previous owner's labels now carried on
	// Sharko's connection.
	PreservedLabels map[string]string `json:"preserved_labels,omitempty"`
	// DroppedLabels are the previous owner's labels removed during the
	// takeover, when the caller asked for that.
	DroppedLabels map[string]string `json:"dropped_labels,omitempty"`
	// SecretSwapped is true when the connection's owner actually changed
	// on this call. False with AlreadyOwned true means it was already
	// Sharko's.
	SecretSwapped bool `json:"secret_swapped"`
	AlreadyOwned  bool `json:"already_owned,omitempty"`
	// ProtectionRepaired is true when Sharko already owned the connection
	// but had no record of which labels came from the previous owner, and
	// this call wrote that record back.
	ProtectionRepaired bool `json:"protection_repaired,omitempty"`

	Git       *orchestrator.GitResult    `json:"git,omitempty"`
	DryRun    *orchestrator.DryRunResult `json:"dry_run,omitempty"`
	Preflight *takeover.Report           `json:"preflight,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
	Message  string   `json:"message"`
}

// DropLegacyLabelsRequest is the body of the drop-labels call.
type DropLegacyLabelsRequest struct {
	// Yes is the explicit confirmation.
	Yes bool `json:"yes"`
	// DryRun reports what would be removed and removes nothing.
	DryRun bool `json:"dry_run,omitempty"`
	// Labels narrows the drop to specific keys. Empty means "every label
	// the takeover carried over".
	Labels []string `json:"labels,omitempty"`
	// AcknowledgedFindings names the warnings the caller has read, by the
	// ids the dry run returned in warning_ids. Same reasoning as the
	// takeover call: the warnings are recomputed on this request, so only
	// an id can prove a warning was actually seen.
	AcknowledgedFindings []string `json:"acknowledged_findings,omitempty"`
}

// DropLegacyLabelsResponse reports the outcome.
type DropLegacyLabelsResponse struct {
	Cluster string `json:"cluster"`
	Status  string `json:"status"`
	// Removed are the label keys actually taken off the connection.
	Removed []string `json:"removed,omitempty"`
	// Remaining are the carried-over labels still on the connection.
	Remaining []string `json:"remaining,omitempty"`
	// Warnings names any ApplicationSet that selects on a label being
	// removed, and says what it would do about it.
	Warnings []string `json:"warnings,omitempty"`
	// WarningIDs are the stable ids of those same warnings, in the same
	// order, to send back in acknowledged_findings.
	WarningIDs []string `json:"warning_ids,omitempty"`
	Message    string   `json:"message"`
}

// UnregisterConsequence is one thing that will happen if you go ahead.
type UnregisterConsequence struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	WhatItMeans string `json:"what_it_means"`
	// Severity is "info" or "warning". Warnings are the ones that can
	// stop things running.
	Severity string `json:"severity"`
}

// UnregisterConsequencesResponse is the read that must come before an
// unregister.
type UnregisterConsequencesResponse struct {
	Cluster string `json:"cluster"`
	Summary string `json:"summary"`
	// ConfirmationRequired restates how to actually do it, so the caller
	// never has to guess the shape of the confirmation.
	ConfirmationRequired string                  `json:"confirmation_required"`
	Consequences         []UnregisterConsequence `json:"consequences"`
}

// ─────────────────────────────────────────────────────────────────────────
// Shared gathering
// ─────────────────────────────────────────────────────────────────────────

// gatherPreflightInputs collects everything takeover.Preflight needs. Every
// source degrades in the direction that makes a person look rather than
// assume: a failure to read the ApplicationSets is reported as "I could not
// check", not as "all clear".
func (s *Server) gatherPreflightInputs(r *http.Request, name string) (takeover.Inputs, error) {
	ctx := r.Context()
	in := takeover.Inputs{Cluster: name}

	// (a) who owns the existing cluster Secret.
	if s.argoSecretManager == nil {
		return in, fmt.Errorf("Sharko is not connected to the cluster it runs in, so it cannot read this cluster's connection — the takeover checks need an in-cluster install")
	}
	detail, err := s.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		return in, err
	}
	in.SecretFound = detail.Found
	in.Server = detail.Server
	in.SecretLabels = detail.Labels
	in.SecretAnnotations = detail.Annotations
	in.ManagedBy = detail.ManagedBy
	in.ForeignOwnerAppName = detail.ForeignOwnerAppName
	in.ForeignOwnerConfidence = string(detail.ForeignOwnerConfidence)
	in.ForeignOwnerFound = detail.ForeignOwnerFound

	// (b) the ApplicationSets and how they behave on deletion.
	if s.appSetReader == nil {
		in.ApplicationSetsError = "Sharko has no read access to ApplicationSets in this install"
	} else if list, listErr := s.appSetReader.List(ctx); listErr != nil {
		in.ApplicationSetsError = listErr.Error()
	} else {
		in.ApplicationSets = list
	}

	// (c) the Applications pointed at this cluster.
	if ac, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil {
		if apps, appsErr := argocd.NewService(ac).GetClusterApplications(ctx, name); appsErr == nil {
			for _, app := range apps {
				in.Applications = append(in.Applications, app.Name)
			}
		}
	}

	// (d) a name clash with a cluster Sharko already has.
	names, namesErr := s.registeredClusterNames(r)
	if namesErr != nil {
		in.RegisteredClustersError = namesErr.Error()
	}
	in.RegisteredClusters = names
	in.AlreadyTakenOver = detail.ManagedBy == argosecrets.ManagedByValue

	return in, nil
}

// registeredClusterNames reads the v4 fleet record and returns the cluster
// names in it.
//
// A fleet record that is not there yet is an ordinary answer: no names, no
// error. Anything else — no Git connection, an unreadable file, a file that
// does not parse — comes back as an error, and the name-collision check then
// says it could not check rather than "the name is free".
//
// The difference is not cosmetic. "The name is free" is what lets the
// takeover stamp Sharko's ownership onto the ArgoCD connection of a cluster
// that happens to share a name with one Sharko already has — two different
// clusters, one name, and the settings of the first start being applied to
// the second.
func (s *Server) registeredClusterNames(r *http.Request) ([]string, error) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return nil, fmt.Errorf("Sharko has no Git connection to read its own list of clusters from: %w", err)
	}
	branch := s.gitopsConfig().BaseBranch
	if branch == "" {
		branch = "main"
	}
	data, err := gp.GetFileContent(r.Context(), orchestrator.V4ManagedClustersPath, branch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			// No fleet record yet — Sharko genuinely has no clusters.
			return nil, nil
		}
		return nil, fmt.Errorf("Sharko could not read its own list of clusters from %s on the %s branch: %w",
			orchestrator.V4ManagedClustersPath, branch, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	spec, err := models.LoadManagedClusters(data)
	if err != nil {
		return nil, fmt.Errorf("Sharko could not make sense of its own list of clusters in %s: %w",
			orchestrator.V4ManagedClustersPath, err)
	}
	out := make([]string, 0, len(spec.Clusters))
	for _, c := range spec.Clusters {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────
// 6.1 + 6.2 — the preflight
// ─────────────────────────────────────────────────────────────────────────

// handleClusterTakeoverPreflight godoc
//
// @Summary Check whether a cluster can be taken over
// @Description Runs four read-only checks against a cluster ArgoCD already manages: who owns its connection today, which ApplicationSets would delete workloads if it stopped matching them, everything ArgoCD is deploying to it, and whether the name clashes with a cluster Sharko already has.
// @Description Writes nothing. Safe to re-run — fix something and run it again to see it go green.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} takeover.Report "The preflight report"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 503 {object} map[string]interface{} "Sharko cannot read the cluster's connection in this install"
// @Router /clusters/{name}/takeover/preflight [get]
func (s *Server) handleClusterTakeoverPreflight(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.takeover.preflight") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	in, err := s.gatherPreflightInputs(r, name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, takeover.Preflight(in))
}

// ─────────────────────────────────────────────────────────────────────────
// 6.3 — the takeover itself
// ─────────────────────────────────────────────────────────────────────────

// handleClusterTakeover godoc
//
// @Summary Take over a cluster ArgoCD already manages
// @Description Makes Sharko the owner of an existing cluster's ArgoCD connection, keeping the same name, the same API address and — by default — every label the previous owner left on it. Adds the cluster to Sharko's fleet through a pull request and creates an empty addon file for it; no addon is turned on.
// @Description Nothing is written without "yes": true in the body. The checks are re-run on this call, and every warning they raise has to be named in "acknowledged_findings" by its finding id — a warning that appeared since you last looked comes back as a 409 naming it. Send "dry_run": true to see the plan and change nothing.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body api.TakeoverRequest true "Takeover request"
// @Success 200 {object} api.TakeoverResponse "Cluster taken over (or the plan, on a dry run)"
// @Success 207 {object} api.TakeoverResponse "Partly done — see the message"
// @Failure 400 {object} map[string]interface{} "Bad request, or the confirmation is missing"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 409 {object} map[string]interface{} "The preflight is blocking, or a warning was not named in acknowledged_findings, or the repo has not been migrated"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "Sharko cannot reach the cluster's connection in this install"
// @Router /clusters/{name}/takeover [post]
func (s *Server) handleClusterTakeover(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.takeover") {
		return
	}
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	var req TakeoverRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}

	// The preflight runs again here, on this call's own reading of the
	// world. The caller may have looked at a report from an hour ago, and
	// an hour is plenty of time for someone else to point an
	// ApplicationSet at this cluster.
	in, err := s.gatherPreflightInputs(r, name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	report := takeover.Preflight(in)

	if !report.Ready {
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":     "this cluster is not ready to be taken over yet — " + report.Summary,
			"preflight": report,
		})
		return
	}
	if !req.DryRun {
		if unacked := unacknowledgedFindings(report, req.AcknowledgedFindings); len(unacked) > 0 {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error": fmt.Sprintf(
					"%s %s not been read yet — look at %s in the checks below, then send %s back in acknowledged_findings: %s",
					plural(len(unacked), "warning", "warnings"), wasWere(len(unacked)),
					joinList(unacked), pluralIt(len(unacked)), report.Summary),
				"unacknowledged_findings": unacked,
				"preflight":               report,
			})
			return
		}
	}
	if !req.DryRun && !req.Yes {
		writeError(w, http.StatusBadRequest, "confirmation required: set yes: true in the request body")
		return
	}

	preserve := true
	if req.PreserveLegacyLabels != nil {
		preserve = *req.PreserveLegacyLabels
	}

	resp := TakeoverResponse{
		Cluster:   name,
		Server:    report.Server,
		Preflight: &report,
	}

	// The pull request comes first. Git is the record of what the fleet
	// is; making the record before changing the live cluster means an
	// interrupted takeover leaves a reviewable pull request rather than a
	// relabelled Secret nobody wrote down.
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}
	// The tiered resolver is the ONLY way this commit gets attributed to the
	// person who asked for it, and the only way the audit entry gets stamped
	// with a tier. Carrying on with an unattributed provider when it fails
	// would open a pull request nobody's name is on (v4-wave2 review M1), so
	// this fails hard — same stance as handleMigrateRepo.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)

	gitResult, gitErr := orch.TakeoverClusterGit(ctx, orchestrator.TakeoverClusterRequest{
		Name:      name,
		Region:    req.Region,
		DryRun:    req.DryRun,
		Yes:       req.Yes,
		AutoMerge: req.AutoMerge,
	})
	if gitErr != nil {
		if gitErr == orchestrator.ErrTakeoverNeedsV4Repo {
			writeError(w, http.StatusConflict, gitErr.Error())
			return
		}
		writeError(w, http.StatusBadGateway, gitErr.Error())
		return
	}

	if req.DryRun {
		resp.Status = "planned"
		if gitResult != nil {
			resp.DryRun = gitResult.DryRun
		}
		if preserve {
			resp.PreservedLabels = report.LegacyLabels
		} else {
			resp.DroppedLabels = report.LegacyLabels
		}
		resp.Message = takeoverPlanMessage(name, preserve, report.LegacyLabels)
		writeJSON(w, http.StatusOK, withAttributionWarning(resp, tokRes))
		return
	}

	if gitResult != nil {
		resp.Git = gitResult.Git
	}

	// Now the swap. One in-place update on the existing Secret — see
	// argosecrets.TakeOverClusterSecret for why this is not a
	// create-then-delete.
	swap, swapErr := s.argoSecretManager.TakeOverClusterSecret(
		ctx, name, preserve, time.Now().UTC().Format(time.RFC3339), takeoverLegacyLabelKey)
	if swapErr != nil {
		resp.Status = "partial"
		resp.Message = fmt.Sprintf(
			"The pull request adding %s to the fleet was opened, but Sharko could not take ownership of its ArgoCD connection: %v. Nothing was deleted. Fix the problem and run the takeover again — it is safe to repeat.",
			name, swapErr)
		audit.Enrich(ctx, audit.Fields{Event: "cluster_takeover_partial", Resource: "cluster:" + name})
		writeJSON(w, http.StatusMultiStatus, resp)
		return
	}
	if !swap.Found {
		resp.Status = "partial"
		resp.Message = fmt.Sprintf(
			"The pull request adding %s to the fleet was opened, but its ArgoCD connection disappeared between the check and the swap. Nothing was deleted. Check the cluster still exists in ArgoCD, then run the takeover again.",
			name)
		audit.Enrich(ctx, audit.Fields{Event: "cluster_takeover_partial", Resource: "cluster:" + name})
		writeJSON(w, http.StatusMultiStatus, resp)
		return
	}

	resp.Status = "success"
	resp.SecretSwapped = swap.Changed
	resp.AlreadyOwned = swap.AlreadyOwned
	resp.ProtectionRepaired = swap.ProtectionRepaired
	resp.PreservedLabels = swap.PreservedLabels
	resp.DroppedLabels = swap.DroppedLabels
	if swap.Server != "" {
		resp.Server = swap.Server
	}
	resp.Message = takeoverDoneMessage(name, swap.AlreadyOwned, swap.ProtectionRepaired, swap.PreservedLabels, swap.DroppedLabels)

	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}
	audit.Enrich(ctx, audit.Fields{Event: "cluster_taken_over", Resource: "cluster:" + name})
	writeJSON(w, http.StatusOK, withAttributionWarning(resp, tokRes))
}

// takeoverLegacyLabelKey is the classifier handed to the swap: it decides
// which of the live labels belonged to the previous owner. Defined here as
// a one-line bridge so internal/argosecrets does not have to import
// internal/takeover just to know what a legacy label is.
func takeoverLegacyLabelKey(key string) bool {
	if key == takeover.ArgoCDSecretTypeLabel {
		return false
	}
	return !takeover.SharkoOwnedLabelKey(key)
}

func takeoverPlanMessage(name string, preserve bool, legacy map[string]string) string {
	if len(legacy) == 0 {
		return fmt.Sprintf(
			"Plan: %s joins the fleet through a pull request, and Sharko becomes the owner of its existing ArgoCD connection. The connection keeps the same name and the same address. Nothing gets deleted and no addon is turned on.",
			name)
	}
	keys := takeover.SortedKeys(legacy)
	if preserve {
		return fmt.Sprintf(
			"Plan: %s joins the fleet through a pull request, and Sharko becomes the owner of its existing ArgoCD connection — same name, same address. These %d existing labels come across unchanged: %s. Nothing gets deleted and no addon is turned on.",
			name, len(keys), joinList(keys))
	}
	return fmt.Sprintf(
		"Plan: %s joins the fleet through a pull request, and Sharko becomes the owner of its existing ArgoCD connection — same name, same address. You asked NOT to keep the previous owner's labels, so these %d would be removed at the same time: %s. Anything that selects clusters on those labels will stop matching this one.",
		name, len(keys), joinList(keys))
}

func takeoverDoneMessage(name string, alreadyOwned, protectionRepaired bool, preserved, dropped map[string]string) string {
	if alreadyOwned {
		if protectionRepaired {
			return fmt.Sprintf(
				"Sharko already owned %s's connection. Its address, its credentials and its labels were not touched — but the note saying which of those labels came from the previous owner was missing, so Sharko wrote it back: %s. That note is what stops a later re-sync taking those labels off.",
				name, joinList(takeover.SortedKeys(preserved)))
		}
		return fmt.Sprintf("Sharko already owned %s's connection, so nothing about it changed. Its fleet record is up to date.", name)
	}
	base := fmt.Sprintf("Sharko now owns %s's ArgoCD connection. It kept the same name and the same address, and nothing was deleted.", name)
	if len(preserved) > 0 {
		base += fmt.Sprintf(" These %d labels from the previous owner came across unchanged: %s. When you no longer need them, remove them with the drop-labels action — that step warns you first if anything still selects on them.",
			len(preserved), joinList(takeover.SortedKeys(preserved)))
	}
	if len(dropped) > 0 {
		base += fmt.Sprintf(" These %d labels from the previous owner were removed as you asked: %s.",
			len(dropped), joinList(takeover.SortedKeys(dropped)))
	}
	return base
}

// ─────────────────────────────────────────────────────────────────────────
// 6.4 — dropping the labels that were carried over
// ─────────────────────────────────────────────────────────────────────────

// handleClusterDropLegacyLabels godoc
//
// @Summary Remove the labels a takeover carried over
// @Description Takes the previous owner's labels off a cluster's connection. Warns first — by name — about any ApplicationSet that still picks clusters using one of those labels, because removing it is what makes this cluster fall out of that ApplicationSet.
// @Description Nothing is removed without "yes": true. The dry run returns each warning with a stable id in "warning_ids"; every one of those ids has to come back in "acknowledged_findings" before the removal runs. Send "dry_run": true to see what would go.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body api.DropLegacyLabelsRequest true "Drop request"
// @Success 200 {object} api.DropLegacyLabelsResponse "Labels removed (or the plan, on a dry run)"
// @Failure 400 {object} map[string]interface{} "Bad request, or the confirmation is missing"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "No connection with that name"
// @Failure 409 {object} map[string]interface{} "An ApplicationSet still selects one of these labels and that warning was not named in acknowledged_findings"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "Sharko cannot reach the cluster's connection in this install"
// @Router /clusters/{name}/takeover/legacy-labels/drop [post]
func (s *Server) handleClusterDropLegacyLabels(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.takeover.drop-labels") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	var req DropLegacyLabelsRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	if s.argoSecretManager == nil {
		writeError(w, http.StatusServiceUnavailable,
			"Sharko is not connected to the cluster it runs in, so it cannot change this cluster's connection")
		return
	}

	ctx := r.Context()
	detail, err := s.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !detail.Found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ArgoCD has no connection saved for %q", name))
		return
	}

	carried := argosecrets.PreservedLabelKeys(detail.Annotations)
	targets := req.Labels
	if len(targets) == 0 {
		targets = carried
	} else {
		// Only labels this takeover actually carried over are droppable
		// here. Anything else is either Sharko's own or something that
		// arrived by another route, and this action is not the place to
		// guess about it.
		for _, k := range targets {
			if !containsStr(carried, k) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf(
					"%q is not one of the labels the takeover carried over for %s, so this action will not remove it. The ones it can remove are: %s",
					k, name, joinList(carried)))
				return
			}
		}
	}

	resp := DropLegacyLabelsResponse{Cluster: name}
	if len(targets) == 0 {
		resp.Status = "success"
		resp.Message = fmt.Sprintf("There are no carried-over labels left on %s's connection — there is nothing to remove.", name)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	sort.Strings(targets)

	// The warning: which ApplicationSets still pick clusters using one of
	// these labels, and what each of them does when a cluster stops
	// matching. This is the read the whole story exists for.
	// Each warning carries a stable id alongside its sentence, so the
	// confirmation names the warnings it covers instead of covering
	// whatever this call happens to find.
	var warnings, warningIDs []string
	if s.appSetReader == nil {
		warnings = append(warnings, "Sharko has no read access to ApplicationSets in this install, so it cannot tell you whether anything still selects these labels. Check by hand before you go ahead.")
		warningIDs = append(warningIDs, dropWarningAppSetsUnreadable)
	} else if list, listErr := s.appSetReader.List(ctx); listErr != nil {
		warnings = append(warnings, "Sharko could not read the ApplicationSets ("+listErr.Error()+"), so it cannot tell you whether anything still selects these labels. Check by hand before you go ahead.")
		warningIDs = append(warningIDs, dropWarningAppSetsUnreadable)
	} else {
		for _, as := range appsets.SelectingLabelKeys(list, targets) {
			var keys []string
			for _, k := range targets {
				if as.SelectsLabelKey(k) {
					keys = append(keys, k)
				}
			}
			consequence := as.WhyUnsafe()
			if as.DeletionSafe() {
				consequence = as.WhySafe() + ", so nothing it installed gets removed"
			}
			warnings = append(warnings, fmt.Sprintf(
				"The ApplicationSet %q picks clusters using %s. Remove %s and this cluster stops matching it — %s.",
				as.Name, joinList(keys), pluralThat(len(keys)), consequence))
			warningIDs = append(warningIDs, dropWarningAppSetPrefix+as.Name)
		}
	}
	resp.Warnings = warnings
	resp.WarningIDs = warningIDs

	if req.DryRun {
		resp.Status = "planned"
		resp.Removed = targets
		resp.Message = fmt.Sprintf("Plan: remove %d label(s) from %s's connection — %s. Nothing has been changed.",
			len(targets), name, joinList(targets))
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if unacked := missingIDs(warningIDs, req.AcknowledgedFindings); len(unacked) > 0 {
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error": fmt.Sprintf(
				"%s %s not been read yet — read %s below, then send %s back in acknowledged_findings.",
				plural(len(unacked), "warning", "warnings"), wasWere(len(unacked)),
				joinList(unacked), pluralIt(len(unacked))),
			"unacknowledged_findings": unacked,
			"warnings":                warnings,
			"warning_ids":             warningIDs,
			"labels":                  targets,
		})
		return
	}
	if !req.Yes {
		writeError(w, http.StatusBadRequest, "confirmation required: set yes: true in the request body")
		return
	}

	removed, found, dropErr := s.argoSecretManager.DropLabels(ctx, name, targets)
	if dropErr != nil {
		writeError(w, http.StatusBadGateway, dropErr.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("ArgoCD has no connection saved for %q", name))
		return
	}

	remaining := []string{}
	for _, k := range carried {
		if !containsStr(removed, k) {
			remaining = append(remaining, k)
		}
	}
	resp.Status = "success"
	resp.Removed = removed
	resp.Remaining = remaining
	if len(removed) == 0 {
		resp.Message = fmt.Sprintf("Nothing changed — none of those labels were on %s's connection any more.", name)
	} else {
		resp.Message = fmt.Sprintf("Removed %d label(s) from %s's connection: %s. The connection itself, its address and its credentials were not touched.",
			len(removed), name, joinList(removed))
	}
	audit.Enrich(ctx, audit.Fields{Event: "cluster_legacy_labels_dropped", Resource: "cluster:" + name})
	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────
// 6.5 — unregister, with your eyes open
// ─────────────────────────────────────────────────────────────────────────

// handleClusterUnregisterConsequences godoc
//
// @Summary List what happens if you unregister a cluster
// @Description Reads out, one by one, what unregistering this cluster will do — what leaves the repo, what happens to its ArgoCD connection, what is deployed there today, and which ApplicationSets may react to the labels a takeover carried over.
// @Description Writes nothing and deletes nothing. It is the read that comes before the removal.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} api.UnregisterConsequencesResponse "The consequences"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /clusters/{name}/unregister/consequences [get]
func (s *Server) handleClusterUnregisterConsequences(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.unregister.consequences") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	ctx := r.Context()

	// Removal is available on every repo layout now (v4-coherence-closure
	// lane K — RemoveCluster grew its own v4 branch, remove.go /
	// remove_v4_test.go, the same way adopt/unadopt/the label-patch already
	// had one). This probe only decides the WORDING below: a v4 repo
	// deletes more than one file per cluster (the addon-assignment file
	// plus every per-cluster Helm values file, not one combined values
	// file), so the "its files leave the repo" consequence says so
	// precisely instead of implying there is only ever one file.
	//
	// A Git connection Sharko cannot reach answers "not v4" — the same
	// direction every other format probe takes — so a hiccup shows the v3
	// wording rather than guessing.
	v4Repo := false
	if s.connSvc != nil {
		if gp, gpErr := s.connSvc.GetActiveGitProvider(); gpErr == nil {
			v4Repo = s.isV4Repo(ctx, gp)
		}
	}

	resp := UnregisterConsequencesResponse{Cluster: name}
	resp.ConfirmationRequired = "Nothing here has happened yet. To go ahead, send DELETE /api/v1/clusters/" + name +
		" with \"yes\": true in the body. Add \"cleanup\": \"git\" to take the cluster out of Sharko's records but leave its ArgoCD connection alone."

	gitRecordDetail := "A pull request removes this cluster from the fleet record and deletes its addon file."
	if v4Repo {
		gitRecordDetail = "A pull request removes this cluster from the fleet record and deletes its addon-assignment file, plus every per-cluster Helm values file it has."
	}
	resp.Consequences = append(resp.Consequences, UnregisterConsequence{
		ID:          "git-record",
		Title:       "Its files leave the repo",
		Detail:      gitRecordDetail,
		WhatItMeans: "Sharko stops treating it as one of your clusters. The change goes through a pull request, so you can read it before it lands and undo it afterwards by reverting.",
		Severity:    "info",
	})

	// What owns the connection, and therefore what happens to it.
	var detail argosecrets.ClusterSecretDetail
	if s.argoSecretManager != nil {
		if d, err := s.argoSecretManager.GetClusterSecretDetail(ctx, name); err == nil {
			detail = d
		}
	}
	switch {
	case !detail.Found:
		resp.Consequences = append(resp.Consequences, UnregisterConsequence{
			ID:          "argocd-connection",
			Title:       "There is no ArgoCD connection to remove",
			Detail:      fmt.Sprintf("ArgoCD has no connection saved for %q.", name),
			WhatItMeans: "Only the repo files change.",
			Severity:    "info",
		})
	case detail.ManagedBy == argosecrets.ManagedByValue:
		resp.Consequences = append(resp.Consequences, UnregisterConsequence{
			ID:     "argocd-connection",
			Title:  "Its ArgoCD connection gets deleted",
			Detail: "Sharko owns this cluster's connection, so a full removal deletes it from ArgoCD.",
			WhatItMeans: "ArgoCD stops being able to reach the cluster. Anything ArgoCD is deploying there goes to an unknown state, and other tools that rely on that same connection lose it too. " +
				"If you only want the cluster out of Sharko, ask for \"cleanup\": \"git\" and the connection stays exactly as it is.",
			Severity: "warning",
		})
	default:
		resp.Consequences = append(resp.Consequences, UnregisterConsequence{
			ID:          "argocd-connection",
			Title:       "Its ArgoCD connection stays",
			Detail:      fmt.Sprintf("This cluster's connection is marked as owned by %q, not by Sharko.", detail.ManagedBy),
			WhatItMeans: "Sharko will not delete a connection it does not own. ArgoCD keeps reaching the cluster; only Sharko's records change.",
			Severity:    "info",
		})
	}

	// What is running there today.
	var appNames []string
	if ac, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil {
		if apps, appsErr := argocd.NewService(ac).GetClusterApplications(ctx, name); appsErr == nil {
			for _, a := range apps {
				appNames = append(appNames, a.Name)
			}
			sort.Strings(appNames)
		}
	}
	if len(appNames) > 0 {
		resp.Consequences = append(resp.Consequences, UnregisterConsequence{
			ID:          "running-applications",
			Title:       fmt.Sprintf("%d ArgoCD Application(s) are deployed there right now", len(appNames)),
			Detail:      joinList(appNames) + ".",
			WhatItMeans: "These are what you would be affecting. Read the list and make sure you recognise everything on it before you go ahead.",
			Severity:    "warning",
		})
	} else {
		resp.Consequences = append(resp.Consequences, UnregisterConsequence{
			ID:          "running-applications",
			Title:       "Nothing is deployed there through ArgoCD",
			Detail:      "No ArgoCD Applications point at this cluster.",
			WhatItMeans: "There are no running workloads to disturb.",
			Severity:    "info",
		})
	}

	// The labels a takeover carried over, and who still selects on them.
	carried := argosecrets.PreservedLabelKeys(detail.Annotations)
	if len(carried) > 0 {
		reactors := []string{}
		if s.appSetReader != nil {
			if list, listErr := s.appSetReader.List(ctx); listErr == nil {
				for _, as := range appsets.SelectingLabelKeys(list, carried) {
					reactors = append(reactors, as.Name)
				}
				sort.Strings(reactors)
			}
		}
		c := UnregisterConsequence{
			ID:       "carried-over-labels",
			Title:    "The labels carried over from the previous owner go with it",
			Detail:   fmt.Sprintf("This cluster's connection still carries %s.", joinList(carried)),
			Severity: "warning",
		}
		if len(reactors) > 0 {
			c.WhatItMeans = fmt.Sprintf(
				"These ApplicationSets pick clusters using those labels: %s. If the connection is deleted they stop seeing this cluster, and each will do whatever it is set to do when a cluster goes away.",
				joinList(reactors))
		} else {
			c.WhatItMeans = "Nothing Sharko can see picks clusters using those labels, but anything outside ArgoCD that reads them will stop finding this cluster."
		}
		resp.Consequences = append(resp.Consequences, c)
	}

	warnCount := 0
	for _, c := range resp.Consequences {
		if c.Severity == "warning" {
			warnCount++
		}
	}
	switch {
	case warnCount == 0:
		resp.Summary = fmt.Sprintf("Unregistering %s looks straightforward — read the list below, then confirm.", name)
	default:
		resp.Summary = fmt.Sprintf("Unregistering %s has %d thing(s) worth reading carefully first. Nothing has been changed yet.", name, warnCount)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────
// Acknowledging warnings — by name, not by flag
// ─────────────────────────────────────────────────────────────────────────

// Warning ids for the drop-labels call. The preflight's findings already have
// their own ids (takeover.Finding*); these cover the drop-labels warnings,
// which are computed here rather than in the takeover package.
const (
	// dropWarningAppSetPrefix + the ApplicationSet's name identifies "this
	// ApplicationSet still picks clusters using one of these labels".
	dropWarningAppSetPrefix = "appset:"
	// dropWarningAppSetsUnreadable is the "I could not check" warning.
	dropWarningAppSetsUnreadable = "appsets-unreadable"
)

// unacknowledgedFindings returns the ids of every warning in the FRESH
// preflight report that the caller did not name in acknowledged_findings,
// sorted so the message reads the same every time.
//
// The report is recomputed on the write call, so this is the check that
// catches a warning which appeared after the caller looked at the page. A
// blanket "yes I read them" flag could not: it would cover a warning nobody
// ever saw.
func unacknowledgedFindings(report takeover.Report, acknowledged []string) []string {
	var warned []string
	for _, f := range report.Findings {
		if f.Status == takeover.StatusWarning {
			warned = append(warned, f.ID)
		}
	}
	return missingIDs(warned, acknowledged)
}

// missingIDs returns the entries of required that are absent from
// acknowledged, sorted and de-duplicated.
func missingIDs(required, acknowledged []string) []string {
	if len(required) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(acknowledged))
	for _, id := range acknowledged {
		seen[id] = struct{}{}
	}
	out := []string{}
	added := make(map[string]struct{}, len(required))
	for _, id := range required {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, dup := added[id]; dup {
			continue
		}
		added[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// small helpers
// ─────────────────────────────────────────────────────────────────────────

// plural renders a count with its noun: "1 warning", "3 warnings".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func wasWere(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

func pluralIt(n int) string {
	if n == 1 {
		return "its id"
	}
	return "their ids"
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// joinList renders a list the way a person would say it: "a", "a and b",
// "a, b and c".
func joinList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	out := ""
	for i, s := range items[:len(items)-1] {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out + " and " + items[len(items)-1]
}

func pluralThat(n int) string {
	if n == 1 {
		return "that label"
	}
	return "those labels"
}
