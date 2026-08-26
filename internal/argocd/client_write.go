package argocd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ErrNoOperationInProgress is the sentinel TerminateOperation wraps in when
// ArgoCD refuses the terminate because there is nothing to terminate.
//
// # Why this sentinel has to be minted here and nowhere else
//
// Two callers — the restart-sync button and auto-remediation — need to tell
// this one refusal apart from every other, because it is harmless: the
// operation finished on its own between Sharko looking and Sharko acting, and
// the right response is to carry on and sync. Both used to work it out by
// lowercasing the error and searching it for the words "no operation is in
// progress". That is exactly the kind of match this project bans: it is
// reading ArgoCD's prose to decide what Sharko does, and it stops working the
// day ArgoCD rephrases itself.
//
// # How it is decided without reading a single word
//
// By the call and the status code. Sharko sent DELETE to one application's
// /operation endpoint with a path it built itself, and ArgoCD answered 400.
// ArgoCD's terminate handler has exactly one thing it calls a bad request —
// being asked to terminate when no operation is running. So the fact is
// carried by the shape of the exchange, not by the reply, and the reply can be
// thrown away without losing it.
//
// If ArgoCD ever grows a second reason to answer 400 here, the cost is that
// Sharko carries on to the sync instead of stopping — which is what it did
// before this sentinel existed, and the sync then reports its own failure in
// its own words.
var ErrNoOperationInProgress = errors.New("ArgoCD had no sync operation to terminate on this application")

