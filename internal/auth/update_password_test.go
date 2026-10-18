package auth

// update_password_test.go — L14 (epic video-leftovers, 2026-10-18).
//
// UpdatePassword (a user changing their own password) used to do a plain
// Get then Update of the Secret with no retry, so a change another writer made
// at the same time was refused as a conflict, and the error was sorted into
// 400 or 401 by looking for words in its text. It now reads fresh, retries on
// a conflict, and returns typed errors: a wrong current password, a new
// password that is too short (an input error), or a storage failure.

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

const (
	pwOld = "dana-old-password-1"
	pwNew = "dana-new-password-2"
)

func newPasswordServer(t *testing.T) (*rvServer, *Store) {
	t.Helper()
	oldHash, err := bcrypt.GenerateFromPassword([]byte(pwOld), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	srv := newRVServer(t, delAccounts, map[string][]byte{
		"alice.password":       []byte("alice-hash"),
		"dana.password":        oldHash,
		"dana.initialPassword": []byte("dana-initial"),
	})
	st := srv.store()
	if err := st.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	return srv, st
}

func TestUpdatePasswordK8s_ConflictRetries(t *testing.T) {
	srv, st := newPasswordServer(t)
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpSecret(t, "someone.else")
		}
	}
	if err := st.UpdatePassword("dana", pwOld, pwNew); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	data := srv.secretData(t)
	if bcrypt.CompareHashAndPassword(data["dana.password"], []byte(pwNew)) != nil {
		t.Error("the new password was not saved in the Secret")
	}
	if string(data["someone.else"]) != "other-writer" {
		t.Errorf("another writer's change was lost: %q", data["someone.else"])
	}
	if _, ok := data["dana.initialPassword"]; ok {
		t.Error("the initial password entry was not taken out")
	}
	if string(data["alice.password"]) != "alice-hash" {
		t.Error("another user's password changed")
	}
	if !st.ValidateCredentials("dana", pwNew) || st.ValidateCredentials("dana", pwOld) {
		t.Error("memory does not hold the new password")
	}
}

func TestUpdatePasswordK8s_WriteFailsIsReturned(t *testing.T) {
	srv, st := newPasswordServer(t)
	srv.failCall("update", "secrets", 1, errBoom)
	err := st.UpdatePassword("dana", pwOld, pwNew)
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("want the write error, got %v", err)
	}
	if err.Error() != "updating Secret: "+errBoom.Error() {
		t.Errorf("error = %q, want it to say which write failed", err.Error())
	}
	if IsInputError(err) || errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Error("a storage failure was reported as a bad request")
	}
	if strings.Contains(err.Error(), pwNew) || strings.Contains(err.Error(), pwOld) {
		t.Errorf("the error text holds a password: %q", err.Error())
	}
	assertNoSecretInError(t, srv, err)
	if !st.ValidateCredentials("dana", pwOld) || st.ValidateCredentials("dana", pwNew) {
		t.Error("memory changed although the write failed")
	}
}

func TestUpdatePasswordK8s_ReadFailsIsReturned(t *testing.T) {
	srv, st := newPasswordServer(t)
	srv.mu.Lock()
	reads := srv.calls["get secrets"]
	srv.mu.Unlock()
	// The first read is the reload inside ValidateCredentials (the current
	// password check). The second is UpdatePassword's own fresh read.
	srv.failCall("get", "secrets", reads+2, errBoom)
	err := st.UpdatePassword("dana", pwOld, pwNew)
	if err == nil || err.Error() != "reading Secret: "+errBoom.Error() {
		t.Fatalf("error = %v, want the read error", err)
	}
	if IsInputError(err) {
		t.Error("a storage failure was reported as a bad request")
	}
	if !st.ValidateCredentials("dana", pwOld) {
		t.Error("memory changed although nothing was saved")
	}
}

func TestUpdatePassword_InputErrors(t *testing.T) {
	_, st := newPasswordServer(t)

	err := st.UpdatePassword("dana", "not-the-password", pwNew)
	if !errors.Is(err, ErrCurrentPasswordIncorrect) || err.Error() != "current password is incorrect" {
		t.Errorf("wrong current password: error = %v", err)
	}
	if IsInputError(err) {
		t.Error("a wrong current password must not be a plain input error (it is 401, not 400)")
	}

	err = st.UpdatePassword("dana", pwOld, "short")
	if err == nil || err.Error() != "new password must be at least 12 characters" || !IsInputError(err) {
		t.Errorf("short new password: error = %v, want the input error", err)
	}
	if !st.ValidateCredentials("dana", pwOld) {
		t.Error("the old password stopped working after a refused change")
	}
}
