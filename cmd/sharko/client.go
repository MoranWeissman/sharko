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

// configPath returns the path to ~/.sharko/config.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sharko", "config")
}

// loadConfig reads ~/.sharko/config.
func loadConfig() (*SharkoConfig, error) {
	path := configPath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine home directory")
	}

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

// saveConfig writes ~/.sharko/config.
func saveConfig(cfg *SharkoConfig) error {
	path := configPath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
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
