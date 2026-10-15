package auth

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sharko-auth-users",
			Namespace: "default",
		},
		Data: map[string]string{
			"accounts": "",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sharko-auth",
			Namespace: "default",
		},
		Data: map[string][]byte{},
	}
	clientset := fake.NewSimpleClientset(configMap, secret)

	s := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	s.SetClientForTest(clientset, "default", "sharko-auth")
	return s
}

// TestUsernameValidation_TableDriven tests the username validation with various inputs.
func TestUsernameValidation_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid simple name",
			username:  "john",
			wantError: false,
		},
		{
			name:      "valid name with dot",
			username:  "john.doe",
			wantError: false,
		},
		{
			name:      "valid name with dash",
			username:  "john-doe",
			wantError: false,
		},
		{
			name:      "valid name with underscore",
			username:  "john_doe",
			wantError: false,
		},
		{
			name:      "valid mixed alphanumeric",
			username:  "user123",
			wantError: false,
		},
		{
			name:      "valid complex name",
			username:  "john.doe_123-test",
			wantError: false,
		},
		{
			name:      "invalid - contains space",
			username:  "john doe",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "invalid - contains multiple spaces",
			username:  "dana operator",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "invalid - contains slash",
			username:  "john/doe",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "invalid - empty string",
			username:  "",
			wantError: true,
			errorMsg:  "username is required",
		},
		{
			name:      "invalid - too long (over 63 chars)",
			username:  strings.Repeat("a", 64),
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "valid - exactly 63 chars",
			username:  "a" + strings.Repeat("b", 62),
			wantError: false,
		},
		{
			name:      "invalid - starts with dot",
			username:  ".john",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "invalid - starts with dash",
			username:  "-john",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
		{
			name:      "invalid - contains special chars",
			username:  "john@example.com",
			wantError: true,
			errorMsg:  ErrInvalidUsername,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			_, err := store.CreateUser(tt.username, "viewer")

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error for username %q, got nil", tt.username)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for valid username %q: %v", tt.username, err)
				}
			}
		})
	}
}

// TestExactErrorMessage verifies the exact error message text for invalid usernames.
func TestExactErrorMessage(t *testing.T) {
	const expectedMessage = "A user name can use letters, numbers, dots, dashes and underscores, with no spaces."

	if ErrInvalidUsername != expectedMessage {
		t.Errorf("ErrInvalidUsername constant mismatch.\nExpected: %q\nGot:      %q", expectedMessage, ErrInvalidUsername)
	}

	// Also test that CreateUser returns this exact message
	store := newTestStore(t)
	_, err := store.CreateUser("invalid name", "viewer")
	if err == nil {
		t.Fatal("expected error for username with space, got nil")
	}

	if err.Error() != expectedMessage {
		t.Errorf("CreateUser error message mismatch.\nExpected: %q\nGot:      %q", expectedMessage, err.Error())
	}
}

// TestCreateUser_RollbackOnSecretFailure verifies that if the Secret write fails,
// the ConfigMap change is rolled back, leaving no half-made user.
func TestCreateUser_RollbackOnSecretFailure(t *testing.T) {
	// Create a fake K8s client that will fail on Secret update
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sharko-auth-users",
			Namespace: "default",
		},
		Data: map[string]string{
			"accounts": "",
		},
	}
	// Don't add the secret, so Get will fail
	clientset := fake.NewSimpleClientset(configMap)

	store := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	store.SetClientForTest(clientset, "default", "sharko-auth")

	_, err := store.CreateUser("testuser", "viewer")
	if err == nil {
		t.Fatal("expected error when Secret is missing, got nil")
	}

	// Verify the error mentions the Secret
	if !strings.Contains(err.Error(), "Secret") {
		t.Errorf("expected error to mention Secret, got: %v", err)
	}

	// Verify the ConfigMap was rolled back (should still be empty or unchanged)
	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), "sharko-auth-users", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
	}

	// The accounts field should be empty (rolled back) or not contain the new user
	if strings.Contains(cm.Data["accounts"], "testuser") {
		t.Errorf("ConfigMap should have been rolled back, but contains 'testuser': %s", cm.Data["accounts"])
	}

	// Verify the user is not in memory either
	if _, exists := store.users["testuser"]; exists {
		t.Error("user should not be in memory after failed creation")
	}
}
