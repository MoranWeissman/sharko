package auth

import (
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const tokenPrefix = "sharko_"

// APIToken represents an API key for automation.
type APIToken struct {
	Name      string    `json:"name"`
	Hash      string    `json:"-"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used_at,omitempty"`
}

// CreateToken generates a new API token with the given name and role.
// Returns the plaintext token ONCE — it cannot be retrieved again.
func (s *Store) CreateToken(name, role string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("token name is required")
	}
	if role == "" {
		role = "viewer"
	}
	if role != "admin" && role != "operator" && role != "viewer" {
		return "", fmt.Errorf("role must be admin, operator, or viewer")
	}

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

	token := &APIToken{
		Name:      name,
		Hash:      string(hash),
		Role:      role,
		CreatedAt: time.Now(),
	}

	s.mu.Lock()
	s.tokens[name] = token
	s.mu.Unlock()

	slog.Info("API token created", "name", name, "role", role)
	return plaintext, nil
}

// ListTokens returns all tokens without plaintext or hash.
func (s *Store) ListTokens() []APIToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]APIToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		result = append(result, APIToken{
			Name:      t.Name,
			Role:      t.Role,
			CreatedAt: t.CreatedAt,
			LastUsed:  t.LastUsed,
		})
	}
	return result
}

// RevokeToken deletes a token by name.
func (s *Store) RevokeToken(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tokens[name]; !exists {
		return fmt.Errorf("token %q not found", name)
	}
	delete(s.tokens, name)

	slog.Info("API token revoked", "name", name)
	return nil
}

// ValidateToken checks a plaintext token against all stored hashes.
// Returns the token name (as username), role, and whether validation succeeded.
func (s *Store) ValidateToken(plaintext string) (username string, role string, ok bool) {
	if len(plaintext) != 39 || plaintext[:7] != tokenPrefix {
		return "", "", false
	}

	s.mu.RLock()
	// Collect candidates under read lock
	type candidate struct {
		name string
		hash string
		role string
	}
	candidates := make([]candidate, 0, len(s.tokens))
	for _, t := range s.tokens {
		candidates = append(candidates, candidate{name: t.Name, hash: t.Hash, role: t.Role})
	}
	s.mu.RUnlock()

	for _, c := range candidates {
		if bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(plaintext)) == nil {
			// Update LastUsed
			s.mu.Lock()
			if t, exists := s.tokens[c.name]; exists {
				t.LastUsed = time.Now()
			}
			s.mu.Unlock()
			return c.name, c.role, true
		}
	}

	return "", "", false
}
