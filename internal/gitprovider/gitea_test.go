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

// TestGiteaProviderListPullRequestsUnknownState tests that an unknown state defaults to "all".
func TestGiteaProviderListPullRequestsUnknownState(t *testing.T) {
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

		// Check state query parameter — should be "all" since we're passing an unknown state
		state := r.URL.Query().Get("state")
		if state != "all" {
			t.Errorf("expected state 'all', got %q", state)
		}

		// Return empty PR list
		response := []map[string]interface{}{}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	// Call with an unrecognized state — should hit the default case and map to StateAll
	prs, err := provider.ListPullRequests(context.Background(), "bogus")
	if err != nil {
		t.Fatalf("ListPullRequests failed: %v", err)
	}

	// Should succeed with empty list (the mock returns empty)
	if len(prs) != 0 {
		t.Errorf("expected 0 PRs, got %d", len(prs))
	}
}

// TestGiteaProviderWritePath tests the full write cycle: CreateBranch, CreateOrUpdateFile,
// BatchCreateFiles, CreatePullRequest, GetPullRequestStatus, MergePullRequest, DeleteBranch, DeleteFile.
func TestGiteaProviderWritePath(t *testing.T) {
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

		// CreateBranch: POST /api/v1/repos/{owner}/{repo}/branches
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/branches" {
			w.WriteHeader(201)
			w.Write([]byte(`{"name":"feature-branch","commit":{"id":"abc123"}}`))
			return
		}

		// GetContents (for checking file existence before create/update): GET /api/v1/repos/{owner}/{repo}/contents/{filepath}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/newfile.txt" {
			// First call (for CreateOrUpdateFile create path): file doesn't exist → 404
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/existingfile.txt" {
			// Second call (for CreateOrUpdateFile update path): file exists
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"name": "existingfile.txt",
				"path": "existingfile.txt",
				"sha":  "oldsha123",
				"type": "file",
			})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/deleteme.txt" {
			// For DeleteFile: file exists
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"name": "deleteme.txt",
				"path": "deleteme.txt",
				"sha":  "deletesha",
				"type": "file",
			})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/batch1.txt" {
			w.WriteHeader(404) // batch file doesn't exist yet
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/batch2.txt" {
			w.WriteHeader(404)
			return
		}

		// CreateFile: POST /api/v1/repos/{owner}/{repo}/contents/{filepath}
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/newfile.txt" {
			w.WriteHeader(201)
			w.Write([]byte(`{"content":{"name":"newfile.txt","sha":"newsha"}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/batch1.txt" {
			w.WriteHeader(201)
			w.Write([]byte(`{"content":{"name":"batch1.txt"}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/batch2.txt" {
			w.WriteHeader(201)
			w.Write([]byte(`{"content":{"name":"batch2.txt"}}`))
			return
		}

		// UpdateFile: PUT /api/v1/repos/{owner}/{repo}/contents/{filepath}
		if r.Method == "PUT" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/existingfile.txt" {
			w.WriteHeader(200)
			w.Write([]byte(`{"content":{"name":"existingfile.txt","sha":"newsha"}}`))
			return
		}

		// DeleteFile: DELETE /api/v1/repos/{owner}/{repo}/contents/{filepath}
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/deleteme.txt" {
			w.WriteHeader(204)
			return
		}

		// CreatePullRequest: POST /api/v1/repos/{owner}/{repo}/pulls
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/pulls" {
			now := time.Now()
			pr := map[string]interface{}{
				"id":       1,
				"number":   42,
				"title":    "Test PR",
				"body":     "Test body",
				"state":    "open",
				"html_url": "https://gitea.example.com/testowner/testrepo/pulls/42",
				"user": map[string]interface{}{
					"login": "testuser",
				},
				"base": map[string]interface{}{
					"ref": "main",
				},
				"head": map[string]interface{}{
					"ref": "feature-branch",
				},
				"created_at": now.Format(time.RFC3339),
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(pr)
			return
		}

		// GetPullRequest: GET /api/v1/repos/{owner}/{repo}/pulls/{index}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/pulls/42" {
			pr := map[string]interface{}{
				"id":          1,
				"number":      42,
				"title":       "Test PR",
				"state":       "open",
				"merged":      false,
				"has_merged":  false,
				"mergeable":   true,
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(pr)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/pulls/999" {
			// PR not found
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}

		// MergePullRequest: POST /api/v1/repos/{owner}/{repo}/pulls/{index}/merge
		if r.Method == "POST" && r.URL.Path == "/api/v1/repos/testowner/testrepo/pulls/42/merge" {
			w.WriteHeader(200)
			w.Write([]byte(`{"merged":true}`))
			return
		}

		// DeleteBranch: DELETE /api/v1/repos/{owner}/{repo}/branches/{branch}
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/repos/testowner/testrepo/branches/feature-branch" {
			w.WriteHeader(204)
			return
		}

		// Catch-all
		t.Logf("unhandled request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	ctx := context.Background()

	// 1. CreateBranch
	if err := provider.CreateBranch(ctx, "feature-branch", "main"); err != nil {
		t.Errorf("CreateBranch failed: %v", err)
	}

	// 2. CreateOrUpdateFile — create path (file doesn't exist)
	if err := provider.CreateOrUpdateFile(ctx, "newfile.txt", []byte("new content"), "feature-branch", "Add new file"); err != nil {
		t.Errorf("CreateOrUpdateFile (create) failed: %v", err)
	}

	// 3. CreateOrUpdateFile — update path (file exists)
	if err := provider.CreateOrUpdateFile(ctx, "existingfile.txt", []byte("updated content"), "feature-branch", "Update file"); err != nil {
		t.Errorf("CreateOrUpdateFile (update) failed: %v", err)
	}

	// 4. BatchCreateFiles
	files := map[string][]byte{
		"batch1.txt": []byte("batch content 1"),
		"batch2.txt": []byte("batch content 2"),
	}
	if err := provider.BatchCreateFiles(ctx, files, "feature-branch", "Batch commit"); err != nil {
		t.Errorf("BatchCreateFiles failed: %v", err)
	}

	// 5. CreatePullRequest
	pr, err := provider.CreatePullRequest(ctx, "Test PR", "Test body", "feature-branch", "main")
	if err != nil {
		t.Fatalf("CreatePullRequest failed: %v", err)
	}
	if pr.ID != 42 {
		t.Errorf("expected PR ID 42, got %d", pr.ID)
	}
	if pr.Status != "open" {
		t.Errorf("expected PR status 'open', got %q", pr.Status)
	}

	// 6. GetPullRequestStatus
	status, err := provider.GetPullRequestStatus(ctx, 42)
	if err != nil {
		t.Errorf("GetPullRequestStatus failed: %v", err)
	}
	if status != "open" {
		t.Errorf("expected status 'open', got %q", status)
	}

	// 7. MergePullRequest
	if err := provider.MergePullRequest(ctx, 42); err != nil {
		t.Errorf("MergePullRequest failed: %v", err)
	}

	// 8. DeleteFile
	if err := provider.DeleteFile(ctx, "deleteme.txt", "main", "Remove file"); err != nil {
		t.Errorf("DeleteFile failed: %v", err)
	}

	// 9. DeleteBranch
	if err := provider.DeleteBranch(ctx, "feature-branch"); err != nil {
		t.Errorf("DeleteBranch failed: %v", err)
	}
}

