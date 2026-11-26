package ai

// Pre-release fix round 2026-11-26, story C: what an AI provider (or any
// address typed as one) answers is never passed on word for word, a key is
// never played back, and some addresses are never called.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPError_NeverPlaysTheKeyBack(t *testing.T) {
	const key = "sk-TESTONLYabcdefghijklmnopqrstuvwxyzWXYZ"
	c := NewClient(Config{Provider: ProviderOpenAI, APIKey: key})
	// The shape OpenAI really answers with: the key, masked, start and end left in.
	body := []byte(`{"error":{"message":"Incorrect API key provided: sk-TESTO*********************WXYZ. You can find your API key at https://platform.openai.com/account/api-keys.","type":"invalid_request_error"}}`)
	got := c.httpError("openai", 401, body).Error()

	const want = `openai returned 401: the provider refused the API key. The provider said: "Incorrect API key provided: [hidden] You can find your API key at https://platform.openai.com/account/api-keys."`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, leak := range []string{"sk-TESTO", "WXYZ", "*"} {
		if strings.Contains(got, leak) {
			t.Errorf("the error still shows %q of the key: %s", leak, got)
		}
	}
}

func TestHTTPError_HidesAKeyEchoedInOtherShapes(t *testing.T) {
	const key = "AIzaTESTONLY0123456789abcdefghijkLMNO"
	c := NewClient(Config{Provider: ProviderGemini, APIKey: key})
	for _, said := range []string{
		"API key " + key + " is not valid",
		"key AIza...LMNO rejected",
		"key AIzaTESTONLY... rejected",
	} {
		body := []byte(fmt.Sprintf(`{"error":{"message":%q}}`, said))
		got := c.httpError("gemini", 403, body).Error()
		if strings.Contains(got, "AIza") || strings.Contains(got, "LMNO") {
			t.Errorf("a piece of the key is shown: %s", got)
		}
		if !strings.Contains(got, hiddenKeyMark) {
			t.Errorf("nothing was hidden in: %s", got)
		}
	}
}

