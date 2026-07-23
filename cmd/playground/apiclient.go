package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// apiClient is a minimal HTTP client for calling Sharko REST API endpoints.
// It mirrors the shapes from tests/e2e/harness/apiclient*.go but without
// the e2e build tag so the playground command can use it.
type apiClient struct {
	baseURL string
	token   string // Bearer token from /api/v1/auth/login
}

// newAPIClient creates an API client for the given Sharko base URL.
func newAPIClient(baseURL string) *apiClient {
	return &apiClient{baseURL: baseURL}
}

// login authenticates with the Sharko server and stores the bearer token.
func (c *apiClient) login(username, password string) error {
	reqBody := map[string]string{
		"username": username,
		"password": password,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal login request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST /api/v1/auth/login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /api/v1/auth/login: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}

	c.token = loginResp.Token
	return nil
}

// registerCluster registers a single cluster with Sharko via POST /api/v1/clusters.
func (c *apiClient) registerCluster(name, kubeconfig string, addons map[string]bool) error {
	reqBody := map[string]interface{}{
		"name":       name,
		"kubeconfig": kubeconfig,
		"addons":     addons,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal register request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/clusters", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST /api/v1/clusters: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /api/v1/clusters: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// waitForSharkoReady polls the Sharko health endpoint until it returns a successful
// response (any non-connection-refused response), or the timeout expires.
func waitForSharkoReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	healthURL := baseURL + "/api/v1/health"

	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err != nil {
			// Check if it's a connection-refused error — if so, keep polling.
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				time.Sleep(2 * time.Second)
				continue
			}
			// For connection-refused, the error message contains "connection refused".
			// Keep polling for those as well.
			if isConnectionRefused(err) {
				time.Sleep(2 * time.Second)
				continue
			}
			// Other errors (e.g. DNS, routing) are unexpected — fail fast.
			return fmt.Errorf("GET %s: %w", healthURL, err)
		}
		resp.Body.Close()

		// Any response (even non-200) means the server is up.
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		// Non-200 but server responded — consider it ready (might be a transient issue).
		return nil
	}

	return fmt.Errorf("Sharko health endpoint did not become ready within %s", timeout)
}

// isConnectionRefused returns true if the error is a connection-refused error.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	return bytes.Contains([]byte(err.Error()), []byte("connection refused"))
}
