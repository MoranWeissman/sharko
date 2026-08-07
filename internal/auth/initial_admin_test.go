package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newEmptyK8sStore returns a K8s-mode store backed by a fake clientset that
// has NO objects at all — the "bare in-cluster run without the chart" case.
func newEmptyK8sStore(t *testing.T) *Store {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	s := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	s.SetClientForTest(clientset, testBootstrapNS, testBootstrapSecret)
	return s
}

// newLocalStore returns a local-mode store with no users.
func newLocalStore(t *testing.T) *Store {
	t.Helper()
	return &Store{
		mode:       ModeLocal,
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
}

func TestEnsureInitialAdmin_K8sBareCluster_CreatesEverything(t *testing.T) {
	t.Setenv(EnvBootstrapAdminPassword, "")
	t.Setenv(EnvWriteInitialAdminSecret, "")

	s := newEmptyK8sStore(t)
	buf, restore := captureSlog(t)
	defer restore()

	created, err := s.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	if !created {
		t.Fatal("expected the initial admin to be created on a bare cluster")
	}

	ctx := context.Background()

	// The retrieval Secret carries the plaintext, ArgoCD-style.
	adminSecret, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(ctx, InitialAdminSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read %s: %v", InitialAdminSecretName, err)
	}
	password := string(adminSecret.Data["password"])
	if len(password) < 16 {
		t.Fatalf("generated password is %d chars, want >= 16", len(password))
	}
	if got := string(adminSecret.Data["username"]); got != "admin" {
		t.Fatalf("initial-admin-secret username = %q, want admin", got)
	}

	// The auth Secret carries a bcrypt hash, never the plaintext.
	authSecret, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(ctx, testBootstrapSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read auth secret: %v", err)
	}
	hash := string(authSecret.Data["admin.password"])
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("admin.password is not a bcrypt hash: %q", hash)
	}
	if hash == password {
		t.Fatal("auth secret must store the hash, not the plaintext")
	}

	// The users ConfigMap has an enabled admin account.
	cm, err := s.clientset.CoreV1().ConfigMaps(testBootstrapNS).Get(ctx, testBootstrapSecret+"-users", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read users ConfigMap: %v", err)
	}
	if !strings.Contains(cm.Data["accounts"], "admin") {
		t.Fatalf("users ConfigMap has no admin account: %q", cm.Data["accounts"])
	}

	// The generated credential actually logs in.
	if !s.ValidateCredentials("admin", password) {
		t.Fatal("generated admin credential does not validate")
	}
	if s.ValidateCredentials("admin", "wrong-password-123") {
		t.Fatal("a wrong password must not validate")
	}

	// SECURITY: the password must never enter the structured log stream.
	if strings.Contains(buf.String(), password) {
		t.Fatalf("generated password leaked into slog output:\n%s", buf.String())
	}
}

func TestEnsureInitialAdmin_K8s_ExistingUsers_NoOp(t *testing.T) {
	t.Setenv(EnvBootstrapAdminPassword, "")

	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: testBootstrapSecret + "-users", Namespace: testBootstrapNS},
			Data:       map[string]string{"accounts": "alice:\n  enabled: true\n  role: admin\n"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testBootstrapSecret, Namespace: testBootstrapNS},
			Data:       map[string][]byte{"alice.password": []byte("$2a$10$fakebcrypt")},
		},
	)
	s := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	s.SetClientForTest(clientset, testBootstrapNS, testBootstrapSecret)

	created, err := s.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	if created {
		t.Fatal("must not generate an admin when users already exist")
	}

	// No admin account was invented, no retrieval secret written.
	if _, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), InitialAdminSecretName, metav1.GetOptions{}); err == nil {
		t.Fatal("initial-admin-secret must not be written when users exist")
	}
	secret, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), testBootstrapSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read auth secret: %v", err)
	}
	if _, ok := secret.Data["admin.password"]; ok {
		t.Fatal("existing auth secret must not gain an admin.password key")
	}
	if got := string(secret.Data["alice.password"]); got != "$2a$10$fakebcrypt" {
		t.Fatalf("existing user's hash was modified: %q", got)
	}
}

