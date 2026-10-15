package auth

// create_user_rollback_test.go — CreateUser in Kubernetes mode must never
// leave a half-made user behind (a user entry in the ConfigMap with no
// password in the Secret), on a fake that behaves like a real API server.
//
// The plain client-go fake does NOT check resourceVersion. A real API server
// does: an update carrying an old resourceVersion gets 409 Conflict. The old
// rollback re-sent the object it had read before its first write, so on a
// real server the undo was refused, the error was ignored, and the half-made
// user stayed. That passed every test, because the fake took the stale write.
// rvServer below closes that gap: every update must carry the current
// resourceVersion, and every accepted update bumps it.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	rvNS     = "sharko"
	rvSecret = "sharko-auth"
	rvCM     = "sharko-auth-users"
)

// rvServer is a fake clientset that enforces resourceVersion on update, and
// can be told to fail chosen calls.
type rvServer struct {
	cs *fake.Clientset

	mu sync.Mutex
	// fail maps "verb resource" (for example "update secrets") to the
	// numbers of the calls of that kind that must fail (1 = the first call).
	fail map[string]map[int]error
	// calls counts calls per "verb resource".
	calls map[string]int
	// beforeUpdate, when set, runs before an update of that resource is
	// checked. Tests use it to change the object behind CreateUser's back,
	// the way another writer would.
	beforeUpdate map[string]func(n int)
	// sentHashes records every password value sent in a Secret update, so a
	// test can check no error text carries one.
	sentHashes [][]byte
}

func newRVServer(t *testing.T, accounts string, secretData map[string][]byte) *rvServer {
	t.Helper()
	if secretData == nil {
		secretData = map[string][]byte{}
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: rvCM, Namespace: rvNS, ResourceVersion: "1"},
		Data:       map[string]string{"accounts": accounts},
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: rvSecret, Namespace: rvNS, ResourceVersion: "1"},
		Data:       secretData,
	}
	srv := &rvServer{
		cs:           fake.NewSimpleClientset(cm, sec),
		fail:         map[string]map[int]error{},
		calls:        map[string]int{},
		beforeUpdate: map[string]func(int){},
	}

	// Added first, so it runs AFTER any failure reactor added later
	// (PrependReactor puts each new reactor in front).
	srv.cs.PrependReactor("update", "*", srv.enforceResourceVersion)
	srv.cs.PrependReactor("*", "*", srv.injectFailure)
	return srv
}

func (s *rvServer) failCall(verb, resource string, n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := verb + " " + resource
	if s.fail[key] == nil {
		s.fail[key] = map[int]error{}
	}
	s.fail[key][n] = err
}

func (s *rvServer) injectFailure(action k8stesting.Action) (bool, runtime.Object, error) {
	key := action.GetVerb() + " " + action.GetResource().Resource
	s.mu.Lock()
	s.calls[key]++
	n := s.calls[key]
	err := s.fail[key][n]
	if u, ok := action.(k8stesting.UpdateAction); ok {
		if sec, ok := u.GetObject().(*corev1.Secret); ok {
			for k, v := range sec.Data {
				if strings.HasSuffix(k, ".password") {
					s.sentHashes = append(s.sentHashes, append([]byte(nil), v...))
				}
			}
		}
	}
	hook := s.beforeUpdate[action.GetResource().Resource]
	s.mu.Unlock()
	// The hook runs first, so a test can change the object AND fail the
	// same call (another writer got in, then our write failed anyway).
	if hook != nil && action.GetVerb() == "update" {
		hook(n)
	}
	if err != nil {
		return true, nil, err
	}
	return false, nil, nil
}

