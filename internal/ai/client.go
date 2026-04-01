package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider identifies which AI backend to use.
type Provider string

const (
	ProviderOllama Provider = "ollama"
	ProviderClaude Provider = "claude"
	ProviderOpenAI Provider = "openai"
	ProviderGemini       Provider = "gemini"
	ProviderCustomOpenAI Provider = "custom-openai"
	ProviderNone         Provider = "none"
)

// Config holds AI provider settings.
type Config struct {
	Provider    Provider `yaml:"provider"`
	OllamaURL   string   `yaml:"ollama_url"`
	OllamaModel string   `yaml:"ollama_model"`
	AgentModel  string   `yaml:"agent_model"`  // Separate model for agent (tool calling needs larger model)
	APIKey        string   `yaml:"api_key"`         // API key for Claude/OpenAI
	CloudModel    string   `yaml:"cloud_model"`     // Model name for cloud providers (e.g., "claude-sonnet-4-20250514", "gpt-4o")
	BaseURL       string   `yaml:"base_url"`        // Base URL for custom OpenAI-compatible providers
	AuthHeader    string   `yaml:"auth_header"`     // Custom auth header name (default: "Authorization")
	AuthPrefix    string   `yaml:"auth_prefix"`     // Custom auth value prefix (default: "Bearer " when using Authorization header)
	MaxIterations int      `yaml:"max_iterations"`  // Max agent tool-calling iterations (default: 8)
	GitOpsEnabled bool     `yaml:"gitops_enabled"`  // Enable write tools (enable_addon, disable_addon, etc.)
}

// GetAgentModel returns the model to use for agent conversations.
// Cloud providers always use CloudModel. Ollama uses AgentModel or falls back to OllamaModel.
func (c Config) GetAgentModel() string {
	if c.Provider == ProviderClaude || c.Provider == ProviderOpenAI || c.Provider == ProviderGemini || c.Provider == ProviderCustomOpenAI {
		if c.CloudModel != "" {
			return c.CloudModel
		}
		return "gemini-2.5-flash" // sensible default
	}
	// Ollama: prefer AgentModel, fall back to OllamaModel
	if c.AgentModel != "" {
		return c.AgentModel
	}
	return c.OllamaModel
}

// Client wraps communication with an AI provider.
type Client struct {
	config Config
	http   *http.Client
}

// NewClient creates a new AI client with the given configuration.
func NewClient(cfg Config) *Client {
	return &Client{config: cfg, http: &http.Client{}}
}

// GetConfig returns the current AI configuration.
func (c *Client) GetConfig() Config {
	return c.config
}

// SetProvider switches the active AI provider at runtime.
func (c *Client) SetProvider(provider Provider) {
	c.config.Provider = provider
}

// SetConfig replaces the full AI configuration at runtime.
func (c *Client) SetConfig(cfg Config) {
	c.config = cfg
}

// GetProviderName returns the current provider as a string.
func (c *Client) GetProviderName() string {
	return string(c.config.Provider)
}

// IsEnabled returns true when a valid AI provider is configured.
func (c *Client) IsEnabled() bool {
	return c.config.Provider != ProviderNone && c.config.Provider != ""
}

// Summarize sends a prompt to the LLM and returns the response text.
func (c *Client) Summarize(ctx context.Context, prompt string) (string, error) {
	if !c.IsEnabled() {
		return "", fmt.Errorf("AI provider not configured")
	}

	switch c.config.Provider {
	case ProviderOllama:
		return c.ollamaSummarize(ctx, prompt)
	case ProviderClaude:
		return c.claudeSummarize(ctx, prompt)
	case ProviderOpenAI:
		return c.openaiSummarize(ctx, prompt)
	case ProviderGemini:
		return c.geminiSummarize(ctx, prompt)
	case ProviderCustomOpenAI:
		return c.customOpenAISummarize(ctx, prompt)
	default:
		return "", fmt.Errorf("unsupported AI provider: %s", c.config.Provider)
	}
}

