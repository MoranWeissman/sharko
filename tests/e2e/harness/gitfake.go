//go:build e2e

// Package harness — gitfake test API.
//
// This file provides the `*testing.T`-coupled constructors and helpers for
// the in-process GitFake. The core type + HTTP handlers live in
// gitfake_core.go (no build tag) so the standalone gitfake-server binary
// at tests/e2e/harness/gitfake/cmd/gitfake-server can reuse them.
//
// V125-1-13.x.1: split out so containerised mode can serve the same
// git-protocol surface from a kind Pod without dragging in `testing`.
package harness

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// StartGitFake spins up an in-memory git server with one bare-style repo.
// The repo is initialized with a single empty "init" commit on `main` so
// that sharko's "is this repo initialized" probe finds a HEAD.
//
// Auto-cleanup is registered via t.Cleanup; the server is stopped and
// in-memory storage discarded automatically at test end.
//
// Calls t.Fatalf on any setup failure — does not return on error.
func StartGitFake(t *testing.T) *GitFake {
	t.Helper()

	g, err := NewGitFakeForServer(defaultRepoName, "main")
	if err != nil {
		t.Fatalf("StartGitFake: %v", err)
	}

	if err := g.startInProcessHTTP(); err != nil {
		t.Fatalf("StartGitFake: %v", err)
	}

	t.Cleanup(func() { g.Close() })
	t.Logf("harness: gitfake serving %s (in-memory bare repo, single seed commit on main)", g.RepoURL)
	return g
}

// CommitFile writes content to the in-memory worktree at path, stages it,
// and commits with the given message on the `main` branch. Returns the new
// commit's SHA. Useful for seeding fixtures or asserting downstream
// behaviour against a known starting state.
//
// Calls t.Fatalf on failure.
func (g *GitFake) CommitFile(t *testing.T, path, content, message string) string {
	t.Helper()
	hash, err := g.SeedFile(path, content, message)
	if err != nil {
		t.Fatalf("GitFake.CommitFile: %v", err)
	}
	return hash
}

// LatestCommit returns the SHA at HEAD on the given branch (default "main"
// when branch is empty).
//
// Calls t.Fatalf if the branch is missing or unreadable.
func (g *GitFake) LatestCommit(t *testing.T, branch string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.LatestCommit: gitfake is closed")
	}
	if branch == "" {
		branch = "main"
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("GitFake.LatestCommit: resolve branch %q: %v", branch, err)
	}
	return ref.Hash().String()
}

// FileAt returns the content of path at branch's HEAD. Used by tests to
// assert that sharko committed the expected YAML.
//
// Calls t.Fatalf if the branch, commit, or file is missing.
func (g *GitFake) FileAt(t *testing.T, branch, path string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.FileAt: gitfake is closed")
	}
	if branch == "" {
		branch = "main"
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("GitFake.FileAt: resolve branch %q: %v", branch, err)
	}
	commit, err := g.repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("GitFake.FileAt: commit %s: %v", ref.Hash(), err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("GitFake.FileAt: tree: %v", err)
	}
	file, err := tree.File(path)
	if err != nil {
		t.Fatalf("GitFake.FileAt: file %q at %s on %s: %v", path, ref.Hash(), branch, err)
	}
	body, err := file.Contents()
	if err != nil {
		t.Fatalf("GitFake.FileAt: read %q: %v", path, err)
	}
	return body
}

// ListBranches returns all branch names in the repository, sorted.
// Tests use this to assert that sharko opened (or closed) a feature branch
// — our PR fake represents PRs as branches with a known prefix.
//
// Calls t.Fatalf on iteration failure (in practice, only on closed gitfake).
func (g *GitFake) ListBranches(t *testing.T) []string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.ListBranches: gitfake is closed")
	}
	iter, err := g.repo.Branches()
	if err != nil {
		t.Fatalf("GitFake.ListBranches: branches iter: %v", err)
	}
	var out []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, ref.Name().Short())
		return nil
	}); err != nil {
		t.Fatalf("GitFake.ListBranches: foreach: %v", err)
	}
	return out
}
