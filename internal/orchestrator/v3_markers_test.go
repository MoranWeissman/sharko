package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// TestInitRepo_RefusesV3Repo — a v3 repo has no engine pin, so the
// already-initialized check passed and InitRepo would have written the whole
// v4 seed (five empty data folders + the engine pin + a README) on top of a
// live v3 repo. Moving a v3 repo across is its own operation; re-running
// Initialize must never be the thing that does it.
func TestInitRepo_RefusesV3Repo(t *testing.T) {
	for _, marker := range []string{V3BootstrapMarkerPath, V3SecondaryMarkerPath} {
		t.Run(marker, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[marker] = []byte("name: cluster-addons\n")
			gitops := defaultGitOps()
			gitops.RepoURL = "https://example.com/org/addons.git"
			orch := New(nil, nil, newMockArgocd(), git, gitops, defaultPaths(), nil)

			before := len(git.files)
			_, err := orch.InitRepo(context.Background(), InitRepoRequest{})
			if err == nil {
				t.Fatal("InitRepo returned nil on a v3 repo — the v4 seed would have landed on top of it")
			}
			if !strings.Contains(err.Error(), "older (v3) layout") {
				t.Errorf("error = %v, want the plain-words v3 refusal", err)
			}
			if len(git.files) != before {
				t.Errorf("InitRepo wrote %d file(s) despite refusing", len(git.files)-before)
			}
			if len(git.branches) != 0 || len(git.prs) != 0 {
				t.Errorf("InitRepo created branches=%v prs=%d despite refusing", git.branches, len(git.prs))
			}
		})
	}
}

// TestInitRepo_EmptyRepoStillProceeds — with neither an engine pin nor a v3
// marker the guard is inert and init runs exactly as before.
func TestInitRepo_EmptyRepoStillProceeds(t *testing.T) {
	git := newMockGitProvider()
	// newMockGitProvider seeds a v3 catalog file for the addon tests; clear
	// the file set so this repo is genuinely empty.
	git.files = map[string][]byte{}
	gitops := defaultGitOps()
	gitops.RepoURL = "https://example.com/org/addons.git"
	orch := New(nil, nil, newMockArgocd(), git, gitops, defaultPaths(), nil)

	result, err := orch.InitRepo(context.Background(), InitRepoRequest{})
	if err != nil {
		t.Fatalf("InitRepo on an empty repo: %v", err)
	}
	if result == nil || result.Repo == nil || len(result.Repo.FilesCreated) == 0 {
		t.Fatalf("expected the v4 seed to be written, got %+v", result)
	}
	if _, ok := git.files[EnginePinPath]; !ok {
		t.Errorf("the engine pin was not written; files=%v", v3MarkerFileKeys(git.files))
	}
}

func TestHasV3Markers(t *testing.T) {
	tests := []struct {
		name  string
		files map[string][]byte
		want  bool
	}{
		{"bootstrap chart", map[string][]byte{V3BootstrapMarkerPath: []byte("name: x\n")}, true},
		{"cluster registry", map[string][]byte{V3SecondaryMarkerPath: []byte("clusters: []\n")}, true},
		{"empty repo", map[string][]byte{}, false},
		{"v4 repo only", map[string][]byte{EnginePinPath: []byte("kind: Application\n")}, false},
		{"marker present but empty", map[string][]byte{V3BootstrapMarkerPath: {}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files = tt.files
			if got := HasV3Markers(context.Background(), git, "main"); got != tt.want {
				t.Errorf("HasV3Markers = %v, want %v", got, tt.want)
			}
		})
	}
}

func v3MarkerFileKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
