package orchestrator

import (
	"context"
	"errors"
	"fmt"
)

// ErrV4RepoUnsupported marks an operation that still writes the v3 cluster
// registry (configuration/managed-clusters.yaml) and therefore must not run
// against a repo in the v4 data-file format.
//
// Why a refusal rather than a best-effort write: on a v4 repo the cluster
// registry lives at managed-clusters.yaml (design doc §2.4). An operation
// that writes managed-clusters.yaml there creates a SECOND registry file,
// and the reconciler prefers the v3 file whenever both exist — so every
// cluster registered the v4 way instantly disappears from the desired
// state, becomes an orphan, and has its ArgoCD cluster Secret deleted.
// Losing the fleet's Secrets is not a recoverable "oops"; refusing the
// operation is.
//
// The v4 implementations of these operations are Wave 2 takeover scope.
// Callers map this to HTTP 409 — the request is well-formed, the repo is
// simply in a state this operation does not handle yet.
var ErrV4RepoUnsupported = errors.New("operation not supported on a v4 repo")

// IsV4RepoUnsupported reports whether err is a v4-repo refusal. The API
// layer uses it to answer 409 instead of the default 502.
func IsV4RepoUnsupported(err error) bool {
	return errors.Is(err, ErrV4RepoUnsupported)
}

// refuseOnV4Repo returns a plain-English ErrV4RepoUnsupported when the
// connected repo is v4-format, and nil otherwise.
//
// operation is the thing the person asked for, phrased so it reads as a
// sentence: "adopting a cluster", "removing a cluster". Call this BEFORE
// any git read-modify-write, branch creation, or ArgoCD mutation — the
// whole point is that nothing happens.
//
// FAIL CLOSED (v4 Wave 2 review, the isV4Repo fail-open flag). When the
// layout probe cannot get an answer at all — an expired token, a rate
// limit, the git host having a bad minute — this refuses too, with the
// upstream error attached. The old behaviour treated an unanswerable
// probe as "not a v4 repo" and let the v3 write proceed, which on a repo
// that really was v4 recreates configuration/managed-clusters.yaml
// alongside managed-clusters.yaml. The reconciler prefers the v3 file
// whenever both exist, so every cluster registered the v4 way instantly
// becomes an orphan and loses its ArgoCD connection Secret. Refusing
// costs a retry; guessing costs the fleet.
//
// The probe-failure refusal is deliberately NOT an ErrV4RepoUnsupported:
// "this repo is v4" and "we could not find out" are different facts and
// deserve different answers. Callers map the former to 409 and everything
// else to a 502-class upstream error, which is the honest shape for a git
// host that would not answer.
func (o *Orchestrator) refuseOnV4Repo(ctx context.Context, operation string) error {
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return fmt.Errorf("Sharko stopped before %s: %w", operation, err)
	}
	if !v4 {
		return nil
	}
	return fmt.Errorf("%w: %s is not yet supported on a v4 repo — coming with the takeover work", ErrV4RepoUnsupported, operation)
}

// ErrMixedRepoLayout marks a repo that carries the v4 engine pin AND the
// old v3 files at the same time (review finding F4). Callers map it to 409,
// like the other "the repo is in a state this does not handle" refusals.
var ErrMixedRepoLayout = errors.New("repo has both layouts")

// refuseOnMixedLayout stops a v4 catalog/addon write on a half-converted
// repo.
//
// The two halves are read by different parts of Sharko: the engine renders
// from catalog.yaml + clusters/*.yaml, while the cluster reconciler prefers
// configuration/managed-clusters.yaml whenever it exists. On a repo with
// both, a catalog change lands in files the reconciler is not looking at,
// and the person sees a merged pull request that changed nothing. Refusing
// with the reason is better than writing into the half nobody reads.
//
// LENIENT on a probe failure, deliberately, and the opposite stance from
// refuseOnV4Repo: there, "we could not check" could mean a v3 write landing
// on a v4 repo and taking the fleet's Secrets with it. Here the only thing
// at stake is an ordinary, correct write on an ordinary, correct v4 repo —
// turning every git hiccup into "your repo is half-converted" would be a
// lie about the repo, and a far more common outcome than the real thing.
func (o *Orchestrator) refuseOnMixedLayout(ctx context.Context) error {
	v4, err := o.isV4Repo(ctx)
	if err != nil || !v4 {
		return nil
	}
	mixed, markerErr := o.hasV3MarkersChecked(ctx)
	if markerErr != nil || !mixed {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMixedRepoLayout, MixedLayoutMessage)
}

// V4CatalogWriteDoor is the sentence every legacy catalog writer hands back
// on a v4 repo. One constant so a person reads the same pointer whichever
// door they came through, and so the UI can match on it if it ever needs to.
const V4CatalogWriteDoor = `this repo keeps its approved addons in catalog.yaml, and the only way in is the approval pull request — use "Add to catalog" in the UI, or POST /api/v1/catalog/addons`

// V4EnableDoor is the same pointer for switching an addon on or off.
const V4EnableDoor = "this repo switches addons on per cluster in clusters/<cluster>.yaml — use POST /api/v1/v4/clusters/{cluster}/addons/{addon} (and the addon has to be in catalog.yaml first)"

// refuseV3ShapedWriteOnV4Repo is refuseOnV4Repo with a pointer at the door
// that DOES work, for the operations that have a v4 replacement today.
//
// It exists because the three legacy catalog writers (POST /addons,
// PATCH /addons/{name}, DELETE /addons/{name}) and the v3
// EnableAddon/DisableAddon pair all write files a v4 repo does not have:
// configuration/addons-catalog.yaml and the addon on/off labels in
// configuration/managed-clusters.yaml. On a v4 repo those writes CREATE
// those files. A second catalog nothing reads is merely confusing; a second
// cluster registry is not — the reconciler prefers the v3 file whenever
// both exist, so every v4-registered cluster loses its ArgoCD connection
// Secret. Before this gate existed, EnableAddon/DisableAddon happened to
// fail on a v4 repo only because the file they read was missing; that is an
// accident, not a guarantee, and it stops holding the moment anything
// recreates the file.
//
// Fail closed on an unanswerable probe, for the same reason refuseOnV4Repo
// does: refusing costs a retry, guessing costs the fleet.
func (o *Orchestrator) refuseV3ShapedWriteOnV4Repo(ctx context.Context, operation, door string) error {
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return fmt.Errorf("Sharko stopped before %s: %w", operation, err)
	}
	if !v4 {
		return nil
	}
	return fmt.Errorf("%w: %s writes files this repo does not use — %s", ErrV4RepoUnsupported, operation, door)
}