func (c *Client) ollamaSummarize(ctx context.Context, prompt string) (string, error) {
	url := c.config.OllamaURL + "/api/generate"

	body, _ := json.Marshal(map[string]interface{}{
		"model":  c.config.OllamaModel,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.3,
			"num_predict": 1500,
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing ollama response: %w", err)
	}

	return result.Response, nil
}

func (c *Client) claudeSummarize(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model":      c.config.CloudModel,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling claude request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("claude returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing claude response: %w", err)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", fmt.Errorf("empty response from Claude")
}

func (c *Client) openaiSummarize(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model":      c.config.CloudModel,
		"max_tokens": 4096,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing openai response: %w", err)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("empty response from OpenAI")
}

func (c *Client) geminiSummarize(ctx context.Context, prompt string) (string, error) {
	model := c.config.CloudModel
	if model == "" {
		model = "gemini-2.5-flash"
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)

	body, err := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]interface{}{
			"temperature":    0.3,
			"maxOutputTokens": 4096,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling gemini request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.config.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing gemini response: %w", err)
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("empty response from Gemini")
}

// customOpenAIAuthHeader returns the header name and value for custom OpenAI-compatible auth.
func (c *Client) customOpenAIAuthHeader() (string, string) {
	header := c.config.AuthHeader
	if header == "" {
		header = "Authorization"
	}
	prefix := c.config.AuthPrefix
	if prefix == "" && (header == "Authorization" || c.config.AuthHeader == "") {
		prefix = "Bearer "
	}
	return header, prefix + c.config.APIKey
}

func (c *Client) customOpenAISummarize(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model":      c.config.CloudModel,
		"max_tokens": 4096,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling custom-openai request: %w", err)
	}

	apiURL := fmt.Sprintf("%s/v2/%s/chat/completions",
		strings.TrimRight(c.config.BaseURL, "/"), c.config.CloudModel)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader, authValue := c.customOpenAIAuthHeader()
	req.Header.Set(authHeader, authValue)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("custom-openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("custom-openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing custom-openai response: %w", err)
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("empty response from custom OpenAI provider")
}

// ChatWithTools sends messages to the LLM with tool definitions and returns the response.
// It dispatches to the correct provider based on the current configuration.
func (c *Client) ChatWithTools(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	switch c.config.Provider {
	case ProviderOllama:
		return c.chatOllama(ctx, messages, tools)
	case ProviderClaude:
		return c.chatClaude(ctx, messages, tools)
	case ProviderOpenAI:
		return c.chatOpenAI(ctx, messages, tools)
	case ProviderGemini:
		return c.chatGemini(ctx, messages, tools)
	case ProviderCustomOpenAI:
		return c.chatCustomOpenAI(ctx, messages, tools)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", c.config.Provider)
	}
}

func (c *Client) chatOllama(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	reqBody := ollamaChatRequest{
		Model:    c.config.GetAgentModel(),
		Messages: messages,
		Tools:    tools,
		Stream:   false,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"num_predict": 600,
		},
	}

	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.OllamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing ollama chat response: %w", err)
	}

	return &ChatResponse{
		Content:   result.Message.Content,
		ToolCalls: result.Message.ToolCalls,
	}, nil
}

func (c *Client) chatClaude(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	claudeTools := convertToolsToClaude(tools)
	claudeMessages := convertMessagesToClaude(messages)

	// Extract system prompt from messages (first system message)
	sysPrompt := ""
	for _, m := range messages {
		if m.Role == "system" {
			sysPrompt = m.Content
			break
		}
	}

	reqBody := map[string]interface{}{
		"model":      c.config.GetAgentModel(),
		"max_tokens": 4096,
		"system":     sysPrompt,
		"messages":   claudeMessages,
		"tools":      claudeTools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling claude chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("claude returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing claude chat response: %w", err)
	}

	chatResp := &ChatResponse{}
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			chatResp.Content += block.Text
		case "tool_use":
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      block.Name,
					Arguments: block.Input,
				},
			})
		}
	}

	return chatResp, nil
}

func (c *Client) chatOpenAI(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	openaiTools := convertToolsToOpenAI(tools)
	openaiMessages := convertMessagesToOpenAI(messages)

	reqBody := map[string]interface{}{
		"model":    c.config.GetAgentModel(),
		"messages": openaiMessages,
		"tools":    openaiTools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling openai chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing openai chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty response from OpenAI")
	}

	choice := result.Choices[0]
	chatResp := &ChatResponse{Content: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: ToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			},
		})
	}

	return chatResp, nil
}

