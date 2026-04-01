package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v68/github"
)

// newTestGitHubProvider creates a GitHubProvider backed by the given httptest server.
func newTestGitHubProvider(t *testing.T, server *httptest.Server) *GitHubProvider {
	t.Helper()
	client := github.NewClient(server.Client())
	client.BaseURL, _ = client.BaseURL.Parse(server.URL + "/")
	return &GitHubProvider{
		client: client,
		owner:  "test-owner",
		repo:   "test-repo",
	}
}

func TestCreateBranch_Success(t *testing.T) {
	mux := http.NewServeMux()

	// GET /repos/{owner}/{repo}/git/ref/heads/main -> returns SHA
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Reference{
			Ref: github.Ptr("refs/heads/main"),
			Object: &github.GitObject{
				SHA: github.Ptr("abc123"),
			},
		})
	})

	// POST /repos/{owner}/{repo}/git/refs -> creates new ref
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Ref != "refs/heads/feature-branch" {
			t.Errorf("expected ref refs/heads/feature-branch, got %s", req.Ref)
		}
		if req.SHA != "abc123" {
			t.Errorf("expected SHA abc123, got %s", req.SHA)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.Reference{
			Ref: github.Ptr("refs/heads/feature-branch"),
			Object: &github.GitObject{
				SHA: github.Ptr("abc123"),
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	err := provider.CreateBranch(context.Background(), "feature-branch", "main")
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
}

func TestCreateOrUpdateFile_NewFile(t *testing.T) {
	mux := http.NewServeMux()

	// GET /repos/{owner}/{repo}/contents/new-file.yaml -> 404 (file doesn't exist)
	mux.HandleFunc("GET /repos/test-owner/test-repo/contents/new-file.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusNotFound},
			Message:  "Not Found",
		})
	})

	// PUT /repos/{owner}/{repo}/contents/new-file.yaml -> creates file
	mux.HandleFunc("PUT /repos/test-owner/test-repo/contents/new-file.yaml", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message string `json:"message"`
			Content string `json:"content"`
			Branch  string `json:"branch"`
			SHA     string `json:"sha,omitempty"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.SHA != "" {
			t.Errorf("expected no SHA for new file, got %s", req.SHA)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.RepositoryContentResponse{
			Content: &github.RepositoryContent{
				Name: github.Ptr("new-file.yaml"),
				SHA:  github.Ptr("newsha456"),
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	err := provider.CreateOrUpdateFile(
		context.Background(),
		"new-file.yaml",
		[]byte("key: value\n"),
		"feature-branch",
		"add new-file.yaml",
	)
	if err != nil {
		t.Fatalf("CreateOrUpdateFile failed: %v", err)
	}
}

func TestCreatePullRequest_Success(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /repos/test-owner/test-repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		var req github.NewPullRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.GetTitle() != "Add feature" {
			t.Errorf("expected title 'Add feature', got %q", req.GetTitle())
		}
		if !req.GetMaintainerCanModify() {
			t.Error("expected MaintainerCanModify to be true")
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.PullRequest{
			Number:  github.Ptr(42),
			Title:   req.Title,
			Body:    req.Body,
			HTMLURL: github.Ptr("https://github.com/test-owner/test-repo/pull/42"),
			State:   github.Ptr("open"),
			Head: &github.PullRequestBranch{
				Ref: req.Head,
			},
			Base: &github.PullRequestBranch{
				Ref: req.Base,
			},
			User: &github.User{
				Login: github.Ptr("aap-bot"),
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	pr, err := provider.CreatePullRequest(
		context.Background(),
		"Add feature",
		"This PR adds a feature",
		"feature-branch",
		"main",
	)
	if err != nil {
		t.Fatalf("CreatePullRequest failed: %v", err)
	}

	if pr.ID != 42 {
		t.Errorf("expected PR number 42, got %d", pr.ID)
	}
	if pr.URL != "https://github.com/test-owner/test-repo/pull/42" {
		t.Errorf("expected URL https://github.com/test-owner/test-repo/pull/42, got %s", pr.URL)
	}
	if pr.Title != "Add feature" {
		t.Errorf("expected title 'Add feature', got %q", pr.Title)
	}
	if pr.Status != "open" {
		t.Errorf("expected status 'open', got %q", pr.Status)
	}
}
