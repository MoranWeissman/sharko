package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// V2-cleanup-89.5 / V2-cleanup-90.1 — foreign ArgoCD ownership detection.
//
// A user-managed connection's ArgoCD cluster Secret is sometimes ALSO
// rendered from Git by another ArgoCD Application — or is simply a plain
// Helm-installed secret that happens to carry Helm's own release label.
// These tests pin the tracking-id (annotation, ArgoCD's default
// trackingMethod) and app.kubernetes.io/instance (label, the other
// trackingMethod) detection paths, the hard/soft confidence split
// (V2-cleanup-90.1 — review finding H1: a label-only match is also what
// plain Helm stamps, so it must never be treated with the same confidence
// as a verified tracking-id match), and GetTrackingOwner's
// not-found/no-marker/error surfaces.

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

// ----- ParseTrackingID (V2-cleanup-90.1) --------------------------------

func TestParseTrackingID_ValidFormat(t *testing.T) {
	t.Parallel()
	appName, nsName, ok := ParseTrackingID("my-app:/Secret:argocd/prod-eu")
	if !ok {
		t.Fatal("expected ok=true for a well-formed tracking-id")
	}
	if appName != "my-app" {
		t.Errorf("appName = %q, want %q", appName, "my-app")
	}
	if nsName != "argocd/prod-eu" {
		t.Errorf("nsName = %q, want %q", nsName, "argocd/prod-eu")
	}
}

func TestParseTrackingID_GroupedKind(t *testing.T) {
	t.Parallel()
	appName, nsName, ok := ParseTrackingID("secret-renderer-app:apps/Deployment:argocd/some-deploy")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if appName != "secret-renderer-app" {
		t.Errorf("appName = %q, want %q", appName, "secret-renderer-app")
	}
	if nsName != "argocd/some-deploy" {
		t.Errorf("nsName = %q, want %q", nsName, "argocd/some-deploy")
	}
}

// TestParseTrackingID_ColonsInNsNameSegment asserts a hand-edited or
// unusual tracking-id whose namespace/name segment itself contains extra
// colons is captured whole (not truncated, not panicked on) — SplitN(_,
// ":", 3) means the third field is everything after the second colon.
func TestParseTrackingID_ColonsInNsNameSegment(t *testing.T) {
	t.Parallel()
	appName, nsName, ok := ParseTrackingID("my-app:/Secret:argocd/prod-eu:extra:colons")
	if !ok {
		t.Fatal("expected ok=true — malformed tail must not make parsing fail outright")
	}
	if appName != "my-app" {
		t.Errorf("appName = %q, want %q", appName, "my-app")
	}
	if nsName != "argocd/prod-eu:extra:colons" {
		t.Errorf("nsName = %q, want the whole remainder captured, got %q", nsName, nsName)
	}
}

func TestParseTrackingID_Empty(t *testing.T) {
	t.Parallel()
	if _, _, ok := ParseTrackingID(""); ok {
		t.Fatal("expected ok=false for an empty tracking-id")
	}
}

func TestParseTrackingID_NoColon(t *testing.T) {
	t.Parallel()
	if _, _, ok := ParseTrackingID("not-a-tracking-id"); ok {
		t.Fatal("expected ok=false for a value with no colon separator")
	}
}

// TestParseTrackingID_OnlyOneColon asserts a value with a single colon
// (missing the namespace/name segment entirely) parses to not-found —
// ArgoCD never produces a two-field tracking-id.
func TestParseTrackingID_OnlyOneColon(t *testing.T) {
	t.Parallel()
	if _, _, ok := ParseTrackingID("my-app:/Secret"); ok {
		t.Fatal("expected ok=false for a tracking-id missing its namespace/name segment")
	}
}

// TestParseTrackingID_LeadingColon asserts an empty app-name segment
// (tracking-id starting with a colon) is rejected — an app name can never
// be empty.
func TestParseTrackingID_LeadingColon(t *testing.T) {
	t.Parallel()
	if _, _, ok := ParseTrackingID(":/Secret:argocd/prod-eu"); ok {
		t.Fatal("expected ok=false for a tracking-id with an empty app-name segment")
	}
}

// ----- DetectForeignOwner confidence (V2-cleanup-90.1) -------------------

