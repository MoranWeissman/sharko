package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Mode represents the auth backend mode.
type Mode string

const (
	ModeK8s   Mode = "k8s"
	ModeLocal Mode = "local"
)

// UserAccount represents a user account from the ConfigMap.
type UserAccount struct {
	Username string `json:"username"`
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Role     string `json:"role" yaml:"role"`
}

// Store manages user authentication backed by K8s resources or env vars.
type Store struct {
	mode       Mode
	namespace  string
	secretName string
	clientset  kubernetes.Interface

	// Local mode fallback
	localUser string
	localPass string

	mu sync.RWMutex
	// Cached data from K8s
	users    map[string]*UserAccount
	passHash map[string]string // username -> bcrypt hash
}

// NewStore creates an auth store with auto-detection of the backend mode.
// It tries K8s in-cluster config first, then falls back to env vars.
func NewStore() *Store {
	s := &Store{
		users:    make(map[string]*UserAccount),
		passHash: make(map[string]string),
	}

	// Try K8s mode first
	config, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err == nil {
			s.mode = ModeK8s
			s.clientset = clientset
			s.namespace = detectNamespace()
			s.secretName = getEnvDefault("AAP_SECRET_NAME", "aap")
			slog.Info("auth store initialized in K8s mode", "namespace", s.namespace, "secret", s.secretName)
			// Load initial data
			if err := s.reload(); err != nil {
				slog.Warn("failed to load auth data from K8s, will retry on requests", "error", err)
			}
			return s
		}
	}

	// Fall back to local mode (env vars)
	s.mode = ModeLocal
	s.localUser = os.Getenv("AAP_AUTH_USER")
	s.localPass = os.Getenv("AAP_AUTH_PASSWORD")

	if s.localUser != "" {
		s.users[s.localUser] = &UserAccount{
			Username: s.localUser,
			Enabled:  true,
			Role:     "admin",
		}
		s.passHash[s.localUser] = s.localPass
	}

	if s.localUser != "" {
		slog.Info("auth store initialized in local mode", "user", s.localUser)
	} else {
		slog.Info("auth store initialized in local mode (no credentials configured, auth disabled)")
	}

	return s
}

// Mode returns the current auth backend mode.
func (s *Store) Mode() Mode {
	return s.mode
}

// HasUsers returns true if any user accounts are configured.
func (s *Store) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) > 0
}

// ValidateCredentials checks if the given username/password combination is valid.
func (s *Store) ValidateCredentials(username, password string) bool {
	// Reload from K8s on each validation to pick up changes
	if s.mode == ModeK8s {
		if err := s.reload(); err != nil {
			slog.Error("failed to reload auth data", "error", err)
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[username]
	if !ok || !user.Enabled {
		return false
	}

	hash, ok := s.passHash[username]
	if !ok || hash == "" {
		return false
	}

	// Check bcrypt hash or plaintext (for local dev)
	if strings.HasPrefix(hash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	return hash == password
}

// UpdatePassword changes a user's password. Verifies the current password first.
// In K8s mode, persists the new bcrypt hash to the Secret.
func (s *Store) UpdatePassword(username, currentPassword, newPassword string) error {
	if !s.ValidateCredentials(username, currentPassword) {
		return fmt.Errorf("current password is incorrect")
	}

	if len(newPassword) < 8 {
		return fmt.Errorf("new password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	hashStr := string(hash)

	if s.mode == ModeK8s {
		// Update the K8s Secret
		ctx := context.Background()
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to read secret: %w", err)
		}

		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[username+".password"] = []byte(hashStr)
		// Remove initial password key if it exists (already changed)
		delete(secret.Data, username+".initialPassword")

		_, err = s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
	} else {
		// Local mode: update env var and in-memory
		os.Setenv("AAP_AUTH_PASSWORD", hashStr)
		s.localPass = hashStr
	}

	s.mu.Lock()
	s.passHash[username] = hashStr
	s.mu.Unlock()

	slog.Info("password updated", "username", username)
	return nil
}

// GetUser returns a user account by username, or nil if not found.
func (s *Store) GetUser(username string) *UserAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok {
		return nil
	}
	// Return a copy
	copy := *u
	return &copy
}

// ListUsers returns all configured user accounts.
func (s *Store) ListUsers() []UserAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]UserAccount, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, *u)
	}
	return result
}

// CreateUser adds a new user account with a temporary password.
// Returns the generated password so the admin can share it.
func (s *Store) CreateUser(username, role string) (string, error) {
	if username == "" {
		return "", fmt.Errorf("username is required")
	}
	if role == "" {
		role = "viewer"
	}
	if role != "admin" && role != "operator" && role != "viewer" {
		return "", fmt.Errorf("role must be admin, operator, or viewer")
	}

	// Check if user already exists
	s.mu.RLock()
	if _, exists := s.users[username]; exists {
		s.mu.RUnlock()
		return "", fmt.Errorf("user %q already exists", username)
	}
	s.mu.RUnlock()

	// Generate a temporary password
	tempPass := generateTempPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPass), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	if s.mode == ModeK8s {
		ctx := context.Background()

		// Update ConfigMap with new user
		cmName := s.secretName + "-users"
		cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("reading ConfigMap: %w", err)
		}

		accounts := make(map[string]struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		})
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		if existing, ok := cm.Data["accounts"]; ok {
			yaml.Unmarshal([]byte(existing), &accounts)
		}
		accounts[username] = struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		}{Enabled: true, Role: role}

		data, _ := yaml.Marshal(accounts)
		cm.Data["accounts"] = string(data)
		if _, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("updating ConfigMap: %w", err)
		}

		// Update Secret with password hash
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("reading Secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[username+".password"] = hash
		if _, err := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("updating Secret: %w", err)
		}
	}

	// Update in-memory
	s.mu.Lock()
	s.users[username] = &UserAccount{Username: username, Enabled: true, Role: role}
	s.passHash[username] = string(hash)
	s.mu.Unlock()

	slog.Info("user created", "username", username, "role", role)
	return tempPass, nil
}

