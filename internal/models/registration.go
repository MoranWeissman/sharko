package models

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Registration-window contract shared by the kubeconfig-registration
// direct-write (internal/orchestrator/cluster.go) and the cluster-Secret
// orphan sweep (internal/clusterreconciler/reconciler.go).
//
// The race this guards against (V2-cleanup-11.1):
//
// The kubeconfig-registration path direct-writes the ArgoCD cluster Secret
// during registration Stage 1 — BEFORE the registration PR that adds the
// cluster to configuration/managed-clusters.yaml has merged. Until that PR
// merges the cluster is not "in git", so the reconciler's orphan sweep sees
// the freshly-written Secret as in-argocd ∖ in-git and deletes it (observed
// live ~200ms after the direct-write). After the PR eventually merges the
// reconciler can NOT recreate the Secret: kubeconfig bearer-token credentials
// live in no secrets backend, which is the exact reason the direct-write
// exists in the first place.
//
// The fix: the direct-write stamps AnnotationRegistrationPending with an
// RFC3339 timestamp. The orphan sweep skips any Secret carrying that
// annotation while the timestamp is within RegistrationPendingGraceWindow,
// giving the PR time to merge. Once the cluster appears in
// managed-clusters.yaml the reconciler strips the annotation (converting the
// Secret to a normal managed Secret). Once the window ends, the sweep still
// keeps the Secret while an open pull request registers the cluster, or
// while it cannot read the open pull requests (LO-10 R13). It is reaped on a
// later sweep only when no open pull request registers it (closed without
// merging, or never opened) — no permanent leak.
//
// These two values are defined here, in the shared leaf package both writers
// already import, so the writer and the sweep can never disagree about the
// key or the window. internal/models imports neither orchestrator nor
// clusterreconciler, so this is cycle-free.
const (
	// AnnotationRegistrationPending marks an ArgoCD cluster Secret that was
	// direct-written during registration Stage 1, before the registration PR
	// added the cluster to managed-clusters.yaml. Its value is an RFC3339
	// (UTC) timestamp recording the moment of the direct-write.
	//
	// The "sharko.dev/" prefix matches Sharko's annotation/label domain
	// convention (the maintainer-owned domain — V2-cleanup-59).
	AnnotationRegistrationPending = "sharko.dev/registration-pending"

	// AnnotationRegistrationPendingLegacy is the pre-V2-cleanup-59 key
	// (sharko.io — a domain the project never owned). Only ever READ:
	// a registration that was in flight on a live cluster at upgrade time
	// carries the old key, and the orphan sweep must keep honouring its
	// grace window (deleting the Secret mid-registration is exactly the
	// race this annotation exists to prevent). Writers stamp only
	// AnnotationRegistrationPending; the sweep's clear pass removes both.
	AnnotationRegistrationPendingLegacy = "sharko.io/registration-pending"

	// RegistrationPendingTimeFormat is the layout the annotation value is
	// written and parsed with. RFC3339 so the expiry can be computed from the
	// annotation itself (restart-safe) rather than from process-start or a
	// wall-clock-only assumption.
	RegistrationPendingTimeFormat = time.RFC3339
)

// RegistrationPendingGraceWindow is how long after the direct-write the
// orphan sweep tolerates a registration-pending Secret that is not yet in
// managed-clusters.yaml. 10 minutes covers a slow PR review/merge. Computed
// from the annotation timestamp, so a reconciler restart mid-window still
// honours the remaining grace.
const RegistrationPendingGraceWindow = 10 * time.Minute

// RegistrationPendingTimestamp returns the RFC3339 (UTC) timestamp string to
// stamp on a freshly direct-written cluster Secret. Centralised so the writer
// and any test fixtures format the value identically.
func RegistrationPendingTimestamp(now time.Time) string {
	return now.UTC().Format(RegistrationPendingTimeFormat)
}

// IsRegistrationPending reports whether the given annotations carry an
// unexpired registration-pending marker, evaluated against now.
//
// Return values:
//   - pending == true  → the Secret was direct-written and is still within
//     the grace window; the orphan sweep must NOT delete it.
//   - pending == false, malformed == false → no annotation present, or the
//     annotation timestamp is older than the grace window (expired). Eligible
//     for the normal orphan sweep, which still keeps an expired marked Secret
//     while an open pull request registers its cluster (LO-10 R13).
//   - pending == false, malformed == true → the annotation is present but its
//     value is not a parseable RFC3339 timestamp. Treated as NOT pending
//     (fail-safe: an unparseable marker must never make a Secret immune to
//     the sweep forever). The caller logs a warning.
func IsRegistrationPending(annotations map[string]string, now time.Time) (pending, malformed bool) {
	raw, ok := RegistrationPendingValue(annotations)
	if !ok || raw == "" {
		return false, false
	}
	stamped, err := time.Parse(RegistrationPendingTimeFormat, raw)
	if err != nil {
		// Fail-safe: unparseable timestamp → not pending, flag malformed.
		return false, true
	}
	expiry := stamped.Add(RegistrationPendingGraceWindow)
	return now.Before(expiry), false
}

// RegistrationPendingValue returns the registration-pending annotation value
// under EITHER the canonical or the legacy key, preferring the canonical one.
// ok reports whether either key is present. nil-safe. Readers (the orphan
// sweep's presence checks and logs) must use this accessor rather than a
// direct map lookup so an in-flight registration stamped before the group
// rename keeps its grace window (V2-cleanup-59).
func RegistrationPendingValue(annotations map[string]string) (value string, ok bool) {
	if annotations == nil {
		return "", false
	}
	if v, has := annotations[AnnotationRegistrationPending]; has {
		return v, true
	}
	if v, has := annotations[AnnotationRegistrationPendingLegacy]; has {
		return v, true
	}
	return "", false
}

