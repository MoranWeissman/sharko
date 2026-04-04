# Phase 3: Remote Cluster Secrets Management — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable Sharko to create, update, and delete K8s Secrets on remote clusters for addon dependencies (e.g., Datadog API keys, ESO credentials), replacing the need for External Secrets Operator.

**Architecture:** New `internal/remoteclient/` package builds temporary `kubernetes.Interface` clients from kubeconfig bytes. A `SecretValueFetcher` interface abstracts fetching secret values from the provider (AWS SM / K8s Secrets). Addon secret definitions are stored in server config (env var JSON or Helm ConfigMap). The orchestrator gains secret management steps in RegisterCluster, DeregisterCluster, and UpdateClusterAddons flows. New API endpoints manage addon-secret definitions and per-cluster secret operations.

**Tech Stack:** Go 1.25.8, `k8s.io/client-go` (already a dependency), existing `providers.ClusterCredentialsProvider`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/remoteclient/client.go` | Create | Build temp `kubernetes.Interface` from kubeconfig bytes |
| `internal/remoteclient/secrets.go` | Create | Create/update/delete/list K8s Secrets on remote cluster |
| `internal/remoteclient/client_test.go` | Create | Tests for client builder and secret operations (fake K8s client) |
| `internal/orchestrator/secrets.go` | Create | `AddonSecretDefinition` type, `SecretValueFetcher` interface, orchestrator methods for secret operations |
| `internal/orchestrator/orchestrator.go` | Modify | Add `secretDefs`, `secretFetcher`, `remoteClientFactory` fields to Orchestrator + New() |
| `internal/orchestrator/cluster.go` | Modify | Integrate secret creation/deletion into Register/Deregister/Update flows |
| `internal/orchestrator/types.go` | Modify | Add `SecretsResult` to `RegisterClusterResult` |
| `internal/api/addon_secrets.go` | Create | Handlers for addon-secret definition CRUD endpoints |
| `internal/api/cluster_secrets.go` | Create | Handlers for cluster secret list/refresh endpoints |
| `internal/api/router.go` | Modify | Add `addonSecretDefs` field to Server, register 5 new routes |
| `cmd/sharko/serve.go` | Modify | Load addon secret config from env/ConfigMap, wire into Server |

---

### Task 1: Create `internal/remoteclient/` package — client builder

**Files:**
- Create: `internal/remoteclient/client.go`
- Create: `internal/remoteclient/client_test.go`

- [ ] **Step 1: Write the test for building a K8s client from kubeconfig bytes**

```go
// internal/remoteclient/client_test.go
package remoteclient

import (
	"testing"
)

