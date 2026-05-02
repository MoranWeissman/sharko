package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// SharkoConfig holds CLI configuration (~/.sharko/config).
type SharkoConfig struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

// configHomeWarned ensures the "$HOME not set" warning is only printed once
// per CLI invocation, even when both load + save call configDir.
var configHomeWarned bool

// configDir returns the directory where the CLI config lives.
//
// Resolution order:
//  1. SHARKO_CONFIG_DIR — explicit override (used by tests and constrained
//     environments)
//  2. ~/.sharko — the normal case, when $HOME is set to a real user home
//  3. <os.TempDir()>/.sharko — fallback when $HOME is missing or resolves to
//     an unwritable root path (e.g. inside a container running with no HOME
//     env var, where os.UserHomeDir() can return "" or "/")
//
// The fallback exists so `sharko login` does not crash with
// "mkdir /.sharko: permission denied" when run as a non-root user inside a
// minimal container image. The first time the fallback fires, a one-line
// warning is printed to stderr so the operator notices the unusual
// resolution.
func configDir() string {
	if v := os.Getenv("SHARKO_CONFIG_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && home != "/" {
		return filepath.Join(home, ".sharko")
	}
	fallback := filepath.Join(os.TempDir(), ".sharko")
	if !configHomeWarned {
		fmt.Fprintf(os.Stderr,
			"warning: $HOME not set, using %s for config storage (set $HOME or SHARKO_CONFIG_DIR to override)\n",
			fallback)
		configHomeWarned = true
	}
	return fallback
}

// configPath returns the path to the CLI config file.
func configPath() string {
	return filepath.Join(configDir(), "config")
}

// loadConfig reads the CLI config file.
func loadConfig() (*SharkoConfig, error) {
	path := configPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config not found: run 'sharko login' first")
	}

	var cfg SharkoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file: %w", err)
	}
	return &cfg, nil
}

// saveConfig writes the CLI config file.
func saveConfig(cfg *SharkoConfig) error {
	path := configPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}
	return nil
}

// buildHTTPClient creates an HTTP client with a 15-second timeout.
// If insecure is true, TLS certificate verification is skipped.
func buildHTTPClient(insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
}

// apiRequest sends an authenticated HTTP request to the Sharko server.
// Returns the response body bytes and status code.
func apiRequest(method, path string, body interface{}) ([]byte, int, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, 0, err
	}
	if cfg.Token == "" {
		return nil, 0, fmt.Errorf("not authenticated — run 'sharko login' first")
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("cannot marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := cfg.Server + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	insecure, _ := rootCmd.PersistentFlags().GetBool("insecure")
	client := buildHTTPClient(insecure)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("cannot read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// apiGet is a convenience wrapper for GET requests.
func apiGet(path string) ([]byte, int, error) {
	return apiRequest(http.MethodGet, path, nil)
}

// apiPost is a convenience wrapper for POST requests.
func apiPost(path string, body interface{}) ([]byte, int, error) {
	return apiRequest(http.MethodPost, path, body)
}

// apiPatch is a convenience wrapper for PATCH requests.
func apiPatch(path string, body interface{}) ([]byte, int, error) {
	return apiRequest(http.MethodPatch, path, body)
}
