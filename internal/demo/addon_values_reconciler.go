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
	"github.com/MoranWeissman/sharko/internal/models"
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
// ("unchanged", "out_of_sync", "missing", "created", "updated", "error") —
// never the secret's own content.
type demoAddonValueRecord struct {
	outcome     string
	lastChecked time.Time
	// errMsg (P1-B B3) is a RAW-shaped error string — set only for the
	// "error" outcome (a check that itself failed, not a real drift
	// finding). Mirrors internal/secrets.ItemRecord.Error's shape exactly
	// (a substring one of addonValuesSecretCheckFailureSentence's real
	// branches recognizes) so the demo exercises the SAME mapping the real
	// engine's raw errors go through, rather than showing a hand-written
	// sentence that happens to look right.
	errMsg string
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

	// clusters is every cluster name in the demo estate (task #152,
	// SyncCluster) — the demo counterpart to the real engine's
	// plan.clusters, which lists every managed cluster regardless of
	// whether any addon currently defines a push for it. A cluster with
	// zero addon-values pairs is still a known cluster, so a cluster-wide
	// refresh on it succeeds with an empty list rather than refusing.
	clusters map[string]bool

	// secretNames maps addon name → destination Secret name (task #152,
	// SyncCluster) — copied from the seeded definitions at construction so
	// a cluster-wide refresh can report the same secret NAMES the real
	// engine reports. Names only, never a value, never a backend path.
	secretNames map[string]string

	interval time.Duration
	lastRun  time.Time

	auditLog *audit.Log

	// enabledFn (M5, code review) mirrors secrets.Reconciler.enabledFn/
	// SetEnabledFn exactly — wired from the same settings.Store CheckAll
	// (internal/api/system_managed_secrets.go's addonValuesEngineInfo) reads
	// for the real engine, so `make demo-big` demonstrates the true
	// behavior of the off switch instead of a "Check all now" that always
	// ran. nil reads as enabled, same nil-safe default the real reconciler
	// uses when no settings store is wired at all.
	enabledFn func(ctx context.Context) bool

	// lastErrorCluster/lastErrorMsg/lastErrorAt (P1-B B3) seed the
	// engine-level error the page's top strip shows for this engine —
	// mirrors what a real reconcile() pass with at least one failing item
	// would report via LastError/LastErrorCluster/LastErrorAt. Set once at
	// construction and never mutated afterward: the real engine's
	// equivalent fields are only ever written by the periodic WRITE pass,
	// never by a check (CheckAll/CheckOne), so the demo's Refresh/Sync
	// re-stamps correctly leave these alone too.
	lastErrorCluster string
	lastErrorMsg     string
	lastErrorAt      time.Time

	// orphans (leftover-secrets S1) holds the seeded leftover-secret
	// records, keyed by cluster — mirrors secrets.Reconciler.orphanRecords'
	// shape so this demo exercises the same OrphanedSecrets/
	// DeleteOrphanedSecret contract the real engine does. Guarded by mu,
	// same as items/valid above — a small demo struct, one lock is enough.
	orphans map[string][]models.OrphanedSecret
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
// The spread: 2 pairs in every 12 (by cycle position) come back
// "out_of_sync", 1 in every 12 comes back "missing", 1 in every 12 comes
// back "foreign" (a secret with that name is already on the cluster and
// Sharko did not create it — P1-A), 1 in every 12 comes back "error" (P1-B
// B3 — a check that itself failed, closing queued row
// demo-seed-a-failing-check: the values side has always had the
// last_check_error field, but the demo never seeded a row that used it),
// everything else is "unchanged" (in sync) — except the very first
// qualifying pair overall, which is deliberately left out of items
// entirely so it reads as genuinely never checked (S2's "hasn't been
// checked yet" row).
func newDemoAddonValuesReconciler(estate *GeneratedEstate, defs map[string]orchestrator.AddonSecretDefinition, now time.Time, auditLog *audit.Log) *demoAddonValuesReconciler {
	r := &demoAddonValuesReconciler{
		items:       make(map[demoAddonValueKey]demoAddonValueRecord),
		valid:       make(map[demoAddonValueKey]bool),
		clusters:    make(map[string]bool, len(estate.Clusters)),
		secretNames: make(map[string]string, len(defs)),
		interval:    5 * time.Minute,
		lastRun:     now.Add(-90 * time.Second),
		auditLog:    auditLog,
	}
	for _, c := range estate.Clusters {
		r.clusters[c.Name] = true
	}
	for name, def := range defs {
		r.secretNames[name] = def.SecretName
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
			errMsg := ""
			switch i % 12 {
			case 6:
				outcome = "foreign"
			case 7, 8:
				outcome = "out_of_sync"
			case 9:
				outcome = "missing"
			case 10:
				// P1-B B3: a check that itself failed — the demo's
				// counterpart to a real reconcileSecret error. Raw-shaped
				// on purpose (see demoAddonValueRecord.errMsg's doc
				// comment): "connecting to cluster: ..." is a substring
				// addonValuesSecretCheckFailureSentence already recognizes,
				// so the row's last_check_error goes through the SAME
				// mapping a real error would.
				outcome = "error"
				errMsg = "connecting to cluster: connection refused (demo)"
			}
			checkedAt := now.Add(-addonValuesAgeOffsets[i%len(addonValuesAgeOffsets)])
			r.items[key] = demoAddonValueRecord{
				outcome:     outcome,
				lastChecked: checkedAt,
				errMsg:      errMsg,
			}

			// Seed the engine-level error strip (P1-B B3) off the FIRST
			// "error" row this loop finds — a real reconcile() pass's
			// LastError/LastErrorCluster/LastErrorAt name the first
			// failing item it hit, so this mirrors that shape instead of
			// picking an arbitrary cluster.
			if outcome == "error" && r.lastErrorCluster == "" {
				r.lastErrorCluster = c.Name
				r.lastErrorMsg = "Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh."
				r.lastErrorAt = checkedAt
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

	// leftover-secrets S1 — seed one believable leftover secret so the
	// Managed Secrets page has a real "orphaned" row to show, not zero.
	// The story: "redis-ha" used to be an addon in the catalog with its own
	// push definition; the catalog entry was removed (or hand-deleted) at
	// some point, but nobody ever asked Sharko to clean up the Secret it
	// had already written — it is still sitting on the first cluster,
	// still carrying Sharko's labels. "redis-ha" deliberately appears
	// nowhere in demoGeneratedAddonSecretDefs or any cluster's Addons map,
	// so it reads the same way a real hand-deleted catalog entry would.
	// Deterministic (first cluster in sorted order, fixed namespace/name/
	// age), not random, matching every other seed in this function.
	if len(clusters) > 0 {
		orphanCluster := clusters[0].Name
		r.orphans = map[string][]models.OrphanedSecret{
			orphanCluster: {
				{
					Cluster:     orphanCluster,
					Namespace:   "data",
					Name:        "redis-ha-auth",
					Addon:       "redis-ha",
					LastChecked: now.Add(-47 * time.Minute),
				},
			},
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

// LastError reports the engine-level error seeded at construction (P1-B
// B3) — already an already-canned, safe-to-show sentence, exactly the
// shape secrets.Reconciler.LastError returns for real (secrets.
// FailureSentence-mapped), never raw text. Empty when the estate seeded no
// "error" outcome row.
func (r *demoAddonValuesReconciler) LastError() string { return r.lastErrorMsg }

// LastErrorCluster names the cluster LastError is about (P1-B B3) — the
// same cluster that seeded the engine-level error at construction.
func (r *demoAddonValuesReconciler) LastErrorCluster() string { return r.lastErrorCluster }

// LastErrorAt is the timestamp LastError was seeded with (P1-B B3).
func (r *demoAddonValuesReconciler) LastErrorAt() time.Time { return r.lastErrorAt }

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

// LastItemError reports the seeded row's raw-shaped error text (S8, P1-B
// B3) — "" for every outcome except "error", the same rule the real
// reconciler follows (ItemRecord.Error is populated only alongside
// ItemOutcomeError; an "out_of_sync" row is a seeded comparison outcome,
// not a check that itself failed). The caller (addonValuesRowLastCheckError
// in internal/api/system_managed_secrets.go) maps this through
// addonValuesSecretCheckFailureSentence exactly as it does for a real
// reconciler — the demo never hands back an already-rendered sentence here.
func (r *demoAddonValuesReconciler) LastItemError(cluster, addon string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.items[demoAddonValueKey{Cluster: cluster, Addon: addon}]
	if !ok || rec.errMsg == "" {
		return "", false
	}
	return rec.errMsg, true
}

// LastItemConsecutiveFailures (P2-D D3) always reports 0/false in demo
// mode: the seeded "error" outcome (S2/P1-B B3) is a fixed snapshot, not a
// pass-over-pass streak a demo estate has no real history to accumulate —
// the row's consecutive-failure warning is honestly absent here rather
// than a fabricated number.
func (r *demoAddonValuesReconciler) LastItemConsecutiveFailures(_, _ string) (int, bool) {
	return 0, false
}

// KnownItemCount (P3-F1) reports how many cluster+addon pairs this demo
// engine has on file — the same real number the live engine reports, so
// the audit entry a demo "Refresh all" writes states a blast radius the
// maintainer can check against the rows on screen.
func (r *demoAddonValuesReconciler) KnownItemCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.valid)
}

// demoForeignRefusal is the sentence a demo Sync gets back for a row whose
// secret Sharko did not create. Kept word-for-word identical to
// internal/secrets.ErrForeignSecret (the real engine's refusal) and to the
// disabled-Sync reason on the page — the demo has to say the same thing the
// real thing says, or the walk teaches the wrong sentence. Copied rather
// than imported to keep this package free of an internal/secrets dependency.
const demoForeignRefusal = "Someone else created this one — Sharko will not touch it."

// demoAddonValuesEngineDisabled is CheckAll's refusal sentence when the
// engine is switched off (M5, code review) — kept word-for-word identical
// to secrets.ErrReconcilerDisabled so the demo teaches the same sentence
// the real engine gives. Copied rather than imported, same reasoning as
// demoForeignRefusal above (keeps this package free of an internal/secrets
// dependency).
const demoAddonValuesEngineDisabled = "the addon-values engine is switched off"

// SetEnabledFn wires the addon-values engine's off switch into demo mode
// (M5, code review) — mirrors secrets.Reconciler.SetEnabledFn exactly.
// Before this, demo's CheckAll had no seam at all: flipping Settings ->
// Addon Values Engine off changed what the real engine's "Check all now"
// did but left the demo one running exactly as before, so `make demo-big`
// could not demonstrate the off switch's real behavior.
func (r *demoAddonValuesReconciler) SetEnabledFn(fn func(ctx context.Context) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabledFn = fn
}

// isEnabled mirrors secrets.Reconciler.isEnabled — nil enabledFn (no
// settings store wired) reads as enabled, matching every other nil-safe
// wrapper in this codebase's settings pattern.
func (r *demoAddonValuesReconciler) isEnabled(ctx context.Context) bool {
	r.mu.RLock()
	fn := r.enabledFn
	r.mu.RUnlock()
	if fn == nil {
		return true
	}
	return fn(ctx)
}

// IsEnabled exports isEnabled (M6, code review) — mirrors
// secrets.Reconciler.IsEnabled, reachable synchronously the same way.
func (r *demoAddonValuesReconciler) IsEnabled(ctx context.Context) bool {
	return r.isEnabled(ctx)
}

// CheckAll is the page's "Refresh all" on this engine — re-stamps every
// known row's last-checked time and leaves every outcome exactly where it
// was. Same rule as CheckOne below: a check looks, it never fixes.
//
// M5 (code review): gated on the same off switch the real engine's CheckAll
// checks first — a switched-off engine has nothing to check with in demo
// mode either.
func (r *demoAddonValuesReconciler) CheckAll(ctx context.Context) error {
	if !r.isEnabled(ctx) {
		return errors.New(demoAddonValuesEngineDisabled)
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.valid {
		rec, ok := r.items[key]
		outcome := "unchanged"
		errMsg := ""
		if ok {
			outcome = rec.outcome
			// P1-B B3: a check that fails again stays failed — carry the
			// error text forward exactly like the outcome, so a row seeded
			// "error" is still "error" (with its reason) after a Refresh,
			// not silently cleared by the re-stamp.
			errMsg = rec.errMsg
		}
		r.items[key] = demoAddonValueRecord{outcome: outcome, lastChecked: now, errMsg: errMsg}
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
	errMsg := ""
	if ok {
		outcome = rec.outcome
		// P1-B B3: same carry-forward as CheckAll — a single-row Refresh
		// on a row whose last check failed must still say so afterward.
		errMsg = rec.errMsg
	}
	r.items[key] = demoAddonValueRecord{outcome: outcome, lastChecked: time.Now(), errMsg: errMsg}
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

// SyncCluster is the demo counterpart to secrets.Reconciler.SyncCluster
// (task #152, story 152.A) — the engine behind POST
// /clusters/{name}/secrets/refresh. Same contract as the real one: the
// cluster must be known to the estate (the demo's stand-in for "in the
// managed clusters list in Git"), a named addon must have a definition on
// that cluster, and each pair syncs exactly the way SyncOne syncs it — an
// out-of-sync row comes back updated, a missing or never-checked row comes
// back created, a foreign row is left alone in neither list, a row whose
// last check failed fails again and lands in failed. The refusal error
// texts carry the same substrings the real engine's sentinel errors carry,
// so internal/api's clusterSecretsRefreshRefusal maps a demo refusal to
// the exact sentence a real one produces.
func (r *demoAddonValuesReconciler) SyncCluster(_ context.Context, cluster, addonName string) (refreshed []string, failed []string, err error) {
	r.mu.Lock()
	if !r.clusters[cluster] {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("cluster %q is not in the managed clusters list in Git — nothing to refresh", cluster)
	}

	// Collect this cluster's pairs in sorted addon order, so the same demo
	// refresh always reports the same list in the same order.
	var addons []string
	for key := range r.valid {
		if key.Cluster != cluster {
			continue
		}
		if addonName != "" && key.Addon != addonName {
			continue
		}
		addons = append(addons, key.Addon)
	}
	if addonName != "" && len(addons) == 0 {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("Git does not define an addon-values secret for addon %q on cluster %q — nothing to refresh", addonName, cluster)
	}
	sort.Strings(addons)

	type auditEvent struct{ event, detail, addon string }
	var auditEvents []auditEvent
	for _, addon := range addons {
		key := demoAddonValueKey{Cluster: cluster, Addon: addon}
		prev, hadRecord := r.items[key]
		secretName := r.secretNames[addon]
		if secretName == "" {
			secretName = addon
		}

		// Foreign rows are a boundary, not a failure — left alone, in
		// neither list, exactly like the real SyncCluster.
		if hadRecord && prev.outcome == "foreign" {
			continue
		}
		// A row whose last check itself failed fails its sync too — the
		// demo's deterministic stand-in for "the cluster is unreachable".
		if hadRecord && prev.outcome == "error" {
			r.items[key] = demoAddonValueRecord{outcome: prev.outcome, lastChecked: time.Now(), errMsg: prev.errMsg}
			failed = append(failed, secretName)
			continue
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
		refreshed = append(refreshed, secretName)
		if event != "" {
			auditEvents = append(auditEvents, auditEvent{event: event, detail: detail, addon: addon})
		}
	}
	r.mu.Unlock()

	if r.auditLog != nil {
		for _, ev := range auditEvents {
			r.auditLog.Add(audit.Entry{
				Level:    "info",
				Event:    ev.event,
				User:     "sharko",
				Action:   "push",
				Resource: fmt.Sprintf("cluster:%s/addon:%s", cluster, ev.addon),
				Source:   "reconciler",
				Result:   "success",
				Detail:   ev.detail,
			})
		}
	}

	return refreshed, failed, nil
}

// OrphanedSecrets (leftover-secrets S1) returns a deterministic, sorted
// snapshot of every seeded leftover-secret record — the demo counterpart to
// secrets.Reconciler.OrphanedSecrets, same sort order (cluster, then
// namespace, then name).
func (r *demoAddonValuesReconciler) OrphanedSecrets() []models.OrphanedSecret {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]models.OrphanedSecret, 0)
	for _, list := range r.orphans {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DeleteOrphanedSecret (leftover-secrets S1) is the demo counterpart to
// secrets.Reconciler.DeleteOrphanedSecret: removes the seeded record — so
// the row genuinely disappears from the next read, exactly like a real
// delete (S3's "actions genuinely change what the next read reports" rule,
// applied to this new one too) — and writes the SAME audit entry shape the
// real handler produces (event orphaned_secret_deleted, resource
// "cluster:<name>", detail "deleted leftover secret <ns>/<name>"), so a
// walk through demo mode teaches the real audit trail, not a
// demo-only approximation of it.
func (r *demoAddonValuesReconciler) DeleteOrphanedSecret(_ context.Context, cluster, namespace, name string) error {
	r.mu.Lock()
	list := r.orphans[cluster]
	idx := -1
	for i, rec := range list {
		if rec.Namespace == namespace && rec.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		r.mu.Unlock()
		return fmt.Errorf("no leftover secret is on record for cluster %q, namespace %q, name %q", cluster, namespace, name)
	}
	r.orphans[cluster] = append(list[:idx:idx], list[idx+1:]...)
	r.mu.Unlock()

	if r.auditLog != nil {
		r.auditLog.Add(audit.Entry{
			Level:    "info",
			Event:    "orphaned_secret_deleted",
			User:     "sharko",
			Action:   "delete",
			Resource: fmt.Sprintf("cluster:%s", cluster),
			Source:   "reconciler",
			Result:   "success",
			Detail:   fmt.Sprintf("deleted leftover secret %s/%s", namespace, name),
		})
	}
	return nil
}
