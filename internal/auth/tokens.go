package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const tokenPrefix = "sharko_"

// Token expiry policy (v4-wave2 8.2).
//
// Every token created from now on carries an expiry. Callers may ask for a
// different window via `expires_in_days`, bounded to keep long-lived
// credentials out of the system by accident.
const (
	// DefaultTokenExpiryDays is the expiry applied when the caller does not
	// ask for a specific window. Documented in
	// docs/site/api/endpoints.md and docs/site/operator/security.md.
	DefaultTokenExpiryDays = 90

	// MinTokenExpiryDays / MaxTokenExpiryDays bound an explicit
	// `expires_in_days` request.
	MinTokenExpiryDays = 1
	MaxTokenExpiryDays = 365
)

// tokenExpiringSoonWindow is how far ahead of the expiry date a token starts
// reporting expiring_soon so operators can renew before anything breaks.
const tokenExpiringSoonWindow = 14 * 24 * time.Hour

// Token status values reported by ListTokens / RenewToken.
const (
	// TokenStatusActive — has an expiry date that is still in the future.
	TokenStatusActive = "active"
	// TokenStatusExpired — has an expiry date that has passed. Refused at auth.
	TokenStatusExpired = "expired"
	// TokenStatusLegacyNoExpiry — stored before expiry existed, so it has no
	// expiry date. These keep working; we do NOT force-expire them. Operators
	// should recreate them so they pick up an expiry.
	TokenStatusLegacyNoExpiry = "legacy-no-expiry"
)

// ErrTokenNotFound is returned by AuthenticateToken when the presented value
// does not match any stored token. Callers MUST NOT tell the client anything
// more specific than "unauthorized" for this case — the caller does not hold
// a real credential, so any detail is information they did not have.
var ErrTokenNotFound = errors.New("token not recognized")

// TokenNotFoundError is returned by RenewToken / RevokeToken when no stored
// token carries the given name.
//
// This is deliberately NOT the same error as ErrTokenNotFound above.
// ErrTokenNotFound belongs to the AUTHENTICATION path, where the caller
// presented a secret and must be told nothing at all. This one belongs to the
// MANAGEMENT path, where an already-authorized caller named a token themselves
// — echoing that name back tells them nothing they did not already know.
//
// Handlers should match on this type (errors.As) rather than sniffing the
// message text for "not found".
type TokenNotFoundError struct {
	Name string
}

func (e *TokenNotFoundError) Error() string {
	return fmt.Sprintf("token %q not found", e.Name)
}

// TokenExpiredError is returned by AuthenticateToken when the presented value
// matched a stored token whose expiry has passed. The caller already holds
// this token's secret, so naming the token and its expiry date in the refusal
// leaks nothing and tells them exactly what to fix.
type TokenExpiredError struct {
	Name      string
	ExpiredAt time.Time
}

func (e *TokenExpiredError) Error() string {
	return fmt.Sprintf("API token %q expired on %s", e.Name, e.ExpiredAt.UTC().Format("2006-01-02"))
}

