package models

import "testing"

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"abc", "***"},
		{"12345678", "********"},
		{"1234567890", "1234**7890"},
		{"abcdefghijklmnop", "abcd********mnop"},
	}

	for _, tt := range tests {
		got := MaskToken(tt.input)
		if got != tt.expected {
			t.Errorf("MaskToken(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestConnectionToResponse(t *testing.T) {
	conn := Connection{
		Name:        "test",
		Description: "Test connection",
		Git: GitRepoConfig{
			Provider: GitProviderGitHub,
			Owner:    "my-org",
			Repo:     "my-repo",
			Token:    "ghp_1234567890abcdef",
		},
		Argocd: ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     "eyJhbGciOiJIUzI1NiJ9.token",
			Namespace: "argocd",
		},
		IsDefault: true,
	}

	resp := conn.ToResponse(true)

	if resp.Name != "test" {
		t.Errorf("expected name=test, got %s", resp.Name)
	}
	if resp.GitProvider != GitProviderGitHub {
		t.Errorf("expected provider=github, got %s", resp.GitProvider)
	}
	if resp.GitRepoIdentifier != "my-org/my-repo" {
		t.Errorf("expected repo=my-org/my-repo, got %s", resp.GitRepoIdentifier)
	}
	if resp.GitTokenMasked == "ghp_1234567890abcdef" {
		t.Error("token should be masked")
	}
	if resp.ArgocdServerURL != "https://argocd.example.com" {
		t.Errorf("unexpected argocd URL: %s", resp.ArgocdServerURL)
	}
	if !resp.IsActive {
		t.Error("expected isActive=true")
	}
}

// TestParseRepoURL_ExplicitFieldsOverride covers V126-4.1 / BUG-189:
// ParseRepoURL must accept a URL whose path can't be parsed into owner/repo
// when the caller has already populated the explicit Owner+Repo fields.
// This unblocks self-hosted Gitea, corporate proxies, in-cluster gitfake
// URLs, and any other deployment whose URL shape doesn't match a public-SaaS
// path layout.
//
// The four canonical cases below preserve every existing accept/reject and
// add the new accept case the bug fix unlocks.
func TestParseRepoURL_ExplicitFieldsOverride(t *testing.T) {
	tests := []struct {
		name      string
		in        GitRepoConfig
		wantErr   bool
		wantOwner string
		wantRepo  string
	}{
		{
			name: "PathWithOwnerRepo_NoExplicitFields_Accept_PreservedBehavior",
			in: GitRepoConfig{
				RepoURL: "https://github.com/sharko-e2e/sharko-addons",
			},
			wantErr:   false,
			wantOwner: "sharko-e2e",
			wantRepo:  "sharko-addons",
		},
		{
			name: "PathWithoutOwnerRepo_ExplicitFieldsPopulated_Accept_NewBehavior_FixesBug",
			in: GitRepoConfig{
				// Single path segment — pre-fix this produced
				// "Git URL must contain owner/repo (got: /sharko-e2e)".
				RepoURL: "http://127.0.0.1:34567/sharko-e2e.git",
				Owner:   "sharko-e2e",
				Repo:    "sharko-addons",
			},
			wantErr:   false,
			wantOwner: "sharko-e2e",  // explicit field preserved
			wantRepo:  "sharko-addons", // explicit field preserved
		},
		{
			name: "PathWithoutOwnerRepo_ExplicitFieldsEmpty_Reject_PreservedBehavior",
			in: GitRepoConfig{
				RepoURL: "http://127.0.0.1:34567/sharko-e2e.git",
				// no Owner / Repo set
			},
			wantErr: true,
		},
		{
			name: "GitHubURL_ExplicitFieldsPopulated_Accept_PathParseStillWins_PreservedBehavior",
			in: GitRepoConfig{
				// Both URL path AND explicit fields present — path parse
				// wins (path is the canonical source when valid). The
				// explicit fields the caller passed are overwritten with
				// the parsed values; for github.com this is a no-op when
				// they agree, and a self-correction when they disagree.
				RepoURL: "https://github.com/parsed-owner/parsed-repo",
				Owner:   "explicit-owner",
				Repo:    "explicit-repo",
			},
			wantErr:   false,
			wantOwner: "parsed-owner",
			wantRepo:  "parsed-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.in // copy so we don't mutate the table
			err := g.ParseRepoURL()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRepoURL() = nil, want error (case: %s)", tt.name)
				}
				// The reject path MUST keep the friendly "Git URL must
				// contain owner/repo" wording so operators can still grep
				// the message they've seen in past logs.
				if !contains(err.Error(), "owner/repo") {
					t.Errorf("error %q should still mention 'owner/repo' so operators get a stable, friendly message", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRepoURL() = %v, want nil (case: %s)", err, tt.name)
			}
			if g.Provider != GitProviderGitHub {
				t.Errorf("Provider = %q, want %q", g.Provider, GitProviderGitHub)
			}
			if g.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", g.Owner, tt.wantOwner)
			}
			if g.Repo != tt.wantRepo {
				t.Errorf("Repo = %q, want %q", g.Repo, tt.wantRepo)
			}
		})
	}
}

// contains is a tiny strings.Contains shim that keeps the test file free
// of an extra import when the rest of the package's tests don't need it.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestConnectionToResponseAzureDevOps(t *testing.T) {
	conn := Connection{
		Name: "azure-test",
		Git: GitRepoConfig{
			Provider:     GitProviderAzureDevOps,
			Organization: "my-org",
			Project:      "my-project",
			Repository:   "my-repo",
			PAT:          "pat-secret-token-here",
		},
		Argocd: ArgocdConfig{
			ServerURL: "https://argocd.example.com",
			Token:     "token123",
			Namespace: "argocd",
		},
	}

	resp := conn.ToResponse(false)

	if resp.GitProvider != GitProviderAzureDevOps {
		t.Errorf("expected provider=azuredevops, got %s", resp.GitProvider)
	}
	if resp.GitRepoIdentifier != "my-org/my-project/my-repo" {
		t.Errorf("expected repo identifier, got %s", resp.GitRepoIdentifier)
	}
	if resp.IsActive {
		t.Error("expected isActive=false")
	}
}
