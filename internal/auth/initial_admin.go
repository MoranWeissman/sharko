package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newUsersConfigMap builds the <secretName>-users ConfigMap carrying the
// given accounts YAML, labeled the way every Sharko-created object is.
func newUsersConfigMap(namespace, name, accountsYAML string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
			},
		},
		Data: map[string]string{
			"accounts": accountsYAML,
		},
	}
}

// This file implements the v4 "no more running open" contract: when Sharko
// starts with zero users configured (and demo mode is not seeding its own),
// it creates an admin user with a random password instead of serving the
// API without authentication. The old zero-users fail-open in
// internal/api/router.go is gone — auth is enforced from the first request.
//
// Where the generated password lands, so the operator can read it:
//
//   - K8s mode: the `sharko-initial-admin-secret` Secret (the same
//     ArgoCD-style pattern MaybeLogBootstrapCredential already uses), plus
//     a one-time banner on stdout. The bcrypt hash goes into the regular
//     Sharko auth Secret and the users ConfigMap gets an enabled admin
//     entry, so the account is durable across restarts.
//   - Local mode (no cluster): a JSON file, default
//     ~/.sharko/initial-admin.json (override with SHARKO_INITIAL_ADMIN_FILE),
//     written 0600 with a 0700 parent directory. On the next start the
//     file is REUSED, so the password stays stable across restarts.
//
// SECURITY: the plaintext password is printed on the stdout banner and
// stored in the places named above — it NEVER goes through slog. That is
// the same contract MaybeLogBootstrapCredential enforces, and it is
// exercised by TestEnsureInitialAdmin_PasswordNotInStructuredLog.

// EnvInitialAdminFile overrides where local-mode (non-cluster) runs persist
// the generated initial admin credential. Default: ~/.sharko/initial-admin.json.
const EnvInitialAdminFile = "SHARKO_INITIAL_ADMIN_FILE"

// initialAdminUsername is the account name the self-generation path creates.
const initialAdminUsername = "admin"

// initialAdminPasswordLength is the generated password length. The security
// bar for this path is >= 16 characters from crypto/rand.
const initialAdminPasswordLength = 20

// initialAdminFile is the on-disk JSON shape for local-mode persistence.
type initialAdminFile struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// generateInitialAdminPassword returns a random password built from a
// 64-character alphabet. 64 divides 256 evenly, so mapping raw crypto/rand
// bytes with a modulo introduces no bias.
func generateInitialAdminPassword() (string, error) {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	b := make([]byte, initialAdminPasswordLength)
	if _, err := cryptoRand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b), nil
}

// EnsureInitialAdmin makes sure at least one user exists before the HTTP
// router starts serving. If users are already configured (chart-seeded
// accounts, SHARKO_BOOTSTRAP_ADMIN_PASSWORD, SHARKO_AUTH_USER, or demo
// seeding) it does nothing and existing accounts are never touched.
//
// Returns true when this call put an initial admin in place (freshly
// generated, or reused from the local-mode file).
//
// Demo mode must NOT call this — demo seeds its own users before the
// router is built, so HasUsers() is already true there and the serve path
// skips this call entirely (see cmd/sharko/serve.go).
func (s *Store) EnsureInitialAdmin(ctx context.Context) (bool, error) {
	if s.HasUsers() {
		return false, nil
	}

	if s.mode == ModeK8s {
		// The startup reload can fail transiently (API hiccup) or
		// because the ConfigMap/Secret simply do not exist yet (a bare
		// in-cluster run without the chart). Retry once so we never
		// generate over accounts that do exist but were not readable a
		// moment ago.
		if err := s.reload(); err == nil && s.HasUsers() {
			return false, nil
		}
		return s.ensureInitialAdminK8s(ctx)
	}
	return s.ensureInitialAdminLocal()
}