func TestNewClientFromKubeconfig_InvalidBytes(t *testing.T) {
	_, err := NewClientFromKubeconfig([]byte("not valid kubeconfig"))
	if err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remoteclient/ -run TestNewClientFromKubeconfig_InvalidBytes -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write client.go**

```go
// internal/remoteclient/client.go
package remoteclient

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientFromKubeconfig builds a temporary kubernetes.Interface from raw kubeconfig bytes.
// The caller should discard the client after use — no persistent connections.
func NewClientFromKubeconfig(kubeconfig []byte) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return client, nil
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/remoteclient/ -run TestNewClientFromKubeconfig_InvalidBytes -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/remoteclient/
git commit -m "feat: add remoteclient package with K8s client builder"
```

---

### Task 2: Add secret operations to remoteclient

**Files:**
- Create: `internal/remoteclient/secrets.go`
- Modify: `internal/remoteclient/client_test.go`

- [ ] **Step 1: Write tests for secret CRUD**

```go
// Add to internal/remoteclient/client_test.go
func TestEnsureSecret_CreatesNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	err := EnsureSecret(context.Background(), client, "datadog", "datadog-keys", map[string][]byte{
		"api-key": []byte("secret-value"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret, err := client.CoreV1().Secrets("datadog").Get(context.Background(), "datadog-keys", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if secret.Labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Error("expected managed-by label")
	}
	if string(secret.Data["api-key"]) != "secret-value" {
		t.Errorf("unexpected data: %s", secret.Data["api-key"])
	}
}

func TestEnsureSecret_UpdatesExisting(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "datadog-keys", Namespace: "datadog",
			Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
		},
		Data: map[string][]byte{"api-key": []byte("old-value")},
	}
	client := fake.NewSimpleClientset(existing)
	err := EnsureSecret(context.Background(), client, "datadog", "datadog-keys", map[string][]byte{
		"api-key": []byte("new-value"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret, _ := client.CoreV1().Secrets("datadog").Get(context.Background(), "datadog-keys", metav1.GetOptions{})
	if string(secret.Data["api-key"]) != "new-value" {
		t.Errorf("expected updated value, got %s", secret.Data["api-key"])
	}
}

func TestDeleteManagedSecrets(t *testing.T) {
	secrets := []runtime.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "datadog-keys", Namespace: "datadog",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "user-secret", Namespace: "datadog",
				// No managed-by label — should NOT be deleted.
			},
		},
	}
	client := fake.NewSimpleClientset(secrets...)

	deleted, err := DeleteManagedSecrets(context.Background(), client, "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "datadog-keys" {
		t.Errorf("expected [datadog-keys], got %v", deleted)
	}

	// User secret should still exist.
	_, err = client.CoreV1().Secrets("datadog").Get(context.Background(), "user-secret", metav1.GetOptions{})
	if err != nil {
		t.Error("user secret should not have been deleted")
	}
}

func TestListManagedSecrets(t *testing.T) {
	secrets := []runtime.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "s1", Namespace: "ns1",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "s2", Namespace: "ns2",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
	}
	client := fake.NewSimpleClientset(secrets...)

	result, err := ListManagedSecrets(context.Background(), client, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(result))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/remoteclient/ -v`
Expected: FAIL — functions not defined yet.

- [ ] **Step 3: Write secrets.go**

```go
// internal/remoteclient/secrets.go
package remoteclient

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "sharko"

// ManagedSecretInfo describes a Sharko-managed secret on a remote cluster.
type ManagedSecretInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// EnsureSecret creates or updates a K8s Secret on the remote cluster.
// The secret is labeled with app.kubernetes.io/managed-by=sharko.
func EnsureSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				managedByLabel: managedByValue,
			},
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}

	existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, createErr := client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("creating secret %s/%s: %w", namespace, name, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking secret %s/%s: %w", namespace, name, err)
	}

	// Update existing secret.
	existing.Data = data
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	existing.Labels[managedByLabel] = managedByValue
	_, err = client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteManagedSecrets deletes all Sharko-managed secrets in a namespace.
// Returns the names of deleted secrets.
func DeleteManagedSecrets(ctx context.Context, client kubernetes.Interface, namespace string) ([]string, error) {
	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets in %s: %w", namespace, err)
	}

	var deleted []string
	for _, s := range secrets.Items {
		if err := client.CoreV1().Secrets(namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil {
			return deleted, fmt.Errorf("deleting secret %s/%s: %w", namespace, s.Name, err)
		}
		deleted = append(deleted, s.Name)
	}
	return deleted, nil
}

// ListManagedSecrets lists all Sharko-managed secrets across all namespaces (or a specific one).
func ListManagedSecrets(ctx context.Context, client kubernetes.Interface, namespace string) ([]ManagedSecretInfo, error) {
	opts := metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	}

	secrets, err := client.CoreV1().Secrets(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing managed secrets: %w", err)
	}

	result := make([]ManagedSecretInfo, 0, len(secrets.Items))
	for _, s := range secrets.Items {
		result = append(result, ManagedSecretInfo{
			Name:      s.Name,
			Namespace: s.Namespace,
		})
	}
	return result, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/remoteclient/ -v`
Expected: All 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/remoteclient/
git commit -m "feat: add secret CRUD operations to remoteclient package"
```

---

### Task 3: Add addon secret definitions and orchestrator secret methods

**Files:**
- Create: `internal/orchestrator/secrets.go`
- Modify: `internal/orchestrator/orchestrator.go`

- [ ] **Step 1: Create secrets.go with types and orchestrator methods**

```go
// internal/orchestrator/secrets.go
package orchestrator

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
	"k8s.io/client-go/kubernetes"
)

