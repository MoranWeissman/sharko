// Package auth — API token persistence (v4-coherence lane J).
//
// Before this file, API tokens lived only in the Store's in-memory map, so
// every machine token died on pod restart — breaking the machine journey and
// the restart-recovery promise (NFR12). Now the store persists tokens:
//
//   - K8s mode: a dedicated Secret Sharko owns in its own namespace
//     (`sharko-api-tokens`, labeled app.kubernetes.io/managed-by: sharko).
//   - Local mode: a 0600 JSON file in a 0700 directory, default
//     ~/.sharko/api-tokens.json (override with SHARKO_API_TOKENS_FILE) —
//     the same pattern the initial-admin credential file uses.
//
// What is stored: the bcrypt HASH of each token value plus its metadata
// (name, role, created, expiry, created-by, last-used). The raw token value
// is NEVER stored anywhere — it is returned once at creation time and then
// only exists on the caller's side. That is exactly the in-memory contract
// tokens.go already enforces; persistence writes the same bytes down.
//
// Lifecycle: InitTokenPersistence loads persisted tokens at startup and
// installs the write-through persister. Create / renew / revoke write
// through under the store's mutex; a failed write rolls the in-memory
// change back and surfaces the error to the API caller, so a token can
// never LOOK created (or revoked, or renewed) while the durable copy
// silently disagrees. LastUsed is deliberately NOT written per
// authentication — a Secret update per API request would hammer the API
// server — it rides along on the next create/renew/revoke write instead.
//
// A store that never calls InitTokenPersistence (demo mode, unit tests)
// keeps the pure in-memory behavior unchanged.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// APITokensSecretName is the dedicated Kubernetes Secret carrying persisted
// API tokens (bcrypt hashes + metadata, never raw values) in K8s mode.
const APITokensSecretName = "sharko-api-tokens"

// apiTokensSecretKey is the single data key inside APITokensSecretName that
// holds the JSON payload.
const apiTokensSecretKey = "tokens.json"

// EnvAPITokensFile overrides where local-mode (non-cluster) runs persist
// API tokens. Default: ~/.sharko/api-tokens.json.
const EnvAPITokensFile = "SHARKO_API_TOKENS_FILE"

