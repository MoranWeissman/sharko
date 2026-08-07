//go:build e2e

// Package lifecycle — v4 closing wave, lane 2, task #65: live-Gitea e2e suite.
//
// Every other GiteaProvider exercise in this repo talks to a fake (unit
// tests under internal/gitprovider/gitea_test.go hit an in-process
// httptest server that only stubs the Gitea REST surface). This file is
// the one place that drives internal/gitprovider.GiteaProvider against a
// REAL Gitea server, so it catches drift between the fake's stub shapes
// and what Gitea v0.23.2's SDK actually returns on the wire (pagination,
// merge-readiness polling, PR state fields, etc).
//
// It is fully opt-in and env-gated: with SHARKO_E2E_GITEA_URL unset, the
// single top-level test skips with one clear line and no other work
// happens, so `go test ./...` and `go test -tags e2e ./...` both stay
// hermetic — no network calls, no ambient config required.
//
// CI runs it via the `live-gitea` job in .github/workflows/e2e.yml: a
// `gitea/gitea` service container, a small provisioning step that creates
// an admin user + API token + an empty repo (auto-inited so a "main"
// branch exists), then `go test -tags e2e -run '^TestGiteaLiveWriteLoop$'`
// with the gate env vars exported. Locally, point SHARKO_E2E_GITEA_URL at
// any Gitea instance you control (`docker run -p 3000:3000 gitea/gitea`
// plus the same one-time setup) — see
// docs/site/developer-guide/e2e-testing.md for the full recipe.
//
// The write loop exercised, in order, matches the GitProvider interface's
// full write surface plus its two read methods:
//
//	TestConnection → CreateBranch → CreateOrUpdateFile (+ GetFileContent
//	round-trip, then an update-in-place round-trip) → BatchCreateFiles
//	(+ ListDirectory + GetFileContent reads) → CreatePullRequest →
//	GetPullRequestStatus ("open") → MergePullRequest →
//	GetPullRequestStatus ("merged") → DeleteBranch.
//
// Everything the suite creates (branch, files landed on the base branch
// via merge) is removed on a best-effort basis via t.Cleanup so repeat
// runs against a long-lived Gitea instance don't accumulate junk.
package lifecycle

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// Env vars that gate this suite. See docs/site/developer-guide/e2e-testing.md
// for the full contract (what CI sets, how to run locally).
const (
	giteaLiveURLEnv   = "SHARKO_E2E_GITEA_URL"   // required: base URL, e.g. http://localhost:3000
	giteaLiveTokenEnv = "SHARKO_E2E_GITEA_TOKEN" // required: API token with repo write access
	giteaLiveOwnerEnv = "SHARKO_E2E_GITEA_OWNER" // optional: defaults to giteaLiveDefaultOwner
	giteaLiveRepoEnv  = "SHARKO_E2E_GITEA_REPO"  // optional: defaults to giteaLiveDefaultRepo
	giteaLiveBaseEnv  = "SHARKO_E2E_GITEA_BASE"  // optional: defaults to giteaLiveDefaultBase

	giteaLiveDefaultOwner = "sharko-e2e"
	giteaLiveDefaultRepo  = "sharko-e2e-live"
	giteaLiveDefaultBase  = "main"
)

// giteaLiveConfig holds the resolved connection details for the live suite.
type giteaLiveConfig struct {
	baseURL string
	token   string
	owner   string
	repo    string
	base    string
}

// loadGiteaLiveConfig reads the gate env vars and skips the calling test
// with a clear one-line message when the required ones are unset. This is
// the ONLY skip path in the file — every subtest assumes the config already
// resolved successfully.
func loadGiteaLiveConfig(t *testing.T) *giteaLiveConfig {
	t.Helper()

	baseURL := os.Getenv(giteaLiveURLEnv)
	token := os.Getenv(giteaLiveTokenEnv)
	if baseURL == "" || token == "" {
		t.Skipf("live-Gitea e2e suite skipped: set %s and %s to a real Gitea instance to run it "+
			"(CI does this via the live-gitea job's gitea/gitea service container; "+
			"see docs/site/developer-guide/e2e-testing.md)", giteaLiveURLEnv, giteaLiveTokenEnv)
	}

	cfg := &giteaLiveConfig{
		baseURL: baseURL,
		token:   token,
		owner:   os.Getenv(giteaLiveOwnerEnv),
		repo:    os.Getenv(giteaLiveRepoEnv),
		base:    os.Getenv(giteaLiveBaseEnv),
	}
	if cfg.owner == "" {
		cfg.owner = giteaLiveDefaultOwner
	}
	if cfg.repo == "" {
		cfg.repo = giteaLiveDefaultRepo
	}
	if cfg.base == "" {
		cfg.base = giteaLiveDefaultBase
	}
	return cfg
}

