// Package harness — gitfake core.
//
// This file holds the non-test surface of the in-memory git server. It is
// intentionally NOT gated by the `e2e` build tag so the standalone server
// binary at tests/e2e/harness/gitfake/cmd/gitfake-server can import the
// HTTP handler without pulling in the test-only constructors.
//
// The test-coupled constructor (StartGitFake), CommitFile/LatestCommit/
// FileAt/ListBranches helpers live in gitfake.go behind the `e2e` build
// tag. Both files share the same `GitFake` type defined here.
//
// Split out of gitfake.go so the in-process API stays stable while a
// parallel pod-mode can serve the same git-protocol surface from a
// kind Pod.
package harness

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"
)

// defaultRepoName is the single repo hosted by every GitFake. Tests address
// it as Addr/<defaultRepoName>.git.
const defaultRepoName = "sharko-e2e"

// GitFake is an in-memory git server for e2e tests AND for the standalone
// containerised harness. It hosts ONE bare-style repository (with an
// in-memory worktree for ergonomic CommitFile) reachable over HTTP at Addr.
// Tests use the Commit/File helpers directly against the in-memory storage;
// Sharko (or any other client) can clone/fetch/push via the HTTP smart
// protocol at RepoURL.
//
// In-process (test) mode: construct via StartGitFake(t) — auto-cleanup is
// wired via t.Cleanup, no Close() needed.
//
// Standalone (binary) mode: construct via NewGitFakeForServer and call
// ServeHTTP from a real *http.Server. The caller owns shutdown via Close().
type GitFake struct {
	// Addr is the base URL of the HTTP smart-protocol server (e.g.
	// "http://127.0.0.1:34567"). Useful when constructing alternative
	// repo paths in tests, though today we host exactly one repo.
	// Empty when the GitFake is driven by an external *http.Server.
	Addr string
	// RepoName is the path-segment name of the hosted repo (without the
	// trailing ".git"). Defaults to "sharko-e2e".
	RepoName string
	// RepoURL is the full clone URL: Addr + "/" + RepoName + ".git".
	// This is the value to feed into sharko's git.repo_url config.
	// Empty when the GitFake is driven by an external *http.Server (the
	// caller knows its own listen address).
	RepoURL string

	storage *memory.Storage      // in-memory git storage
	repo    *git.Repository      // helper handle backed by storage
	server  *httptest.Server     // HTTP wrapper (in-process mode only)
	mu      sync.Mutex           // serialises HTTP smart-protocol requests
	loader  server.MapLoader     // single-repo loader for transport server
	upx     transport.Transport  // upload-pack server (clone/fetch)
	rcv     transport.Transport  // receive-pack server (push)
	closed  bool
}

// NewGitFakeForServer constructs a GitFake suitable for embedding inside a
// caller-owned HTTP server (the standalone gitfake-server binary). The
// returned value has NO httptest server attached; the caller wires
// ServeHTTP to its own *http.Server.
//
// repoName overrides the path-segment name; pass "" for the default
// "sharko-e2e".
//
// The repo is initialised with a single empty "init" commit on the given
// branch (default "main") so the "is this repo initialised" probe in
// sharko's git client finds a HEAD.
//
// Unlike StartGitFake, this constructor returns errors instead of calling
// t.Fatalf — the binary main() decides how to react (typically log.Fatal).
func NewGitFakeForServer(repoName, defaultBranch string) (*GitFake, error) {
	if repoName == "" {
		repoName = defaultRepoName
	}
	branch := plumbing.Main
	if defaultBranch != "" {
		branch = plumbing.NewBranchReferenceName(defaultBranch)
	}

	storage := memory.NewStorage()
	wt := memfs.New()
	repo, err := git.InitWithOptions(storage, wt, git.InitOptions{
		DefaultBranch: branch,
	})
	if err != nil {
		return nil, fmt.Errorf("init repo: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	if _, err := worktree.Commit("init: empty seed commit", &git.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  "sharko-e2e",
			Email: "e2e@sharko.invalid",
			When:  time.Unix(0, 0).UTC(),
		},
	}); err != nil {
		return nil, fmt.Errorf("seed commit: %w", err)
	}

	g := &GitFake{
		RepoName: repoName,
		storage:  storage,
		repo:     repo,
		loader:   server.MapLoader{},
	}
	g.upx = server.NewServer(g.loader)
	g.rcv = server.NewServer(g.loader)

	// Register the storer under every key the transport server might
	// derive from inbound requests. MapLoader.Load looks up by
	// transport.Endpoint.String(); our handler builds an endpoint with
	// only Protocol+Path set (Host is empty since we don't know our own
	// listen address from inside the handler), which serialises to
	// "http:///<repo>.git". The bare-path variants ("/repo.git" and
	// "repo.git") are kept for compatibility with any client that hits
	// the loader directly. The absolute-URL variant is appended later by
	// startInProcessHTTP once httptest assigns a port.
	g.loader["http:///"+g.RepoName+".git"] = storage
	g.loader["/"+g.RepoName+".git"] = storage
	g.loader[g.RepoName+".git"] = storage

	return g, nil
}

// SeedFile commits content at path on the repository's default branch with
// the given message. Returns the new commit's SHA.
//
// This is the non-test seam used by the standalone binary to honour
// SEED_FILE / SEED_CONTENT env vars; in-process tests use CommitFile(t,
// ...) which carries a *testing.T for fatal-on-error semantics.
func (g *GitFake) SeedFile(path, content, message string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		return "", fmt.Errorf("gitfake: closed")
	}
	wt, err := g.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("worktree: %w", err)
	}
	f, err := wt.Filesystem.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %q: %w", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %q: %w", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		return "", fmt.Errorf("add %q: %w", path, err)
	}
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "sharko-e2e",
			Email: "e2e@sharko.invalid",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return hash.String(), nil
}

