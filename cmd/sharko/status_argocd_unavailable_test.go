package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestStatus_WhenArgoCDCannotBeRead pins ep4 T1 on the CLI: while ArgoCD
// refuses Sharko's login or cannot be reached, `sharko status` says that in
// plain words and says the clusters' health is not known. It never calls the
// clusters disconnected.
func TestStatus_WhenArgoCDCannotBeRead(t *testing.T) {
	cases := []struct {
		name     string
		reason   string
		wantLine string
	}{
		{"ArgoCD refused the login", "auth_refused",
			"ArgoCD:       ArgoCD refused Sharko's login, so Sharko does not know right now if your clusters are healthy."},
		{"ArgoCD could not be reached", "unreachable",
			"ArgoCD:       Sharko could not reach ArgoCD, so Sharko does not know right now if your clusters are healthy."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/fleet/status" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				writeCLIJSON(t, w, map[string]interface{}{
					"server_version":          "4.0.5",
					"uptime":                  "5m",
					"argo_unavailable":        true,
					"argo_unavailable_reason": tc.reason,
					"total_clusters":          2,
					"healthy_clusters":        0,
					"degraded_clusters":       0,
					"disconnected_clusters":   0,
					"clusters":                []interface{}{},
				})
			})
			out, err := captureStdoutT(t, func() error { return statusCmd.RunE(statusCmd, nil) })
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			has := func(want string) bool {
				for _, l := range lines {
					if l == want {
						return true
					}
				}
				return false
			}
			if !has(tc.wantLine) {
				t.Errorf("missing line %q in:\n%s", tc.wantLine, out)
			}
			if !has("Clusters:     2 total (health not known)") {
				t.Errorf("missing the cluster count line in:\n%s", out)
			}
			for _, banned := range []string{"disconnected", "0 healthy", "not connected"} {
				if strings.Contains(out, banned) {
					t.Errorf("output says %q, which is not true while ArgoCD is not answering:\n%s", banned, out)
				}
			}
		})
	}

	t.Run("no ArgoCD set up at all", func(t *testing.T) {
		startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeCLIJSON(t, w, map[string]interface{}{
				"server_version":   "4.0.5",
				"argo_unavailable": true,
				"clusters":         []interface{}{},
			})
		})
		out, err := captureStdoutT(t, func() error { return statusCmd.RunE(statusCmd, nil) })
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if !strings.Contains(out, "ArgoCD:       not connected\n") {
			t.Errorf("want the old not connected line when no reason is known:\n%s", out)
		}
		if strings.Contains(out, "Clusters:") {
			t.Errorf("no cluster line expected when ArgoCD is not set up:\n%s", out)
		}
	})
}
