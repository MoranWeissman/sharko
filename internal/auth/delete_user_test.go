package auth

// delete_user_test.go — DeleteUser in Kubernetes mode, on the fake from
// create_user_rollback_test.go that refuses stale resourceVersions like a
// real API server. DeleteUser used to ignore its own write errors and send
// back the object it had read, so a refused write was never noticed.

import (
	"errors"
	"strings"
	"testing"
)

const (
	delAccounts = "alice:\n  enabled: true\n  role: admin\n" +
		"dana:\n  enabled: true\n  role: viewer\n"
)

func newDeleteServer(t *testing.T) (*rvServer, *Store) {
	t.Helper()
	srv := newRVServer(t, delAccounts, map[string][]byte{
		"alice.password":       []byte("alice-hash"),
		"dana.password":        []byte("dana-hash"),
		"dana.initialPassword": []byte("dana-initial"),
	})
	st := srv.store()
	if err := st.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if st.GetUser("dana") == nil {
		t.Fatal("setup: dana is not in memory")
	}
	return srv, st
}

// assertNoHashInError checks the error text holds none of the stored values.
func assertNoHashInError(t *testing.T, err error) {
	t.Helper()
	for _, v := range []string{"alice-hash", "dana-hash", "dana-initial", "$2a$"} {
		if strings.Contains(err.Error(), v) {
			t.Errorf("the error text holds a stored password value: %q", err.Error())
		}
	}
}

func TestDeleteUserK8s_HappyPath(t *testing.T) {
	srv, st := newDeleteServer(t)
	if err := st.DeleteUser("dana"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if accountsHasUser(srv.accounts(t), "dana") {
		t.Errorf("dana is still in the user list: %q", srv.accounts(t))
	}
	if !accountsHasUser(srv.accounts(t), "alice") {
		t.Errorf("alice was removed too: %q", srv.accounts(t))
	}
	data := srv.secretData(t)
	if _, ok := data["dana.password"]; ok {
		t.Error("dana's password is still in the Secret")
	}
	if _, ok := data["dana.initialPassword"]; ok {
		t.Error("dana's first password is still in the Secret")
	}
	if string(data["alice.password"]) != "alice-hash" {
		t.Error("alice's password changed")
	}
	if st.GetUser("dana") != nil {
		t.Error("dana is still in memory")
	}
}

// TestDeleteUserK8s_ConfigMapWriteFails: the user list write is refused. The
// error must come back, and nothing may be removed anywhere.
func TestDeleteUserK8s_ConfigMapWriteFails(t *testing.T) {
	srv, st := newDeleteServer(t)
	srv.failCall("update", "configmaps", 1, errBoom)

	err := st.DeleteUser("dana")
	if err == nil {
		t.Fatal("want an error, got none: the refused write was ignored")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("the API error is not in the result: %v", err)
	}
	const want = `removing user "dana" from the user list: updating ConfigMap: `
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error %q does not start with %q", err.Error(), want)
	}
	assertNoHashInError(t, err)
	if !accountsHasUser(srv.accounts(t), "dana") {
		t.Errorf("dana left the user list although the write failed: %q", srv.accounts(t))
	}
	if string(srv.secretData(t)["dana.password"]) != "dana-hash" {
		t.Error("dana's password was removed although her user entry is still there")
	}
	if st.GetUser("dana") == nil {
		t.Error("dana was dropped from memory although she is still saved")
	}
}

// TestDeleteUserK8s_ConfigMapReadFails: the first read fails, nothing changes.
func TestDeleteUserK8s_ConfigMapReadFails(t *testing.T) {
	srv, st := newDeleteServer(t)
	// get configmaps 1 was the reload in setup.
	srv.failCall("get", "configmaps", 2, errBoom)

	err := st.DeleteUser("dana")
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("want the read error, got %v", err)
	}
	if !strings.Contains(err.Error(), "reading ConfigMap") {
		t.Errorf("error %q does not say which read failed", err.Error())
	}
	if srv.calls["update secrets"] != 0 {
		t.Error("the Secret was written although the user list was not")
	}
}

