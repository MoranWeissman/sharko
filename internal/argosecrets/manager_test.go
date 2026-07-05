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
		// base64("fake-ca-cert") — a realistic stand-in for a PEM CA certificate.
		CAData: "ZmFrZS1jYS1jZXJ0",
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

// TestEnsure_BearerTokenPath is the V2-cleanup-8.2 regression test.
//
// Kubeconfig-registered clusters carry a static bearer token. The Secret
// written for them MUST use ArgoCD's bearerToken config shape
// ({"bearerToken": ..., "tlsClientConfig": {...}}) — that is the only shape
// providers.ArgoCDProvider.GetCredentials can read back, and the only way the
// cluster becomes Reachable. Before this fix the only Secret writer was the
// reconciler, which always emitted the execProviderConfig (EKS) shape that
// GetCredentials rejects — so a kubeconfig cluster stayed Unreachable forever.
//
// The struct used here mirrors providers.argoCDClusterConfig field-for-field
// (it is unexported, so it cannot be imported) — if GetCredentials's parser
// changes, this assertion still pins the wire contract Ensure must satisfy.
func TestEnsure_BearerTokenPath(t *testing.T) {
	// Mirror of the config shape providers.ArgoCDProvider.GetCredentials parses.
	type tlsClientConfig struct {
		Insecure bool   `json:"insecure"`
		CAData   string `json:"caData"`
	}
	type argoConfig struct {
		BearerToken        string           `json:"bearerToken"`
		ExecProviderConfig *json.RawMessage `json:"execProviderConfig"`
		TLSClientConfig    tlsClientConfig  `json:"tlsClientConfig"`
	}

	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	spec := ClusterSecretSpec{
		Name:   "kind-sharko",
		Server: "https://127.0.0.1:60123",
		Token:  "ya29.example-bearer-token",
		// base64("fake-ca-bytes")
		CAData: "ZmFrZS1jYS1ieXRlcw==",
		Labels: map[string]string{"monitoring": "true"},
	}

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Error("Ensure() reported no change on create")
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after Ensure(): %v", err)
	}

	// Name / server fields must match what findClusterSecret looks up by
	// (data["name"]) and what GetCredentials reads as the server URL.
	if secret.StringData["name"] != spec.Name {
		t.Errorf("stringData.name = %q, want %q", secret.StringData["name"], spec.Name)
	}
	if secret.StringData["server"] != spec.Server {
		t.Errorf("stringData.server = %q, want %q", secret.StringData["server"], spec.Server)
	}

	// Labels: addon label + system labels, exactly what the reconciler emits.
	if secret.Labels[LabelSecretType] != "cluster" {
		t.Errorf("label %q = %q, want cluster", LabelSecretType, secret.Labels[LabelSecretType])
	}
	if secret.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("label %q = %q, want %q", LabelManagedBy, secret.Labels[LabelManagedBy], ManagedByValue)
	}
	if secret.Labels["monitoring"] != "true" {
		t.Errorf("addon label monitoring = %q, want true", secret.Labels["monitoring"])
	}

	// The crux: config must be the bearerToken shape, NOT execProviderConfig,
	// so GetCredentials accepts it.
	var cfg argoConfig
	if err := json.Unmarshal([]byte(secret.StringData["config"]), &cfg); err != nil {
		t.Fatalf("config JSON did not parse as bearerToken shape: %v\nconfig=%s", err, secret.StringData["config"])
	}
	if cfg.BearerToken != spec.Token {
		t.Errorf("config.bearerToken = %q, want %q", cfg.BearerToken, spec.Token)
	}
	if cfg.ExecProviderConfig != nil {
		t.Errorf("config must NOT contain execProviderConfig for a token cluster; got %s", string(*cfg.ExecProviderConfig))
	}
	if cfg.TLSClientConfig.CAData != spec.CAData {
		t.Errorf("config.tlsClientConfig.caData = %q, want %q", cfg.TLSClientConfig.CAData, spec.CAData)
	}
	if cfg.TLSClientConfig.Insecure {
		t.Error("config.tlsClientConfig.insecure = true, want false when CAData is present")
	}
}

