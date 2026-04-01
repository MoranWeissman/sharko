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