// enforceResourceVersion does what a real API server does on update: refuse
// a stale resourceVersion with 409, and bump the version on success.
func (s *rvServer) enforceResourceVersion(action k8stesting.Action) (bool, runtime.Object, error) {
	u := action.(k8stesting.UpdateAction)
	gvr := action.GetResource()
	incoming, ok := u.GetObject().(metav1.Object)
	if !ok {
		return false, nil, nil
	}
	current, err := s.cs.Tracker().Get(gvr, action.GetNamespace(), incoming.GetName())
	if err != nil {
		return true, nil, err
	}
	cur := current.(metav1.Object)
	gr := schema.GroupResource{Group: gvr.Group, Resource: gvr.Resource}
	if rv := incoming.GetResourceVersion(); rv != "" && rv != cur.GetResourceVersion() {
		return true, nil, apierrors.NewConflict(gr, incoming.GetName(),
			errors.New("the object has been modified; please apply your changes to the latest version and try again"))
	}
	next, _ := strconv.Atoi(cur.GetResourceVersion())
	obj := u.GetObject().DeepCopyObject()
	obj.(metav1.Object).SetResourceVersion(strconv.Itoa(next + 1))
	if err := s.cs.Tracker().Update(gvr, obj, action.GetNamespace()); err != nil {
		return true, nil, err
	}
	return true, obj, nil
}

// bumpSecret changes the Secret the way another writer would: it writes a new
// key straight into the tracker and moves the resourceVersion on.
func (s *rvServer) bumpSecret(t *testing.T, key string) {
	t.Helper()
	gvr := corev1.SchemeGroupVersion.WithResource("secrets")
	obj, err := s.cs.Tracker().Get(gvr, rvNS, rvSecret)
	if err != nil {
		t.Fatalf("bumpSecret get: %v", err)
	}
	sec := obj.(*corev1.Secret)
	n, _ := strconv.Atoi(sec.ResourceVersion)
	sec.ResourceVersion = strconv.Itoa(n + 1)
	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data[key] = []byte("other-writer")
	if err := s.cs.Tracker().Update(gvr, sec, rvNS); err != nil {
		t.Fatalf("bumpSecret update: %v", err)
	}
}

// bumpConfigMap does the same for the users ConfigMap (adds another user).
func (s *rvServer) bumpConfigMap(t *testing.T, user string) {
	t.Helper()
	gvr := corev1.SchemeGroupVersion.WithResource("configmaps")
	obj, err := s.cs.Tracker().Get(gvr, rvNS, rvCM)
	if err != nil {
		t.Fatalf("bumpConfigMap get: %v", err)
	}
	cm := obj.(*corev1.ConfigMap)
	n, _ := strconv.Atoi(cm.ResourceVersion)
	cm.ResourceVersion = strconv.Itoa(n + 1)
	cm.Data["accounts"] += user + ":\n  enabled: true\n  role: viewer\n"
	if err := s.cs.Tracker().Update(gvr, cm, rvNS); err != nil {
		t.Fatalf("bumpConfigMap update: %v", err)
	}
}

func (s *rvServer) store() *Store {
	st := &Store{
		users:      make(map[string]*UserAccount),
		passHash:   make(map[string]string),
		userTokens: make(map[string]string),
		tokens:     make(map[string]*APIToken),
	}
	st.SetClientForTest(s.cs, rvNS, rvSecret)
	return st
}

func (s *rvServer) accounts(t *testing.T) string {
	t.Helper()
	cm, err := s.cs.CoreV1().ConfigMaps(rvNS).Get(context.Background(), rvCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading ConfigMap: %v", err)
	}
	return cm.Data["accounts"]
}

func (s *rvServer) secretData(t *testing.T) map[string][]byte {
	t.Helper()
	sec, err := s.cs.CoreV1().Secrets(rvNS).Get(context.Background(), rvSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading Secret: %v", err)
	}
	return sec.Data
}

// assertNoTrace checks that nothing about username is left anywhere: not in
// the ConfigMap, not in the Secret, not in memory.
func assertNoTrace(t *testing.T, srv *rvServer, st *Store, username string) {
	t.Helper()
	if accountsHasUser(srv.accounts(t), username) {
		t.Errorf("HALF-MADE USER: %q is still in the users ConfigMap: %q", username, srv.accounts(t))
	}
	if _, ok := srv.secretData(t)[username+".password"]; ok {
		t.Errorf("a password entry for %q is still in the Secret", username)
	}
	if st.GetUser(username) != nil {
		t.Errorf("%q is still in memory", username)
	}
}

// assertNoSecretInError checks the error holds no password hash that was sent.
func assertNoSecretInError(t *testing.T, srv *rvServer, err error) {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, h := range srv.sentHashes {
		if strings.Contains(err.Error(), string(h)) {
			t.Errorf("the error text holds a password hash: %q", err.Error())
		}
	}
	if strings.Contains(err.Error(), "$2a$") {
		t.Errorf("the error text holds something shaped like a bcrypt hash: %q", err.Error())
	}
}