// TestDeleteUserK8s_ConfigMapConflictRetries: another writer adds a user
// between our read and our write. The write gets 409 and must read again,
// keeping the other writer's user.
func TestDeleteUserK8s_ConfigMapConflictRetries(t *testing.T) {
	srv, st := newDeleteServer(t)
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "erin")
		}
	}
	if err := st.DeleteUser("dana"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	acc := srv.accounts(t)
	if accountsHasUser(acc, "dana") {
		t.Errorf("dana is still in the user list: %q", acc)
	}
	if !accountsHasUser(acc, "erin") || !accountsHasUser(acc, "alice") {
		t.Errorf("another writer's user was lost: %q", acc)
	}
}

// TestDeleteUserK8s_SecretWriteFails: the user entry is gone, but the
// password could not be removed. The error must say exactly that.
func TestDeleteUserK8s_SecretWriteFails(t *testing.T) {
	srv, st := newDeleteServer(t)
	srv.failCall("update", "secrets", 1, errBoom)

	err := st.DeleteUser("dana")
	if err == nil {
		t.Fatal("want an error, got none: the refused write was ignored")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("the API error is not in the result: %v", err)
	}
	const want = `user "dana" was removed from the user list, but the password entry could not be taken out of the Secret: updating Secret: `
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error %q does not start with %q", err.Error(), want)
	}
	assertNoHashInError(t, err)
	if accountsHasUser(srv.accounts(t), "dana") {
		t.Errorf("dana is still in the user list: %q", srv.accounts(t))
	}
	if st.GetUser("dana") != nil {
		t.Error("dana is still in memory although she is out of the user list")
	}
}

// TestDeleteUserK8s_SecretConflictRetries: another writer changes the Secret
// between our read and our write. Our write gets 409 and must read again,
// keeping the other writer's key.
func TestDeleteUserK8s_SecretConflictRetries(t *testing.T) {
	srv, st := newDeleteServer(t)
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpSecret(t, "someone.else")
		}
	}
	if err := st.DeleteUser("dana"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	data := srv.secretData(t)
	if _, ok := data["dana.password"]; ok {
		t.Error("dana's password is still in the Secret")
	}
	if string(data["someone.else"]) != "other-writer" {
		t.Errorf("another writer's change was lost: %q", data["someone.else"])
	}
}

// TestDeleteUserK8s_BrokenUserList: a user list that cannot be read must not
// be written back (that would throw away every user).
func TestDeleteUserK8s_BrokenUserList(t *testing.T) {
	srv, st := newDeleteServer(t)
	// Break the list after setup loaded it into memory.
	srv.bumpConfigMap(t, "  : : not yaml [")

	before := srv.accounts(t)
	err := st.DeleteUser("dana")
	if err == nil {
		t.Fatal("want an error for a user list that cannot be read")
	}
	if !strings.Contains(err.Error(), "reading the user list in the ConfigMap") {
		t.Errorf("unexpected error: %v", err)
	}
	if got := srv.accounts(t); got != before {
		t.Errorf("the broken user list was written over: %q", got)
	}
}

// TestIsInputError: only a bare input error counts. Joined with a storage
// failure (an undo that failed), it must not, so the API answers 500.
func TestIsInputError(t *testing.T) {
	in := inputErrorf("user %q already exists", "dana")
	if !IsInputError(in) {
		t.Error("a bare input error is not seen as one")
	}
	if !IsInputError(ErrInvalidUsername) || !errors.Is(ErrInvalidUsername, ErrInvalidUsername) {
		t.Error("ErrInvalidUsername must be an input error")
	}
	if IsInputError(errors.Join(in, errBoom)) {
		t.Error("an input error joined with a storage failure counts as input only")
	}
	if IsInputError(errBoom) || IsInputError(nil) {
		t.Error("a plain error counts as an input error")
	}
}

// TestCreateUserK8s_ExistsButUndoFailsIsNotInputError: the user shows up
// meanwhile (an input error) and then the undo fails (a storage failure).
// The result must not be a bare input error.
func TestCreateUserK8s_ExistsButUndoFailsIsNotInputError(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "dana")
		}
	}
	srv.failCall("update", "secrets", 2, errBoom)
	st := srv.store()
	_, err := st.CreateUser("dana", "viewer")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsInputError(err) {
		t.Errorf("a failed undo was reported as only a bad request: %v", err)
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("the undo error is missing: %v", err)
	}
}
