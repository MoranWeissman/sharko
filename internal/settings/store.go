// Package settings persists small, server-wide (not per-cluster) Sharko
// configuration toggles in a ConfigMap via internal/cmstore — the same
// K8s-object-state pattern internal/observations and internal/notifications
// use. It intentionally stays narrow: a handful of keys so far (probe_mode,
// V2-cleanup-85.4; allow_inline_credentials, V2-cleanup-89.6).
//
// V3 C1: settings are now git-native — Helm env values are authoritative
// (git wins), Sharko reconciles the ConfigMap toward the env-declared value,
// and runtime PUT edits are reclaimed for declared keys. When a key is NOT
// env-declared (unset), the runtime ConfigMap value persists (API
// authoritative, back-compat). Keep adding typed getters one at a time;
// store stays install-scoped (not multi-tenant). Exposes desired-from-env
// resolution + a reconcile entry point.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// Probe mode values (V2-cleanup-85.4). probe_mode controls whether Sharko
// deploys a transient "connectivity-check" ArgoCD Application to newly
// registered clusters with zero enabled addons.
const (
	// ProbeModeCheckApp is the default: Sharko deploys the connectivity-check
	// app so a brand-new, zero-addon cluster proves end-to-end deployability
	// even before the operator enables a real addon.
	ProbeModeCheckApp = "check-app"

	// ProbeModeAPITest disables the connectivity-check app entirely — no
	// app is ever auto-deployed to a new cluster, even transiently.
	// Reachability is derived purely from ArgoCD's own connection state.
	ProbeModeAPITest = "api-test"
)

const (
	configMapName                = "sharko-server-settings"
	keyProbeMode                 = "probe_mode"
	keyAllowInlineCredentials    = "allow_inline_credentials"
	keyManagedClusterSelfHeal    = "managed_cluster_self_heal"
)

// Store persists server-wide settings in a ConfigMap via cmstore.
//
// It also keeps a small thread-safe cache of the last value each setting
// successfully read as (V2-cleanup-90.3 / review finding M4). The
// error-swallowing convenience wrappers below (IsAPITest,
// IsInlineCredentialsAllowed) are called from hot, non-HTTP code paths
// (register, reconcile) that must never block on a settings-store outage —
// but "never block" must not mean "silently fail open" either. On a read
// error they fall back to this cache instead of jumping straight to the
// static default, so a transient ConfigMap read failure cannot flip a
// kill switch back on. Only before the FIRST successful read (fresh
// process, or a nil Store — dev/out-of-cluster mode) does the static
// default apply.
type Store struct {
	cm *cmstore.Store

	cacheMu sync.RWMutex

	cachedProbeMode      string
	cachedProbeModeValid bool

	cachedAllowInlineCredentials      bool
	cachedAllowInlineCredentialsValid bool

	cachedManagedClusterSelfHeal      bool
	cachedManagedClusterSelfHealValid bool
}

// NewStore creates a new settings store backed by a ConfigMap named
// "sharko-server-settings" in namespace.
func NewStore(client kubernetes.Interface, namespace string) *Store {
	return &Store{
		cm: cmstore.NewStore(client, namespace, configMapName),
	}
}

// isValidProbeMode reports whether mode is a recognized probe_mode value.
func isValidProbeMode(mode string) bool {
	return mode == ProbeModeCheckApp || mode == ProbeModeAPITest
}

// GetProbeMode returns the persisted probe_mode, defaulting to
// ProbeModeCheckApp when the ConfigMap does not exist yet, the key was
// never set, or the stored value is not recognized (e.g. written by a
// future Sharko version). Never returns an unrecognized value.
func (s *Store) GetProbeMode(ctx context.Context) (string, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return ProbeModeCheckApp, err
	}
	mode, _ := data[keyProbeMode].(string)
	if !isValidProbeMode(mode) {
		mode = ProbeModeCheckApp
	}
	s.cacheMu.Lock()
	s.cachedProbeMode = mode
	s.cachedProbeModeValid = true
	s.cacheMu.Unlock()
	return mode, nil
}

