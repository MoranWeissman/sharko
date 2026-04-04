package api

import (
	"encoding/json"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

func (s *Server) handleListAddonSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.addonSecretDefs)
}

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

	s.addonSecretDefs[def.AddonName] = def
	writeJSON(w, http.StatusCreated, def)
}

func (s *Server) handleDeleteAddonSecret(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	addon := r.PathValue("addon")
	if addon == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	if _, ok := s.addonSecretDefs[addon]; !ok {
		writeError(w, http.StatusNotFound, "no secret definition for addon: "+addon)
		return
	}
	delete(s.addonSecretDefs, addon)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "addon": addon})
}
