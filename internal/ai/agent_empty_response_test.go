package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// fakeOllamaResponse builds the JSON body that a fake Ollama /api/chat endpoint
// returns.  content is the assistant message text; toolCalls is included only
// when non-nil.
func fakeOllamaResponse(content string, toolCalls []ToolCall) []byte {
	msg := ChatMessage{Role: "assistant", Content: content, ToolCalls: toolCalls}
	resp := ollamaChatResponse{Message: msg, Done: true}
	b, _ := json.Marshal(resp)
	return b
}

// newMinimalAgent builds an Agent wired to the given Ollama base URL.
// The ToolExecutor is minimal: it has no git provider, no ArgoCD client, and
// no managed-clusters path — the tests below never trigger tool calls so that
// is fine.
func newMinimalAgent(ollamaURL string) *Agent {
	client := NewClient(Config{
		Provider:  ProviderOllama,
		OllamaURL: ollamaURL,
	})
	exec := &ToolExecutor{
		parser: config.NewParser(),
		gp: func() gitprovider.GitProvider { return failingProvider{} }(),
	}
	// Build agent without initContext (no git provider wired, so context
	// loading would fail).  We set up the messages slice directly.
	a := &Agent{client: client, executor: exec}
	a.messages = []ChatMessage{{Role: "system", Content: "test"}}
	return a
}

// TestChat_EmptyResponseNoToolCalls asserts that when the LLM returns an empty
// content string and zero tool calls, Chat returns a non-empty fallback
// string.  (V2-cleanup-42 Defect 2 fix.)
func TestChat_EmptyResponseNoToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fakeOllamaResponse("", nil))
	}))
	defer srv.Close()

	a := newMinimalAgent(srv.URL)
	resp, err := a.Chat(context.Background(), "hello", authz.RoleViewer)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if strings.TrimSpace(resp) == "" {
		t.Error("Chat returned empty string; expected a non-empty fallback")
	}
}

// TestChat_EmptyResponseAfterToolCalls asserts that when a prior iteration used
// tool calls (so there are tool-role messages in history) but the final
// iteration returns empty content, Chat returns the "gathered data" variant of
// the fallback rather than a blank string.
func TestChat_EmptyResponseAfterToolCalls(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			// First LLM call: return a tool call so the loop processes it
			tc := ToolCall{
				ID:   "tc1",
				Type: "function",
				Function: ToolCallFunc{
					Name:      "list_clusters",
					Arguments: json.RawMessage(`{}`),
				},
			}
			_, _ = w.Write(fakeOllamaResponse("", []ToolCall{tc}))
			return
		}
		// Second LLM call: return empty content with no tool calls
		_, _ = w.Write(fakeOllamaResponse("", nil))
	}))
	defer srv.Close()

	a := newMinimalAgent(srv.URL)
	resp, err := a.Chat(context.Background(), "list my clusters", authz.RoleViewer)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if strings.TrimSpace(resp) == "" {
		t.Error("Chat returned empty string after tool calls; expected a non-empty fallback")
	}
	// The "gathered data" path should mention data/environment
	if !strings.Contains(resp, "gathered") && !strings.Contains(resp, "data") {
		t.Errorf("fallback for post-tool-call empty response should mention gathered data; got: %q", resp)
	}
}

// TestChat_NonEmptyResponseUnchanged asserts the normal (non-empty content)
// path is untouched by the fallback change — the LLM's answer is returned
// verbatim.
func TestChat_NonEmptyResponseUnchanged(t *testing.T) {
	const want = "Here are your clusters: prod-eu, staging-eu."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fakeOllamaResponse(want, nil))
	}))
	defer srv.Close()

	a := newMinimalAgent(srv.URL)
	resp, err := a.Chat(context.Background(), "list my clusters", authz.RoleViewer)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp != want {
		t.Errorf("expected %q, got %q", want, resp)
	}
}

// TestChat_MaxIterationsFallbackUnchanged asserts the max-iterations fallback
// is still returned when the LLM keeps emitting tool calls until the limit is
// hit.
func TestChat_MaxIterationsFallbackUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tc := ToolCall{
			ID:   "tc1",
			Type: "function",
			Function: ToolCallFunc{
				Name:      "list_clusters",
				Arguments: json.RawMessage(`{}`),
			},
		}
		_, _ = w.Write(fakeOllamaResponse("", []ToolCall{tc}))
	}))
	defer srv.Close()

	a := newMinimalAgent(srv.URL)
	// Override MaxIterations to 2 so the test is fast.
	a.client.config.MaxIterations = 2

	resp, err := a.Chat(context.Background(), "spin forever", authz.RoleViewer)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	const wantPrefix = "I've used all my available tool calls"
	if !strings.HasPrefix(resp, wantPrefix) {
		t.Errorf("expected max-iterations fallback starting with %q; got %q", wantPrefix, resp)
	}
}