var errBoom = errors.New("boom from the API server")

// secretKeyInvalid is the error a real API server gives for a Secret key it
// does not accept — the error from episode 1 ("dana operator.password").
func secretKeyInvalid() error {
	return apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, rvSecret, nil)
}

// TestCreateUserK8s_FailurePaths runs every place CreateUser can fail, on the
// resourceVersion-checking fake, and checks no half-made user is left.
func TestCreateUserK8s_FailurePaths(t *testing.T) {
	const existing = "alice:\n  enabled: true\n  role: admin\n"
	cases := []struct {
		name      string
		verb, res string
		n         int
		err       error
		wantInErr string
	}{
		{"first ConfigMap read fails", "get", "configmaps", 1, errBoom, "reading ConfigMap"},
		{"Secret read fails", "get", "secrets", 1, errBoom, "reading Secret"},
		{"Secret write fails (episode 1)", "update", "secrets", 1, secretKeyInvalid(), "updating Secret"},
		{"Secret write fails, plain error", "update", "secrets", 1, errBoom, "updating Secret"},
		{"second ConfigMap read fails", "get", "configmaps", 2, errBoom, "reading ConfigMap"},
		{"ConfigMap write fails", "update", "configmaps", 1, errBoom, "updating ConfigMap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newRVServer(t, existing, map[string][]byte{"alice.password": []byte("alice-hash")})
			srv.failCall(tc.verb, tc.res, tc.n, tc.err)
			st := srv.store()

			pass, err := st.CreateUser("dana", "operator")
			if err == nil {
				t.Fatalf("expected an error, got none (password %q)", pass)
			}
			if pass != "" {
				t.Errorf("a password came back with the error")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q does not say %q", err.Error(), tc.wantInErr)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("error %q does not wrap the API error", err.Error())
			}
			assertNoTrace(t, srv, st, "dana")
			assertNoSecretInError(t, srv, err)

			// Nothing else was touched.
			if got := srv.accounts(t); got != existing {
				t.Errorf("the users ConfigMap changed: %q", got)
			}
			if got := string(srv.secretData(t)["alice.password"]); got != "alice-hash" {
				t.Errorf("another user's password changed: %q", got)
			}
		})
	}
}

// TestCreateUserK8s_SecretWriteFailsWritesNoUser pins the episode 1 path
// directly: the Secret refuses the password write, and the ConfigMap must
// never have been written at all.
func TestCreateUserK8s_SecretWriteFailsWritesNoUser(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.failCall("update", "secrets", 1, secretKeyInvalid())
	st := srv.store()

	if _, err := st.CreateUser("dana", "operator"); err == nil {
		t.Fatal("expected an error")
	}
	if n := srv.calls["update configmaps"]; n != 0 {
		t.Errorf("the ConfigMap was written %d time(s) before the password was saved", n)
	}
	assertNoTrace(t, srv, st, "dana")
}

// TestCreateUserK8s_UndoReadsFresh: between the password write and the undo,
// another writer changes the Secret. The undo must read it fresh (a stale
// object gets 409 on a real server) and must keep the other writer's change.
func TestCreateUserK8s_UndoReadsFresh(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.failCall("update", "configmaps", 1, errBoom)
	srv.beforeUpdate["configmaps"] = func(int) { srv.bumpSecret(t, "someone.else") }
	st := srv.store()

	_, err := st.CreateUser("dana", "viewer")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "could not take") {
		t.Errorf("the undo failed: %v", err)
	}
	assertNoTrace(t, srv, st, "dana")
	if got := string(srv.secretData(t)["someone.else"]); got != "other-writer" {
		t.Errorf("the undo threw away another writer's change: %q", got)
	}
}

// TestCreateUserK8s_UndoRetriesOnConflict: the undo's own first write gets
// 409. It must read again and retry, not give up.
func TestCreateUserK8s_UndoRetriesOnConflict(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.failCall("update", "configmaps", 1, errBoom)
	// Secret update 1 is the password write, 2 is the undo.
	srv.failCall("update", "secrets", 2, apierrors.NewConflict(
		schema.GroupResource{Resource: "secrets"}, rvSecret, errors.New("changed")))
	st := srv.store()

	if _, err := st.CreateUser("dana", "viewer"); err == nil {
		t.Fatal("expected an error")
	} else if strings.Contains(err.Error(), "could not take") {
		t.Errorf("the undo gave up on one conflict: %v", err)
	}
	assertNoTrace(t, srv, st, "dana")
}