// ensureInitialAdminK8s creates the admin account durably through the same
// objects the rest of the store uses: bcrypt hash in the Sharko auth Secret
// (key admin.password) and an enabled admin entry in the <secretName>-users
// ConfigMap. Both objects are created when absent (the chart's RBAC already
// grants create). The plaintext is surfaced via the stdout banner and the
// sharko-initial-admin-secret (unless opted out via
// SHARKO_WRITE_INITIAL_ADMIN_SECRET=false).
func (s *Store) ensureInitialAdminK8s(ctx context.Context) (bool, error) {
	password, err := generateInitialAdminPassword()
	if err != nil {
		return false, fmt.Errorf("generating initial admin password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hashing initial admin password: %w", err)
	}

	if err := s.ensureAdminAccountInConfigMap(ctx); err != nil {
		return false, err
	}
	if err := s.ensureAdminPasswordInSecret(ctx, hash); err != nil {
		return false, err
	}

	// Seed in-memory state so the very first login works without waiting
	// for a reload.
	s.mu.Lock()
	s.users[initialAdminUsername] = &UserAccount{Username: initialAdminUsername, Enabled: true, Role: "admin"}
	s.passHash[initialAdminUsername] = string(hash)
	s.mu.Unlock()

	// Banner to stdout, NEVER through slog — same contract as
	// MaybeLogBootstrapCredential.
	fmt.Fprintln(os.Stdout, "=== INITIAL ADMIN CREDENTIAL (GENERATED) ===")
	fmt.Fprintln(os.Stdout, "username: "+initialAdminUsername)
	fmt.Fprintln(os.Stdout, "password:", password)
	fmt.Fprintln(os.Stdout, "No users were configured, so Sharko created this admin user. Store the password securely.")
	fmt.Fprintln(os.Stdout, "Retrieve later via: kubectl -n "+s.namespace+" get secret "+InitialAdminSecretName+" -o jsonpath='{.data.password}' | base64 -d")
	fmt.Fprintln(os.Stdout, "=== END INITIAL ADMIN CREDENTIAL ===")

	// Plain-English pointer for log scrapers: username only, never the
	// password.
	slog.Info("no users were configured, so Sharko created an admin user — the password was printed once above and is stored in the "+InitialAdminSecretName+" Secret",
		"username", initialAdminUsername,
		"namespace", s.namespace)

	if writeInitialAdminSecretEnabled() {
		if err := s.writeInitialAdminSecret(ctx, password); err != nil {
			// Non-fatal: the stdout banner already carried the credential.
			slog.Warn("could not write "+InitialAdminSecretName+"; the password was only printed on stdout above",
				"namespace", s.namespace,
				"error", err)
		}
	} else {
		slog.Info("skipping " + InitialAdminSecretName + " write per " + EnvWriteInitialAdminSecret + "=false")
	}

	return true, nil
}

// ensureAdminAccountInConfigMap creates or updates the <secretName>-users
// ConfigMap so it carries an enabled admin account. Only called when the
// store has zero users, so there are no existing accounts to preserve.
func (s *Store) ensureAdminAccountInConfigMap(ctx context.Context) error {
	cmName := s.secretName + "-users"
	cmClient := s.clientset.CoreV1().ConfigMaps(s.namespace)

	accounts := map[string]struct {
		Enabled bool   `yaml:"enabled"`
		Role    string `yaml:"role"`
	}{
		initialAdminUsername: {Enabled: true, Role: "admin"},
	}
	accountsYAML, err := yaml.Marshal(accounts)
	if err != nil {
		return fmt.Errorf("marshal admin account: %w", err)
	}

	cm, err := cmClient.Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		if !apierrorsIsNotFound(err) {
			return fmt.Errorf("read users ConfigMap %s/%s: %w", s.namespace, cmName, err)
		}
		created := newUsersConfigMap(s.namespace, cmName, string(accountsYAML))
		if _, err := cmClient.Create(ctx, created, metav1.CreateOptions{}); err != nil {
			if !apierrorsIsAlreadyExists(err) {
				return fmt.Errorf("create users ConfigMap %s/%s: %w", s.namespace, cmName, err)
			}
			// Race: someone else created it between Get and Create —
			// fall through to the update path below.
			cm, err = cmClient.Get(ctx, cmName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("re-read users ConfigMap %s/%s: %w", s.namespace, cmName, err)
			}
		} else {
			return nil
		}
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	// Merge instead of clobber: keep any accounts that appeared since the
	// zero-users check (defensive; normally empty on this path).
	existing := make(map[string]struct {
		Enabled bool   `yaml:"enabled"`
		Role    string `yaml:"role"`
	})
	if prior, ok := cm.Data["accounts"]; ok {
		_ = yaml.Unmarshal([]byte(prior), &existing)
	}
	if _, ok := existing[initialAdminUsername]; !ok {
		existing[initialAdminUsername] = struct {
			Enabled bool   `yaml:"enabled"`
			Role    string `yaml:"role"`
		}{Enabled: true, Role: "admin"}
	}
	merged, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal accounts: %w", err)
	}
	cm.Data["accounts"] = string(merged)
	if _, err := cmClient.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update users ConfigMap %s/%s: %w", s.namespace, cmName, err)
	}
	return nil
}