// UpdateUser updates a user's role and enabled status.
func (s *Store) UpdateUser(username string, enabled bool, role string) error {
	s.mu.RLock()
	user, exists := s.users[username]
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("user %q not found", username)
	}

	if role != "" && role != "admin" && role != "operator" && role != "viewer" {
		return fmt.Errorf("role must be admin, operator, or viewer")
	}
	if role == "" {
		role = user.Role
	}

	if s.mode == ModeK8s {
		ctx := context.Background()
		cmName := s.secretName + "-users"
		cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading ConfigMap: %w", err)
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		accounts := make(map[string]struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		})
		if existing, ok := cm.Data["accounts"]; ok {
			yaml.Unmarshal([]byte(existing), &accounts)
		}
		accounts[username] = struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		}{Enabled: enabled, Role: role}

		data, _ := yaml.Marshal(accounts)
		cm.Data["accounts"] = string(data)
		if _, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating ConfigMap: %w", err)
		}
	}

	s.mu.Lock()
	s.users[username] = &UserAccount{Username: username, Enabled: enabled, Role: role}
	s.mu.Unlock()

	slog.Info("user updated", "username", username, "role", role, "enabled", enabled)
	return nil
}

// DeleteUser removes a user account.
func (s *Store) DeleteUser(username string) error {
	s.mu.RLock()
	_, exists := s.users[username]
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("user %q not found", username)
	}

	if s.mode == ModeK8s {
		ctx := context.Background()

		// Remove from ConfigMap
		cmName := s.secretName + "-users"
		cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err == nil {
			if cm.Data == nil {
				cm.Data = make(map[string]string)
			}
			accounts := make(map[string]interface{})
			if existing, ok := cm.Data["accounts"]; ok {
				yaml.Unmarshal([]byte(existing), &accounts)
			}
			delete(accounts, username)
			data, _ := yaml.Marshal(accounts)
			cm.Data["accounts"] = string(data)
			s.clientset.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
		}

		// Remove from Secret
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err == nil {
			delete(secret.Data, username+".password")
			delete(secret.Data, username+".initialPassword")
			s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		}
	}

	s.mu.Lock()
	delete(s.users, username)
	delete(s.passHash, username)
	s.mu.Unlock()

	slog.Info("user deleted", "username", username)
	return nil
}

// ResetPassword generates a new temporary password for a user.
func (s *Store) ResetPassword(username string) (string, error) {
	s.mu.RLock()
	_, exists := s.users[username]
	s.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("user %q not found", username)
	}

	tempPass := generateTempPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPass), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	if s.mode == ModeK8s {
		ctx := context.Background()
		secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("reading Secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[username+".password"] = hash
		if _, err := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("updating Secret: %w", err)
		}
	}

	s.mu.Lock()
	s.passHash[username] = string(hash)
	s.mu.Unlock()

	slog.Info("password reset", "username", username)
	return tempPass, nil
}

func generateTempPassword() string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	randBytes := make([]byte, 12)
	cryptoRand.Read(randBytes)
	for i := range b {
		b[i] = chars[int(randBytes[i])%len(chars)]
	}
	return string(b)
}

// reload reads user accounts from ConfigMap and password hashes from Secret.
func (s *Store) reload() error {
	if s.mode != ModeK8s {
		return nil
	}

	ctx := context.Background()
	users := make(map[string]*UserAccount)
	passHash := make(map[string]string)

	// Read ConfigMap for user accounts
	cmName := s.secretName + "-users"
	cm, err := s.clientset.CoreV1().ConfigMaps(s.namespace).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read ConfigMap %s: %w", cmName, err)
	}

	accountsYAML, ok := cm.Data["accounts"]
	if ok {
		var accounts map[string]struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		}
		if err := yaml.Unmarshal([]byte(accountsYAML), &accounts); err != nil {
			return fmt.Errorf("failed to parse accounts YAML: %w", err)
		}
		for name, acct := range accounts {
			users[name] = &UserAccount{
				Username: name,
				Enabled:  acct.Enabled,
				Role:     acct.Role,
			}
		}
	}

	// Read Secret for password hashes
	secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read Secret %s: %w", s.secretName, err)
	}

	for key, val := range secret.Data {
		if strings.HasSuffix(key, ".password") {
			username := strings.TrimSuffix(key, ".password")
			passHash[username] = string(val)
		}
	}

	s.mu.Lock()
	s.users = users
	s.passHash = passHash
	s.mu.Unlock()

	return nil
}

// detectNamespace returns the Kubernetes namespace the pod is running in.
func detectNamespace() string {
	// Try service account namespace file first
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}

	// Fall back to env var
	if ns := os.Getenv("AAP_NAMESPACE"); ns != "" {
		return ns
	}

	return "argocd-addons-platform"
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
