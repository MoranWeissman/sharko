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
	// /health returns a mixed-type body (strings + bool capability flags
	// like cluster_test_available — BUG-041), so decode as map[string]any
	// to match the handler contract in internal/api/health.go. Decoding as
	// map[string]string previously panicked the test on the bool field
	// (ZG1-A.265).
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health body: %v", err)
	}
	if status, _ := body["status"].(string); status != "healthy" {
		t.Errorf("/health status field: got %v want %q", body["status"], "healthy")
	}
	if version, _ := body["version"].(string); version == "" {
		t.Errorf("/health version field is empty or non-string (want %q): got %v", "e2e", body["version"])
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

// TestHarnessSharkoHealthResponseShape pins the /api/v1/health response
// shape so a future handler rename / type change in internal/api/health.go
// breaks loudly at the harness boundary rather than silently flipping
// other e2e suites to confusing decode errors. Asserts:
//   - status and version are strings
//   - mode is a string
//   - cluster_test_available is a bool (BUG-041 capability flag)
//
// Pairs with internal/api/health_test.go which covers the handler in
// isolation; this test guards the contract over the live HTTP boundary.
// Regression target for ZG1-A.265.
func TestHarnessSharkoHealthResponseShape(t *testing.T) {
	git := StartGitFake(t)
	sharko := StartSharko(t, SharkoConfig{
		Mode:    SharkoModeInProcess,
		GitFake: git,
	})
	sharko.WaitHealthy(t, 10*time.Second)

	resp, err := http.Get(sharko.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/health: status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health body: %v", err)
	}

	// Required string fields.
	for _, field := range []string{"status", "version", "mode"} {
		v, ok := body[field]
		if !ok {
			t.Errorf("/health missing required field %q; got keys: %v", field, mapKeys(body))
			continue
		}
		if _, isString := v.(string); !isString {
			t.Errorf("/health field %q: got %T (%v), want string", field, v, v)
		}
	}

	// Required bool capability flag (BUG-041).
	v, ok := body["cluster_test_available"]
	if !ok {
		t.Errorf("/health missing required field %q; got keys: %v", "cluster_test_available", mapKeys(body))
	} else if _, isBool := v.(bool); !isBool {
		t.Errorf("/health field %q: got %T (%v), want bool", "cluster_test_available", v, v)
	}
}

// mapKeys returns the keys of m as a slice. Tiny helper for clearer
// diagnostics in shape-mismatch failures (handler likely renamed a field).
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
