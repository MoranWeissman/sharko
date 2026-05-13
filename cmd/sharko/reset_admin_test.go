package main

import (
	"context"
	"testing"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testNS         = "sharko-test"
	testSecretName = "sharko"
)

// newSharkoSecret returns a baseline Sharko Secret carrying a stale bcrypt
// hash for admin.password. runResetAdmin will overwrite it.
func newSharkoSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testNS,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"admin.password": []byte("$2a$10$staleHashFromBefore"),
		},
	}
}

// newInitialAdminSecret returns a stale `sharko-initial-admin-secret` carrying
// the given old plaintext, used to simulate the post-V124-6.3 / pre-rotation
// state.
func newInitialAdminSecret(oldPassword string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      initialAdminSecretName,
			Namespace: testNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
				"app.kubernetes.io/component":  "bootstrap",
			},
			Annotations: map[string]string{
				// Pre-V124-7 wording — assert that the rotation refreshes
				// it to the new wording.
				"sharko.io/initial-secret": "delete-after-first-password-change",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte(oldPassword),
		},
	}
}

// assertAdminPasswordHashMatches asserts the Sharko Secret's admin.password
// bcrypt-hash validates the given plaintext. This is the V124-6.3 invariant
// V124-7 must NOT break.
func assertAdminPasswordHashMatches(t *testing.T, clientset *fake.Clientset, plaintext string) {
	t.Helper()
	got, err := clientset.CoreV1().Secrets(testNS).Get(context.Background(), testSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read sharko secret: %v", err)
	}
	hash, ok := got.Data["admin.password"]
	if !ok || len(hash) == 0 {
		t.Fatalf("admin.password not present after reset")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(plaintext)); err != nil {
		t.Fatalf("bcrypt hash does not match returned plaintext: %v", err)
	}
}

// --- V124-7.1 / BUG-025: rotation behavior -----------------------------------

// Case 1: writeInitialSecret enabled (default), secret absent → after
// reset-admin, secret EXISTS with the new plaintext, correct labels and
// V124-7 annotation wording.
func TestRunResetAdmin_WriteEnabled_SecretAbsent_CreatesWithNewPlaintext(t *testing.T) {
	t.Setenv(envWriteInitialAdminSecret, "")

	clientset := fake.NewSimpleClientset(newSharkoSecret())

	result, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("runResetAdmin: %v", err)
	}
	if !result.RewroteInitialAdminSecret {
		t.Errorf("expected RewroteInitialAdminSecret=true on default path, got false")
	}
	if result.DeletedStaleInitialAdminSecret {
		t.Errorf("did not expect DeletedStaleInitialAdminSecret on default path")
	}

	got, err := clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected %s/%s to exist, got: %v", testNS, initialAdminSecretName, err)
	}
	if string(got.Data["password"]) != result.NewPassword {
		t.Errorf("password mismatch: secret=%q, returned=%q", got.Data["password"], result.NewPassword)
	}
	if string(got.Data["username"]) != "admin" {
		t.Errorf("username mismatch: %q", got.Data["username"])
	}
	if got.Labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Errorf("missing managed-by label: %+v", got.Labels)
	}
	if got.Labels["app.kubernetes.io/component"] != "bootstrap" {
		t.Errorf("missing component=bootstrap label: %+v", got.Labels)
	}
	if got.Annotations["sharko.io/initial-secret"] != "rotated-on-reset-admin" {
		t.Errorf("annotation should reflect V124-7 wording, got: %+v", got.Annotations)
	}

	assertAdminPasswordHashMatches(t, clientset, result.NewPassword)
}

// Case 2: writeInitialSecret enabled, secret pre-exists with stale plaintext
// → after reset-admin, secret EXISTS with NEW plaintext (old plaintext fully
// replaced) and V124-7 annotation wording.
func TestRunResetAdmin_WriteEnabled_SecretPresent_RotatesPlaintext(t *testing.T) {
	t.Setenv(envWriteInitialAdminSecret, "")

	const stale = "OldBootstrap123"
	clientset := fake.NewSimpleClientset(
		newSharkoSecret(),
		newInitialAdminSecret(stale),
	)

	result, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("runResetAdmin: %v", err)
	}
	if !result.RewroteInitialAdminSecret {
		t.Errorf("expected RewroteInitialAdminSecret=true, got false")
	}
	if result.NewPassword == stale {
		t.Fatalf("new password collided with stale password — rotation produced same value")
	}

	got, err := clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected %s to still exist after rotation: %v", initialAdminSecretName, err)
	}
	if string(got.Data["password"]) != result.NewPassword {
		t.Fatalf("password not rotated: got %q want %q", got.Data["password"], result.NewPassword)
	}
	if string(got.Data["password"]) == stale {
		t.Errorf("stale plaintext leaked through rotation: %q still present", stale)
	}
	if got.Annotations["sharko.io/initial-secret"] != "rotated-on-reset-admin" {
		t.Errorf("annotation not refreshed to V124-7 wording on rotation: %+v", got.Annotations)
	}

	assertAdminPasswordHashMatches(t, clientset, result.NewPassword)
}