// AddonSecretDefinition maps an addon to the K8s Secret it needs on remote clusters.
type AddonSecretDefinition struct {
	AddonName  string            `json:"addon_name"`
	SecretName string            `json:"secret_name"`
	Namespace  string            `json:"namespace"`
	Keys       map[string]string `json:"keys"` // secret data key → provider path (e.g. "api-key" → "secrets/datadog/api-key")
}

// SecretValueFetcher abstracts fetching raw secret values from the secrets provider.
// The provider path (e.g. "secrets/datadog/api-key") maps to a secret in AWS SM or K8s Secrets.
type SecretValueFetcher interface {
	GetSecretValue(ctx context.Context, path string) ([]byte, error)
}

// RemoteClientFactory builds a kubernetes.Interface from raw kubeconfig bytes.
// Abstracted for testing — production uses remoteclient.NewClientFromKubeconfig.
type RemoteClientFactory func(kubeconfig []byte) (kubernetes.Interface, error)

// createAddonSecrets creates K8s Secrets on a remote cluster for all addons that have secret definitions.
// Returns the list of created secret names. If any fail, returns partial results and the error.
func (o *Orchestrator) createAddonSecrets(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil || o.secretFetcher == nil {
		return nil, nil // no secret management configured
	}

	// Filter to addons that are enabled AND have secret definitions.
	var toCreate []AddonSecretDefinition
	for addonName, enabled := range addons {
		if !enabled {
			continue
		}
		if def, ok := o.secretDefs[addonName]; ok {
			toCreate = append(toCreate, def)
		}
	}
	if len(toCreate) == 0 {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var created []string
	for _, def := range toCreate {
		data := make(map[string][]byte)
		for key, providerPath := range def.Keys {
			val, fetchErr := o.secretFetcher.GetSecretValue(ctx, providerPath)
			if fetchErr != nil {
				return created, fmt.Errorf("fetching secret value for %s key %q from %q: %w", def.AddonName, key, providerPath, fetchErr)
			}
			data[key] = val
		}

		if err := remoteclient.EnsureSecret(ctx, client, def.Namespace, def.SecretName, data); err != nil {
			return created, fmt.Errorf("creating secret for addon %s: %w", def.AddonName, err)
		}
		created = append(created, def.SecretName)
	}
	return created, nil
}

// deleteAddonSecrets deletes Sharko-managed secrets for specific addons from a remote cluster.
func (o *Orchestrator) deleteAddonSecrets(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var deleted []string
	for addonName, enabled := range addons {
		if enabled {
			continue // only delete secrets for disabled addons
		}
		def, ok := o.secretDefs[addonName]
		if !ok {
			continue
		}
		names, delErr := remoteclient.DeleteManagedSecrets(ctx, client, def.Namespace)
		if delErr != nil {
			return deleted, fmt.Errorf("deleting secrets for addon %s: %w", addonName, delErr)
		}
		deleted = append(deleted, names...)
	}
	return deleted, nil
}

// deleteAllAddonSecrets deletes ALL Sharko-managed secrets from a remote cluster (used during deregister).
func (o *Orchestrator) deleteAllAddonSecrets(ctx context.Context, kubeconfig []byte) ([]string, error) {
	if o.remoteClientFn == nil || o.secretDefs == nil {
		return nil, nil
	}

	client, err := o.remoteClientFn(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to remote cluster: %w", err)
	}

	var deleted []string
	for _, def := range o.secretDefs {
		names, delErr := remoteclient.DeleteManagedSecrets(ctx, client, def.Namespace)
		if delErr != nil {
			return deleted, fmt.Errorf("deleting secrets in namespace %s: %w", def.Namespace, delErr)
		}
		deleted = append(deleted, names...)
	}
	return deleted, nil
}
```

- [ ] **Step 2: Update orchestrator.go — add new fields to Orchestrator and New()**

Add three new fields to the Orchestrator struct and extend `New()`:

```go
type Orchestrator struct {
	gitMu          *sync.Mutex
	credProvider   providers.ClusterCredentialsProvider
	argocd         ArgocdClient
	git            gitprovider.GitProvider
	gitops         GitOpsConfig
	paths          RepoPathsConfig
	templateFS     fs.FS
	secretDefs     map[string]AddonSecretDefinition // addon name → definition
	secretFetcher  SecretValueFetcher
	remoteClientFn RemoteClientFactory
}

func New(
	gitMu *sync.Mutex,
	credProvider providers.ClusterCredentialsProvider,
	argocd ArgocdClient,
	git gitprovider.GitProvider,
	gitops GitOpsConfig,
	paths RepoPathsConfig,
	templateFS fs.FS,
) *Orchestrator {
	return &Orchestrator{
		gitMu:        gitMu,
		credProvider: credProvider,
		argocd:       argocd,
		git:          git,
		gitops:       gitops,
		paths:        paths,
		templateFS:   templateFS,
	}
}

// SetSecretManagement configures remote cluster secret operations.
// Called after New() when the server has addon secret definitions configured.
func (o *Orchestrator) SetSecretManagement(defs map[string]AddonSecretDefinition, fetcher SecretValueFetcher, clientFn RemoteClientFactory) {
	o.secretDefs = defs
	o.secretFetcher = fetcher
	o.remoteClientFn = clientFn
}
```

Using `SetSecretManagement` instead of extending `New()` keeps the constructor unchanged — existing callers (7 API handlers + all tests) don't need modification. The orchestrator gracefully handles nil secret deps by skipping secret operations.

- [ ] **Step 3: Build check**

Run: `go build ./...`
Expected: Pass. No existing callers need changes.

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/secrets.go internal/orchestrator/orchestrator.go
git commit -m "feat: add addon secret definitions and orchestrator secret methods"
```

---

### Task 4: Integrate secrets into orchestrator cluster flows

**Files:**
- Modify: `internal/orchestrator/cluster.go`
- Modify: `internal/orchestrator/types.go`

- [ ] **Step 1: Add SecretsResult to RegisterClusterResult**

In `types.go`, add to `RegisterClusterResult`:
```go
type RegisterClusterResult struct {
	Status         string        `json:"status"`
	Cluster        ClusterResult `json:"cluster"`
	Git            *GitResult    `json:"git,omitempty"`
	Secrets        []string      `json:"secrets_created,omitempty"` // names of created secrets
	CompletedSteps []string      `json:"completed_steps,omitempty"`
	FailedStep     string        `json:"failed_step,omitempty"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
}
```

- [ ] **Step 2: Update RegisterCluster to create secrets between ArgoCD registration and Git commit**

After ArgoCD registration (step 4) and before Git commit (step 5), add:
```go
// Step 5: Create addon secrets on remote cluster (if configured).
secretNames, secretErr := o.createAddonSecrets(ctx, creds.Raw, req.Addons)
if secretErr != nil {
	result.Status = "partial"
	result.CompletedSteps = steps
	result.Secrets = secretNames
	result.FailedStep = "create_secrets"
	result.Error = secretErr.Error()
	result.Message = "Cluster registered in ArgoCD but addon secret creation failed. PR not opened."
	return result, nil
}
if len(secretNames) > 0 {
	steps = append(steps, "create_secrets")
	result.Secrets = secretNames
}
```

- [ ] **Step 3: Update DeregisterCluster to delete secrets before ArgoCD deletion**

After ArgoCD label removal (step 1), add secret deletion:
```go
// Step 2: Delete Sharko-managed secrets from remote cluster.
if o.credProvider != nil {
	creds, credErr := o.credProvider.GetCredentials(name)
	if credErr == nil {
		o.deleteAllAddonSecrets(ctx, creds.Raw) // best-effort, don't fail deregister for this
	}
}
```

- [ ] **Step 4: Update UpdateClusterAddons to create/delete secrets for toggled addons**

Before the Git commit, determine which addons are being enabled vs disabled and act:
```go
// Create secrets for newly enabled addons, delete for disabled.
if o.credProvider != nil {
	creds, credErr := o.credProvider.GetCredentials(name)
	if credErr == nil {
		enabledAddons := make(map[string]bool)
		for a, e := range addons {
			if e {
				enabledAddons[a] = true
			}
		}
		o.createAddonSecrets(ctx, creds.Raw, enabledAddons)

		disabledAddons := make(map[string]bool)
		for a, e := range addons {
			if !e {
				disabledAddons[a] = false
			}
		}
		o.deleteAddonSecrets(ctx, creds.Raw, disabledAddons)
	}
}
```

- [ ] **Step 5: Renumber step comments in RegisterCluster**

Steps become: 1. Validate, 2. Duplicate check, 3. Fetch creds, 4. ArgoCD register, 5. Create secrets, 6. Git commit.

- [ ] **Step 6: Build and run existing tests**

Run: `go build ./... && go test ./internal/orchestrator/ -v`
Expected: All existing tests still pass (secret deps are nil, so secret steps are skipped).

- [ ] **Step 7: Commit**

```bash
git add internal/orchestrator/cluster.go internal/orchestrator/types.go
git commit -m "feat: integrate remote secrets into cluster registration flows"
```

---

### Task 5: Add tests for secret integration in orchestrator

**Files:**
- Modify: `internal/orchestrator/orchestrator_test.go`

- [ ] **Step 1: Add mock implementations for SecretValueFetcher and RemoteClientFactory**

```go
type mockSecretFetcher struct {
	secrets map[string][]byte // provider path → value
	err     error
}

