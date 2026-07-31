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
