package argosecrets

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testNamespace = "argocd"

func baseSpec() ClusterSecretSpec {
	return ClusterSecretSpec{
		Name:    "my-cluster",
		Server:  "https://ABC123.gr7.us-east-1.eks.amazonaws.com",
		Region:  "us-east-1",
		RoleARN: "arn:aws:iam::123456789012:role/argocd-manager",
		Labels: map[string]string{
			"addon-datadog":   "true",
			"addon-karpenter": "true",
		},
	}
}

// TestEnsure_CreatePath verifies that Ensure creates a new secret when none exists.
func TestEnsure_CreatePath(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	spec := baseSpec()

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after Ensure(): %v", err)
	}

	// Verify StringData fields.
	if secret.StringData["name"] != spec.Name {
		t.Errorf("stringData.name = %q, want %q", secret.StringData["name"], spec.Name)
	}
	if secret.StringData["server"] != spec.Server {
		t.Errorf("stringData.server = %q, want %q", secret.StringData["server"], spec.Server)
	}
	if secret.StringData["config"] == "" {
		t.Error("stringData.config is empty")
	}

	// Verify system labels.
	if secret.Labels[LabelSecretType] != "cluster" {
		t.Errorf("label %q = %q, want %q", LabelSecretType, secret.Labels[LabelSecretType], "cluster")
	}
	if secret.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("label %q = %q, want %q", LabelManagedBy, secret.Labels[LabelManagedBy], ManagedByValue)
	}

	// Verify secret type.
	if secret.Type != corev1.SecretTypeOpaque {
		t.Errorf("secret.Type = %q, want Opaque", secret.Type)
	}

	// Verify exactly one Create action was recorded.
	actions := client.Actions()
	var creates int
	for _, a := range actions {
		if a.GetVerb() == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("expected 1 create action, got %d", creates)
	}
}

// TestEnsure_UpdatePath verifies that Ensure updates a secret when labels differ.
func TestEnsure_UpdatePath(t *testing.T) {
	// Pre-populate with a secret that has stale labels.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
				// No addon labels — simulating a stale state.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("my-cluster"),
			"server": []byte("https://OLD.gr7.us-east-1.eks.amazonaws.com"),
			"config": []byte("{}"),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)
	spec := baseSpec() // desired state differs from existing

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	// Verify at least one update action was issued.
	var updates int
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			updates++
		}
	}
	if updates != 1 {
		t.Errorf("expected 1 update action, got %d", updates)
	}

	// Verify updated secret has the new server URL.
	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after update: %v", err)
	}
	if secret.StringData["server"] != spec.Server {
		t.Errorf("after update stringData.server = %q, want %q", secret.StringData["server"], spec.Server)
	}
}

// TestEnsure_SkipPath verifies that Ensure performs no update when state matches.
func TestEnsure_SkipPath(t *testing.T) {
	spec := baseSpec()

	// Build the exact desired state so hashes will match.
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}
	desiredLabels := buildLabels(spec)

	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: testNamespace,
			Labels:    desiredLabels,
		},
		Type: corev1.SecretTypeOpaque,
		// K8s returns Data, not StringData.
		Data: map[string][]byte{
			"name":   []byte(spec.Name),
			"server": []byte(spec.Server),
			"config": []byte(configJSON),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	// Verify no update was issued.
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			t.Error("expected no update action, but one was issued")
		}
	}
}

// TestBuildSecretConfig_WithRoleARN verifies the full execProviderConfig JSON structure.
func TestBuildSecretConfig_WithRoleARN(t *testing.T) {
	spec := baseSpec()
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}

	var cfg execProviderConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		t.Fatalf("invalid JSON from buildSecretConfig: %v", err)
	}

	if cfg.ExecProviderConfig.Command != "argocd-k8s-auth" {
		t.Errorf("command = %q, want argocd-k8s-auth", cfg.ExecProviderConfig.Command)
	}
	if cfg.ExecProviderConfig.APIVersion != "client.authentication.k8s.io/v1beta1" {
		t.Errorf("apiVersion = %q", cfg.ExecProviderConfig.APIVersion)
	}
	if cfg.TLSClientConfig.Insecure {
		t.Error("tlsClientConfig.insecure should be false")
	}

	// Verify args contain expected values including --role-arn.
	args := cfg.ExecProviderConfig.Args
	mustContainSequence(t, args, []string{"aws"})
	mustContainSequence(t, args, []string{"--cluster-name", spec.Name})
	mustContainSequence(t, args, []string{"--region", spec.Region})
	mustContainSequence(t, args, []string{"--role-arn", spec.RoleARN})
}

