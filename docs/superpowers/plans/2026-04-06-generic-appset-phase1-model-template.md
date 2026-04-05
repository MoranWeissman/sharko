# Generic AppSet Template — Phase 1: Model + Template

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expand the AddonCatalogEntry model with 5 new fields, fix the name/appName inconsistency, rewrite the AppSet template to be catalog-driven (zero addon-specific logic), and delete the starter template.

**Architecture:** The model expansion in `internal/models/addon.go` adds `SyncWave`, `SelfHeal`, `SyncOptions`, `AdditionalSources`, and `ExtraHelmValues` fields. The `InMigration` field is removed. The orchestrator's `generateAddonCatalogEntry` is updated to emit `appName:` (not `name:`) and include all new fields. The AppSet template at `templates/bootstrap/templates/addons-appset.yaml` is rewritten to render all features from catalog fields — no `if eq appName "datadog"` blocks.

**Tech Stack:** Go 1.25, Helm template (Go templates), YAML

---

## File Structure

### Modified files
| File | Changes |
|------|---------|
| `internal/models/addon.go` | Expand `AddonCatalogEntry` (5 new fields, remove `InMigration`), add `AddonSource` struct |
| `internal/orchestrator/types.go` | Expand `AddAddonRequest` with new fields |
| `internal/orchestrator/addon.go` | Fix `generateAddonCatalogEntry` (appName, new fields), update `generateAddonGlobalValues` |
| `internal/orchestrator/values_generator.go` | Add `_sharko` computed block to `generateClusterValues` |
| `internal/config/parser.go` | Ensure parser handles new fields (YAML tags already match) |
| `internal/gitops/yaml_mutator.go` | No changes needed (operates on raw YAML lines) |
| `templates/bootstrap/templates/addons-appset.yaml` | Complete rewrite — generic catalog-driven template |

### Deleted files
| File | Reason |
|------|--------|
| `templates/starter/` | Entire directory removed per spec. `sharko init` uses `templates/bootstrap/` |

---

### Task 1: Expand AddonCatalogEntry Model

**Files:**
- Modify: `internal/models/addon.go`

- [ ] **Step 1: Add AddonSource struct**

Add above `AddonCatalogEntry` in `internal/models/addon.go`:

```go
// AddonSource represents an additional Helm chart or manifest source for an addon.
type AddonSource struct {
	RepoURL    string            `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`
	Path       string            `json:"path,omitempty" yaml:"path,omitempty"`
	Chart      string            `json:"chart,omitempty" yaml:"chart,omitempty"`
	Version    string            `json:"version,omitempty" yaml:"version,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	ValueFiles []string          `json:"valueFiles,omitempty" yaml:"valueFiles,omitempty"`
}
```

- [ ] **Step 2: Update AddonCatalogEntry struct**

Replace the `AddonCatalogEntry` struct with:

```go
// AddonCatalogEntry represents an addon definition from addons-catalog.yaml.
type AddonCatalogEntry struct {
	// Basic (required)
	AppName string `json:"appName" yaml:"appName"`
	RepoURL string `json:"repoURL" yaml:"repoURL"`
	Chart   string `json:"chart" yaml:"chart"`
	Version string `json:"version" yaml:"version"`

	// Basic (optional)
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // defaults to AppName

	// Advanced — deployment behavior
	SyncWave    int      `json:"syncWave,omitempty" yaml:"syncWave,omitempty"`
	SelfHeal    *bool    `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
	SyncOptions []string `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"`

	// Advanced — additional sources
	AdditionalSources []AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`

	// Advanced — ArgoCD behavior
	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`

	// Advanced — extra Helm configuration
	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"`
}
```

Note: `InMigration` is removed. `SelfHeal` is `*bool` (nil = default true, false = explicitly disabled).

- [ ] **Step 3: Update AddonCatalogItem to remove InMigration**

In the same file, find `AddonCatalogItem` and remove the `InMigration` field:

```go
// Remove this line:
InMigration bool `json:"in_migration,omitempty"`
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

Expected: Compilation errors in any code referencing `InMigration` on `AddonCatalogEntry` or `AddonCatalogItem`. Note these — they'll be fixed in subsequent steps.

- [ ] **Step 5: Fix all InMigration references**

Search for `InMigration` across the codebase:

```bash
grep -rn "InMigration\|inMigration\|in_migration" --include="*.go" . | grep -v vendor | grep -v _test.go
```

For each reference:
- In `internal/service/addon.go` (or wherever `AddonCatalogItem` is populated): remove the `InMigration: entry.InMigration` line
- In `internal/config/parser.go`: the `addonsCatalogFile` struct has `MigrationIgnoreDifferences` — remove it
- Any other Go files: remove the field reference

```bash
go build ./...
```

Expected: Clean build.

