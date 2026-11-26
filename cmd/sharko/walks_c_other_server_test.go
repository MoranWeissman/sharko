package main

// Pre-release fix round 2026-11-26, story C (walk 8 S1): the saved login was
// sent to whatever --server said. A plain listener on another port received
// "Authorization: Bearer <admin token>".

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSavedLoginIsNeverSentToAnotherServer(t *testing.T) {
	var homeHits, otherHits atomic.Int32
	var otherSawAuth atomic.Value
	otherSawAuth.Store("")

	// The server the login was made for.
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		homeHits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})

	// Some other machine.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		otherSawAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(other.Close)

	serverFlag = other.URL
	_, _, err := apiGet("/api/v1/clusters")
	if err == nil {
		t.Fatal("a command pointed at another server must stop, it went through")
	}
	want := "you are not logged in to " + other.URL + ". The saved login belongs to another Sharko server and is never sent anywhere else. Run 'sharko login --server " + other.URL + "' first."
	if err.Error() != want {
		t.Errorf("message:\n got  %s\n want %s", err.Error(), want)
	}
	if otherHits.Load() != 0 {
		t.Errorf("the other server was called %d time(s); it must not be called at all", otherHits.Load())
	}
	if got := otherSawAuth.Load().(string); got != "" {
		t.Errorf("the other server received the login: %q", got)
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Error("the message carries the token")
	}
	if homeHits.Load() != 0 {
		t.Error("the saved server must not be called either when --server names another one")
	}
}

func TestSavedLoginIsSentToItsOwnServer(t *testing.T) {
	var sawAuth atomic.Value
	sawAuth.Store("")
	srv := startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	})

	// No --server: the saved server.
	if _, _, err := apiGet("/api/v1/clusters"); err != nil {
		t.Fatalf("saved server: %v", err)
	}
	if sawAuth.Load().(string) != "Bearer test-token" {
		t.Fatalf("the saved server did not get the login: %q", sawAuth.Load())
	}

	// --server naming the same server, written a little differently.
	sawAuth.Store("")
	serverFlag = strings.Replace(srv.URL, "http://", "HTTP://", 1) + "/"
	if _, _, err := apiGet("/api/v1/clusters"); err != nil {
		t.Fatalf("same server through --server: %v", err)
	}
	if sawAuth.Load().(string) != "Bearer test-token" {
		t.Errorf("the same server, named with --server, did not get the login: %q", sawAuth.Load())
	}
}

func TestSameServer(t *testing.T) {
	same := [][2]string{
		{"https://sharko.example.com", "https://sharko.example.com/"},
		{"https://Sharko.Example.com", "https://sharko.example.com"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://example.com/sharko/", "https://example.com/sharko"},
	}
	for _, p := range same {
		if !sameServer(p[0], p[1]) {
			t.Errorf("%q and %q are the same server", p[0], p[1])
		}
	}
	different := [][2]string{
		{"https://sharko.example.com", "http://sharko.example.com"},
		{"https://sharko.example.com", "https://sharko.example.com:8443"},
		{"https://sharko.example.com", "https://sharko.example.com.evil.test"},
		{"http://localhost:8080", "http://localhost:19556"},
		{"https://example.com/sharko", "https://example.com/other"},
		{"https://sharko.example.com", ""},
	}
	for _, p := range different {
		if sameServer(p[0], p[1]) {
			t.Errorf("%q and %q are NOT the same server", p[0], p[1])
		}
	}
}

// `sharko token create` with no --role used to make an ADMIN key. The API and
// the UI default to viewer.
func TestTokenCreate_DefaultRoleIsViewer(t *testing.T) {
	f := tokenCreateCmd.Flags().Lookup("role")
	if f == nil {
		t.Fatal("no --role flag")
	}
	if f.DefValue != "viewer" {
		t.Errorf("--role default = %q, want viewer", f.DefValue)
	}
}
