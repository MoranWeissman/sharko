package demo

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// MockArgocdServer is an HTTP server that mimics the ArgoCD REST API.
// It returns realistic fake data for clusters and applications.
type MockArgocdServer struct {
	server   *http.Server
	listener net.Listener
	mu       sync.RWMutex

	// In-memory state (allows write operations to be accepted)
	clusters []mockCluster
	apps     []mockApp
}

type mockCluster struct {
	Name          string            `json:"name"`
	Server        string            `json:"server"`
	ServerVersion string            `json:"serverVersion,omitempty"`
	Namespaces    []string          `json:"namespaces,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Info          mockClusterInfo   `json:"info"`
}

type mockClusterInfo struct {
	ConnectionState mockConnectionState `json:"connectionState"`
	ServerVersion   string              `json:"serverVersion"`
}

type mockConnectionState struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type mockApp struct {
	Metadata mockAppMetadata `json:"metadata"`
	Spec     mockAppSpec     `json:"spec"`
	Status   mockAppStatus   `json:"status"`
}

type mockAppMetadata struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	CreationTimestamp string `json:"creationTimestamp"`
}

type mockAppSpec struct {
	Project     string          `json:"project"`
	Source      mockSource      `json:"source"`
	Destination mockDestination `json:"destination"`
}

type mockSource struct {
	RepoURL        string `json:"repoURL"`
	Chart          string `json:"chart"`
	TargetRevision string `json:"targetRevision"`
}

type mockDestination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

type mockAppStatus struct {
	Sync         mockSyncStatus     `json:"sync"`
	Health       mockHealthStatus   `json:"health"`
	ReconciledAt string             `json:"reconciledAt"`
	History      []mockHistoryEntry `json:"history,omitempty"`
}

// mockHistoryEntry mirrors the ArgoCD application history entry shape
// internal/argocd/client.go's argocdApplicationItem decodes
// (status.history[].id/deployedAt/deployStartedAt/revision). Only
// generated (non-default) estates populate this — the default estate's
// buildMockApps leaves it nil/omitted, unchanged from before S1.
type mockHistoryEntry struct {
	ID              int    `json:"id"`
	DeployedAt      string `json:"deployedAt"`
	DeployStartedAt string `json:"deployStartedAt"`
	Revision        string `json:"revision,omitempty"`
}

type mockSyncStatus struct {
	Status string `json:"status"`
}

type mockHealthStatus struct {
	Status             string `json:"status"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// NewMockArgocdServer creates and starts a mock ArgoCD HTTP server on a random port.
func NewMockArgocdServer() (*MockArgocdServer, error) {
	return newMockArgocdServerWithData(buildMockClusters(), buildMockApps())
}

// NewMockArgocdServerWithConfig builds a mock ArgoCD server sized per cfg.
// For the default size (cfg.IsDefault()) it defers to NewMockArgocdServer
// unchanged. For any other size it derives the cluster/application list
// from a freshly generated estate (GenerateEstate) instead of the
// hand-written demoClusters/demoAddons.
func NewMockArgocdServerWithConfig(cfg ScaleConfig) (*MockArgocdServer, error) {
	if cfg.IsDefault() {
		return NewMockArgocdServer()
	}
	estate, err := GenerateEstate(cfg)
	if err != nil {
		return nil, fmt.Errorf("demo: generating estate for mock argocd server: %w", err)
	}
	return NewMockArgocdServerFromEstate(estate)
}

// NewMockArgocdServerFromEstate builds a mock ArgoCD server directly from
// an already-generated estate — see NewMockGitProviderFromEstate's doc for
// why SetupDemoServer prefers this over NewMockArgocdServerWithConfig.
func NewMockArgocdServerFromEstate(estate *GeneratedEstate) (*MockArgocdServer, error) {
	return newMockArgocdServerWithData(buildMockClustersFromEstate(estate), buildMockAppsFromEstate(estate))
}

// newMockArgocdServerWithData starts the shared HTTP mux + listener over a
// caller-supplied cluster/app list, factored out so both the default and
// generated-estate constructors share one place that wires up routes.
func newMockArgocdServerWithData(clusters []mockCluster, apps []mockApp) (*MockArgocdServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting mock argocd listener: %w", err)
	}

	s := &MockArgocdServer{
		listener: ln,
		clusters: clusters,
		apps:     apps,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/v1/clusters", s.handleClusters)
	mux.HandleFunc("/api/v1/applications", s.handleApplications)
	mux.HandleFunc("/api/v1/repositories", s.handleRepositories)
	mux.HandleFunc("/api/v1/projects", s.handleProjects)
	// Catch-all for named resources
	mux.HandleFunc("/", s.handleDynamic)

	s.server = &http.Server{Handler: mux}

	go s.server.Serve(ln) //nolint:errcheck

	return s, nil
}

// URL returns the base URL of the mock server.
func (s *MockArgocdServer) URL() string {
	return "http://" + s.listener.Addr().String()
}

// Close shuts down the mock server.
func (s *MockArgocdServer) Close() {
	s.server.Close()
}

// --- Route handlers ---

func (s *MockArgocdServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"Version":  "v2.12.0",
		"Platform": "linux/amd64",
	})
}