func (c *Client) chatGemini(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	geminiTools := convertToolsToGemini(tools)
	geminiContents := convertMessagesToGemini(messages)

	// Extract system prompt from messages
	sysPrompt := ""
	for _, m := range messages {
		if m.Role == "system" {
			sysPrompt = m.Content
			break
		}
	}

	model := c.config.GetAgentModel()
	if model == "" {
		model = "gemini-2.5-flash"
	}

	reqBody := map[string]interface{}{
		"contents": geminiContents,
		"tools": []map[string]interface{}{
			{"function_declarations": geminiTools},
		},
		"system_instruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": sysPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.3,
			"maxOutputTokens": 4096,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling gemini chat request: %w", err)
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.config.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text,omitempty"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall,omitempty"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing gemini chat response: %w", err)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	chatResp := &ChatResponse{}
	for _, part := range result.Candidates[0].Content.Parts {
		if part.FunctionCall != nil {
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:   part.FunctionCall.Name,
				Type: "function",
				Function: ToolCallFunc{
					Name:      part.FunctionCall.Name,
					Arguments: part.FunctionCall.Args,
				},
			})
		} else if part.Text != "" {
			chatResp.Content += part.Text
		}
	}

	return chatResp, nil
}

func (c *Client) chatCustomOpenAI(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (*ChatResponse, error) {
	openaiTools := convertToolsToOpenAI(tools)
	openaiMessages := convertMessagesToOpenAI(messages)

	model := c.config.GetAgentModel()

	reqBody := map[string]interface{}{
		"model":    model,
		"messages": openaiMessages,
		"tools":    openaiTools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling custom-openai chat request: %w", err)
	}

	apiURL := fmt.Sprintf("%s/v2/%s/chat/completions",
		strings.TrimRight(c.config.BaseURL, "/"), model)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader, authValue := c.customOpenAIAuthHeader()
	req.Header.Set(authHeader, authValue)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom-openai chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("custom-openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing custom-openai chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty response from custom OpenAI provider")
	}

	choice := result.Choices[0]
	chatResp := &ChatResponse{Content: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: ToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			},
		})
	}

	return chatResp, nil
}

// BuildUpgradePrompt creates a concise prompt for analyzing an upgrade.
// Keeps prompt small for faster LLM response.
func BuildUpgradePrompt(addonName, currentVersion, targetVersion string, added, removed, changed int, changedDetails string, conflicts string, releaseNotes string) string {
	// Limit changed details to top 15 entries to keep prompt short
	lines := strings.Split(changedDetails, "\n")
	if len(lines) > 15 {
		lines = append(lines[:15], fmt.Sprintf("... and %d more changes", len(lines)-15))
	}
	trimmedChanges := strings.Join(lines, "\n")

	prompt := fmt.Sprintf(`Helm chart upgrade analysis. Be brief.

%s: v%s → v%s
+%d added, -%d removed, ~%d changed

Key changes:
%s`, addonName, currentVersion, targetVersion, added, removed, changed, trimmedChanges)

	if conflicts != "" {
		prompt += "\n\nConflicts with our config:\n" + conflicts
	}

	if releaseNotes != "" {
		// Truncate release notes for the prompt to keep it manageable
		rn := releaseNotes
		if len(rn) > 1500 {
			rn = rn[:1500] + "\n... (truncated)"
		}
		prompt += "\n\nRelease notes for target version:\n" + rn
	}

	prompt += `

Provide a detailed analysis with these sections. Use markdown formatting with headers, bullet points, and bold text:

## Summary
Explain what changed in this upgrade in 3-5 sentences. Mention specific components affected (agents, operators, CRDs, etc.) and the nature of the changes (version bumps, feature flags, behavioral changes).

## What's New
List the most important new features or changes as bullet points. Be specific about what each change means in practice.

## Risk Assessment
State the risk level (Low / Medium / High) and explain why in 2-3 sentences. Consider: are there breaking changes? Do default behaviors change? Are any features removed?

## Action Items
List specific steps to take before upgrading as a numbered list. Include:
- Which configuration values to review
- Any deprecated features to migrate away from
- Testing recommendations

## Impact on Your Configuration
Based on the conflicts detected (if any), explain which of your custom values may be affected and what to do about them.`

	return prompt
}