func (m *mockSecretFetcher) GetSecretValue(_ context.Context, path string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.secrets[path]
	if !ok {
		return nil, fmt.Errorf("secret not found: %s", path)
	}
	return v, nil
}
```

- [ ] **Step 2: Add test for RegisterCluster with secrets**

Test that when secret definitions are configured and addons are enabled, secrets are created on the remote cluster before the Git commit.

- [ ] **Step 3: Add test for RegisterCluster with secret failure → partial success**

Test that when secret creation fails, the result is partial with FailedStep="create_secrets" and no Git commit happens.

- [ ] **Step 4: Add test for RegisterCluster without secret definitions (backward compat)**

Verify existing behavior is unchanged when no secret management is configured.

- [ ] **Step 5: Run all tests**

Run: `go test -race ./internal/orchestrator/ -v`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/orchestrator/orchestrator_test.go
git commit -m "test: add secret integration tests for orchestrator"
```

---

### Task 6: Add API endpoints for addon-secret definitions

**Files:**
- Create: `internal/api/addon_secrets.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Create addon_secrets.go with CRUD handlers**

```go
// internal/api/addon_secrets.go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

func (s *Server) handleListAddonSecrets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.addonSecretDefs)
}

func (s *Server) handleCreateAddonSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }

	var def orchestrator.AddonSecretDefinition
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if def.AddonName == "" || def.SecretName == "" || def.Namespace == "" || len(def.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "addon_name, secret_name, namespace, and keys are required")
		return
	}

	s.addonSecretDefs[def.AddonName] = def
	writeJSON(w, http.StatusCreated, def)
}

