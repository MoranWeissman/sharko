package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// token_persistence_test.go — v4-coherence lane J. API tokens must survive
// restarts in both modes, the raw token value must never touch persisted
// bytes or the structured log, and a failed persistence write must surface
// an error instead of leaving a token that silently dies on restart.

// newK8sPersistentStore builds a K8s-mode store on the given fake clientset
// with token persistence initialized. Calling it twice with the same
// clientset simulates a pod restart: a brand-new Store loading whatever the
// previous one persisted.
func newK8sPersistentStore(t *testing.T, clientset kubernetes.Interface) *Store {
	t.Helper()
	s := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	s.SetClientForTest(clientset, testBootstrapNS, testBootstrapSecret)
	if err := s.InitTokenPersistence(context.Background()); err != nil {
		t.Fatalf("InitTokenPersistence: %v", err)
	}
	return s
}

// newLocalPersistentStore builds a local-mode store persisting to the given
// file path. Same restart semantics: same path, new Store.
func newLocalPersistentStore(t *testing.T, path string) *Store {
	t.Helper()
	t.Setenv(EnvAPITokensFile, path)
	s := newLocalStore(t)
	if err := s.InitTokenPersistence(context.Background()); err != nil {
		t.Fatalf("InitTokenPersistence: %v", err)
	}
	return s
}

func TestTokenPersistence_K8s_CreateSurvivesRestart(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	s1 := newK8sPersistentStore(t, clientset)
	plaintext, err := s1.CreateTokenFor("alice", "ci-deploy", "operator", 30)
	if err != nil {
		t.Fatalf("CreateTokenFor: %v", err)
	}

	// Restart: a brand-new store loads from the same cluster.
	s2 := newK8sPersistentStore(t, clientset)

	username, role, err := s2.AuthenticateToken(plaintext)
	if err != nil {
		t.Fatalf("token must still authenticate after a restart, got: %v", err)
	}
	if username != "ci-deploy" || role != "operator" {
		t.Errorf("got user=%q role=%q, want ci-deploy/operator", username, role)
	}

	// Metadata round-trips.
	view, ok := s2.GetToken("ci-deploy")
	if !ok {
		t.Fatal("GetToken: token missing after restart")
	}
	if view.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want alice", view.CreatedBy)
	}
	if view.ExpiresAt == nil {
		t.Fatal("expiry must survive the restart")
	}
	want := time.Now().AddDate(0, 0, 30)
	if diff := view.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiry = %s, want about %s", view.ExpiresAt, want)
	}
	if view.CreatedAt.IsZero() {
		t.Error("created_at must survive the restart")
	}
	if view.Status != TokenStatusActive {
		t.Errorf("status = %q, want %q", view.Status, TokenStatusActive)
	}
}

func TestTokenPersistence_K8s_RevokeSurvivesRestart(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	s1 := newK8sPersistentStore(t, clientset)
	plaintext, err := s1.CreateToken("gone-for-good", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := s1.RevokeToken("gone-for-good"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	s2 := newK8sPersistentStore(t, clientset)
	if _, _, err := s2.AuthenticateToken(plaintext); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("a revoked token must stay revoked across restarts, got: %v", err)
	}
	if _, ok := s2.GetToken("gone-for-good"); ok {
		t.Error("a revoked token must not reappear after a restart")
	}
}

func TestTokenPersistence_K8s_RenewPersistsNewExpiry(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	s1 := newK8sPersistentStore(t, clientset)
	plaintext, err := s1.CreateToken("renew-me", "operator", 2)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, err := s1.RenewToken("renew-me", 60); err != nil {
		t.Fatalf("RenewToken: %v", err)
	}

	s2 := newK8sPersistentStore(t, clientset)
	view, ok := s2.GetToken("renew-me")
	if !ok {
		t.Fatal("token missing after restart")
	}
	if view.ExpiresAt == nil {
		t.Fatal("renewed expiry must survive the restart")
	}
	want := time.Now().AddDate(0, 0, 60)
	if diff := view.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiry = %s, want about %s (the renewed window)", view.ExpiresAt, want)
	}
	// The same secret still works — renew never changes the value.
	if _, _, err := s2.AuthenticateToken(plaintext); err != nil {
		t.Errorf("renewed token must authenticate after restart: %v", err)
	}
}

