// Package orchestrator — the v4 engine pin-bump check + upgrade PR
// (v4 Wave 1 Story 2.5).
//
// docs/design/2026-07-30-v4-data-file-format.md section 2.5 defines
// engine/application.yaml, "the engine pin" — a real ArgoCD Application
// whose spec.sources[0].targetRevision names the exact sharko-engine
// chart version deployed. Upgrading the engine is, by design, "a pull
// request that changes one line" — nothing in the user's clusters moves
// until that PR is merged and ArgoCD's own sync picks it up.
//
// This file adds the two operations that make that promise actionable:
//
//   - CheckEnginePin — read-only. Compares the version pinned in the
//     connected repo against the version bundled with this Sharko build
//     (internal/engineversion, generated from charts/sharko-engine/Chart.yaml
//     by Story 2.4's release pipeline). Never errors on a missing pin —
//     that is the ordinary "not a v4 repo yet" case.
//   - UpgradeEnginePin — opens (or, with dryRun, previews) a PR that
//     changes ONLY the pin's targetRevision to the bundled version.
//     gitops.UpdateEnginePinVersion guarantees the minimal diff by
//     construction; this method is the thin PR-opening wrapper around it,
//     mirroring UpgradeAddonGlobal (internal/orchestrator/upgrade.go) and
//     the dry-run preview shape used throughout this package (e.g.
//     SetGlobalAddonValuesWithOp).
package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/engineversion"
	"github.com/MoranWeissman/sharko/internal/gitops"
)

// EnginePinPath is the canonical, non-configurable path of the engine pin
// file inside a v4 GitOps repo (design doc section 2.5). Unlike
// RepoPathsConfig's other paths, this one is not read from server Helm
// values: the engine chart's own ApplicationSet generator arm
// (charts/sharko-engine/templates/appset.yaml) hard-codes the same
// "clusters/<name>.yaml"-style repo convention, so the pin's own location
// is likewise fixed by the format, not by per-connection configuration.
//
// Aliased to BootstrapRootAppPath (constants.go) rather than duplicating the
// literal — v4 Wave 1 Story 4.2 made the bootstrap seed's root-app path AND
// the pin-bump machinery's edit target the very same file, so there is
// exactly one string constant for both.
const EnginePinPath = BootstrapRootAppPath

// EnginePinCheckResult is the outcome of CheckEnginePin.
type EnginePinCheckResult struct {
	// V4Repo is false when engine/application.yaml does not exist in the
	// connected repo — a v3 repo, or a v4 repo that has not been
	// bootstrapped yet. Every other field is the zero value in that case.
	// This is NOT an error condition (story brief: "v3 repos: the check
	// must respond cleanly ... never error").
	V4Repo bool `json:"v4_repo"`

	// BundledVersion is this Sharko build's own engine chart version.
	// Empty when V4Repo is false.
	BundledVersion string `json:"bundled_version,omitempty"`

	// PinnedVersion is the version currently pinned in the repo's
	// engine/application.yaml. Empty when V4Repo is false.
	PinnedVersion string `json:"pinned_version,omitempty"`

	// UpgradeAvailable is true when BundledVersion is newer than
	// PinnedVersion. Always false when V4Repo is false.
	UpgradeAvailable bool `json:"upgrade_available"`

	// Message is a short, human-readable summary safe to surface directly
	// to a user without further formatting.
	Message string `json:"message"`
}

// CheckEnginePin reads the connected repo's engine/application.yaml (if
// any) and compares its pinned engine chart version against the version
// bundled with this Sharko build.
//
// Any read failure from the Git provider (file not found, path not found,
// ...) is treated as "no engine pin here" rather than an error — mirrors
// the readFileIfExists convention used throughout this package for files
// that are optional-by-design (git_helpers.go). Only a file that EXISTS
// but does not parse as the engine pin Sharko itself writes returns an
// error, since that is a genuinely unexpected shape.
func (o *Orchestrator) CheckEnginePin(ctx context.Context) (*EnginePinCheckResult, error) {
	content, ok := o.readFileIfExists(ctx, EnginePinPath)
	if !ok {
		return &EnginePinCheckResult{
			V4Repo:  false,
			Message: "not a v4 repo — no engine pin found at " + EnginePinPath,
		}, nil
	}

	pinned, err := gitops.EnginePinVersion(content, engineversion.BundledChartName)
	if err != nil {
		return nil, fmt.Errorf("reading engine pin at %s: %w", EnginePinPath, err)
	}

	bundled := engineversion.BundledVersion
	upgrade := semverGreater(bundled, pinned)

	msg := fmt.Sprintf("engine is up to date at %s", pinned)
	switch {
	case upgrade:
		msg = fmt.Sprintf("engine upgrade available: %s -> %s", pinned, bundled)
	case pinned != bundled:
		// Pinned version is not older by our parse, but also not textually
		// equal (e.g. unparseable / non-semver string in the pin file).
		// Report the mismatch plainly rather than silently claiming
		// "up to date".
		msg = fmt.Sprintf("engine pinned at %s; this Sharko build ships %s", pinned, bundled)
	}

	return &EnginePinCheckResult{
		V4Repo:           true,
		BundledVersion:   bundled,
		PinnedVersion:    pinned,
		UpgradeAvailable: upgrade,
		Message:          msg,
	}, nil
}