func TestHTTPError_DropsAnythingThatIsNotAProviderErrorSentence(t *testing.T) {
	c := NewClient(Config{Provider: ProviderCustomOpenAI})
	cases := map[string][]byte{
		"a web page":              []byte("<html><body>secret internal dashboard</body></html>"),
		"plain text":              []byte("Client sent an HTTP request to an HTTPS server."),
		"another JSON shape":      []byte(`{"kind":"Status","message":"forbidden: User system:anonymous cannot get path /","code":403}`),
		"an error that is a list": []byte(`{"error":["a","b"]}`),
		"nothing":                 nil,
	}
	for name, body := range cases {
		got := c.httpError("custom-openai", 404, body).Error()
		const want = "custom-openai returned 404: the provider did not find the model or the address."
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestHTTPError_KeepsAShortProviderSentenceAndCutsALongOne(t *testing.T) {
	c := NewClient(Config{Provider: ProviderOllama})
	got := c.httpError("ollama", 404, []byte(`{"error":"model 'llama3.2' not found, try pulling it first"}`)).Error()
	const want = `ollama returned 404: this address does not answer like an Ollama server, or the model is not there. The provider said: "model 'llama3.2' not found, try pulling it first"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	long := strings.Repeat("word ", 400)
	got = c.httpError("ollama", 500, []byte(fmt.Sprintf(`{"error":%q}`, long))).Error()
	if len(got) > 400 {
		t.Errorf("a long provider sentence was not cut: %d characters", len(got))
	}
}

func TestStatusMeaning_Sentences(t *testing.T) {
	cases := []struct {
		provider string
		status   int
		want     string
	}{
		{"openai", 401, "the provider refused the API key."},
		{"claude", 403, "the provider refused the API key."},
		{"ollama", 401, "the server refused the request."},
		{"gemini", 404, "the provider did not find the model or the address."},
		{"openai", 429, "the provider's request limit or spending limit was reached."},
		{"openai", 503, "the provider had a problem of its own."},
		{"openai", 400, "the provider did not accept the request."},
	}
	for _, tc := range cases {
		if got := statusMeaning(tc.provider, tc.status); got != tc.want {
			t.Errorf("%s %d: got %q, want %q", tc.provider, tc.status, got, tc.want)
		}
	}
}

func TestValidateProviderAddress(t *testing.T) {
	ok := []string{
		"http://localhost:11434",
		"http://127.0.0.1:11434",
		"http://ollama.ai.svc.cluster.local:11434",
		"http://10.96.4.12:11434",
		"https://gateway.example.com/api",
	}
	for _, a := range ok {
		if err := ValidateProviderAddress(a); err != nil {
			t.Errorf("%s must be allowed (a self-hosted provider lives on such an address): %v", a, err)
		}
	}
	bad := map[string]string{
		"http://169.254.169.254":             AddressNotAllowedSentence,
		"http://169.254.169.254/latest/meta": AddressNotAllowedSentence,
		"http://[fe80::1]:11434":             AddressNotAllowedSentence,
		"http://0.0.0.0:11434":               AddressNotAllowedSentence,
		"http://224.0.0.1":                   AddressNotAllowedSentence,
		"https://user:pass@gateway.example":  AddressHasLoginSentence,
		"https://token@gateway.example":      AddressHasLoginSentence,
		"ftp://gateway.example":              AddressNeedsHTTPSentence,
		"gateway.example:8080":               AddressNeedsHTTPSentence,
		"http://":                            AddressCouldNotBeReadSentence,
	}
	for a, want := range bad {
		err := ValidateProviderAddress(a)
		if err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", a, err, want)
		}
	}
}

func TestAddressSentences_ExactText(t *testing.T) {
	pins := map[string]string{
		AddressNeedsHTTPSentence:      "The address must start with http:// or https://.",
		AddressHasLoginSentence:       "Take the user name and password out of the address. If the provider needs a key, put it in the API key field.",
		AddressNotAllowedSentence:     "Sharko does not call this address. Addresses that start with 169.254 (and fe80:) belong to the machine's own network setup, and no AI provider lives there.",
		AddressCouldNotBeReadSentence: "Sharko could not read this address. Write it like http://ollama.example.com:11434.",
	}
	for got, want := range pins {
		if got != want {
			t.Errorf("sentence changed:\n got  %q\n want %q", got, want)
		}
	}
}

// The check also runs at the moment of connecting, on the real address, so a
// name that resolves to a never-called address is stopped too.
func TestCheckDialAddress(t *testing.T) {
	for _, a := range []string{"169.254.169.254:80", "[fe80::1]:11434", "0.0.0.0:80", "[::]:80", "224.0.0.1:80", "not-an-address", "[fe80::1%en0]:80"} {
		if err := checkDialAddress(a); !errors.Is(err, errBlockedAddress) {
			t.Errorf("%s must be refused at connect time, got %v", a, err)
		}
	}
	for _, a := range []string{"127.0.0.1:11434", "10.96.4.12:11434", "192.168.1.10:8080", "[::1]:11434", "93.184.216.34:443"} {
		if err := checkDialAddress(a); err != nil {
			t.Errorf("%s must be allowed at connect time, got %v", a, err)
		}
	}
}

// A redirect to an address Sharko never calls is stopped by that same check,
// whatever the typed address was. The error is the check's own error, which
// proves the connection was refused by Sharko and not by the network.
func TestGuardedTransport_StopsARedirectToANeverCalledAddress(t *testing.T) {
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusTemporaryRedirect)
	}))
	defer hop.Close()

	c := NewClient(Config{Provider: ProviderOllama, OllamaURL: hop.URL, OllamaModel: "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hop.URL, nil)
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the call followed a redirect to 169.254.169.254")
	}
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("the redirect was not stopped by Sharko's own check: %v", err)
	}

	// And through the real call: Sharko's own sentence, with no address in it.
	_, err = c.Summarize(ctx, "hi")
	if err == nil || strings.Contains(err.Error(), "169.254") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "the ollama request did not complete") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNeverCalled(t *testing.T) {
	if !neverCalled(nil) {
		t.Error("an address that cannot be read must be refused")
	}
}
