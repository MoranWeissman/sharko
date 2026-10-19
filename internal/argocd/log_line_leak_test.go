package argocd

// log_line_leak_test.go — LO-8 R7 (epic video-leftovers, 2026-10-18).
//
// The other leak tests in this package look at the ERROR a failed call
// returns. This file looks at the LOG LINE the same call writes, after it has
// gone through the real log sink (logging.NewHandler), because that line is
// what an operator reads and pastes into a ticket. Two things an operator
// types can carry a credential into it:
//
//   - the ArgoCD server address (the base address every call is made to);
//   - a cluster server address, which DeleteCluster and UpdateClusterLabels
//     put into the request path, and the path is the "endpoint" field.
//
// Neither may show its user part, its query or its fragment in the log.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/logging"
)

// captureSinkLog points slog at the real Sharko log sink, writing into a
// buffer, for the length of one test.
func captureSinkLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewHandler(&buf, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// clusterServerCarriers are the four shapes a credential can take in a
// cluster server address that a caller hands to DeleteCluster or
// UpdateClusterLabels.
// safeSegment is what the log and the error show in its place: the address
// with the credential part taken off, encoded the same way.
var clusterServerCarriers = []struct{ name, server, safeSegment string }{
	{"password slot", "https://alice:" + writeLeakSentinel + "@kube.example:6443", "https%3A%2F%2Fkube.example%3A6443"},
	{"username slot", "https://" + writeLeakSentinel + "@kube.example:6443", "https%3A%2F%2Fkube.example%3A6443"},
	{"query parameter", "https://kube.example:6443/?token=" + writeLeakSentinel, "https%3A%2F%2Fkube.example%3A6443%2F"},
	{"fragment", "https://kube.example:6443/#" + writeLeakSentinel, "https%3A%2F%2Fkube.example%3A6443%2F"},
}

// assertErrorHasNoSentinel checks the error the caller gets back, which can
// end up in an API answer.
func assertErrorHasNoSentinel(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), writeLeakSentinel) || strings.Contains(err.Error(), "alice") {
		t.Errorf("the returned error carries the credential written into the cluster address: %q", err.Error())
	}
}

func assertLogHasNoSentinel(t *testing.T, logs string, wantMsg string) {
	t.Helper()
	if !strings.Contains(logs, `"msg":"`+wantMsg+`"`) {
		t.Fatalf("the log has no %q line, so this test proves nothing:\n%s", wantMsg, logs)
	}
	if strings.Contains(logs, writeLeakSentinel) {
		t.Errorf("the log line carries the credential written into the address:\n%s", logs)
	}
	if strings.Contains(logs, "alice") {
		t.Errorf("the log line carries the user name written into the address:\n%s", logs)
	}
}

// "argocd call got no answer": the base address is never in the line, in any
// of the four shapes, and neither is its host and port.
func TestLogLine_NoAnswer_DoesNotShowTheBaseAddress(t *testing.T) {
	port := closedLoopbackPort(t)
	for _, carrier := range unreachableBaseURLs(port) {
		t.Run(carrier.name, func(t *testing.T) {
			logs := captureSinkLog(t)
			c := NewClient(carrier.url, "tok", false)
			if _, err := c.ListApplications(context.Background()); err == nil {
				t.Fatal("the call to a closed loopback port succeeded — this test proves nothing")
			}
			out := logs.String()
			assertLogHasNoSentinel(t, out, "argocd call got no answer")
			if strings.Contains(out, "127.0.0.1:"+port) {
				t.Errorf("the log line carries the address Sharko dialled:\n%s", out)
			}
			if !strings.Contains(out, `"endpoint":"/api/v1/applications"`) {
				t.Errorf("the log line lost the endpoint, which is what triage needs:\n%s", out)
			}
		})
	}
}

