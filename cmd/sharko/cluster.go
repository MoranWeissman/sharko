package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MoranWeissman/sharko/internal/fanout"
)

func init() {
	addClusterCmd.Flags().String("addons", "", "Comma-separated list of addons to enable")
	addClusterCmd.Flags().String("region", "", "Cloud region of the cluster (EKS and stored-kubeconfig sources only)")
	addClusterCmd.Flags().String("creds-source", "", `How Sharko gets the cluster's credentials: "eks-token", "secret-kubeconfig" or "inline-kubeconfig"`)
	addClusterCmd.Flags().String("kubeconfig-file", "", "Path to a kubeconfig file to hand to Sharko (inline-kubeconfig source only)")
	addClusterCmd.Flags().String("secret-path", "", "Name or path of the secret that holds the credentials (eks-token and secret-kubeconfig sources)")
	addClusterCmd.Flags().String("role-arn", "", "AWS role Sharko takes on to reach this cluster (eks-token source only)")
	addClusterCmd.Flags().String("connection-managed-by", "", `Who looks after the ArgoCD cluster secret: "sharko" (default) or "user" (you make it yourself)`)
	addClusterCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	rootCmd.AddCommand(addClusterCmd)

	removeClusterCmd.Flags().BoolP("yes", "y", false, "Confirm the removal (required — the server refuses without it)")
	removeClusterCmd.Flags().String("cleanup", "", `How much to remove: "all" (default), "git", or "none"`)
	rootCmd.AddCommand(removeClusterCmd)

	updateClusterCmd.Flags().String("add-addon", "", "Comma-separated addons to enable")
	updateClusterCmd.Flags().String("remove-addon", "", "Comma-separated addons to disable")
	rootCmd.AddCommand(updateClusterCmd)

	rootCmd.AddCommand(listClustersCmd)
}

// knownNonAddonLabels are label keys that should not be counted as addons.
var knownNonAddonLabels = map[string]bool{
	"region": true,
}

// Values the register API accepts for the credentials source and for who
// manages the ArgoCD connection. They match internal/orchestrator
// (CredsSource*) and internal/models (ConnectionManagedBy*), which is also
// what the register dialog in the UI sends.
const (
	credsSourceInline = "inline-kubeconfig"
	credsSourceSecret = "secret-kubeconfig"
	credsSourceEKS    = "eks-token"
)

// buildAddClusterBody turns the add-cluster flags into the request body for
// POST /api/v1/clusters. It does the cheap checks the server also does, so a
// wrong mix of flags is explained before anything is sent. readFile is passed
// in so tests do not need real files.
func buildAddClusterBody(name string, addons map[string]bool, region, credsSource, kubeconfigFile, secretPath, roleARN, managedBy string, readFile func(string) ([]byte, error)) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"name":   name,
		"addons": addons,
		"region": region,
	}

	switch credsSource {
	case "", credsSourceInline, credsSourceSecret, credsSourceEKS:
	default:
		return nil, fmt.Errorf("unknown --creds-source %q (use %s, %s or %s)", credsSource, credsSourceEKS, credsSourceSecret, credsSourceInline)
	}
	switch managedBy {
	case "", "sharko", "user":
	default:
		return nil, fmt.Errorf("unknown --connection-managed-by %q (use \"sharko\" or \"user\")", managedBy)
	}

	if credsSource == "" {
		// No source chosen: the server works it out from the other fields,
		// as it always did. Flags that only make sense with a source still
		// go through, and the server answers.
		if kubeconfigFile != "" {
			return nil, fmt.Errorf("--kubeconfig-file needs --creds-source %s", credsSourceInline)
		}
		if secretPath != "" {
			body["secret_path"] = secretPath
		}
		if roleARN != "" {
			body["role_arn"] = roleARN
		}
	} else {
		body["creds_source"] = credsSource
		if credsSource == credsSourceInline {
			// Same rules as the server: a pasted kubeconfig stands alone.
			if secretPath != "" {
				return nil, fmt.Errorf("--secret-path cannot be used with --creds-source %s", credsSourceInline)
			}
			if region != "" {
				return nil, fmt.Errorf("--region cannot be used with --creds-source %s", credsSourceInline)
			}
			if roleARN != "" {
				return nil, fmt.Errorf("--role-arn cannot be used with --creds-source %s", credsSourceInline)
			}
			body["provider"] = "kubeconfig"
			if kubeconfigFile != "" {
				data, err := readFile(kubeconfigFile)
				if err != nil {
					return nil, fmt.Errorf("could not read the kubeconfig file: %w", err)
				}
				if strings.TrimSpace(string(data)) == "" {
					return nil, fmt.Errorf("the kubeconfig file %s is empty", kubeconfigFile)
				}
				body["kubeconfig"] = string(data)
			}
		} else {
			if kubeconfigFile != "" {
				return nil, fmt.Errorf("--kubeconfig-file can only be used with --creds-source %s", credsSourceInline)
			}
			if credsSource == credsSourceSecret && roleARN != "" {
				return nil, fmt.Errorf("--role-arn can only be used with --creds-source %s", credsSourceEKS)
			}
			body["provider"] = "eks"
			if secretPath != "" {
				body["secret_path"] = secretPath
			}
			if roleARN != "" {
				body["role_arn"] = roleARN
			}
		}
	}

	if managedBy == "user" {
		// Only sent for the non-default choice, like the register dialog.
		body["connection_managed_by"] = "user"
	}
	return body, nil
}

