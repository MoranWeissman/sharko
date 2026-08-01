package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// InitRepo scaffolds the addons repository with the v4 bootstrap seed
// (design doc §1 / v4 Wave 1 Story 4.2: empty data folders, the engine
// pin, and a README — nothing else), commits it via a PR (using
// commitChanges), optionally registers the repo in ArgoCD by applying the
// engine pin, and polls for sync verification.
func (o *Orchestrator) InitRepo(ctx context.Context, req InitRepoRequest) (*InitRepoResult, error) {
	if o.gitops.RepoURL == "" {
		return nil, fmt.Errorf("git repo URL is required for init — set SHARKO_GITOPS_REPO_URL")
	}

	// Step 1 — Check if repo is already initialized. The engine pin is the
	// v4 seed's one moving part and BootstrapRootAppPath is the single
	// source of truth for where it lives — same check the async
	// (CollectBootstrapFiles-driven) path uses via isPRMerged.
	if _, err := o.git.GetFileContent(ctx, BootstrapRootAppPath, o.gitops.BaseBranch); err == nil {
		return nil, fmt.Errorf("repo already initialized: %s exists", BootstrapRootAppPath)
	}

	// Step 1b — a v3 repo has no engine pin, so the check above passes and
	// this seed would drop the whole v4 folder tree on top of a live v3
	// repo. Refuse: the v3 -> v4 migration is its own operation (Wave 2's
	// takeover work), never a side effect of re-running Initialize.
	if o.hasV3Markers(ctx) {
		return nil, fmt.Errorf(
			"this repo is already set up in the older (v3) layout — %s or %s is present. Initializing would write the new layout on top of it. Moving a v3 repo across is its own operation, coming with the takeover work",
			V3BootstrapMarkerPath, V3SecondaryMarkerPath)
	}

	// Step 2 — Build the v4 seed: empty data folders + the engine pin +
	// README. Nothing else — no Helm chart, no catalog seed, no per-addon
	// values stubs (design doc §1, "What the bootstrap PR actually
	// contains").
	files := BuildV4SeedFiles(o.gitops, o.paths)

	// Step 3 — Commit all files via PR (using commitChanges with shared mutex).
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, "initialize repository",
		o.prMeta(req.AutoMerge, "init-repo", "Initialize repository", "", ""))
	if err != nil {
		return nil, fmt.Errorf("committing init files: %w", err)
	}

	filesCreated := make([]string, 0, len(files))
	for path := range files {
		filesCreated = append(filesCreated, path)
	}

	result := &InitRepoResult{
		Status: "success",
		Repo: &InitRepoInfo{
			URL:          o.gitops.RepoURL,
			Branch:       gitResult.Branch,
			FilesCreated: filesCreated,
			PRUrl:        gitResult.PRUrl,
			PRID:         gitResult.PRID,
			Merged:       gitResult.Merged,
		},
	}

	if req.BootstrapArgoCD && o.argocd != nil {
		// Step 4 — Add repository to ArgoCD.
		if req.GitUsername != "" && req.GitToken != "" {
			if addRepoErr := o.argocd.AddRepository(ctx, o.gitops.RepoURL, req.GitUsername, req.GitToken); addRepoErr != nil {
				result.ArgoCD = &InitArgocdInfo{
					Bootstrapped: false,
					RootApp:      fmt.Sprintf("failed to add repository to ArgoCD: %v", addRepoErr),
				}
				result.Status = "partial"
				return result, nil
			}
		}

		// Step 5 & 6 — Apply the engine pin (design doc §2.5). v4 has no
		// separate AppProject to create here: sharko-engine.yaml's
		// spec.project is "default", and the engine chart's own render
		// creates the shared "sharko-addons" AppProject
		// (charts/sharko-engine/templates/project.yaml) the first time
		// ArgoCD syncs it — one Application is the whole of what bootstrap
		// applies through the ArgoCD API.
		rootAppContent := files[BootstrapRootAppPath]

		bootstrapErr := o.bootstrapArgoCD(ctx, rootAppContent)
		if bootstrapErr != nil {
			result.ArgoCD = &InitArgocdInfo{
				Bootstrapped: false,
				RootApp:      fmt.Sprintf("bootstrap failed: %v", bootstrapErr),
			}
			result.Status = "partial"
			return result, nil
		}

		result.ArgoCD = &InitArgocdInfo{
			Bootstrapped: true,
			RootApp:      BootstrapRootAppName,
		}

		// Step 7 — Poll for sync verification (up to 2 minutes).
		syncStatus, syncErr := o.waitForSync(ctx, BootstrapRootAppName, 2*time.Minute)
		result.ArgoCD.SyncStatus = syncStatus
		result.ArgoCD.SyncError = syncErr
		if syncStatus != "synced" {
			// A sync timeout/failure must surface as a non-nil error so the
			// operations framework marks the operation as `failed` and the
			// first-run wizard renders the actual cause instead of a
			// generic success message. The caller may still inspect
			// `result` for partial info.
			result.Status = "syncing"
			detail := syncStatus
			if syncErr != "" {
				detail = syncStatus + ": " + syncErr
			}
			return result, fmt.Errorf("argocd application %q did not reach synced state: %s",
				BootstrapRootAppName, detail)
		}
	}

	return result, nil
}

