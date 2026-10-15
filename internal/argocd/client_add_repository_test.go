package argocd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestAddRepository_SendsUpsertAsQueryParameter checks the exact request
// AddRepository sends. ArgoCD reads "upsert" from the query string of
// POST /api/v1/repositories (repository.proto declares the call with
// body: "repo", and the swagger lists upsert as "in": "query"). When upsert
// sat in the body, ArgoCD ignored it and refused a second setup with a changed
// repository setting ("existing repository spec is different").
func TestAddRepository_SendsUpsertAsQueryParameter(t *testing.T) {
	var (
		calls     int
		gotMethod string
		gotPath   string
		gotQuery  string
		gotAuth   string
		gotBody   []byte
	)
	ts := newPlainArgoTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"repo":"https://github.example/sharko-org/addons.git"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	err := c.AddRepository(context.Background(), "https://github.example/sharko-org/addons.git", "x-access-token", "new-git-token")
	if err != nil {
		t.Fatalf("AddRepository: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected exactly one request, got %d", calls)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/repositories" {
		t.Errorf("path = %q, want /api/v1/repositories", gotPath)
	}
	if gotQuery != "upsert=true" {
		t.Errorf("query = %q, want exactly \"upsert=true\"", gotQuery)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want the bearer token", gotAuth)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, gotBody)
	}
	want := map[string]interface{}{
		"repo":     "https://github.example/sharko-org/addons.git",
		"username": "x-access-token",
		"password": "new-git-token",
		"type":     "git",
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %v, want exactly %v", body, want)
	}
	if _, ok := body["upsert"]; ok {
		t.Errorf("body carries an \"upsert\" field; ArgoCD ignores it there, it belongs in the query")
	}
}

// TestAddRepository_FailureKeepsCredentialsOut checks that when ArgoCD refuses
// the call, neither the Git token, the ArgoCD token, nor a password written
// into the repository address shows up in the error or in the logs.
func TestAddRepository_FailureKeepsCredentialsOut(t *testing.T) {
	const (
		gitToken    = "git-token-must-not-leak"
		argoToken   = "argocd-token-must-not-leak"
		urlPassword = "url-password-must-not-leak"
	)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError} {
		ts := newPlainArgoTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			// ArgoCD's own reply echoes what it was given.
			_, _ = w.Write([]byte(`{"message":"existing repository spec is different; password ` + gitToken + `"}`))
		}))

		c := NewClient(ts.URL, argoToken, false)
		repoURL := "https://x-access-token:" + urlPassword + "@github.example/sharko-org/addons.git"
		err := c.AddRepository(context.Background(), repoURL, "x-access-token", gitToken)
		ts.Close()
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}
		msg := err.Error()
		for _, secret := range []string{gitToken, argoToken, urlPassword} {
			if strings.Contains(msg, secret) {
				t.Errorf("status %d: error carries a credential: %q", status, msg)
			}
		}
		if !strings.Contains(msg, "github.example/sharko-org/addons.git") {
			t.Errorf("status %d: error should still name the repository without its credential, got %q", status, msg)
		}
	}

	for _, secret := range []string{gitToken, argoToken, urlPassword} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("logs carry a credential: %s", logs.String())
		}
	}
}
