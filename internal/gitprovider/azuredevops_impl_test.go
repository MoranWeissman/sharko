package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestAzureProvider creates an AzureDevOpsProvider backed by the given httptest server.
func newTestAzureProvider(t *testing.T, server *httptest.Server) *AzureDevOpsProvider {
	t.Helper()
	return &AzureDevOpsProvider{
		client:       server.Client(),
		organisation: "test-org",
		project:      "test-project",
		repository:   "test-repo",
		pat:          "test-pat",
		baseURL:      server.URL,
	}
}

func TestAzure_TestConnection_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"repo-id","name":"test-repo"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	err := provider.TestConnection(context.Background())
	if err != nil {
		t.Fatalf("TestConnection failed: %v", err)
	}
}

func TestAzure_TestConnection_Unauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	err := provider.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestAzure_GetFileContent_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		version := r.URL.Query().Get("versionDescriptor.version")
		if path != "charts/values.yaml" {
			t.Errorf("expected path charts/values.yaml, got %s", path)
		}
		if version != "main" {
			t.Errorf("expected version main, got %s", version)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("key: value\n"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	content, err := provider.GetFileContent(context.Background(), "charts/values.yaml", "main")
	if err != nil {
		t.Fatalf("GetFileContent failed: %v", err)
	}
	if string(content) != "key: value\n" {
		t.Errorf("expected 'key: value\\n', got %q", string(content))
	}
}

func TestAzure_ListDirectory_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": []map[string]interface{}{
				{"path": "/charts", "isFolder": true},
				{"path": "/charts/Chart.yaml", "isFolder": false},
				{"path": "/charts/values.yaml", "isFolder": false},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	entries, err := provider.ListDirectory(context.Background(), "/charts", "main")
	if err != nil {
		t.Fatalf("ListDirectory failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
	if entries[0] != "Chart.yaml" {
		t.Errorf("expected Chart.yaml, got %s", entries[0])
	}
	if entries[1] != "values.yaml" {
		t.Errorf("expected values.yaml, got %s", entries[1])
	}
}

func TestAzure_ListPullRequests_MapsStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pullrequests", func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("searchCriteria.status")
		if status != "active" {
			t.Errorf("expected status 'active', got %s", status)
		}
		resp := map[string]interface{}{
			"value": []map[string]interface{}{
				{
					"pullRequestId": 10,
					"title":         "Add feature",
					"description":   "A new feature",
					"status":        "active",
					"createdBy":     map[string]string{"displayName": "dev-user"},
					"sourceRefName": "refs/heads/feature",
					"targetRefName": "refs/heads/main",
					"creationDate":  "2025-01-15T10:00:00Z",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	prs, err := provider.ListPullRequests(context.Background(), "open")
	if err != nil {
		t.Fatalf("ListPullRequests failed: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if prs[0].Status != "open" {
		t.Errorf("expected status 'open', got %q", prs[0].Status)
	}
	if prs[0].SourceBranch != "feature" {
		t.Errorf("expected source branch 'feature', got %q", prs[0].SourceBranch)
	}
	if prs[0].Author != "dev-user" {
		t.Errorf("expected author 'dev-user', got %q", prs[0].Author)
	}
}

func TestAzure_CreateBranch_Success(t *testing.T) {
	mux := http.NewServeMux()

	// GET refs to resolve source SHA
	mux.HandleFunc("GET /refs", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"value": []map[string]string{
				{"objectId": "abc123def456", "name": "refs/heads/main"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// POST refs to create branch
	mux.HandleFunc("POST /refs", func(w http.ResponseWriter, r *http.Request) {
		var body []map[string]string
		json.NewDecoder(r.Body).Decode(&body)

		if len(body) != 1 {
			t.Fatalf("expected 1 ref update, got %d", len(body))
		}
		if body[0]["name"] != "refs/heads/feature-branch" {
			t.Errorf("expected refs/heads/feature-branch, got %s", body[0]["name"])
		}
		if body[0]["newObjectId"] != "abc123def456" {
			t.Errorf("expected newObjectId abc123def456, got %s", body[0]["newObjectId"])
		}
		if body[0]["oldObjectId"] != "0000000000000000000000000000000000000000" {
			t.Errorf("expected oldObjectId all zeros, got %s", body[0]["oldObjectId"])
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"value": []map[string]string{
				{"name": "refs/heads/feature-branch", "newObjectId": "abc123def456"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	err := provider.CreateBranch(context.Background(), "feature-branch", "main")
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
}

func TestAzure_CreateOrUpdateFile_Success(t *testing.T) {
	mux := http.NewServeMux()

	// GET refs to get current SHA
	mux.HandleFunc("GET /refs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"value": []map[string]string{
				{"objectId": "sha-current", "name": "refs/heads/feature"},
			},
		})
	})

	// GET items to check file existence (return 404 = new file)
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	// POST pushes to create the file
	mux.HandleFunc("POST /pushes", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		refUpdates := body["refUpdates"].([]interface{})
		if len(refUpdates) != 1 {
			t.Fatalf("expected 1 refUpdate, got %d", len(refUpdates))
		}

		commits := body["commits"].([]interface{})
		commit := commits[0].(map[string]interface{})
		changes := commit["changes"].([]interface{})
		change := changes[0].(map[string]interface{})

		if change["changeType"] != "add" {
			t.Errorf("expected changeType 'add', got %v", change["changeType"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pushId": 1,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	err := provider.CreateOrUpdateFile(
		context.Background(),
		"charts/values.yaml",
		[]byte("key: value\n"),
		"feature",
		"add values",
	)
	if err != nil {
		t.Fatalf("CreateOrUpdateFile failed: %v", err)
	}
}

func TestAzure_DeleteFile_Success(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /refs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"value": []map[string]string{
				{"objectId": "sha-current", "name": "refs/heads/main"},
			},
		})
	})

	mux.HandleFunc("POST /pushes", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		commits := body["commits"].([]interface{})
		commit := commits[0].(map[string]interface{})
		changes := commit["changes"].([]interface{})
		change := changes[0].(map[string]interface{})

		if change["changeType"] != "delete" {
			t.Errorf("expected changeType 'delete', got %v", change["changeType"])
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"pushId": 2})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	err := provider.DeleteFile(context.Background(), "old-file.yaml", "main", "remove old file")
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
}

func TestAzure_CreatePullRequest_Success(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /pullrequests", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)

		if body["sourceRefName"] != "refs/heads/feature" {
			t.Errorf("expected sourceRefName refs/heads/feature, got %s", body["sourceRefName"])
		}
		if body["targetRefName"] != "refs/heads/main" {
			t.Errorf("expected targetRefName refs/heads/main, got %s", body["targetRefName"])
		}
		if body["title"] != "Add feature" {
			t.Errorf("expected title 'Add feature', got %q", body["title"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pullRequestId": 42,
			"title":         body["title"],
			"description":   body["description"],
			"status":        "active",
			"createdBy":     map[string]string{"displayName": "dev-user"},
			"sourceRefName": body["sourceRefName"],
			"targetRefName": body["targetRefName"],
			"creationDate":  "2025-01-15T10:00:00Z",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	provider := newTestAzureProvider(t, server)
	pr, err := provider.CreatePullRequest(
		context.Background(),
		"Add feature",
		"This PR adds a feature",
		"feature",
		"main",
	)
	if err != nil {
		t.Fatalf("CreatePullRequest failed: %v", err)
	}
	if pr.ID != 42 {
		t.Errorf("expected PR ID 42, got %d", pr.ID)
	}
	if pr.Title != "Add feature" {
		t.Errorf("expected title 'Add feature', got %q", pr.Title)
	}
	if pr.Status != "open" {
		t.Errorf("expected status 'open', got %q", pr.Status)
	}
	expectedURL := "https://dev.azure.com/test-org/test-project/_git/test-repo/pullrequest/42"
	if pr.URL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, pr.URL)
	}
	if pr.SourceBranch != "feature" {
		t.Errorf("expected source branch 'feature', got %q", pr.SourceBranch)
	}
	if pr.TargetBranch != "main" {
		t.Errorf("expected target branch 'main', got %q", pr.TargetBranch)
	}
}