// waitForSync polls ArgoCD for an application's sync/health status.
// Returns the final status ("synced", "failed", "timeout") and an optional error message.
func (o *Orchestrator) waitForSync(ctx context.Context, appName string, timeout time.Duration) (string, string) {
	check := func() (string, string, bool) {
		app, err := o.argocd.GetApplication(ctx, appName)
		if err != nil {
			return "", "", false
		}
		if app.SyncStatus == "Synced" && app.HealthStatus == "Healthy" {
			return "synced", "", true
		}
		if app.SyncStatus == "OutOfSync" && app.HealthStatus == "Degraded" {
			return "failed", "application sync failed", true
		}
		return "", "", false
	}

	// Immediate first check before entering the polling loop.
	if status, msg, done := check(); done {
		return status, msg
	}

	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return "timeout", "sync verification timed out after " + timeout.String()
		case <-ctx.Done():
			return "timeout", "context cancelled"
		case <-ticker.C:
			if status, msg, done := check(); done {
				return status, msg
			}
		}
	}
}

// bootstrapArgoCD parses the root-app.yaml (multi-document YAML with AppProject + Application)
// and applies each resource to ArgoCD via the API.
func (o *Orchestrator) bootstrapArgoCD(ctx context.Context, rootAppYAML []byte) error {
	// Split on YAML document separator
	docs := bytes.Split(rootAppYAML, []byte("\n---"))

	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		// Parse YAML to determine the kind
		var resource map[string]interface{}
		if err := yaml.Unmarshal(doc, &resource); err != nil {
			return fmt.Errorf("parsing YAML document: %w", err)
		}

		kind, _ := resource["kind"].(string)

		// Convert to JSON for the ArgoCD REST API
		jsonData, err := json.Marshal(resource)
		if err != nil {
			return fmt.Errorf("converting %s to JSON: %w", kind, err)
		}

		switch kind {
		case "AppProject":
			if err := o.argocd.CreateProject(ctx, jsonData); err != nil {
				return fmt.Errorf("creating AppProject: %w", err)
			}
		case "Application":
			if err := o.argocd.CreateApplication(ctx, jsonData); err != nil {
				return fmt.Errorf("creating Application: %w", err)
			}
		default:
			return fmt.Errorf("unexpected resource kind %q in root-app.yaml", kind)
		}
	}

	return nil
}

// ─── Exported step-by-step helpers for the async init flow ───────────────────
//
// These methods expose individual phases of the init workflow so that the API
// handler can drive them one step at a time, recording progress in the
// operations store between each step.

