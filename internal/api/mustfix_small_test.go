package api

import (
	"errors"
	"net/http"
	"testing"
)

// Small findings from the three reviews of the pre-release fix branch, fixed
// together with the must-fixes.

// A connection test that was refused because of the caller's role was written
// in the audit log as "clusters_created": the refusal returned before the row
// got a name, so the name was made from the route.
func TestSmallFix_RefusedConnectionTestIsStillLoggedAsATest(t *testing.T) {
	srv, h := walkCServer(t, "viewer") // bob is a viewer
	bob := walkCLogin(t, srv, "bob")

	rw := walkCCall(t, h, bob, http.MethodPost, "/api/v1/clusters/spoke-9/test", nil)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("a viewer's connection test: %d %s, want 403", rw.Code, rw.Body.String())
	}
	e := lastAuditEntry(t, srv)
	if e.Event != "cluster_tested" {
		t.Errorf("event = %q, want %q", e.Event, "cluster_tested")
	}
	if e.Resource != "cluster:spoke-9" {
		t.Errorf("resource = %q, want %q", e.Resource, "cluster:spoke-9")
	}
	if e.Result != "rejected" {
		t.Errorf("result = %q, want rejected (the caller was not allowed)", e.Result)
	}
}

// A values file with nothing in it is there. It used to be reported as "no
// file", and the page then said "There is no file at <path> ... Create it
// there", for a file that exists.
func TestSmallFix_AZeroByteValuesFileIsAFileThatIsThere(t *testing.T) {
	router := v4ValuesRouter(t, map[string][]byte{
		"values/global/reloader.yaml":           {},
		"values/clusters/spoke-1/reloader.yaml": {},
	})

	body := getValuesJSON(t, router, "/api/v1/addons/reloader/values-schema")
	if body["file_exists"] != true {
		t.Errorf("global file with nothing in it: file_exists = %v, want true", body["file_exists"])
	}
	if body["current_values"] != "" {
		t.Errorf("current_values = %q, want empty", body["current_values"])
	}

	body = getValuesJSON(t, router, "/api/v1/clusters/spoke-1/addons/reloader/values")
	if body["file_exists"] != true {
		t.Errorf("cluster file with nothing in it: file_exists = %v, want true", body["file_exists"])
	}
	if body["wrapped_under_addon_name"] == true {
		t.Error("a file with nothing in it cannot be wrapped")
	}
}

// "429" only means "asked too often" when it stands as a number on its own.
func TestSmallFix_429InsideOtherTextIsNotARateLimit(t *testing.T) {
	notALimit := []string{
		"merge pull request #429: conflict",
		"reading commit 9f429ab1: object not found",
		"dial tcp 10.1.2.3:4290: i/o failure",
		"chart podinfo version 1.429.0 has no values file",
		"GET /repos/acme/gitops/pulls/429: not found",
		"file values-429.yaml is not valid YAML",
	}
	for _, msg := range notALimit {
		if got := classifyUpstreamError(errors.New(msg)); got == http.StatusTooManyRequests {
			t.Errorf("%q was answered as a request limit (429)", msg)
		}
	}
	isALimit := []string{
		"unexpected status: 429 Too Many Requests",
		"upstream replied 429",
		"get file content: unexpected status 429",
		"server answered (429)",
		"status=429",
		"429",
	}
	for _, msg := range isALimit {
		if got := classifyUpstreamError(errors.New(msg)); got != http.StatusTooManyRequests {
			t.Errorf("%q → %d, want 429", msg, got)
		}
	}
}