// TestDetectForeignOwner_AnnotationMethod_Hard pins the HARD-confidence
// path: the tracking-id annotation's own "<namespace>/<name>" suffix
// matches the secret being inspected, so ArgoCD really does render THIS
// secret from Git.
func TestDetectForeignOwner_AnnotationMethod_Hard(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Annotations: map[string]string{
				AnnotationTrackingID: "renderer-app:/Secret:argocd/prod-eu",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true via the tracking-id annotation")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
	if confidence != ConfidenceHard {
		t.Fatalf("confidence = %q, want %q (annotation suffix matches this secret)", confidence, ConfidenceHard)
	}
}

// TestDetectForeignOwner_AnnotationMethod_SoftOnMismatch pins the
// SOFT-confidence path when the tracking-id annotation IS present but its
// own "<namespace>/<name>" suffix does NOT match the secret being
// inspected — most likely copied or cloned from another resource, so the
// app name found is not reliably this secret's owner.
func TestDetectForeignOwner_AnnotationMethod_SoftOnMismatch(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Annotations: map[string]string{
				// Suffix names a DIFFERENT secret ("staging-eu", not "prod-eu").
				AnnotationTrackingID: "renderer-app:/Secret:argocd/staging-eu",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true — the annotation is still a signal, just a weaker one")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
	if confidence != ConfidenceSoft {
		t.Fatalf("confidence = %q, want %q (annotation suffix does not match this secret)", confidence, ConfidenceSoft)
	}
}

// TestDetectForeignOwner_LabelMethod_Soft pins detection via the
// app.kubernetes.io/instance label (trackingMethod=label) — its value IS
// the app name directly, no parsing, but this is ALWAYS soft confidence:
// a plain Helm release stamps this exact label with no ArgoCD involved at
// all (V2-cleanup-90.1, review finding H1 — the false-positive this story
// fixes).
func TestDetectForeignOwner_LabelMethod_Soft(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Labels: map[string]string{
				LabelAppInstance: "renderer-app",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true via the app.kubernetes.io/instance label")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
	if confidence != ConfidenceSoft {
		t.Fatalf("confidence = %q, want %q (label-only match — could be plain Helm)", confidence, ConfidenceSoft)
	}
}

// TestDetectForeignOwner_AnnotationWinsOverLabel — when BOTH markers are
// present (unusual, but not impossible under a mixed-history ArgoCD
// config), the annotation (the default trackingMethod) takes precedence,
// and its own confidence rule (hard/soft on suffix match) still applies.
func TestDetectForeignOwner_AnnotationWinsOverLabel(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Annotations: map[string]string{
				AnnotationTrackingID: "annotation-app:/Secret:argocd/prod-eu",
			},
			Labels: map[string]string{
				LabelAppInstance: "label-app",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "annotation-app" {
		t.Fatalf("name = %q, want %q (annotation should win)", name, "annotation-app")
	}
	if confidence != ConfidenceHard {
		t.Fatalf("confidence = %q, want %q", confidence, ConfidenceHard)
	}
}

// TestDetectForeignOwner_MalformedAnnotation_FallsBackToLabel — a tracking
// ID missing its namespace/name segment fails to parse (ParseTrackingID
// returns ok=false), so DetectForeignOwner must fall through to the label
// check rather than reporting found=false outright.
func TestDetectForeignOwner_MalformedAnnotation_FallsBackToLabel(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Annotations: map[string]string{
				AnnotationTrackingID: "not-a-tracking-id",
			},
			Labels: map[string]string{
				LabelAppInstance: "label-app",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true via the label fallback")
	}
	if name != "label-app" {
		t.Fatalf("name = %q, want %q", name, "label-app")
	}
	if confidence != ConfidenceSoft {
		t.Fatalf("confidence = %q, want %q", confidence, ConfidenceSoft)
	}
}

// TestDetectForeignOwner_ColonsInNsNameSegment_StillDetectedAsSoft —
// DetectForeignOwner must not panic or drop the marker when the annotation
// value has more than two colons; ParseTrackingID captures the tail
// intact, which then simply fails the exact-suffix match (soft), never a
// crash.
func TestDetectForeignOwner_ColonsInNsNameSegment_StillDetectedAsSoft(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argocd",
			Name:      "prod-eu",
			Annotations: map[string]string{
				AnnotationTrackingID: "renderer-app:/Secret:argocd/prod-eu:unexpected:extra",
			},
		},
	}
	name, confidence, found := DetectForeignOwner(secret)
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "renderer-app" {
		t.Fatalf("name = %q, want %q", name, "renderer-app")
	}
	if confidence != ConfidenceSoft {
		t.Fatalf("confidence = %q, want %q (mangled suffix can never exact-match)", confidence, ConfidenceSoft)
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
	if _, _, found := DetectForeignOwner(secret); found {
		t.Fatal("expected found=false for a Secret with no tracking markers")
	}
	if _, _, found := DetectForeignOwner(nil); found {
		t.Fatal("expected found=false (not a panic) for a nil secret")
	}
}

// ----- GetTrackingOwner --------------------------------------------------

// TestGetTrackingOwner_SecretNotFound — a missing Secret is NOT an error;
// it's the common case for a self-managed connection whose user hasn't
// created their Secret yet.
func TestGetTrackingOwner_SecretNotFound(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	name, _, found, err := mgr.GetTrackingOwner(context.Background(), "no-such-cluster")
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

	name, _, found, err := mgr.GetTrackingOwner(context.Background(), "user-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if found {
		t.Fatalf("found = true (name=%q), want false", name)
	}
}

// TestGetTrackingOwner_ForeignMarkerFound_Hard — the end-to-end happy
// path: GetTrackingOwner surfaces the owning app name AND hard confidence
// from a live Secret whose tracking-id annotation suffix matches its own
// namespace/name.
func TestGetTrackingOwner_ForeignMarkerFound_Hard(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Annotations: map[string]string{
				AnnotationTrackingID: "cluster-secrets-app:/Secret:" + testNamespace + "/user-cluster",
			},
		},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	name, confidence, found, err := mgr.GetTrackingOwner(context.Background(), "user-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "cluster-secrets-app" {
		t.Fatalf("name = %q, want %q", name, "cluster-secrets-app")
	}
	if confidence != ConfidenceHard {
		t.Fatalf("confidence = %q, want %q", confidence, ConfidenceHard)
	}
}

// TestGetTrackingOwner_ForeignMarkerFound_Soft — a live Secret whose
// tracking-id annotation names a DIFFERENT namespace/name (a copied
// annotation) surfaces soft confidence, not hard.
func TestGetTrackingOwner_ForeignMarkerFound_Soft(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Annotations: map[string]string{
				AnnotationTrackingID: "cluster-secrets-app:/Secret:" + testNamespace + "/some-other-cluster",
			},
		},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	name, confidence, found, err := mgr.GetTrackingOwner(context.Background(), "user-cluster")
	if err != nil {
		t.Fatalf("GetTrackingOwner() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if name != "cluster-secrets-app" {
		t.Fatalf("name = %q, want %q", name, "cluster-secrets-app")
	}
	if confidence != ConfidenceSoft {
		t.Fatalf("confidence = %q, want %q", confidence, ConfidenceSoft)
	}
}

// ----- GetSecretOwnership (V2-cleanup-90.1 — single-Get doctor helper) ---

// TestGetSecretOwnership_NotFound mirrors GetTrackingOwner's not-found
// convention: missing Secret is found=false, err=nil.
func TestGetSecretOwnership_NotFound(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	ownership, found, err := mgr.GetSecretOwnership(context.Background(), "no-such-cluster")
	if err != nil {
		t.Fatalf("GetSecretOwnership() error = %v, want nil", err)
	}
	if found {
		t.Fatalf("found = true, want false for a missing secret (ownership=%+v)", ownership)
	}
}

// TestGetSecretOwnership_SingleGet asserts GetSecretOwnership issues
// EXACTLY ONE Get against the K8s API — the whole point of the method is
// to replace the doctor's previous GetManagedByLabel + GetTrackingOwner
// double-Get pattern with one read.
func TestGetSecretOwnership_SingleGet(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Labels:    map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{
				AnnotationTrackingID: "cluster-secrets-app:/Secret:" + testNamespace + "/user-cluster",
			},
		},
	}
	client := fake.NewSimpleClientset(existing)
	getCount := 0
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		getCount++
		return false, nil, nil
	})
	mgr := NewManager(client, testNamespace)

	if _, _, err := mgr.GetSecretOwnership(context.Background(), "user-cluster"); err != nil {
		t.Fatalf("GetSecretOwnership() error = %v, want nil", err)
	}
	if getCount != 1 {
		t.Fatalf("Get call count = %d, want exactly 1", getCount)
	}
}