- [ ] **Step 6: Run tests**

```bash
go test ./...
```

Fix any test failures from removed `InMigration` field.

- [ ] **Step 7: Commit**

```bash
git add internal/models/addon.go internal/service/ internal/config/
git commit -m "feat: expand AddonCatalogEntry model — 5 new fields, remove InMigration"
```

---

### Task 2: Update AddAddonRequest and Generator

**Files:**
- Modify: `internal/orchestrator/types.go`
- Modify: `internal/orchestrator/addon.go`

- [ ] **Step 1: Expand AddAddonRequest**

In `internal/orchestrator/types.go`, replace `AddAddonRequest`:

```go
// AddAddonRequest is the input for adding an addon to the catalog.
type AddAddonRequest struct {
	Name              string                   `json:"name"`
	Chart             string                   `json:"chart"`
	RepoURL           string                   `json:"repo_url"`
	Version           string                   `json:"version"`
	Namespace         string                   `json:"namespace"`
	SyncWave          int                      `json:"sync_wave,omitempty"`
	SelfHeal          *bool                    `json:"self_heal,omitempty"`
	SyncOptions       []string                 `json:"sync_options,omitempty"`
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"`
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`
}
```

Add the models import if not present:

```go
import "github.com/MoranWeissman/sharko/internal/models"
```

- [ ] **Step 2: Fix generateAddonCatalogEntry — appName + new fields**

In `internal/orchestrator/addon.go`, replace `generateAddonCatalogEntry`:

```go
// generateAddonCatalogEntry creates the YAML catalog entry for an addon.
func generateAddonCatalogEntry(req AddAddonRequest) []byte {
	entry := models.AddonCatalogEntry{
		AppName:           req.Name,
		RepoURL:           req.RepoURL,
		Chart:             req.Chart,
		Version:           req.Version,
		Namespace:         req.Namespace,
		SyncWave:          req.SyncWave,
		SelfHeal:          req.SelfHeal,
		SyncOptions:       req.SyncOptions,
		AdditionalSources: req.AdditionalSources,
		IgnoreDifferences: req.IgnoreDifferences,
		ExtraHelmValues:   req.ExtraHelmValues,
	}

	data, err := yaml.Marshal(entry)
	if err != nil {
		// Fallback to manual generation
		var b strings.Builder
		b.WriteString(fmt.Sprintf("appName: %s\n", req.Name))
		b.WriteString(fmt.Sprintf("chart: %s\n", req.Chart))
		b.WriteString(fmt.Sprintf("repoURL: %s\n", req.RepoURL))
		b.WriteString(fmt.Sprintf("version: %s\n", req.Version))
		return []byte(b.String())
	}

	header := fmt.Sprintf("# Addon catalog entry for %s\n", req.Name)
	return append([]byte(header), data...)
}
```

Add `yaml.v3` import:

```go
"gopkg.in/yaml.v3"
```