// Close stops the HTTP server (if owned) and releases in-memory storage.
// Safe to call multiple times. In-process mode wires this through
// t.Cleanup; standalone mode (binary) calls it explicitly on shutdown.
func (g *GitFake) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.closed = true
	if g.server != nil {
		g.server.Close()
	}
	g.storage = nil
	g.repo = nil
}

// startInProcessHTTP attaches an httptest.Server bound to a random
// localhost port. Used only by the test-mode StartGitFake constructor in
// gitfake.go; the standalone binary wires its own *http.Server instead.
//
// Returns an error if the listener fails to bind. On success, Addr +
// RepoURL are populated and the loader is finalised with the absolute URL
// key (which the transport server can derive from inbound request URLs).
func (g *GitFake) startInProcessHTTP() error {
	g.server = httptest.NewServer(http.HandlerFunc(g.ServeHTTP))
	g.Addr = g.server.URL
	g.RepoURL = g.Addr + "/" + g.RepoName + ".git"
	g.loader[g.RepoURL] = g.storage
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(g.Addr, "http://"), 2*time.Second); err != nil {
		g.server.Close()
		return fmt.Errorf("server not reachable at %s: %w", g.Addr, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP smart-protocol implementation
// ---------------------------------------------------------------------------
//
// We implement the three endpoints required by the Smart HTTP protocol:
//
//   - GET  /<repo>.git/info/refs?service=git-upload-pack
//   - GET  /<repo>.git/info/refs?service=git-receive-pack
//   - POST /<repo>.git/git-upload-pack
//   - POST /<repo>.git/git-receive-pack
//
// Pre-receive/post-receive hooks, side-band-64k progress messages, and
// shallow/partial clone capabilities are NOT exercised here — the server
// advertises only what go-git's own server transport supports natively.
//
// This is enough for `git clone`, `git fetch`, and `git push` from a real
// `git` CLI client AND from go-git client code.

// ServeHTTP is the smart-HTTP entrypoint. Exported so the standalone
// gitfake-server binary can mount it on its own *http.Server. In-process
// tests reach it indirectly via the httptest server attached in
// startInProcessHTTP.
func (g *GitFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only serve under /<repo>.git/. Anything else 404s.
	prefix := "/" + g.RepoName + ".git"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, prefix)

	switch {
	case r.Method == http.MethodGet && suffix == "/info/refs":
		g.handleInfoRefs(w, r)
	case r.Method == http.MethodPost && suffix == "/git-upload-pack":
		g.handleUploadPack(w, r)
	case r.Method == http.MethodPost && suffix == "/git-receive-pack":
		g.handleReceivePack(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *GitFake) endpoint() *transport.Endpoint {
	// MapLoader keys the storer under the storer's path; we registered
	// "/<repo>.git" above, so build an Endpoint with matching Path.
	return &transport.Endpoint{
		Protocol: "http",
		Path:     "/" + g.RepoName + ".git",
	}
}

func (g *GitFake) handleInfoRefs(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service != "git-upload-pack" && service != "git-receive-pack" {
		http.Error(w, "unsupported service", http.StatusBadRequest)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		http.Error(w, "gitfake closed", http.StatusServiceUnavailable)
		return
	}

	var (
		sess transport.Session
		err  error
	)
	switch service {
	case "git-upload-pack":
		sess, err = g.upx.NewUploadPackSession(g.endpoint(), nil)
	case "git-receive-pack":
		sess, err = g.rcv.NewReceivePackSession(g.endpoint(), nil)
	}
	if err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer sess.Close()

	ar, err := sess.AdvertisedReferencesContext(r.Context())
	if err != nil {
		http.Error(w, "advertise: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Smart-HTTP requires a "# service=<svc>\n" pkt-line + flush before
	// the standard advertisement payload. Encode it manually.
	enc := pktline.NewEncoder(w)
	if err := enc.Encodef("# service=%s\n", service); err != nil {
		return
	}
	if err := enc.Flush(); err != nil {
		return
	}
	if err := ar.Encode(w); err != nil {
		return
	}
}

func (g *GitFake) handleUploadPack(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		http.Error(w, "gitfake closed", http.StatusServiceUnavailable)
		return
	}

	sess, err := g.upx.NewUploadPackSession(g.endpoint(), nil)
	if err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer sess.Close()

	req := packp.NewUploadPackRequest()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := req.Decode(bytes.NewReader(body)); err != nil {
		http.Error(w, "decode upload-pack request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Capabilities == nil {
		req.Capabilities = capability.NewList()
	}

	resp, err := sess.UploadPack(r.Context(), req)
	if err != nil {
		http.Error(w, "upload-pack: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Close()

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if err := resp.Encode(w); err != nil {
		// Connection broken mid-stream — nothing we can do at this
		// point; the response status is already on the wire.
		return
	}
}

func (g *GitFake) handleReceivePack(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		http.Error(w, "gitfake closed", http.StatusServiceUnavailable)
		return
	}

	sess, err := g.rcv.NewReceivePackSession(g.endpoint(), nil)
	if err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer sess.Close()

	req := packp.NewReferenceUpdateRequest()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := req.Decode(bytes.NewReader(body)); err != nil {
		http.Error(w, "decode receive-pack request: "+err.Error(), http.StatusBadRequest)
		return
	}

	report, err := sess.ReceivePack(r.Context(), req)
	if err != nil && report == nil {
		http.Error(w, "receive-pack: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if report != nil {
		if encErr := report.Encode(w); encErr != nil {
			return
		}
	}
}
