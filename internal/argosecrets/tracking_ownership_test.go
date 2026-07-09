package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// V2-cleanup-89.5 — foreign ArgoCD ownership detection.
//
// A user-managed connection's ArgoCD cluster Secret is sometimes ALSO
// rendered from Git by another ArgoCD Application. These tests pin the
// tracking-id (annotation, ArgoCD's default trackingMethod) and
// app.kubernetes.io/instance (label, the other trackingMethod) detection
// paths, and GetTrackingOwner's not-found/no-marker/error surfaces.

// TestParseTrackingAppName_ValidFormat pins the "<app-name>:<group>/<kind>:
// <namespace>/<name>" parse — the app name is everything before the FIRST
// colon.
func TestParseTrackingAppName_ValidFormat(t *testing.T) {
	t.Parallel()
	name, ok := ParseTrackingAppName("my-app:/Secret:argocd/prod-eu")
	if !ok {
		t.Fatal("expected ok=true for a well-formed tracking-id")
	}
	if name != "my-app" {
		t.Fatalf("name = %q, want %q", name, "my-app")
	}
}

// TestParseTrackingAppName_GroupedKind pins parsing when the resource has a
// non-empty API group (e.g. "apps/Deployment") — the app name must still
// be exactly the segment before the first colon, not split further.
func TestParseTrackingAppName_GroupedKind(t *testing.T) {
	t.Parallel()
	name, ok := ParseTrackingAppName("secret-renderer-app:apps/Deployment:argocd/some-deploy")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if name != "secret-renderer-app" {
		t.Fatalf("name = %q, want %q", name, "secret-renderer-app")
	}
}

// TestParseTrackingAppName_Empty asserts an empty tracking-id parses to
// not-found rather than an empty app name.
func TestParseTrackingAppName_Empty(t *testing.T) {
	t.Parallel()
	if _, ok := ParseTrackingAppName(""); ok {
		t.Fatal("expected ok=false for an empty tracking-id")
	}
}

// TestParseTrackingAppName_NoColon asserts a value ArgoCD would never
// produce (no colon at all) parses to not-found instead of returning the
// whole string as the app name.
func TestParseTrackingAppName_NoColon(t *testing.T) {
	t.Parallel()
	if _, ok := ParseTrackingAppName("not-a-tracking-id"); ok {
		t.Fatal("expected ok=false for a value with no colon separator")
	}
}

// TestDetectForeignOwner_AnnotationMethod pins detection via the
// tracking-id annotation — ArgoCD's default trackingMethod.
func TestDetectForeignOwner_AnnotationMethod(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationTrackingID: "renderer-app:/Secret:argocd/prod-eu",
			},
		},
	}
	name, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true via the tracking-id annotation")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
}

// TestDetectForeignOwner_LabelMethod pins detection via the
// app.kubernetes.io/instance label (trackingMethod=label) — its value IS
// the app name directly, no parsing.
func TestDetectForeignOwner_LabelMethod(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				LabelAppInstance: "renderer-app",
			},
		},
	}
	name, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true via the app.kubernetes.io/instance label")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
}

// TestDetectForeignOwner_AnnotationWinsOverLabel — when BOTH markers are
// present (unusual, but not impossible under a mixed-history ArgoCD
// config), the annotation (the default trackingMethod) takes precedence.
func TestDetectForeignOwner_AnnotationWinsOverLabel(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationTrackingID: "annotation-app:/Secret:argocd/prod-eu",
			},
			Labels: map[string]string{
				LabelAppInstance: "label-app",
			},
		},
	}
	name, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "annotation-app" {
		t.Fatalf("name = %q, want %q (annotation should win)", name, "annotation-app")
	}
}

// TestDetectForeignOwner_NoMarkers asserts an ordinary Secret with neither
// marker reports not-found, and that DetectForeignOwner is nil-safe.
func TestDetectForeignOwner_NoMarkers(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"argocd.argoproj.io/secret-type": "cluster"},
		},
	}
	if _, found := DetectForeignOwner(secret); found {
		t.Fatal("expected found=false for a Secret with no tracking markers")
	}
	if _, found := DetectForeignOwner(nil); found {
		t.Fatal("expected found=false (not a panic) for a nil secret")
	}
}

// TestGetTrackingOwner_SecretNotFound — a missing Secret is NOT an error;
// it's the common case for a self-managed connection whose user hasn't
// created their Secret yet.
func TestGetTrackingOwner_SecretNotFound(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	name, found, err := mgr.GetTrackingOwner(context.Background(), "no-such-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if found {
		t.Fatalf("found = true (name=%q), want false for a missing secret", name)
	}
}

// TestGetTrackingOwner_NoMarker — an existing Secret with no tracking
// marker reports found=false, no error.
func TestGetTrackingOwner_NoMarker(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Labels:    map[string]string{"argocd.argoproj.io/secret-type": "cluster"},
		},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	name, found, err := mgr.GetTrackingOwner(context.Background(), "user-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if found {
		t.Fatalf("found = true (name=%q), want false", name)
	}
}

// TestGetTrackingOwner_ForeignMarkerFound — the end-to-end happy path:
// GetTrackingOwner surfaces the owning app name from a live Secret's
// tracking-id annotation.
func TestGetTrackingOwner_ForeignMarkerFound(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Annotations: map[string]string{
				AnnotationTrackingID: "cluster-secrets-app:/Secret:argocd/user-cluster",
			},
		},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	name, found, err := mgr.GetTrackingOwner(context.Background(), "user-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "cluster-secrets-app" {
		t.Fatalf("name = %q, want %q", name, "cluster-secrets-app")
	}
}
