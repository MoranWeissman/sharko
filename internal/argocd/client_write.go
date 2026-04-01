package argocd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/moran/argocd-addons-platform/internal/models"
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
