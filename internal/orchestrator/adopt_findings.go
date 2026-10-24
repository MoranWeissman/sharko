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
	"crypto/sha256"
	"encoding/hex"
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
	return uniqueAdoptWarningIDs(out)
}

// adoptWarningContentKey is everything a warning says, in one string. Two
// warnings with the same key are the same warning.
func adoptWarningContentKey(w AdoptWarning) string {
	sets := append([]string(nil), w.ApplicationSets...)
	apps := append([]string(nil), w.Applications...)
	sort.Strings(sets)
	sort.Strings(apps)
	return strings.Join([]string{
		w.Message, w.Title, w.WhatItMeans, w.WhatToDo,
		strings.Join(sets, "\x01"), strings.Join(apps, "\x01"),
	}, "\x00")
}

// contentWarningID is kind plus a short hash of what the warning says. The
// same warning gets the same id on every call and in any order, and a
// different warning gets a different one (EP3 dry run round S, S17).
func contentWarningID(kind string, w AdoptWarning) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + adoptWarningContentKey(w)))
	return kind + "-" + hex.EncodeToString(sum[:4])
}

// uniqueAdoptWarningIDs makes every warning id on one cluster different,
// from the warnings' own words, never from their order. An exact copy of a
// warning is dropped. When two different warnings share an id, each of them
// gets its id plus a short hash of what it says. Before this, the second one
// got "-2" by its place in the list, so a new warning could take the id of
// one already ticked (EP3 dry run round S, S17; round R, R11).
func uniqueAdoptWarningIDs(ws []AdoptWarning) []AdoptWarning {
	type entry struct {
		w   AdoptWarning
		key string
	}
	byID := map[string][]entry{}
	var order []string
	for _, w := range ws {
		key := adoptWarningContentKey(w)
		list := byID[w.ID]
		dup := false
		for _, e := range list {
			if e.key == key {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if len(list) == 0 {
			order = append(order, w.ID)
		}
		byID[w.ID] = append(list, entry{w: w, key: key})
	}
	out := make([]AdoptWarning, 0, len(ws))
	for _, id := range order {
		list := byID[id]
		for _, e := range list {
			if len(list) > 1 {
				e.w.ID = contentWarningID(id, e.w)
			}
			out = append(out, e.w)
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
// There can be more than one, so each id carries a hash of its own
// sentence, even when there is only one: a tick on one foreign owner then
// never covers a different one that shows up later (EP3 dry run round S,
// S17).
func foreignOwnerAdoptWarnings(sentences []string) []AdoptWarning {
	out := make([]AdoptWarning, 0, len(sentences))
	for _, s := range sentences {
		w := AdoptWarning{Message: s}
		w.ID = contentWarningID(AdoptFindingForeignOwner, w)
		out = append(out, w)
	}
	return uniqueAdoptWarningIDs(out)
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
