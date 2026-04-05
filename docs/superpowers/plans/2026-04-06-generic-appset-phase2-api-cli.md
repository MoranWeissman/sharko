# Generic AppSet Phase 2: API + CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PATCH endpoint for addon configuration, update POST/GET handlers for new fields, and add `configure-addon` + `describe-addon` CLI commands.

**Architecture:** PATCH reads the current addon catalog entry from Git (`charts/<name>/addon.yaml`), merges provided fields, and writes back via `yaml.Marshal` + Git PR. The CLI's `configure-addon` calls PATCH, `describe-addon` calls GET. POST already accepts the new fields from Phase 1's `AddAddonRequest` expansion.

**Tech Stack:** Go 1.25, Cobra CLI, `yaml.v3`, REST API

---

## File Structure

### New files
| File | Responsibility |
|------|---------------|
| `internal/orchestrator/addon_configure.go` | `ConfigureAddon` orchestrator method — read/merge/write catalog entry |

### Modified files
| File | Changes |
|------|---------|
| `internal/orchestrator/types.go` | Add `ConfigureAddonRequest` type |
| `internal/api/addons_write.go` | Add `handleConfigureAddon` (PATCH handler) |
| `internal/api/addons.go` | Update GET addon detail to return all new fields |
| `internal/api/router.go` | Register PATCH route |
| `cmd/sharko/addon.go` | Add `configure-addon` and `describe-addon` commands |

---

### Task 1: ConfigureAddon Orchestrator Method

**Files:**
- Create: `internal/orchestrator/addon_configure.go`
- Modify: `internal/orchestrator/types.go`

- [ ] **Step 1: Add ConfigureAddonRequest type**

In `internal/orchestrator/types.go`, add after `AddAddonRequest`:

```go
// ConfigureAddonRequest is the input for updating an addon's catalog configuration.
// Only non-nil/non-zero fields are applied (partial update / merge semantics).
type ConfigureAddonRequest struct {
	Name              string                   `json:"name"`                         // required — identifies which addon
	Version           string                   `json:"version,omitempty"`            // update chart version
	SyncWave          *int                     `json:"sync_wave,omitempty"`          // update sync wave
	SelfHeal          *bool                    `json:"self_heal,omitempty"`          // update self-heal
	SyncOptions       []string                 `json:"sync_options,omitempty"`       // replace sync options
	AdditionalSources []models.AddonSource     `json:"additional_sources,omitempty"` // replace additional sources
	IgnoreDifferences []map[string]interface{} `json:"ignore_differences,omitempty"` // replace ignore differences
	ExtraHelmValues   map[string]string        `json:"extra_helm_values,omitempty"`  // replace extra helm values
}
```

Note: `SyncWave` is `*int` (pointer) to distinguish "not provided" (nil) from "set to zero" (0).

- [ ] **Step 2: Create ConfigureAddon method**

Create `internal/orchestrator/addon_configure.go`:

```go
package orchestrator

import (
	"context"
	"fmt"
	"path"

	"github.com/MoranWeissman/sharko/internal/models"
	"gopkg.in/yaml.v3"
)

// ConfigureAddon updates an existing addon's catalog entry with the provided fields.
// Only non-nil/non-zero fields in the request are applied (merge semantics).
func (o *Orchestrator) ConfigureAddon(ctx context.Context, req ConfigureAddonRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	gp := o.git
	catalogPath := path.Join(o.paths.Charts, req.Name, "addon.yaml")

	// Read current catalog entry from Git
	data, err := gp.GetFileContent(ctx, o.gitOps.BaseBranch, catalogPath)
	if err != nil {
		return nil, fmt.Errorf("addon %q not found in catalog: %w", req.Name, err)
	}

	var entry models.AddonCatalogEntry
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("parsing addon %q catalog entry: %w", req.Name, err)
	}

	// Merge: only update fields that are provided
	if req.Version != "" {
		entry.Version = req.Version
	}
	if req.SyncWave != nil {
		entry.SyncWave = *req.SyncWave
	}
	if req.SelfHeal != nil {
		entry.SelfHeal = req.SelfHeal
	}
	if req.SyncOptions != nil {
		entry.SyncOptions = req.SyncOptions
	}
	if req.AdditionalSources != nil {
		entry.AdditionalSources = req.AdditionalSources
	}
	if req.IgnoreDifferences != nil {
		entry.IgnoreDifferences = req.IgnoreDifferences
	}
	if req.ExtraHelmValues != nil {
		entry.ExtraHelmValues = req.ExtraHelmValues
	}

	// Marshal back to YAML
	updatedData, err := yaml.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("serializing addon %q: %w", req.Name, err)
	}

	header := fmt.Sprintf("# Addon catalog entry for %s\n", req.Name)
	files := map[string][]byte{
		catalogPath: append([]byte(header), updatedData...),
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("configure addon %s", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing addon %q configuration: %w", req.Name, err)
	}

	return gitResult, nil
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/addon_configure.go internal/orchestrator/types.go
git commit -m "feat: add ConfigureAddon orchestrator method with merge semantics"
```

