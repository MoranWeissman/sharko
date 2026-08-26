package ai

// agent_tool_error_leak_test.go — the AI assistant's tool-error boundary (B14).
//
// # What went to the model provider
//
// The tool-calling loop turned a failed tool into
// `fmt.Sprintf("Error executing %s: %v", name, err)`, appended it to the
// conversation, and posted the whole conversation to whichever provider the
// operator configured. The tools dial chart repositories and Git hosts, and
// Go's *url.Error keeps a token written in the USERNAME position of the
// address. So the operator's chart repository access token was posted to a
// third party by an ordinary "which repo failed?" error message.
//
// # How this is proved
//
// Not by unit-testing the helper. A fake provider records the EXACT bytes the
// agent posts to it, and the tool is made to fail the way production fails: a
// real helm fetch against a real closed port, with a real tokenised address
// read out of a real addons-catalog.yaml.
//
// Then the recorded request body is swept. And the fixed sentence must be
// PRESENT in it, because an absence is also what a run that never called the
// tool produces.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/helm"
)

// aiLeakSentinel stands in for the chart repository's access token.
const aiLeakSentinel = "W8HD-ai-tool-error-token-sentinel-2p5r7t-never-leaves-the-server-c4a6"

// aiClosedHostPort returns an address nothing is listening on, so the fetch
// fails the way it fails in production rather than in a way invented here.
func aiClosedHostPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// aiCatalogProvider serves one real addons-catalog.yaml whose repo address
// carries the token in the username position.
type aiCatalogProvider struct {
	gitprovider.GitProvider
	yaml []byte
}

func (p aiCatalogProvider) GetFileContent(_ context.Context, _, _ string) ([]byte, error) {
	return p.yaml, nil
}

// recordingProvider is a fake model provider that keeps every request body the
// agent posts to it. That body IS what leaves the machine.
type recordingProvider struct {
	mu     sync.Mutex
	bodies []string
	calls  int
}

func (r *recordingProvider) record(b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, b)
}

func (r *recordingProvider) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.bodies, "\n")
}

// TestAgent_ToolError_NeverReachesTheModelProvider is the end-to-end proof.
func TestAgent_ToolError_NeverReachesTheModelProvider(t *testing.T) {
	dead := aiClosedHostPort(t)
	repoURL := "https://" + aiLeakSentinel + "@" + dead

	rec := &recordingProvider{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record(string(body))
		rec.mu.Lock()
		rec.calls++
		n := rec.calls
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// First turn: ask for the tool that reaches out to the chart repo.
			args, _ := json.Marshal(map[string]string{"addon_name": "leaky"})
			_, _ = w.Write(fakeOllamaResponse("", []ToolCall{{
				ID:       "tc1",
				Type:     "function",
				Function: ToolCallFunc{Name: "list_chart_versions", Arguments: args},
			}}))
			return
		}
		_, _ = w.Write(fakeOllamaResponse("done", nil))
	}))
	defer srv.Close()

	catalogYAML := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: leaky
      repoURL: ` + repoURL + `
      chart: leaky
      version: "1.0.0"
      namespace: leaky
`)

	// Two preconditions, both needed before any absence below means
	// anything.
	//
	// First: the tool the model asks for must really fail, so the boundary
	// under test is really reached. Since BF9 it fails by refusing the
	// address rather than by dialling it, which is the point of BF9.
	fetcher := helm.NewFetcher()
	if _, err := fetcher.ListVersions(context.Background(), repoURL, "leaky"); err == nil {
		t.Fatal("the chart fetch was expected to fail")
	}

	// Second: an error that DOES carry the token has to exist, and the
	// boundary has to strip it. Sharko's chart fetcher no longer produces
	// one, but *url.Error is not only made there — any HTTP client handed
	// such an address makes the same value — so the boundary still has to be
	// safe against it. Dialled directly, so the token is genuinely in the
	// error's text.
	resp, dialErr := (&http.Client{}).Get("https://" + aiLeakSentinel + "@" + dead + "/index.yaml")
	if dialErr == nil {
		resp.Body.Close()
		t.Fatal("the request to a closed port was expected to fail")
	}
	if !strings.Contains(dialErr.Error(), aiLeakSentinel) {
		t.Fatalf("the control error does NOT carry the token, so the strip below proves nothing:\n%v", dialErr)
	}
	if safe := credsafe.SafeToolFailure(dialErr); strings.Contains(safe, aiLeakSentinel) || strings.Contains(safe, dead) {
		t.Errorf("the tool-result boundary hands the model provider the address it was given: %q", safe)
	}

	client := NewClient(Config{Provider: ProviderOllama, OllamaURL: srv.URL})
	exec := &ToolExecutor{
		parser:  config.NewParser(),
		fetcher: helm.NewFetcher(),
		gp:      aiCatalogProvider{yaml: catalogYAML},
	}
	a := &Agent{client: client, executor: exec}
	a.messages = []ChatMessage{{Role: "system", Content: "test"}}

	if _, err := a.Chat(context.Background(), "what versions are there?", authz.RoleViewer); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	// The tool must really have been called, or the loop short-circuited and
	// the boundary under test was never reached.
	if rec.calls < 2 {
		t.Fatalf("the provider was called %d times, want at least 2 — the tool result was never posted, so nothing below was swept", rec.calls)
	}

	posted := rec.all()
	if strings.Contains(posted, aiLeakSentinel) {
		t.Errorf("the conversation posted to the model provider carries the chart repository's access token:\n\n%s", posted)
	}
	// The host and port would identify the operator's private repository too;
	// LogClass reports Go type names only, so neither should appear.
	if strings.Contains(posted, dead) {
		t.Errorf("the conversation posted to the model provider carries the repository address %q:\n\n%s", dead, posted)
	}

	// And the model must still be TOLD the step failed. A boundary that
	// posted nothing would pass every sweep and leave the model inventing a
	// reason.
	//
	// Typed out here as a literal, not read from the constant the code
	// assigned — a test that quotes the code passes whatever the code says.
	const wantSentence = "That step did not complete. Sharko is not passing on the underlying message, because a failure from a chart repository, a Git host or a credentials backend can quote an address that carries an access token."
	if !strings.Contains(posted, wantSentence) {
		t.Errorf("the model was never told the step failed.\nwant the conversation to contain:\n%q\n\ngot:\n%s", wantSentence, posted)
	}
	if wantSentence != credsafe.ToolFailureMessage {
		t.Errorf("the sentence in the code has changed and this test's literal has not been updated with it:\ncode: %q\ntest: %q", credsafe.ToolFailureMessage, wantSentence)
	}
	// And roughly WHY, so the model can say "the repository did not answer".
	if !strings.Contains(posted, "chain=") {
		t.Errorf("the model was told nothing about the KIND of failure — LogClass's type chain is missing:\n%s", posted)
	}
}

// TestSafeToolFailure_NeverQuotesTheErrorText is the unit-level companion: the
// helper is handed an error whose words ARE the token and must not repeat any
// of them.
func TestSafeToolFailure_NeverQuotesTheErrorText(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: aiLeakSentinel, Addr: aiLeakSentinel}}
	got := credsafe.SafeToolFailure(err)
	if strings.Contains(got, aiLeakSentinel) {
		t.Errorf("SafeToolFailure quoted the error's words: %q", got)
	}
	if !strings.Contains(got, "chain=") {
		t.Errorf("SafeToolFailure said nothing about the KIND of failure: %q", got)
	}
	if credsafe.SafeToolFailure(nil) != "" {
		t.Errorf("SafeToolFailure(nil) = %q, want the empty string", credsafe.SafeToolFailure(nil))
	}
}
