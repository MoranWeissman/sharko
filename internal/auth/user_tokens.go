// Package auth — per-user Git tokens (encrypted at rest).
//
// Used by the v1.20 tiered attribution model: when a user is configured with a
// personal GitHub PAT, Tier 2 (configuration) writes use that PAT instead of the
// shared service token, so the resulting Git commit is authored by the user.
//
// Storage:
//   - K8s mode: stored in the same Secret as user passwords (`<secretName>`),
//     under the key `<username>.github_token`, encrypted with SHARKO_ENCRYPTION_KEY
//     using AES-256-GCM (the same key/scheme used by the connection store).
//   - Local mode: in-memory only, never persisted (dev convenience).
//
// PATs are write-once-read-back. The API never returns the plaintext token; it
// only exposes a `has_github_token` boolean on the user profile.
package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/crypto"
)

// userTokenSuffix is the K8s Secret key suffix for a per-user GitHub PAT.
const userTokenSuffix = ".github_token"

// SetUserGitHubToken stores a per-user GitHub PAT, encrypted with the provided
// encryption key. In K8s mode it persists to the auth Secret; in local mode it
// is held only in memory. Returns an error if the user does not exist.
//
// An empty token is rejected — callers should use ClearUserGitHubToken to remove.
func (s *Store) SetUserGitHubToken(username, token, encryptionKey string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if token == "" {
		return fmt.Errorf("token is required")
	}
	if encryptionKey == "" {
		return fmt.Errorf("encryption key not configured")
	}

	s.mu.RLock()
	_, exists := s.users[username]
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("user %q not found", username)
	}

	encrypted, err := crypto.Encrypt([]byte(token), encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypting token: %w", err)
	}

	if s.mode == ModeK8s {
		ctx := context.Background()
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading auth Secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[username+userTokenSuffix] = []byte(encrypted)
		if _, err := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("writing auth Secret: %w", err)
		}
	}

	s.mu.Lock()
	if s.userTokens == nil {
		s.userTokens = make(map[string]string)
	}
	s.userTokens[username] = encrypted
	s.mu.Unlock()

	slog.Info("per-user github token set", "username", username)
	return nil
}

// GetUserGitHubToken returns the decrypted per-user GitHub PAT for the given
// username, or "" if the user has none configured. Returns an error only if
// decryption fails (e.g. wrong key).
func (s *Store) GetUserGitHubToken(username, encryptionKey string) (string, error) {
	if username == "" {
		return "", nil
	}
	if encryptionKey == "" {
		return "", fmt.Errorf("encryption key not configured")
	}

	// Reload from K8s on each lookup so that tokens set in another replica
	// are visible immediately. Cheap — same path as ValidateCredentials.
	if s.mode == ModeK8s {
		if err := s.reload(); err != nil {
			slog.Warn("failed to reload auth data for token lookup", "error", err)
		}
	}

	s.mu.RLock()
	encrypted, ok := s.userTokens[username]
	s.mu.RUnlock()

	if !ok || encrypted == "" {
		return "", nil
	}

	plain, err := crypto.Decrypt(encrypted, encryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypting token: %w", err)
	}
	return string(plain), nil
}

// HasUserGitHubToken reports whether the named user has a PAT configured,
// without decrypting it.
func (s *Store) HasUserGitHubToken(username string) bool {
	if username == "" {
		return false
	}
	if s.mode == ModeK8s {
		if err := s.reload(); err != nil {
			slog.Warn("failed to reload auth data for has-token check", "error", err)
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.userTokens[username]
	return ok && v != ""
}

// ClearUserGitHubToken removes a user's PAT. Idempotent — calling on a user
// without a configured token is a no-op (returns nil).
func (s *Store) ClearUserGitHubToken(username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}

	if s.mode == ModeK8s {
		ctx := context.Background()
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading auth Secret: %w", err)
		}
		if secret.Data != nil {
			delete(secret.Data, username+userTokenSuffix)
			if _, err := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("writing auth Secret: %w", err)
			}
		}
	}

	s.mu.Lock()
	delete(s.userTokens, username)
	s.mu.Unlock()

	slog.Info("per-user github token cleared", "username", username)
	return nil
}

// hydrateTokensFromSecretData populates s.userTokens from raw Secret data.
// Called by reload() under the existing lock to keep token state in sync with
// the password hash state.
//
// Caller must hold s.mu.Lock() — modifies the map in place.
func (s *Store) hydrateTokensFromSecretData(data map[string][]byte) {
	if s.userTokens == nil {
		s.userTokens = make(map[string]string)
	}
	// Rebuild fully from the Secret to drop deleted entries.
	tokens := make(map[string]string)
	for key, val := range data {
		if !strings.HasSuffix(key, userTokenSuffix) {
			continue
		}
		username := strings.TrimSuffix(key, userTokenSuffix)
		if username == "" || len(val) == 0 {
			continue
		}
		tokens[username] = string(val)
	}
	s.userTokens = tokens
}