func TestTokenPersistence_K8s_LegacyNoExpiryRoundTrips(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	s1 := newK8sPersistentStore(t, clientset)
	plaintext, err := s1.CreateToken("legacy", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Strip the expiry, standing in for a token stored before expiry
	// existed, then trigger a write-through so the nil expiry persists
	// (the test hook itself deliberately does not persist).
	if !s1.SetTokenExpiryForTest("legacy", nil) {
		t.Fatal("SetTokenExpiryForTest: token missing")
	}
	if _, err := s1.CreateToken("rider", "viewer", 0); err != nil {
		t.Fatalf("CreateToken rider: %v", err)
	}

	s2 := newK8sPersistentStore(t, clientset)
	view, ok := s2.GetToken("legacy")
	if !ok {
		t.Fatal("legacy token missing after restart")
	}
	if view.ExpiresAt != nil {
		t.Error("a legacy token's missing expiry must stay missing, not gain one")
	}
	if view.Status != TokenStatusLegacyNoExpiry {
		t.Errorf("status = %q, want %q", view.Status, TokenStatusLegacyNoExpiry)
	}
	if _, _, err := s2.AuthenticateToken(plaintext); err != nil {
		t.Errorf("legacy token must keep working after restart: %v", err)
	}
}

func TestTokenPersistence_K8s_RawValueNeverPersistedOrLogged(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	buf, restore := captureSlog(t)
	defer restore()

	s := newK8sPersistentStore(t, clientset)
	plaintext, err := s.CreateToken("secret-keeper", "admin", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, err := s.RenewToken("secret-keeper", 30); err != nil {
		t.Fatalf("RenewToken: %v", err)
	}

	secret, err := clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), APITokensSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read %s: %v", APITokensSecretName, err)
	}

	// The ownership label is on the Secret Sharko created.
	if got := secret.Labels["app.kubernetes.io/managed-by"]; got != "sharko" {
		t.Errorf("managed-by label = %q, want sharko", got)
	}

	// SECURITY: the raw token value appears in NO persisted bytes.
	for key, val := range secret.Data {
		if strings.Contains(string(val), plaintext) {
			t.Fatalf("raw token value leaked into Secret data key %q", key)
		}
	}
	// What IS stored is the bcrypt hash.
	if !strings.Contains(string(secret.Data[apiTokensSecretKey]), "$2") {
		t.Error("persisted payload should carry a bcrypt hash")
	}

	// SECURITY: the raw token value appears in NO structured log output.
	if strings.Contains(buf.String(), plaintext) {
		t.Fatalf("raw token value leaked into slog output:\n%s", buf.String())
	}
}

func TestTokenPersistence_K8s_FirstBootAbsentSecret(t *testing.T) {
	clientset := fake.NewSimpleClientset() // no Secret at all

	s := newK8sPersistentStore(t, clientset) // must not error on absence
	if got := len(s.ListTokens()); got != 0 {
		t.Fatalf("first boot should start with 0 tokens, got %d", got)
	}

	// The first create brings the Secret into existence.
	if _, err := s.CreateToken("first", "viewer", 0); err != nil {
		t.Fatalf("CreateToken on first boot: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets(testBootstrapNS).Get(context.Background(), APITokensSecretName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the api-tokens Secret should exist after the first create: %v", err)
	}
}

func TestTokenPersistence_K8s_WriteFailureSurfacesError(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	s := newK8sPersistentStore(t, clientset)
	keeperPlaintext, err := s.CreateToken("keeper", "operator", 2)
	if err != nil {
		t.Fatalf("CreateToken keeper: %v", err)
	}
	keeperBefore, _ := s.GetToken("keeper")

	// From here on, every Secret write fails.
	boom := errors.New("api server unavailable")
	clientset.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, boom
	})
	clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, boom
	})

	// Create: error surfaces, and the token is NOT left half-created in
	// memory — that would be the silent-vanish-on-restart bug reborn.
	if _, err := s.CreateToken("doomed", "viewer", 0); err == nil {
		t.Fatal("CreateToken must fail when persistence fails")
	}
	if _, ok := s.GetToken("doomed"); ok {
		t.Error("a token whose persistence failed must not stay in memory")
	}

	// Renew: error surfaces, expiry unchanged.
	if _, err := s.RenewToken("keeper", 60); err == nil {
		t.Fatal("RenewToken must fail when persistence fails")
	}
	keeperAfter, ok := s.GetToken("keeper")
	if !ok {
		t.Fatal("keeper vanished")
	}
	if keeperBefore.ExpiresAt == nil || keeperAfter.ExpiresAt == nil {
		t.Fatal("keeper must have an expiry on both reads")
	}
	if !keeperAfter.ExpiresAt.Equal(*keeperBefore.ExpiresAt) {
		t.Error("a failed renew must leave the expiry unchanged")
	}

	// Revoke: error surfaces, token still works — the caller retries from
	// a consistent state instead of holding a revoked-in-one-place token.
	if err := s.RevokeToken("keeper"); err == nil {
		t.Fatal("RevokeToken must fail when persistence fails")
	}
	if _, _, err := s.AuthenticateToken(keeperPlaintext); err != nil {
		t.Errorf("after a failed revoke the token must still authenticate: %v", err)
	}
}

