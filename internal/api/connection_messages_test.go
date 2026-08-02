package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
)

func TestPlainConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		err      error
		wantNone bool
	}{
		{name: "nil error returns empty", kind: "git", err: nil, wantNone: true},
		{name: "git network failure", kind: "git", err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused")},
		{name: "git auth failure", kind: "git", err: errors.New("401 unauthorized")},
		{name: "argocd forbidden", kind: "argocd", err: errors.New("403 forbidden")},
		{name: "vault unknown failure", kind: "vault", err: errors.New("some AWS SDK internal error XYZ123")},
		{name: "unrecognized kind falls back to generic wording", kind: "mystery", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plainConnectionError(tt.kind, tt.err)
			if tt.wantNone {
				if got != "" {
					t.Fatalf("expected empty message for nil error, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected a non-empty plain message")
			}
			// The whole point: raw Go/SDK error text must never leak through.
			if strings.Contains(got, tt.err.Error()) {
				t.Fatalf("plain message leaked the raw error text: %q", got)
			}
			if !strings.HasPrefix(got, "Sharko can't reach") && !strings.HasPrefix(got, "Sharko can") {
				t.Fatalf("expected message to open in plain first-person Sharko voice, got %q", got)
			}
		})
	}
}

// TestPlainConnectionError_KindAwareAuthHint — review findings r1, M9.
// verify.Hint's ERR_AUTH wording ("regenerate the kubeconfig/token") is
// written for cluster-registration credentials. A Git or secrets-provider
// connection test must never tell the operator to regenerate a kubeconfig —
// that's the wrong credential entirely.
func TestPlainConnectionError_KindAwareAuthHint(t *testing.T) {
	authErr := errors.New("401 unauthorized")

	gitMsg := plainConnectionError("git", authErr)
	if strings.Contains(gitMsg, "kubeconfig") {
		t.Errorf("git auth message must not mention kubeconfig, got %q", gitMsg)
	}
	if !strings.Contains(gitMsg, "Git token") {
		t.Errorf("git auth message should point at the Git token/PAT, got %q", gitMsg)
	}

	vaultMsg := plainConnectionError("vault", authErr)
	if strings.Contains(vaultMsg, "kubeconfig") {
		t.Errorf("vault auth message must not mention kubeconfig, got %q", vaultMsg)
	}
	if !strings.Contains(vaultMsg, "secrets-provider credentials") && !strings.Contains(vaultMsg, "secrets store") {
		t.Errorf("vault auth message should point at the secrets-provider credentials, got %q", vaultMsg)
	}
}

// TestConnectionErrorFields_KindAwareAuthHint checks the hint FIELD (not just
// the message) that connectionErrorFields returns — this is the second copy
// of the cluster-flavored wording (via shapeError), which needed its own
// override (review findings r1, M9).
func TestConnectionErrorFields_KindAwareAuthHint(t *testing.T) {
	authErr := errors.New("401 unauthorized")

	gitBody := connectionErrorFields("git", authErr)
	gitHint, _ := gitBody["hint"].(string)
	if strings.Contains(gitHint, "kubeconfig") {
		t.Errorf("git hint field must not mention kubeconfig, got %q", gitHint)
	}
	if !strings.Contains(gitHint, "Git token") {
		t.Errorf("git hint field should point at the Git token/PAT, got %q", gitHint)
	}

	vaultBody := connectionErrorFields("vault", authErr)
	vaultHint, _ := vaultBody["hint"].(string)
	if strings.Contains(vaultHint, "kubeconfig") {
		t.Errorf("vault hint field must not mention kubeconfig, got %q", vaultHint)
	}
	if !strings.Contains(vaultHint, "secrets-provider credentials") {
		t.Errorf("vault hint field should point at the secrets-provider credentials, got %q", vaultHint)
	}
}

// TestConnectionErrorFields_ArgocdAuthFailure_KeepsSentinelHint — the "cluster
// → current text" half of M9: kinds other than git/vault (here, argocd via
// its own argocd.ErrTokenInvalid sentinel) must keep shapeError's existing,
// already-correct hint untouched by the git/vault override.
func TestConnectionErrorFields_ArgocdAuthFailure_KeepsSentinelHint(t *testing.T) {
	err := fmt.Errorf("checking token: %w", argocd.ErrTokenInvalid)
	body := connectionErrorFields("argocd", err)
	hint, _ := body["hint"].(string)
	if !strings.Contains(hint, "Settings") || !strings.Contains(hint, "Connections") {
		t.Errorf("expected the existing ArgoCD-sentinel hint naming Settings → Connections, got %q", hint)
	}
	if strings.Contains(hint, "Git token") || strings.Contains(hint, "secrets-provider") {
		t.Errorf("argocd kind must not get the git/vault override, got %q", hint)
	}
}
