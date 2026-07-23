package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestGiteaProviderInterfaceAssertion verifies that GiteaProvider implements GitProvider.
func TestGiteaProviderInterfaceAssertion(t *testing.T) {
	var _ GitProvider = (*GiteaProvider)(nil)
}

// TestGiteaProviderGetFileContent tests the GetFileContent method.
func TestGiteaProviderGetFileContent(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		wantErr    bool
		wantNotFound bool
	}{
		{
			name:       "success",
			statusCode: 200,
			body:       []byte("file content here"),
			wantErr:    false,
		},
		{
			name:       "404_not_found",
			statusCode: 404,
			body:       []byte(`{"message":"Not Found"}`),
			wantErr:    true,
			wantNotFound: true,
		},
		{
			name:       "500_server_error",
			statusCode: 500,
			body:       []byte(`{"message":"Internal Server Error"}`),
			wantErr:    true,
			wantNotFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that mimics Gitea's /api/v1/repos/{owner}/{repo}/contents/{filepath}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The Gitea SDK calls /version during client construction
				if r.URL.Path == "/api/v1/version" {
					w.WriteHeader(200)
					w.Write([]byte(`{"version":"1.20.0"}`))
					return
				}
				// The Gitea SDK appends /api/v1 automatically
				if r.URL.Path != "/api/v1/repos/testowner/testrepo/raw/testfile.txt" {
					// For non-raw paths, handle the GetRepo call (used by TestConnection)
					if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
						w.WriteHeader(200)
						w.Write([]byte(`{"name":"testrepo"}`))
						return
					}
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				w.WriteHeader(tt.statusCode)
				w.Write(tt.body)
			}))
			defer server.Close()

			provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
			if err != nil {
				t.Fatalf("NewGiteaProvider failed: %v", err)
			}

			content, err := provider.GetFileContent(context.Background(), "testfile.txt", "main")
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				if tt.wantNotFound && !errors.Is(err, ErrFileNotFound) {
					t.Errorf("expected ErrFileNotFound, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if string(content) != string(tt.body) {
				t.Errorf("content mismatch: got %q, want %q", string(content), string(tt.body))
			}
		})
	}
}

// TestGiteaProviderListDirectory tests the ListDirectory method.
func TestGiteaProviderListDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Gitea SDK calls /version during client construction
		if r.URL.Path == "/api/v1/version" {
			w.WriteHeader(200)
			w.Write([]byte(`{"version":"1.20.0"}`))
			return
		}
		// Handle GetRepo call (used by TestConnection)
		if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
			w.WriteHeader(200)
			w.Write([]byte(`{"name":"testrepo"}`))
			return
		}

		// ListContents path
		if r.URL.Path != "/api/v1/repos/testowner/testrepo/contents/testdir" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		// Return a JSON array of ContentsResponse objects
		response := []map[string]interface{}{
			{"name": "file1.txt", "path": "testdir/file1.txt", "type": "file"},
			{"name": "file2.md", "path": "testdir/file2.md", "type": "file"},
			{"name": "subdir", "path": "testdir/subdir", "type": "dir"},
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	names, err := provider.ListDirectory(context.Background(), "testdir", "main")
	if err != nil {
		t.Fatalf("ListDirectory failed: %v", err)
	}

	// We expect basenames (consistent with GitHub and AzureDevOps providers)
	expected := []string{"file1.txt", "file2.md", "subdir"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(names))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("entry %d: got %q, want %q", i, name, expected[i])
		}
	}
}

// TestGiteaProviderListPullRequests tests the ListPullRequests method.
func TestGiteaProviderListPullRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Gitea SDK calls /version during client construction
		if r.URL.Path == "/api/v1/version" {
			w.WriteHeader(200)
			w.Write([]byte(`{"version":"1.20.0"}`))
			return
		}
		// Handle GetRepo call (used by TestConnection)
		if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
			w.WriteHeader(200)
			w.Write([]byte(`{"name":"testrepo"}`))
			return
		}

		// ListRepoPullRequests path
		if r.URL.Path != "/api/v1/repos/testowner/testrepo/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}

		// Check state query parameter
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "all" // default
		}

		// Return a JSON array of PullRequest objects
		now := time.Now()
		response := []map[string]interface{}{
			{
				"id":       1,
				"number":   42,
				"title":    "Test PR",
				"body":     "Test description",
				"state":    state,
				"html_url": "https://gitea.example.com/testowner/testrepo/pulls/42",
				"user": map[string]interface{}{
					"login": "testuser", // Gitea User struct uses "login" as JSON tag
				},
				"base": map[string]interface{}{
					"ref": "main",
				},
				"head": map[string]interface{}{
					"ref": "feature-branch",
				},
				"created_at": now.Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
			},
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	prs, err := provider.ListPullRequests(context.Background(), "open")
	if err != nil {
		t.Fatalf("ListPullRequests failed: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}

	pr := prs[0]
	if pr.ID != 42 {
		t.Errorf("expected ID 42, got %d", pr.ID)
	}
	if pr.Title != "Test PR" {
		t.Errorf("expected title 'Test PR', got %q", pr.Title)
	}
	if pr.Author != "testuser" {
		t.Errorf("expected author 'testuser', got %q", pr.Author)
	}
	if pr.SourceBranch != "feature-branch" {
		t.Errorf("expected source branch 'feature-branch', got %q", pr.SourceBranch)
	}
	if pr.TargetBranch != "main" {
		t.Errorf("expected target branch 'main', got %q", pr.TargetBranch)
	}
}

// TestGiteaProviderWriteStubs tests that write operations return "not yet implemented" errors.
func TestGiteaProviderWriteStubs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Gitea SDK calls /version during client construction
		if r.URL.Path == "/api/v1/version" {
			w.WriteHeader(200)
			w.Write([]byte(`{"version":"1.20.0"}`))
			return
		}
		// Handle GetRepo call for constructor
		if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
			w.WriteHeader(200)
			w.Write([]byte(`{"name":"testrepo"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	ctx := context.Background()

	// Test each write method returns an error
	if err := provider.CreateBranch(ctx, "test", "main"); err == nil {
		t.Error("expected CreateBranch to return error")
	}
	if err := provider.CreateOrUpdateFile(ctx, "test", nil, "main", "msg"); err == nil {
		t.Error("expected CreateOrUpdateFile to return error")
	}
	if err := provider.BatchCreateFiles(ctx, nil, "main", "msg"); err == nil {
		t.Error("expected BatchCreateFiles to return error")
	}
	if err := provider.DeleteFile(ctx, "test", "main", "msg"); err == nil {
		t.Error("expected DeleteFile to return error")
	}
	if _, err := provider.CreatePullRequest(ctx, "title", "body", "head", "base"); err == nil {
		t.Error("expected CreatePullRequest to return error")
	}
	if err := provider.MergePullRequest(ctx, 1); err == nil {
		t.Error("expected MergePullRequest to return error")
	}
	if _, err := provider.GetPullRequestStatus(ctx, 1); err == nil {
		t.Error("expected GetPullRequestStatus to return error")
	}
	if err := provider.DeleteBranch(ctx, "test"); err == nil {
		t.Error("expected DeleteBranch to return error")
	}
}
