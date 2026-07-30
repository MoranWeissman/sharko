package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// RepoFileReader is the one-method read surface the v3-marker probe needs.
// Both gitprovider.GitProvider and the API layer's test fakes satisfy it,
// so the probe can be shared by the orchestrator's own InitRepo guard and
// by the read-only /repo/status and /init/status handlers without either
// side re-implementing the marker list.
type RepoFileReader interface {
	GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
}

// HasV3Markers reports whether the repo at branch looks like an ALREADY
// SET UP v3 GitOps repo — one Sharko bootstrapped (or has been managing)
// under the pre-v4 layout.
//
// It exists because "does engine/application.yaml exist?" is the v4
// question, and answering only that question tells a v3 user their repo was
// never bootstrapped. The first-run wizard believes that answer and offers
// Initialize, which would seed the whole v4 folder tree on top of a live v3
// repo. So: no engine pin AND a v3 marker means "initialized, v3 format",
// not "empty".
//
// Two markers, checked in order:
//
//   - V3BootstrapMarkerPath (bootstrap/Chart.yaml) — what every v3
//     bootstrap PR wrote, and the exact file this probe checked before v4
//     Wave 1 Story 4.2 re-pointed it at the engine pin.
//   - V3SecondaryMarkerPath (configuration/managed-clusters.yaml) — for a
//     repo Sharko adopted rather than bootstrapped, or one whose bootstrap
//     chart was reorganised by hand. Present the moment one cluster is
//     registered.
//
// A read error of any kind (missing file, transport failure) counts as
// "marker absent". This is deliberately the SAFE direction for the two
// read-only status handlers, which have their own connection_error
// classification for transport trouble; and for InitRepo the worst case of
// a false "absent" is the pre-existing behaviour, not a new failure.
func HasV3Markers(ctx context.Context, git RepoFileReader, branch string) bool {
	if git == nil {
		return false
	}
	if branch == "" {
		branch = "main"
	}
	for _, marker := range []string{V3BootstrapMarkerPath, V3SecondaryMarkerPath} {
		if body, err := git.GetFileContent(ctx, marker, branch); err == nil && len(body) > 0 {
			return true
		}
	}
	return false
}

// hasV3Markers is the orchestrator-internal form, using the orchestrator's
// own git provider and base branch.
func (o *Orchestrator) hasV3Markers(ctx context.Context) bool {
	if o.git == nil {
		return false
	}
	var reader RepoFileReader = o.git
	return HasV3Markers(ctx, reader, o.gitops.BaseBranch)
}

// hasV3MarkersChecked is the honest form: it tells a genuinely ABSENT
// marker apart from a git host that would not answer (review finding L-9).
//
// The lenient form above cannot make that distinction, and for the
// migration status probe the distinction is the whole point. "No markers"
// is the answer that means "empty repo — initialize it", and offering
// initialize to a live v3 repo whose probe merely timed out is how a
// person ends up seeding a v4 folder tree on top of a running fleet.
//
// A marker found on the FIRST read wins immediately: once the answer is
// "yes, v3", a failure reading the second marker changes nothing.
func (o *Orchestrator) hasV3MarkersChecked(ctx context.Context) (bool, error) {
	if o.git == nil {
		return false, nil
	}
	branch := o.gitops.BaseBranch
	if branch == "" {
		branch = "main"
	}
	var firstErr error
	for _, marker := range []string{V3BootstrapMarkerPath, V3SecondaryMarkerPath} {
		body, err := o.git.GetFileContent(ctx, marker, branch)
		if err == nil {
			if len(body) > 0 {
				return true, nil
			}
			continue
		}
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			continue
		}
		if firstErr == nil {
			firstErr = fmt.Errorf(
				"could not read %s from the %s branch, so Sharko cannot tell whether this repo has been set up: %w",
				marker, branch, err)
		}
	}
	if firstErr != nil {
		return false, firstErr
	}
	return false, nil
}

// compile-time assurance that the real provider satisfies the narrow
// reader interface (so a future GitProvider signature change fails here
// rather than at a call site).
var _ RepoFileReader = (gitprovider.GitProvider)(nil)