// SetProbeMode validates and persists mode. Returns an error for any value
// other than ProbeModeCheckApp / ProbeModeAPITest — callers must not write
// unrecognized values into the ConfigMap.
func (s *Store) SetProbeMode(ctx context.Context, mode string) error {
	if !isValidProbeMode(mode) {
		return &InvalidProbeModeError{Value: mode}
	}
	return s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data[keyProbeMode] = mode
		return nil
	})
}

// IsAPITest is a nil-safe convenience wrapper for non-HTTP callers (the
// register/reconcile paths) that need a plain bool and must never let a
// transient settings-store read failure block cluster reconciliation. On a
// read error it falls back to the last successfully-read probe_mode
// (V2-cleanup-90.3 — mirrors IsInlineCredentialsAllowed's cache-on-error
// shape); only before any successful read has ever happened, or when s is
// nil (settings store not wired, e.g. out-of-cluster dev mode), does it
// fall back to the static default (check-app / false).
func (s *Store) IsAPITest(ctx context.Context) bool {
	if s == nil {
		return false
	}
	mode, err := s.GetProbeMode(ctx)
	if err == nil {
		return mode == ProbeModeAPITest
	}

	s.cacheMu.RLock()
	cached, valid := s.cachedProbeMode, s.cachedProbeModeValid
	s.cacheMu.RUnlock()
	if valid {
		slog.Warn("probe_mode: settings read failed, serving last-known value from cache",
			"error", err, "cached_probe_mode", cached)
		return cached == ProbeModeAPITest
	}
	return false
}

// InvalidProbeModeError is returned by SetProbeMode for an unrecognized
// value. Exported so API handlers can distinguish "bad input" (400) from
// a ConfigMap write failure (500).
type InvalidProbeModeError struct {
	Value string
}

func (e *InvalidProbeModeError) Error() string {
	return fmt.Sprintf("invalid probe_mode %q: must be %q or %q", e.Value, ProbeModeCheckApp, ProbeModeAPITest)
}

// allow_inline_credentials (V2-cleanup-89.6) — admin-level kill switch for
// the "Paste a kubeconfig" registration path. Defaults to true (today's
// behavior, unchanged): inline credential paste stays available for day-1
// onboarding until an admin explicitly turns it off install-wide.
//
// Sharko has no user RBAC today — there is a single admin login, so this is
// necessarily an install-wide switch rather than a per-user permission. When
// V2.x scoped RBAC lands (see project_attribution_design), this setting is
// expected to become a per-role permission (e.g. "who may paste inline
// credentials") instead of a single global bool.
const defaultAllowInlineCredentials = true

// GetAllowInlineCredentials returns the persisted allow_inline_credentials
// value, defaulting to true (inline paste allowed) when the ConfigMap does
// not exist yet, the key was never set, or the stored value is not a bool
// (e.g. written by a future Sharko version). The safe default matches
// today's behavior — installs that never touch this setting see no change.
func (s *Store) GetAllowInlineCredentials(ctx context.Context) (bool, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return defaultAllowInlineCredentials, err
	}
	allow, ok := data[keyAllowInlineCredentials].(bool)
	if !ok {
		allow = defaultAllowInlineCredentials
	}
	s.cacheMu.Lock()
	s.cachedAllowInlineCredentials = allow
	s.cachedAllowInlineCredentialsValid = true
	s.cacheMu.Unlock()
	return allow, nil
}

// SetAllowInlineCredentials persists allow. There is no invalid value for a
// bool setting — unlike SetProbeMode, this has nothing to reject.
//
// On a successful write it also seeds the read cache with the just-persisted
// value (V2-cleanup-90.3). Without this, an admin flipping the switch off
// via PUT would not be reflected in IsInlineCredentialsAllowed's
// error-fallback cache until some later GetAllowInlineCredentials call
// happened to succeed — a narrow but real gap between "admin turned it off"
// and "the kill switch's own error-recovery path knows it's off".
func (s *Store) SetAllowInlineCredentials(ctx context.Context, allow bool) error {
	err := s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data[keyAllowInlineCredentials] = allow
		return nil
	})
	if err != nil {
		return err
	}
	s.cacheMu.Lock()
	s.cachedAllowInlineCredentials = allow
	s.cachedAllowInlineCredentialsValid = true
	s.cacheMu.Unlock()
	return nil
}

