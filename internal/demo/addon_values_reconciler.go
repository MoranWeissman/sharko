// addon_values_reconciler.go — an in-memory stand-in for
// internal/secrets.Reconciler (S2/S3), wired ONLY for a generated
// (non-default) demo estate. Demo mode never talks to a real vault or a
// real remote cluster, so nothing here fetches, stores, or returns an
// actual secret value — it only tracks, per cluster+addon pair, the same
// honest vocabulary the real reconciler reports
// (internal/secrets.ItemOutcome): whether the last check found the secret
// matching its source, out of sync, or missing outright, and when that
// check happened.
//
// Implements api.SecretReconciler (internal/api/router.go) in full —
// Refresh (CheckOne) and Sync (SyncOne) genuinely change what the next
// read reports (S3), they are not stubs.
package demo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// demoAddonValueKey identifies one addon-values secret row — mirrors
// secrets.ItemKey's shape (cluster+addon), redeclared here so this package
// never needs to import internal/secrets.
type demoAddonValueKey struct {
	Cluster string
	Addon   string
}

// demoAddonValueRecord is the last known state of one addon-values secret,
// in the same outcome vocabulary internal/secrets.ItemOutcome uses
// ("unchanged", "out_of_sync", "missing", "created", "updated") — never the
// secret's own content.
type demoAddonValueRecord struct {
	outcome     string
	lastChecked time.Time
}

// demoAddonValuesReconciler implements api.SecretReconciler for demo mode
// (S2). Every cluster+addon pair it knows about is one
// buildAddonValuesSecretRows (internal/api/system_managed_secrets.go) would
// also list as a row — same membership test (an addon with a registered
// secret definition, enabled on the cluster) — computed once at
// construction straight from the estate's own Addons map.
type demoAddonValuesReconciler struct {
	mu    sync.RWMutex
	items map[demoAddonValueKey]demoAddonValueRecord
	// valid is every cluster+addon pair CheckOne/SyncOne may act on — a
	// superset of items' keys, since one pair is deliberately left out of
	// items at construction (the "never checked" exemplar, S2) but is
	// still a real, checkable row.
	valid map[demoAddonValueKey]bool

	interval time.Duration
	lastRun  time.Time

	auditLog *audit.Log
}

// addonValuesAgeOffsets is the "how long ago was this last checked" spread
// newDemoAddonValuesReconciler cycles through — realistic, varied, and
// always relative to the "now" passed in at construction (server start),
// never a fixed calendar date.
var addonValuesAgeOffsets = []time.Duration{
	4 * time.Minute,
	11 * time.Minute,
	19 * time.Minute,
	33 * time.Minute,
	47 * time.Minute,
	1*time.Hour + 6*time.Minute,
	1*time.Hour + 52*time.Minute,
	2*time.Hour + 40*time.Minute,
	3*time.Hour + 15*time.Minute,
	4*time.Hour + 5*time.Minute,
}

