package orchestrator

import (
	"errors"
	"fmt"
	"strings"
)

// InvalidCredsSourceError is returned by the creds-source resolver when the
// request's creds_source is unknown, or when the (explicit or derived)
// source is inconsistent with the rest of the request (e.g. an inline source
// with no kubeconfig). It is a CALLER error — the API layer maps it to 400.
//
// This mirrors the *AddonNotInCatalogError pattern: a typed error plus an
// Is-helper the handler uses to choose a 4xx status without string-matching.
//
// Err optionally wraps a sentinel identifying WHICH validation case fired
// (e.g. ErrMissingInlineKubeconfig). This lets a caller narrow via
// errors.Is(err, ErrMissingInlineKubeconfig) instead of string-matching Msg —
// see RegisterCluster's self-managed relaxation (V2-cleanup-60 M3), which
// must swallow ONLY that one specific case and let every other
// InvalidCredsSourceError surface.
type InvalidCredsSourceError struct {
	Msg string
	Err error
}

func (e *InvalidCredsSourceError) Error() string {
	if e == nil || e.Msg == "" {
		return "invalid creds_source"
	}
	return e.Msg
}

// Unwrap exposes the wrapped sentinel (if any) so errors.Is can match
// through the concrete *InvalidCredsSourceError wrapper.
func (e *InvalidCredsSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsInvalidCredsSource reports whether err is (or wraps) an
// *InvalidCredsSourceError. The API layer uses this to choose a 400 status.
func IsInvalidCredsSource(err error) bool {
	var target *InvalidCredsSourceError
	return errors.As(err, &target)
}

// ErrMissingInlineKubeconfig is the ONE InvalidCredsSourceError case
// RegisterCluster's self-managed relaxation is allowed to swallow: an
// inline creds_source with no kubeconfig payload supplied at all. A
// self-managed connection may register with zero credentials — Sharko never
// writes the ArgoCD cluster Secret for it, so there is nothing the
// credentials are strictly required for. Every OTHER InvalidCredsSourceError
// (e.g. a contradictory secret_path set alongside an inline source) must
// still surface even for self-managed registrations (V2-cleanup-60 M3).
var ErrMissingInlineKubeconfig = errors.New("creds_source inline-kubeconfig requires a kubeconfig")

// ResolveCredsSource computes the effective CredsSource for a request.
//
// Backward-compatibility is the whole point: when req.CredsSource is empty
// the source is DERIVED from Provider so every request that works today keeps
// behaving EXACTLY as it did before this field existed:
//
//   - Provider == "kubeconfig"  → inline-kubeconfig (the inline path).
//   - Provider != "kubeconfig"  → secret-kubeconfig (the backend path).
//
// We collapse the empty-non-kubeconfig case to the single canonical label
// secret-kubeconfig because v1 routes secret-kubeconfig AND eks-token through
// the SAME credProvider.GetCredentials path — the secret backend sniffs raw
// kubeconfig YAML vs. structured EKS JSON itself (internal/providers/aws_sm.go).
// So at the orchestrator both backend labels mean one thing: "ask the backend".
//
// When req.CredsSource IS set it is authoritative and wins over Provider:
// Provider becomes optional cluster-type metadata. An unknown value is a
// caller error (→ *InvalidCredsSourceError → 400).
//
// Exported so the API handler can run the same derivation for its edge-level
// field validation (keeping one source of truth for the inline-vs-backend
// decision). It does NOT run the kubeconfig-presence validation — that is
// validateCredsSource, applied inside RegisterCluster.
func ResolveCredsSource(req RegisterClusterRequest) (CredsSource, error) {
	if req.CredsSource == "" {
		if req.Provider == "kubeconfig" {
			return CredsSourceInlineKubeconfig, nil
		}
		// A pasted kubeconfig with NOTHING else said (no creds_source, no
		// provider) unambiguously means the inline path (V2-cleanup-60.4):
		// deriving the backend source here would silently ignore the pasted
		// payload and look up a backend secret that does not exist — the
		// eks-token/backend default trap. An explicit non-kubeconfig
		// Provider still wins (the caller said "backend"), preserving every
		// pre-field request byte-for-byte.
		if req.Provider == "" && strings.TrimSpace(req.Kubeconfig) != "" {
			return CredsSourceInlineKubeconfig, nil
		}
		return CredsSourceSecretKubeconfig, nil
	}

	switch req.CredsSource {
	case CredsSourceInlineKubeconfig, CredsSourceSecretKubeconfig, CredsSourceEKSToken:
		return req.CredsSource, nil
	default:
		return "", &InvalidCredsSourceError{Msg: fmt.Sprintf(
			"unknown creds_source %q (want %s, %s, or %s)",
			req.CredsSource,
			CredsSourceInlineKubeconfig,
			CredsSourceSecretKubeconfig,
			CredsSourceEKSToken,
		)}
	}
}

// isInlineSource reports whether the effective source is the inline-kubeconfig
// path (ParseInlineKubeconfig, credProvider NOT required). Everything else is
// a backend (secret) source resolved through credProvider.GetCredentials.
func isInlineSource(src CredsSource) bool {
	return src == CredsSourceInlineKubeconfig
}

// validateCredsSource performs cheap per-source validation that improves error
// messages without changing what today's accepted requests do.
//
//   - inline-kubeconfig with a secret_path set → caller error (V2-cleanup-60
//     M3). secret_path is a backend-only lookup key (secret-kubeconfig /
//     eks-token); it has no meaning for the inline path, so a request
//     setting both is contradicting itself about which creds path it wants.
//     No accepted-today request sets both (the UI sends a disjoint field set
//     per source — see ui/src/views/ClustersOverview.tsx buildRegisterPayload),
//     so this cannot fire for anything that works today.
//   - inline-kubeconfig with an empty Kubeconfig → caller error (no payload to
//     parse), wrapping the ErrMissingInlineKubeconfig sentinel. This fires for
//     BOTH the explicit and the derived inline case; today the inline path
//     already fails on an empty kubeconfig (the parser rejects it), so this
//     only sharpens the message — it never accepts a request that is
//     rejected today, nor rejects one that is accepted today. This is the
//     ONLY case a self-managed registration may relax (M3) — checked AFTER
//     the secret_path check above so a contradictory secret_path still
//     surfaces even when Kubeconfig is also empty.
//   - backend source with BOTH SecretPath AND Name empty → caller error.
//     Name is always required upstream (RegisterCluster rejects an empty name
//     before we get here), so in practice this never trips; it documents the
//     contract that the backend path needs a lookup key and preserves today's
//     fallback (Name serves as the lookup key when SecretPath is empty).
//
// Validation must NOT fire for any request that is accepted today.
func validateCredsSource(src CredsSource, req RegisterClusterRequest) error {
	if isInlineSource(src) {
		if req.SecretPath != "" {
			return &InvalidCredsSourceError{Msg: fmt.Sprintf(
				"creds_source %s does not use secret_path (got %q); secret_path only applies to backend creds sources",
				CredsSourceInlineKubeconfig, req.SecretPath)}
		}
		if req.Kubeconfig == "" {
			return &InvalidCredsSourceError{
				Msg: fmt.Sprintf("creds_source %s requires a kubeconfig", CredsSourceInlineKubeconfig),
				Err: ErrMissingInlineKubeconfig,
			}
		}
		return nil
	}

	// Backend source (secret-kubeconfig / eks-token). Today's ELSE path falls
	// back to req.Name as the lookup key when SecretPath is empty, so we only
	// error if BOTH are empty — never over-tighten the existing behavior.
	if req.SecretPath == "" && req.Name == "" {
		return &InvalidCredsSourceError{Msg: fmt.Sprintf(
			"creds_source %s requires a secret_path or a cluster name to look up", src)}
	}
	return nil
}
