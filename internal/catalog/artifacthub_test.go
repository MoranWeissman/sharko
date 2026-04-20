package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArtifactHubClient_SearchHelm_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("kind") != "0" {
			t.Errorf("expected kind=0 (Helm) got %q", r.URL.Query().Get("kind"))
		}
		if r.URL.Query().Get("ts_query_web") != "prometheus" {
			t.Errorf("expected ts_query_web=prometheus got %q", r.URL.Query().Get("ts_query_web"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"packages":[
			{"package_id":"a","name":"prometheus","stars":12000,
			 "repository":{"name":"prometheus-community","kind":0,"verified_publisher":true}},
			{"package_id":"b","name":"kube-prometheus-stack","stars":8000,
			 "repository":{"name":"prometheus-community","kind":0}}
		]}`))
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL

	hits, err := c.SearchHelm(context.Background(), "prometheus", 20)
	if err != nil {
		t.Fatalf("SearchHelm: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Name != "prometheus" || !hits[0].Repository.VerifiedPublisher {
		t.Errorf("first hit = %+v", hits[0])
	}
}

func TestArtifactHubClient_SearchHelm_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	_, err := c.SearchHelm(context.Background(), "prometheus", 20)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsArtifactHubClass(err, AHErrRateLimited) {
		t.Errorf("expected rate_limited class, got %v", err)
	}
}

func TestArtifactHubClient_SearchHelm_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	_, err := c.SearchHelm(context.Background(), "x", 20)
	if !IsArtifactHubClass(err, AHErrServerError) {
		t.Errorf("expected server_error class, got %v", err)
	}
}

func TestArtifactHubClient_SearchHelm_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	_, err := c.SearchHelm(context.Background(), "x", 20)
	if !IsArtifactHubClass(err, AHErrMalformed) {
		t.Errorf("expected malformed class, got %v", err)
	}
}

func TestArtifactHubClient_SearchHelm_EmptyQuery(t *testing.T) {
	c := NewArtifactHubClient(nil)
	_, err := c.SearchHelm(context.Background(), "", 20)
	if !IsArtifactHubClass(err, AHErrInvalidInput) {
		t.Errorf("expected invalid_input, got %v", err)
	}
}

func TestArtifactHubClient_GetPackage_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/packages/helm/jetstack/cert-manager" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"package_id":"a","name":"cert-manager",
			"version":"1.20.2","app_version":"1.20.2","license":"Apache-2.0",
			"repository":{"name":"jetstack","kind":0,"verified_publisher":true}}`))
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL

	pkg, err := c.GetPackage(context.Background(), "jetstack", "cert-manager")
	if err != nil {
		t.Fatalf("GetPackage: %v", err)
	}
	if pkg.Name != "cert-manager" || pkg.License != "Apache-2.0" {
		t.Errorf("pkg = %+v", pkg)
	}
}

func TestArtifactHubClient_GetPackage_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	_, err := c.GetPackage(context.Background(), "jetstack", "missing")
	if !IsArtifactHubClass(err, AHErrNotFound) {
		t.Errorf("expected not_found, got %v", err)
	}
}

func TestArtifactHubClient_Probe_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

func TestArtifactHubClient_Probe_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewArtifactHubClient(nil)
	c.BaseURL = srv.URL
	err := c.Probe(context.Background())
	if !IsArtifactHubClass(err, AHErrServerError) {
		t.Errorf("expected server_error, got %v", err)
	}
}
