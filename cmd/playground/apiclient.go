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

// enableLegacyInlineCredentials opts this sandbox into legacy inline
// credentials through the real settings door — the same
// PUT /api/v1/settings/allow-inline-credentials an admin uses in Settings.
//
// The playground registers its spokes by pasting their in-cluster
// kubeconfigs (the inline path), and the server-wide default is OFF
// (connection-reconciliation epic, product correction 5): a pasted
// credential exists only in the live connection Secret and cannot be
// restored from Git. That trade-off is fine for a throwaway kind sandbox,
// so the playground opts in explicitly — the server-side default and the
// refusal are never weakened, and this is exactly the "existing
// installations may explicitly enable the legacy option" door the ruling
// describes.
func (c *apiClient) enableLegacyInlineCredentials() error {
	status, body, err := c.doJSON(http.MethodPut, "/api/v1/settings/allow-inline-credentials",
		map[string]interface{}{"allow_inline_credentials": true}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("PUT /api/v1/settings/allow-inline-credentials: status %d: %s", status, string(body))
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

// operationSession mirrors the fields the playground needs from
// operations.Session (internal/operations/store.go) to poll a long-running
// operation such as POST /api/v1/init.
type operationSession struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // pending|running|waiting|completed|failed|cancelled
	WaitDetail  string `json:"wait_detail,omitempty"`
	WaitPayload string `json:"wait_payload,omitempty"` // PR URL while waiting
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
}

// gitResult mirrors the fields the playground needs from orchestrator.GitResult
// — the PR info returned by register-cluster and enable-addon-v4.
type gitResult struct {
	PRUrl  string `json:"pr_url,omitempty"`
	PRID   int    `json:"pr_id,omitempty"`
	Branch string `json:"branch,omitempty"`
	Merged bool   `json:"merged"`
}

// decodePossiblyWrapped decodes body into out (a pointer to a struct that
// embeds gitResult), then checks gr — the pointer to that embedded field —
// for any PR info. If the flat decode found none (PRID==0, Merged==false,
// PRUrl==""), it retries assuming the response is the attribution-fallback
// wrapper shape internal/api/tiered_git.go's withAttributionWarning produces:
// {"result": <out-shaped payload>, "attribution_warning": "..."}. Handlers
// on the withAttributionWarning list (addon_ops_v4 is NOT one of them, but
// catalog_org and others are) emit that wrapper whenever the caller's git
// token resolved to the fallback service token instead of a personal one —
// which is every request the playground's admin login makes. Without this,
// a wrapped response silently decodes PRID/Merged/PRUrl as zero values and
// callers wrongly conclude there is no PR.
//
// The second decode is best-effort: if it fails or still finds no PR info,
// out is left with whatever the flat decode produced, and the caller's own
// "no PR info" check reports the failure.
func decodePossiblyWrapped(body []byte, out interface{}, gr *gitResult) error {
	if err := json.Unmarshal(body, out); err != nil {
		return err
	}
	if gr.PRID != 0 || gr.Merged || gr.PRUrl != "" {
		return nil
	}
	wrapper := struct {
		Result interface{} `json:"result"`
	}{Result: out}
	_ = json.Unmarshal(body, &wrapper)
	return nil
}

// doJSON performs an authenticated JSON request against the Sharko API.
// If out is non-nil and the response has a body, the body is decoded into it.
// Always returns the raw status code and body so callers can report server
// error text verbatim.
func (c *apiClient) doJSON(method, path string, reqBody interface{}, out interface{}) (int, []byte, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal %s %s request: %w", method, path, err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("create %s %s request: %w", method, path, err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, respBody, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return resp.StatusCode, respBody, nil
}

// initRepo calls POST /api/v1/init — the real seed-bootstrap door. autoMerge
// is always passed explicitly (never relies on the connection's default) so
// the caller controls whether Sharko merges the seed PR itself or leaves it
// for the playground to merge "like a human" via the Gitea REST API.
func (c *apiClient) initRepo(autoMerge bool) (operationID, status, waitPayload string, err error) {
	reqBody := map[string]interface{}{
		"bootstrap_argocd": true,
		"auto_merge":       autoMerge,
	}
	statusCode, body, err := c.doJSON("POST", "/api/v1/init", reqBody, nil)
	if err != nil {
		return "", "", "", err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusAccepted {
		return "", "", "", fmt.Errorf("POST /api/v1/init: status %d: %s", statusCode, string(body))
	}
	var resp struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
		WaitPayload string `json:"wait_payload,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", "", fmt.Errorf("decode init response: %w", err)
	}
	return resp.OperationID, resp.Status, resp.WaitPayload, nil
}

// getOperation polls GET /api/v1/operations/{id}.
func (c *apiClient) getOperation(id string) (*operationSession, error) {
	statusCode, body, err := c.doJSON("GET", "/api/v1/operations/"+id, nil, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/v1/operations/%s: status %d: %s", id, statusCode, string(body))
	}
	var sess operationSession
	if err := json.Unmarshal(body, &sess); err != nil {
		return nil, fmt.Errorf("decode operation session: %w", err)
	}
	return &sess, nil
}

// heartbeatOperation calls POST /api/v1/operations/{id}/heartbeat so a
// "waiting" init session isn't abandoned while the playground is off merging
// the PR through Gitea's own API.
func (c *apiClient) heartbeatOperation(id string) error {
	statusCode, body, err := c.doJSON("POST", "/api/v1/operations/"+id+"/heartbeat", nil, nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("POST /api/v1/operations/%s/heartbeat: status %d: %s", id, statusCode, string(body))
	}
	return nil
}

// registerClusterV4 registers a cluster via POST /api/v1/clusters with an
// explicit auto_merge decision, returning the PR info so the caller can
// merge it itself. No addons are passed here — on a v4 repo addon
// assignment happens separately via enableAddonV4.
func (c *apiClient) registerClusterV4(name, kubeconfig string, autoMerge bool) (*gitResult, error) {
	reqBody := map[string]interface{}{
		"name":       name,
		"kubeconfig": kubeconfig,
		"auto_merge": autoMerge,
	}
	statusCode, body, err := c.doJSON("POST", "/api/v1/clusters", reqBody, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated && statusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("POST /api/v1/clusters: status %d: %s", statusCode, string(body))
	}
	var resp struct {
		Status  string     `json:"status"`
		Error   string     `json:"error,omitempty"`
		Message string     `json:"message,omitempty"`
		Git     *gitResult `json:"git,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode register response: %w", err)
	}
	if resp.Git == nil {
		return nil, fmt.Errorf("POST /api/v1/clusters: no Git PR info in response (status=%s message=%s error=%s)", resp.Status, resp.Message, resp.Error)
	}
	return resp.Git, nil
}

