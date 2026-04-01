package gitprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
)

// ---------- Read operations ----------

// TestConnection verifies that the configured repository is accessible.
func (a *AzureDevOpsProvider) TestConnection(_ context.Context) error {
	apiURL := a.baseURL + "?api-version=7.1"
	resp, _, err := a.doGet(apiURL)
	if err != nil {
		return fmt.Errorf("test connection: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("test connection: unexpected status %d", resp.StatusCode)
	}
	slog.Info("azure devops connection ok", "org", a.organisation, "project", a.project, "repo", a.repository)
	return nil
}

// GetFileContent retrieves the raw content of a single file at the given ref.
func (a *AzureDevOpsProvider) GetFileContent(_ context.Context, filePath, ref string) ([]byte, error) {
	// Use includeContent=true to get raw file content
	apiURL := fmt.Sprintf("%s/items?path=%s&versionDescriptor.version=%s&includeContent=true&api-version=7.1",
		a.baseURL, url.QueryEscape(filePath), url.QueryEscape(ref))

	resp, body, err := a.doGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("get file content: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get file content: unexpected status %d", resp.StatusCode)
	}

	// Azure DevOps returns JSON with a "content" field when using includeContent=true
	var item struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &item); err == nil && item.Content != "" {
		slog.Info("azure devops file fetched", "path", filePath, "ref", ref, "size", len(item.Content))
		return []byte(item.Content), nil
	}

	// Debug: log first 200 chars of the response to understand the format
	preview := string(body)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	slog.Info("azure devops file response debug", "path", filePath, "size", len(body), "preview", preview)

	return body, nil
}

// ListDirectory returns the names of entries in a directory at the given ref.
func (a *AzureDevOpsProvider) ListDirectory(_ context.Context, dirPath, ref string) ([]string, error) {
	apiURL := fmt.Sprintf("%s/items?scopePath=%s&recursionLevel=OneLevel&versionDescriptor.version=%s&api-version=7.1",
		a.baseURL, url.QueryEscape(dirPath), url.QueryEscape(ref))

	resp, body, err := a.doGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list directory: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Value []struct {
			Path     string `json:"path"`
			IsFolder bool   `json:"isFolder"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("list directory: decode response: %w", err)
	}

	names := make([]string, 0, len(result.Value))
	for _, item := range result.Value {
		// The first entry is the directory itself; skip it.
		if item.Path == dirPath || item.Path == dirPath+"/" {
			continue
		}
		names = append(names, path.Base(item.Path))
	}
	return names, nil
}

// ListPullRequests returns pull requests filtered by state ("open", "closed", or "all").
func (a *AzureDevOpsProvider) ListPullRequests(_ context.Context, state string) ([]PullRequest, error) {
	// Map generic state to Azure DevOps status values.
	adoStatus := "all"
	switch strings.ToLower(state) {
	case "open":
		adoStatus = "active"
	case "closed":
		adoStatus = "completed"
	case "all":
		adoStatus = "all"
	}

	apiURL := fmt.Sprintf("%s/pullrequests?searchCriteria.status=%s&api-version=7.1",
		a.baseURL, adoStatus)

	resp, body, err := a.doGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list pull requests: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Value []struct {
			PullRequestID int    `json:"pullRequestId"`
			Title         string `json:"title"`
			Description   string `json:"description"`
			Status        string `json:"status"`
			CreatedBy     struct {
				DisplayName string `json:"displayName"`
			} `json:"createdBy"`
			SourceRefName string `json:"sourceRefName"`
			TargetRefName string `json:"targetRefName"`
			CreationDate  string `json:"creationDate"`
			ClosedDate    string `json:"closedDate"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("list pull requests: decode response: %w", err)
	}

	prs := make([]PullRequest, 0, len(result.Value))
	for _, p := range result.Value {
		pr := PullRequest{
			ID:           p.PullRequestID,
			Title:        p.Title,
			Description:  p.Description,
			Author:       p.CreatedBy.DisplayName,
			SourceBranch: strings.TrimPrefix(p.SourceRefName, "refs/heads/"),
			TargetBranch: strings.TrimPrefix(p.TargetRefName, "refs/heads/"),
			URL: fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
				a.organisation, a.project, a.repository, p.PullRequestID),
			CreatedAt: p.CreationDate,
			ClosedAt:  p.ClosedDate,
		}

		switch p.Status {
		case "active":
			pr.Status = "open"
		case "completed":
			pr.Status = "merged"
		case "abandoned":
			pr.Status = "closed"
		default:
			pr.Status = p.Status
		}

		prs = append(prs, pr)
	}

	slog.Info("azure devops pull requests listed", "state", state, "count", len(prs))
	return prs, nil
}

// ---------- Write operations ----------

