package auth

// update_user_test.go — UpdateUser and ResetPassword in Kubernetes mode, on
// the fake from create_user_rollback_test.go that refuses stale
// resourceVersions like a real API server. Both used to send back the object
// they had read with no retry, and UpdateUser wrote back a user list it could
// not read, which threw away every other user.

import (
	"errors"
	"strings"
	"testing"
)

func newUpdateServer(t *testing.T) (*rvServer, *Store) {
	t.Helper()
	srv := newRVServer(t, delAccounts, map[string][]byte{
		"alice.password": []byte("alice-hash"),
		"dana.password":  []byte("dana-hash"),
	})
	st := srv.store()
	if err := st.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	return srv, st
}

func TestUpdateUserK8s_ConflictRetries(t *testing.T) {
	srv, st := newUpdateServer(t)
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "erin")
		}
	}
	if err := st.UpdateUser("dana", true, "operator"); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	acc := srv.accounts(t)
	if !strings.Contains(acc, "dana:\n    enabled: true\n    role: operator") {
		t.Errorf("dana's new role was not saved: %q", acc)
	}
	if !accountsHasUser(acc, "erin") || !accountsHasUser(acc, "alice") {
		t.Errorf("another writer's user was lost: %q", acc)
	}
}

func TestUpdateUserK8s_BrokenUserListIsNotWrittenBack(t *testing.T) {
	srv, st := newUpdateServer(t)
	srv.bumpConfigMap(t, "  : : not yaml [")
	before := srv.accounts(t)

	err := st.UpdateUser("dana", false, "")
	if err == nil {
		t.Fatal("want an error for a user list that cannot be read")
	}
	if !strings.HasPrefix(err.Error(), "reading the user list in the ConfigMap: ") {
		t.Errorf("unexpected error: %v", err)
	}
	if IsInputError(err) {
		t.Error("a storage problem was reported as a bad request")
	}
	if got := srv.accounts(t); got != before {
		t.Errorf("the broken user list was written over: %q", got)
	}
}

func TestUpdateUserK8s_WriteFailsIsReturned(t *testing.T) {
	srv, st := newUpdateServer(t)
	srv.failCall("update", "configmaps", 1, errBoom)
	err := st.UpdateUser("dana", true, "admin")
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("want the write error, got %v", err)
	}
	if IsInputError(err) {
		t.Error("a storage failure was reported as a bad request")
	}
	if u := st.GetUser("dana"); u == nil || u.Role != "viewer" {
		t.Errorf("memory changed although the write failed: %+v", u)
	}
}

func TestUpdateUser_InputErrors(t *testing.T) {
	_, st := newUpdateServer(t)
	for _, tc := range []struct {
		name, user, role, want string
	}{
		{"unknown user", "nobody", "viewer", `user "nobody" not found`},
		{"unknown role", "dana", "boss", "role must be admin, operator, or viewer"},
	} {
		err := st.UpdateUser(tc.user, true, tc.role)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
		if !IsInputError(err) {
			t.Errorf("%s: not an input error", tc.name)
		}
	}
}

func TestResetPasswordK8s_ConflictRetries(t *testing.T) {
	srv, st := newUpdateServer(t)
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpSecret(t, "someone.else")
		}
	}
	pass, err := st.ResetPassword("dana")
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	data := srv.secretData(t)
	if string(data["dana.password"]) == "dana-hash" {
		t.Error("dana's password was not changed")
	}
	if string(data["someone.else"]) != "other-writer" {
		t.Errorf("another writer's change was lost: %q", data["someone.else"])
	}
	if !st.ValidateCredentials("dana", pass) {
		t.Error("the new password does not work")
	}
}

func TestResetPasswordK8s_WriteFailsIsReturned(t *testing.T) {
	srv, st := newUpdateServer(t)
	srv.failCall("update", "secrets", 1, errBoom)
	pass, err := st.ResetPassword("dana")
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("want the write error, got %v", err)
	}
	if pass != "" {
		t.Error("a password was handed back although it was not saved")
	}
	if IsInputError(err) {
		t.Error("a storage failure was reported as a bad request")
	}
	if !strings.HasPrefix(err.Error(), "updating Secret: ") {
		t.Errorf("error %q does not say which write failed", err.Error())
	}
	for _, h := range srv.sentHashes {
		if strings.Contains(err.Error(), string(h)) {
			t.Errorf("the error text holds a password hash: %q", err.Error())
		}
	}
	assertNoHashInError(t, err)
}

func TestResetPassword_UnknownUserIsInputError(t *testing.T) {
	_, st := newUpdateServer(t)
	_, err := st.ResetPassword("nobody")
	if err == nil || err.Error() != `user "nobody" not found` || !IsInputError(err) {
		t.Errorf("error = %v, want the input error for an unknown user", err)
	}
}