// TestGiteaProviderGetPullRequestStatusNotFound tests that GetPullRequestStatus
// wraps ErrPullRequestNotFound on 404.
func TestGiteaProviderGetPullRequestStatusNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			w.WriteHeader(200)
			w.Write([]byte(`{"version":"1.20.0"}`))
			return
		}
		if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
			w.WriteHeader(200)
			w.Write([]byte(`{"name":"testrepo"}`))
			return
		}
		// GetPullRequest returns 404
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/pulls/999" {
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	_, err = provider.GetPullRequestStatus(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrPullRequestNotFound) {
		t.Errorf("expected ErrPullRequestNotFound, got: %v", err)
	}
}

// TestGiteaProviderDeleteFileMissing tests that DeleteFile errors when the file doesn't exist.
func TestGiteaProviderDeleteFileMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			w.WriteHeader(200)
			w.Write([]byte(`{"version":"1.20.0"}`))
			return
		}
		if r.URL.Path == "/api/v1/repos/testowner/testrepo" {
			w.WriteHeader(200)
			w.Write([]byte(`{"name":"testrepo"}`))
			return
		}
		// GetContents for missing file returns 404
		if r.Method == "GET" && r.URL.Path == "/api/v1/repos/testowner/testrepo/contents/missing.txt" {
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider, err := NewGiteaProvider(server.URL, "testowner", "testrepo", "test-token")
	if err != nil {
		t.Fatalf("NewGiteaProvider failed: %v", err)
	}

	err = provider.DeleteFile(context.Background(), "missing.txt", "main", "Delete missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errors.New("file not found")) && err.Error() != `delete file: file "missing.txt" not found on branch "main"` {
		t.Errorf("expected 'file not found' error, got: %v", err)
	}
}
