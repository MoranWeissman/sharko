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

// EnvBootstrapAdminPassword is the environment variable that, when set,
// supplies the bootstrap admin password from an operator (Helm value or an
// existing Secret). When this variable is non-empty, Sharko adopts that
// password as the bcrypt-hashed `admin.password` and MUST NOT log it
// anywhere — operator-supplied secrets are never logged. This contract is
// enforced by MaybeLogBootstrapCredential / SeedBootstrapAdminFromEnv.
const EnvBootstrapAdminPassword = "SHARKO_BOOTSTRAP_ADMIN_PASSWORD"

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

	// Per-user GitHub PATs, encrypted at rest. See user_tokens.go.
	// In K8s mode this mirrors the `<username>.github_token` keys in the auth Secret;
	// in local mode it is in-memory only.
	userTokens map[string]string // username -> AES-256-GCM ciphertext (base64)

	// API tokens (in-memory)
	tokens map[string]*APIToken // name -> token
}

// NewStore creates an auth store with auto-detection of the backend mode.
// It tries K8s in-cluster config first, then falls back to env vars.
func NewStore() *Store {
	s := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}

	// Try K8s mode first
	config, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err == nil {
			s.mode = ModeK8s
			s.clientset = clientset
			s.namespace = detectNamespace()
			s.secretName = getEnvDefault("SHARKO_SECRET_NAME", "sharko")
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
	s.localUser = os.Getenv("SHARKO_AUTH_USER")
	s.localPass = os.Getenv("SHARKO_AUTH_PASSWORD")

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

	// Check bcrypt hash first.
	if strings.HasPrefix(hash, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	// Plaintext fallback only in local dev mode. In K8s mode, passwords must be bcrypt-hashed.
	if s.mode == ModeLocal {
		return hash == password
	}
	slog.Warn("password for user is not bcrypt-hashed — rejecting in K8s mode", "username", username)
	return false
}

// UpdatePassword changes a user's password. Verifies the current password first.
// In K8s mode, persists the new bcrypt hash to the Secret.
func (s *Store) UpdatePassword(username, currentPassword, newPassword string) error {
	if !s.ValidateCredentials(username, currentPassword) {
		return fmt.Errorf("current password is incorrect")
	}

	if len(newPassword) < 12 {
		return fmt.Errorf("new password must be at least 12 characters")
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
		os.Setenv("SHARKO_AUTH_PASSWORD", hashStr)
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
	s.hydrateTokensFromSecretData(secret.Data)
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
	if ns := os.Getenv("SHARKO_NAMESPACE"); ns != "" {
		return ns
	}

	return "sharko"
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// MaybeLogBootstrapCredential logs the auto-generated bootstrap admin
// credential exactly once on first boot, when all of the following hold:
//
//   - The store is running in K8s mode.
//   - The bootstrap admin password was NOT supplied by the operator
//     (the SHARKO_BOOTSTRAP_ADMIN_PASSWORD env var is empty).
//   - The Sharko Secret carries an `admin.initialPassword` key, which the
//     Helm chart writes only on first install when no operator-supplied
//     password is configured.
//
// The credential is logged in a clearly-marked block so operators can
// recover it from `kubectl logs -n sharko deployment/sharko | grep -A4 BOOTSTRAP`.
// After logging, the `admin.initialPassword` key is removed from the Secret
// so subsequent restarts do not re-emit the credential.
//
// SECURITY: this function MUST NOT log when the operator supplied a
// password (env var path). Operator-supplied passwords are never logged
// anywhere — see SeedBootstrapAdminFromEnv. This invariant is exercised
// by TestMaybeLogBootstrapCredential_OperatorSuppliedNotLogged.
func (s *Store) MaybeLogBootstrapCredential() {
	if s.mode != ModeK8s || s.clientset == nil {
		return
	}
	// CRITICAL: never log when operator supplied a password.
	if os.Getenv(EnvBootstrapAdminPassword) != "" {
		return
	}

	ctx := context.Background()
	secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		slog.Debug("bootstrap credential check skipped: cannot read secret", "error", err)
		return
	}

	pwBytes, ok := secret.Data["admin.initialPassword"]
	if !ok || len(pwBytes) == 0 {
		return
	}
	password := string(pwBytes)

	slog.Info("=== BOOTSTRAP ADMIN CREDENTIAL ===")
	slog.Info("bootstrap admin generated", "username", "admin", "password", password)
	slog.Info("This is the only time this credential will be shown. Store it securely.")
	slog.Info("=== END BOOTSTRAP ADMIN CREDENTIAL ===")

	// Best-effort cleanup so the credential is not logged on every restart.
	// A failure here is non-fatal — the next restart will simply re-emit.
	delete(secret.Data, "admin.initialPassword")
	if _, updateErr := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); updateErr != nil {
		slog.Warn("failed to remove admin.initialPassword from secret after bootstrap log", "error", updateErr)
	}
}

// SeedBootstrapAdminFromEnv consumes the SHARKO_BOOTSTRAP_ADMIN_PASSWORD
// env var and writes its bcrypt hash into the Sharko Secret as
// `admin.password`. This is the operator-supplied credential path, used
// when the Helm value `bootstrapAdmin.password` is set or when
// `bootstrapAdmin.existingSecret.name` is wired into the deployment as an
// env var via `valueFrom.secretKeyRef`.
//
// On every startup the env var is authoritative — Sharko overwrites
// admin.password with the bcrypt hash of the env value. Operators rotate
// the password by updating the source (Helm value or existing Secret) and
// restarting the pod.
//
// Also clears any stale `admin.initialPassword` key so that
// MaybeLogBootstrapCredential never emits a stale credential.
//
// SECURITY: the plaintext env value is NEVER logged. The function emits a
// single info log noting that an operator-supplied password was applied,
// without the value. This invariant is exercised by
// TestSeedBootstrapAdminFromEnv_DoesNotLogPassword.
func (s *Store) SeedBootstrapAdminFromEnv() error {
	password := os.Getenv(EnvBootstrapAdminPassword)
	if password == "" {
		return nil
	}
	if s.mode != ModeK8s || s.clientset == nil {
		// Local-mode operator-supplied passwords flow through SHARKO_AUTH_*.
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	ctx := context.Background()
	secret, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read sharko secret %s/%s: %w", s.namespace, s.secretName, err)
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data["admin.password"] = hash
	// Clear any stale initial-password marker so MaybeLogBootstrapCredential
	// does not log a value that has been superseded by the operator.
	delete(secret.Data, "admin.initialPassword")

	if _, err := s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update sharko secret with bootstrap password: %w", err)
	}

	// Refresh in-memory state so authentication works without waiting for
	// the next reload tick.
	s.mu.Lock()
	if _, ok := s.users["admin"]; !ok {
		s.users["admin"] = &UserAccount{Username: "admin", Enabled: true, Role: "admin"}
	}
	s.passHash["admin"] = string(hash)
	s.mu.Unlock()

	// SECURITY: do NOT log the password. Only log that an operator-supplied
	// credential was applied.
	slog.Info("operator-supplied bootstrap admin password applied (not logged)")
	return nil
}

// SetClientForTest installs a fake K8s client for tests. Production code
// must use NewStore(); this exists only so unit tests can exercise the
// bootstrap-credential flows without a real in-cluster config.
func (s *Store) SetClientForTest(clientset kubernetes.Interface, namespace, secretName string) {
	s.mode = ModeK8s
	s.clientset = clientset
	s.namespace = namespace
	s.secretName = secretName
}

// AddUser creates a user with a known plaintext password directly in the
// in-memory store. This is intended for demo and test mode only — it does
// NOT persist to K8s. If the user already exists it is a no-op.
func (s *Store) AddUser(username, password, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[username]; exists {
		return nil // already configured
	}

	s.users[username] = &UserAccount{Username: username, Enabled: true, Role: role}
	s.passHash[username] = password // plaintext — validated by ValidateCredentials local fallback
	return nil
}