// TestCreateUserK8s_UndoFailureIsReported: when the undo itself fails, the
// caller must hear about it, joined with the first error, and the words must
// hold no password.
func TestCreateUserK8s_UndoFailureIsReported(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.failCall("update", "configmaps", 1, errBoom)
	undoErr := errors.New("undo refused")
	srv.failCall("update", "secrets", 2, undoErr)
	st := srv.store()

	_, err := st.CreateUser("dana", "viewer")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("the first error is missing: %v", err)
	}
	if !errors.Is(err, undoErr) {
		t.Errorf("the undo error is missing: %v", err)
	}
	const want = `could not take the password entry for user "dana" back out of the Secret, so it is still there with no user to go with it`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not say %q", err.Error(), want)
	}
	assertNoSecretInError(t, srv, err)
	// The user entry must still not be in the ConfigMap: no visible half-user.
	if accountsHasUser(srv.accounts(t), "dana") {
		t.Errorf("HALF-MADE USER in the ConfigMap: %q", srv.accounts(t))
	}
	if st.GetUser("dana") != nil {
		t.Error("dana is in memory")
	}
}

// TestCreateUserK8s_ConfigMapConflictRetries: another writer adds a user to
// the ConfigMap between our read and our write. The write gets 409, and must
// read again and keep both users.
func TestCreateUserK8s_ConfigMapConflictRetries(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "erin")
		}
	}
	st := srv.store()

	pass, err := st.CreateUser("dana", "viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !accountsHasUser(srv.accounts(t), "dana") || !accountsHasUser(srv.accounts(t), "erin") {
		t.Errorf("want both dana and erin, got %q", srv.accounts(t))
	}
	if _, ok := srv.secretData(t)["dana.password"]; !ok {
		t.Error("dana has no password in the Secret")
	}
	if !st.ValidateCredentials("dana", pass) {
		t.Error("dana cannot log in with the password she was given")
	}
}

// TestCreateUserK8s_SecretConflictRetries: the password write gets 409 once
// and must read again and retry.
func TestCreateUserK8s_SecretConflictRetries(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpSecret(t, "someone.else")
		}
	}
	st := srv.store()

	pass, err := st.CreateUser("dana", "viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if got := string(srv.secretData(t)["someone.else"]); got != "other-writer" {
		t.Errorf("the other writer's change was lost: %q", got)
	}
	if !st.ValidateCredentials("dana", pass) {
		t.Error("dana cannot log in with the password she was given")
	}
}

// TestCreateUserK8s_UserAppearsMeanwhile: the user is not in memory, but by
// the time of the ConfigMap write someone else has added them. CreateUser must
// refuse, and must put back the password that was there before, not delete it
// and not leave ours.
func TestCreateUserK8s_UserAppearsMeanwhile(t *testing.T) {
	srv := newRVServer(t, "", map[string][]byte{"dana.password": []byte("danas-real-hash")})
	// Between our password write and the ConfigMap read, dana is added.
	srv.beforeUpdate["secrets"] = func(n int) {
		if n == 1 {
			srv.bumpConfigMap(t, "dana")
		}
	}
	st := srv.store()

	_, err := st.CreateUser("dana", "viewer")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `user "dana" already exists`) {
		t.Errorf("unexpected error: %v", err)
	}
	if got := string(srv.secretData(t)["dana.password"]); got != "danas-real-hash" {
		t.Errorf("dana's real password was not put back: %q", got)
	}
}

