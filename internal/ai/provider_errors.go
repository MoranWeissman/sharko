package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// provider_errors.go: what Sharko says when an AI provider answers with an
// error.
//
// The old code put the provider's whole answer into the error, word for word:
// `openai returned 401: {…raw JSON…}`. That error is shown on the AI settings
// page, in the API answer and in the assistant chat. Three things were wrong
// with it:
//
//   - The answer of a wrong OpenAI key quotes the start and the end of the key
//     the admin typed. The owner asked that a key is never played back.
//   - The address of an Ollama or custom provider is typed by a person. With
//     the whole answer in the error, the test call was a way to read whatever
//     any address Sharko can reach sends back (a web page, another service).
//   - Raw JSON or a whole HTML page on the screen helps nobody.
//
// Now the error is Sharko's own short sentence, picked by the status code. The
// provider's own error sentence is added only when the answer is a JSON error
// in the shape every supported provider uses ({"error": "..."} or
// {"error": {"message": "..."}}), with anything that looks like a key taken
// out, and cut short. Anything else in the answer is dropped.

// maxProviderSaidLen is how much of the provider's own error sentence is
// passed on.
const maxProviderSaidLen = 200

// hiddenKeyMark replaces anything in a provider's sentence that looks like a
// key.
const hiddenKeyMark = "[hidden]"

// httpError builds the error for a provider answer with a status other than
// 200. provider is Sharko's own name for it ("openai", "ollama", ...).
func (c *Client) httpError(provider string, status int, body []byte) error {
	msg := fmt.Sprintf("%s returned %d: %s", provider, status, statusMeaning(provider, status))
	if said := providerSaid(body, c.config.APIKey); said != "" {
		msg += fmt.Sprintf(" The provider said: %q", said)
	}
	return fmt.Errorf("%s", msg)
}

// statusMeaning is Sharko's plain sentence for a status code.
func statusMeaning(provider string, status int) string {
	selfHosted := provider == string(ProviderOllama)
	switch {
	case status == 401 || status == 403:
		if selfHosted {
			return "the server refused the request."
		}
		return "the provider refused the API key."
	case status == 404:
		if selfHosted {
			return "this address does not answer like an Ollama server, or the model is not there."
		}
		return "the provider did not find the model or the address."
	case status == 429:
		return "the provider's request limit or spending limit was reached."
	case status >= 500:
		return "the provider had a problem of its own."
	default:
		return "the provider did not accept the request."
	}
}

// providerSaid pulls the provider's own error sentence out of an error answer.
// It returns "" unless the answer is a JSON object with an "error" that is a
// string, or an object with a string "message".
func providerSaid(body []byte, apiKey string) string {
	var outer struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &outer); err != nil || len(outer.Error) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(outer.Error, &text); err != nil {
		var inner struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(outer.Error, &inner); err != nil {
			return ""
		}
		text = inner.Message
	}
	text = hideKeyLookalikes(strings.TrimSpace(text), apiKey)
	if len(text) > maxProviderSaidLen {
		text = strings.TrimSpace(text[:maxProviderSaidLen]) + "..."
	}
	return text
}

// hideKeyLookalikes replaces every word of text that looks like the key, or
// like a masked copy of one, with hiddenKeyMark. A word is hidden when it
// holds the key itself, holds a "*" (how providers mask a key), or starts or
// ends the way the key does.
func hideKeyLookalikes(text, apiKey string) string {
	if text == "" {
		return ""
	}
	head, tail := "", ""
	if len(apiKey) >= 8 {
		head, tail = apiKey[:4], apiKey[len(apiKey)-4:]
	}
	words := strings.Fields(text)
	for i, w := range words {
		bare := strings.Trim(w, ".,;:'\"()[]{}")
		switch {
		case strings.Contains(w, "*"):
			words[i] = hiddenKeyMark
		case apiKey != "" && strings.Contains(w, apiKey):
			words[i] = hiddenKeyMark
		case head != "" && len(bare) >= 8 && (strings.HasPrefix(bare, head) || strings.HasSuffix(bare, tail)):
			words[i] = hiddenKeyMark
		}
	}
	return strings.Join(words, " ")
}
