package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/auth"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/service"
	"golang.org/x/crypto/bcrypt"
)

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	connSvc          *service.ConnectionService
	clusterSvc       *service.ClusterService
	addonSvc         *service.AddonService
	dashboardSvc     *service.DashboardService
	observabilitySvc *service.ObservabilityService
	upgradeSvc       *service.UpgradeService
	aiClient          *ai.Client
	agentMemory       *ai.MemoryStore
	authStore         *auth.Store
	aiConfigStore     *config.AIConfigStore

	// Write API dependencies (optional — set via SetOrchestrator).
	credProvider providers.ClusterCredentialsProvider
	providerCfg  *providers.Config
	repoPaths    orchestrator.RepoPathsConfig
	gitopsCfg    orchestrator.GitOpsConfig
	gitMu        sync.Mutex // shared mutex serializing all Git operations across requests

	// Remote secret management (optional — set via SetAddonSecretDefs).
	addonSecretDefs map[string]orchestrator.AddonSecretDefinition
	secretFetcher   orchestrator.SecretValueFetcher

	// Template filesystem for POST /api/v1/init (always available).
	templateFS fs.FS
}

// NewServer creates a new API server.
func NewServer(
	connSvc *service.ConnectionService,
	clusterSvc *service.ClusterService,
	addonSvc *service.AddonService,
	dashboardSvc *service.DashboardService,
	observabilitySvc *service.ObservabilityService,
	upgradeSvc *service.UpgradeService,
	aiClient *ai.Client,
) *Server {
	// Initialize agent memory — store in /tmp for containers (writable), or local dir for dev
	memoryPath := "/tmp/sharko-agent-memory.json"
	agentMemory := ai.NewMemoryStore(memoryPath)

	// Initialize auth store (auto-detects K8s vs local mode)
	authStore := auth.NewStore()

	if !authStore.HasUsers() {
		slog.Warn("WARNING: Authentication is DISABLED — all API endpoints are publicly accessible. Configure users via K8s ConfigMap or SHARKO_AUTH_USER env var.")
	}

	return &Server{
		connSvc:           connSvc,
		clusterSvc:        clusterSvc,
		addonSvc:          addonSvc,
		dashboardSvc:      dashboardSvc,
		observabilitySvc:  observabilitySvc,
		upgradeSvc:        upgradeSvc,
		aiClient:          aiClient,
		agentMemory:       agentMemory,
		authStore:         authStore,
		aiConfigStore:     nil, // set via SetAIConfigStore
		addonSecretDefs:   make(map[string]orchestrator.AddonSecretDefinition),
	}
}

// SetAIConfigStore sets the persistent AI config store (K8s mode only).
func (s *Server) SetAIConfigStore(store *config.AIConfigStore) {
	s.aiConfigStore = store
}

// SetTemplateFS sets the embedded template filesystem for POST /api/v1/init.
func (s *Server) SetTemplateFS(tfs fs.FS) {
	s.templateFS = tfs
}

// SetWriteAPIDeps configures the dependencies for write API endpoints.
// credProvider is the cluster credentials backend (e.g. AWS SM, K8s secrets).
// provCfg holds the provider configuration for system info endpoints.
// paths and gitops hold the repo layout and gitops commit settings.
func (s *Server) SetWriteAPIDeps(credProvider providers.ClusterCredentialsProvider, provCfg *providers.Config, paths orchestrator.RepoPathsConfig, gitops orchestrator.GitOpsConfig) {
	s.credProvider = credProvider
	s.providerCfg = provCfg
	s.repoPaths = paths
	s.gitopsCfg = gitops
}

// SetAddonSecretDefs sets the addon secret definitions (loaded from env/config).
func (s *Server) SetAddonSecretDefs(defs map[string]orchestrator.AddonSecretDefinition) {
	s.addonSecretDefs = defs
}

// SetSecretFetcher sets the secret value fetcher for remote cluster secret operations.
func (s *Server) SetSecretFetcher(fetcher orchestrator.SecretValueFetcher) {
	s.secretFetcher = fetcher
}

