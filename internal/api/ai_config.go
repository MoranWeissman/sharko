package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/audit"
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

	// AnnotateOnSeed is the global Settings toggle for "Annotate values
	// on generate". When the AI provider is `none` the field is
	// reported as false regardless of the persisted value — the UI uses
	// this to gate the toggle row visibility.
	//
	// Default-true semantics: on a fresh install with a configured AI
	// provider but no explicit value (legacy config blobs), the field
	// reports `true`. The Save handler stamps the explicit value on
	// every persist so subsequent reads are authoritative.
	AnnotateOnSeed bool `json:"annotate_on_seed"`

	// HasAPIKey says whether a key is saved for the current provider. The
	// key itself is never sent back. The edit form uses this to say "a key
	// is saved, leave the field empty to keep it".
	HasAPIKey bool `json:"has_api_key"`
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

	// Only the provider that is in use is "configured", and only it has a
	// model. The list used to mark every cloud provider configured as soon
	// as any key was saved, show one provider's model on all of them, and
	// call Ollama configured on an install with no AI at all.
	names := []struct {
		id   ai.Provider
		name string
	}{
		{ai.ProviderOllama, "Ollama (Local)"},
		{ai.ProviderClaude, "Claude (Anthropic)"},
		{ai.ProviderOpenAI, "OpenAI"},
		{ai.ProviderGemini, "Gemini (Google)"},
		{ai.ProviderCustomOpenAI, "Custom OpenAI-compatible"},
	}
	providers := make([]aiProviderInfo, 0, len(names))
	for _, n := range names {
		info := aiProviderInfo{ID: string(n.id), Name: n.name}
		if cfg.Provider == n.id {
			switch n.id {
			case ai.ProviderOllama:
				info.Configured = cfg.OllamaURL != ""
				info.Model = cfg.OllamaModel
			case ai.ProviderCustomOpenAI:
				info.Configured = cfg.BaseURL != ""
				info.Model = cfg.CloudModel
			default:
				info.Configured = cfg.APIKey != ""
				info.Model = cfg.CloudModel
			}
		}
		providers = append(providers, info)
	}

	// AnnotateOnSeed default-true: when AI is configured but the
	// persisted blob doesn't have an explicit value, report `true`.
	// Defer to ai.Client.AnnotateOnSeedEnabled so the same default rule
	// lives in one place.
	annotate := s.aiClient.AnnotateOnSeedEnabled()

	resp := aiConfigResponse{
		CurrentProvider:    string(cfg.Provider),
		AvailableProviders: providers,
		AnnotateOnSeed:     annotate,
		HasAPIKey:          s.aiClient.IsEnabled() && cfg.APIKey != "",
	}

	writeJSON(w, http.StatusOK, resp)
}

type saveAIConfigRequest struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key,omitempty"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	OllamaURL string `json:"ollama_url,omitempty"`

	// AnnotateOnSeed is a tri-state via the *bool sentinel:
	//   nil  → caller didn't pass the field; preserve the current value
	//          (or fall through to default-true if no current value exists).
	//   *false → explicit OFF.
	//   *true  → explicit ON.
	// Once any save sets the field, AnnotateOnSeedSet flips to true and
	// the explicit value is authoritative on subsequent reads.
	AnnotateOnSeed *bool `json:"annotate_on_seed,omitempty"`
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
	if !authz.RequireWithResponse(w, r, "ai.config") {
		return
	}
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
	// A part that is left out is kept, as long as the provider stays the
	// same. See keepSavedAIParts.
	req = keepSavedAIParts(req, current)
	if msg := checkAIAddresses(provider, req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
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
		// Preserve the existing annotate flag unless the request
		// explicitly overrides it. First save without an explicit flag
		// stamps the default-true so the persisted blob is authoritative
		// going forward.
		AnnotateOnSeed:    current.AnnotateOnSeed,
		AnnotateOnSeedSet: current.AnnotateOnSeedSet,
	}
	if req.AnnotateOnSeed != nil {
		newCfg.AnnotateOnSeed = *req.AnnotateOnSeed
		newCfg.AnnotateOnSeedSet = true
	} else if !newCfg.AnnotateOnSeedSet && provider != ai.ProviderNone {
		// First save with an enabled provider and no explicit flag —
		// stamp the default so subsequent reads are authoritative.
		newCfg.AnnotateOnSeed = true
		newCfg.AnnotateOnSeedSet = true
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
		cfgJSON, _ := json.Marshal(newCfg) // #nosec G117 -- this JSON goes only into AIConfigStore.SaveJSON, which AES-encrypts it into the sharko-ai-config K8s Secret; persisting the API key is the store's purpose and it never reaches a log or response
		if err := s.aiConfigStore.SaveJSON(cfgJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist AI config: "+err.Error())
			return
		}
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:  "ai_config_updated",
		Detail: fmt.Sprintf("provider=%s", req.Provider),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": req.Provider})
}