func TestEnsureInitialAdmin_K8s_OptOut_SkipsRetrievalSecret(t *testing.T) {
	t.Setenv(EnvBootstrapAdminPassword, "")
	t.Setenv(EnvWriteInitialAdminSecret, "false")

	s := newEmptyK8sStore(t)
	created, err := s.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	if !created {
		t.Fatal("expected admin creation even with the retrieval-secret opt-out")
	}

	if _, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), InitialAdminSecretName, metav1.GetOptions{}); err == nil {
		t.Fatalf("%s must not be written when %s=false", InitialAdminSecretName, EnvWriteInitialAdminSecret)
	}
	// The durable admin account still exists.
	if _, err := s.clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), testBootstrapSecret, metav1.GetOptions{}); err != nil {
		t.Fatalf("auth secret missing: %v", err)
	}
}

func TestEnsureInitialAdmin_Local_CreatesFileWithTightPerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "creds")
	path := filepath.Join(dir, "initial-admin.json")
	t.Setenv(EnvInitialAdminFile, path)

	s := newLocalStore(t)
	buf, restore := captureSlog(t)
	defer restore()

	created, err := s.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	if !created {
		t.Fatal("expected initial admin creation in local mode")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credential file mode = %o, want 0600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat credential dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("credential dir mode = %o, want 0700", perm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var f initialAdminFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse credential file: %v", err)
	}
	if f.Username != "admin" {
		t.Fatalf("username = %q, want admin", f.Username)
	}
	if len(f.Password) < 16 {
		t.Fatalf("generated password is %d chars, want >= 16", len(f.Password))
	}

	if !s.ValidateCredentials("admin", f.Password) {
		t.Fatal("generated admin credential does not validate")
	}

	// SECURITY: the password must never enter the structured log stream.
	if strings.Contains(buf.String(), f.Password) {
		t.Fatalf("generated password leaked into slog output:\n%s", buf.String())
	}
}

func TestEnsureInitialAdmin_Local_ReusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initial-admin.json")
	t.Setenv(EnvInitialAdminFile, path)

	first := newLocalStore(t)
	if _, err := first.EnsureInitialAdmin(context.Background()); err != nil {
		t.Fatalf("first EnsureInitialAdmin: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var f initialAdminFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse credential file: %v", err)
	}

	// Second boot (fresh store) must REUSE the stored password, not
	// regenerate it — restarts keep the credential stable.
	buf, restore := captureSlog(t)
	defer restore()
	second := newLocalStore(t)
	created, err := second.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("second EnsureInitialAdmin: %v", err)
	}
	if !created {
		t.Fatal("expected the second boot to seed the admin from the file")
	}
	if !second.ValidateCredentials("admin", f.Password) {
		t.Fatal("second boot does not accept the password from the first boot")
	}
	rawAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read credential file: %v", err)
	}
	if string(rawAfter) != string(raw) {
		t.Fatal("credential file changed across restarts — the password must stay stable")
	}
	// SECURITY: reuse must not log the password either.
	if strings.Contains(buf.String(), f.Password) {
		t.Fatalf("reused password leaked into slog output:\n%s", buf.String())
	}
}

func TestEnsureInitialAdmin_Local_ExistingUsers_NoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initial-admin.json")
	t.Setenv(EnvInitialAdminFile, path)

	s := newLocalStore(t)
	if err := s.AddUser("someone", "a-password-that-is-long", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	created, err := s.EnsureInitialAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureInitialAdmin: %v", err)
	}
	if created {
		t.Fatal("must not generate an admin when a user already exists")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("no credential file may be written when users exist")
	}
}

func TestGenerateInitialAdminPassword_LengthAndRandomness(t *testing.T) {
	a, err := generateInitialAdminPassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := generateInitialAdminPassword()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(a) < 16 || len(b) < 16 {
		t.Fatalf("passwords too short: %d and %d chars, want >= 16", len(a), len(b))
	}
	if a == b {
		t.Fatal("two generated passwords are identical — generator is not random")
	}
}