// NewRouter builds the HTTP router with all API routes and static file serving.
// staticFS can be nil if no static files are available (e.g., dev mode).
func NewRouter(srv *Server, staticFS fs.FS) http.Handler {
	startSessionCleanup()
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/v1/health", srv.handleHealth)

	// Connections
	mux.HandleFunc("GET /api/v1/connections/", srv.handleListConnections)
	mux.HandleFunc("POST /api/v1/connections/", srv.handleCreateConnection)
	mux.HandleFunc("PUT /api/v1/connections/{name}", srv.handleUpdateConnection)
	mux.HandleFunc("DELETE /api/v1/connections/{name}", srv.handleDeleteConnection)
	mux.HandleFunc("POST /api/v1/connections/active", srv.handleSetActiveConnection)
	mux.HandleFunc("POST /api/v1/connections/test", srv.handleTestConnection)
	mux.HandleFunc("POST /api/v1/connections/test-credentials", srv.handleTestCredentials)
	mux.HandleFunc("GET /api/v1/connections/discover-argocd", srv.handleDiscoverArgocd)

	// Clusters (read)
	mux.HandleFunc("GET /api/v1/clusters", srv.handleListClusters)
	mux.HandleFunc("GET /api/v1/clusters/{name}/values", srv.handleGetClusterValues)
	mux.HandleFunc("GET /api/v1/clusters/{name}/config-diff", srv.handleGetConfigDiff)
	mux.HandleFunc("GET /api/v1/clusters/{name}/comparison", srv.handleGetClusterComparison)
	mux.HandleFunc("GET /api/v1/clusters/{name}", srv.handleGetCluster)

	// Clusters (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/clusters", srv.handleRegisterCluster)
	mux.HandleFunc("DELETE /api/v1/clusters/{name}", srv.handleDeregisterCluster)
	mux.HandleFunc("PATCH /api/v1/clusters/{name}", srv.handleUpdateClusterAddons)
	mux.HandleFunc("POST /api/v1/clusters/{name}/refresh", srv.handleRefreshClusterCredentials)

	// Init (orchestrator-backed)
	mux.HandleFunc("POST /api/v1/init", srv.handleInit)

	// Addons (write — orchestrator-backed)
	mux.HandleFunc("POST /api/v1/addons", srv.handleAddAddon)
	mux.HandleFunc("DELETE /api/v1/addons/{name}", srv.handleRemoveAddon)

	// Addon secrets (definition CRUD)
	mux.HandleFunc("GET /api/v1/addon-secrets", srv.handleListAddonSecrets)
	mux.HandleFunc("POST /api/v1/addon-secrets", srv.handleCreateAddonSecret)
	mux.HandleFunc("DELETE /api/v1/addon-secrets/{addon}", srv.handleDeleteAddonSecret)

	// Cluster secrets (remote cluster operations)
	mux.HandleFunc("GET /api/v1/clusters/{name}/secrets", srv.handleListClusterSecrets)
	mux.HandleFunc("POST /api/v1/clusters/{name}/secrets/refresh", srv.handleRefreshClusterSecrets)

	// Fleet status
	mux.HandleFunc("GET /api/v1/fleet/status", srv.handleGetFleetStatus)

	// System
	mux.HandleFunc("GET /api/v1/providers", srv.handleGetProviders)
	mux.HandleFunc("POST /api/v1/providers/test", srv.handleTestProvider)
	mux.HandleFunc("POST /api/v1/providers/test-config", srv.handleTestProviderConfig)
	mux.HandleFunc("GET /api/v1/config", srv.handleGetConfig)

	// Addons (read)
	mux.HandleFunc("GET /api/v1/addons/list", srv.handleListAddons)
	mux.HandleFunc("GET /api/v1/addons/catalog", srv.handleGetAddonCatalog)
	mux.HandleFunc("GET /api/v1/addons/version-matrix", srv.handleGetVersionMatrix)
	mux.HandleFunc("GET /api/v1/addons/{name}/values", srv.handleGetAddonValues)
	mux.HandleFunc("GET /api/v1/addons/{name}", srv.handleGetAddonDetail)

	// Dashboard
	mux.HandleFunc("GET /api/v1/dashboard/stats", srv.handleGetDashboardStats)
	mux.HandleFunc("GET /api/v1/dashboard/attention", srv.handleGetAttentionItems)
	mux.HandleFunc("GET /api/v1/dashboard/pull-requests", srv.handleGetPullRequests)

	// Embedded dashboards (persisted in K8s ConfigMap)
	mux.HandleFunc("GET /api/v1/embedded-dashboards", srv.handleListDashboards)
	mux.HandleFunc("POST /api/v1/embedded-dashboards", srv.handleSaveDashboards)

	// Upgrade Impact Checker
	mux.HandleFunc("GET /api/v1/upgrade/{addonName}/versions", srv.handleListUpgradeVersions)
	mux.HandleFunc("POST /api/v1/upgrade/check", srv.handleCheckUpgrade)
	mux.HandleFunc("POST /api/v1/upgrade/ai-summary", srv.handleGetAISummary)
	mux.HandleFunc("GET /api/v1/upgrade/ai-status", srv.handleGetAIStatus)

	// AI Configuration
	mux.HandleFunc("GET /api/v1/ai/config", srv.handleGetAIConfig)
	mux.HandleFunc("POST /api/v1/ai/config", srv.handleSaveAIConfig)
	mux.HandleFunc("POST /api/v1/ai/provider", srv.handleSetAIProvider)
	mux.HandleFunc("POST /api/v1/ai/test", srv.handleTestAI)
	mux.HandleFunc("POST /api/v1/ai/test-config", srv.handleTestAIConfig)

	// Observability
	mux.HandleFunc("GET /api/v1/observability/overview", srv.handleGetObservabilityOverview)

	// AI Agent
	mux.HandleFunc("POST /api/v1/agent/chat", srv.handleAgentChat)
	mux.HandleFunc("POST /api/v1/agent/reset", srv.handleAgentReset)

	// Documentation
	mux.HandleFunc("GET /api/v1/docs/list", srv.handleDocsList)
	mux.HandleFunc("GET /api/v1/docs/{slug}", srv.handleDocsGet)

	// Cluster info
	mux.HandleFunc("GET /api/v1/cluster/nodes", srv.handleGetNodeInfo)

	// Auth (login is rate-limited: 10 attempts per IP per minute)
	loginRL := newLoginRateLimiter(10, 1*time.Minute)
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !loginRL.Allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "too many login attempts, please try again later")
			return
		}
		srv.handleLogin(w, r)
	})
	mux.HandleFunc("POST /api/v1/auth/update-password", srv.handleUpdatePassword)
	mux.HandleFunc("POST /api/v1/auth/hash", srv.handleHashPassword)

	// User management (admin only)
	mux.HandleFunc("GET /api/v1/users", srv.handleListUsers)
	mux.HandleFunc("POST /api/v1/users", srv.handleCreateUser)
	mux.HandleFunc("PUT /api/v1/users/{username}", srv.handleUpdateUser)
	mux.HandleFunc("DELETE /api/v1/users/{username}", srv.handleDeleteUser)
	mux.HandleFunc("POST /api/v1/users/{username}/reset-password", srv.handleResetPassword)

	// Static files (SPA)
	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file; if not found, serve index.html for SPA routing
			path := r.URL.Path
			if path == "/" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticFS, path[1:]); err != nil {
				// File not found — serve index.html for client-side routing
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	// Wrap with middleware
	var handler http.Handler = mux
	handler = maxBodySize(handler, 1<<20) // 1MB request body limit
	handler = srv.basicAuthMiddleware(handler)
	handler = corsMiddleware(handler)
	handler = loggingMiddleware(handler)

	return handler
}