// TestCreateUserK8s_UserAlreadyInConfigMap: memory is behind, the ConfigMap
// already has the user. Nothing may be written, in particular not the Secret
// (that would replace the user's real password).
func TestCreateUserK8s_UserAlreadyInConfigMap(t *testing.T) {
	srv := newRVServer(t, "dana:\n  enabled: true\n  role: admin\n",
		map[string][]byte{"dana.password": []byte("danas-real-hash")})
	st := srv.store()

	_, err := st.CreateUser("dana", "viewer")
	if err == nil || !strings.Contains(err.Error(), `user "dana" already exists`) {
		t.Fatalf("want already exists, got %v", err)
	}
	if n := srv.calls["update secrets"]; n != 0 {
		t.Errorf("the Secret was written %d time(s)", n)
	}
	if got := string(srv.secretData(t)["dana.password"]); got != "danas-real-hash" {
		t.Errorf("dana's password changed: %q", got)
	}
}

// TestCreateUserK8s_HappyPath: on the strict fake, a normal create works and
// the user can log in.
func TestCreateUserK8s_HappyPath(t *testing.T) {
	srv := newRVServer(t, "alice:\n  enabled: true\n  role: admin\n", nil)
	st := srv.store()
	pass, err := st.CreateUser("dana", "operator")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !accountsHasUser(srv.accounts(t), "dana") || !accountsHasUser(srv.accounts(t), "alice") {
		t.Errorf("accounts: %q", srv.accounts(t))
	}
	if !st.ValidateCredentials("dana", pass) {
		t.Error("dana cannot log in")
	}
}

// TestCreateUserK8s_AnotherCreateOverwritesPassword: two CreateUser calls for
// the same username run at once. The second one overwrites the password the
// first one wrote. When the first one's ConfigMap write fails and it tries to
// undo the password write, it must see the password is no longer its own and
// leave it alone — the second create's password must survive.
func TestCreateUserK8s_AnotherCreateOverwritesPassword(t *testing.T) {
	srv := newRVServer(t, "", nil)
	srv.failCall("update", "configmaps", 1, errBoom)
	// Between our password write and the ConfigMap write, another CreateUser for
	// the same username writes a different password into the Secret.
	srv.beforeUpdate["configmaps"] = func(n int) {
		if n == 1 {
			// Simulate the second create by directly updating the tracker like
			// bumpSecret does. Read the Secret, change dana.password to a
			// different hash, bump the resourceVersion.
			gvr := corev1.SchemeGroupVersion.WithResource("secrets")
			obj, err := srv.cs.Tracker().Get(gvr, rvNS, rvSecret)
			if err != nil {
				t.Fatalf("beforeUpdate get: %v", err)
			}
			sec := obj.(*corev1.Secret)
			n, _ := strconv.Atoi(sec.ResourceVersion)
			sec.ResourceVersion = strconv.Itoa(n + 1)
			if sec.Data == nil {
				sec.Data = map[string][]byte{}
			}
			sec.Data["dana.password"] = []byte("second-create-hash")
			if err := srv.cs.Tracker().Update(gvr, sec, rvNS); err != nil {
				t.Fatalf("beforeUpdate update: %v", err)
			}
		}
	}
	st := srv.store()

	_, err := st.CreateUser("dana", "viewer")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "could not take") {
		t.Errorf("the undo failed even though the password was changed: %v", err)
	}
	// The second create's password must still be there.
	if got := string(srv.secretData(t)["dana.password"]); got != "second-create-hash" {
		t.Errorf("the second create's password was lost, got %q", got)
	}
	// No user entry (our ConfigMap write failed).
	if accountsHasUser(srv.accounts(t), "dana") {
		t.Errorf("dana should not be in the ConfigMap: %q", srv.accounts(t))
	}
}

// TestRVServer_RefusesStaleUpdate proves the fake itself: an update carrying
// an old resourceVersion is refused with 409. Without this, the tests above
// could pass on a fake that takes anything.
func TestRVServer_RefusesStaleUpdate(t *testing.T) {
	srv := newRVServer(t, "", nil)
	ctx := context.Background()
	cms := srv.cs.CoreV1().ConfigMaps(rvNS)
	stale, err := cms.Get(ctx, rvCM, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fresh := stale.DeepCopy()
	fresh.Data["accounts"] = "x: {}\n"
	if _, err := cms.Update(ctx, fresh, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	stale.Data["accounts"] = "y: {}\n"
	_, err = cms.Update(ctx, stale, metav1.UpdateOptions{})
	if !apierrors.IsConflict(err) {
		t.Fatalf("stale update: want 409 Conflict, got %v", err)
	}
}
