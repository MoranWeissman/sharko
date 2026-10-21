//go:build e2e

package lifecycle

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Story H (episode 2, take 2): pure, in-process unit tests for
// postRetryOnTimeout (cluster_helpers.go). No kind cluster or Docker needed:
//
//	go test -tags e2e ./tests/e2e/lifecycle/ -run TestPostRetryOnTimeout -count=1
//
// The rule they pin: one more try only when the first try timed out, never
// after any answer (a 4xx included), and never more than two tries.

// stallingServer answers each request with statuses[n] for the n-th call; a
// status of 0 means "stall until the client gives up".
func stallingServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		_, _ = io.ReadAll(r.Body)
		status := statuses[len(statuses)-1]
		if n < len(statuses) {
			status = statuses[n]
		}
		if status == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newPost(url string) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		return http.NewRequest(http.MethodPost, url, strings.NewReader(`{"name":"c1"}`))
	}
}

func TestPostRetryOnTimeout_RetriesOnceAfterATimeout(t *testing.T) {
	srv, calls := stallingServer(t, 0, http.StatusOK)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	retries := 0

	resp, err := postRetryOnTimeout(client, newPost(srv.URL), func(error) { retries++ })
	if err != nil {
		t.Fatalf("want success on the second try, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
	if retries != 1 {
		t.Errorf("onRetry called %d times, want 1", retries)
	}
}

func TestPostRetryOnTimeout_NeverRetriesA4xx(t *testing.T) {
	srv, calls := stallingServer(t, http.StatusBadRequest, http.StatusOK)
	client := &http.Client{Timeout: 200 * time.Millisecond}

	resp, err := postRetryOnTimeout(client, newPost(srv.URL), nil)
	if err != nil {
		t.Fatalf("a 4xx is an answer, not an error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 returned as is", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1 (no retry after an answer)", got)
	}
}

func TestPostRetryOnTimeout_GivesUpAfterTwoTimeouts(t *testing.T) {
	srv, calls := stallingServer(t, 0)
	client := &http.Client{Timeout: 200 * time.Millisecond}

	resp, err := postRetryOnTimeout(client, newPost(srv.URL), nil)
	if err == nil {
		resp.Body.Close()
		t.Fatal("want a timeout error after two stalled tries")
	}
	if !isTimeoutError(err) {
		t.Errorf("want a timeout error, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want exactly 2", got)
	}
}

func TestPostRetryOnTimeout_NoRetryOnOtherErrors(t *testing.T) {
	// A server that is closed straight away: connection refused, not a timeout.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	builds := 0
	newReq := func() (*http.Request, error) {
		builds++
		return newPost(url)()
	}
	resp, err := postRetryOnTimeout(&http.Client{Timeout: 2 * time.Second}, newReq, nil)
	if err == nil {
		resp.Body.Close()
		t.Fatal("want an error from a closed server")
	}
	if isTimeoutError(err) {
		t.Fatalf("test setup: expected a non-timeout error, got %v", err)
	}
	if builds != 1 {
		t.Errorf("request built %d times, want 1 (no retry on a non-timeout error)", builds)
	}
}

func TestPostRetryOnTimeout_BuildErrorIsReturned(t *testing.T) {
	want := errors.New("bad request build")
	_, err := postRetryOnTimeout(&http.Client{}, func() (*http.Request, error) { return nil, want }, nil)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}
