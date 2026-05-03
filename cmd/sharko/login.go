package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
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
			return formatConnectionError(server, err)
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

// terminalIO abstracts the subset of golang.org/x/term that
// readPasswordSafeWith depends on. The interface exists so tests can inject a
// recording double — without it, the GetState/Restore/ReadPassword sequence
// can only be exercised against a real PTY, which CI and unit tests don't
// have. Production callers go through readPasswordSafe which passes
// realTerminalIO{}.
type terminalIO interface {
	GetState(fd int) (*term.State, error)
	Restore(fd int, state *term.State) error
	ReadPassword(fd int) ([]byte, error)
	IsTerminal(fd int) bool
}

// realTerminalIO is the production impl: a thin pass-through to
// golang.org/x/term. It carries no state — every call delegates to the
// package-level term.* function with the same signature.
type realTerminalIO struct{}

func (realTerminalIO) GetState(fd int) (*term.State, error)   { return term.GetState(fd) }
func (realTerminalIO) Restore(fd int, state *term.State) error { return term.Restore(fd, state) }
func (realTerminalIO) ReadPassword(fd int) ([]byte, error)    { return term.ReadPassword(fd) }
func (realTerminalIO) IsTerminal(fd int) bool                 { return term.IsTerminal(fd) }

// readPasswordSafe is the production entry point — wraps
// readPasswordSafeWith with the real golang.org/x/term implementation.
func readPasswordSafe(prompt string) (string, error) {
	return readPasswordSafeWith(realTerminalIO{}, prompt)
}

// readPasswordSafeWith prompts the user for a password and returns the
// entered value. It explicitly snapshots and restores the terminal state
// around the read, defending against the well-known footgun where a panic,
// signal, or bug inside golang.org/x/term leaves the parent shell in raw
// mode (visible as stair-stepped output requiring `stty sane`).
//
// The double-restore is intentional: term.ReadPassword internally restores
// the saved state, but our outer defer guarantees restoration even if
// ReadPassword's defer is skipped (e.g. if the goroutine is interrupted by a
// signal that bypasses normal defer semantics, or if a future refactor
// replaces ReadPassword with a non-restoring primitive).
//
// If GetState fails (M3 — review finding), this returns an error
// immediately rather than continuing. The previous behaviour (silently
// skipping the defer registration when GetState failed) defeated the entire
// purpose of the BUG-006 fix — if we can't snapshot the state, we have no
// safety net should ReadPassword leave the TTY in raw mode. Better to fail
// loudly than to leave the shell broken.
func readPasswordSafeWith(tio terminalIO, prompt string) (string, error) {
	fd := int(syscall.Stdin)

	// If stdin is not a terminal (e.g. piped input), fall back to a plain
	// line read. This keeps non-interactive callers working without trying
	// to set a terminal mode that does not exist.
	if !tio.IsTerminal(fd) {
		fmt.Print(prompt)
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// Snapshot current TTY state. If we can't snapshot, bail out — there
	// is no safety net for a subsequent ReadPassword failure (M3).
	state, stateErr := tio.GetState(fd)
	if stateErr != nil {
		return "", fmt.Errorf("cannot snapshot terminal state for password prompt: %w", stateErr)
	}
	// state may legitimately be nil on some platforms even with stateErr
	// nil (e.g. a future term backend). Only defer Restore if we actually
	// got a state to restore to.
	if state != nil {
		defer func() {
			_ = tio.Restore(fd, state)
		}()
	}

	fmt.Print(prompt)
	pwBytes, err := tio.ReadPassword(fd)
	// Always emit the trailing newline (ReadPassword swallows the user's
	// CR), even on the error path, so the next line of output is not glued
	// to the prompt.
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(pwBytes), nil
}

// formatConnectionError turns a low-level dial error into a friendly,
// actionable message that points the user at their --server flag. The
// underlying error is preserved (wrapped) so verbose debugging is not lost.
//
// Categories detected:
//   - connection refused (ECONNREFUSED) — server not running on that port
//   - DNS lookup failure (*net.DNSError) — hostname does not resolve
//   - generic *net.OpError — falls back to the catch-all hint
func formatConnectionError(server string, err error) error {
	if err == nil {
		return nil
	}

	host := server
	if u, parseErr := url.Parse(server); parseErr == nil && u.Host != "" {
		host = u.Host
	}

	if isConnectionRefused(err) {
		return fmt.Errorf(
			"cannot reach Sharko server at %s — connection refused\n"+
				"  → check that the --server URL is correct and the server is running\n"+
				"  → underlying: %w",
			server, err)
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf(
			"cannot reach Sharko server at %s — DNS lookup failed for %s\n"+
				"  → check the --server hostname for typos\n"+
				"  → underlying: %w",
			server, host, err)
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf(
			"cannot reach Sharko server at %s — network error\n"+
				"  → check that the --server URL is reachable from this host\n"+
				"  → underlying: %w",
			server, err)
	}

	return fmt.Errorf("login request failed: %w", err)
}

// isConnectionRefused reports whether err (or any wrapped error) is a TCP
// connection-refused condition. errors.Is on syscall.ECONNREFUSED works on
// Linux and macOS for the standard net.OpError → os.SyscallError chain.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// Best-effort string match for environments where the syscall errno is
	// wrapped in a non-standard way (rare, but cheaper to check than to
	// unwrap manually).
	return strings.Contains(err.Error(), "connection refused")
}
