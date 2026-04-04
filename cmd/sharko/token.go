package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API tokens for automation",
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API token",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")

		if name == "" {
			return fmt.Errorf("--name is required")
		}

		body := map[string]string{
			"name": name,
			"role": role,
		}

		respBody, status, err := apiPost("/api/v1/tokens", body)
		if err != nil {
			return err
		}
		if status != 201 {
			return printAPIError(respBody, status)
		}

		var result struct {
			Name  string `json:"name"`
			Token string `json:"token"`
			Role  string `json:"role"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Token created:\n")
		fmt.Printf("  Name:  %s\n", result.Name)
		fmt.Printf("  Role:  %s\n", result.Role)
		fmt.Printf("  Token: %s\n", result.Token)
		fmt.Println()
		fmt.Println("WARNING: This token will not be shown again. Store it securely.")

		return nil
	},
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/tokens")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var tokens []struct {
			Name      string `json:"name"`
			Role      string `json:"role"`
			CreatedAt string `json:"created_at"`
			LastUsed  string `json:"last_used_at"`
		}
		if err := json.Unmarshal(respBody, &tokens); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens found.")
			return nil
		}

		nameW, roleW := 4, 4
		for _, t := range tokens {
			if len(t.Name) > nameW {
				nameW = len(t.Name)
			}
			if len(t.Role) > roleW {
				roleW = len(t.Role)
			}
		}

		fmt.Printf("%-*s  %-*s  %-20s  %s\n", nameW, "NAME", roleW, "ROLE", "CREATED", "LAST USED")
		for _, t := range tokens {
			lastUsed := "-"
			if t.LastUsed != "" && t.LastUsed != "0001-01-01T00:00:00Z" {
				lastUsed = t.LastUsed
			}
			fmt.Printf("%-*s  %-*s  %-20s  %s\n", nameW, t.Name, roleW, t.Role, t.CreatedAt, lastUsed)
		}

		return nil
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke <name>",
	Short: "Revoke an API token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		respBody, status, err := apiRequest("DELETE", "/api/v1/tokens/"+url.PathEscape(name), nil)
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		fmt.Printf("Token %q revoked.\n", name)
		return nil
	},
}

func init() {
	tokenCreateCmd.Flags().String("name", "", "Token name (required)")
	tokenCreateCmd.Flags().String("role", "admin", "Token role (admin, operator, viewer)")

	tokenCmd.AddCommand(tokenCreateCmd)
	tokenCmd.AddCommand(tokenListCmd)
	tokenCmd.AddCommand(tokenRevokeCmd)

	rootCmd.AddCommand(tokenCmd)
}