// "argocd call got no answer" for a cluster call: the cluster server address
// in the path keeps no user part, query or fragment.
func TestLogLine_NoAnswer_DoesNotShowTheClusterServerCredential(t *testing.T) {
	port := closedLoopbackPort(t)
	for _, carrier := range clusterServerCarriers {
		t.Run(carrier.name, func(t *testing.T) {
			logs := captureSinkLog(t)
			c := NewClient("https://127.0.0.1:"+port, "tok", false)
			err := c.DeleteCluster(context.Background(), carrier.server)
			if err == nil {
				t.Fatal("the call to a closed loopback port succeeded — this test proves nothing")
			}
			out := logs.String()
			assertLogHasNoSentinel(t, out, "argocd call got no answer")
			assertErrorHasNoSentinel(t, err)
			if want := `"endpoint":"/api/v1/clusters/` + carrier.safeSegment + `"`; !strings.Contains(out, want) {
				t.Errorf("the log line should still name the cluster as %s:\n%s", want, out)
			}
		})
	}
}

// "argocd write call failed" and "argocd call failed": ArgoCD answered with an
// error, and the endpoint field names the cluster without its credential.
func TestLogLine_CallFailed_DoesNotShowTheClusterServerCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	for _, carrier := range clusterServerCarriers {
		t.Run(carrier.name+"/delete", func(t *testing.T) {
			logs := captureSinkLog(t)
			c := NewClient(srv.URL, "tok", false)
			err := c.DeleteCluster(context.Background(), carrier.server)
			if err == nil {
				t.Fatal("ArgoCD answered 500 and the call still succeeded")
			}
			out := logs.String()
			assertLogHasNoSentinel(t, out, "argocd write call failed")
			assertErrorHasNoSentinel(t, err)
			if want := `"endpoint":"/api/v1/clusters/` + carrier.safeSegment + `"`; !strings.Contains(out, want) {
				t.Errorf("the log line should still name the cluster as %s:\n%s", want, out)
			}
		})
		t.Run(carrier.name+"/labels", func(t *testing.T) {
			logs := captureSinkLog(t)
			c := NewClient(srv.URL, "tok", false)
			err := c.UpdateClusterLabels(context.Background(), carrier.server, map[string]string{"a": "b"})
			if err == nil {
				t.Fatal("ArgoCD answered 500 and the call still succeeded")
			}
			out := logs.String()
			assertLogHasNoSentinel(t, out, "argocd call failed")
			assertErrorHasNoSentinel(t, err)
			if want := `"endpoint":"/api/v1/clusters/` + carrier.safeSegment + `"`; !strings.Contains(out, want) {
				t.Errorf("the log line should still name the cluster as %s:\n%s", want, out)
			}
		})
	}
}

// safeEndpoint pinned by exact text: what is kept, what comes off.
func TestSafeEndpoint_ExactText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/api/v1/clusters/" + escapeServerURL("https://alice:pw@kube.example:6443") + "?updateMask=metadata.labels",
			"/api/v1/clusters/https%3A%2F%2Fkube.example%3A6443?updateMask=metadata.labels"},
		{"/api/v1/clusters/" + escapeServerURL("https://kube.example:6443/?token=pw"),
			"/api/v1/clusters/https%3A%2F%2Fkube.example%3A6443%2F"},
		{"/api/v1/clusters/" + escapeServerURL("https://kube.example:6443"),
			"/api/v1/clusters/https%3A%2F%2Fkube.example%3A6443"},
		{"/api/v1/clusters/%zz", "/api/v1/clusters/(address-hidden)"},
		{"/api/v1/applications/my-app?refresh=normal", "/api/v1/applications/my-app?refresh=normal"},
	}
	for _, tc := range cases {
		if got := safeEndpoint(tc.in); got != tc.want {
			t.Errorf("safeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := safeClusterServer("https://alice:pw@kube.example:6443"); got != "https://kube.example:6443" {
		t.Errorf("safeClusterServer = %q", got)
	}
	if got := safeClusterServer("::bad@"); got != "the cluster" {
		t.Errorf("safeClusterServer of an address it cannot read = %q, want %q", got, "the cluster")
	}
}
