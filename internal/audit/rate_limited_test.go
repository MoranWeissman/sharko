package audit

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/go-github/v68/github"
)

func TestClassify_RequestLimitHasItsOwnReason(t *testing.T) {
	err := fmt.Errorf("get file content: %w", &github.RateLimitError{
		Response: &http.Response{StatusCode: http.StatusForbidden, Request: &http.Request{Method: http.MethodGet, URL: &url.URL{Host: "api.github.com"}}},
		Message:  "API rate limit exceeded",
	})
	if got := Classify(err); got != ReasonRateLimited {
		t.Errorf("Classify(github limit) = %q, want %q", got, ReasonRateLimited)
	}
}

func TestSafeSentences_RequestLimitAndNotAllowed_ExactText(t *testing.T) {
	want := map[Reason]string{
		ReasonRateLimited: "A request limit was reached, either Sharko's own or the one set by a service Sharko uses, such as GitHub. Nothing was changed. Try again in a moment.",
		ReasonNotAllowed:  "The person or key that asked is not allowed to do this, so nothing was changed.",
	}
	for r, s := range want {
		if got := sentenceFor(r).String(); got != s {
			t.Errorf("%s: %q", r, got)
		}
		if !r.Valid() {
			t.Errorf("%s is not a valid reason", r)
		}
	}
	// A request limit must not read as the generic "upstream refused" row.
	if sentenceFor(ReasonRateLimited).String() == sentenceFor(ReasonUpstream).String() {
		t.Error("a request limit reads the same as a generic upstream failure")
	}
}