func TestTokenPersistence_Local_CreateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens", "api-tokens.json")
	buf, restore := captureSlog(t)
	defer restore()

	s1 := newLocalPersistentStore(t, path)
	plaintext, err := s1.CreateToken("laptop-ci", "operator", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// File exists with tight permissions: 0600 file in a 0700 directory.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token file mode = %o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat token dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("token dir mode = %o, want 0700", got)
	}

	// SECURITY: the raw value is in neither the file nor the log.
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if strings.Contains(string(fileBytes), plaintext) {
		t.Fatal("raw token value leaked into the persisted file")
	}
	if !strings.Contains(string(fileBytes), "$2") {
		t.Error("persisted file should carry a bcrypt hash")
	}
	if strings.Contains(buf.String(), plaintext) {
		t.Fatalf("raw token value leaked into slog output:\n%s", buf.String())
	}

	// Restart: same path, new store.
	s2 := newLocalPersistentStore(t, path)
	username, role, err := s2.AuthenticateToken(plaintext)
	if err != nil {
		t.Fatalf("token must still authenticate after a restart, got: %v", err)
	}
	if username != "laptop-ci" || role != "operator" {
		t.Errorf("got user=%q role=%q, want laptop-ci/operator", username, role)
	}
}

func TestTokenPersistence_Local_RevokeAndRenewSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-tokens.json")

	s1 := newLocalPersistentStore(t, path)
	revokedPlaintext, err := s1.CreateToken("revoked-one", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, err := s1.CreateToken("renewed-one", "viewer", 2); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := s1.RevokeToken("revoked-one"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, err := s1.RenewToken("renewed-one", 90); err != nil {
		t.Fatalf("RenewToken: %v", err)
	}

	s2 := newLocalPersistentStore(t, path)
	if _, _, err := s2.AuthenticateToken(revokedPlaintext); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("revoked token must stay revoked across restarts, got: %v", err)
	}
	view, ok := s2.GetToken("renewed-one")
	if !ok {
		t.Fatal("renewed token missing after restart")
	}
	want := time.Now().AddDate(0, 0, 90)
	if view.ExpiresAt == nil {
		t.Fatal("renewed expiry must survive the restart")
	}
	if diff := view.ExpiresAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiry = %s, want about %s", view.ExpiresAt, want)
	}
}

func TestTokenPersistence_Local_FirstBootAbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist-yet", "api-tokens.json")
	s := newLocalPersistentStore(t, path) // must not error on absence
	if got := len(s.ListTokens()); got != 0 {
		t.Fatalf("first boot should start with 0 tokens, got %d", got)
	}
}

func TestTokenPersistence_Local_WriteFailureSurfacesError(t *testing.T) {
	dir := t.TempDir()
	// The token "file" path is a directory, so the final rename must fail.
	path := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Setenv(EnvAPITokensFile, path)
	s := newLocalStore(t)
	// Load fails (the path is a directory) — the persister must NOT be
	// installed, and the error must surface.
	if err := s.InitTokenPersistence(context.Background()); err == nil {
		t.Fatal("InitTokenPersistence must fail when the token file cannot be read")
	}

	// With no persister installed the store still works in-memory —
	// existing behavior, nothing silently half-persisted.
	if _, err := s.CreateToken("mem-only", "viewer", 0); err != nil {
		t.Fatalf("in-memory create after failed init: %v", err)
	}
}

// TestTokenPersistence_NoPersisterKeepsInMemoryContract pins the compatibility
// promise: a store that never initializes persistence (demo mode, existing
// unit tests) behaves exactly as before — create, use, revoke all in memory.
func TestTokenPersistence_NoPersisterKeepsInMemoryContract(t *testing.T) {
	s := newLocalStore(t)

	plaintext, err := s.CreateToken("plain-memory", "viewer", 0)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, _, err := s.AuthenticateToken(plaintext); err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if err := s.RevokeToken("plain-memory"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, _, err := s.AuthenticateToken(plaintext); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("revoked in-memory token should be gone, got: %v", err)
	}
}
