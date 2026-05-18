package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestServeHTTP_InfoRefsSmoke wires the same NewGitFakeForServer + ServeHTTP
// path that main() uses into a real net.Listener on an ephemeral port,
// then hits /info/refs?service=git-upload-pack and asserts the smart-HTTP
// content-type. Catches regressions where the gitfake core split breaks
// the binary's HTTP surface.
//
// Doesn't shell out to `git` — keeps the smoke test hermetic and fast
// (no PATH dependency, no subprocess).
func TestServeHTTP_InfoRefsSmoke(t *testing.T) {
	gf, err := harness.NewGitFakeForServer("sharko-e2e", "main")
	if err != nil {
		t.Fatalf("NewGitFakeForServer: %v", err)
	}
	t.Cleanup(gf.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	srv := &http.Server{
		Handler:           http.HandlerFunc(gf.ServeHTTP),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln) // returns on srv.Shutdown
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/sharko-e2e.git/info/refs?service=git-upload-pack"

	// Brief retry — server goroutine may not have hit Accept yet.
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}

	const wantCTPrefix = "application/x-git-upload-pack-advertisement"
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, wantCTPrefix) {
		t.Errorf("Content-Type = %q, want prefix %q", ct, wantCTPrefix)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// First pkt-line MUST be "# service=git-upload-pack\n" framed by the
	// smart-HTTP pkt-line encoder. The framing byte prefix varies in
	// length but the readable token "service=git-upload-pack" is stable.
	if !strings.Contains(string(body), "service=git-upload-pack") {
		t.Errorf("advertisement body missing service token; got %q", string(body))
	}
}