func (s *Server) handleDeleteAddonSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	addon := r.PathValue("addon")
	if addon == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	if _, ok := s.addonSecretDefs[addon]; !ok {
		writeError(w, http.StatusNotFound, "no secret definition for addon: "+addon)
		return
	}
	delete(s.addonSecretDefs, addon)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "addon": addon})
}
```

- [ ] **Step 2: Add `addonSecretDefs` to Server struct and initialize in NewServer**

In `router.go`:
```go
// In Server struct:
addonSecretDefs map[string]orchestrator.AddonSecretDefinition

// In NewServer, initialize:
addonSecretDefs: make(map[string]orchestrator.AddonSecretDefinition),
```

Add a setter:
```go
func (s *Server) SetAddonSecretDefs(defs map[string]orchestrator.AddonSecretDefinition) {
	s.addonSecretDefs = defs
}
```

- [ ] **Step 3: Register routes in NewRouter**

```go
// Addon secrets
mux.HandleFunc("GET /api/v1/addon-secrets", srv.handleListAddonSecrets)
mux.HandleFunc("POST /api/v1/addon-secrets", srv.handleCreateAddonSecret)
mux.HandleFunc("DELETE /api/v1/addon-secrets/{addon}", srv.handleDeleteAddonSecret)
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./...`
Expected: Pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/addon_secrets.go internal/api/router.go
git commit -m "feat: add addon-secret definition CRUD API endpoints"
```

---

### Task 7: Add API endpoints for cluster secret operations