func (s *MockArgocdServer) handleClusters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		clusters := make([]mockCluster, len(s.clusters))
		copy(clusters, s.clusters)
		s.mu.RUnlock()
		writeJSON(w, map[string]interface{}{"items": clusters})

	case http.MethodPost:
		// Accept cluster registration — in-memory add
		var req mockCluster
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if req.Info.ConnectionState.Status == "" {
			req.Info.ConnectionState.Status = "Successful"
		}
		s.mu.Lock()
		s.clusters = append(s.clusters, req)
		s.mu.Unlock()
		writeJSON(w, req)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *MockArgocdServer) handleApplications(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		apps := make([]mockApp, len(s.apps))
		copy(apps, s.apps)
		s.mu.RUnlock()
		writeJSON(w, map[string]interface{}{"items": apps})

	case http.MethodPost:
		// Accept application creation
		var req mockApp
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if req.Status.Sync.Status == "" {
			req.Status.Sync.Status = "Synced"
		}
		if req.Status.Health.Status == "" {
			req.Status.Health.Status = "Healthy"
		}
		s.mu.Lock()
		s.apps = append(s.apps, req)
		s.mu.Unlock()
		writeJSON(w, req)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *MockArgocdServer) handleRepositories(w http.ResponseWriter, r *http.Request) {
	// Accept any repo registration
	writeJSON(w, map[string]string{"connectionState": "Successful"})
}

func (s *MockArgocdServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	// Accept project creation
	writeJSON(w, map[string]string{"metadata": "{}"})
}

// handleDynamic handles named-resource routes dynamically.
func (s *MockArgocdServer) handleDynamic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// DELETE /api/v1/clusters/{server} — encoded server URL
	if r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/clusters/") {
		server := strings.TrimPrefix(path, "/api/v1/clusters/")
		s.mu.Lock()
		filtered := s.clusters[:0]
		for _, c := range s.clusters {
			if c.Server != server && c.Name != server {
				filtered = append(filtered, c)
			}
		}
		s.clusters = filtered
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// PUT /api/v1/clusters/{server}?updateMask=... — update labels
	if r.Method == http.MethodPut && strings.HasPrefix(path, "/api/v1/clusters/") {
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	// GET /api/v1/applications/{name}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/applications/") {
		suffix := strings.TrimPrefix(path, "/api/v1/applications/")
		// Ignore sub-routes like /sync, /resource-tree, etc.
		parts := strings.SplitN(suffix, "/", 2)
		appName := parts[0]

		s.mu.RLock()
		var found *mockApp
		for i := range s.apps {
			if s.apps[i].Metadata.Name == appName {
				cp := s.apps[i]
				found = &cp
				break
			}
		}
		s.mu.RUnlock()

		if found == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, found)
		return
	}

	// POST /api/v1/applications/{name}/sync
	if r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v1/applications/") && strings.HasSuffix(path, "/sync") {
		writeJSON(w, map[string]string{"status": "Running"})
		return
	}

	// Default: 200 OK for any unrecognised route so the server doesn't break callers.
	writeJSON(w, map[string]string{"status": "ok"})
}

// --- Seed data builders ---

func buildMockClusters() []mockCluster {
	clusters := make([]mockCluster, 0, len(demoClusters))
	for _, dc := range demoClusters {
		status := "Successful"
		message := ""
		if dc.ConnStatus == "Failed" {
			status = "Failed"
			message = "dial tcp: connect: connection refused"
		}
		clusters = append(clusters, mockCluster{
			Name:          dc.Name,
			Server:        dc.Server,
			ServerVersion: dc.K8sVersion,
			Namespaces:    []string{},
			Labels: map[string]string{
				"region": dc.Region,
				"env":    dc.Env,
			},
			Info: mockClusterInfo{
				ConnectionState: mockConnectionState{
					Status:  status,
					Message: message,
				},
				ServerVersion: dc.K8sVersion,
			},
		})
	}
	return clusters
}

