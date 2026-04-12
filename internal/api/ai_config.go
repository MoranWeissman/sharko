package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/authz"
)

type aiProviderInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Model      string `json:"model"`
}

type aiConfigResponse struct {
	CurrentProvider    string           `json:"current_provider"`
	AvailableProviders []aiProviderInfo `json:"available_providers"`
}

// handleGetAIConfig godoc
//
// @Summary Get AI configuration
// @Description Returns the current AI provider configuration and available providers
// @Tags ai
// @Produce json
// @Security BearerAuth
// @Success 200 {object} aiConfigResponse "AI configuration"
// @Router /ai/config [get]
func (s *Server) handleGetAIConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.aiClient.GetConfig()

	providers := []aiProviderInfo{
		{
			ID:         "ollama",
			Name:       "Ollama (Local)",
			Configured: cfg.OllamaURL != "",
			Model:      cfg.OllamaModel,
		},
		{
			ID:         "claude",
			Name:       "Claude (Anthropic)",
			Configured: cfg.APIKey != "" && cfg.Provider == ai.ProviderClaude,
			Model:      cloudModelForProvider(cfg, ai.ProviderClaude),
		},
		{
			ID:         "openai",
			Name:       "OpenAI",
			Configured: cfg.APIKey != "" && cfg.Provider == ai.ProviderOpenAI,
			Model:      cloudModelForProvider(cfg, ai.ProviderOpenAI),
		},
		{
			ID:         "gemini",
			Name:       "Gemini (Google)",
			Configured: cfg.APIKey != "" && cfg.Provider == ai.ProviderGemini,
			Model:      cloudModelForProvider(cfg, ai.ProviderGemini),
		},
	}

	// If an API key is set, mark all cloud providers that could use it as configured
	if cfg.APIKey != "" {
		for i := range providers {
			if providers[i].ID != "ollama" {
				providers[i].Configured = true
				if providers[i].Model == "" {
					providers[i].Model = cfg.CloudModel
				}
			}
		}
	}

	resp := aiConfigResponse{
		CurrentProvider:    string(cfg.Provider),
		AvailableProviders: providers,
	}

	writeJSON(w, http.StatusOK, resp)
}

func cloudModelForProvider(cfg ai.Config, p ai.Provider) string {
	if cfg.Provider == p {
		return cfg.CloudModel
	}
	return cfg.CloudModel
}

type saveAIConfigRequest struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key,omitempty"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	OllamaURL string `json:"ollama_url,omitempty"`
}

// handleSaveAIConfig godoc
//
// @Summary Save AI configuration
// @Description Saves and applies new AI provider configuration (admin only)
// @Tags ai
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body saveAIConfigRequest true "AI config to save"
// @Success 200 {object} map[string]interface{} "Configuration saved"
// @Failure 400 {object} map[string]interface{} "Bad request or unsupported provider"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /ai/config [post]
func (s *Server) handleSaveAIConfig(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "ai.config") { return }
	var req saveAIConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider := ai.Provider(req.Provider)
	switch provider {
	case ai.ProviderOllama, ai.ProviderClaude, ai.ProviderOpenAI, ai.ProviderGemini, ai.ProviderCustomOpenAI, ai.ProviderNone:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported provider: %s", req.Provider))
		return
	}

	// Build new config preserving non-UI fields from current config
	current := s.aiClient.GetConfig()
	newCfg := ai.Config{
		Provider:      provider,
		APIKey:        req.APIKey,
		CloudModel:    req.Model,
		BaseURL:       req.BaseURL,
		OllamaURL:     req.OllamaURL,
		OllamaModel:   req.Model, // for ollama, model goes here
		AuthHeader:    current.AuthHeader,
		AuthPrefix:    current.AuthPrefix,
		MaxIterations: current.MaxIterations,
		GitOpsEnabled: current.GitOpsEnabled,
		AgentModel:    current.AgentModel,
	}
	if provider == ai.ProviderOllama {
		if newCfg.OllamaURL == "" {
			newCfg.OllamaURL = "http://localhost:11434"
		}
		if newCfg.OllamaModel == "" {
			newCfg.OllamaModel = "llama3.2"
		}
	}

	s.aiClient.SetConfig(newCfg)

	// Persist to K8s Secret if store is available
	if s.aiConfigStore != nil {
		cfgJSON, _ := json.Marshal(newCfg)
		if err := s.aiConfigStore.SaveJSON(cfgJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist AI config: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": req.Provider})
}

// handleTestAIConfig godoc
//
// @Summary Test AI configuration
// @Description Tests an AI provider configuration without saving it
// @Tags ai
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body saveAIConfigRequest true "AI config to test"
// @Success 200 {object} map[string]interface{} "Test result with status and response"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /ai/test-config [post]
func (s *Server) handleTestAIConfig(w http.ResponseWriter, r *http.Request) {
	var req saveAIConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Create temporary client with the provided config
	testCfg := ai.Config{
		Provider:   ai.Provider(req.Provider),
		APIKey:     req.APIKey,
		CloudModel: req.Model,
		BaseURL:    req.BaseURL,
		OllamaURL:  req.OllamaURL,
		OllamaModel: req.Model,
	}
	if testCfg.Provider == ai.ProviderOllama && testCfg.OllamaURL == "" {
		testCfg.OllamaURL = "http://localhost:11434"
	}

	testClient := ai.NewClient(testCfg)
	result, err := testClient.Summarize(r.Context(), "Say 'AI connection successful' in one short sentence.")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"response": result,
	})
}

// handleSetAIProvider godoc
//
// @Summary Set AI provider
// @Description Switches the active AI provider without changing other configuration (admin only)
// @Tags ai
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Provider selection with provider name"
// @Success 200 {object} map[string]interface{} "Provider set"
// @Failure 400 {object} map[string]interface{} "Bad request or unsupported provider"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /ai/provider [post]
func (s *Server) handleSetAIProvider(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "ai.provider") {
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate provider
	switch ai.Provider(req.Provider) {
	case ai.ProviderOllama, ai.ProviderClaude, ai.ProviderOpenAI, ai.ProviderGemini, ai.ProviderNone:
		// valid
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported provider: %s", req.Provider))
		return
	}

	s.aiClient.SetProvider(ai.Provider(req.Provider))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": req.Provider})
}

// handleTestAI godoc
//
// @Summary Test active AI connection
// @Description Tests the currently active AI provider by sending a simple prompt
// @Tags ai
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Test result with status and response"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "AI not configured"
// @Router /ai/test [post]
func (s *Server) handleTestAI(w http.ResponseWriter, r *http.Request) {
	if !s.aiClient.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "AI not configured")
		return
	}

	result, err := s.aiClient.Summarize(r.Context(), "Say 'AI connection successful' in one short sentence.")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "response": result})
}
