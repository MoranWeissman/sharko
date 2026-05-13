//go:build e2e

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestHarnessGitFakeStandalone exercises the in-memory git fixture without
// booting Sharko. Smoke-tests CommitFile / FileAt / ListBranches so a
// regression in gitfake surfaces independently of the boot harness.
func TestHarnessGitFakeStandalone(t *testing.T) {
	git := StartGitFake(t)

	if git.Addr == "" || git.RepoURL == "" {
		t.Fatalf("StartGitFake: empty Addr/RepoURL (Addr=%q, RepoURL=%q)", git.Addr, git.RepoURL)
	}

	sha1 := git.CommitFile(t, "a.txt", "hello\n", "first")
	sha2 := git.CommitFile(t, "b.txt", "world\n", "second")
	if sha1 == "" || sha2 == "" {
		t.Fatalf("CommitFile returned empty sha (sha1=%q, sha2=%q)", sha1, sha2)
	}
	if sha1 == sha2 {
		t.Fatalf("commits should have distinct SHAs, got %s twice", sha1)
	}

	if got := git.FileAt(t, "main", "a.txt"); got != "hello\n" {
		t.Fatalf("FileAt(main, a.txt): got %q want %q", got, "hello\n")
	}
	if got := git.FileAt(t, "main", "b.txt"); got != "world\n" {
		t.Fatalf("FileAt(main, b.txt): got %q want %q", got, "world\n")
	}

	if head := git.LatestCommit(t, "main"); head != sha2 {
		t.Fatalf("LatestCommit(main): got %s want %s (second commit)", head, sha2)
	}

	branches := git.ListBranches(t)
	if len(branches) < 1 {
		t.Fatalf("expected at least 1 branch (main), got %v", branches)
	}
	foundMain := false
	for _, b := range branches {
		if b == "main" {
			foundMain = true
		}
	}
	if !foundMain {
		t.Fatalf("expected 'main' in branches, got %v", branches)
	}
}

// TestHarnessSharkoInProcess proves that StartSharko + StartGitFake combine
// to give a working API surface. Asserts /api/v1/health returns 200 with a
// version field; piggy-backs a gitfake CommitFile/FileAt round-trip to
// detect cross-harness regressions in a single failure point.
func TestHarnessSharkoInProcess(t *testing.T) {
	git := StartGitFake(t)
	sharko := StartSharko(t, SharkoConfig{
		Mode:    SharkoModeInProcess,
		GitFake: git,
	})
	sharko.WaitHealthy(t, 10*time.Second)

	// /api/v1/health — 200 + body has expected fields.
	resp, err := http.Get(sharko.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/health: status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health body: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("/health status field: got %q want %q", body["status"], "healthy")
	}
	if body["version"] == "" {
		t.Errorf("/health version field is empty (want %q)", "e2e")
	}

	// Sanity-check the credentials are populated for downstream stories.
	if sharko.AdminUser == "" || sharko.AdminPass == "" {
		t.Errorf("StartSharko: expected AdminUser/AdminPass populated, got %q/%q",
			sharko.AdminUser, sharko.AdminPass)
	}

	// Sanity-check the gitfake fixture is reachable from the test side.
	sha := git.CommitFile(t, "managed-clusters.yaml", "clusters: []\n", "init managed-clusters")
	if sha == "" {
		t.Fatal("git.CommitFile returned empty sha")
	}
	if got := git.FileAt(t, "main", "managed-clusters.yaml"); got != "clusters: []\n" {
		t.Fatalf("git.FileAt mismatch: got %q want %q", got, "clusters: []\n")
	}
}