// getRefSHA resolves a branch name to its current object ID.
func (a *AzureDevOpsProvider) getRefSHA(branchName string) (string, error) {
	apiURL := fmt.Sprintf("%s/refs?filter=heads/%s&api-version=7.1",
		a.baseURL, url.QueryEscape(branchName))

	resp, body, err := a.doGet(apiURL)
	if err != nil {
		return "", fmt.Errorf("get ref SHA: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("get ref SHA: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Value []struct {
			ObjectID string `json:"objectId"`
			Name     string `json:"name"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("get ref SHA: decode: %w", err)
	}
	if len(result.Value) == 0 {
		return "", fmt.Errorf("get ref SHA: branch %q not found", branchName)
	}
	return result.Value[0].ObjectID, nil
}

// fileExists checks whether a file exists at the given path and ref.
func (a *AzureDevOpsProvider) fileExists(filePath, ref string) bool {
	apiURL := fmt.Sprintf("%s/items?path=%s&versionDescriptor.version=%s&api-version=7.1",
		a.baseURL, url.QueryEscape(filePath), url.QueryEscape(ref))
	resp, _, err := a.doGet(apiURL)
	if err != nil {
		return false
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// CreateBranch creates a new branch from the given source ref.
func (a *AzureDevOpsProvider) CreateBranch(_ context.Context, branchName, fromRef string) error {
	sourceSHA, err := a.getRefSHA(fromRef)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	payload, _ := json.Marshal([]map[string]string{
		{
			"name":        "refs/heads/" + branchName,
			"oldObjectId": "0000000000000000000000000000000000000000",
			"newObjectId": sourceSHA,
		},
	})

	apiURL := a.baseURL + "/refs?api-version=7.1"
	resp, _, err := a.doPost(apiURL, payload)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create branch: unexpected status %d", resp.StatusCode)
	}

	slog.Info("azure devops branch created", "branch", branchName, "from", fromRef)
	return nil
}

// CreateOrUpdateFile creates a new file or updates an existing one on the given branch.
func (a *AzureDevOpsProvider) CreateOrUpdateFile(_ context.Context, filePath string, content []byte, branch, commitMessage string) error {
	currentSHA, err := a.getRefSHA(branch)
	if err != nil {
		return fmt.Errorf("create or update file: %w", err)
	}

	changeType := "add"
	if a.fileExists(filePath, branch) {
		changeType = "edit"
	}

	encoded := base64.StdEncoding.EncodeToString(content)

	payload, _ := json.Marshal(map[string]interface{}{
		"refUpdates": []map[string]string{
			{
				"name":        "refs/heads/" + branch,
				"oldObjectId": currentSHA,
			},
		},
		"commits": []map[string]interface{}{
			{
				"comment": commitMessage,
				"changes": []map[string]interface{}{
					{
						"changeType": changeType,
						"item":       map[string]string{"path": "/" + strings.TrimPrefix(filePath, "/")},
						"newContent": map[string]string{
							"content":     encoded,
							"contentType": "base64encoded",
						},
					},
				},
			},
		},
	})

	apiURL := a.baseURL + "/pushes?api-version=7.1"
	resp, _, err := a.doPost(apiURL, payload)
	if err != nil {
		return fmt.Errorf("create or update file: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create or update file: unexpected status %d", resp.StatusCode)
	}

	slog.Info("azure devops file written", "path", filePath, "branch", branch, "changeType", changeType)
	return nil
}

// DeleteFile removes a file from the given branch.
func (a *AzureDevOpsProvider) DeleteFile(_ context.Context, filePath, branch, commitMessage string) error {
	currentSHA, err := a.getRefSHA(branch)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"refUpdates": []map[string]string{
			{
				"name":        "refs/heads/" + branch,
				"oldObjectId": currentSHA,
			},
		},
		"commits": []map[string]interface{}{
			{
				"comment": commitMessage,
				"changes": []map[string]interface{}{
					{
						"changeType": "delete",
						"item":       map[string]string{"path": "/" + strings.TrimPrefix(filePath, "/")},
					},
				},
			},
		},
	})

	apiURL := a.baseURL + "/pushes?api-version=7.1"
	resp, _, err := a.doPost(apiURL, payload)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete file: unexpected status %d", resp.StatusCode)
	}

	slog.Info("azure devops file deleted", "path", filePath, "branch", branch)
	return nil
}

// CreatePullRequest opens a new pull request.
func (a *AzureDevOpsProvider) CreatePullRequest(_ context.Context, title, body, head, base string) (*PullRequest, error) {
	payload, _ := json.Marshal(map[string]string{
		"sourceRefName": "refs/heads/" + head,
		"targetRefName": "refs/heads/" + base,
		"title":         title,
		"description":   body,
	})

	apiURL := a.baseURL + "/pullrequests?api-version=7.1"
	resp, respBody, err := a.doPost(apiURL, payload)
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("create pull request: unexpected status %d", resp.StatusCode)
	}

	var adoPR struct {
		PullRequestID int    `json:"pullRequestId"`
		Title         string `json:"title"`
		Description   string `json:"description"`
		Status        string `json:"status"`
		CreatedBy     struct {
			DisplayName string `json:"displayName"`
		} `json:"createdBy"`
		SourceRefName string `json:"sourceRefName"`
		TargetRefName string `json:"targetRefName"`
		CreationDate  string `json:"creationDate"`
	}
	if err := json.Unmarshal(respBody, &adoPR); err != nil {
		return nil, fmt.Errorf("create pull request: decode response: %w", err)
	}

	pr := &PullRequest{
		ID:           adoPR.PullRequestID,
		Title:        adoPR.Title,
		Description:  adoPR.Description,
		Author:       adoPR.CreatedBy.DisplayName,
		Status:       "open",
		SourceBranch: strings.TrimPrefix(adoPR.SourceRefName, "refs/heads/"),
		TargetBranch: strings.TrimPrefix(adoPR.TargetRefName, "refs/heads/"),
		URL: fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
			a.organisation, a.project, a.repository, adoPR.PullRequestID),
		CreatedAt: adoPR.CreationDate,
	}

	slog.Info("azure devops pull request created", "id", pr.ID, "url", pr.URL)
	return pr, nil
}

// MergePullRequest approves and completes (merges) a pull request in Azure DevOps.
// Azure DevOps requires approval before completion, then a PATCH with status=completed.
func (a *AzureDevOpsProvider) MergePullRequest(ctx context.Context, prNumber int) error {
	prURL := fmt.Sprintf("%s/pullrequests/%d?api-version=7.1", a.baseURL, prNumber)

	// Step 1: GET PR to retrieve lastMergeSourceCommit and reviewers info
	_, getBody, err := a.doGet(prURL)
	if err != nil {
		return fmt.Errorf("getting pull request #%d: %w", prNumber, err)
	}

	var prData struct {
		LastMergeSourceCommit struct {
			CommitID string `json:"commitId"`
		} `json:"lastMergeSourceCommit"`
		CreatedBy struct {
			ID string `json:"id"`
		} `json:"createdBy"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(getBody, &prData); err != nil {
		return fmt.Errorf("parsing pull request #%d: %w", prNumber, err)
	}

	// Already merged
	if prData.Status == "completed" {
		slog.Info("azure devops pull request already completed", "number", prNumber)
		return nil
	}

	// Step 2: Auto-approve the PR (vote: 10 = approved)
	if prData.CreatedBy.ID != "" {
		voteURL := fmt.Sprintf("%s/pullrequests/%d/reviewers/%s?api-version=7.1", a.baseURL, prNumber, prData.CreatedBy.ID)
		voteBody, _ := json.Marshal(map[string]interface{}{
			"vote": 10, // 10 = approved
		})
		_, _, voteErr := a.doPatch(voteURL, voteBody) // best-effort, some policies may block self-approve
		if voteErr != nil {
			slog.Warn("azure devops: auto-approve failed (may need manual approval)", "pr", prNumber, "error", voteErr)
		} else {
			slog.Info("azure devops pull request auto-approved", "number", prNumber)
		}
	}

	// Step 3: PATCH to complete (merge) the PR, bypassing branch policies
	// The bypass is needed because automated migrations may not satisfy all
	// branch policies (required reviewers, build validation, etc.)
	patchBody, _ := json.Marshal(map[string]interface{}{
		"status": "completed",
		"lastMergeSourceCommit": map[string]string{
			"commitId": prData.LastMergeSourceCommit.CommitID,
		},
		"completionOptions": map[string]interface{}{
			"deleteSourceBranch":    true,
			"mergeStrategy":         "squash",
			"bypassPolicy":         true,
			"bypassReason":         "Automated migration via ArgoCD Addons Platform",
		},
	})

	resp, respBody, err := a.doPatch(prURL, patchBody)
	if err != nil {
		return fmt.Errorf("merge pull request #%d: %w", prNumber, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("merge pull request #%d failed (status %d): %s", prNumber, resp.StatusCode, string(respBody))
	}

	slog.Info("azure devops pull request merged", "number", prNumber)
	return nil
}

// DeleteBranch removes a branch in Azure DevOps.
func (a *AzureDevOpsProvider) DeleteBranch(ctx context.Context, branchName string) error {
	// Azure DevOps deletes branches via refs endpoint with all-zero newObjectId
	return fmt.Errorf("azure devops: DeleteBranch not yet implemented")
}