// IsInlineCredentialsAllowed is a nil-safe convenience wrapper for non-HTTP
// callers (the orchestrator's RegisterCluster gate) that need a plain bool
// and must never let a transient settings-store read failure block
// registration outright — but "never block" must not mean "silently fail
// open" (V2-cleanup-90.3 / review finding M4). On a read error it falls
// back to the last successfully-read (or written) value; only before any
// successful read/write has ever happened, or when s is nil (settings store
// not wired, e.g. out-of-cluster dev mode), does it fall back to the static
// default (true, allowed) — matching today's behavior for installs that
// never touch this setting.
func (s *Store) IsInlineCredentialsAllowed(ctx context.Context) bool {
	if s == nil {
		return defaultAllowInlineCredentials
	}
	allow, err := s.GetAllowInlineCredentials(ctx)
	if err == nil {
		return allow
	}

	s.cacheMu.RLock()
	cached, valid := s.cachedAllowInlineCredentials, s.cachedAllowInlineCredentialsValid
	s.cacheMu.RUnlock()
	if valid {
		slog.Warn("allow_inline_credentials: settings read failed, serving last-known value from cache",
			"error", err, "cached_allow_inline_credentials", cached)
		return cached
	}
	return defaultAllowInlineCredentials
}

// V3 C1: git-native settings reconcile — resolves desired state from env
// and reconciles the ConfigMap toward it (git wins).

// desiredSettingFromEnv is a reusable helper for reading a declared setting
// from an env var. Returns (value, isDeclared). When isDeclared is false,
// the key is NOT env-declared → runtime ConfigMap value persists (API
// authoritative). When true, the returned value is authoritative (git wins).
//
// For bool settings, malformed env values (non "true"/"false") are treated
// as undeclared + warn (lenient, never crash boot on a typo).
func desiredSettingFromEnv(envKey string, isBool bool) (interface{}, bool) {
	raw := os.Getenv(envKey)
	if raw == "" {
		return nil, false // undeclared
	}

	if !isBool {
		// String setting — declared, value is raw string
		return raw, true
	}

	// Bool setting — parse, warn on malformed
	switch raw {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		slog.Warn("malformed bool setting in env, treating as undeclared",
			"env_key", envKey, "value", raw)
		return nil, false
	}
}

// Reconcile reads the desired state from env (when declared) and reconciles
// the ConfigMap toward it. Keys NOT env-declared are skipped (runtime value
// persists). Called at boot (before reconcilers start) and periodically (60s)
// to reclaim runtime API edits on declared keys. Lenient on malformed env
// values (warn + fallback, never crash).
func (s *Store) Reconcile(ctx context.Context) error {
	// Resolve desired state for all git-native settings
	desiredProbeMode, probeModeIsDeclared := desiredSettingFromEnv("SHARKO_PROBE_MODE", false)
	desiredAllowInline, allowInlineIsDeclared := desiredSettingFromEnv("SHARKO_ALLOW_INLINE_CREDENTIALS", true)

	// If nothing is declared, no-op (back-compat — all settings API-authoritative)
	if !probeModeIsDeclared && !allowInlineIsDeclared {
		return nil
	}

	// Reconcile declared keys toward env values (git wins)
	return s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		if probeModeIsDeclared {
			mode := desiredProbeMode.(string)
			if !isValidProbeMode(mode) {
				// Malformed declared value → warn + skip (lenient)
				slog.Warn("invalid probe_mode in env, skipping reconcile", "value", mode)
			} else {
				data[keyProbeMode] = mode
			}
		}

		if allowInlineIsDeclared {
			data[keyAllowInlineCredentials] = desiredAllowInline.(bool)
		}

		return nil
	})
}