**Files:**
- Create: `internal/api/cluster_secrets.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Create cluster_secrets.go with list and refresh handlers**

```go
// internal/api/cluster_secrets.go
package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

func (s *Server) handleListClusterSecrets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching cluster credentials: "+err.Error())
		return
	}

	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connecting to cluster: "+err.Error())
		return
	}

	secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, "")
	if err != nil {
		writeError(w, http.StatusBadGateway, "listing secrets: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster": name,
		"secrets": secrets,
	})
}

func (s *Server) handleRefreshClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	// This re-runs createAddonSecrets for all addons that have definitions.
	// TODO: determine which addons are enabled for this cluster from ArgoCD labels.
	// For now, refresh all defined addon secrets.
	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching cluster credentials: "+err.Error())
		return
	}

	allEnabled := make(map[string]bool)
	for addonName := range s.addonSecretDefs {
		allEnabled[addonName] = true
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	created, secretErr := orch.CreateAddonSecretsForCluster(r.Context(), creds.Raw, allEnabled)
	if secretErr != nil {
		writeError(w, http.StatusBadGateway, "refreshing secrets: "+secretErr.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster":          name,
		"secrets_refreshed": created,
	})
}
```

Note: `handleRefreshClusterSecrets` needs `s.secretFetcher` on the Server. Add this field alongside `addonSecretDefs`.

- [ ] **Step 2: Add secretFetcher to Server struct**

```go
// In Server struct:
secretFetcher orchestrator.SecretValueFetcher
```

Update `SetWriteAPIDeps` or add a new setter for secret management.

- [ ] **Step 3: Add public `CreateAddonSecretsForCluster` method to orchestrator**

This is a thin wrapper around `createAddonSecrets` for the refresh endpoint:
```go
// In internal/orchestrator/secrets.go:
func (o *Orchestrator) CreateAddonSecretsForCluster(ctx context.Context, kubeconfig []byte, addons map[string]bool) ([]string, error) {
	return o.createAddonSecrets(ctx, kubeconfig, addons)
}
```

- [ ] **Step 4: Register routes**

```go
// Cluster secrets
mux.HandleFunc("GET /api/v1/clusters/{name}/secrets", srv.handleListClusterSecrets)
mux.HandleFunc("POST /api/v1/clusters/{name}/secrets/refresh", srv.handleRefreshClusterSecrets)
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./...`
Expected: Pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/cluster_secrets.go internal/api/router.go internal/orchestrator/secrets.go
git commit -m "feat: add cluster secret list and refresh API endpoints"
```

---

### Task 8: Wire secret management in serve.go

**Files:**
- Modify: `cmd/sharko/serve.go`

- [ ] **Step 1: Add addon secret config loading**

After the provider setup block, add:
```go
// Addon secret definitions (optional — from SHARKO_ADDON_SECRETS env var as JSON)
addonSecretsJSON := os.Getenv("SHARKO_ADDON_SECRETS")
if addonSecretsJSON != "" {
	var defs map[string]orchestrator.AddonSecretDefinition
	if err := json.Unmarshal([]byte(addonSecretsJSON), &defs); err != nil {
		log.Printf("WARNING: Could not parse SHARKO_ADDON_SECRETS: %v", err)
	} else {
		srv.SetAddonSecretDefs(defs)
		log.Printf("Addon secret definitions loaded for %d addons", len(defs))
	}
}
```

- [ ] **Step 2: Build and verify**

Run: `go build ./...`
Expected: Pass.

- [ ] **Step 3: Commit**

```bash
git add -f cmd/sharko/serve.go
git commit -m "feat: wire addon secret config loading in serve.go"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run full quality gates**

```bash
go clean -testcache
go build ./...
go vet ./...
go test -race ./...
```

- [ ] **Step 2: Security grep**

```bash
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" --include="*.go" --include="*.ts" --include="*.yaml" . | grep -v node_modules | grep -v .git/
```
Expected: Empty.

- [ ] **Step 3: Verify new routes registered**

Grep for the 5 new routes:
```bash
grep -n "addon-secrets\|secrets/refresh\|/secrets" internal/api/router.go
```
Expected: 5 new route registrations.