---

### Task 2: PATCH API Endpoint

**Files:**
- Modify: `internal/api/addons_write.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add handleConfigureAddon handler**

In `internal/api/addons_write.go`, add after `handleRemoveAddon`:

```go
// handleConfigureAddon godoc
//
// @Summary Configure addon
// @Description Updates an addon's catalog configuration. Only provided fields are modified (merge semantics).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body orchestrator.ConfigureAddonRequest true "Configuration update"
// @Success 200 {object} map[string]interface{} "Addon configured"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name} [patch]
func (s *Server) handleConfigureAddon(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
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

	var req orchestrator.ConfigureAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Name = name // path param takes precedence

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.ConfigureAddon(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
```

Add `"strings"` to the imports if not already present.

- [ ] **Step 2: Register route**

In `internal/api/router.go`, find the addons routes section and add:

```go
mux.HandleFunc("PATCH /api/v1/addons/{name}", s.requireAuth(s.handleConfigureAddon))
```

Place it near the existing `DELETE /api/v1/addons/{name}` route.

- [ ] **Step 3: Verify build + tests**

```bash
go build ./...
go test ./internal/api/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/addons_write.go internal/api/router.go
git commit -m "feat: add PATCH /api/v1/addons/{name} endpoint for addon configuration"
```

---

### Task 3: CLI Commands — configure-addon + describe-addon

**Files:**
- Modify: `cmd/sharko/addon.go`

- [ ] **Step 1: Add configure-addon command**

Add to `cmd/sharko/addon.go`, after the `removeAddonCmd` block:

```go
func init() {
	// ... existing init code ...

	configureAddonCmd.Flags().String("version", "", "Update chart version")
	configureAddonCmd.Flags().Int("sync-wave", 0, "Deployment ordering (-2=early, 0=default, 2=late)")
	configureAddonCmd.Flags().String("self-heal", "", "Auto-revert manual changes (true/false)")
	configureAddonCmd.Flags().StringSlice("sync-option", nil, "ArgoCD sync option (repeatable)")
	configureAddonCmd.Flags().String("ignore-differences", "", "ArgoCD ignoreDifferences JSON array")
	configureAddonCmd.Flags().StringSlice("extra-helm-value", nil, "Extra Helm parameter as key=value (repeatable)")
	configureAddonCmd.Flags().String("add-source", "", "Additional source JSON (repeatable)")
	rootCmd.AddCommand(configureAddonCmd)

	rootCmd.AddCommand(describeAddonCmd)
}
```

Wait — the `init()` function already exists at the top of the file. Add the new commands to the existing `init()`.

```go
var configureAddonCmd = &cobra.Command{
	Use:   "configure-addon <name>",
	Short: "Update addon configuration in the catalog",
	Long: `Update advanced configuration for an addon. Only provided flags are modified.

Examples:
  # Set deployment ordering
  sharko configure-addon istio-base --sync-wave -1

  # Enable server-side apply
  sharko configure-addon kyverno --sync-option ServerSideApply=true

  # Disable self-heal for manual hotfixes
  sharko configure-addon prometheus --self-heal=false

  # Update version
  sharko configure-addon cert-manager --version 1.15.0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		body := map[string]interface{}{}

		if v, _ := cmd.Flags().GetString("version"); v != "" {
			body["version"] = v
		}
		if cmd.Flags().Changed("sync-wave") {
			sw, _ := cmd.Flags().GetInt("sync-wave")
			body["sync_wave"] = sw
		}
		if v, _ := cmd.Flags().GetString("self-heal"); v != "" {
			body["self_heal"] = v == "true"
		}
		if opts, _ := cmd.Flags().GetStringSlice("sync-option"); len(opts) > 0 {
			body["sync_options"] = opts
		}
		if v, _ := cmd.Flags().GetString("ignore-differences"); v != "" {
			var parsed []map[string]interface{}
			if err := json.Unmarshal([]byte(v), &parsed); err != nil {
				return fmt.Errorf("invalid --ignore-differences JSON: %w", err)
			}
			body["ignore_differences"] = parsed
		}
		if pairs, _ := cmd.Flags().GetStringSlice("extra-helm-value"); len(pairs) > 0 {
			vals := map[string]string{}
			for _, p := range pairs {
				parts := strings.SplitN(p, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid --extra-helm-value %q (expected key=value)", p)
				}
				vals[parts[0]] = parts[1]
			}
			body["extra_helm_values"] = vals
		}

		if len(body) == 0 {
			return fmt.Errorf("no configuration flags provided — use --help to see options")
		}

		fmt.Printf("Configuring addon %s... ", name)
		respBody, status, err := apiRequest("PATCH", "/api/v1/addons/"+url.PathEscape(name), body)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		fmt.Println("done")
		var result struct {
			PRUrl string `json:"pr_url"`
		}
		if err := json.Unmarshal(respBody, &result); err == nil && result.PRUrl != "" {
			fmt.Printf("  PR: %s\n", result.PRUrl)
		}
		return nil
	},
}

var describeAddonCmd = &cobra.Command{
	Use:   "describe-addon <name>",
	Short: "Show full addon configuration including defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiGet("/api/v1/addons/" + url.PathEscape(name) + "/detail")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var detail struct {
			Addon struct {
				AddonName         string                   `json:"addon_name"`
				Chart             string                   `json:"chart"`
				RepoURL           string                   `json:"repo_url"`
				Version           string                   `json:"version"`
				Namespace         string                   `json:"namespace"`
				SyncWave          int                      `json:"syncWave"`
				SelfHeal          *bool                    `json:"selfHeal"`
				SyncOptions       []string                 `json:"syncOptions"`
				AdditionalSources []interface{}            `json:"additionalSources"`
				IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences"`
				ExtraHelmValues   map[string]string        `json:"extraHelmValues"`
			} `json:"addon"`
		}
		if err := json.Unmarshal(respBody, &detail); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		a := detail.Addon
		fmt.Printf("Addon: %s\n", a.AddonName)
		fmt.Printf("  Chart:     %s\n", a.Chart)
		fmt.Printf("  Repo:      %s\n", a.RepoURL)
		fmt.Printf("  Version:   %s\n", a.Version)
		ns := a.Namespace
		if ns == "" {
			ns = a.AddonName + " (default)"
		}
		fmt.Printf("  Namespace: %s\n", ns)
		fmt.Printf("  Sync Wave: %d\n", a.SyncWave)
		selfHeal := "true (default)"
		if a.SelfHeal != nil && !*a.SelfHeal {
			selfHeal = "false"
		}
		fmt.Printf("  Self-Heal: %s\n", selfHeal)
		if len(a.SyncOptions) > 0 {
			fmt.Printf("  Sync Options: %s\n", strings.Join(a.SyncOptions, ", "))
		}
		if len(a.IgnoreDifferences) > 0 {
			diffJSON, _ := json.MarshalIndent(a.IgnoreDifferences, "    ", "  ")
			fmt.Printf("  Ignore Differences:\n    %s\n", string(diffJSON))
		}
		if len(a.ExtraHelmValues) > 0 {
			fmt.Printf("  Extra Helm Values:\n")
			for k, v := range a.ExtraHelmValues {
				fmt.Printf("    %s = %s\n", k, v)
			}
		}
		if len(a.AdditionalSources) > 0 {
			fmt.Printf("  Additional Sources: %d\n", len(a.AdditionalSources))
		}
		return nil
	},
}
```

- [ ] **Step 2: Register commands in init()**

In the existing `init()` at the top of the file, add:

```go
// configure-addon flags
configureAddonCmd.Flags().String("version", "", "Update chart version")
configureAddonCmd.Flags().Int("sync-wave", 0, "Deployment ordering")
configureAddonCmd.Flags().String("self-heal", "", "Auto-revert (true/false)")
configureAddonCmd.Flags().StringSlice("sync-option", nil, "Sync option (repeatable)")
configureAddonCmd.Flags().String("ignore-differences", "", "ignoreDifferences JSON")
configureAddonCmd.Flags().StringSlice("extra-helm-value", nil, "key=value (repeatable)")
rootCmd.AddCommand(configureAddonCmd)
rootCmd.AddCommand(describeAddonCmd)
```

Wait — the flags are already set in the command definition's struct. Actually, Cobra flags should be set in `init()`, not inline. Let me check the existing pattern in the file.

Looking at the current code, flags ARE set in `init()`. So move the flag setup to `init()` and keep the command struct definitions clean. Follow the existing pattern.

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Test CLI help**

```bash
go run ./cmd/sharko configure-addon --help
go run ./cmd/sharko describe-addon --help
```

- [ ] **Step 5: Commit**

```bash
git add cmd/sharko/addon.go
git commit -m "feat: add configure-addon and describe-addon CLI commands"
```

---

### Task 4: Swagger + Quality Gates

- [ ] **Step 1: Regenerate swagger**

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

- [ ] **Step 2: Full quality gates**

```bash
go build ./...
go vet ./...
go test ./...
cd ui && npm run build && npm test -- --run
```

- [ ] **Step 3: Commit**

```bash
git add docs/swagger/
git commit -m "docs: regenerate swagger after PATCH endpoint + CLI commands"
```
