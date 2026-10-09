package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage Sharko users",
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all users",
	RunE: func(cmd *cobra.Command, args []string) error {
		respBody, status, err := apiGet("/api/v1/users")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		output, _ := cmd.Flags().GetString("output")
		if output == "json" {
			fmt.Println(string(respBody))
			return nil
		}

		var users []struct {
			Username string `json:"username"`
			Role     string `json:"role"`
			Enabled  bool   `json:"enabled"`
		}
		if err := json.Unmarshal(respBody, &users); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if len(users) == 0 {
			fmt.Println("No users found.")
			return nil
		}

		nameW, roleW := 8, 4
		for _, u := range users {
			if len(u.Username) > nameW {
				nameW = len(u.Username)
			}
			if len(u.Role) > roleW {
				roleW = len(u.Role)
			}
		}

		fmt.Printf("%-*s  %-*s  %s\n", nameW, "USERNAME", roleW, "ROLE", "ENABLED")
		for _, u := range users {
			fmt.Printf("%-*s  %-*s  %v\n", nameW, u.Username, roleW, u.Role, u.Enabled)
		}

		return nil
	},
}

var userCreateCmd = &cobra.Command{
	Use:   "create <username>",
	Short: "Create a new user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		role, _ := cmd.Flags().GetString("role")

		body := map[string]string{
			"username": username,
			"role":     role,
		}

		respBody, status, err := apiPost("/api/v1/users", body)
		if err != nil {
			return err
		}
		if status != 201 {
			return printAPIError(respBody, status)
		}

		var result struct {
			Username     string `json:"username"`
			Role         string `json:"role"`
			TempPassword string `json:"temp_password"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("User created:\n")
		fmt.Printf("  Username:       %s\n", result.Username)
		fmt.Printf("  Role:           %s\n", result.Role)
		fmt.Printf("  Temp password:  %s\n", result.TempPassword)
		fmt.Println()
		fmt.Println("Share the temporary password with the user — they must change it on first login.")

		return nil
	},
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete <username>",
	Short: "Delete a user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		if !yes {
			fmt.Printf("Delete user %q? This cannot be undone. [y/N]: ", username)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		respBody, status, err := apiRequest("DELETE", "/api/v1/users/"+url.PathEscape(username), nil)
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		fmt.Printf("User %q deleted.\n", username)
		return nil
	},
}

var userUpdateCmd = &cobra.Command{
	Use:   "update <username>",
	Short: "Update a user's role or enabled status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]

		body := map[string]interface{}{}

		if cmd.Flags().Changed("role") {
			role, _ := cmd.Flags().GetString("role")
			body["role"] = role
		}
		if cmd.Flags().Changed("enabled") {
			enabled, _ := cmd.Flags().GetBool("enabled")
			body["enabled"] = enabled
		}

		if len(body) == 0 {
			return fmt.Errorf("at least one of --role or --enabled must be specified")
		}

		respBody, status, err := apiRequest("PUT", "/api/v1/users/"+url.PathEscape(username), body)
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}

		var result struct {
			Username string `json:"username"`
			Role     string `json:"role"`
			Enabled  bool   `json:"enabled"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("User updated:\n")
		fmt.Printf("  Username:  %s\n", result.Username)
		fmt.Printf("  Role:      %s\n", result.Role)
		fmt.Printf("  Enabled:   %v\n", result.Enabled)

		return nil
	},
}

func init() {
	userListCmd.Flags().String("output", "table", "Output format (table, json)")

	userCreateCmd.Flags().String("role", "viewer", "User role (admin, operator, viewer)")

	userDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	userUpdateCmd.Flags().String("role", "", "New role (admin, operator, viewer)")
	userUpdateCmd.Flags().Bool("enabled", true, "Enable or disable the user")

	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userDeleteCmd)
	userCmd.AddCommand(userUpdateCmd)

	rootCmd.AddCommand(userCmd)
}