// UpgradeEnginePin opens (or, with dryRun, previews) a pull request that
// changes ONLY the engine pin's targetRevision to this build's bundled
// engine chart version. Returns an error if the repo has no engine pin
// (call CheckEnginePin first — the API handler does exactly this) or if
// the pin is already at the bundled version.
func (o *Orchestrator) UpgradeEnginePin(ctx context.Context, autoMerge *bool, dryRun bool) (*GitResult, error) {
	content, ok := o.readFileIfExists(ctx, EnginePinPath)
	if !ok {
		return nil, fmt.Errorf("no engine pin found at %s — not a v4 repo, or not yet bootstrapped", EnginePinPath)
	}

	pinned, err := gitops.EnginePinVersion(content, engineversion.BundledChartName)
	if err != nil {
		return nil, fmt.Errorf("reading engine pin at %s: %w", EnginePinPath, err)
	}

	bundled := engineversion.BundledVersion
	if pinned == bundled {
		return nil, fmt.Errorf("engine pin is already at %s — nothing to upgrade", bundled)
	}

	updated, err := gitops.UpdateEnginePinVersion(content, engineversion.BundledChartName, bundled)
	if err != nil {
		return nil, fmt.Errorf("updating engine pin: %w", err)
	}

	title := fmt.Sprintf("Upgrade sharko-engine to %s", bundled)

	// Dry-run exit point: preview only, no side effects — mirrors every
	// other dry-run branch in this package (e.g. SetGlobalAddonValuesWithOp).
	if dryRun {
		diff := o.buildFileDiff(EnginePinPath, content, updated, "update")
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite:    []FilePreview{{Path: EnginePinPath, Action: "update", Diff: diff}},
				PRTitle:         title,
				SecretsToCreate: []string{},
			},
		}, nil
	}

	files := map[string][]byte{EnginePinPath: updated}
	op := fmt.Sprintf("upgrade engine pin from %s to %s", pinned, bundled)

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, op,
		o.prMeta(autoMerge, "engine-pin-upgrade", title, "", ""))
	if err != nil {
		return nil, fmt.Errorf("committing engine pin upgrade: %w", err)
	}
	return gitResult, nil
}

// semverParts holds the parsed major.minor.patch components of a version
// string. Deliberately local + minimal (no prerelease/build-metadata
// handling beyond stripping) rather than pulling in a semver dependency —
// go.mod stays untouched and chart versions in this codebase are plain
// major.minor.patch (see charts/sharko-engine/Chart.yaml, charts/sharko/Chart.yaml).
// Mirrors the shape of the (unexported, package-local) semver helper in
// internal/service/upgrade.go — duplicated rather than shared because that
// helper is unexported and this is the only other call site; promote to a
// shared internal/semver package if a third caller shows up.
type semverParts struct {
	major, minor, patch int
}

// parseSemver parses "1.2.3" or "v1.2.3" (optional leading "v", any
// "-prerelease"/"+build" suffix is stripped before parsing). ok is false
// when the numeric major.minor.patch shape does not hold.
func parseSemver(v string) (p semverParts, ok bool) {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return p, false
	}
	var err error
	if p.major, err = strconv.Atoi(parts[0]); err != nil {
		return p, false
	}
	if p.minor, err = strconv.Atoi(parts[1]); err != nil {
		return p, false
	}
	if p.patch, err = strconv.Atoi(parts[2]); err != nil {
		return p, false
	}
	return p, true
}

// semverGreater returns true when a > b in semver order. Falls back to a
// plain string inequality check when either side fails to parse as
// semver — safer than silently reporting "no upgrade available" when the
// version strings simply look unusual (e.g. a hand-edited pin file).
func semverGreater(a, b string) bool {
	pa, okA := parseSemver(a)
	pb, okB := parseSemver(b)
	if !okA || !okB {
		return a != b
	}
	if pa.major != pb.major {
		return pa.major > pb.major
	}
	if pa.minor != pb.minor {
		return pa.minor > pb.minor
	}
	return pa.patch > pb.patch
}