// keepSavedAIParts fills the parts a save or test request left out from what
// is saved, when the request is for the SAME provider that is in use.
//
// Without this, any request that named the provider and nothing else erased
// the saved key and model. The "Annotate values on generate" switch sends
// exactly that, so one click on the switch deleted the key. The edit form had
// the same problem: changing only the model sent an empty key.
//
// A request for a DIFFERENT provider gets nothing carried over: a key for one
// provider is not a key for another. To remove a saved key, turn AI off
// (provider "none").
func keepSavedAIParts(req saveAIConfigRequest, saved ai.Config) saveAIConfigRequest {
	if req.Provider == "" || req.Provider == string(ai.ProviderNone) || req.Provider != string(saved.Provider) {
		return req
	}
	if req.APIKey == "" {
		req.APIKey = saved.APIKey
	}
	if req.Model == "" {
		if saved.Provider == ai.ProviderOllama {
			req.Model = saved.OllamaModel
		} else {
			req.Model = saved.CloudModel
		}
	}
	if req.BaseURL == "" {
		req.BaseURL = saved.BaseURL
	}
	if req.OllamaURL == "" {
		req.OllamaURL = saved.OllamaURL
	}
	return req
}

// checkAIAddresses checks the address a request carries for a self-hosted
// provider against the rule in internal/ai/address.go. It returns the
// sentence to show, or "" when the address is fine or there is none.
func checkAIAddresses(provider ai.Provider, req saveAIConfigRequest) string {
	switch provider {
	case ai.ProviderOllama:
		if req.OllamaURL != "" {
			if err := ai.ValidateProviderAddress(req.OllamaURL); err != nil {
				return err.Error()
			}
		}
	case ai.ProviderCustomOpenAI:
		if req.BaseURL == "" {
			return AICustomNeedsAddressSentence
		}
		if err := ai.ValidateProviderAddress(req.BaseURL); err != nil {
			return err.Error()
		}
	}
	return ""
}

// Sentences the AI test and save calls answer with.
const (
	// AIEnterKeyFirstSentence is the answer to a test of a cloud provider
	// with no key typed and none saved. Sharko used to send the call anyway
	// and show the provider's refusal.
	AIEnterKeyFirstSentence = "Enter an API key first."
	// AICustomNeedsAddressSentence is the answer when the custom provider is
	// picked with no address.
	AICustomNeedsAddressSentence = "Enter the address of the provider first."
	// AIKeyHasSpaceSentence is the answer when the typed key has a space or
	// a line break inside. The old message blamed the address.
	AIKeyHasSpaceSentence = "The API key has a space or a line break in it. Paste the key again, on one line."
	// AIKeyTooLongSentence is the answer when the typed key is far longer
	// than any real key.
	AIKeyTooLongSentence = "The API key is too long to be a real key. Paste the key again."
)

// maxAIKeyLen is far above any real provider key.
const maxAIKeyLen = 1024

// checkAIKey checks the shape of a typed key. It returns the sentence to show,
// or "".
func checkAIKey(key string) string {
	if len(key) > maxAIKeyLen {
		return AIKeyTooLongSentence
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return AIKeyHasSpaceSentence
	}
	return ""
}

