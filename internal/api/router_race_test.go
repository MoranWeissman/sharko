package api

import (
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestGitopsConfigConcurrentPublishAndRead exercises the GF2 race fix:
// ReinitializeFromConnection (called by the V3 C1 60s reclaim goroutine)
// hot-swaps gitops config fields via publishGitopsCfg while request handlers
// read them via gitopsConfig(). Under `go test -race` this must be clean.
func TestGitopsConfigConcurrentPublishAndRead(t *testing.T) {
	srv := newIsolatedTestServer(t)

	// Seed an active connection with initial gitops settings.
	autoMerge := true
	seedActiveConnection(t, srv, models.Connection{
		Name: "race-test-conn",
		Git:  models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "owner", Repo: "repo"},
		GitOps: &models.GitOpsSettings{
			BaseBranch:   "main",
			BranchPrefix: "sharko/",
			CommitPrefix: "sharko:",
			PRAutoMerge:  &autoMerge,
		},
	})

	srv.ReinitializeFromConnection()

	var wg sync.WaitGroup

	// Writer goroutine: repeatedly call ReinitializeFromConnection (simulates the 60s reclaim loop).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			srv.ReinitializeFromConnection()
		}
	}()

	// Reader goroutine: repeatedly read gitops config fields (simulates handler requests).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			cfg := srv.gitopsConfig()
			// Touch each field to ensure the race detector sees the read.
			_ = cfg.BaseBranch
			_ = cfg.BranchPrefix
			_ = cfg.CommitPrefix
			_ = cfg.PRAutoMerge
			_ = cfg.RepoURL
		}
	}()

	wg.Wait()

	// Final read to confirm the config is still valid.
	cfg := srv.gitopsConfig()
	if cfg.BaseBranch != "main" {
		t.Errorf("expected BaseBranch=main after concurrent access, got %q", cfg.BaseBranch)
	}
}

// TestGitopsConfigMultipleReaders exercises concurrent reads under -race
// (the accessor returns a value copy, so multiple simultaneous reads must be clean).
func TestGitopsConfigMultipleReaders(t *testing.T) {
	srv := newIsolatedTestServer(t)
	srv.publishGitopsCfg(orchestrator.GitOpsConfig{
		BaseBranch:   "develop",
		BranchPrefix: "feat/",
		CommitPrefix: "feat:",
		PRAutoMerge:  true,
		RepoURL:      "https://github.com/example/repo.git",
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cfg := srv.gitopsConfig()
				if cfg.BaseBranch != "develop" {
					t.Errorf("expected BaseBranch=develop, got %q", cfg.BaseBranch)
				}
			}
		}()
	}
	wg.Wait()
}