// IsManagedByGit reports whether the given setting key is env-declared
// (git-native, authoritative). Used by GET endpoints to populate the
// `managed_by_git` response field.
func IsManagedByGit(key string) bool {
	switch key {
	case keyProbeMode:
		_, declared := desiredSettingFromEnv("SHARKO_PROBE_MODE", false)
		return declared
	case keyAllowInlineCredentials:
		_, declared := desiredSettingFromEnv("SHARKO_ALLOW_INLINE_CREDENTIALS", true)
		return declared
	default:
		return false
	}
}

// managed_cluster_self_heal (V3 G3) — opt-in self-heal for Sharko-managed
// clusters when drift is detected. Defaults to false (today's behavior —
// drift detection only, no automatic re-apply): the reconciler detects label
// drift and surfaces it via the OutOfSync state + LabelDrift field, but does
// NOT modify the cluster Secret. When set to true, the reconciler re-applies
// git-desired addon labels onto drifted managed-cluster Secrets every tick
// (enforcement-by-reconcile, same mechanism as the self-managed path's
// SyncLabelsOnly).
//
// This is a temporary API-mutable setting for v3.0.0. EPIC-2 C1 (Helm value →
// env → git wins) will make it git-native afterward — the setting will stay in
// this store as the runtime-resolved value, but the source-of-truth will move
// to a GitOps file (sharko-config.yaml or similar). C1 wires the git-native
// layer on top; G3 just adds the setting to the store the standard way.
const defaultManagedClusterSelfHeal = false

// GetManagedClusterSelfHeal returns the persisted managed_cluster_self_heal
// value, defaulting to false (self-heal disabled — drift detection only) when
// the ConfigMap does not exist yet, the key was never set, or the stored value
// is not a bool.
func (s *Store) GetManagedClusterSelfHeal(ctx context.Context) (bool, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return defaultManagedClusterSelfHeal, err
	}
	selfHeal, ok := data[keyManagedClusterSelfHeal].(bool)
	if !ok {
		selfHeal = defaultManagedClusterSelfHeal
	}
	s.cacheMu.Lock()
	s.cachedManagedClusterSelfHeal = selfHeal
	s.cachedManagedClusterSelfHealValid = true
	s.cacheMu.Unlock()
	return selfHeal, nil
}

// SetManagedClusterSelfHeal persists the self-heal setting. On a successful
// write it also seeds the read cache (same pattern as SetAllowInlineCredentials).
func (s *Store) SetManagedClusterSelfHeal(ctx context.Context, selfHeal bool) error {
	err := s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data[keyManagedClusterSelfHeal] = selfHeal
		return nil
	})
	if err != nil {
		return err
	}
	s.cacheMu.Lock()
	s.cachedManagedClusterSelfHeal = selfHeal
	s.cachedManagedClusterSelfHealValid = true
	s.cacheMu.Unlock()
	return nil
}

// IsManagedClusterSelfHealEnabled is a nil-safe convenience wrapper for the
// reconciler's non-HTTP drift-detection path. On a read error it falls back to
// the last successfully-read value; only before any successful read has ever
// happened, or when s is nil (settings store not wired, e.g. out-of-cluster
// dev mode), does it fall back to the static default (false, disabled).
func (s *Store) IsManagedClusterSelfHealEnabled(ctx context.Context) bool {
	if s == nil {
		return defaultManagedClusterSelfHeal
	}
	selfHeal, err := s.GetManagedClusterSelfHeal(ctx)
	if err == nil {
		return selfHeal
	}

	s.cacheMu.RLock()
	cached, valid := s.cachedManagedClusterSelfHeal, s.cachedManagedClusterSelfHealValid
	s.cacheMu.RUnlock()
	if valid {
		slog.Warn("managed_cluster_self_heal: settings read failed, serving last-known value from cache",
			"error", err, "cached_managed_cluster_self_heal", cached)
		return cached
	}
	return defaultManagedClusterSelfHeal
}
