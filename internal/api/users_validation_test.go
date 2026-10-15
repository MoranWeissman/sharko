package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestAuthStore(t *testing.T) *auth.Store {
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

	// Create store using the same pattern as the auth tests
	s := auth.NewStore()
	s.SetClientForTest(clientset, "default", "sharko-auth")
	return s
}

// TestHandleCreateUser_InvalidUsername verifies that the API returns 400 with
// the exact error message for invalid usernames.
func TestHandleCreateUser_InvalidUsername(t *testing.T) {
	tests := []struct {
		name         string
		username     string
		expectedCode int
		expectedMsg  string
	}{
		{
			name:         "username with space",
			username:     "john doe",
			expectedCode: http.StatusBadRequest,
			expectedMsg:  auth.ErrInvalidUsername,
		},
		{
			name:         "username with slash",
			username:     "john/doe",
			expectedCode: http.StatusBadRequest,
			expectedMsg:  auth.ErrInvalidUsername,
		},
		{
			name:         "empty username",
			username:     "",
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "username is required",
		},
		{
			name:         "too long username",
			username:     strings.Repeat("a", 64),
			expectedCode: http.StatusBadRequest,
			expectedMsg:  auth.ErrInvalidUsername,
		},
		{
			name:         "valid username",
			username:     "john.doe",
			expectedCode: http.StatusCreated,
			expectedMsg:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authStore := newTestAuthStore(t)
			auditLog := audit.NewLog(100)
			server := &Server{
				authStore: authStore,
				auditLog:  auditLog,
			}

			// Create request
			reqBody := map[string]string{
				"username": tt.username,
				"role":     "viewer",
			}
			bodyBytes, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(bodyBytes))
			req.Header.Set("X-Sharko-User", "admin")
			req.Header.Set("X-Sharko-Role", "admin")

			w := httptest.NewRecorder()

			// Call handler
			server.handleCreateUser(w, req)

			// Check response
			if w.Code != tt.expectedCode {
				t.Errorf("expected status %d, got %d", tt.expectedCode, w.Code)
			}

			if tt.expectedMsg != "" {
				var respBody map[string]interface{}
				json.Unmarshal(w.Body.Bytes(), &respBody)
				if errMsg, ok := respBody["error"].(string); ok {
					if errMsg != tt.expectedMsg {
						t.Errorf("expected error message %q, got %q", tt.expectedMsg, errMsg)
					}
				} else {
					t.Errorf("expected error message in response, got: %s", w.Body.String())
				}
			}
		})
	}
}

// TestExactErrorMessage_API verifies the exact error message text in the API response.
func TestExactErrorMessage_API(t *testing.T) {
	const expectedMessage = "A user name can use letters, numbers, dots, dashes and underscores, with no spaces."

	authStore := newTestAuthStore(t)
	auditLog := audit.NewLog(100)
	server := &Server{
		authStore: authStore,
		auditLog:  auditLog,
	}

	// Create request with invalid username
	reqBody := map[string]string{
		"username": "invalid name",
		"role":     "viewer",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(bodyBytes))
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")

	w := httptest.NewRecorder()
	server.handleCreateUser(w, req)

	// Check response
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}

	var respBody map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	errMsg, ok := respBody["error"].(string)
	if !ok {
		t.Fatalf("expected error field in response, got: %+v", respBody)
	}

	if errMsg != expectedMessage {
		t.Errorf("API error message mismatch.\nExpected: %q\nGot:      %q", expectedMessage, errMsg)
	}
}
