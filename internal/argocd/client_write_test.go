package argocd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncApplication_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/applications/my-app/sync" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing or wrong Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing or wrong Content-Type header")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	err := c.SyncApplication(context.Background(), "my-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSyncApplication_Non200ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"permission denied"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	err := c.SyncApplication(context.Background(), "my-app")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestRefreshApplication_Hard(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/applications/my-app" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("refresh") != "hard" {
			t.Errorf("expected refresh=hard, got %q", r.URL.Query().Get("refresh"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "my-app", "namespace": "argocd"},
			"spec": {"project": "default", "source": {"repoURL": "https://github.com/example/repo"}},
			"status": {
				"sync": {"status": "Synced"},
				"health": {"status": "Healthy"}
			}
		}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	app, err := c.RefreshApplication(context.Background(), "my-app", true)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if app.Name != "my-app" {
		t.Errorf("expected name my-app, got %s", app.Name)
	}
	if app.HealthStatus != "Healthy" {
		t.Errorf("expected Healthy, got %s", app.HealthStatus)
	}
	if app.SyncStatus != "Synced" {
		t.Errorf("expected Synced, got %s", app.SyncStatus)
	}
}

func TestRefreshApplication_Normal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("refresh") != "true" {
			t.Errorf("expected refresh=true, got %q", r.URL.Query().Get("refresh"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "my-app", "namespace": "argocd"},
			"spec": {"project": "default", "source": {"repoURL": "https://github.com/example/repo"}},
			"status": {
				"sync": {"status": "OutOfSync"},
				"health": {"status": "Degraded"}
			}
		}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "test-token", false)
	app, err := c.RefreshApplication(context.Background(), "my-app", false)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if app.SyncStatus != "OutOfSync" {
		t.Errorf("expected OutOfSync, got %s", app.SyncStatus)
	}
}