// persistedAPIToken is the on-disk / in-Secret shape of one token.
//
// It exists separately from APIToken because APIToken tags Hash `json:"-"`
// (the hash must never leave the process over the API) and carries derived
// read-only fields (Status, Expired, ExpiringSoon) that must not be stored.
// Here the hash IS the point of the record — and it is a bcrypt hash, never
// the raw token value.
type persistedAPIToken struct {
	Name      string     `json:"name"`
	Hash      string     `json:"hash"` // bcrypt hash of the token value — NEVER the raw value
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // nil = legacy-no-expiry, preserved as-is
	LastUsed  time.Time  `json:"last_used_at,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
}

// persistedAPITokens is the full payload written to the Secret key / file.
type persistedAPITokens struct {
	Version int                          `json:"version"`
	Tokens  map[string]persistedAPIToken `json:"tokens"`
}

// persistedAPITokensVersion is the current payload version. Bump only with a
// migration path for older payloads.
const persistedAPITokensVersion = 1

// marshalAPITokens converts the in-memory token map to the persisted JSON
// payload. The caller must hold s.mu (read or write).
func marshalAPITokens(tokens map[string]*APIToken) ([]byte, error) {
	out := persistedAPITokens{
		Version: persistedAPITokensVersion,
		Tokens:  make(map[string]persistedAPIToken, len(tokens)),
	}
	for name, t := range tokens {
		p := persistedAPIToken{
			Name:      t.Name,
			Hash:      t.Hash,
			Role:      t.Role,
			CreatedAt: t.CreatedAt,
			LastUsed:  t.LastUsed,
			CreatedBy: t.CreatedBy,
		}
		if t.ExpiresAt != nil {
			exp := *t.ExpiresAt
			p.ExpiresAt = &exp
		}
		out.Tokens[name] = p
	}
	return json.MarshalIndent(out, "", "  ")
}

// unmarshalAPITokens parses a persisted payload back into token structs.
func unmarshalAPITokens(data []byte) (map[string]*APIToken, error) {
	var in persistedAPITokens
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("parsing persisted API tokens: %w", err)
	}
	tokens := make(map[string]*APIToken, len(in.Tokens))
	for name, p := range in.Tokens {
		if name == "" || p.Hash == "" {
			// A record without a hash can never authenticate — skip it
			// rather than carrying dead weight forward.
			continue
		}
		t := &APIToken{
			Name:      p.Name,
			Hash:      p.Hash,
			Role:      p.Role,
			CreatedAt: p.CreatedAt,
			LastUsed:  p.LastUsed,
			CreatedBy: p.CreatedBy,
		}
		if t.Name == "" {
			t.Name = name
		}
		if p.ExpiresAt != nil {
			exp := *p.ExpiresAt
			t.ExpiresAt = &exp
		}
		tokens[name] = t
	}
	return tokens, nil
}

// tokenPersister is the storage seam behind API token persistence. Two
// implementations: the K8s Secret (in-cluster) and the local 0600 file.
type tokenPersister interface {
	// load reads the persisted tokens. An absent Secret / file (first boot)
	// is NOT an error — it returns an empty map.
	load(ctx context.Context) (map[string]*APIToken, error)
	// save writes the full token set through to durable storage.
	save(ctx context.Context, tokens map[string]*APIToken) error
	// where names the backing store for logs and errors ("Secret ns/name",
	// "file /path"). Never includes secret material.
	where() string
}

// secretTokenPersister persists tokens in the dedicated sharko-api-tokens
// Kubernetes Secret in Sharko's own namespace.
type secretTokenPersister struct {
	clientset kubernetes.Interface
	namespace string
	name      string
}

func (p *secretTokenPersister) where() string {
	return fmt.Sprintf("Secret %s/%s", p.namespace, p.name)
}

func (p *secretTokenPersister) load(ctx context.Context) (map[string]*APIToken, error) {
	secret, err := p.clientset.CoreV1().Secrets(p.namespace).Get(ctx, p.name, metav1.GetOptions{})
	if err != nil {
		if apierrorsIsNotFound(err) {
			// First boot — the Secret does not exist yet. It will be
			// created on the first write.
			return map[string]*APIToken{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p.where(), err)
	}
	data, ok := secret.Data[apiTokensSecretKey]
	if !ok || len(data) == 0 {
		return map[string]*APIToken{}, nil
	}
	return unmarshalAPITokens(data)
}

func (p *secretTokenPersister) save(ctx context.Context, tokens map[string]*APIToken) error {
	payload, err := marshalAPITokens(tokens)
	if err != nil {
		return fmt.Errorf("marshal API tokens: %w", err)
	}

	secClient := p.clientset.CoreV1().Secrets(p.namespace)
	secret, err := secClient.Get(ctx, p.name, metav1.GetOptions{})
	if err != nil {
		if !apierrorsIsNotFound(err) {
			return fmt.Errorf("read %s: %w", p.where(), err)
		}
		created := &corev1Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      p.name,
				Namespace: p.namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sharko",
					"app.kubernetes.io/component":  "api-tokens",
				},
			},
			Type: corev1SecretTypeOpaque,
			Data: map[string][]byte{
				apiTokensSecretKey: payload,
			},
		}
		if _, err := secClient.Create(ctx, created, metav1.CreateOptions{}); err != nil {
			if !apierrorsIsAlreadyExists(err) {
				return fmt.Errorf("create %s: %w", p.where(), err)
			}
			// Race: created between Get and Create — fall through to update.
			secret, err = secClient.Get(ctx, p.name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("re-read %s: %w", p.where(), err)
			}
		} else {
			return nil
		}
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[apiTokensSecretKey] = payload
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	// Ownership-label invariant: every Sharko-created object carries the
	// managed-by label, kept in place across updates.
	secret.Labels["app.kubernetes.io/managed-by"] = "sharko"
	secret.Labels["app.kubernetes.io/component"] = "api-tokens"
	if _, err := secClient.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update %s: %w", p.where(), err)
	}
	return nil
}

// fileTokenPersister persists tokens in a 0600 JSON file (0700 parent
// directory) for local-mode (non-cluster) runs.
type fileTokenPersister struct {
	path string
}

func (p *fileTokenPersister) where() string {
	return "file " + p.path
}

func (p *fileTokenPersister) load(_ context.Context) (map[string]*APIToken, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			// First boot — the file does not exist yet.
			return map[string]*APIToken{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p.where(), err)
	}
	// Tighten permissions in case the file was created loose by hand.
	// Best-effort, same as the initial-admin file.
	_ = os.Chmod(p.path, 0o600)
	return unmarshalAPITokens(data)
}

func (p *fileTokenPersister) save(_ context.Context, tokens map[string]*APIToken) error {
	payload, err := marshalAPITokens(tokens)
	if err != nil {
		return fmt.Errorf("marshal API tokens: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("creating directory for %s: %w", p.where(), err)
	}
	// Write-then-rename so a crash mid-write can never leave a torn file
	// behind — the old payload stays intact until the rename lands.
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", p.where(), err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", p.where(), err)
	}
	// Rename preserves the tmp file's 0600 mode; chmod is belt-and-braces
	// for filesystems that do not.
	_ = os.Chmod(p.path, 0o600)
	return nil
}

// apiTokensFilePath resolves the local-mode token file location:
// SHARKO_API_TOKENS_FILE when set, else ~/.sharko/api-tokens.json.
func apiTokensFilePath() (string, error) {
	if p := os.Getenv(EnvAPITokensFile); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for API tokens file (set %s to override): %w", EnvAPITokensFile, err)
	}
	return filepath.Join(home, ".sharko", "api-tokens.json"), nil
}

// InitTokenPersistence loads persisted API tokens and turns on write-through
// persistence for create/renew/revoke. Called once at startup from the serve
// path (cmd/sharko/serve.go), after the store is built.
//
// Mode selection follows the store's own mode: K8s mode persists to the
// dedicated sharko-api-tokens Secret, local mode to the 0600 file.
//
// If the persisted state cannot be READ, the persister is NOT installed and
// the error is returned — starting empty and then writing through would
// clobber the durable copy and silently lose every existing token, which is
// the exact bug this file exists to fix. The caller decides whether that is
// fatal (serve.go treats it as fatal).
//
// Tokens already in memory (none, on the normal startup path) are kept;
// persisted tokens fill in around them. A store that never calls this method
// stays pure in-memory (demo mode, unit tests).
func (s *Store) InitTokenPersistence(ctx context.Context) error {
	var p tokenPersister
	if s.mode == ModeK8s && s.clientset != nil {
		p = &secretTokenPersister{
			clientset: s.clientset,
			namespace: s.namespace,
			name:      APITokensSecretName,
		}
	} else {
		path, err := apiTokensFilePath()
		if err != nil {
			return err
		}
		p = &fileTokenPersister{path: path}
	}

	loaded, err := p.load(ctx)
	if err != nil {
		return fmt.Errorf("loading persisted API tokens: %w", err)
	}

	s.mu.Lock()
	for name, tok := range loaded {
		if _, exists := s.tokens[name]; !exists {
			s.tokens[name] = tok
		}
	}
	s.tokenPersister = p
	s.mu.Unlock()

	// SECURITY: log the backend and the count only — never hashes, never
	// token names in bulk (names are not secrets, but keep startup logs
	// lean; individual names appear on their own lifecycle events).
	slog.Info("API token persistence enabled — tokens now survive restarts",
		"backend", p.where(),
		"loaded", len(loaded))
	return nil
}

// persistTokensLocked writes the current token map through to durable
// storage. Caller MUST hold s.mu (write lock) — holding the lock across the
// write serializes saves so two concurrent mutations can never interleave
// their payloads, and it keeps the snapshot consistent with what the caller
// just changed.
//
// A nil persister (persistence never initialized) is a no-op: pure in-memory
// stores keep their existing behavior.
func (s *Store) persistTokensLocked(ctx context.Context) error {
	if s.tokenPersister == nil {
		return nil
	}
	if err := s.tokenPersister.save(ctx, s.tokens); err != nil {
		return fmt.Errorf("persisting API tokens to %s: %w", s.tokenPersister.where(), err)
	}
	return nil
}
