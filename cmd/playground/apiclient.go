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

// createConnection creates a new Sharko connection and sets it as active.
// The argocdToken is optional (empty string for in-cluster service account auth).
// Retries up to 5 times with a 3s backoff to handle transient failures during
// cold playground launch (e.g. hitting a pre-rollout pod).
func (c *apiClient) createConnection(name, provider, giteaURL, giteaToken, argocdServerURL, argocdNamespace, argocdToken string) error {
	const maxAttempts = 5
	const retryDelay = 3 * time.Second

	// Build the create connection request body once (shared across retries)
	reqBody := map[string]interface{}{
		"name":           name,
		"set_as_default": true,
		"git": map[string]interface{}{
			"provider": provider,
			"repo_url": giteaURL,
			"token":    giteaToken,
		},
		"argocd": map[string]interface{}{
			"server_url": argocdServerURL,
			"namespace":  argocdNamespace,
			"insecure":   true, // in-cluster self-signed cert
		},
	}
	// Only add argocd.token if provided (in-cluster uses SA tokens)
	if argocdToken != "" {
		reqBody["argocd"].(map[string]interface{})["token"] = argocdToken
	}

	setActiveReq := map[string]string{
		"connection_name": name,
	}

	// Retry loop for both create + set-active
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			fmt.Printf("  Creating Gitea connection (attempt %d/%d)...\n", attempt, maxAttempts)
			time.Sleep(retryDelay)
		}

		// Attempt to create the connection
		body, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal create connection request: %w", err)
		}

		req, err := http.NewRequest("POST", c.baseURL+"/api/v1/connections/", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create connection request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.token)

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("POST /api/v1/connections/: %w", err)
			continue // retry on transport error
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("POST /api/v1/connections/: status %d: %s", resp.StatusCode, string(bodyBytes))
			continue // retry on non-2xx
		}
		resp.Body.Close()

		// Set the connection as active
		body, err = json.Marshal(setActiveReq)
		if err != nil {
			return fmt.Errorf("marshal set active request: %w", err)
		}

		req, err = http.NewRequest("POST", c.baseURL+"/api/v1/connections/active", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create set active request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.token)

		resp, err = client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("POST /api/v1/connections/active: %w", err)
			continue // retry on transport error
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("POST /api/v1/connections/active: status %d: %s", resp.StatusCode, string(bodyBytes))
			continue // retry on non-200
		}
		resp.Body.Close()

		// Success!
		return nil
	}

	// All attempts failed
	return fmt.Errorf("createConnection failed after %d attempts: %w", maxAttempts, lastErr)
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
