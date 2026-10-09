package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Task #152 story D — this file proves the same "a fetched value never
// leaves through a log line or an error message" property for the
// on-demand secret-push path (createAddonSecrets, the code behind
// POST /api/v1/clusters/{name}/secrets/refresh's CreateAddonSecretsForCluster
// wrapper).
//
// This is Engine B in the security report — the path task #152 story A is
// separately re-pointing onto the Git catalog. Until that lane lands, this
// is the code a live server actually runs on a refresh call, so it is
// covered here rather than assumed safe by association with the reconciler
// (internal/secrets.Reconciler, Engine A) covered by
// internal/secrets/sentinel_test.go. Fake value only.
const orchestratorSentinelValue = "CANARY-6bb4f271-do-not-log-me-71d0a3e9-sentinel"

// captureOrchestratorLogs mirrors internal/secrets/sentinel_test.go's
// captureLogs — duplicated locally rather than exported cross-package to
// keep each package's test helpers self-contained. Not safe under
// t.Parallel(); no test in this file uses it.
func captureOrchestratorLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	fn()
	return buf.String()
}

// TestSentinel_CreateAddonSecrets_HappyPath_NeverLeaksInLogs puts the
// sentinel through createAddonSecrets' fetch-then-push loop and confirms
// the value lands only in the destination Secret's Data — never in a log
// line, at any level.
func TestSentinel_CreateAddonSecrets_HappyPath_NeverLeaksInLogs(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{
		"secrets/datadog/api-key": []byte(orchestratorSentinelValue),
	}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	var result *secretCreationResult
	var err error
	logs := captureOrchestratorLogs(t, func() {
		result, err = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.Failed)
	}

	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if string(secret.Data["api-key"]) != orchestratorSentinelValue {
		t.Fatalf("secret Data is the legitimate destination — expected the sentinel there, got %q", secret.Data["api-key"])
	}
	for k, v := range secret.Annotations {
		if strings.Contains(v, orchestratorSentinelValue) {
			t.Errorf("sentinel leaked into Secret annotation %q: %q", k, v)
		}
	}
	if strings.Contains(logs, orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into createAddonSecrets logs:\n%s", logs)
	}
}

// TestSentinel_CreateAddonSecrets_PushFailure_ErrorNeverLeaksValue exercises
// the failure path: the value is fetched successfully (it exists in memory)
// and then the Kubernetes write itself fails. The per-secret SecretError
// text this function returns (which the API/CLI surface to a caller) and
// every log line around the failure must never carry it.
func TestSentinel_CreateAddonSecrets_PushFailure_ErrorNeverLeaksValue(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("simulated apiserver rejection")
	})
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{
		"secrets/datadog/api-key": []byte(orchestratorSentinelValue),
	}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	var result *secretCreationResult
	var err error
	logs := captureOrchestratorLogs(t, func() {
		result, err = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	})
	if err != nil {
		t.Fatalf("unexpected top-level error (failures are partial-success, not fatal): %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected 1 failed secret, got %d: %v", len(result.Failed), result.Failed)
	}
	// This assertion used to be
	//   if !strings.Contains(result.Failed[0].Error, "simulated apiserver rejection")
	// — it REQUIRED the Kubernetes API server's own words to be in the text a
	// caller sees, which is the leak written down as the design. What the
	// caller gets now is the catalog sentence plus the structured fields, and
	// the apiserver's words must be absent.
	got := result.Failed[0]
	if got.Code != SecretFailureWrite {
		t.Fatalf("a failed cluster write should be coded as the write failure, got %q", got.Code)
	}
	if got.Error.String() != secretFailureSentences[SecretFailureWrite] {
		t.Fatalf("expected the catalog write sentence, got: %q", got.Error.String())
	}
	if strings.Contains(got.Error.String(), "simulated apiserver rejection") {
		t.Fatalf("the apiserver's own words reached the caller: %q", got.Error.String())
	}
	if strings.Contains(got.Error.String(), orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into the SecretError text a caller sees: %q", got.Error.String())
	}
	if strings.Contains(logs, orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into logs on a push failure:\n%s", logs)
	}
}

// TestSentinel_CreateAddonSecrets_FetchFailure_ErrorNeverLeaksValue proves
// the fetch-error branch: when the secrets provider itself fails (a vault
// outage, in the existing FetcherError_PartialSuccess test's terms), the
// returned SecretError text and the logs around it are built from the
// provider's error and the request path only — never a value, since none
// was ever fetched on this path.
func TestSentinel_CreateAddonSecrets_FetchFailure_ErrorNeverLeaksValue(t *testing.T) {
	client := fake.NewSimpleClientset()
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{err: errors.New("simulated vault outage")}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	var result *secretCreationResult
	var err error
	logs := captureOrchestratorLogs(t, func() {
		result, err = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected 1 failed secret, got %d", len(result.Failed))
	}
	if result.Failed[0].Code != SecretFailureFetch {
		t.Fatalf("a failed store read should be coded as the fetch failure, got %q", result.Failed[0].Code)
	}
	if strings.Contains(result.Failed[0].Error.String(), "simulated vault outage") {
		t.Fatalf("the secrets store's own words reached the caller: %q", result.Failed[0].Error.String())
	}
	if strings.Contains(result.Failed[0].Error.String(), orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into the fetch-failure SecretError text: %q", result.Failed[0].Error.String())
	}
	if strings.Contains(logs, orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into logs on a fetch failure:\n%s", logs)
	}
	if _, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); getErr == nil {
		t.Fatal("no secret should have been written when the fetch failed")
	}
}

// TestSentinel_CreateAddonSecrets_ForeignSecret_ValueFetchedButNeverWritten
// covers the ownership-gate boundary on this engine too: the value is
// fetched, then EnsureSecret refuses to touch a pre-existing Secret Sharko
// did not create — the value must not have leaked into the resulting
// SecretError or the logs on the way to that refusal.
func TestSentinel_CreateAddonSecrets_ForeignSecret_ValueFetchedButNeverWritten(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "datadog-secret", Namespace: "monitoring"}, // no managed-by label
		Data:       map[string][]byte{"api-key": []byte("someone-elses-value")},
	})
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	defs := map[string]AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secret",
			Namespace:  "monitoring",
			Keys:       map[string]string{"api-key": "secrets/datadog/api-key"},
		},
	}
	fetcher := &mockSecretFetcher{secrets: map[string][]byte{
		"secrets/datadog/api-key": []byte(orchestratorSentinelValue),
	}}
	orch.SetSecretManagement(defs, fetcher, fakeClientFactoryFor(client))

	var result *secretCreationResult
	var err error
	logs := captureOrchestratorLogs(t, func() {
		result, err = orch.createAddonSecrets(context.Background(), nil, map[string]bool{"datadog": true})
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("expected the foreign-secret refusal recorded as a failed push, got %d", len(result.Failed))
	}
	if strings.Contains(result.Failed[0].Error.String(), orchestratorSentinelValue) {
		t.Fatalf("sentinel leaked into the foreign-secret SecretError text: %q", result.Failed[0].Error.String())
	}
	if strings.Contains(logs, orchestratorSentinelValue) {
		t.Fatalf("sentinel (fetched, then discarded because the secret is foreign) leaked into logs:\n%s", logs)
	}
	secret, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("the foreign secret should still exist, untouched: %v", getErr)
	}
	if string(secret.Data["api-key"]) != "someone-elses-value" {
		t.Fatalf("the foreign secret's data must be untouched, got %q", secret.Data["api-key"])
	}
}
