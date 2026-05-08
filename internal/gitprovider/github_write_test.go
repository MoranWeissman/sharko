package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
				Login: github.Ptr("sharko-bot"),
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

// TestCreateBranch_EmptyRepo_409 — V124-11 regression. A freshly-created
// GitHub repo (no commits) returns 409 Conflict with "Git Repository is
// empty." on Git/GetRef. CreateBranch must detect this, route to
// initEmptyRepo (which seeds README.md via the Contents API), and then
// create the requested branch from the new commit's SHA.
func TestCreateBranch_EmptyRepo_409(t *testing.T) {
	mux := http.NewServeMux()

	// Track whether GetRef was invoked twice (once before init -> 409,
	// once after init -> 200) so we exercise the post-init path.
	var getRefCalls atomic.Int32

	// GET /repos/{owner}/{repo}/git/ref/heads/main
	//   1st call: 409 "Git Repository is empty."
	//   2nd call (after initEmptyRepo seeds README): 200 with SHA
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		n := getRefCalls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(github.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusConflict},
				Message:  "Git Repository is empty.",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(github.Reference{
			Ref:    github.Ptr("refs/heads/main"),
			Object: &github.GitObject{SHA: github.Ptr("seedsha")},
		})
	})

	// PUT /repos/{owner}/{repo}/contents/README.md — Contents API auto-creates
	// main on the empty repo with the README commit.
	var contentsPutCalled atomic.Int32
	mux.HandleFunc("PUT /repos/test-owner/test-repo/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		contentsPutCalled.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.RepositoryContentResponse{
			Content: &github.RepositoryContent{
				Name: github.Ptr("README.md"),
				SHA:  github.Ptr("readmesha"),
			},
			Commit: github.Commit{SHA: github.Ptr("seedsha")},
		})
	})

	// POST /repos/{owner}/{repo}/git/refs — creates the bootstrap branch
	// pointing at the seed commit's SHA.
	var createRefCalled atomic.Int32
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		createRefCalled.Add(1)
		var req struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Ref != "refs/heads/sharko/initialize-repository-deadbeef" {
			t.Errorf("expected ref refs/heads/sharko/initialize-repository-deadbeef, got %s", req.Ref)
		}
		if req.SHA != "seedsha" {
			t.Errorf("expected SHA seedsha, got %s", req.SHA)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.Reference{
			Ref:    github.Ptr(req.Ref),
			Object: &github.GitObject{SHA: github.Ptr(req.SHA)},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	if err := provider.CreateBranch(context.Background(), "sharko/initialize-repository-deadbeef", "main"); err != nil {
		t.Fatalf("CreateBranch on empty repo: %v", err)
	}

	if got := contentsPutCalled.Load(); got != 1 {
		t.Errorf("expected initEmptyRepo to PUT README.md once, got %d calls", got)
	}
	if got := getRefCalls.Load(); got != 2 {
		t.Errorf("expected GetRef called twice (initial 409 + post-init 200), got %d", got)
	}
	if got := createRefCalled.Load(); got != 1 {
		t.Errorf("expected branch ref creation once, got %d calls", got)
	}
}

// TestCreateBranch_NotFound_404 — pre-existing fallback for "ref not found"
// (e.g., default branch differs from configured base branch). Locked in as a
// regression alongside the V124-11 409 path.
func TestCreateBranch_NotFound_404(t *testing.T) {
	mux := http.NewServeMux()
	var getRefCalls atomic.Int32

	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		n := getRefCalls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(github.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusNotFound},
				Message:  "Not Found",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(github.Reference{
			Ref:    github.Ptr("refs/heads/main"),
			Object: &github.GitObject{SHA: github.Ptr("seedsha")},
		})
	})

	mux.HandleFunc("PUT /repos/test-owner/test-repo/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.RepositoryContentResponse{
			Content: &github.RepositoryContent{Name: github.Ptr("README.md"), SHA: github.Ptr("readmesha")},
		})
	})
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(github.Reference{
			Ref:    github.Ptr("refs/heads/feature"),
			Object: &github.GitObject{SHA: github.Ptr("seedsha")},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	if err := provider.CreateBranch(context.Background(), "feature", "main"); err != nil {
		t.Fatalf("CreateBranch with 404 fallback: %v", err)
	}
}

// TestCreateBranch_409_NonEmptyMessage_Propagates — defensive guard against
// false positives. A 409 that is NOT the empty-repo signal (e.g., a
// hypothetical merge-conflict response on this endpoint) must NOT trigger
// the empty-repo bootstrap path. The original error must surface to the
// caller so the operator can diagnose it.
func TestCreateBranch_409_NonEmptyMessage_Propagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusConflict},
			Message:  "Reference update failed",
		})
	})
	mux.HandleFunc("PUT /repos/test-owner/test-repo/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		t.Error("Contents API must not be called when 409 message is not 'empty'")
		w.WriteHeader(http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestGitHubProvider(t, server)
	err := provider.CreateBranch(context.Background(), "feature", "main")
	if err == nil {
		t.Fatal("expected error for unrelated 409, got nil")
	}
	if !strings.Contains(err.Error(), "create branch: get ref") {
		t.Errorf("expected wrapped error mentioning 'create branch: get ref', got: %v", err)
	}
}

// TestCreateBranch_OtherErrors_Propagate — 401/403/5xx and arbitrary
// non-emptyrepo errors must propagate verbatim, not silently route through
// the bootstrap fallback.
func TestCreateBranch_OtherErrors_Propagate(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
	}{
		{"unauthorized_401", http.StatusUnauthorized, "Bad credentials"},
		{"forbidden_403", http.StatusForbidden, "Resource not accessible"},
		{"server_error_500", http.StatusInternalServerError, "Server error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(github.ErrorResponse{
					Response: &http.Response{StatusCode: tc.status},
					Message:  tc.msg,
				})
			})
			mux.HandleFunc("PUT /repos/test-owner/test-repo/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("%s: Contents API must not be called", tc.name)
				w.WriteHeader(http.StatusInternalServerError)
			})

			server := httptest.NewServer(mux)
			defer server.Close()

			provider := newTestGitHubProvider(t, server)
			err := provider.CreateBranch(context.Background(), "feature", "main")
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
		})
	}
}

// TestIsEmptyRepo — unit test the detector directly so future callers can
// rely on its semantics without re-deriving them.
func TestIsEmptyRepo(t *testing.T) {
	emptyMsg := &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusConflict},
		Message:  "Git Repository is empty.",
	}
	emptyMsgLower := &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusConflict},
		Message:  "git repository is empty",
	}
	nonEmpty409 := &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusConflict},
		Message:  "Reference update failed",
	}
	notConflict := &github.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusNotFound},
		Message:  "Not Found",
	}
	nilResponse := &github.ErrorResponse{Response: nil, Message: "Git Repository is empty."}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"409 empty (canonical)", emptyMsg, true},
		{"409 empty (lowercase)", emptyMsgLower, true},
		{"409 wrapped", fmt.Errorf("get ref: %w", emptyMsg), true},
		{"409 non-empty message", nonEmpty409, false},
		{"404 not found", notConflict, false},
		{"nil response", nilResponse, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEmptyRepo(tc.err); got != tc.want {
				t.Errorf("isEmptyRepo(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

