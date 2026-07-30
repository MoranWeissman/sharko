// Package orchestrator — the v3-side write gate (v4 Wave 2 Story 5.1).
//
// This is the mirror image of v4_guard.go. That file refuses operations
// that would write the V3 registry onto a V4 repo; this one refuses
// operations that would write the V3 format at all, once a repo is
// recognised as v3 and a one-PR migration is available.
//
// Why block rather than keep writing v3: every v3 write deepens the thing
// the migration has to convert, and a migration PR raised against a moving
// target is a merge conflict waiting to happen. Reads are untouched — a
// person can still see every cluster, every addon, and every values file
// while they decide when to press Migrate.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
)

// V3MigrationRequiredMessage is the ONE sentence every blocked v3 write
// answers with — the HTTP handlers as a 409 body, the orchestrator as the
// error text. One constant so the person reads the same words whichever
// door they came through, and so a test can pin the wording.
const V3MigrationRequiredMessage = "this repo uses the v3 format — migrate to continue (one PR); reads keep working"

// ErrV3MigrationRequired marks an operation refused because the connected
// repo is still in the v3 data-file format and has a migration available.
//
// Callers map this to HTTP 409: the request is well-formed and the
// endpoint exists — the repo is simply in a state this operation no longer
// writes, the same shape as ErrV4RepoUnsupported on the other side.
var ErrV3MigrationRequired = errors.New(V3MigrationRequiredMessage)

// IsV3MigrationRequired reports whether err is a v3 migrate-first refusal.
// The API layer uses it to answer 409 instead of the default 502.
func IsV3MigrationRequired(err error) bool {
	return errors.Is(err, ErrV3MigrationRequired)
}

// RefuseOnV3Repo returns ErrV3MigrationRequired when the connected repo is
// recognised as v3-format, and nil otherwise.
//
// Three states, three answers:
//
//   - v4 repo (engine pin present) → nil. The v4 guards in v4_guard.go
//     own that case; this gate must not double-refuse.
//   - v3 repo (no engine pin, a v3 marker present) → refusal.
//   - empty repo (neither) → nil. There is nothing to migrate, and the
//     bootstrap path must stay reachable.
//
// Call this BEFORE any git read-modify-write, branch creation, or ArgoCD
// mutation — the whole point is that nothing happens.
//
// Exported (unlike refuseOnV4Repo) because the API layer renders the same
// decision for handlers that never reach an orchestrator method, and the
// two must not drift.
// A layout probe that cannot get an answer refuses too, with the upstream
// error attached — same fail-closed stance as refuseOnV4Repo, and for the
// same reason: this gate stands in front of writes.
func (o *Orchestrator) RefuseOnV3Repo(ctx context.Context) error {
	v3, err := o.isV3Repo(ctx)
	if err != nil {
		return fmt.Errorf("Sharko stopped before writing: %w", err)
	}
	if !v3 {
		return nil
	}
	return ErrV3MigrationRequired
}

// IsV3Repo reports whether the connected repo is in the v3 data-file
// format: no engine pin, but at least one v3 marker. Exported so the
// migration status/preview endpoints and the write gate share one answer.
//
// Lenient: an unanswerable probe reports false. Every caller of THIS form
// is a read or a status surface. Writes go through RefuseOnV3Repo, which
// fails closed.
func (o *Orchestrator) IsV3Repo(ctx context.Context) bool {
	v3, err := o.isV3Repo(ctx)
	return err == nil && v3
}

// isV3Repo is the error-returning form the write gate uses.
func (o *Orchestrator) isV3Repo(ctx context.Context) (bool, error) {
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return false, err
	}
	if v4 {
		return false, nil
	}
	return o.hasV3Markers(ctx), nil
}