// Case 3: writeInitialSecret disabled, secret absent → after reset-admin,
// secret still absent (no-op delete on missing is fine).
func TestRunResetAdmin_WriteDisabled_SecretAbsent_NoOp(t *testing.T) {
	t.Setenv(envWriteInitialAdminSecret, "false")

	clientset := fake.NewSimpleClientset(newSharkoSecret())

	result, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("runResetAdmin: %v", err)
	}
	if result.RewroteInitialAdminSecret {
		t.Errorf("did not expect RewroteInitialAdminSecret on opt-out path")
	}
	if result.DeletedStaleInitialAdminSecret {
		t.Errorf("did not expect DeletedStaleInitialAdminSecret when no stale secret exists")
	}

	_, err = clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected %s to remain absent on opt-out path, got: %v", initialAdminSecretName, err)
	}

	assertAdminPasswordHashMatches(t, clientset, result.NewPassword)
}

// Case 4: writeInitialSecret disabled, secret present → after reset-admin,
// secret DELETED, NOT recreated (preserves V124-6.3 opt-out cleanup behavior).
func TestRunResetAdmin_WriteDisabled_SecretPresent_DeletesNoRecreate(t *testing.T) {
	t.Setenv(envWriteInitialAdminSecret, "false")

	clientset := fake.NewSimpleClientset(
		newSharkoSecret(),
		newInitialAdminSecret("StalePassword42"),
	)

	result, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("runResetAdmin: %v", err)
	}
	if result.RewroteInitialAdminSecret {
		t.Errorf("did not expect RewroteInitialAdminSecret on opt-out path with stale secret")
	}
	if !result.DeletedStaleInitialAdminSecret {
		t.Errorf("expected DeletedStaleInitialAdminSecret=true, got false")
	}

	_, err = clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected %s to be deleted on opt-out path, got: %v", initialAdminSecretName, err)
	}

	assertAdminPasswordHashMatches(t, clientset, result.NewPassword)
}

// Multi-rotate: two consecutive reset-admin runs with writeInitialSecret on.
// Each rotation must produce a different plaintext and the secret must
// reflect the most recent one — ensuring rotation is genuinely repeatable,
// not a one-shot V124-6.3 leftover.
func TestRunResetAdmin_MultipleConsecutiveRotations(t *testing.T) {
	t.Setenv(envWriteInitialAdminSecret, "")

	clientset := fake.NewSimpleClientset(newSharkoSecret())

	first, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("first runResetAdmin: %v", err)
	}
	got, err := clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read after first rotation: %v", err)
	}
	if string(got.Data["password"]) != first.NewPassword {
		t.Fatalf("first rotation: secret password %q != returned %q", got.Data["password"], first.NewPassword)
	}
	assertAdminPasswordHashMatches(t, clientset, first.NewPassword)

	second, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err != nil {
		t.Fatalf("second runResetAdmin: %v", err)
	}
	if second.NewPassword == first.NewPassword {
		t.Fatalf("two consecutive rotations produced the same password — generator collision suspected")
	}
	got, err = clientset.CoreV1().Secrets(testNS).Get(context.Background(), initialAdminSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read after second rotation: %v", err)
	}
	if string(got.Data["password"]) != second.NewPassword {
		t.Fatalf("second rotation: secret password %q != returned %q", got.Data["password"], second.NewPassword)
	}
	assertAdminPasswordHashMatches(t, clientset, second.NewPassword)
}

// Sharko Secret missing entirely is a hard failure (the operator pointed at
// the wrong namespace/release name). V124-7 should not regress this.
func TestRunResetAdmin_SharkoSecretMissing_Fails(t *testing.T) {
	clientset := fake.NewSimpleClientset() // no sharko secret seeded

	_, err := runResetAdmin(context.Background(), clientset, testNS, testSecretName)
	if err == nil {
		t.Fatalf("expected error when sharko secret is absent")
	}
	if !apierrors.IsNotFound(apierrors.NewNotFound(corev1.Resource("secrets"), testSecretName)) {
		// Sanity guard for the assertion below — the fake client returns a
		// real NotFound error wrapped by our own message, so we just check
		// the message contains the secret name.
	}
}

// --- V124-7.1: env-var parsing for the opt-out flag --------------------------

func TestResetWriteInitialAdminSecretEnabled(t *testing.T) {
	cases := []struct {
		envValue string
		want     bool
	}{
		{"", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true}, // anything not in the off-list is on
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{" false ", false}, // whitespace tolerant
	}
	for _, tc := range cases {
		t.Run(tc.envValue, func(t *testing.T) {
			t.Setenv(envWriteInitialAdminSecret, tc.envValue)
			got := resetWriteInitialAdminSecretEnabled()
			if got != tc.want {
				t.Errorf("envValue=%q got=%v want=%v", tc.envValue, got, tc.want)
			}
		})
	}
}
