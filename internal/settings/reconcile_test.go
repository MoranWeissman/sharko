package settings

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestReconcile_DeclaredKeyWins tests that a Helm/git-declared setting
// (via env var) overwrites the runtime ConfigMap value (git wins).
func TestReconcile_DeclaredKeyWins(t *testing.T) {
	t.Setenv("SHARKO_PROBE_MODE", "api-test")

	// Start with ConfigMap holding a different runtime value
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "sharko",
		},
		Data: map[string]string{
			"state": `{"probe_mode": "check-app"}`,
		},
	})

	store := NewStore(client, "sharko")
	ctx := context.Background()

	// Reconcile should overwrite runtime value with env-declared value
	if err := store.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Read back and verify git-declared value won
	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode failed: %v", err)
	}
	if mode != ProbeModeAPITest {
		t.Errorf("expected env-declared value %q, got %q", ProbeModeAPITest, mode)
	}
}

// TestReconcile_UndeclaredKeyPersists tests that when a key is NOT
// env-declared (unset), the runtime ConfigMap value persists (API
// authoritative, back-compat).
func TestReconcile_UndeclaredKeyPersists(t *testing.T) {
	// No SHARKO_PROBE_MODE env var → undeclared

	// ConfigMap holds a runtime-set value
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "sharko",
		},
		Data: map[string]string{
			"state": `{"probe_mode": "api-test"}`,
		},
	})

	store := NewStore(client, "sharko")
	ctx := context.Background()

	// Reconcile should no-op (nothing declared)
	if err := store.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Runtime value should persist unchanged
	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode failed: %v", err)
	}
	if mode != ProbeModeAPITest {
		t.Errorf("expected runtime value to persist, got %q", mode)
	}
}

// TestReconcile_DefaultWhenUnset tests that when a key is neither
// env-declared nor runtime-set, the built-in default applies.
func TestReconcile_DefaultWhenUnset(t *testing.T) {
	// No env var, empty ConfigMap
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "sharko",
		},
		Data: map[string]string{
			"state": `{}`,
		},
	})

	store := NewStore(client, "sharko")
	ctx := context.Background()

	if err := store.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Should return built-in default
	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode failed: %v", err)
	}
	if mode != ProbeModeCheckApp {
		t.Errorf("expected default %q, got %q", ProbeModeCheckApp, mode)
	}
}

// TestReconcile_MalformedEnvValue tests that a malformed declared value
// (invalid probe_mode or non-bool for allow_inline_credentials) is lenient
// — warns + falls back, never crashes boot.
func TestReconcile_MalformedEnvValue(t *testing.T) {
	t.Setenv("SHARKO_PROBE_MODE", "invalid-mode")

	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "sharko",
		},
		Data: map[string]string{
			"state": `{}`,
		},
	})

	store := NewStore(client, "sharko")
	ctx := context.Background()

	// Reconcile should NOT crash; malformed value skipped with warning
	if err := store.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Should return default (malformed value ignored)
	mode, err := store.GetProbeMode(ctx)
	if err != nil {
		t.Fatalf("GetProbeMode failed: %v", err)
	}
	if mode != ProbeModeCheckApp {
		t.Errorf("expected fallback to default, got %q", mode)
	}
}

// TestReconcile_BoolSetting tests git-wins for a bool setting
// (allow_inline_credentials).
func TestReconcile_BoolSetting(t *testing.T) {
	t.Setenv("SHARKO_ALLOW_INLINE_CREDENTIALS", "false")

	// Runtime ConfigMap holds true (opposite of env)
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "sharko",
		},
		Data: map[string]string{
			"state": `{"allow_inline_credentials": true}`,
		},
	})

	store := NewStore(client, "sharko")
	ctx := context.Background()

	// Reconcile should overwrite with env-declared false
	if err := store.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	allow, err := store.GetAllowInlineCredentials(ctx)
	if err != nil {
		t.Fatalf("GetAllowInlineCredentials failed: %v", err)
	}
	if allow != false {
		t.Errorf("expected env-declared false, got %v", allow)
	}
}

// TestIsManagedByGit tests that IsManagedByGit correctly reports whether
// a key is env-declared (git-native).
func TestIsManagedByGit(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envKey   string
		envValue string
		want     bool
	}{
		{
			name:     "probe_mode declared",
			key:      keyProbeMode,
			envKey:   "SHARKO_PROBE_MODE",
			envValue: "api-test",
			want:     true,
		},
		{
			name:     "probe_mode undeclared",
			key:      keyProbeMode,
			envKey:   "SHARKO_PROBE_MODE",
			envValue: "",
			want:     false,
		},
		{
			name:     "allow_inline_credentials declared",
			key:      keyAllowInlineCredentials,
			envKey:   "SHARKO_ALLOW_INLINE_CREDENTIALS",
			envValue: "true",
			want:     true,
		},
		{
			name:     "allow_inline_credentials undeclared",
			key:      keyAllowInlineCredentials,
			envKey:   "SHARKO_ALLOW_INLINE_CREDENTIALS",
			envValue: "",
			want:     false,
		},
		{
			name: "unknown key",
			key:  "unknown",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear and optionally set env
			if tt.envKey != "" {
				os.Unsetenv(tt.envKey)
				if tt.envValue != "" {
					t.Setenv(tt.envKey, tt.envValue)
				}
			}

			got := IsManagedByGit(tt.key)
			if got != tt.want {
				t.Errorf("IsManagedByGit(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// TestDesiredSettingFromEnv tests the helper that resolves desired state
// from env vars.
func TestDesiredSettingFromEnv(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		envValue   string
		isBool     bool
		wantValue  interface{}
		wantDeclrd bool
	}{
		{
			name:       "string declared",
			envKey:     "TEST_STRING",
			envValue:   "some-value",
			isBool:     false,
			wantValue:  "some-value",
			wantDeclrd: true,
		},
		{
			name:       "string undeclared",
			envKey:     "TEST_STRING",
			envValue:   "",
			isBool:     false,
			wantValue:  nil,
			wantDeclrd: false,
		},
		{
			name:       "bool true",
			envKey:     "TEST_BOOL",
			envValue:   "true",
			isBool:     true,
			wantValue:  true,
			wantDeclrd: true,
		},
		{
			name:       "bool false",
			envKey:     "TEST_BOOL",
			envValue:   "false",
			isBool:     true,
			wantValue:  false,
			wantDeclrd: true,
		},
		{
			name:       "bool malformed",
			envKey:     "TEST_BOOL",
			envValue:   "maybe",
			isBool:     true,
			wantValue:  nil,
			wantDeclrd: false,
		},
		{
			name:       "bool undeclared",
			envKey:     "TEST_BOOL",
			envValue:   "",
			isBool:     true,
			wantValue:  nil,
			wantDeclrd: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(tt.envKey)
			if tt.envValue != "" {
				t.Setenv(tt.envKey, tt.envValue)
			}

			gotValue, gotDeclrd := desiredSettingFromEnv(tt.envKey, tt.isBool)
			if gotDeclrd != tt.wantDeclrd {
				t.Errorf("declared: got %v, want %v", gotDeclrd, tt.wantDeclrd)
			}
			if gotValue != tt.wantValue {
				t.Errorf("value: got %v, want %v", gotValue, tt.wantValue)
			}
		})
	}
}