var addClusterCmd = &cobra.Command{
	Use:   "add-cluster <name>",
	Short: "Register a new cluster",
	Long: `Registers a cluster with Sharko through a pull request.

Use --creds-source to say how Sharko gets the cluster's credentials. These are
the same choices as the register dialog in the UI.

  eks-token          Sharko makes a short-lived AWS token each time it connects.
                     Nothing to store. EKS only. --role-arn and --secret-path
                     are optional.
  secret-kubeconfig  Sharko reads a kubeconfig that is kept in your secrets
                     backend. Give its name with --secret-path.
  inline-kubeconfig  You hand Sharko a kubeconfig file once and it stores it.
                     Give the file with --kubeconfig-file. The token inside can
                     run out, so you will need to hand over a new one then.

Leave --creds-source out and Sharko works it out from the other flags, as it
did before.

--connection-managed-by user means you create the ArgoCD cluster secret
yourself and Sharko never writes it. The default is sharko.

Examples:
  sharko add-cluster prod-eu --creds-source eks-token --region eu-west-1 --role-arn arn:aws:iam::123456789012:role/sharko-access
  sharko add-cluster prod-eu --creds-source secret-kubeconfig --secret-path clusters/prod-eu
  sharko add-cluster prod-eu --creds-source inline-kubeconfig --kubeconfig-file ./prod-eu.kubeconfig
  sharko add-cluster prod-eu --creds-source eks-token --connection-managed-by user`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		addonsFlag, _ := cmd.Flags().GetString("addons")
		region, _ := cmd.Flags().GetString("region")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		credsSource, _ := cmd.Flags().GetString("creds-source")
		kubeconfigFile, _ := cmd.Flags().GetString("kubeconfig-file")
		secretPath, _ := cmd.Flags().GetString("secret-path")
		roleARN, _ := cmd.Flags().GetString("role-arn")
		managedBy, _ := cmd.Flags().GetString("connection-managed-by")

		addons := make(map[string]bool)
		if addonsFlag != "" {
			for _, a := range strings.Split(addonsFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					addons[a] = true
				}
			}
		}

		body, err := buildAddClusterBody(name, addons, region, credsSource, kubeconfigFile, secretPath, roleARN, managedBy, os.ReadFile)
		if err != nil {
			return err
		}
		if dryRun {
			body["dry_run"] = true
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing cluster %s registration...\n", name)
		} else {
			fmt.Printf("Registering cluster %s... ", name)
		}
		respBody, status, err := apiPost("/api/v1/clusters", body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}

		if !dryRun && status != 201 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		if dryRun && status != 200 && status != 207 {
			return printAPIError(respBody, status)
		}

		// Handle dry-run response.
		if dryRun {
			var result struct {
				DryRun  *cliDryRunResult `json:"dry_run"`
				Cluster struct {
					Server        string `json:"server"`
					ServerVersion string `json:"server_version"`
				} `json:"cluster"`
				FailedStep string `json:"failed_step"`
				Error      string `json:"error"`
				Message    string `json:"message"`
			}
			if err := json.Unmarshal(respBody, &result); err != nil {
				return fmt.Errorf("invalid response: %w", err)
			}

			fmt.Println()

			// 207: the preview stopped part-way (LO-7 R5) — for example
			// Sharko could not read the cluster registry from Git. There is
			// no preview to show, so say why and exit non-zero, as before
			// when this came back as an error.
			if status == 207 {
				fmt.Printf("  %s\n", result.Message)
				if result.FailedStep != "" {
					fmt.Printf("  Failed step: %s\n", result.FailedStep)
				}
				if result.Error != "" {
					fmt.Printf("  Error:       %s\n", result.Error)
				}
				return fmt.Errorf("the preview for cluster %s could not be built", name)
			}

			if result.Cluster.Server != "" {
				fmt.Printf("  Server:  %s\n", result.Cluster.Server)
			}
			if result.Cluster.ServerVersion != "" {
				fmt.Printf("  Version: %s\n", result.Cluster.ServerVersion)
			}

			printDryRun(result.DryRun)
			return nil
		}

		var result struct {
			Status  string `json:"status"`
			Cluster struct {
				Name          string          `json:"name"`
				Server        string          `json:"server"`
				ServerVersion string          `json:"server_version"`
				Addons        map[string]bool `json:"addons"`
			} `json:"cluster"`
			Git *struct {
				Mode   string `json:"mode"`
				PRUrl  string `json:"pr_url"`
				Branch string `json:"branch"`
			} `json:"git"`
			CompletedSteps []string `json:"completed_steps"`
			FailedStep     string   `json:"failed_step"`
			FailedSecrets  []struct {
				Name  string `json:"name"`
				Error string `json:"error"`
			} `json:"failed_secrets"`
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			fmt.Println("no readable answer")
			return fmt.Errorf("invalid response: %w", err)
		}

		// One count decides the word that finishes the progress line, the
		// headline, the review warning and the exit code, so they cannot
		// tell four different stories. It is the same decision
		// `sharko add-clusters` takes — one cluster is a fan-out of one.
		outcome := fanout.SingleStatus(result.Status, status == 207)

		// "done" used to be printed the moment the HTTP call came back, so a
		// registration that stopped part-way said "done" and then said the
		// opposite two lines later.
		fmt.Println(outcome.ProgressWord())

		fmt.Println()
		if outcome.Completed() {
			fmt.Println("Cluster registered:")
		} else {
			fmt.Print(outcome.TroubleHeadline(addClusterOperation, name))
			fmt.Print(outcome.ReviewWarning())
			if result.FailedStep != "" {
				fmt.Printf("  Failed step: %s\n", result.FailedStep)
			}
			if result.Error != "" {
				fmt.Printf("  Error:       %s\n", result.Error)
			}
			for _, fs := range result.FailedSecrets {
				fmt.Printf("  Secret failed: %s — %s\n", fs.Name, fs.Error)
			}
		}

		if result.Cluster.Server != "" {
			fmt.Printf("  Server:  %s\n", result.Cluster.Server)
		}
		if result.Cluster.ServerVersion != "" {
			fmt.Printf("  Version: %s\n", result.Cluster.ServerVersion)
		}
		if len(result.Cluster.Addons) > 0 {
			var enabled []string
			for k, v := range result.Cluster.Addons {
				if v {
					enabled = append(enabled, k)
				}
			}
			fmt.Printf("  Addons:  %s\n", strings.Join(enabled, ", "))
		}
		if result.Git != nil {
			if result.Git.PRUrl != "" {
				fmt.Printf("  Git:     PR %s\n", result.Git.PRUrl)
			} else if result.Git.Branch != "" {
				fmt.Printf("  Git:     branch %s\n", result.Git.Branch)
			}
		}
		if result.Message != "" {
			fmt.Printf("  Note:    %s\n", result.Message)
		}

		// The command used to return nil whatever came back, so a
		// registration that stopped half-way — pull request merged, ArgoCD
		// connection swapped, Secrets landed — exited 0 and a wrapping
		// script was told it had completed.
		return outcome.ExitError(addClusterOperation, name)
	},
}