func buildMockApps() []mockApp {
	var apps []mockApp
	createdAt := "2024-11-15T10:00:00Z"
	reconciledAt := "2025-01-20T14:30:00Z"

	// Seed the bootstrap root app so ProbeBootstrapApp returns healthy and the
	// first-run wizard does not nag (LW-9, Story LW-F gap #1). The app name
	// must match orchestrator.BootstrapRootAppName exactly so init.go's
	// ProbeBootstrapApp finds it.
	apps = append(apps, mockApp{
		Metadata: mockAppMetadata{
			Name:              orchestrator.BootstrapRootAppName,
			Namespace:         "argocd",
			CreationTimestamp: createdAt,
		},
		Spec: mockAppSpec{
			Project: "sharko",
			Source: mockSource{
				RepoURL:        "https://github.com/demo/sharko-addons",
				Chart:          "",
				TargetRevision: "HEAD",
			},
			Destination: mockDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "argocd",
			},
		},
		Status: mockAppStatus{
			Sync:         mockSyncStatus{Status: "Synced"},
			Health:       mockHealthStatus{Status: "Healthy", LastTransitionTime: reconciledAt},
			ReconciledAt: reconciledAt,
		},
	})

	for _, cluster := range demoClusters {
		for addonName, version := range cluster.Addons {
			// Determine sync/health status — perf-asia has issues, staging is mostly ok
			syncStatus := "Synced"
			healthStatus := "Healthy"
			switch {
			case cluster.Name == "perf-asia":
				// Degraded cluster — one addon out of sync, one progressing
				if addonName == "cert-manager" {
					syncStatus = "OutOfSync"
					healthStatus = "Degraded"
				} else if addonName == "kube-prometheus-stack" {
					healthStatus = "Progressing"
				}
			case cluster.Name == "staging-eu" && addonName == "cert-manager":
				// Older version on staging — out of sync
				syncStatus = "OutOfSync"
			}

			addon := findAddon(addonName)
			repoURL := "https://charts.example.com"
			if addon != nil {
				repoURL = addon.RepoURL
			}

			appName := fmt.Sprintf("%s-%s", addonName, cluster.Name)
			apps = append(apps, mockApp{
				Metadata: mockAppMetadata{
					Name:              appName,
					Namespace:         "argocd",
					CreationTimestamp: createdAt,
				},
				Spec: mockAppSpec{
					Project: "sharko",
					Source: mockSource{
						RepoURL:        repoURL,
						Chart:          addonName,
						TargetRevision: version,
					},
					Destination: mockDestination{
						Server:    cluster.Server,
						Namespace: addonName,
					},
				},
				Status: mockAppStatus{
					Sync:         mockSyncStatus{Status: syncStatus},
					Health:       mockHealthStatus{Status: healthStatus, LastTransitionTime: reconciledAt},
					ReconciledAt: reconciledAt,
				},
			})
		}
	}
	return apps
}

// findAddon returns the addon definition for the given name, or nil.
func findAddon(name string) *Addon {
	for i := range demoAddons {
		if demoAddons[i].Name == name {
			return &demoAddons[i]
		}
	}
	return nil
}

// buildMockClustersFromEstate is buildMockClusters' generated-estate
// analogue: same shape, driven by a GeneratedEstate's registered clusters
// instead of the hand-written demoClusters. Two coverage-addition
// adjustments: estate.MissingClusterNames are skipped entirely (the
// "missing ArgoCD secret" exemplar — ArgoCD simply doesn't know about
// them), and estate.ArgoOnlyClusters are appended (the "discovered /
// not_in_git" and "orphan" exemplars — clusters ArgoCD knows about that
// have no git entry at all).
func buildMockClustersFromEstate(estate *GeneratedEstate) []mockCluster {
	missing := make(map[string]bool, len(estate.MissingClusterNames))
	for _, name := range estate.MissingClusterNames {
		missing[name] = true
	}

	toMockCluster := func(dc Cluster) mockCluster {
		status := "Successful"
		message := ""
		if dc.ConnStatus == "Failed" {
			status = "Failed"
			message = "dial tcp: connect: connection refused"
		}
		return mockCluster{
			Name:          dc.Name,
			Server:        dc.Server,
			ServerVersion: dc.K8sVersion,
			Namespaces:    []string{},
			Labels: map[string]string{
				"region": dc.Region,
				"env":    dc.Env,
			},
			Info: mockClusterInfo{
				ConnectionState: mockConnectionState{
					Status:  status,
					Message: message,
				},
				ServerVersion: dc.K8sVersion,
			},
		}
	}

	clusters := make([]mockCluster, 0, len(estate.Clusters)+len(estate.ArgoOnlyClusters))
	for _, dc := range estate.Clusters {
		if missing[dc.Name] {
			continue
		}
		clusters = append(clusters, toMockCluster(dc))
	}
	for _, ao := range estate.ArgoOnlyClusters {
		clusters = append(clusters, toMockCluster(ao.Cluster))
	}
	return clusters
}

