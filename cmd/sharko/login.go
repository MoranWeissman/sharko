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
			pw, err := readPasswordSafe("Password: ")
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = pw
		default:
			// Neither provided — prompt for both (original behavior)
			fmt.Print("Username: ")
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read username: %w", err)
			}
			username = strings.TrimSpace(line)

			pw, err := readPasswordSafe("Password: ")
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = pw
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
			_ = json.Unmarshal(respBody, &errResp)
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

		fmt.Printf("Logged in as %s. Token saved to %s\n", username, configPath())
		return nil
	},
}

// readPasswordSafe prompts the user for a password and returns the entered
// value. It explicitly snapshots and restores the terminal state around the
// read, defending against the well-known footgun where a panic, signal, or
// bug inside golang.org/x/term leaves the parent shell in raw mode (visible
// as stair-stepped output requiring `stty sane`).
//
// The double-restore is intentional: term.ReadPassword internally restores
// the saved state, but our outer defer guarantees restoration even if
// ReadPassword's defer is skipped (e.g. if the goroutine is interrupted by a
// signal that bypasses normal defer semantics, or if a future refactor
// replaces ReadPassword with a non-restoring primitive).
func readPasswordSafe(prompt string) (string, error) {
	fd := int(syscall.Stdin)

	// If stdin is not a terminal (e.g. piped input), fall back to a plain
	// line read. This keeps non-interactive callers working without trying
	// to set a terminal mode that does not exist.
	if !term.IsTerminal(fd) {
		fmt.Print(prompt)
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// Snapshot current TTY state and guarantee restoration even if
	// ReadPassword fails to do so on its own.
	state, stateErr := term.GetState(fd)
	if stateErr == nil && state != nil {
		defer func() {
			_ = term.Restore(fd, state)
		}()
	}

	fmt.Print(prompt)
	pwBytes, err := term.ReadPassword(fd)
	// Always emit the trailing newline (ReadPassword swallows the user's
	// CR), even on the error path, so the next line of output is not glued
	// to the prompt.
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(pwBytes), nil
}