// handleTestAIConfig godoc
//
// @Summary Test AI configuration
// @Description Tests an AI provider configuration without saving it (admin only). A part that is left out is taken from the saved configuration when the provider is the one in use, so "change only the model" can be tested without typing the key again. The answer of the provider is never passed on word for word.
// @Tags ai
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body saveAIConfigRequest true "AI config to test"
// @Success 200 {object} map[string]interface{} "Test result with status and response"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /ai/test-config [post]
func (s *Server) handleTestAIConfig(w http.ResponseWriter, r *http.Request) {
	// Admin only. This call makes Sharko connect to an address the caller
	// names, so it sits with saving the AI settings, not with reading them.
	// It used to have no role check at all: a viewer could use it to find
	// out what answers on the cluster network.
	if !authz.RequireWithResponse(w, r, "ai.test-config") {
		return
	}
	var req saveAIConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider := ai.Provider(req.Provider)
	switch provider {
	case ai.ProviderOllama, ai.ProviderClaude, ai.ProviderOpenAI, ai.ProviderGemini, ai.ProviderCustomOpenAI:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported provider: %s", req.Provider))
		return
	}

	req = keepSavedAIParts(req, s.aiClient.GetConfig())

	// Refusals come back in the same shape as a failed test, so the form
	// shows them next to the Test button.
	refuse := func(msg string) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "error", "message": msg})
	}
	if msg := checkAIAddresses(provider, req); msg != "" {
		refuse(msg)
		return
	}
	if msg := checkAIKey(req.APIKey); msg != "" {
		refuse(msg)
		return
	}
	switch provider {
	case ai.ProviderClaude, ai.ProviderOpenAI, ai.ProviderGemini:
		if req.APIKey == "" {
			refuse(AIEnterKeyFirstSentence)
			return
		}
	}

	// Create temporary client with the provided config
	testCfg := ai.Config{
		Provider:    ai.Provider(req.Provider),
		APIKey:      req.APIKey,
		CloudModel:  req.Model,
		BaseURL:     req.BaseURL,
		OllamaURL:   req.OllamaURL,
		OllamaModel: req.Model,
	}
	if testCfg.Provider == ai.ProviderOllama && testCfg.OllamaURL == "" {
		testCfg.OllamaURL = "http://localhost:11434"
	}

	testClient := ai.NewClient(testCfg)
	// A test is a test: the audit log says so, and says which provider.
	// It used to be written down as "Ai created".
	audit.Enrich(r.Context(), audit.Fields{
		Event:   "ai_config_tested",
		Detail:  fmt.Sprintf("provider=%s", req.Provider),
		Changes: audit.ChangesNotApplicable,
	})
	result, err := testClient.Summarize(r.Context(), "Say 'AI connection successful' in one short sentence.")
	if err != nil {
		audit.Enrich(r.Context(), audit.Fields{Result: "failure"})
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
	case ai.ProviderOllama, ai.ProviderClaude, ai.ProviderOpenAI, ai.ProviderGemini, ai.ProviderCustomOpenAI, ai.ProviderNone:
		// valid
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported provider: %s", req.Provider))
		return
	}

	s.aiClient.SetProvider(ai.Provider(req.Provider))
	audit.Enrich(r.Context(), audit.Fields{
		Event:  "ai_config_updated",
		Detail: fmt.Sprintf("provider=%s", req.Provider),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": req.Provider})
}

// handleTestAI godoc
//
// @Summary Test active AI connection
// @Description Tests the currently active AI provider by sending a simple prompt (admin only)
// @Tags ai
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Test result with status and response"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "AI not configured"
// @Router /ai/test [post]
func (s *Server) handleTestAI(w http.ResponseWriter, r *http.Request) {
	// Admin only: this call spends the saved key, and it belongs to the
	// AI settings page, which only an admin can change.
	if !authz.RequireWithResponse(w, r, "ai.test") {
		return
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:   "ai_tested",
		Detail:  fmt.Sprintf("provider=%s", s.aiClient.GetProviderName()),
		Changes: audit.ChangesNotApplicable,
	})
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