// The five single-cluster operations, named for the headline, the review
// warning and the exit message. Short noun phrases: they are read as the
// subject of a sentence.
const (
	addClusterOperation    = "Cluster registration"
	removeClusterOperation = "Cluster removal"
	updateClusterOperation = "The addon update"
)

var removeClusterCmd = &cobra.Command{
	Use:   "remove-cluster <name>",
	Short: "Deregister a cluster",
	Long: `Removes a cluster from Sharko through a pull request.

The server refuses a removal without an explicit confirmation, so this
command requires -y/--yes. Before confirming, see exactly what the removal
will do with: sharko unregister-consequences <name>.

--cleanup controls how much goes: "all" (default — repo files, remote addon
secrets, and the ArgoCD connection IF Sharko owns it; a connection owned by
another tool or by you is always left alone), "git" (repo files only, the
connection stays), or "none" (only the fleet-record entry).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			// The server would refuse anyway (it requires "yes": true) — but
			// with instructions instead of a bare 400: point at the
			// consequences read first, then the confirmed rerun.
			return fmt.Errorf(
				"removing a cluster needs an explicit confirmation.\nFirst see what it would do:  sharko unregister-consequences %s\nThen run:                    sharko remove-cluster %s -y",
				name, name)
		}
		cleanup, _ := cmd.Flags().GetString("cleanup")
		body := map[string]interface{}{"yes": true}
		if cleanup != "" {
			body["cleanup"] = cleanup
		}

		fmt.Printf("Removing cluster %s... ", name)
		respBody, status, err := apiRequest("DELETE", "/api/v1/clusters/"+url.PathEscape(name), body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		var result struct {
			Status     string `json:"status"`
			FailedStep string `json:"failed_step"`
			Error      string `json:"error"`
			Message    string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			fmt.Println("no readable answer")
			return fmt.Errorf("invalid response: %w", err)
		}

		// The same one decision as everywhere else. This endpoint answers
		// 200 with a body that says "failed" when the Git commit did not go
		// through, and the command printed "Cluster prod-eu removed." over
		// the top of that and exited 0.
		outcome := fanout.SingleStatus(result.Status, status == 207)
		fmt.Println(outcome.ProgressWord())

		if outcome.Completed() {
			fmt.Printf("Cluster %s removed.\n", name)
		} else {
			fmt.Print(outcome.TroubleHeadline(removeClusterOperation, name))
			fmt.Print(outcome.ReviewWarning())
			if result.FailedStep != "" {
				fmt.Printf("  Failed step: %s\n", result.FailedStep)
			}
			if result.Error != "" {
				fmt.Printf("  Error: %s\n", result.Error)
			}
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return outcome.ExitError(removeClusterOperation, name)
	},
}

var updateClusterCmd = &cobra.Command{
	Use:   "update-cluster <name>",
	Short: "Update addon assignments for a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		addFlag, _ := cmd.Flags().GetString("add-addon")
		removeFlag, _ := cmd.Flags().GetString("remove-addon")

		addons := make(map[string]bool)
		if addFlag != "" {
			for _, a := range strings.Split(addFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					addons[a] = true
				}
			}
		}
		if removeFlag != "" {
			for _, a := range strings.Split(removeFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					if _, conflict := addons[a]; conflict {
						return fmt.Errorf("addon %q appears in both --add-addon and --remove-addon", a)
					}
					addons[a] = false
				}
			}
		}

		if len(addons) == 0 {
			return fmt.Errorf("at least one of --add-addon or --remove-addon is required")
		}

		body := map[string]interface{}{
			"addons": addons,
		}

		fmt.Printf("Updating cluster %s... ", name)
		respBody, status, err := apiRequest("PATCH", "/api/v1/clusters/"+url.PathEscape(name), body)
		if err != nil {
			fmt.Println("failed")
			return err
		}

		if status != 200 && status != 207 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}

		var result struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			fmt.Println("no readable answer")
			return fmt.Errorf("invalid response: %w", err)
		}

		outcome := fanout.SingleStatus(result.Status, status == 207)
		fmt.Println(outcome.ProgressWord())

		if outcome.Completed() {
			fmt.Printf("Cluster %s updated.\n", name)
		} else {
			fmt.Print(outcome.TroubleHeadline(updateClusterOperation, name))
			fmt.Print(outcome.ReviewWarning())
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		return outcome.ExitError(updateClusterOperation, name)
	},
}

var listClustersCmd = &cobra.Command{
	Use:   "list-clusters",
	Short: "List all clusters",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/clusters")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var resp struct {
			Clusters []struct {
				Name             string            `json:"name"`
				Region           string            `json:"region"`
				ConnectionStatus string            `json:"connection_status"`
				Labels           map[string]string `json:"labels"`
			} `json:"clusters"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.Clusters) == 0 {
			fmt.Println("No clusters found.")
			return nil
		}

		nameW, statusW, regionW := 4, 6, 6
		for _, c := range resp.Clusters {
			if len(c.Name) > nameW {
				nameW = len(c.Name)
			}
			s := c.ConnectionStatus
			if s == "" {
				s = "unknown"
			}
			if len(s) > statusW {
				statusW = len(s)
			}
			r := c.Region
			if r == "" {
				r = "-"
			}
			if len(r) > regionW {
				regionW = len(r)
			}
		}

		fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameW, "NAME", statusW, "STATUS", regionW, "REGION", "ADDONS")

		for _, c := range resp.Clusters {
			s := c.ConnectionStatus
			if s == "" {
				s = "unknown"
			}
			r := c.Region
			if r == "" {
				r = "-"
			}
			addonCount := 0
			for k, v := range c.Labels {
				if v == "enabled" && !knownNonAddonLabels[k] && !strings.Contains(k, "/") {
					addonCount++
				}
			}
			fmt.Printf("%-*s  %-*s  %-*s  %d\n", nameW, c.Name, statusW, s, regionW, r, addonCount)
		}

		return nil
	},
}

// printAPIError formats and returns an error from an API error response.
//
// The first line is always "API error (HTTP <status>): <message>" — that
// format is depended on by scripts parsing CLI output, so it never changes.
// When the server's error body also carries a coded-error "code" field
// and/or a "problems" list (the v4 semantic-validation shape — see
// writeV4OrchestratorError and writeCodedError in internal/api), those are
// appended on their own lines so a caller doesn't have to re-run the
// request with --dry-run just to see which values or secrets are missing.
func printAPIError(body []byte, status int) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err != nil {
		return fmt.Errorf("API error (HTTP %d): %s", status, string(body))
	}
	msg, _ := errResp["error"].(string)
	if msg == "" {
		msg = string(body)
	}
	out := fmt.Sprintf("API error (HTTP %d): %s", status, msg)
	if code, ok := errResp["code"].(string); ok && code != "" {
		out += fmt.Sprintf("\n  code: %s", code)
	}
	if problems, ok := errResp["problems"]; ok && problems != nil {
		if b, err := json.Marshal(problems); err == nil && string(b) != "null" && string(b) != "[]" {
			out += fmt.Sprintf("\n  problems: %s", string(b))
		}
	}
	return fmt.Errorf("%s", out)
}