// TerminateOperation cancels the in-flight sync operation for the named ArgoCD
// application. It is a no-op when no operation is active (ArgoCD returns 200
// with no body in that case). Use this before re-syncing an application that is
// permanently failing due to a stale operation snapshot.
//
// A 400 comes back wrapped in ErrNoOperationInProgress — see that sentinel for
// why the status code alone is the whole answer.
func (c *Client) TerminateOperation(ctx context.Context, appName string) error {
	path := "/api/v1/applications/" + url.PathEscape(appName) + "/operation"
	_, err := c.doDelete(ctx, path)
	if err != nil {
		var refused *WriteRefusedError
		if errors.As(err, &refused) && refused.Status == http.StatusBadRequest {
			return fmt.Errorf("terminating operation for %q: %w: %w", appName, ErrNoOperationInProgress, err)
		}
		return fmt.Errorf("terminating operation for %q: %w", appName, err)
	}
	return nil
}

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
		// Address-free for the same reason as the read path (BF12).
		return nil, fmt.Errorf("the %s call to %s could not be built (%s)",
			http.MethodPost, path, credsafe.PlainFailureReason(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, unreachableCallError(http.MethodPost, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, writeCallError(http.MethodPost, path, resp.StatusCode)
	}

	return body, nil
}

// doPut performs an authenticated PUT request and returns the response body.
func (c *Client) doPut(ctx context.Context, path string, payload []byte) ([]byte, error) {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(payload))
	if err != nil {
		// Address-free for the same reason as the read path (BF12).
		return nil, fmt.Errorf("the %s call to %s could not be built (%s)",
			http.MethodPut, path, credsafe.PlainFailureReason(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, unreachableCallError(http.MethodPut, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, writeCallError(http.MethodPut, path, resp.StatusCode)
	}

	return body, nil
}

// doDelete performs an authenticated DELETE request and returns the response body.
func (c *Client) doDelete(ctx context.Context, path string) ([]byte, error) {
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		// Address-free for the same reason as the read path (BF12).
		return nil, fmt.Errorf("the %s call to %s could not be built (%s)",
			http.MethodDelete, path, credsafe.PlainFailureReason(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, unreachableCallError(http.MethodDelete, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, writeCallError(http.MethodDelete, path, resp.StatusCode)
	}

	return body, nil
}

// WriteRefusalCode is the stable, machine-readable class of a refused ArgoCD
// write. It is written here in Go source by a programmer; no value from
// ArgoCD, from a repository, or from a credentials backend can ever put text
// into one. That is what makes it safe to show a person and safe to branch on.
type WriteRefusalCode string

const (
	// WriteRefusalTokenInvalid is HTTP 401 — the credential Sharko presented
	// was not accepted.
	WriteRefusalTokenInvalid WriteRefusalCode = "argocd_token_invalid"
	// WriteRefusalPermissionDenied is HTTP 403 — the credential is fine and
	// this account may not do this.
	WriteRefusalPermissionDenied WriteRefusalCode = "argocd_permission_denied"
	// WriteRefusalNotFound is HTTP 404 — ArgoCD has no such object or no such
	// endpoint.
	WriteRefusalNotFound WriteRefusalCode = "argocd_not_found"
	// WriteRefusalRejected is any other 4xx — ArgoCD understood the call and
	// would not do it.
	WriteRefusalRejected WriteRefusalCode = "argocd_rejected"
	// WriteRefusalUpstreamFailure is 5xx, or any other status outside 2xx —
	// something broke on ArgoCD's side.
	WriteRefusalUpstreamFailure WriteRefusalCode = "argocd_upstream_failure"
)

// WriteRefusedError is the ONE error every failed ArgoCD write call returns,
// and it is the boundary ArgoCD's own reply does not cross.
//
// # Every field is Sharko's own
//
// Verb is the HTTP method Sharko chose. Endpoint is the path Sharko itself
// built out of a cluster name, an application name or a fixed string. Status
// is the number ArgoCD answered with, and Code is one of the constants above,
// which are spelled out in this file. There is deliberately NO field for the
// response body, and no option that puts one back: a caller must not be able
// to switch this off.
//
// # Why the body is gone rather than filtered
//
// ArgoCD quotes whatever it was working on inside its error payloads, and for
// a repository that includes the access token in the address —
// https://x-access-token:<token>@host/org/repo.git. Scanning that payload for
// things that look like secrets is the idea this project has refused
// everywhere else and refuses here: it fails on the first shape nobody thought
// of. So the payload is read off the wire, used to close the connection
// cleanly, and dropped.
//
// # What a caller can still find out
//
// Which call failed and how — the verb, the endpoint and the status — plus a
// stable code to branch on. errors.Is against ErrTokenInvalid and
// ErrPermissionDenied keeps working, so every caller that already told "not
// allowed" apart from "ArgoCD is broken" still can.
type WriteRefusedError struct {
	// Verb is the HTTP method Sharko used.
	Verb string
	// Endpoint is the ArgoCD API path Sharko built for the call.
	Endpoint string
	// Status is the HTTP status ArgoCD answered with.
	Status int
	// Code is the stable class of the refusal.
	Code WriteRefusalCode
}

// Error returns credsafe's fixed sentence followed by Sharko's own facts, in
// the same key=value shape credsafe.SafeOperationDetail uses so the two read
// alike. Nothing here comes from ArgoCD's reply.
func (e *WriteRefusedError) Error() string {
	return fmt.Sprintf("%s (code=%s call=%s %s status=%d)",
		credsafe.ArgocdWriteRefusedMessage, e.Code, e.Verb, e.Endpoint, e.Status)
}

// Is lets errors.Is match the two sentinels the read path has always returned,
// so a 401 or a 403 on a write is indistinguishable from one on a read as far
// as every existing caller is concerned. It compares the CODE, never any text.
func (e *WriteRefusedError) Is(target error) bool {
	switch target {
	case ErrTokenInvalid:
		return e.Code == WriteRefusalTokenInvalid
	case ErrPermissionDenied:
		return e.Code == WriteRefusalPermissionDenied
	}
	return false
}

// writeRefusalCodeFor classifies a status code. Status codes are integers, so
// this is classification by value out of a closed set — there is no text to
// read and no message to match.
func writeRefusalCodeFor(status int) WriteRefusalCode {
	switch {
	case status == http.StatusUnauthorized:
		return WriteRefusalTokenInvalid
	case status == http.StatusForbidden:
		return WriteRefusalPermissionDenied
	case status == http.StatusNotFound:
		return WriteRefusalNotFound
	case status >= 400 && status < 500:
		return WriteRefusalRejected
	default:
		return WriteRefusalUpstreamFailure
	}
}

// writeCallError turns a non-2xx answer to a POST/PUT/DELETE into the error
// callers see.
//
// It takes no body parameter, on purpose. The three do* functions above read
// the response body so the connection can be reused, and none of them may hand
// it to this function: there is no parameter to hand it to.
func writeCallError(verb, path string, status int) error {
	slog.Error("argocd write call failed", "verb", verb, "endpoint", path, "status", status)
	return &WriteRefusedError{
		Verb:     verb,
		Endpoint: path,
		Status:   status,
		Code:     writeRefusalCodeFor(status),
	}
}

// SafeWriteFailure is the sentence a boundary may show a person for a failed
// ArgoCD write.
//
// Two outcomes, decided by TYPE:
//
//   - the chain carries a *WriteRefusedError — ArgoCD answered, and the
//     sentence carries Sharko's own facts about which call was refused;
//   - it does not — Sharko never got an answer (a refused dial, a DNS failure,
//     a timeout, a TLS handshake that would not verify), and the sentence says
//     that instead.
//
// It never reads the words of the error it was given, so a transport error
// quoting the address it failed on cannot travel through it.
func SafeWriteFailure(err error) string {
	var refused *WriteRefusedError
	if errors.As(err, &refused) {
		return refused.Error()
	}
	return credsafe.ArgocdWriteUnreachableMessage
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

// escapeServerURL encodes an ArgoCD server URL for use as a path segment.
// Go's url.PathEscape leaves ':' unescaped (it is an allowed path-segment
// character per RFC 3986), but ArgoCD's gRPC-gateway path matcher requires
// colons escaped. url.QueryEscape encodes the full RFC 3986 reserved set
// including ':', producing the "https%3A%2F%2F..." form that ArgoCD expects.
func escapeServerURL(serverURL string) string {
	return url.QueryEscape(serverURL)
}

// DeleteCluster removes a cluster from ArgoCD by server URL.
func (c *Client) DeleteCluster(ctx context.Context, serverURL string) error {
	path := "/api/v1/clusters/" + escapeServerURL(serverURL)
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
	getPath := "/api/v1/clusters/" + escapeServerURL(serverURL)
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

	putPath := "/api/v1/clusters/" + escapeServerURL(serverURL) + "?updateMask=metadata.labels"
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

// AddRepository registers a Git repository in ArgoCD so it can be used as a source.
func (c *Client) AddRepository(ctx context.Context, repoURL, username, password string) error {
	body := map[string]interface{}{
		"repo":     repoURL,
		"username": username,
		"password": password,
		"type":     "git",
		"upsert":   true,
	}
	jsonData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling repository request: %w", err)
	}
	_, err = c.doPost(ctx, "/api/v1/repositories", jsonData)
	if err != nil {
		return fmt.Errorf("adding repository %q to ArgoCD: %w", repoURL, err)
	}
	return nil
}
