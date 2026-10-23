package orchestrator

// adopt_findings.go — the warnings and blockers an adopt shows, and the
// rule that a real adopt waits until every warning has been read.
//
// Before this, adopt carried its warnings as plain sentences and went ahead
// anyway, while take over refused until each warning id was sent back in
// acknowledged_findings. The same cluster could be taken in through adopt
// with nobody reading what take over would have stopped on (episode 3 dry
// run, finding 13). Adopt now asks the same way.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/takeover"
)

// Ids for adopt findings that do not come from the take over preflight.
const (
	// AdoptFindingForeignOwner — another ArgoCD Application seems to render
	// this cluster's connection Secret.
	AdoptFindingForeignOwner = "foreign-owner"
	// AdoptBlockerNotInArgoCD — ArgoCD has no cluster with that name.
	AdoptBlockerNotInArgoCD = "not-in-argocd"
	// AdoptBlockerCannotCheck — Sharko could not gather what it needs to
	// decide whether the cluster is ready.
	AdoptBlockerCannotCheck = "cannot-check"
	// AdoptBlockerOtherOwner — the connection is marked as owned by another
	// tool, or Sharko could not read who owns it.
	AdoptBlockerOtherOwner = takeover.FindingSecretOwner
)

// CodeUnacknowledgedFindings is the code a 409 carries when a real adopt
// stopped because a warning was not acknowledged.
const CodeUnacknowledgedFindings = "unacknowledged_findings"

// adoptWarningFromFinding copies a take over preflight warning into the
// adopt shape, so both screens show the same words.
func adoptWarningFromFinding(f takeover.Finding) AdoptWarning {
	return AdoptWarning{
		ID:              f.ID,
		Message:         f.Detail,
		Title:           f.Title,
		WhatItMeans:     f.WhatItMeans,
		WhatToDo:        f.WhatToDo,
		ApplicationSets: f.ApplicationSets,
		Applications:    f.Applications,
	}
}

// adoptWarningsFromReport returns every warning in a preflight report.
func adoptWarningsFromReport(report takeover.Report) []AdoptWarning {
	var out []AdoptWarning
	for _, f := range report.Findings {
		if f.Status == takeover.StatusWarning {
			out = append(out, adoptWarningFromFinding(f))
		}
	}
	return out
}

// adoptBlockersFromReport returns every blocked check in a preflight report,
// each with what was found and what to do about it.
func adoptBlockersFromReport(report takeover.Report) []AdoptBlocker {
	var out []AdoptBlocker
	for _, f := range report.Findings {
		if f.Status != takeover.StatusBlocked {
			continue
		}
		msg := strings.TrimSpace(f.Detail)
		if todo := strings.TrimSpace(f.WhatToDo); todo != "" {
			msg = strings.TrimSpace(msg + " " + todo)
		}
		out = append(out, AdoptBlocker{ID: f.ID, Message: msg})
	}
	return out
}

// foreignOwnerAdoptWarnings gives the old foreign-owner sentences an id.
func foreignOwnerAdoptWarnings(sentences []string) []AdoptWarning {
	out := make([]AdoptWarning, 0, len(sentences))
	for _, s := range sentences {
		out = append(out, AdoptWarning{ID: AdoptFindingForeignOwner, Message: s})
	}
	return out
}

// missingAcknowledgements returns the ids in warnings that are not in
// acknowledged, sorted, each once. nil when nothing is missing.
func missingAcknowledgements(warnings []AdoptWarning, acknowledged []string) []string {
	acked := make(map[string]struct{}, len(acknowledged))
	for _, id := range acknowledged {
		acked[strings.TrimSpace(id)] = struct{}{}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, w := range warnings {
		if _, ok := acked[w.ID]; ok {
			continue
		}
		if _, dup := seen[w.ID]; dup {
			continue
		}
		seen[w.ID] = struct{}{}
		out = append(out, w.ID)
	}
	sort.Strings(out)
	return out
}

// AdoptNeedsAcknowledgementError is returned by AdoptClusters when a real
// adopt found a warning the request did not acknowledge. Nothing was
// written for any cluster.
type AdoptNeedsAcknowledgementError struct {
	// Missing is every warning id that was not acknowledged, sorted.
	Missing []string
	// Results carries, for each cluster with a warning, its warnings.
	Results []AdoptClusterResult
}

func (e *AdoptNeedsAcknowledgementError) Error() string {
	return fmt.Sprintf(
		"Sharko did not adopt anything. Read each warning and acknowledge it first — not acknowledged yet: %s",
		strings.Join(e.Missing, ", "))
}

// unacknowledgedAdoptWarningsMessage is the per-cluster sentence when a
// warning shows up after the first check (for example a new ApplicationSet
// appeared in between) and the cluster was not adopted because of it.
func unacknowledgedAdoptWarningsMessage(missing []string) string {
	return fmt.Sprintf(
		"Not adopted: there is a warning you have not acknowledged yet (%s). Read it, then adopt again and acknowledge it.",
		strings.Join(missing, ", "))
}

// checkAdoptAcknowledgements runs before a real adopt writes anything. It
// collects every cluster's warnings and, when any id is not in
// req.AcknowledgedFindings, returns *AdoptNeedsAcknowledgementError so the
// whole request stops with nothing written.
//
// Clusters that will fail anyway (not in ArgoCD, blocked, cannot be
// checked) are left to the normal run, which reports their blockers.
func (o *Orchestrator) checkAdoptAcknowledgements(ctx context.Context, req AdoptClustersRequest, servers map[string]string, v4Repo bool) error {
	var missingAll []string
	var results []AdoptClusterResult
	for _, name := range req.Clusters {
		serverURL, ok := servers[name]
		if !ok {
			continue
		}
		var warnings []AdoptWarning
		if v4Repo {
			in, err := o.gatherAdoptPreflightInputs(ctx, name, serverURL)
			if err != nil {
				continue
			}
			report := takeover.Preflight(in)
			if !report.Ready {
				continue
			}
			warnings = adoptWarningsFromReport(report)
		} else {
			sentences, err := o.detectForeignOwnerWarnings(ctx, name)
			if err != nil {
				continue
			}
			warnings = foreignOwnerAdoptWarnings(sentences)
		}
		missing := missingAcknowledgements(warnings, req.AcknowledgedFindings)
		if len(missing) == 0 {
			continue
		}
		missingAll = append(missingAll, missing...)
		results = append(results, AdoptClusterResult{
			Name:                   name,
			Status:                 "skipped",
			Warnings:               warnings,
			UnacknowledgedFindings: missing,
			Message:                unacknowledgedAdoptWarningsMessage(missing),
		})
	}
	if len(missingAll) == 0 {
		return nil
	}
	return &AdoptNeedsAcknowledgementError{
		Missing: missingAcknowledgements(idsAsWarnings(missingAll), nil),
		Results: results,
	}
}

func idsAsWarnings(ids []string) []AdoptWarning {
	out := make([]AdoptWarning, 0, len(ids))
	for _, id := range ids {
		out = append(out, AdoptWarning{ID: id})
	}
	return out
}