This fixes the critical `name:` → `appName:` bug and properly serializes all new fields.

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/orchestrator/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/types.go internal/orchestrator/addon.go
git commit -m "feat: fix name→appName bug, add new fields to addon generator"
```

---

### Task 3: Add _sharko Computed Block to Cluster Values

**Files:**
- Modify: `internal/orchestrator/values_generator.go`

- [ ] **Step 1: Update generateClusterValues signature**

The function needs access to the addon catalog to look up namespaces. Update the signature:

```go
func generateClusterValues(clusterName string, region string, addons map[string]bool, catalog []models.AddonCatalogEntry) []byte {
```

Add import for `models`:

```go
import "github.com/MoranWeissman/sharko/internal/models"
```

- [ ] **Step 2: Add _sharko computed block**

After the addon enabled/disabled blocks, add the `_sharko` section:

```go
	// Compute _sharko block with enabled addon namespaces
	if len(addons) > 0 {
		enabledAddons := []struct{ name, ns string }{}
		for _, name := range names {
			if addons[name] {
				ns := name // default namespace = addon name
				for _, entry := range catalog {
					if entry.AppName == name && entry.Namespace != "" {
						ns = entry.Namespace
						break
					}
				}
				enabledAddons = append(enabledAddons, struct{ name, ns string }{name, ns})
			}
		}

		if len(enabledAddons) > 0 {
			b.WriteString("\n# Auto-computed by Sharko — do not edit manually\n")
			b.WriteString("_sharko:\n")

			nsNames := make([]string, 0, len(enabledAddons))
			for _, a := range enabledAddons {
				nsNames = append(nsNames, a.ns)
			}
			b.WriteString(fmt.Sprintf("  enabledAddonNamespaces: \"%s\"\n", strings.Join(nsNames, ",")))

			b.WriteString("  enabledAddons:\n")
			for _, a := range enabledAddons {
				b.WriteString(fmt.Sprintf("    - name: %s\n      namespace: %s\n", a.name, a.ns))
			}
		}
	}
```

- [ ] **Step 3: Update all callers of generateClusterValues**

Search for callers:

```bash
grep -rn "generateClusterValues" --include="*.go" . | grep -v _test.go
```

Update each call site to pass the catalog. In `internal/orchestrator/cluster.go`, the `RegisterCluster` function calls `generateClusterValues` — it needs to fetch the catalog from Git and pass it:

```go
// Before generateClusterValues call, fetch catalog:
catalogData, err := gp.GetFileContent(ctx, o.gitOps.BaseBranch, "configuration/addons-catalog.yaml")
var catalog []models.AddonCatalogEntry
if err == nil && catalogData != nil {
	catalog, _ = o.parser.ParseAddonsCatalog(catalogData)
}

values := generateClusterValues(req.Name, req.Region, req.Addons, catalog)
```

If the catalog can't be fetched (new repo), pass nil — the function handles it gracefully (no `_sharko` block).

- [ ] **Step 4: Verify build + tests**

```bash
go build ./...
go test ./internal/orchestrator/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/values_generator.go internal/orchestrator/cluster.go
git commit -m "feat: add _sharko computed block to cluster values with addon namespaces"
```

---

### Task 4: Rewrite AppSet Template

**Files:**
- Modify: `templates/bootstrap/templates/addons-appset.yaml`

- [ ] **Step 1: Read the current template to understand the structure**

```bash
cat templates/bootstrap/templates/addons-appset.yaml | head -50
```

Understand the Helm template structure: it iterates `range .Values.applicationsets` and generates AppProject + ApplicationSet per addon.

- [ ] **Step 2: Replace with generic catalog-driven template**

Write the new template. Key changes:
- Remove ALL `if eq $appset.appName "xxx"` blocks
- `syncWave` driven by `$appset.syncWave` field (not hardcoded per addon name)
- `selfHeal` driven by `$appset.selfHeal` field (defaults to true)
- `syncOptions` driven by `$appset.syncOptions` array (always includes CreateNamespace=true)
- `additionalSources` driven by `$appset.additionalSources` array
- `ignoreDifferences` driven by `$appset.ignoreDifferences` array only (no migration logic)
- `extraHelmValues` driven by `$appset.extraHelmValues` map
- Keep ALL safety features: secret-type filter, finalizer, ignoreMissingValueFiles, skipSchemaValidation, goTemplateOptions

The template must be valid Helm/Go template syntax. Use `{{ if }}`, `{{ range }}`, `{{ default }}` as shown in the spec Section 4.

- [ ] **Step 3: Validate template syntax**

```bash
helm template sharko charts/sharko/ 2>&1 | head -20
```

If there are template errors, fix them. Note: this renders the Sharko Helm chart, not the bootstrap chart. The bootstrap template is embedded and used by ArgoCD at runtime. We can validate YAML structure but not full ArgoCD rendering locally.

- [ ] **Step 4: Commit**

```bash
git add templates/bootstrap/templates/addons-appset.yaml
git commit -m "feat: rewrite AppSet template — generic catalog-driven, zero addon-specific logic"
```

---

### Task 5: Delete Starter Template

**Files:**
- Delete: `templates/starter/` (entire directory)

- [ ] **Step 1: Check if starter is referenced anywhere**

```bash
grep -rn "starter" --include="*.go" . | grep -v vendor | grep -v _test.go
```

If `templates/starter` is referenced in `embed.go` or init logic, update those references to use `templates/bootstrap` instead.

- [ ] **Step 2: Update embed.go if needed**

Read `templates/embed.go`. If it embeds `starter`, change to embed only `bootstrap`:

```go
//go:embed all:bootstrap
var BootstrapFS embed.FS
```

Remove any `starter` embed.

- [ ] **Step 3: Update init logic**

If `internal/orchestrator/init.go` references `starter`, update it to use `bootstrap`.

- [ ] **Step 4: Delete starter directory**

```bash
rm -rf templates/starter/
```

- [ ] **Step 5: Verify build**

```bash
go build ./...
go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: delete starter template — sharko init uses bootstrap only"
```

---

### Task 6: Update Swagger + Quality Gates

- [ ] **Step 1: Regenerate swagger**

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

- [ ] **Step 2: Run full quality gates**

```bash
go build ./...
go vet ./...
go test ./...
cd ui && npm run build && npm test -- --run
```

- [ ] **Step 3: Security scan**

```bash
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" --include="*.go" --include="*.yaml" . | grep -v node_modules | grep -v .git/
```

Must return empty.

- [ ] **Step 4: Commit swagger updates**

```bash
git add docs/swagger/
git commit -m "docs: regenerate swagger after model expansion"
```