// TestEnsure_BearerTokenPath_InsecureNoCA verifies the no-CA fallback emits
// insecure:true, matching ArgoCDProvider.buildBearerTokenKubeconfig.
func TestEnsure_BearerTokenPath_InsecureNoCA(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	spec := ClusterSecretSpec{
		Name:   "no-ca-cluster",
		Server: "https://10.0.0.1:6443",
		Token:  "tok",
		// CAData intentionally empty.
	}
	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	var cfg struct {
		BearerToken     string `json:"bearerToken"`
		TLSClientConfig struct {
			Insecure bool   `json:"insecure"`
			CAData   string `json:"caData"`
		} `json:"tlsClientConfig"`
	}
	if err := json.Unmarshal([]byte(secret.StringData["config"]), &cfg); err != nil {
		t.Fatalf("config parse: %v", err)
	}
	if !cfg.TLSClientConfig.Insecure {
		t.Error("expected insecure:true when no CAData")
	}
	if cfg.TLSClientConfig.CAData != "" {
		t.Errorf("expected empty caData, got %q", cfg.TLSClientConfig.CAData)
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
	if cfg.TLSClientConfig.CAData != spec.CAData {
		t.Errorf("tlsClientConfig.caData = %q, want %q", cfg.TLSClientConfig.CAData, spec.CAData)
	}

	// Verify args contain expected values including --role-arn.
	// --region must NOT be in args: argocd-k8s-auth doesn't support it.
	args := cfg.ExecProviderConfig.Args
	mustContainSequence(t, args, []string{"aws"})
	mustContainSequence(t, args, []string{"--cluster-name", spec.Name})
	mustContainSequence(t, args, []string{"--role-arn", spec.RoleARN})
	for i, arg := range args {
		if arg == "--region" {
			t.Errorf("--region present at index %d in args; argocd-k8s-auth does not support --region", i)
		}
	}
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
		if arg == "--region" {
			t.Errorf("--region present at index %d in args; argocd-k8s-auth does not support --region", i)
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

// --- Story 1.2: Adoption path tests (updated for V2-cleanup-28 guest semantics) ---

// TestEnsure_AdoptPath verifies that Ensure adopts a pre-existing secret: applies labels
// and the adopted annotation, but PRESERVES the existing connection Data byte-for-byte.
func TestEnsure_AdoptPath(t *testing.T) {
	foreignData := map[string][]byte{
		"name":              []byte("my-cluster"),
		"server":            []byte("https://OLD.gr7.us-east-1.eks.amazonaws.com"),
		"config":            []byte(`{"foreign":"config","tls-extras":"preserved"}`),
		"extra-foreign-key": []byte("should-survive"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType:  "cluster",
				"foreign-label":  "kept",
				// No LabelManagedBy — simulating pre-Sharko secret.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
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
	// Verify system labels are present.
	if secret.Labels[LabelSecretType] != "cluster" {
		t.Errorf("after adoption, label %q = %q, want cluster", LabelSecretType, secret.Labels[LabelSecretType])
	}
	// Verify addon labels from spec are present.
	if secret.Labels["addon-datadog"] != "true" {
		t.Errorf("after adoption, addon label addon-datadog = %q, want true", secret.Labels["addon-datadog"])
	}
	// Verify foreign label is KEPT (guest semantics).
	if secret.Labels["foreign-label"] != "kept" {
		t.Errorf("after adoption, foreign-label = %q, want kept", secret.Labels["foreign-label"])
	}
	// Verify adopted annotation is stamped.
	if secret.Annotations[AnnotationAdopted] != "true" {
		t.Errorf("after adoption, annotation %q = %q, want true", AnnotationAdopted, secret.Annotations[AnnotationAdopted])
	}
	// CRITICAL: verify connection Data is byte-for-byte identical — not replaced with Sharko template.
	for k, want := range foreignData {
		if got := string(secret.Data[k]); got != string(want) {
			t.Errorf("after adoption, Data[%q] = %q, want %q (connection data must be preserved)", k, got, string(want))
		}
	}
	// StringData must NOT have been replaced (that would overwrite connection config).
	if secret.StringData["server"] != "" && secret.StringData["server"] != spec.Server {
		// The fake clientset may or may not move StringData→Data; what matters is
		// Data is not overwritten with Sharko's template values.
		t.Errorf("StringData[server] unexpectedly set to %q on adopted path", secret.StringData["server"])
	}
}

// TestEnsure_AdoptPath_AlwaysWrites verifies that adoption always writes even if labels already match.
func TestEnsure_AdoptPath_AlwaysWrites(t *testing.T) {
	spec := baseSpec()
	desiredLabels := buildLabels(spec)

	// Pre-populate without the managed-by label but with identical labels otherwise.
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
			"config": []byte("{}"),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	// Even though some labels match, adoption must always write
	// (managed-by label and adopted annotation still need to be applied).
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

// TestEnsure_AdoptedSteadyState_LabelsConverge verifies that on a secret that is
// already adopted (has adopted annotation + managed-by label), Ensure converges
// only the labels — it never touches Data/StringData (Story 28.2).
func TestEnsure_AdoptedSteadyState_LabelsConverge(t *testing.T) {
	spec := baseSpec()
	desiredLabels := buildLabels(spec)

	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"my-token","tlsClientConfig":{"insecure":false}}`),
		"server": []byte("https://original-server.example.com"),
		"name":   []byte(spec.Name),
	}

	// Pre-existing adopted secret with stale addon labels.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
				// No addon labels yet (stale).
				"foreign-extra": "preserved",
			},
			Annotations: map[string]string{
				AnnotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Error("Ensure() should report changed=true when labels diverged")
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}

	// Desired labels must be present.
	for k, want := range desiredLabels {
		if got := secret.Labels[k]; got != want {
			t.Errorf("after label convergence, Labels[%q] = %q, want %q", k, got, want)
		}
	}
	// Foreign label must be preserved (guest semantics).
	if secret.Labels["foreign-extra"] != "preserved" {
		t.Errorf("foreign-extra label lost after label convergence; labels=%v", secret.Labels)
	}
	// Data must be byte-for-byte untouched.
	for k, want := range foreignData {
		if got := string(secret.Data[k]); got != string(want) {
			t.Errorf("Data[%q] = %q, want %q (must be untouched on adopted secret)", k, got, string(want))
		}
	}
}

// TestEnsure_AdoptedSteadyState_Idempotent verifies that when labels already match
// on an adopted secret, Ensure performs no write (idempotent) — Story 28.2.
func TestEnsure_AdoptedSteadyState_Idempotent(t *testing.T) {
	spec := baseSpec()
	desiredLabels := buildLabels(spec)

	// Clone desired labels + add a foreign label to simulate real state.
	existingLabels := make(map[string]string, len(desiredLabels)+1)
	for k, v := range desiredLabels {
		existingLabels[k] = v
	}
	existingLabels["foreign-extra"] = "preserved"

	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: testNamespace,
			Labels:    existingLabels,
			Annotations: map[string]string{
				AnnotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"config": []byte(`{"bearerToken":"tok"}`),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	// When the desired labels are already a subset of existing labels,
	// no update should be issued (idempotent).
	if changed {
		t.Error("Ensure() reported changed=true on an already-converged adopted secret (expected idempotent skip)")
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "update" {
			t.Error("Ensure() issued an update action on an already-converged adopted secret")
		}
	}
}

// TestEnsure_CreatedSecret_UnchangedPath verifies that a Sharko-CREATED secret
// (managed-by label, NO adopted annotation) still goes through the full
// hash-compare + data-rewrite path (regression guard for Story 28.2).
func TestEnsure_CreatedSecret_UnchangedPath(t *testing.T) {
	// Pre-existing Sharko-created secret with stale data.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
			// No AnnotationAdopted — this is a Sharko-CREATED secret.
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("my-cluster"),
			"server": []byte("https://STALE.gr7.us-east-1.eks.amazonaws.com"),
			"config": []byte("{}"),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)
	spec := baseSpec() // desired state differs

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Error("Ensure() should report changed=true when a Sharko-created secret is stale")
	}

	// Verify the secret was updated with new server URL (not the old stale one).
	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	// The update path writes via StringData; fake clientset keeps it in StringData.
	if secret.StringData["server"] != spec.Server && string(secret.Data["server"]) != spec.Server {
		t.Errorf("server not updated to %q; StringData[server]=%q Data[server]=%q",
			spec.Server, secret.StringData["server"], string(secret.Data["server"]))
	}
}

// TestEnsure_RaceWindow_UnlabeledBecomesAdopted verifies that when Ensure fires
// on an unlabeled secret before the orchestrator's SetAnnotation call, the secret
// ends up adopted-annotated with data preserved (Story 28.2 race-window closure).
func TestEnsure_RaceWindow_UnlabeledBecomesAdopted(t *testing.T) {
	foreignData := map[string][]byte{
		"config": []byte(`{"execProviderConfig":{"command":"kubectl","args":["..."]}}`),
		"server": []byte("https://race-cluster.example.com"),
		"name":   []byte("race-cluster"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "race-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				// No managed-by, no adopted annotation — simulates the race window
				// where the reconciler fires before the orchestrator's SetAnnotation.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	spec := ClusterSecretSpec{
		Name:   "race-cluster",
		Server: "https://race-cluster.example.com",
		Region: "us-east-1",
		Labels: map[string]string{"addon-foo": "true"},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	// Must have the adopted annotation (closes the race window).
	if secret.Annotations[AnnotationAdopted] != "true" {
		t.Errorf("expected adopted annotation after race-window Ensure; annotations=%v", secret.Annotations)
	}
	// Must have the managed-by label.
	if secret.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("expected managed-by label; labels=%v", secret.Labels)
	}
	// Data must be preserved.
	for k, want := range foreignData {
		if got := string(secret.Data[k]); got != string(want) {
			t.Errorf("Data[%q] = %q, want %q (must be preserved in race-window)", k, got, string(want))
		}
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

// --- Story 29.2: Adopted-gate tests — connectivity-check label must never appear on adopted secrets ---

// TestEnsure_AdoptPath_ConnectivityCheckLabelExcluded verifies that even when the
// caller passes connectivity-check: enabled in the spec's desired labels (the race
// window where the reconciler computes the label before it knows about adoption),
// the takeover write strips it — adopted clusters are guests in a shared ArgoCD hub
// and must never receive the check ApplicationSet.
func TestEnsure_AdoptPath_ConnectivityCheckLabelExcluded(t *testing.T) {
	const checkLabel = "sharko.dev/connectivity-check"

	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"tok","tlsClientConfig":{"insecure":false}}`),
		"server": []byte("https://adopted-cluster.example.com"),
		"name":   []byte("adopted-cluster"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// No managed-by — triggers the takeover adoption path.
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	// Spec deliberately includes the check label (simulating what the reconciler
	// computes for a zero-addon cluster before it is aware of adoption).
	spec := ClusterSecretSpec{
		Name:   "adopted-cluster",
		Server: "https://adopted-cluster.example.com",
		Labels: map[string]string{
			checkLabel: "enabled",
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after adoption: %v", err)
	}
	// The connectivity-check label MUST NOT appear on the adopted secret.
	if v, ok := secret.Labels[checkLabel]; ok {
		t.Errorf("adopted cluster secret must not carry %q; got value=%q", checkLabel, v)
	}
	// The adoption annotation must still be present.
	if secret.Annotations[AnnotationAdopted] != "true" {
		t.Errorf("expected adopted annotation; annotations=%v", secret.Annotations)
	}
	// Data must be untouched.
	for k, want := range foreignData {
		if got := string(secret.Data[k]); got != string(want) {
			t.Errorf("Data[%q] = %q, want %q (connection data must be preserved)", k, got, string(want))
		}
	}
}

// TestEnsure_AdoptedSteadyState_ConnectivityCheckLabelStripped verifies that when
// the reconciler later re-converges an already-adopted secret and includes the
// connectivity-check label in desired labels, the adopted steady-state path strips
// it before writing — the gate must hold on every reconcile, not just at adoption
// time.
func TestEnsure_AdoptedSteadyState_ConnectivityCheckLabelStripped(t *testing.T) {
	const checkLabel = "sharko.dev/connectivity-check"

	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"tok","tlsClientConfig":{"insecure":false}}`),
		"server": []byte("https://adopted2.example.com"),
		"name":   []byte("adopted2"),
	}
	// Already-adopted secret (managed-by + adopted annotation).
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted2",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
				// No addon labels — stale, triggers a label-convergence write.
			},
			Annotations: map[string]string{
				AnnotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	// Spec includes the check label (computed by reconciler for a zero-addon cluster).
	spec := ClusterSecretSpec{
		Name:   "adopted2",
		Server: "https://adopted2.example.com",
		Labels: map[string]string{
			"addon-foo":  "enabled",
			checkLabel: "enabled",
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after steady-state convergence: %v", err)
	}
	// The connectivity-check label MUST NOT appear, even though it was in the desired labels.
	if v, ok := secret.Labels[checkLabel]; ok {
		t.Errorf("adopted cluster steady-state must not carry %q; got value=%q", checkLabel, v)
	}
	// The addon label from the spec must be present.
	if secret.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon label addon-foo = %q, want enabled", secret.Labels["addon-foo"])
	}
	// Data must be untouched.
	for k, want := range foreignData {
		if got := string(secret.Data[k]); got != string(want) {
			t.Errorf("Data[%q] = %q, want %q (connection data must be untouched)", k, got, string(want))
		}
	}
}

// Ensure k8stesting is imported (used indirectly via fake client assertions above).
var _ k8stesting.Action // compile-time import check

// TestSyncLabelsOnly_NormalizesLegacyAddonValues_BothCallerConventions pins
// V2-cleanup-60 H3: SyncLabelsOnly is the single choke point that normalizes
// legacy "true"/"false" addon-label values to the canonical
// "enabled"/"disabled" vocabulary BEFORE comparing or writing — regardless
// of which of the two reconcilers calls it and regardless of whether that
// caller already normalized. Before this fix, the legacy internal/argosecrets
// reconciler passed raw cluster.Labels (still "true"/"false") straight
// through, while internal/clusterreconciler's syncSelfManaged pre-normalized
// its own copy first; whichever reconciler ran last would see the other's
// write as "not matching its own desired set" and rewrite the secret back —
// an infinite flip that toggles the addon ApplicationSet selection.
//
// Both caller conventions are exercised here: one passing raw legacy values
// (mirroring the legacy reconciler), one passing already-canonical values
// (mirroring clusterreconciler's pre-normalization). Both must converge to
// the canonical vocabulary in ONE write, and a second call with the SAME
// caller-supplied labels (the legacy reconciler re-reads "true"/"false" from
// YAML every tick — it never stops offering the old value) must be a no-op.
func TestSyncLabelsOnly_NormalizesLegacyAddonValues_BothCallerConventions(t *testing.T) {
	tests := []struct {
		name         string
		callerLabels map[string]string
	}{
		{
			name: "legacy reconciler passes raw true/false",
			callerLabels: map[string]string{
				"addon-datadog":   "true",
				"addon-karpenter": "false",
			},
		},
		{
			name: "clusterreconciler pre-normalizes before calling in",
			callerLabels: map[string]string{
				"addon-datadog":   "enabled",
				"addon-karpenter": "disabled",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "byo-cluster",
					Namespace: testNamespace,
					Labels: map[string]string{
						"team": "payments", // foreign label — must survive
					},
				},
				Type: corev1.SecretTypeOpaque,
			})
			mgr := NewManager(client, testNamespace)

			changed, found, err := mgr.SyncLabelsOnly(context.Background(), "byo-cluster", tc.callerLabels)
			if err != nil {
				t.Fatalf("first SyncLabelsOnly call: %v", err)
			}
			if !found {
				t.Fatal("secret exists — found must be true")
			}
			if !changed {
				t.Fatal("first call must write — labels are not yet canonical")
			}

			secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "byo-cluster", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("getting secret after sync: %v", err)
			}
			if secret.Labels["addon-datadog"] != "enabled" {
				t.Errorf("addon-datadog = %q, want enabled", secret.Labels["addon-datadog"])
			}
			if secret.Labels["addon-karpenter"] != "disabled" {
				t.Errorf("addon-karpenter = %q, want disabled", secret.Labels["addon-karpenter"])
			}
			if secret.Labels["team"] != "payments" {
				t.Error("foreign label must survive the sync")
			}

			// Second call, same caller convention (the caller never stops
			// offering its own value — the legacy reconciler re-reads the
			// same "true"/"false" from YAML every tick). Must be a no-op:
			// no Update issued, changed == false.
			changed2, found2, err2 := mgr.SyncLabelsOnly(context.Background(), "byo-cluster", tc.callerLabels)
			if err2 != nil {
				t.Fatalf("second SyncLabelsOnly call: %v", err2)
			}
			if !found2 {
				t.Fatal("second call: found must be true")
			}
			if changed2 {
				t.Fatal("second call must be a no-op — labels already converged to canonical values")
			}
		})
	}
}
