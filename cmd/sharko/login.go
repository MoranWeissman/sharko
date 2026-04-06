package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	loginCmd.Flags().String("server", "", "Sharko server URL (required)")
	loginCmd.Flags().String("username", "", "Username (skips interactive prompt)")
	loginCmd.Flags().String("password", "", "Password (skips interactive prompt, use with --username)")
	loginCmd.MarkFlagRequired("server")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with a Sharko server",
	RunE: func(cmd *cobra.Command, args []string) error {
		server, _ := cmd.Flags().GetString("server")
		server = strings.TrimRight(server, "/")

		flagUsername, _ := cmd.Flags().GetString("username")
		flagPassword, _ := cmd.Flags().GetString("password")

		var username, password string

		switch {
		case flagUsername != "" && flagPassword != "":
			// Both provided — fully non-interactive
			username = flagUsername
			password = flagPassword
		case flagUsername != "":
			// Username provided — only prompt for password
			username = flagUsername
			fmt.Print("Password: ")
			passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = string(passwordBytes)
		default:
			// Neither provided — prompt for both (original behavior)
			fmt.Print("Username: ")
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read username: %w", err)
			}
			username = strings.TrimSpace(line)

			fmt.Print("Password: ")
			passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = string(passwordBytes)
		}

		// POST directly to the server — no config saved yet.
		payload, err := json.Marshal(map[string]string{
			"username": username,
			"password": password,
		})
		if err != nil {
			return fmt.Errorf("cannot marshal login request: %w", err)
		}

		insecure, _ := rootCmd.PersistentFlags().GetBool("insecure")
		httpClient := buildHTTPClient(insecure)
		resp, err := httpClient.Post(server+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("login request failed: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != 200 {
			var errResp map[string]string
			json.Unmarshal(respBody, &errResp)
			msg := errResp["error"]
			if msg == "" {
				msg = string(respBody)
			}
			return fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, msg)
		}

		var loginResp map[string]string
		if err := json.Unmarshal(respBody, &loginResp); err != nil {
			return fmt.Errorf("invalid login response: %w", err)
		}

		token := loginResp["token"]
		if token == "" {
			return fmt.Errorf("login response missing token")
		}

		// Only save config AFTER successful auth.
		cfg := &SharkoConfig{
			Server: server,
			Token:  token,
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Logged in as %s. Token saved to ~/.sharko/config\n", username)
		return nil
	},
}
