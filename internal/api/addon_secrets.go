package api

import (
	"encoding/json"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// handleListAddonSecrets godoc
//
// @Summary List addon secret definitions
// @Description Returns all registered addon secret definitions
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Addon secret definitions"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /addon-secrets [get]
func (s *Server) handleListAddonSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.addonSecretDefsMu.RLock()
	defer s.addonSecretDefsMu.RUnlock()
	// Copy the map to avoid holding the lock during JSON encoding.
	defs := make(map[string]orchestrator.AddonSecretDefinition, len(s.addonSecretDefs))
	for k, v := range s.addonSecretDefs {
		defs[k] = v
	}
	writeJSON(w, http.StatusOK, defs)
}

// handleCreateAddonSecret godoc
//
// @Summary Create addon secret definition
// @Description Registers a new addon secret definition for remote cluster secret propagation
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddonSecretDefinition true "Addon secret definition"
// @Success 201 {object} map[string]interface{} "Secret definition created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /addon-secrets [post]
func (s *Server) handleCreateAddonSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	var def orchestrator.AddonSecretDefinition
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if def.AddonName == "" || def.SecretName == "" || def.Namespace == "" || len(def.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "addon_name, secret_name, namespace, and keys are required")
		return
	}

	s.addonSecretDefsMu.Lock()
	s.addonSecretDefs[def.AddonName] = def
	s.addonSecretDefsMu.Unlock()

	writeJSON(w, http.StatusCreated, def)
}

// handleDeleteAddonSecret godoc
//
// @Summary Delete addon secret definition
// @Description Removes the secret definition for a specific addon
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param addon path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Secret definition deleted"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Not found"
// @Router /addon-secrets/{addon} [delete]
func (s *Server) handleDeleteAddonSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	addon := r.PathValue("addon")
	if addon == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	s.addonSecretDefsMu.Lock()
	if _, ok := s.addonSecretDefs[addon]; !ok {
		s.addonSecretDefsMu.Unlock()
		writeError(w, http.StatusNotFound, "no secret definition for addon: "+addon)
		return
	}
	delete(s.addonSecretDefs, addon)
	s.addonSecretDefsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "addon": addon})
}