// catalogAddResult mirrors the fields the playground needs from
// orchestrator.AddToCatalogResult — POST /api/v1/catalog/addons embeds
// *orchestrator.GitResult anonymously, so its JSON fields (pr_url, pr_id,
// branch, merged) sit at the top level right alongside "added"; gitResult
// already carries the matching json tags, so embedding it here decodes the
// same flattened shape.
type catalogAddResult struct {
	gitResult
	Added []string `json:"added"`
}

// addToCatalog adds one addon to the org's catalog via POST
// /api/v1/catalog/addons with an explicit auto_merge decision, returning
// the PR info so the caller can merge it itself. from_marketplace copies
// the chart location and default namespace out of Sharko's curated
// (Marketplace) list for this name; version is always given explicitly —
// the curated list deliberately ships no version (design doc
// 2026-07-31-catalog-approved-model.md §5), so a version baked into a
// signed artefact never goes stale.
func (c *apiClient) addToCatalog(addonName, version string, autoMerge bool) (*catalogAddResult, error) {
	reqBody := map[string]interface{}{
		"addons": []map[string]interface{}{
			{
				"name":             addonName,
				"from_marketplace": true,
				"version":          version,
			},
		},
		"auto_merge": autoMerge,
	}
	statusCode, body, err := c.doJSON("POST", "/api/v1/catalog/addons", reqBody, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("POST /api/v1/catalog/addons: status %d: %s", statusCode, string(body))
	}
	var res catalogAddResult
	if err := decodePossiblyWrapped(body, &res, &res.gitResult); err != nil {
		return nil, fmt.Errorf("decode add-to-catalog response: %w", err)
	}
	return &res, nil
}

// enableAddonV4 enables an addon via POST /api/v1/v4/clusters/{cluster}/addons/{addon}
// with an explicit auto_merge decision, returning the PR info.
func (c *apiClient) enableAddonV4(cluster, addon string, autoMerge bool) (*gitResult, error) {
	reqBody := map[string]interface{}{
		"yes":        true,
		"auto_merge": autoMerge,
	}
	path := "/api/v1/v4/clusters/" + cluster + "/addons/" + addon
	statusCode, body, err := c.doJSON("POST", path, reqBody, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("POST %s: status %d: %s", path, statusCode, string(body))
	}
	var gr gitResult
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, fmt.Errorf("decode enable-addon response: %w", err)
	}
	return &gr, nil
}

// clusterExists checks GET /api/v1/clusters/{name} — used to detect when the
// cluster reconciler has picked up a merged registration.
func (c *apiClient) clusterExists(name string) (bool, error) {
	statusCode, body, err := c.doJSON("GET", "/api/v1/clusters/"+name, nil, nil)
	if err != nil {
		return false, err
	}
	switch statusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GET /api/v1/clusters/%s: status %d: %s", name, statusCode, string(body))
	}
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