// APIToken represents an API key for automation.
//
// SECURITY: Hash is `json:"-"` and must stay that way — the bcrypt hash never
// leaves the process. The plaintext is never stored at all; it is returned
// once at creation time and then only exists on the caller's side.
type APIToken struct {
	Name      string     `json:"name"`
	Hash      string     `json:"-"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	LastUsed  time.Time  `json:"last_used_at,omitempty"`

	// CreatedBy is the username of whoever asked for this token. It is what
	// the own/other split on renew keys off (see TokenOwnedBy). Empty for
	// tokens minted outside a request — bootstrap, tests, or any token that
	// already existed before this field did — and an empty value is owned by
	// nobody, so only an admin can renew those.
	CreatedBy string `json:"created_by,omitempty"`

	// Derived fields, filled in on read. Never stored.
	Status       string `json:"status,omitempty"`
	Expired      bool   `json:"expired"`
	ExpiringSoon bool   `json:"expiring_soon"`
}

// isExpiredAt reports whether the token's expiry has passed at time now.
// A token with no expiry (legacy) never expires.
func (t *APIToken) isExpiredAt(now time.Time) bool {
	return t.ExpiresAt != nil && now.After(*t.ExpiresAt)
}

// view returns a copy of the token with the hash stripped and the derived
// status fields filled in. This is the ONLY shape that goes out over the API.
func (t *APIToken) view(now time.Time) APIToken {
	v := APIToken{
		Name:      t.Name,
		Role:      t.Role,
		CreatedAt: t.CreatedAt,
		LastUsed:  t.LastUsed,
		CreatedBy: t.CreatedBy,
	}
	if t.ExpiresAt != nil {
		exp := *t.ExpiresAt
		v.ExpiresAt = &exp
	}

	switch {
	case v.ExpiresAt == nil:
		v.Status = TokenStatusLegacyNoExpiry
	case now.After(*v.ExpiresAt):
		v.Status = TokenStatusExpired
		v.Expired = true
	default:
		v.Status = TokenStatusActive
		v.ExpiringSoon = now.Add(tokenExpiringSoonWindow).After(*v.ExpiresAt)
	}
	return v
}

// resolveExpiryDays turns a caller-supplied expires_in_days into the window to
// apply. Zero means "use the default". Anything outside the bounds is an error
// the caller can act on.
func resolveExpiryDays(days int) (int, error) {
	if days == 0 {
		return DefaultTokenExpiryDays, nil
	}
	if days < MinTokenExpiryDays || days > MaxTokenExpiryDays {
		return 0, fmt.Errorf(
			"expires_in_days must be between %d and %d — leave it out to get the default of %d days",
			MinTokenExpiryDays, MaxTokenExpiryDays, DefaultTokenExpiryDays)
	}
	return days, nil
}

// CreateToken generates a new API token with the given name and role.
// Returns the plaintext token ONCE — it cannot be retrieved again.
//
// expiresInDays sets how long the token stays usable. Pass 0 for the default
// of DefaultTokenExpiryDays; anything else must be within
// [MinTokenExpiryDays, MaxTokenExpiryDays].
func (s *Store) CreateToken(name, role string, expiresInDays int) (string, error) {
	return s.CreateTokenFor("", name, role, expiresInDays)
}

// CreateTokenFor is CreateToken with the creating user recorded on the token,
// so a later renew can tell "my token" from "somebody else's". Pass an empty
// owner when there is no user behind the call (bootstrap, tests) — such a
// token is owned by nobody and only an admin can renew it.
func (s *Store) CreateTokenFor(owner, name, role string, expiresInDays int) (string, error) {
	if name == "" {
		return "", fmt.Errorf("token name is required")
	}
	if role == "" {
		role = "viewer"
	}
	if role != "admin" && role != "operator" && role != "viewer" {
		return "", fmt.Errorf("role must be admin, operator, or viewer")
	}

	days, err := resolveExpiryDays(expiresInDays)
	if err != nil {
		return "", err
	}

	// Fast-path duplicate check (read lock).
	s.mu.RLock()
	if _, exists := s.tokens[name]; exists {
		s.mu.RUnlock()
		return "", fmt.Errorf("token %q already exists", name)
	}
	s.mu.RUnlock()

	// Generate plaintext: sharko_ + 32 hex chars = 39 chars total
	randBytes := make([]byte, 16) // 16 bytes = 32 hex chars
	if _, err := cryptoRand.Read(randBytes); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	plaintext := tokenPrefix + hex.EncodeToString(randBytes)

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing token: %w", err)
	}

	now := time.Now()
	expiresAt := now.AddDate(0, 0, days)
	token := &APIToken{
		Name:      name,
		Hash:      string(hash),
		Role:      role,
		CreatedAt: now,
		ExpiresAt: &expiresAt,
		CreatedBy: owner,
	}

	// Re-check under write lock to prevent TOCTU race (bcrypt is ~100ms).
	s.mu.Lock()
	if _, exists := s.tokens[name]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("token %q already exists", name)
	}
	s.tokens[name] = token
	// Write through to durable storage (no-op when persistence is off).
	// A token that cannot be persisted is rolled back and the caller gets
	// the error — a token that LOOKS created but silently dies on the next
	// restart is the exact bug persistence exists to fix.
	if err := s.persistTokensLocked(context.Background()); err != nil {
		delete(s.tokens, name)
		s.mu.Unlock()
		return "", fmt.Errorf("token %q was not created: %w", name, err)
	}
	s.mu.Unlock()

	// SECURITY: never log the plaintext or the hash — name/role/expiry only.
	slog.Info("API token created", "name", name, "role", role, "created_by", owner, "expires_at", expiresAt.UTC().Format(time.RFC3339))
	return plaintext, nil
}

// RenewToken pushes a token's expiry out by a fresh window, counted from now.
// It does NOT change the secret — every client using the token keeps working.
//
// expiresInDays follows the same rules as CreateToken: 0 means the default
// window, anything else must be within the allowed bounds.
func (s *Store) RenewToken(name string, expiresInDays int) (APIToken, error) {
	if name == "" {
		return APIToken{}, fmt.Errorf("token name is required")
	}

	days, err := resolveExpiryDays(expiresInDays)
	if err != nil {
		return APIToken{}, err
	}

	now := time.Now()
	expiresAt := now.AddDate(0, 0, days)

	s.mu.Lock()
	tok, exists := s.tokens[name]
	if !exists {
		s.mu.Unlock()
		return APIToken{}, &TokenNotFoundError{Name: name}
	}
	previousExpiry := tok.ExpiresAt
	tok.ExpiresAt = &expiresAt
	// Write through so the new expiry survives a restart. On failure the
	// old expiry is restored and the caller gets the error — the renewed
	// window must never exist only in memory.
	if err := s.persistTokensLocked(context.Background()); err != nil {
		tok.ExpiresAt = previousExpiry
		s.mu.Unlock()
		return APIToken{}, fmt.Errorf("token %q was not renewed: %w", name, err)
	}
	view := tok.view(now)
	s.mu.Unlock()

	slog.Info("API token renewed", "name", name, "expires_at", expiresAt.UTC().Format(time.RFC3339))
	return view, nil
}

// ListTokens returns all tokens without plaintext or hash, with the expiry
// date and a plain status ("active", "expired", or "legacy-no-expiry").
func (s *Store) ListTokens() []APIToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]APIToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		result = append(result, t.view(now))
	}
	return result
}

// GetToken returns one token's metadata — the same hash-free shape ListTokens
// emits — or false if no token by that name exists.
func (s *Store) GetToken(name string) (APIToken, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tok, ok := s.tokens[name]
	if !ok {
		return APIToken{}, false
	}
	return tok.view(time.Now()), true
}

// TokenOwnedBy reports whether the named token belongs to username.
//
// A caller owns a token when they created it (CreatedBy), or when they ARE it
// — an API key authenticates as its own name, so a token renewing itself is
// the same "my own credential" case. An unknown token, or a token created
// before anyone was recorded against it, is owned by nobody: the answer is
// false and the action falls to the admin-only branch.
func (s *Store) TokenOwnedBy(name, username string) bool {
	if name == "" || username == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tok, ok := s.tokens[name]
	if !ok {
		return false
	}
	return tok.CreatedBy == username || tok.Name == username
}

// RevokeToken deletes a token by name. A revoked token stops working
// immediately — there is no grace period.
func (s *Store) RevokeToken(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, exists := s.tokens[name]
	if !exists {
		return &TokenNotFoundError{Name: name}
	}
	delete(s.tokens, name)
	// Write through so the revocation survives a restart. On failure the
	// token is restored and the caller gets the error: "revoked" must mean
	// revoked everywhere, not revoked-until-the-next-restart. The caller
	// retries from a consistent state (the token still works).
	if err := s.persistTokensLocked(context.Background()); err != nil {
		s.tokens[name] = tok
		return fmt.Errorf("token %q was not revoked: %w", name, err)
	}

	slog.Info("API token revoked", "name", name)
	return nil
}

// AuthenticateToken checks a plaintext token against all stored hashes and
// enforces the expiry.
//
// On success it returns the token name (used as the username) and its role.
// On failure the error is either ErrTokenNotFound (nothing matched — the
// caller must be told nothing beyond "unauthorized") or *TokenExpiredError
// (the caller holds a real token that ran out, so the refusal can name it).
func (s *Store) AuthenticateToken(plaintext string) (username string, role string, err error) {
	if len(plaintext) != 39 || plaintext[:7] != tokenPrefix {
		return "", "", ErrTokenNotFound
	}

	s.mu.RLock()
	// Collect candidates under read lock
	type candidate struct {
		name      string
		hash      string
		role      string
		expiresAt *time.Time
	}
	candidates := make([]candidate, 0, len(s.tokens))
	for _, t := range s.tokens {
		c := candidate{name: t.Name, hash: t.Hash, role: t.Role}
		if t.ExpiresAt != nil {
			exp := *t.ExpiresAt
			c.expiresAt = &exp
		}
		candidates = append(candidates, c)
	}
	s.mu.RUnlock()

	now := time.Now()
	for _, c := range candidates {
		if bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(plaintext)) != nil {
			continue
		}
		// Matched. Enforce the expiry before handing out any access.
		if c.expiresAt != nil && now.After(*c.expiresAt) {
			return "", "", &TokenExpiredError{Name: c.name, ExpiredAt: *c.expiresAt}
		}
		// Update LastUsed. Deliberately NOT written through to durable
		// storage here — persisting on every authenticated request would
		// hammer the K8s API server. The value rides along on the next
		// create/renew/revoke write; across a restart it may be stale,
		// which only affects the informational last_used_at field.
		s.mu.Lock()
		if t, exists := s.tokens[c.name]; exists {
			t.LastUsed = now
		}
		s.mu.Unlock()
		return c.name, c.role, nil
	}

	return "", "", ErrTokenNotFound
}

// SetTokenExpiryForTest overwrites a stored token's expiry date. It exists so
// tests in other packages can put a token past its window (or strip the
// expiry, to stand in for a token stored before expiry dates existed) without
// waiting or reaching into unexported state. Returns false if no token by
// that name exists. Production code must use CreateToken / RenewToken.
func (s *Store) SetTokenExpiryForTest(name string, expiresAt *time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[name]
	if !ok {
		return false
	}
	tok.ExpiresAt = expiresAt
	return true
}

// ValidateToken is the boolean form of AuthenticateToken, kept for callers
// that only need a yes/no answer. Expired tokens return false.
func (s *Store) ValidateToken(plaintext string) (username string, role string, ok bool) {
	username, role, err := s.AuthenticateToken(plaintext)
	if err != nil {
		return "", "", false
	}
	return username, role, true
}