// PendingPRMark is what a waiting mark on a cluster's ArgoCD connection
// holds (sharko.dev/adopt-pending-pr, sharko.dev/takeover-pending-pr,
// sharko.dev/remove-keep-pending-pr). It names the pull request that still
// has to finish with the connection, the repository that pull request is
// in, and the choices the finish needs, so the finish can run from the
// mark alone when the PR tracker no longer has the pull request (EP3 dry
// run round S, S8 and S11).
//
// Older marks hold only the pull request number ("12"), or "pending" for
// the moment before the number is known. ParsePendingPRMark still reads
// both.
type PendingPRMark struct {
	// PR is the pull request number. 0 means it is not known yet.
	PR int `json:"pr,omitempty"`
	// Repo is the repository the pull request is in, as RepoKeyFromPRURL
	// gives it ("owner/repo", or "org/project/repo" on Azure DevOps).
	// Empty on older marks.
	Repo string `json:"repo,omitempty"`
	// Op says what finishes the mark when it is a keep mark: "remove" or
	// "unadopt". Empty means "remove".
	Op string `json:"op,omitempty"`
	// KeepOldLabels is the take over choice to keep the previous owner's
	// labels. Only a take over mark has it.
	KeepOldLabels *bool `json:"keep_old_labels,omitempty"`
	// At is when the mark was written, RFC3339 in UTC. Empty on older
	// marks.
	At string `json:"at,omitempty"`
}

// Keep mark kinds carried by PendingPRMark.Op.
const (
	PendingMarkOpRemove  = "remove"
	PendingMarkOpUnadopt = "unadopt"
)

// pendingPRMarkLegacyPending is the value older keep marks hold before the
// pull request number is known.
const pendingPRMarkLegacyPending = "pending"

// String renders the mark as the annotation value.
func (m PendingPRMark) String() string {
	b, err := json.Marshal(m)
	if err != nil {
		// Cannot happen for this struct; fall back to the number alone.
		return strconv.Itoa(m.PR)
	}
	return string(b)
}

// WrittenAt is when the mark was written, and false when it does not say.
func (m PendingPRMark) WrittenAt() (time.Time, bool) {
	if m.At == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, m.At)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ParsePendingPRMark reads a waiting mark's value: the current form, a
// plain pull request number, or "pending". ok is false for an empty or
// unreadable value.
func ParsePendingPRMark(value string) (PendingPRMark, bool) {
	v := strings.TrimSpace(value)
	switch {
	case v == "":
		return PendingPRMark{}, false
	case v == pendingPRMarkLegacyPending:
		return PendingPRMark{}, true
	case strings.HasPrefix(v, "{"):
		var m PendingPRMark
		if err := json.Unmarshal([]byte(v), &m); err != nil || m.PR < 0 {
			return PendingPRMark{}, false
		}
		return m, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return PendingPRMark{}, false
	}
	return PendingPRMark{PR: n}, true
}

// PendingPRMarkNames says whether a mark value names pull request prID.
func PendingPRMarkNames(value string, prID int) bool {
	m, ok := ParsePendingPRMark(value)
	return ok && prID > 0 && m.PR == prID
}

// RepoKeyFromPRURL gives the repository part of a pull request address:
// "owner/repo" for GitHub and Gitea (https://host/owner/repo/pull/12,
// .../pulls/12), "org/project/repo" for Azure DevOps
// (https://dev.azure.com/org/project/_git/repo/pullrequest/12). Lower
// case. "" when the address does not look like a pull request address.
func RepoKeyFromPRURL(prURL string) string {
	u, err := url.Parse(strings.TrimSpace(prURL))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	idx := -1
	for i := len(parts) - 1; i >= 0; i-- {
		switch strings.ToLower(parts[i]) {
		case "pull", "pulls", "pullrequest":
			idx = i
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return ""
	}
	before := parts[:idx]
	if n := len(before); n >= 4 && before[n-2] == "_git" {
		return strings.ToLower(before[n-4] + "/" + before[n-3] + "/" + before[n-1])
	}
	if n := len(before); n >= 2 {
		return strings.ToLower(before[n-2] + "/" + strings.TrimSuffix(before[n-1], ".git"))
	}
	return ""
}

// RepoKey is the same repository key RepoKeyFromPRURL gives, built from a
// Git connection. "" when the connection does not say enough.
func (g GitRepoConfig) RepoKey() string {
	c := g
	_ = c.ParseRepoURL()
	if c.Provider == GitProviderAzureDevOps {
		if c.Organization == "" || c.Project == "" || c.Repository == "" {
			return ""
		}
		return strings.ToLower(c.Organization + "/" + c.Project + "/" + c.Repository)
	}
	if c.Owner == "" || c.Repo == "" {
		return ""
	}
	return strings.ToLower(c.Owner + "/" + strings.TrimSuffix(c.Repo, ".git"))
}

// RepoKeysMatch says whether two repository keys name the same
// repository. One may carry an extra leading path part (a Git server under
// a sub-path), so a key that ends with "/" plus the other also matches.
// An empty key never matches.
func RepoKeysMatch(a, b string) bool {
	a, b = strings.ToLower(strings.Trim(a, "/")), strings.ToLower(strings.Trim(b, "/"))
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}
