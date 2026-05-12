//go:build e2e

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
	"testing"
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

// GitFake is an in-memory git server for e2e tests. It hosts ONE bare-style
// repository (with an in-memory worktree for ergonomic CommitFile) reachable
// over HTTP at Addr. Tests use the Commit/File helpers directly against the
// in-memory storage; Sharko (or any other client) can clone/fetch/push via
// the HTTP smart protocol at RepoURL.
//
// Auto-cleanup is wired via t.Cleanup at construction; callers do not need
// to call Close() explicitly.
type GitFake struct {
	// Addr is the base URL of the HTTP smart-protocol server (e.g.
	// "http://127.0.0.1:34567"). Useful when constructing alternative
	// repo paths in tests, though today we host exactly one repo.
	Addr string
	// RepoName is the path-segment name of the hosted repo (without the
	// trailing ".git"). Defaults to "sharko-e2e".
	RepoName string
	// RepoURL is the full clone URL: Addr + "/" + RepoName + ".git".
	// This is the value to feed into sharko's git.repo_url config.
	RepoURL string

	storage *memory.Storage      // in-memory git storage
	repo    *git.Repository      // helper handle backed by storage
	server  *httptest.Server     // HTTP wrapper
	mu      sync.Mutex           // serialises HTTP smart-protocol requests
	loader  server.MapLoader     // single-repo loader for transport server
	upx     transport.Transport  // upload-pack server (clone/fetch)
	rcv     transport.Transport  // receive-pack server (push)
	closed  bool
}

// StartGitFake spins up an in-memory git server with one bare-style repo.
// The repo is initialized with a single empty "init" commit on `main` so
// that sharko's "is this repo initialized" probe finds a HEAD.
//
// Auto-cleanup is registered via t.Cleanup; the server is stopped and
// in-memory storage discarded automatically at test end.
//
// Calls t.Fatalf on any setup failure — does not return on error.
func StartGitFake(t *testing.T) *GitFake {
	t.Helper()

	storage := memory.NewStorage()
	wt := memfs.New()
	repo, err := git.InitWithOptions(storage, wt, git.InitOptions{
		DefaultBranch: plumbing.Main, // refs/heads/main
	})
	if err != nil {
		t.Fatalf("StartGitFake: init repo: %v", err)
	}

	// Seed an empty initial commit on main so HEAD resolves and clones
	// don't hit the "empty repository" rejection some clients trip on.
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("StartGitFake: worktree: %v", err)
	}
	emptyHash, err := worktree.Commit("init: empty seed commit", &git.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  "sharko-e2e",
			Email: "e2e@sharko.invalid",
			When:  time.Unix(0, 0).UTC(),
		},
	})
	if err != nil {
		t.Fatalf("StartGitFake: seed commit: %v", err)
	}
	_ = emptyHash // explicitly discard — nothing to assert here

	g := &GitFake{
		RepoName: defaultRepoName,
		storage:  storage,
		repo:     repo,
		loader: server.MapLoader{
			// The transport server keys storers by transport.Endpoint.String().
			// Both "http://host/sharko-e2e.git" and the parsed equivalent
			// hit the loader; we register both to be safe.
		},
	}
	g.upx = server.NewServer(g.loader)
	g.rcv = server.NewServer(g.loader)

	// Stand up the HTTP smart-protocol server on a random localhost port.
	g.server = httptest.NewServer(http.HandlerFunc(g.serveHTTP))

	// httptest assigns Addr; populate the URL fields and finalise the loader.
	g.Addr = g.server.URL
	g.RepoURL = g.Addr + "/" + g.RepoName + ".git"

	// Register the storer under both the bare path and the absolute URL so
	// that whichever endpoint key the transport derives from a request URL
	// we can find the storer.
	g.loader["/"+g.RepoName+".git"] = storage
	g.loader[g.RepoName+".git"] = storage
	g.loader[g.RepoURL] = storage

	// Verify the listener actually bound (sanity guard against host firewalls).
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(g.Addr, "http://"), 2*time.Second); err != nil {
		g.server.Close()
		t.Fatalf("StartGitFake: server not reachable at %s: %v", g.Addr, err)
	}

	t.Cleanup(func() { g.Close() })
	t.Logf("harness: gitfake serving %s (in-memory bare repo, single seed commit on main)", g.RepoURL)
	return g
}

