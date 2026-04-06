package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	connectCmd.Flags().String("name", "", "Connection name (required)")
	connectCmd.Flags().String("git-provider", "", "Git provider: github or azuredevops (required)")
	connectCmd.Flags().String("git-repo", "", "Git repository URL (required)")
	connectCmd.Flags().String("git-token", "", "Git personal access token")
	connectCmd.Flags().String("argocd-url", "", "ArgoCD server URL")
	connectCmd.Flags().String("argocd-token", "", "ArgoCD API token")
	connectCmd.Flags().String("argocd-namespace", "argocd", "ArgoCD namespace")
	connectCmd.MarkFlagRequired("name")
	connectCmd.MarkFlagRequired("git-provider")
	connectCmd.MarkFlagRequired("git-repo")

	connectCmd.AddCommand(connectListCmd)
	connectCmd.AddCommand(connectTestCmd)

	rootCmd.AddCommand(connectCmd)
}

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Manage Sharko server connections",
	Long: `Create and manage connections between Sharko and Git/ArgoCD.

Examples:
  sharko connect --name prod \
    --git-provider github \
    --git-repo https://github.com/org/addons \
    --git-token ghp_xxx \
    --argocd-url https://argocd.example.com \
    --argocd-token eyJhbG... \
    --argocd-namespace argocd

  sharko connect list
  sharko connect test`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		gitProvider, _ := cmd.Flags().GetString("git-provider")
		gitRepo, _ := cmd.Flags().GetString("git-repo")
		gitToken, _ := cmd.Flags().GetString("git-token")
		argocdURL, _ := cmd.Flags().GetString("argocd-url")
		argocdToken, _ := cmd.Flags().GetString("argocd-token")
		argocdNamespace, _ := cmd.Flags().GetString("argocd-namespace")

		// Build the create connection request matching models.CreateConnectionRequest
		body := map[string]interface{}{
			"name": name,
			"git": map[string]interface{}{
				"provider": gitProvider,
				"repo_url": gitRepo,
				"token":    gitToken,
			},
			"argocd": map[string]interface{}{
				"server_url": argocdURL,
				"token":      argocdToken,
				"namespace":  argocdNamespace,
			},
			"set_as_default": false,
		}

		fmt.Printf("Creating connection %q... ", name)
		respBody, status, err := apiPost("/api/v1/connections/", body)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 201 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		fmt.Println("done")

		// Set as active connection
		fmt.Printf("Setting %q as active connection... ", name)
		activeBody := map[string]interface{}{
			"connection_name": name,
		}
		respBody, status, err = apiPost("/api/v1/connections/active", activeBody)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		fmt.Println("done")

		fmt.Printf("Connection %q created and set as active.\n", name)
		return nil
	},
}

var connectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured connections",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/connections/")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var resp struct {
			Connections []struct {
				Name              string `json:"name"`
				GitProvider       string `json:"git_provider"`
				GitRepoIdentifier string `json:"git_repo_identifier"`
				ArgocdServerURL   string `json:"argocd_server_url"`
				IsActive          bool   `json:"is_active"`
				IsDefault         bool   `json:"is_default"`
			} `json:"connections"`
			ActiveConnection string `json:"active_connection,omitempty"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(resp.Connections) == 0 {
			fmt.Println("No connections configured.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPROVIDER\tREPO\tARGOCD\tSTATUS")
		for _, c := range resp.Connections {
			status := ""
			if c.IsActive {
				status = "active"
			}
			if c.IsDefault {
				if status != "" {
					status += ", default"
				} else {
					status = "default"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				c.Name, c.GitProvider, c.GitRepoIdentifier, c.ArgocdServerURL, status)
		}
		w.Flush()
		return nil
	},
}

var connectTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test the active connection",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print("Testing active connection... ")
		respBody, status, err := apiPost("/api/v1/connections/test", nil)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if status != 200 {
			fmt.Println("failed")
			return printAPIError(respBody, status)
		}
		fmt.Println("ok")

		var result struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(respBody, &result); err == nil && result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}
		return nil
	},
}
