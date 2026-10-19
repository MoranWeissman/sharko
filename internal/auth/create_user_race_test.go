package auth

// create_user_race_test.go — LO-7 R1 (epic video-leftovers, 2026-10-18).
//
// The LO-4 review found this case: admin B and admin A both create "dana" at
// the same moment. B writes its password, then A writes its password over it.
// B's user entry lands (role viewer). A's user list write fails and its
// answer is lost. A then reads again, finds an entry for dana (B's) and its
// own hash in the Secret, and says "user made" — as admin in memory, while the
// stored list says viewer, and B's password no longer works.
//
// Two fixes, one test each:
//   - the lost-answer check only counts the entry when it has the role and
//     the enabled flag A wrote;
//   - CreateUser holds one lock from its "already exists" check to the end,
//     so two creates in Sharko (one instance) cannot run at the same time.

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// TestCreateUserK8s_LostAnswerAndAnotherCreateOfTheSameName is the
// reviewer's case, on the resourceVersion-checking fake API server.
func TestCreateUserK8s_LostAnswerAndAnotherCreateOfTheSameName(t *testing.T) {
	// B already wrote its password for dana.
	srv := newRVServer(t, "alice:\n  enabled: true\n  role: admin\n",
		map[string][]byte{"dana.password": []byte("b-create-hash")})
	// A's user list write: just before it, B's entry for dana lands (role
	// viewer). Then A's write fails and the answer is lost.
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "dana")
		}
	}
	srv.failCall("update", "configmaps", 1, errAnswerLost)
	st := srv.store()

	pass, err := st.CreateUser("dana", "admin")
	if err == nil {
		t.Fatalf("CreateUser said dana was made as admin (password %q), but the saved entry is B's viewer entry", pass)
	}
	if pass != "" {
		t.Error("a password came back with the error")
	}
	if !errors.Is(err, errAnswerLost) {
		t.Errorf("the error does not hold the API error: %v", err)
	}
	if u := st.GetUser("dana"); u != nil {
		t.Errorf("dana is in memory as %q after a failed create", u.Role)
	}
	// B's password must be back, so the password B was told works.
	if got := string(srv.secretData(t)["dana.password"]); got != "b-create-hash" {
		t.Errorf("B's password for dana was not put back: %q", got)
	}
	// B's entry is untouched.
	entry, ok := accountsEntry(srv.accounts(t), "dana")
	if !ok || entry.Role != "viewer" || !entry.Enabled {
		t.Errorf("B's entry for dana changed: %+v (found %v)", entry, ok)
	}
	assertNoSecretInError(t, srv, err)
}

// TestCreateUserK8s_LostAnswerAndADisabledEntry: the entry that is there has
// our role but is disabled. We wrote an enabled one, so it is not ours.
func TestCreateUserK8s_LostAnswerAndADisabledEntry(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMapEntry(t, "dana", false, "admin")
		}
	}
	srv.failCall("update", "configmaps", 1, errAnswerLost)
	st := srv.store()

	if _, err := st.CreateUser("dana", "admin"); err == nil {
		t.Fatal("CreateUser said dana was made, but the saved entry is a disabled one it did not write")
	}
	if st.GetUser("dana") != nil {
		t.Error("dana is in memory after a failed create")
	}
	if _, ok := srv.secretData(t)["dana.password"]; ok {
		t.Error("our password for dana was left in the Secret")
	}
}

// TestCreateUser_TwoCreatesOfTheSameNameDoNotOverlap: while one create of
// dana is about to write its password, a second create of dana is started.
// The second one must not reach the API server until the first one is done,
// and must then say dana already exists.
//
// How "reach the API server" is seen: the fake runs its reactors under its
// own lock, so a call from the second create waits there while our hook
// sleeps, and goes in the moment the hook returns (Go hands a lock straight
// to a caller that has waited this long). So if the second create is not
// held back, its first call — reading the users ConfigMap — lands before the
// first create writes the user list. Alone, the first create reads that
// ConfigMap exactly twice before its write.
func TestCreateUser_TwoCreatesOfTheSameNameDoNotOverlap(t *testing.T) {
	srv := newRVServer(t, "", nil)
	st := srv.store()

	type result struct {
		pass string
		err  error
	}
	second := make(chan result, 1)
	var once sync.Once
	srv.beforeUpdate["secrets"] = func(int) {
		once.Do(func() {
			// The first create is about to write its password.
			go func() {
				p, err := st.CreateUser("dana", "viewer")
				second <- result{p, err}
			}()
			// Give the second create plenty of time to get going, if
			// nothing holds it back.
			time.Sleep(300 * time.Millisecond)
		})
	}
	readsBeforeListWrite := -1
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.mu.Lock()
			readsBeforeListWrite = srv.calls["get configmaps"]
			srv.mu.Unlock()
		}
	}

	firstPass, firstErr := st.CreateUser("dana", "admin")
	got := <-second

	if readsBeforeListWrite != 2 {
		t.Errorf("the users ConfigMap was read %d times before the first create wrote it, want 2: "+
			"the second create ran while the first one was still going", readsBeforeListWrite)
	}
	if firstErr != nil {
		t.Fatalf("first create: %v", firstErr)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), `user "dana" already exists`) {
		t.Errorf("second create: want already exists, got password %q, error %v", got.pass, got.err)
	}
	if !IsInputError(got.err) {
		t.Errorf("second create: already exists must be an input error, got %v", got.err)
	}
	if u := st.GetUser("dana"); u == nil || u.Role != "admin" {
		t.Errorf("dana in memory: %+v, want admin", u)
	}
	entry, ok := accountsEntry(srv.accounts(t), "dana")
	if !ok || entry.Role != "admin" {
		t.Errorf("dana in the users ConfigMap: %+v (found %v), want admin", entry, ok)
	}
	if !st.ValidateCredentials("dana", firstPass) {
		t.Error("dana cannot log in with the password the first create handed back")
	}
}

// bumpConfigMapEntry adds one user entry with the given enabled flag and
// role, the way another writer would, and moves the resourceVersion on.
func (s *rvServer) bumpConfigMapEntry(t *testing.T, user string, enabled bool, role string) {
	t.Helper()
	line := user + ":\n  enabled: " + map[bool]string{true: "true", false: "false"}[enabled] + "\n  role: " + role + "\n"
	gvr := corev1.SchemeGroupVersion.WithResource("configmaps")
	obj, err := s.cs.Tracker().Get(gvr, rvNS, rvCM)
	if err != nil {
		t.Fatalf("bumpConfigMapEntry get: %v", err)
	}
	cm := obj.(*corev1.ConfigMap)
	n, _ := strconv.Atoi(cm.ResourceVersion)
	cm.ResourceVersion = strconv.Itoa(n + 1)
	cm.Data["accounts"] += line
	if err := s.cs.Tracker().Update(gvr, cm, rvNS); err != nil {
		t.Fatalf("bumpConfigMapEntry update: %v", err)
	}
}
