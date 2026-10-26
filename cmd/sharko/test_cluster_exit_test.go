package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestTestCluster_ExitCodeAndNotTestedLine pins ep4 round T, T2 on the CLI.
// `sharko test-cluster` exits non-zero when the test did not pass or could not
// run, so a script can tell. When Sharko could not read the cluster's login it
// never asked the cluster, so it says "not tested", never "Reachable: no".
func TestTestCluster_ExitCodeAndNotTestedLine(t *testing.T) {
	const awsSentence = "Sharko's AWS login was turned down by AWS."
	const notTested = "Reachable: not tested. Sharko could not read this cluster's login, so it did not try to reach the cluster."

	cases := []struct {
		name       string
		body       map[string]interface{}
		wantErr    string
		wantLines  []string
		bannedText []string
	}{
		{
			name: "the login read failed",
			body: map[string]interface{}{
				"name": "prod-eu", "reachable": false, "stage": "credentials",
				"error_code": "ERR_AUTH", "error_message": awsSentence,
			},
			wantErr:    `the connection test for "prod-eu" could not run`,
			wantLines:  []string{"Cluster: prod-eu", notTested, "Error: " + awsSentence},
			bannedText: []string{"Reachable: no\n", "Reachable: yes"},
		},
		{
			name: "the cluster did not answer",
			body: map[string]interface{}{
				"name": "prod-eu", "reachable": false, "stage": "connectivity",
				"error_message": "Sharko could not reach the cluster.",
			},
			wantErr:    `the connection test for "prod-eu" did not pass`,
			wantLines:  []string{"Cluster: prod-eu", "Reachable: no", "Error: Sharko could not reach the cluster."},
			bannedText: []string{"not tested", "Reachable: yes"},
		},
		{
			name: "the cluster answered",
			body: map[string]interface{}{
				"name": "prod-eu", "reachable": true, "server_version": "v1.29.3",
			},
			wantLines:  []string{"Cluster: prod-eu", "Reachable: yes", "Kubernetes version: v1.29.3"},
			bannedText: []string{"not tested", "Reachable: no\n", "Error:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/prod-eu/test" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				writeCLIJSON(t, w, tc.body)
			})
			resetFlags(testClusterCmd)
			out, err := captureStdoutT(t, func() error {
				return testClusterCmd.RunE(testClusterCmd, []string{"prod-eu"})
			})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("error = %v, want none: the cluster answered", err)
			case tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr):
				t.Errorf("error = %v, want %q (a non-zero exit)", err, tc.wantErr)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			for _, want := range tc.wantLines {
				found := false
				for _, l := range lines {
					if l == want {
						found = true
					}
				}
				if !found {
					t.Errorf("missing line %q in:\n%s", want, out)
				}
			}
			for _, banned := range tc.bannedText {
				if strings.Contains(out, banned) {
					t.Errorf("output says %q:\n%s", banned, out)
				}
			}
		})
	}
}
