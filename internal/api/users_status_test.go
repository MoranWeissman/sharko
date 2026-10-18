package api

// users_status_test.go — CreateUser and DeleteUser answer 400 only when what
// the caller asked for was wrong, and 500 when saving to storage failed.
// Before, a storage failure came back as 400, which told the caller to fix a
// request that was fine.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newTestAuthStoreWithClient is newTestAuthStore, but it also hands back the
// fake client so a test can make chosen calls fail.
func newTestAuthStoreWithClient(t *testing.T) (*auth.Store, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "sharko-auth-users", Namespace: "default"},
			Data:       map[string]string{"accounts": ""},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "sharko-auth", Namespace: "default"},
			Data:       map[string][]byte{},
		},
	)
	st := auth.NewStore()
	st.SetClientForTest(cs, "default", "sharko-auth")
	return st, cs
}

func doCreateUser(t *testing.T, s *Server, name string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": name, "role": "viewer"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	s.handleCreateUser(w, req)
	return w.Code, errorField(t, w)
}

func doDeleteUser(t *testing.T, s *Server, name string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+name, nil)
	req.SetPathValue("username", name)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	s.handleDeleteUser(w, req)
	return w.Code, errorField(t, w)
}

func errorField(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	msg, _ := body["error"].(string)
	return msg
}

func failUpdates(cs *fake.Clientset, resource string) {
	cs.PrependReactor("update", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("storage is down")
	})
}

func TestHandleCreateUser_StorageFailureIs500(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	failUpdates(cs, "secrets")

	code, msg := doCreateUser(t, s, "dana")
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500, got %d (%q)", code, msg)
	}
	const want = "updating Secret: storage is down"
	if msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
}

func TestHandleCreateUser_AlreadyExistsIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if code, msg := doCreateUser(t, s, "dana"); code != http.StatusCreated {
		t.Fatalf("first create: %d %q", code, msg)
	}
	code, msg := doCreateUser(t, s, "dana")
	if code != http.StatusBadRequest {
		t.Errorf("a user that already exists must be 400, got %d", code)
	}
	if msg != `user "dana" already exists` {
		t.Errorf("error = %q", msg)
	}
}

func TestHandleDeleteUser_StorageFailureIs500(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if code, msg := doCreateUser(t, s, "dana"); code != http.StatusCreated {
		t.Fatalf("create: %d %q", code, msg)
	}
	failUpdates(cs, "configmaps")

	code, msg := doDeleteUser(t, s, "dana")
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500, got %d (%q)", code, msg)
	}
	const want = `removing user "dana" from the user list: updating ConfigMap: storage is down`
	if msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
}

func TestHandleDeleteUser_NotFoundIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	code, msg := doDeleteUser(t, s, "nobody")
	if code != http.StatusBadRequest {
		t.Errorf("an unknown user must be 400, got %d", code)
	}
	if msg != `user "nobody" not found` {
		t.Errorf("error = %q", msg)
	}
	if strings.Contains(msg, "$2a$") {
		t.Error("hash in error")
	}
}

func doUpdateUser(t *testing.T, s *Server, name, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+name, strings.NewReader(body))
	req.SetPathValue("username", name)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	s.handleUpdateUser(w, req)
	return w.Code, errorField(t, w)
}

func doResetPassword(t *testing.T, s *Server, name string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+name+"/reset-password", nil)
	req.SetPathValue("username", name)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	w := httptest.NewRecorder()
	s.handleResetPassword(w, req)
	return w.Code, errorField(t, w), w.Body.String()
}

// Updating a user: a storage failure is 500, an unknown user or role is 400.
func TestHandleUpdateUser_StorageFailureIs500(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if code, msg := doCreateUser(t, s, "dana"); code != http.StatusCreated {
		t.Fatalf("create: %d %q", code, msg)
	}
	failUpdates(cs, "configmaps")

	code, msg := doUpdateUser(t, s, "dana", `{"role":"operator"}`)
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500, got %d (%q)", code, msg)
	}
	const want = "updating ConfigMap: storage is down"
	if msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
}

func TestHandleUpdateUser_NotFoundIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	code, msg := doUpdateUser(t, s, "nobody", `{"role":"viewer"}`)
	if code != http.StatusBadRequest {
		t.Errorf("an unknown user must be 400, got %d", code)
	}
	if msg != `user "nobody" not found` {
		t.Errorf("error = %q", msg)
	}
}