// buildMockAppsFromEstate is buildMockApps' generated-estate analogue: one
// ArgoCD application per (cluster, enabled addon) cell, carrying the
// per-cell health/sync/history detail GenerateEstate already computed
// (deployments), instead of the hand-written per-cluster special cases
// (perf-asia/staging-eu) buildMockApps hard-codes for the default estate.
func buildMockAppsFromEstate(estate *GeneratedEstate) []mockApp {
	createdAt := "2024-11-15T10:00:00Z"

	repoByAddon := make(map[string]string, len(estate.Addons))
	for _, a := range estate.Addons {
		repoByAddon[a.Name] = a.RepoURL
	}

	apps := make([]mockApp, 0, len(estate.Clusters)*minAddonsPerCluster)

	// Seed the bootstrap root app, same as buildMockApps, so
	// ProbeBootstrapApp returns healthy and the first-run wizard doesn't nag.
	apps = append(apps, mockApp{
		Metadata: mockAppMetadata{
			Name:              orchestrator.BootstrapRootAppName,
			Namespace:         "argocd",
			CreationTimestamp: createdAt,
		},
		Spec: mockAppSpec{
			Project: "sharko",
			Source: mockSource{
				RepoURL:        "https://github.com/demo/sharko-addons",
				Chart:          "",
				TargetRevision: "HEAD",
			},
			Destination: mockDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "argocd",
			},
		},
		Status: mockAppStatus{
			Sync:         mockSyncStatus{Status: "Synced"},
			Health:       mockHealthStatus{Status: "Healthy", LastTransitionTime: createdAt},
			ReconciledAt: createdAt,
		},
	})

	missing := make(map[string]bool, len(estate.MissingClusterNames))
	for _, name := range estate.MissingClusterNames {
		missing[name] = true
	}

	for _, cluster := range estate.Clusters {
		if missing[cluster.Name] {
			// "missing ArgoCD secret" exemplar — ArgoCD knows nothing
			// about this cluster, including any apps deployed to it.
			continue
		}
		cellsByAddon := estate.Deployments[cluster.Name]
		for addonName, version := range cluster.Addons {
			cell := cellsByAddon[addonName]
			reconciledAt := ""
			if len(cell.History) > 0 {
				reconciledAt = cell.History[len(cell.History)-1].DeployedAt
			}

			var history []mockHistoryEntry
			for _, h := range cell.History {
				history = append(history, mockHistoryEntry{
					ID:              h.ID,
					DeployedAt:      h.DeployedAt,
					DeployStartedAt: h.DeployStartedAt,
					Revision:        h.Revision,
				})
			}

			repoURL := repoByAddon[addonName]
			if repoURL == "" {
				repoURL = "https://charts.example.com"
			}

			appName := fmt.Sprintf("%s-%s", addonName, cluster.Name)
			apps = append(apps, mockApp{
				Metadata: mockAppMetadata{
					Name:              appName,
					Namespace:         "argocd",
					CreationTimestamp: createdAt,
				},
				Spec: mockAppSpec{
					Project: "sharko",
					Source: mockSource{
						RepoURL:        repoURL,
						Chart:          addonName,
						TargetRevision: version,
					},
					Destination: mockDestination{
						Server:    cluster.Server,
						Namespace: addonName,
					},
				},
				Status: mockAppStatus{
					Sync:         mockSyncStatus{Status: cell.Sync},
					Health:       mockHealthStatus{Status: cell.Health, LastTransitionTime: reconciledAt},
					ReconciledAt: reconciledAt,
					History:      history,
				},
			})
		}
	}

	// Connectivity-check apps (coverage addition) — one per disconnected
	// showcase cluster in estate.ConnectivityCheckApps, exercising every
	// internal/api/connectivity_status.go verdict (verified_check,
	// check_pending, check_failed).
	serverByCluster := make(map[string]string, len(estate.Clusters))
	for _, c := range estate.Clusters {
		serverByCluster[c.Name] = c.Server
	}
	for _, check := range estate.ConnectivityCheckApps {
		apps = append(apps, mockApp{
			Metadata: mockAppMetadata{
				Name:              orchestrator.ConnectivityCheckAppPrefix + check.ClusterName,
				Namespace:         "argocd",
				CreationTimestamp: check.CreatedAt,
			},
			Spec: mockAppSpec{
				Project: "sharko",
				Source: mockSource{
					RepoURL:        "https://github.com/demo/sharko-addons",
					Chart:          "",
					TargetRevision: "main",
				},
				Destination: mockDestination{
					Server:    serverByCluster[check.ClusterName],
					Namespace: "sharko-test",
				},
			},
			Status: mockAppStatus{
				Sync:         mockSyncStatus{Status: check.Sync},
				Health:       mockHealthStatus{Status: check.Health, LastTransitionTime: check.CreatedAt},
				ReconciledAt: check.CreatedAt,
			},
		})
	}

	return apps
}

// writeJSON is a local helper for the mock server.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
