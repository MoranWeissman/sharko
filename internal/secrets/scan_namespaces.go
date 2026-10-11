package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// scan_namespaces.go — which namespaces the leftover-secret scan looks in.
//
// The scan used to list Secrets across ALL namespaces on every cluster, every
// pass. That needs a cluster-wide "list secrets" permission on every spoke,
// and with it Sharko can read every Secret on the cluster. It now lists only
// in the namespaces Sharko could have written an addon secret to:
//
//  1. every namespace a push definition in the catalog names RIGHT NOW (for
//     every cluster, enabled there or not — a secret for an addon that was
//     switched off on one cluster is a leftover too), and
//  2. every namespace on this cluster's record: the namespaces earlier passes
//     saw in the catalog. This is what still finds the secret of an addon
//     that was REMOVED from the catalog — its namespace is gone from (1),
//     but it is still on the record.
//
// A namespace leaves a cluster's record only when it is no longer in the
// catalog AND a list there worked AND found no Sharko addon secret at all.
// Then there is nothing left for the scan to find there.
//
// The record is kept in a ConfigMap on the hub when Sharko runs in a cluster
// (see SetNamespaceRecordStore), so a restart does not forget it. Without a
// store (local or dev runs) it lives in memory only.

// NamespaceRecordStore keeps the per-cluster namespace record across
// restarts. Load returns cluster name -> namespaces. Save replaces the whole
// record.
type NamespaceRecordStore interface {
	Load(ctx context.Context) (map[string][]string, error)
	Save(ctx context.Context, record map[string][]string) error
}

// namespaceRecordKey is the key inside the ConfigMap's JSON state.
const namespaceRecordKey = "clusters"

// NamespaceRecordConfigMapName is the hub ConfigMap the record lives in.
const NamespaceRecordConfigMapName = "sharko-addon-secret-namespaces"

// configMapNamespaceStore is the NamespaceRecordStore used in production.
type configMapNamespaceStore struct {
	cm *cmstore.Store
}

// NewConfigMapNamespaceStore keeps the namespace record in a hub ConfigMap.
func NewConfigMapNamespaceStore(cm *cmstore.Store) NamespaceRecordStore {
	return &configMapNamespaceStore{cm: cm}
}

func (s *configMapNamespaceStore) Load(ctx context.Context) (map[string][]string, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return nil, err
	}
	raw, ok := data[namespaceRecordKey]
	if !ok || raw == nil {
		return map[string][]string{}, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("reading the addon secret namespace record: %w", err)
	}
	out := map[string][]string{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("reading the addon secret namespace record: %w", err)
	}
	return out, nil
}

func (s *configMapNamespaceStore) Save(ctx context.Context, record map[string][]string) error {
	return s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data[namespaceRecordKey] = record
		return nil
	})
}

// SetNamespaceRecordStore makes the namespace record survive restarts. The
// record is read from the store on the next scan and merged with anything
// already learned in memory, so wiring the store after Start() loses nothing.
func (r *Reconciler) SetNamespaceRecordStore(store NamespaceRecordStore) {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	r.nsStore = store
	r.nsLoaded = false
}

// KnownSecretNamespaces is every namespace Sharko could have written an addon
// secret to on this cluster: the catalog's push namespaces from the last pass
// plus this cluster's record. Sorted. Empty before the first pass.
func (r *Reconciler) KnownSecretNamespaces(cluster string) []string {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	return r.namespacesToScanLocked(cluster, r.lastCatalogNamespaces)
}

// namespacesToScan is catalog namespaces plus the cluster's record, sorted,
// never containing "" (an empty namespace would mean "all namespaces").
func (r *Reconciler) namespacesToScan(cluster string, catalogNamespaces []string) []string {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	r.lastCatalogNamespaces = append([]string(nil), catalogNamespaces...)
	return r.namespacesToScanLocked(cluster, catalogNamespaces)
}

func (r *Reconciler) namespacesToScanLocked(cluster string, catalogNamespaces []string) []string {
	set := make(map[string]bool)
	for _, ns := range catalogNamespaces {
		if ns != "" {
			set[ns] = true
		}
	}
	for ns := range r.knownNamespaces[cluster] {
		if ns != "" {
			set[ns] = true
		}
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// updateNamespaceRecord adds this pass's catalog namespaces to the cluster's
// record and drops the namespaces in emptyNamespaces that the catalog no
// longer names. Reports whether the record changed, and marks it as needing
// a save when it did.
func (r *Reconciler) updateNamespaceRecord(cluster string, catalogNamespaces, emptyNamespaces []string) bool {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	if r.knownNamespaces == nil {
		r.knownNamespaces = make(map[string]map[string]bool)
	}
	rec := r.knownNamespaces[cluster]
	if rec == nil {
		rec = make(map[string]bool)
		r.knownNamespaces[cluster] = rec
	}
	inCatalog := make(map[string]bool, len(catalogNamespaces))
	changed := false
	for _, ns := range catalogNamespaces {
		if ns == "" {
			continue
		}
		inCatalog[ns] = true
		if !rec[ns] {
			rec[ns] = true
			changed = true
		}
	}
	for _, ns := range emptyNamespaces {
		if !inCatalog[ns] && rec[ns] {
			delete(rec, ns)
			changed = true
		}
	}
	if changed {
		r.nsDirty = true
	}
	return changed
}

// loadNamespaceRecord reads the stored record once and merges it into the
// in-memory one (a union: nothing learned in memory is thrown away). On a
// failed read it stays unloaded, and saveNamespaceRecord will not write, so a
// partial in-memory record never overwrites a fuller stored one. The next
// scan tries the read again.
func (r *Reconciler) loadNamespaceRecord(ctx context.Context) {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	if r.nsStore == nil || r.nsLoaded {
		return
	}
	stored, err := r.nsStore.Load(ctx)
	if err != nil {
		logging.LoggerFromContext(ctx).Warn("[secrets] orphan scan: could not read the saved list of addon secret namespaces — using what this server has seen so far, and not saving until it can be read",
			"error", err)
		return
	}
	if r.knownNamespaces == nil {
		r.knownNamespaces = make(map[string]map[string]bool)
	}
	for cluster, namespaces := range stored {
		rec := r.knownNamespaces[cluster]
		if rec == nil {
			rec = make(map[string]bool)
			r.knownNamespaces[cluster] = rec
		}
		for _, ns := range namespaces {
			if ns != "" {
				rec[ns] = true
			}
		}
	}
	r.nsLoaded = true
}

// saveNamespaceRecord writes the whole record to the store when it changed
// since the last good save, there is a store, and the stored record was read
// successfully first. A failed save leaves the record marked as changed, so
// a later pass tries again.
func (r *Reconciler) saveNamespaceRecord(ctx context.Context) {
	r.nsMu.Lock()
	defer r.nsMu.Unlock()
	if r.nsStore == nil || !r.nsLoaded || !r.nsDirty {
		return
	}
	out := make(map[string][]string, len(r.knownNamespaces))
	for cluster, rec := range r.knownNamespaces {
		list := make([]string, 0, len(rec))
		for ns := range rec {
			list = append(list, ns)
		}
		sort.Strings(list)
		out[cluster] = list
	}
	if err := r.nsStore.Save(ctx, out); err != nil {
		logging.LoggerFromContext(ctx).Warn("[secrets] orphan scan: could not save the list of addon secret namespaces — it is kept in memory and saved on a later pass",
			"error", err)
		return
	}
	r.nsDirty = false
}