func TestHandleUpdateUser_BadRoleIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if code, msg := doCreateUser(t, s, "dana"); code != http.StatusCreated {
		t.Fatalf("create: %d %q", code, msg)
	}
	code, msg := doUpdateUser(t, s, "dana", `{"role":"boss"}`)
	if code != http.StatusBadRequest {
		t.Errorf("an unknown role must be 400, got %d", code)
	}
	if msg != "role must be admin, operator, or viewer" {
		t.Errorf("error = %q", msg)
	}
}

// Resetting a password: a storage failure is 500, an unknown user is 400,
// and the answer to a failure carries no password or hash.
func TestHandleResetPassword_StorageFailureIs500(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if code, msg := doCreateUser(t, s, "dana"); code != http.StatusCreated {
		t.Fatalf("create: %d %q", code, msg)
	}
	failUpdates(cs, "secrets")

	code, msg, body := doResetPassword(t, s, "dana")
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500, got %d (%q)", code, msg)
	}
	const want = "updating Secret: storage is down"
	if msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
	if strings.Contains(body, "temp_password") || strings.Contains(body, "$2a$") {
		t.Errorf("a failed reset answer holds a password or hash: %s", body)
	}
}

func TestHandleResetPassword_NotFoundIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	code, msg, _ := doResetPassword(t, s, "nobody")
	if code != http.StatusBadRequest {
		t.Errorf("an unknown user must be 400, got %d", code)
	}
	if msg != `user "nobody" not found` {
		t.Errorf("error = %q", msg)
	}
}

func doUpdatePassword(t *testing.T, s *Server, user, current, next string) (int, string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/update-password", bytes.NewReader(body))
	req.Header.Set("X-Sharko-User", user)
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	s.handleUpdatePassword(w, req)
	return w.Code, errorField(t, w), w.Body.String()
}

// Changing your own password (L14): a storage failure is 500, a wrong current
// password is 401, a too-short new password is 400. The status is picked by
// error type, so these hold even if the storage error's words change.
func TestHandleUpdatePassword_StorageFailureIs500(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	pass, err := st.CreateUser("dana", "viewer")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	failUpdates(cs, "secrets")

	const next = "a-brand-new-password-1"
	code, msg, body := doUpdatePassword(t, s, "dana", pass, next)
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500, got %d (%q)", code, msg)
	}
	const want = "updating Secret: storage is down"
	if msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
	if strings.Contains(body, next) || strings.Contains(body, pass) || strings.Contains(body, "$2a$") {
		t.Errorf("a failed password change answer holds a password or hash: %s", body)
	}
}

// A storage error whose words happen to say "incorrect" or "at least" must
// still be 500. The old handler looked for those words in the error text.
func TestHandleUpdatePassword_StorageErrorWordsDoNotPickTheStatus(t *testing.T) {
	st, cs := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	pass, err := st.CreateUser("dana", "viewer")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cs.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("disk quota incorrect, needs at least 1 GB")
	})
	code, msg, _ := doUpdatePassword(t, s, "dana", pass, "a-brand-new-password-1")
	if code != http.StatusInternalServerError {
		t.Errorf("a storage failure must be 500 whatever its words, got %d (%q)", code, msg)
	}
}

func TestHandleUpdatePassword_WrongCurrentIs401(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	if _, err := st.CreateUser("dana", "viewer"); err != nil {
		t.Fatalf("create: %v", err)
	}
	code, msg, _ := doUpdatePassword(t, s, "dana", "not-the-password", "a-brand-new-password-1")
	if code != http.StatusUnauthorized {
		t.Errorf("a wrong current password must be 401, got %d", code)
	}
	if msg != "current password is incorrect" {
		t.Errorf("error = %q", msg)
	}
}

func TestHandleUpdatePassword_ShortNewPasswordIs400(t *testing.T) {
	st, _ := newTestAuthStoreWithClient(t)
	s := &Server{authStore: st, auditLog: audit.NewLog(100)}
	pass, err := st.CreateUser("dana", "viewer")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	code, msg, _ := doUpdatePassword(t, s, "dana", pass, "short")
	if code != http.StatusBadRequest {
		t.Errorf("a too-short new password must be 400, got %d", code)
	}
	if msg != "new password must be at least 12 characters" {
		t.Errorf("error = %q", msg)
	}
}