// TestGetSecretOwnership_CombinesManagedByAndForeignOwner — a single Get
// must yield BOTH the managed-by label and the foreign-owner signal from
// the same object, matching what GetManagedByLabel + GetTrackingOwner
// would each report separately.
func TestGetSecretOwnership_CombinesManagedByAndForeignOwner(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: testNamespace,
			Labels:    map[string]string{LabelAppInstance: "helm-or-argo-release"},
		},
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	ownership, found, err := mgr.GetSecretOwnership(context.Background(), "byo-conn")
	if err != nil {
		t.Fatalf("GetSecretOwnership() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if ownership.ManagedBy != "" {
		t.Errorf("ManagedBy = %q, want empty (self-managed secret)", ownership.ManagedBy)
	}
	if !ownership.ForeignOwnerFound {
		t.Fatal("expected ForeignOwnerFound=true")
	}
	if ownership.ForeignOwnerAppName != "helm-or-argo-release" {
		t.Errorf("ForeignOwnerAppName = %q, want %q", ownership.ForeignOwnerAppName, "helm-or-argo-release")
	}
	if ownership.ForeignOwnerConfidence != ConfidenceSoft {
		t.Errorf("ForeignOwnerConfidence = %q, want %q (label-only match)", ownership.ForeignOwnerConfidence, ConfidenceSoft)
	}
}