// giteaLiveRunSuffix returns a short, human-readable-enough tag so repeated
// runs against the same long-lived Gitea instance don't collide on branch
// or file names. Not cryptographically unique — collision odds are fine for
// a test suite that also cleans up after itself.
func giteaLiveRunSuffix() string {
	return fmt.Sprintf("%d%d", time.Now().Unix()%1000000, rand.Intn(10000))
}

// TestGiteaLiveWriteLoop drives internal/gitprovider.GiteaProvider's full
// write surface against a real Gitea server. See the package doc comment
// above for the exact call sequence and the env-var gate contract.
func TestGiteaLiveWriteLoop(t *testing.T) {
	cfg := loadGiteaLiveConfig(t)

	provider, err := gitprovider.NewGiteaProvider(cfg.baseURL, cfg.owner, cfg.repo, cfg.token)
	if err != nil {
		t.Fatalf("NewGiteaProvider(%s, %s/%s): %v", cfg.baseURL, cfg.owner, cfg.repo, err)
	}

	ctx := context.Background()
	suffix := giteaLiveRunSuffix()
	branch := "sharko-e2e-" + suffix

	t.Run("TestConnection", func(t *testing.T) {
		if err := provider.TestConnection(ctx); err != nil {
			t.Fatalf("TestConnection: %v (is %s reachable, and does %s/%s exist?)",
				err, cfg.baseURL, cfg.owner, cfg.repo)
		}
	})

	t.Run("CreateBranch", func(t *testing.T) {
		if err := provider.CreateBranch(ctx, branch, cfg.base); err != nil {
			t.Fatalf("CreateBranch(%s from %s): %v", branch, cfg.base, err)
		}
	})
	// Best-effort branch cleanup. Registered right after the branch exists
	// so a later subtest failure still leaves the live repo tidy. The
	// explicit DeleteBranch subtest near the end covers the happy path;
	// this is the safety net for the unhappy one. A double-delete after the
	// happy path just 404s, which is logged and ignored.
	t.Cleanup(func() {
		if err := provider.DeleteBranch(context.Background(), branch); err != nil {
			t.Logf("cleanup: DeleteBranch(%s): %v (best effort, ignored)", branch, err)
		}
	})

	singlePath := fmt.Sprintf("sharko-e2e/%s/single.txt", suffix)
	singleContent := []byte("sharko live-gitea e2e — single file @ " + suffix)

	t.Run("CreateOrUpdateFile_and_GetFileContent", func(t *testing.T) {
		if err := provider.CreateOrUpdateFile(ctx, singlePath, singleContent, branch, "sharko e2e: add single file"); err != nil {
			t.Fatalf("CreateOrUpdateFile(create): %v", err)
		}
		got, err := provider.GetFileContent(ctx, singlePath, branch)
		if err != nil {
			t.Fatalf("GetFileContent: %v", err)
		}
		if string(got) != string(singleContent) {
			t.Fatalf("GetFileContent round-trip mismatch: got %q want %q", got, singleContent)
		}

		// Exercise the "update" half of CreateOrUpdateFile and confirm the
		// round-trip sees the new content, not a cached/stale copy.
		updated := append(append([]byte{}, singleContent...), []byte(" (updated)")...)
		if err := provider.CreateOrUpdateFile(ctx, singlePath, updated, branch, "sharko e2e: update single file"); err != nil {
			t.Fatalf("CreateOrUpdateFile(update): %v", err)
		}
		got, err = provider.GetFileContent(ctx, singlePath, branch)
		if err != nil {
			t.Fatalf("GetFileContent(after update): %v", err)
		}
		if string(got) != string(updated) {
			t.Fatalf("GetFileContent(after update) mismatch: got %q want %q", got, updated)
		}
		singleContent = updated
	})

	batchDir := fmt.Sprintf("sharko-e2e/%s/batch", suffix)
	batchFiles := map[string][]byte{
		batchDir + "/a.txt": []byte("sharko live-gitea e2e — batch file a @ " + suffix),
		batchDir + "/b.txt": []byte("sharko live-gitea e2e — batch file b @ " + suffix),
	}

	t.Run("BatchCreateFiles_ListDirectory_GetFileContent", func(t *testing.T) {
		if err := provider.BatchCreateFiles(ctx, batchFiles, branch, "sharko e2e: batch write"); err != nil {
			t.Fatalf("BatchCreateFiles: %v", err)
		}

		names, err := provider.ListDirectory(ctx, batchDir, branch)
		if err != nil {
			t.Fatalf("ListDirectory(%s): %v", batchDir, err)
		}
		wantNames := map[string]bool{"a.txt": false, "b.txt": false}
		for _, n := range names {
			if _, ok := wantNames[n]; ok {
				wantNames[n] = true
			}
		}
		for name, seen := range wantNames {
			if !seen {
				t.Errorf("ListDirectory(%s) missing expected entry %q; got %v", batchDir, name, names)
			}
		}

		for path, content := range batchFiles {
			got, err := provider.GetFileContent(ctx, path, branch)
			if err != nil {
				t.Fatalf("GetFileContent(%s): %v", path, err)
			}
			if string(got) != string(content) {
				t.Fatalf("GetFileContent(%s) mismatch: got %q want %q", path, got, content)
			}
		}
	})

	// Best-effort cleanup for the files landed on the BASE branch by the
	// merge below. Registered before the merge happens so it still runs
	// (and just no-ops via 404-ignored) if the merge subtest never gets
	// there.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for path := range batchFiles {
			if err := provider.DeleteFile(cleanupCtx, path, cfg.base, "sharko e2e: cleanup batch file"); err != nil {
				t.Logf("cleanup: DeleteFile(%s): %v (best effort, ignored)", path, err)
			}
		}
		if err := provider.DeleteFile(cleanupCtx, singlePath, cfg.base, "sharko e2e: cleanup single file"); err != nil {
			t.Logf("cleanup: DeleteFile(%s): %v (best effort, ignored)", singlePath, err)
		}
	})

	var prNumber int
	t.Run("CreatePullRequest", func(t *testing.T) {
		pr, err := provider.CreatePullRequest(ctx,
			"sharko e2e: live write loop "+suffix,
			"Opened by the sharko live-Gitea e2e suite (TestGiteaLiveWriteLoop). "+
				"Safe to close manually if this test failed mid-run — it is merged "+
				"and cleaned up automatically on a normal pass.",
			branch, cfg.base)
		if err != nil {
			t.Fatalf("CreatePullRequest: %v", err)
		}
		if pr == nil || pr.ID == 0 {
			t.Fatalf("CreatePullRequest: returned no usable PR: %+v", pr)
		}
		if pr.Status != "open" {
			t.Errorf("CreatePullRequest: Status = %q, want %q", pr.Status, "open")
		}
		if pr.SourceBranch != branch || pr.TargetBranch != cfg.base {
			t.Errorf("CreatePullRequest: SourceBranch/TargetBranch = %q/%q, want %q/%q",
				pr.SourceBranch, pr.TargetBranch, branch, cfg.base)
		}
		prNumber = pr.ID
	})
	if prNumber == 0 {
		t.Fatal("CreatePullRequest subtest did not produce a PR number — cannot continue the write loop")
	}

	t.Run("GetPullRequestStatus_open", func(t *testing.T) {
		status, err := provider.GetPullRequestStatus(ctx, prNumber)
		if err != nil {
			t.Fatalf("GetPullRequestStatus(#%d): %v", prNumber, err)
		}
		if status != "open" {
			t.Fatalf("GetPullRequestStatus(#%d) = %q, want %q", prNumber, status, "open")
		}
	})

	t.Run("MergePullRequest_and_status", func(t *testing.T) {
		// GiteaProvider.MergePullRequest already polls for mergeability
		// internally (Gitea computes it asynchronously after PR creation);
		// give it plenty of headroom on a possibly-cold CI service
		// container rather than inheriting the outer test timeout.
		mergeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if err := provider.MergePullRequest(mergeCtx, prNumber); err != nil {
			t.Fatalf("MergePullRequest(#%d): %v", prNumber, err)
		}
		status, err := provider.GetPullRequestStatus(ctx, prNumber)
		if err != nil {
			t.Fatalf("GetPullRequestStatus(#%d, after merge): %v", prNumber, err)
		}
		if status != "merged" {
			t.Fatalf("GetPullRequestStatus(#%d) after merge = %q, want %q", prNumber, status, "merged")
		}
	})

	t.Run("DeleteBranch", func(t *testing.T) {
		if err := provider.DeleteBranch(ctx, branch); err != nil {
			t.Fatalf("DeleteBranch(%s): %v", branch, err)
		}
	})
}
