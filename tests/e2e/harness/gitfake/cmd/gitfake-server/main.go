// Command gitfake-server is the standalone HTTP entrypoint for the
// in-memory git harness (tests/e2e/harness/gitfake_core.go). It wraps the
// shared GitFake type in a real *http.Server so the same git-protocol
// surface can be deployed as a Pod inside a kind cluster.
//
// Used to give the Sharko pod a real in-cluster git endpoint that the
// git-host allowlist can permit.
//
// Configuration via environment variables (all optional):
//
//	LISTEN_ADDR   listen address, default ":8080"
//	REPO_NAME     repo path-segment name (without ".git"), default "sharko-e2e"
//	SEED_BRANCH   default branch on the initial empty repo, default "main"
//	SEED_FILE     path to seed a single file at (relative to repo root)
//	SEED_CONTENT  content to write at SEED_FILE (empty allowed)
//
// SEED_FILE and SEED_CONTENT are paired — both must be set (or both unset).
// For richer seeding, push to the repo over HTTP after start.
//
// SIGTERM and SIGINT trigger a graceful shutdown with a 5s deadline; in-
// flight requests get that long to drain before the process exits.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

func main() {
	listenAddr := envDefault("LISTEN_ADDR", ":8080")
	repoName := envDefault("REPO_NAME", "sharko-e2e")
	seedBranch := envDefault("SEED_BRANCH", "main")
	seedFile := strings.TrimSpace(os.Getenv("SEED_FILE"))
	seedContent := os.Getenv("SEED_CONTENT")

	slog.Info("gitfake-server: starting",
		"listen", listenAddr, "repo", repoName+".git", "branch", seedBranch)

	gf, err := harness.NewGitFakeForServer(repoName, seedBranch)
	if err != nil {
		slog.Error("gitfake-server: init gitfake failed", "error", err)
		os.Exit(1)
	}
	defer gf.Close()

	if seedFile != "" {
		hash, err := gf.SeedFile(seedFile, seedContent, "seed: initial file from SEED_FILE/SEED_CONTENT")
		if err != nil {
			slog.Error("gitfake-server: seed failed", "seed_file", seedFile, "error", err)
			os.Exit(1)
		}
		slog.Info("gitfake-server: seeded",
			"path", seedFile, "bytes", len(seedContent), "hash", hash)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", loggingMiddleware(gf.ServeHTTP))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run the listener in the background so the main goroutine can wait
	// on signals. errCh surfaces a listener-bind failure (port in use,
	// permission denied, etc.) — those exit non-zero immediately.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("gitfake-server: listening",
			"addr", listenAddr, "repo_url_template", "http://<host>/"+repoName+".git")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("gitfake-server: shutting down", "signal", sig.String(), "timeout", "5s")
	case err := <-errCh:
		slog.Error("gitfake-server: listener failed", "error", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("gitfake-server: graceful shutdown error", "error", err)
		// Best-effort close — Shutdown timed out and forcibly closes
		// listeners on context expiry. Nothing further to do.
	}
	slog.Info("gitfake-server: stopped")
}

// loggingMiddleware emits a one-line request log per inbound HTTP call.
// Sharko's harness traffic is low-volume (git clone/fetch/push from a
// single in-cluster pod), so a per-request log is useful and cheap.
func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		slog.Info("gitfake-server: request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", queryForLog(r.URL.RawQuery),
			"duration", time.Since(start))
	}
}

func queryForLog(raw string) string {
	if raw == "" {
		return ""
	}
	return "?" + raw
}

// envDefault returns os.Getenv(key) if non-empty (after trimming) and
// otherwise returns def.
func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