// Close stops the HTTP server and releases in-memory storage. Safe to call
// multiple times. t.Cleanup invokes this automatically.
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

// CommitFile writes content to the in-memory worktree at path, stages it,
// and commits with the given message on the `main` branch. Returns the new
// commit's SHA. Useful for seeding fixtures or asserting downstream
// behaviour against a known starting state.
//
// Calls t.Fatalf on failure.
func (g *GitFake) CommitFile(t *testing.T, path, content, message string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.CommitFile: gitfake is closed")
	}
	wt, err := g.repo.Worktree()
	if err != nil {
		t.Fatalf("GitFake.CommitFile: worktree: %v", err)
	}
	f, err := wt.Filesystem.Create(path)
	if err != nil {
		t.Fatalf("GitFake.CommitFile: create %q: %v", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		t.Fatalf("GitFake.CommitFile: write %q: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("GitFake.CommitFile: close %q: %v", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		t.Fatalf("GitFake.CommitFile: add %q: %v", path, err)
	}
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "sharko-e2e",
			Email: "e2e@sharko.invalid",
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("GitFake.CommitFile: commit: %v", err)
	}
	return hash.String()
}

// LatestCommit returns the SHA at HEAD on the given branch (default "main"
// when branch is empty).
//
// Calls t.Fatalf if the branch is missing or unreadable.
func (g *GitFake) LatestCommit(t *testing.T, branch string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.LatestCommit: gitfake is closed")
	}
	if branch == "" {
		branch = "main"
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("GitFake.LatestCommit: resolve branch %q: %v", branch, err)
	}
	return ref.Hash().String()
}

// FileAt returns the content of path at branch's HEAD. Used by tests to
// assert that sharko committed the expected YAML.
//
// Calls t.Fatalf if the branch, commit, or file is missing.
func (g *GitFake) FileAt(t *testing.T, branch, path string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.FileAt: gitfake is closed")
	}
	if branch == "" {
		branch = "main"
	}
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("GitFake.FileAt: resolve branch %q: %v", branch, err)
	}
	commit, err := g.repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("GitFake.FileAt: commit %s: %v", ref.Hash(), err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("GitFake.FileAt: tree: %v", err)
	}
	file, err := tree.File(path)
	if err != nil {
		t.Fatalf("GitFake.FileAt: file %q at %s on %s: %v", path, ref.Hash(), branch, err)
	}
	body, err := file.Contents()
	if err != nil {
		t.Fatalf("GitFake.FileAt: read %q: %v", path, err)
	}
	return body
}

// ListBranches returns all branch names in the repository, sorted.
// Tests use this to assert that sharko opened (or closed) a feature branch
// — our PR fake represents PRs as branches with a known prefix.
//
// Calls t.Fatalf on iteration failure (in practice, only on closed gitfake).
func (g *GitFake) ListBranches(t *testing.T) []string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.repo == nil {
		t.Fatalf("GitFake.ListBranches: gitfake is closed")
	}
	iter, err := g.repo.Branches()
	if err != nil {
		t.Fatalf("GitFake.ListBranches: branches iter: %v", err)
	}
	var out []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, ref.Name().Short())
		return nil
	}); err != nil {
		t.Fatalf("GitFake.ListBranches: foreach: %v", err)
	}
	return out
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
// `git` CLI client AND from go-git client code. Sharko itself (today) goes
// through the GitHub REST API and does not exercise this path; the HTTP
// surface exists to give downstream stories a real RepoURL to feed sharko
// in any future story that swaps the GitHub-API mock for a git transport.

func (g *GitFake) serveHTTP(w http.ResponseWriter, r *http.Request) {
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