// TestBuildSecretConfig_WithoutRoleARN verifies --role-arn is omitted when RoleARN is empty.
func TestBuildSecretConfig_WithoutRoleARN(t *testing.T) {
	spec := baseSpec()
	spec.RoleARN = ""

	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}

	var cfg execProviderConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for i, arg := range cfg.ExecProviderConfig.Args {
		if arg == "--role-arn" {
			t.Errorf("--role-arn present at index %d but RoleARN was empty", i)
		}
	}
}

// TestLabelMerging verifies that addon labels are merged with system labels,
// and that system labels take precedence.
func TestLabelMerging(t *testing.T) {
	spec := baseSpec()
	// Attempt to override a system label via addon labels — should be rejected.
	spec.Labels[LabelManagedBy] = "something-else"

	labels := buildLabels(spec)

	if labels[LabelSecretType] != "cluster" {
		t.Errorf("label %q = %q, want %q", LabelSecretType, labels[LabelSecretType], "cluster")
	}
	if labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("system label %q overridden: got %q, want %q", LabelManagedBy, labels[LabelManagedBy], ManagedByValue)
	}
	// Addon labels should be present.
	if labels["addon-datadog"] != "true" {
		t.Errorf("addon label addon-datadog = %q, want %q", labels["addon-datadog"], "true")
	}
	if labels["addon-karpenter"] != "true" {
		t.Errorf("addon label addon-karpenter = %q, want %q", labels["addon-karpenter"], "true")
	}
}

// TestEnsure_CreateRecordsCorrectActions checks the fake client action list after create.
func TestEnsure_CreateRecordsCorrectActions(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), baseSpec()); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}

	actions := client.Actions()
	// Expect: get (not found) + create
	if len(actions) < 2 {
		t.Fatalf("expected at least 2 actions, got %d", len(actions))
	}
	verbs := make([]string, len(actions))
	for i, a := range actions {
		verbs[i] = a.GetVerb()
	}
	if verbs[0] != "get" {
		t.Errorf("action[0] = %q, want get", verbs[0])
	}
	if verbs[1] != "create" {
		t.Errorf("action[1] = %q, want create", verbs[1])
	}
}

// mustContainSequence checks that needle appears as a contiguous subsequence in haystack.
func mustContainSequence(t *testing.T, haystack, needle []string) {
	t.Helper()
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, v := range needle {
			if haystack[i+j] != v {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Errorf("args %v do not contain sequence %v", haystack, needle)
}

// --- Story 1.2: Adoption path tests ---

// TestEnsure_AdoptPath verifies that Ensure adopts a pre-existing secret without the managed-by label.
func TestEnsure_AdoptPath(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// No LabelManagedBy — simulating pre-Sharko secret.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("my-cluster"),
			"server": []byte("https://OLD.gr7.us-east-1.eks.amazonaws.com"),
			"config": []byte("{}"),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)
	spec := baseSpec()

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	// Verify an update (adoption) action was issued — not a create.
	var creates, updates int
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create":
			creates++
		case "update":
			updates++
		}
	}
	if creates != 0 {
		t.Errorf("expected 0 create actions, got %d", creates)
	}
	if updates != 1 {
		t.Errorf("expected 1 update action, got %d", updates)
	}

	// Verify the updated secret has the managed-by label.
	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after adoption: %v", err)
	}
	if secret.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("after adoption, label %q = %q, want %q", LabelManagedBy, secret.Labels[LabelManagedBy], ManagedByValue)
	}
	// Verify system labels take precedence.
	if secret.Labels[LabelSecretType] != "cluster" {
		t.Errorf("after adoption, label %q = %q, want cluster", LabelSecretType, secret.Labels[LabelSecretType])
	}
	// Verify addon labels from spec are present.
	if secret.StringData["addon-datadog"] != "" {
		// addon labels are on ObjectMeta.Labels, not StringData
	}
	if secret.Labels["addon-datadog"] != "true" {
		t.Errorf("after adoption, addon label addon-datadog = %q, want true", secret.Labels["addon-datadog"])
	}
	// Verify credentials are updated.
	if secret.StringData["server"] != spec.Server {
		t.Errorf("after adoption, stringData.server = %q, want %q", secret.StringData["server"], spec.Server)
	}
}

// TestEnsure_AdoptPath_AlwaysWrites verifies that adoption always writes even if data would match.
func TestEnsure_AdoptPath_AlwaysWrites(t *testing.T) {
	spec := baseSpec()

	// Build the exact desired data — same as what Ensure() would write.
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}
	desiredLabels := buildLabels(spec)

	// Pre-populate without the managed-by label but with identical data.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// No LabelManagedBy — missing managed-by triggers adoption.
				"addon-datadog":   desiredLabels["addon-datadog"],
				"addon-karpenter": desiredLabels["addon-karpenter"],
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(spec.Name),
			"server": []byte(spec.Server),
			"config": []byte(configJSON),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	// Even though data matches, adoption must always write.
	var updates int
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			updates++
		}
	}
	if updates != 1 {
		t.Errorf("expected 1 update action (adoption always writes), got %d", updates)
	}
}

