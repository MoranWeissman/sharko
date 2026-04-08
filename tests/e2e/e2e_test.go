//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

var baseURL string

func TestMain(m *testing.M) {
	baseURL = os.Getenv("SHARKO_E2E_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	os.Exit(m.Run())
}

func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(baseURL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var health map[string]string
	json.NewDecoder(resp.Body).Decode(&health)
	if health["status"] != "healthy" {
		t.Errorf("expected healthy, got %s", health["status"])
	}
}

func TestLoginAndAuth(t *testing.T) {
	payload := map[string]string{
		"username": "admin",
		"password": "admin",
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from login, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	token, ok := result["token"].(string)
	if !ok || token == "" {
		t.Errorf("expected non-empty token in login response, got: %v", result)
	}
}

func TestRepoStatus(t *testing.T) {
	// Login first to get a token
	payload := map[string]string{
		"username": "admin",
		"password": "admin",
	}
	body, _ := json.Marshal(payload)

	loginResp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	defer loginResp.Body.Close()

	var loginResult map[string]interface{}
	json.NewDecoder(loginResp.Body).Decode(&loginResult)
	token, _ := loginResult["token"].(string)

	// GET /api/v1/repo/status
	req, err := http.NewRequest("GET", baseURL+"/api/v1/repo/status", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("repo status request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from repo/status, got %d", resp.StatusCode)
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode repo status response: %v", err)
	}

	// Fresh install should not be initialized
	initialized, ok := status["initialized"].(bool)
	if !ok {
		t.Errorf("expected boolean 'initialized' field, got: %v", status)
		return
	}
	if initialized {
		t.Logf("repo is already initialized (may have been set up previously)")
	} else {
		t.Logf("repo is not initialized (expected for fresh install): reason=%v", status["reason"])
	}

}
