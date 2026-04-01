package gitprovider

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// AzureDevOpsProvider implements GitProvider for Azure DevOps repositories.
type AzureDevOpsProvider struct {
	client       *http.Client
	organisation string
	project      string
	repository   string
	pat          string
	baseURL      string
}

// NewAzureDevOpsProvider creates a new Azure DevOps-backed GitProvider.
// The token is used as a personal access token for authentication.
func NewAzureDevOpsProvider(organisation, project, repository, token string) *AzureDevOpsProvider {
	return &AzureDevOpsProvider{
		client:       &http.Client{},
		organisation: organisation,
		project:      project,
		repository:   repository,
		pat:          token,
		baseURL:      fmt.Sprintf("https://dev.azure.com/%s/%s/_apis/git/repositories/%s", organisation, project, repository),
	}
}

// authHeader returns the Basic auth header value for Azure DevOps PAT authentication.
func (a *AzureDevOpsProvider) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+a.pat))
}

// doGet performs an authenticated GET request and returns the response body.
func (a *AzureDevOpsProvider) doGet(url string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, body, nil
}

// doPost performs an authenticated POST request with a JSON body.
func (a *AzureDevOpsProvider) doPost(url string, jsonBody []byte) (*http.Response, []byte, error) {
	return a.doRequest(http.MethodPost, url, jsonBody)
}

// doPatch performs an authenticated PATCH request with a JSON body.
func (a *AzureDevOpsProvider) doPatch(url string, jsonBody []byte) (*http.Response, []byte, error) {
	return a.doRequest(http.MethodPatch, url, jsonBody)
}

func (a *AzureDevOpsProvider) doRequest(method, url string, jsonBody []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, body, nil
}