// TestEnsure_ManagedSecretStillUpdates verifies that an already-managed but stale secret gets updated.
func TestEnsure_ManagedSecretStillUpdates(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
				// No addon labels — stale state.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("my-cluster"),
			"server": []byte("https://OLD.gr7.us-east-1.eks.amazonaws.com"),
			"config": []byte("{}"),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)
	spec := baseSpec()

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	var updates int
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			updates++
		}
	}
	if updates != 1 {
		t.Errorf("expected 1 update action, got %d", updates)
	}
}

// TestEnsure_ManagedSecretStillSkips verifies that an already-managed and current secret is skipped.
func TestEnsure_ManagedSecretStillSkips(t *testing.T) {
	spec := baseSpec()

	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}
	desiredLabels := buildLabels(spec)

	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: testNamespace,
			Labels:    desiredLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(spec.Name),
			"server": []byte(spec.Server),
			"config": []byte(configJSON),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			t.Error("expected no update action for up-to-date managed secret, but one was issued")
		}
	}
}

// --- Story 1.3: List and Delete tests ---

// TestList_ReturnsManagedSecrets verifies that List returns only Sharko-managed secrets.
func TestList_ReturnsManagedSecrets(t *testing.T) {
	managed1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-a",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
		},
	}
	managed2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-b",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
		},
	}
	unmanaged := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-c",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// No managed-by label.
			},
		},
	}

	client := fake.NewSimpleClientset(managed1, managed2, unmanaged)
	mgr := NewManager(client, testNamespace)

	names, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}

	if len(names) != 2 {
		t.Fatalf("expected 2 managed secrets, got %d: %v", len(names), names)
	}

	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["cluster-a"] {
		t.Error("expected cluster-a in list")
	}
	if !nameSet["cluster-b"] {
		t.Error("expected cluster-b in list")
	}
	if nameSet["cluster-c"] {
		t.Error("cluster-c (unmanaged) should not appear in list")
	}
}

// TestList_EmptyNamespace verifies that List returns an empty slice when no secrets exist.
func TestList_EmptyNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	names, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

// TestDelete_ManagedSecret verifies that Delete removes a managed secret.
func TestDelete_ManagedSecret(t *testing.T) {
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-a",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
		},
	}

	client := fake.NewSimpleClientset(managed)
	mgr := NewManager(client, testNamespace)

	if err := mgr.Delete(context.Background(), "cluster-a"); err != nil {
		t.Fatalf("Delete() returned error: %v", err)
	}

	// Verify a delete action was issued.
	var deletes int
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" {
			deletes++
		}
	}
	if deletes != 1 {
		t.Errorf("expected 1 delete action, got %d", deletes)
	}
}

// TestDelete_NotFound verifies that Delete returns nil when the secret doesn't exist (idempotent).
func TestDelete_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	if err := mgr.Delete(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("Delete() on non-existent secret should return nil, got: %v", err)
	}
}

// TestDelete_UnmanagedSecret verifies that Delete refuses to delete an unmanaged secret.
func TestDelete_UnmanagedSecret(t *testing.T) {
	unmanaged := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-c",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// No managed-by label.
			},
		},
	}

	client := fake.NewSimpleClientset(unmanaged)
	mgr := NewManager(client, testNamespace)

	err := mgr.Delete(context.Background(), "cluster-c")
	if err == nil {
		t.Fatal("Delete() should return error for unmanaged secret, got nil")
	}

	// Verify no delete action was issued.
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" {
			t.Error("Delete() issued a delete action on an unmanaged secret")
		}
	}
}

// TestDelete_VerifiesLabel verifies that Delete refuses a secret managed by a different tool.
func TestDelete_VerifiesLabel(t *testing.T) {
	helmManaged := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-d",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  "helm", // managed by Helm, not Sharko
			},
		},
	}

	client := fake.NewSimpleClientset(helmManaged)
	mgr := NewManager(client, testNamespace)

	err := mgr.Delete(context.Background(), "cluster-d")
	if err == nil {
		t.Fatal("Delete() should return error for secret managed by helm, got nil")
	}

	// Verify no delete action was issued.
	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" {
			t.Error("Delete() issued a delete action on a secret managed by another tool")
		}
	}
}

// Ensure k8stesting is imported (used indirectly via fake client assertions above).
var _ k8stesting.Action // compile-time import check
