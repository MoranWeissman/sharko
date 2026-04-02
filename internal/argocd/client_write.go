package argocd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/MoranWeissman/sharko/internal/models"
)

// SyncApplication triggers a sync operation on the named ArgoCD application.
func (c *Client) SyncApplication(ctx context.Context, appName string) error {
	path := "/api/v1/applications/" + appName + "/sync"
	_, err := c.doPost(ctx, path, []byte("{}"))
	if err != nil {
		return fmt.Errorf("syncing application %q: %w", appName, err)
	}
	return nil
}

// RefreshApplication forces ArgoCD to re-fetch the application state.
// When hard is true, the entire application manifest cache is invalidated.
func (c *Client) RefreshApplication(ctx context.Context, appName string, hard bool) (*models.ArgocdApplication, error) {
	refresh := "true"
	if hard {
		refresh = "hard"
	}

	path := fmt.Sprintf("/api/v1/applications/%s?refresh=%s", appName, refresh)
	body, err := c.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("refreshing application %q: %w", appName, err)
	}

	var raw argocdApplicationItem
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding refresh response: %w", err)
	}

	app := raw.toModel()
	return &app, nil
}

// doPost performs an authenticated POST request and returns the response body.
func (c *Client) doPost(ctx context.Context, path string, payload []byte) ([]byte, error) {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("argocd POST call failed", "error", err, "endpoint", path)
		return nil, fmt.Errorf("executing POST request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("argocd POST call failed", "endpoint", path, "status", resp.StatusCode)
		return nil, fmt.Errorf("unexpected status %d from POST %s: %s", resp.StatusCode, path, string(body))
	}

	return body, nil
}

// doPut performs an authenticated PUT request and returns the response body.
func (c *Client) doPut(ctx context.Context, path string, payload []byte) ([]byte, error) {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("argocd PUT call failed", "error", err, "endpoint", path)
		return nil, fmt.Errorf("executing PUT request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("argocd PUT call failed", "endpoint", path, "status", resp.StatusCode)
		return nil, fmt.Errorf("unexpected status %d from PUT %s: %s", resp.StatusCode, path, string(body))
	}

	return body, nil
}

// doDelete performs an authenticated DELETE request and returns the response body.
func (c *Client) doDelete(ctx context.Context, path string) ([]byte, error) {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("argocd DELETE call failed", "error", err, "endpoint", path)
		return nil, fmt.Errorf("executing DELETE request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("argocd DELETE call failed", "endpoint", path, "status", resp.StatusCode)
		return nil, fmt.Errorf("unexpected status %d from DELETE %s: %s", resp.StatusCode, path, string(body))
	}

	return body, nil
}

// RegisterCluster registers a cluster in ArgoCD by POSTing to the clusters API.
func (c *Client) RegisterCluster(ctx context.Context, name, server string, caData []byte, token string, labels map[string]string) error {
	payload := map[string]interface{}{
		"name":   name,
		"server": server,
		"config": map[string]interface{}{
			"bearerToken": token,
			"tlsClientConfig": map[string]interface{}{
				"caData":   base64.StdEncoding.EncodeToString(caData),
				"insecure": false,
			},
		},
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling cluster payload: %w", err)
	}

	_, err = c.doPost(ctx, "/api/v1/clusters", body)
	if err != nil {
		return fmt.Errorf("registering cluster %q: %w", name, err)
	}
	return nil
}

// DeleteCluster removes a cluster from ArgoCD by server URL.
func (c *Client) DeleteCluster(ctx context.Context, serverURL string) error {
	path := "/api/v1/clusters/" + url.PathEscape(serverURL)
	_, err := c.doDelete(ctx, path)
	if err != nil {
		return fmt.Errorf("deleting cluster %q: %w", serverURL, err)
	}
	return nil
}

// UpdateClusterLabels updates the labels on an ArgoCD cluster.
// It fetches the current cluster, merges the new labels, and PUTs it back.
func (c *Client) UpdateClusterLabels(ctx context.Context, serverURL string, labels map[string]string) error {
	// GET the current cluster.
	getPath := "/api/v1/clusters/" + url.PathEscape(serverURL)
	body, err := c.doGet(ctx, getPath)
	if err != nil {
		return fmt.Errorf("fetching cluster %q for label update: %w", serverURL, err)
	}

	var cluster map[string]interface{}
	if err := json.Unmarshal(body, &cluster); err != nil {
		return fmt.Errorf("decoding cluster response: %w", err)
	}

	// Merge labels.
	existing, _ := cluster["labels"].(map[string]interface{})
	if existing == nil {
		existing = make(map[string]interface{})
	}
	for k, v := range labels {
		existing[k] = v
	}
	cluster["labels"] = existing

	updated, err := json.Marshal(cluster)
	if err != nil {
		return fmt.Errorf("marshaling updated cluster: %w", err)
	}

	putPath := "/api/v1/clusters/" + url.PathEscape(serverURL) + "?updateMask=metadata.labels"
	_, err = c.doPut(ctx, putPath, updated)
	if err != nil {
		return fmt.Errorf("updating labels on cluster %q: %w", serverURL, err)
	}
	return nil
}

// CreateProject creates an ArgoCD AppProject.
func (c *Client) CreateProject(ctx context.Context, projectJSON []byte) error {
	// ArgoCD expects the project wrapped in a "project" key.
	var proj map[string]interface{}
	if err := json.Unmarshal(projectJSON, &proj); err != nil {
		return fmt.Errorf("parsing project JSON: %w", err)
	}
	wrapped, _ := json.Marshal(map[string]interface{}{"project": proj})
	_, err := c.doPost(ctx, "/api/v1/projects", wrapped)
	if err != nil {
		return fmt.Errorf("creating ArgoCD project: %w", err)
	}
	return nil
}

// CreateApplication creates an ArgoCD Application.
func (c *Client) CreateApplication(ctx context.Context, appJSON []byte) error {
	_, err := c.doPost(ctx, "/api/v1/applications", appJSON)
	if err != nil {
		return fmt.Errorf("creating ArgoCD application: %w", err)
	}
	return nil
}