// maxBodySize limits request body size to prevent OOM from large payloads.
func maxBodySize(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// --- Login rate limiter ---

type loginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

// Allow checks whether the given IP is within the rate limit.
// It cleans up expired entries on each call.
func (rl *loginRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Clean up all stale entries periodically (every call is cheap enough for login traffic)
	for k, times := range rl.attempts {
		filtered := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			delete(rl.attempts, k)
		} else {
			rl.attempts[k] = filtered
		}
	}

	if len(rl.attempts[ip]) >= rl.limit {
		return false
	}
	rl.attempts[ip] = append(rl.attempts[ip], now)
	return true
}

// clientIP extracts the client IP, preferring X-Forwarded-For (behind ALB/proxy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may contain multiple IPs; the first is the real client
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Fall back to RemoteAddr (strip port)
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// --- Session token auth ---

type sessionInfo struct {
	Username string
	Expiry   time.Time
}

var (
	activeSessions   = make(map[string]*sessionInfo) // token -> session
	sessionsMu       sync.RWMutex
	sessionLifetime  = 24 * time.Hour
	sessionCleanOnce sync.Once
)

func startSessionCleanup() {
	sessionCleanOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				sessionsMu.Lock()
				now := time.Now()
				for token, sess := range activeSessions {
					if now.After(sess.Expiry) {
						delete(activeSessions, token)
					}
				}
				sessionsMu.Unlock()
			}
		}()
	})
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func isValidSession(token string) bool {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := activeSessions[token]
	return ok && time.Now().Before(sess.Expiry)
}