// newDemoAddonValuesReconciler seeds one record per cluster+addon pair that
// has both a definition in defs and the addon enabled on that cluster (per
// the estate's own Addons map — cluster.Addons[addonName] existing means
// "enabled", the same fact the generated managed-clusters/cluster-addons
// files render into labels, which is what buildAddonValuesSecretRows
// actually reads at request time).
//
// Deterministic by construction: clusters and addon names are iterated in
// sorted order, so the SAME pair always lands at the SAME position in the
// cycle — a restart reproduces the identical mix of states (just with
// fresher "ago" numbers, since they're relative to the new now). No
// math/rand, no per-request randomness.
//
// The spread: 2 pairs in every 10 (by cycle position) come back
// "out_of_sync", 1 in every 10 comes back "missing", 1 in every 10 comes
// back "foreign" (a secret with that name is already on the cluster and
// Sharko did not create it — P1-A), everything else is "unchanged" (in
// sync) — except the very first qualifying pair overall, which is
// deliberately left out of items entirely so it reads as genuinely never
// checked (S2's "hasn't been checked yet" row).
func newDemoAddonValuesReconciler(estate *GeneratedEstate, defs map[string]orchestrator.AddonSecretDefinition, now time.Time, auditLog *audit.Log) *demoAddonValuesReconciler {
	r := &demoAddonValuesReconciler{
		items:    make(map[demoAddonValueKey]demoAddonValueRecord),
		valid:    make(map[demoAddonValueKey]bool),
		interval: 5 * time.Minute,
		lastRun:  now.Add(-90 * time.Second),
		auditLog: auditLog,
	}

	defNames := make([]string, 0, len(defs))
	for name := range defs {
		defNames = append(defNames, name)
	}
	sort.Strings(defNames)

	clusters := make([]Cluster, len(estate.Clusters))
	copy(clusters, estate.Clusters)
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })

	// repairEvents/picked (S5) seed a past repair audit entry for a
	// deterministic subset of the "unchanged" (in sync) pairs — a coherent
	// "created a few days ago, still matches now" story. Same resource
	// shape and event names lastAddonValuesSecretRepair
	// (internal/api/system_managed_secrets.go) joins on.
	repairEvents := []struct{ event, detail string }{
		{"addon_secret_created", "secret created"},
		{"addon_secret_updated", "secret updated"},
	}
	picked := 0

	i := 0
	seededNeverChecked := false
	for _, c := range clusters {
		for _, addonName := range defNames {
			if _, enabled := c.Addons[addonName]; !enabled {
				continue
			}
			key := demoAddonValueKey{Cluster: c.Name, Addon: addonName}
			r.valid[key] = true

			if !seededNeverChecked {
				seededNeverChecked = true
				i++
				continue
			}

			outcome := "unchanged"
			switch i % 10 {
			case 6:
				outcome = "foreign"
			case 7, 8:
				outcome = "out_of_sync"
			case 9:
				outcome = "missing"
			}
			r.items[key] = demoAddonValueRecord{
				outcome:     outcome,
				lastChecked: now.Add(-addonValuesAgeOffsets[i%len(addonValuesAgeOffsets)]),
			}

			if outcome == "unchanged" && i%6 == 0 && auditLog != nil {
				ev := repairEvents[picked%len(repairEvents)]
				auditLog.Add(audit.Entry{
					Level:     "info",
					Event:     ev.event,
					User:      "sharko",
					Action:    "push",
					Resource:  fmt.Sprintf("cluster:%s/addon:%s", c.Name, addonName),
					Source:    "reconciler",
					Result:    "success",
					Detail:    ev.detail,
					Timestamp: now.Add(-repairHistoryOffsets[picked%len(repairHistoryOffsets)]),
				})
				picked++
			}

			i++
		}
	}

	return r
}

// Trigger is a no-op in demo mode: nothing here runs a background pass, and
// the fleet-wide POST /secrets/reconcile has nothing real to nudge without
// one.
func (r *demoAddonValuesReconciler) Trigger() {}

// demoReconcileStats mirrors secrets.ReconcileStats' JSON shape closely
// enough for GET /api/v1/secrets/status to render something sane in demo —
// counted straight from the seeded items, never invented.
type demoReconcileStats struct {
	Checked  int       `json:"checked"`
	Created  int       `json:"created"`
	Updated  int       `json:"updated"`
	Deleted  int       `json:"deleted"`
	Skipped  int       `json:"skipped"`
	Errors   int       `json:"errors"`
	Duration string    `json:"duration"`
	LastRun  time.Time `json:"last_run"`
}

// GetStats returns a snapshot built from the seeded items — an honest count
// of what this demo reconciler currently knows, not a real reconcile
// pass's counters (there has never been one in demo mode).
func (r *demoAddonValuesReconciler) GetStats() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := demoReconcileStats{Duration: "0s", LastRun: r.lastRun}
	stats.Checked = len(r.items)
	return stats
}

// GetErrors is always empty in demo mode — nothing here has ever failed a
// real push, so there is nothing honest to report.
func (r *demoAddonValuesReconciler) GetErrors() []string { return nil }

func (r *demoAddonValuesReconciler) LastRunTime() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRun
}

// LastError is always empty in demo mode — same reasoning as GetErrors.
func (r *demoAddonValuesReconciler) LastError() string { return "" }

func (r *demoAddonValuesReconciler) Interval() time.Duration { return r.interval }

func (r *demoAddonValuesReconciler) LastItemChecked(cluster, addon string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.items[demoAddonValueKey{Cluster: cluster, Addon: addon}]
	if !ok {
		return time.Time{}, false
	}
	return rec.lastChecked, true
}

func (r *demoAddonValuesReconciler) LastItemOutcome(cluster, addon string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.items[demoAddonValueKey{Cluster: cluster, Addon: addon}]
	if !ok {
		return "", false
	}
	return rec.outcome, true
}