// CollectBootstrapFiles returns the ready-to-commit v4 bootstrap seed:
// empty data folders, the engine pin, and a README — nothing else (design
// doc §1 / Story 4.2). This is the async init flow's Step 1; the API
// handler (runInitOperation) drives CollectBootstrapFiles →
// CommitBootstrapFiles → CreateInitPR → (wait for merge) →
// ReadRootAppTemplate → BootstrapArgoCD → WaitForSync one step at a time,
// recording progress between each.
func (o *Orchestrator) CollectBootstrapFiles(_ context.Context) (map[string][]byte, error) {
	if o.gitops.RepoURL == "" {
		return nil, fmt.Errorf("git repo URL is required — set SHARKO_GITOPS_REPO_URL")
	}
	return BuildV4SeedFiles(o.gitops, o.paths), nil
}

// CommitBootstrapFiles creates a uniquely-named branch and commits the given
// files to it. Returns the branch name. Does NOT create a PR.
func (o *Orchestrator) CommitBootstrapFiles(ctx context.Context, files map[string][]byte) (string, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	o.detectConflicts(ctx, files)

	branchName := fmt.Sprintf("%sinitialize-repository-%s", o.gitops.BranchPrefix, initBranchSuffix())

	if err := o.git.CreateBranch(ctx, branchName, o.gitops.BaseBranch); err != nil {
		return "", fmt.Errorf("creating branch %q: %w", branchName, err)
	}
	commitMsg := fmt.Sprintf("%s initialize repository", o.gitops.CommitPrefix)
	if err := o.git.BatchCreateFiles(ctx, files, branchName, commitMsg); err != nil {
		return "", fmt.Errorf("writing files on branch %q: %w", branchName, err)
	}
	return branchName, nil
}

// CreateInitPR opens a pull request for the given branch against the base
// branch. The caller is responsible for merging (or waiting for a human
// to merge).
//
// This path does NOT go through commitChanges (the bootstrap branch was
// already created by CommitBootstrapFiles), so the dashboard PR-tracker
// write is performed inline. Skipped silently when no tracker is wired.
func (o *Orchestrator) CreateInitPR(ctx context.Context, branch string) (*GitResult, error) {
	title := fmt.Sprintf("%s initialize repository", o.gitops.CommitPrefix)
	pr, err := o.git.CreatePullRequest(ctx, title, "initialize repository", branch, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("creating pull request: %w", err)
	}

	if o.prTracker != nil {
		_ = o.prTracker.TrackPR(ctx, TrackedPR{
			PRID:       pr.ID,
			PRUrl:      pr.URL,
			PRBranch:   branch,
			PRTitle:    "Initialize repository",
			PRBase:     o.gitops.BaseBranch,
			Operation:  "init-repo",
			User:       "system",
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	return &GitResult{
		PRUrl:  pr.URL,
		PRID:   pr.ID,
		Branch: branch,
	}, nil
}

// ReadRootAppTemplate reads the engine pin (sharko-engine.yaml, at
// BootstrapRootAppPath) back from the base branch — called after the
// bootstrap PR has merged, so this reads exactly what is now in git rather
// than re-deriving it. Unlike the v3 template path there is no
// placeholder substitution step: BuildV4SeedFiles already resolved every
// value (repo URL, branch, host cluster name) when it wrote the file.
func (o *Orchestrator) ReadRootAppTemplate(ctx context.Context) ([]byte, error) {
	content, err := o.git.GetFileContent(ctx, BootstrapRootAppPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading engine pin at %s: %w", BootstrapRootAppPath, err)
	}
	return content, nil
}

// BootstrapArgoCD is the exported counterpart of bootstrapArgoCD.
func (o *Orchestrator) BootstrapArgoCD(ctx context.Context, rootAppYAML []byte) error {
	return o.bootstrapArgoCD(ctx, rootAppYAML)
}

// WaitForSync is the exported counterpart of waitForSync.
func (o *Orchestrator) WaitForSync(ctx context.Context, appName string, timeout time.Duration) (string, string) {
	return o.waitForSync(ctx, appName, timeout)
}

// initBranchSuffix returns a short random hex string for init branch name uniqueness.
func initBranchSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