// ensureAdminPasswordInSecret writes the bcrypt hash into the Sharko auth
// Secret (key admin.password), creating the Secret when it does not exist.
func (s *Store) ensureAdminPasswordInSecret(ctx context.Context, hash []byte) error {
	secClient := s.clientset.CoreV1().Secrets(s.namespace)
	secret, err := secClient.Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if !apierrorsIsNotFound(err) {
			return fmt.Errorf("read auth Secret %s/%s: %w", s.namespace, s.secretName, err)
		}
		created := &corev1Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.secretName,
				Namespace: s.namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sharko",
				},
			},
			Type: corev1SecretTypeOpaque,
			Data: map[string][]byte{
				initialAdminUsername + ".password": hash,
			},
		}
		if _, err := secClient.Create(ctx, created, metav1.CreateOptions{}); err != nil {
			if !apierrorsIsAlreadyExists(err) {
				return fmt.Errorf("create auth Secret %s/%s: %w", s.namespace, s.secretName, err)
			}
			secret, err = secClient.Get(ctx, s.secretName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("re-read auth Secret %s/%s: %w", s.namespace, s.secretName, err)
			}
		} else {
			return nil
		}
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[initialAdminUsername+".password"] = hash
	if _, err := secClient.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update auth Secret %s/%s: %w", s.namespace, s.secretName, err)
	}
	return nil
}

// ensureInitialAdminLocal is the non-cluster path: persist the credential to
// a 0600 file so the password stays stable across restarts, and seed the
// in-memory store from it. SHARKO_AUTH_USER/SHARKO_AUTH_PASSWORD wins when
// set — NewStore already loaded those, so HasUsers() was true and we never
// got here.
func (s *Store) ensureInitialAdminLocal() (bool, error) {
	path, err := initialAdminFilePath()
	if err != nil {
		return false, err
	}

	// Reuse an existing file so restarts keep the same password.
	if data, readErr := os.ReadFile(path); readErr == nil {
		var f initialAdminFile
		if json.Unmarshal(data, &f) == nil && f.Username != "" && f.Password != "" {
			if err := s.seedLocalAdmin(f.Username, f.Password); err != nil {
				return false, err
			}
			// Tighten permissions in case the file was created loose by
			// hand. Best-effort.
			_ = os.Chmod(path, 0o600)
			slog.Info("no users were configured, so Sharko reused the initial admin credential file — read the password from that file to log in",
				"username", f.Username,
				"path", path)
			return true, nil
		}
		slog.Warn("initial admin credential file exists but could not be parsed — generating a fresh credential over it", "path", path)
	}

	password, err := generateInitialAdminPassword()
	if err != nil {
		return false, fmt.Errorf("generating initial admin password: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("creating directory for initial admin file: %w", err)
	}
	payload, err := json.MarshalIndent(initialAdminFile{
		Username: initialAdminUsername,
		Password: password,
	}, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal initial admin file: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return false, fmt.Errorf("writing initial admin file: %w", err)
	}

	if err := s.seedLocalAdmin(initialAdminUsername, password); err != nil {
		return false, err
	}

	// Banner to stdout, NEVER through slog.
	fmt.Fprintln(os.Stdout, "=== INITIAL ADMIN CREDENTIAL (GENERATED) ===")
	fmt.Fprintln(os.Stdout, "username: "+initialAdminUsername)
	fmt.Fprintln(os.Stdout, "password:", password)
	fmt.Fprintln(os.Stdout, "No users were configured, so Sharko created this admin user.")
	fmt.Fprintln(os.Stdout, "The credential is saved at "+path+" (file mode 0600) and will be reused on the next start.")
	fmt.Fprintln(os.Stdout, "=== END INITIAL ADMIN CREDENTIAL ===")

	slog.Info("no users were configured, so Sharko created an admin user — the password was printed once above and saved to a local file",
		"username", initialAdminUsername,
		"path", path)

	return true, nil
}

// seedLocalAdmin loads an admin account with the given plaintext password
// into the in-memory store, bcrypt-hashing it first so the plaintext never
// sits in process memory longer than needed.
func (s *Store) seedLocalAdmin(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing initial admin password: %w", err)
	}
	s.mu.Lock()
	s.users[username] = &UserAccount{Username: username, Enabled: true, Role: "admin"}
	s.passHash[username] = string(hash)
	s.mu.Unlock()
	return nil
}

// initialAdminFilePath resolves the local-mode credential file location:
// SHARKO_INITIAL_ADMIN_FILE when set, else ~/.sharko/initial-admin.json.
func initialAdminFilePath() (string, error) {
	if p := os.Getenv(EnvInitialAdminFile); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for initial admin file (set %s to override): %w", EnvInitialAdminFile, err)
	}
	return filepath.Join(home, ".sharko", "initial-admin.json"), nil
}