// LastItemError is always "" in demo mode (S8) — same reasoning as
// LastError/GetErrors: nothing here has ever failed a real push (an
// "out_of_sync" demo row is a seeded comparison outcome, not a check that
// itself failed), so there is nothing honest to report.
func (r *demoAddonValuesReconciler) LastItemError(_, _ string) (string, bool) {
	return "", false
}

// demoForeignRefusal is the sentence a demo Sync gets back for a row whose
// secret Sharko did not create. Kept word-for-word identical to
// internal/secrets.ErrForeignSecret (the real engine's refusal) and to the
// disabled-Sync reason on the page — the demo has to say the same thing the
// real thing says, or the walk teaches the wrong sentence. Copied rather
// than imported to keep this package free of an internal/secrets dependency.
const demoForeignRefusal = "Someone else created this one — Sharko will not touch it."

// CheckAll is the page's "Refresh all" on this engine — re-stamps every
// known row's last-checked time and leaves every outcome exactly where it
// was. Same rule as CheckOne below: a check looks, it never fixes.
func (r *demoAddonValuesReconciler) CheckAll(_ context.Context) error {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.valid {
		rec, ok := r.items[key]
		outcome := "unchanged"
		if ok {
			outcome = rec.outcome
		}
		r.items[key] = demoAddonValueRecord{outcome: outcome, lastChecked: now}
	}
	r.lastRun = now
	return nil
}

// CheckOne is S3's "Refresh" row action — stamps last-checked to now and
// re-reports whatever outcome is already on file, without changing it (a
// real Refresh only reads; it never fixes what it finds).
func (r *demoAddonValuesReconciler) CheckOne(_ context.Context, cluster, addon string) (string, error) {
	key := demoAddonValueKey{Cluster: cluster, Addon: addon}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid[key] {
		return "", fmt.Errorf("no addon-values secret is defined for cluster %q, addon %q in this demo estate", cluster, addon)
	}
	rec, ok := r.items[key]
	outcome := "unchanged"
	if ok {
		outcome = rec.outcome
	}
	r.items[key] = demoAddonValueRecord{outcome: outcome, lastChecked: time.Now()}
	return outcome, nil
}

// SyncOne is S3's "Sync" row action — pushes the demo secret back in line:
// an out-of-sync row comes back "updated", a missing (or never-checked) row
// comes back "created", and either way gets a repair audit entry with the
// same resource shape and event name the real per-item audit callback uses
// (cmd/sharko/serve.go's SetItemAuditFunc), so the row's "last repaired"
// line appears right after the click. A row that was already in sync stays
// "unchanged" and gets no audit entry — matching the real reconciler's own
// rule that a sync only ever audits an actual change
// (internal/secrets.Reconciler's itemAuditFn fires on Created/Updated only,
// never on Unchanged).
func (r *demoAddonValuesReconciler) SyncOne(_ context.Context, cluster, addon string) (string, error) {
	key := demoAddonValueKey{Cluster: cluster, Addon: addon}
	r.mu.Lock()
	if !r.valid[key] {
		r.mu.Unlock()
		return "", fmt.Errorf("no addon-values secret is defined for cluster %q, addon %q in this demo estate", cluster, addon)
	}
	prev, hadRecord := r.items[key]

	// P1-A: Sharko will not write a secret it did not create, in demo mode
	// exactly as in real life. The page already disables Sync on this row;
	// this is the same refusal one layer down, so a walk that pokes the API
	// directly gets the same answer the button gives.
	if hadRecord && prev.outcome == "foreign" {
		r.mu.Unlock()
		return "", errors.New(demoForeignRefusal)
	}

	outcome := "unchanged"
	event, detail := "", ""
	switch {
	case !hadRecord || prev.outcome == "missing":
		outcome = "created"
		event, detail = "addon_secret_created", "secret created"
	case prev.outcome == "out_of_sync":
		outcome = "updated"
		event, detail = "addon_secret_updated", "secret updated"
	}

	r.items[key] = demoAddonValueRecord{outcome: outcome, lastChecked: time.Now()}
	r.mu.Unlock()

	if event != "" && r.auditLog != nil {
		r.auditLog.Add(audit.Entry{
			Level:    "info",
			Event:    event,
			User:     "sharko",
			Action:   "push",
			Resource: fmt.Sprintf("cluster:%s/addon:%s", cluster, addon),
			Source:   "reconciler",
			Result:   "success",
			Detail:   detail,
		})
	}

	return outcome, nil
}