func getSessionUser(token string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := activeSessions[token]
	if !ok {
		return ""
	}
	return sess.Username
}

// handleLogin validates credentials and returns a session token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// If no auth configured, allow any login
	if !s.authStore.HasUsers() {
		token := generateToken()
		sessionsMu.Lock()
		activeSessions[token] = &sessionInfo{Username: "anonymous", Expiry: time.Now().Add(sessionLifetime)}
		sessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
		return
	}

	if !s.authStore.ValidateCredentials(req.Username, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token := generateToken()
	sessionsMu.Lock()
	activeSessions[token] = &sessionInfo{Username: req.Username, Expiry: time.Now().Add(sessionLifetime)}
	sessionsMu.Unlock()

	user := s.authStore.GetUser(req.Username)
	role := "admin"
	if user != nil {
		role = user.Role
	}

	slog.Info("user logged in", "username", req.Username, "role", role)
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "username": req.Username, "role": role})
}

// basicAuthMiddleware enforces token-based auth on all API routes.
// Accepts: Authorization: Bearer <token>
// Skips: health checks, login endpoint, and static files.
func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	// If no users configured, skip auth entirely
	if !s.authStore.HasUsers() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Skip auth for: health, login, static files
		if path == "/api/v1/health" || path == "/api/v1/auth/login" || !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Check Bearer token
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if isValidSession(token) {
				// Inject username into request for downstream handlers
				r.Header.Set("X-Sharko-User", getSessionUser(token))
				next.ServeHTTP(w, r)
				return
			}
		}

		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

// handleUpdatePassword allows changing the password. Verifies current password first.
func (s *Server) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	if !s.authStore.HasUsers() {
		writeError(w, http.StatusBadRequest, "no password configured")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.NewPassword == "" || len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	if err := s.authStore.UpdatePassword(username, req.CurrentPassword, req.NewPassword); err != nil {
		if strings.Contains(err.Error(), "incorrect") {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if strings.Contains(err.Error(), "at least") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

// handleHashPassword generates a bcrypt hash from a plaintext password.
// Only available when auth is disabled (no users configured) for initial setup.
func (s *Server) handleHashPassword(w http.ResponseWriter, r *http.Request) {
	if s.authStore.HasUsers() {
		writeError(w, http.StatusForbidden, "hash endpoint is only available when auth is disabled")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate hash")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"hash": string(hash)})
}

// corsMiddleware adds CORS and security headers.
func corsMiddleware(next http.Handler) http.Handler {
	corsOrigin := os.Getenv("SHARKO_CORS_ORIGIN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// CORS origin
		origin := r.Header.Get("Origin")
		if corsOrigin == "*" {
			// Dev mode: allow all origins
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if corsOrigin != "" {
			// Explicit origin configured
			if origin == corsOrigin {
				w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
				w.Header().Set("Vary", "Origin")
			}
		} else {
			// Default: same-origin only — reflect Origin if it matches Host
			if origin != "" {
				host := r.Host
				// Check if origin matches the host (same-origin)
				if strings.Contains(origin, "://"+host) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Sharko-Connection")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware logs each request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sr, r)
		slog.Info("request completed", "method", r.Method, "path", r.URL.Path, "status", sr.statusCode, "duration", time.Since(start))
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding response: %v", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
// v1.39.3 route fix
