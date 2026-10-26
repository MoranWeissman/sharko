package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(testClusterCmd)
}

var testClusterCmd = &cobra.Command{
	Use:   "test-cluster <name>",
	Short: "Test connectivity to a cluster",
	Long: `Check that Sharko can reach the cluster's Kubernetes API.

Sharko uses the connection it has for that cluster. That is the one in your
secrets provider, or, for a cluster whose connection ArgoCD holds, the one
stored in ArgoCD. It makes a small test secret on the cluster, reads it back
and deletes it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiPost("/api/v1/clusters/"+url.PathEscape(name)+"/test", nil)
		if err != nil {
			return err
		}

		if status != 200 {
			// The server returns a non-200 only when the test feature itself
			// is unavailable (e.g. no secrets backend configured) — the
			// cluster being unreachable is still a 200 with reachable: false.
			return printAPIError(respBody, status)
		}

		var result struct {
			Reachable     bool     `json:"reachable"`
			Stage         string   `json:"stage"`
			ErrorMessage  string   `json:"error_message"`
			ServerVersion string   `json:"server_version"`
			Suggestions   []string `json:"suggestions"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Cluster: %s\n", name)
		if result.Reachable {
			fmt.Println("Reachable: yes")
			if result.ServerVersion != "" {
				fmt.Printf("Kubernetes version: %s\n", result.ServerVersion)
			}
			return nil
		}

		// Sharko could not read the cluster's login, so it never asked the
		// cluster anything (ep4 round T, T2). Do not call the cluster
		// unreachable: say the test could not run, and why.
		couldNotRun := result.Stage == testClusterStageCredentials
		if couldNotRun {
			fmt.Println(testClusterNotTestedLine)
		} else {
			fmt.Println("Reachable: no")
		}
		if result.ErrorMessage != "" {
			fmt.Printf("Error: %s\n", result.ErrorMessage)
		}
		if len(result.Suggestions) > 0 {
			fmt.Println("Did you mean one of these secret paths:")
			for _, s := range result.Suggestions {
				fmt.Printf("  %s\n", s)
			}
		}

		// A script must be able to tell a failed test from a passed one
		// by the exit code.
		if couldNotRun {
			return fmt.Errorf(testClusterCouldNotRunFmt, name)
		}
		return fmt.Errorf(testClusterFailedFmt, name)
	},
}

// testClusterStageCredentials is the `stage` the server names when reading
// the cluster's login failed, before the cluster was asked anything. It is
// the same wire value as verifyStageCredentials in internal/api.
const testClusterStageCredentials = "credentials"

// What `sharko test-cluster` says when the test did not pass (ep4 round T).
const (
	testClusterNotTestedLine  = "Reachable: not tested. Sharko could not read this cluster's login, so it did not try to reach the cluster."
	testClusterCouldNotRunFmt = "the connection test for %q could not run"
	testClusterFailedFmt      = "the connection test for %q did not pass"
)
