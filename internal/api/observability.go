package api

import (
	"net/http"
)

func (s *Server) handleGetObservabilityOverview(w http.ResponseWriter, r *http.Request) {
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		// Git provider is optional for observability — resource alerts will be skipped
		gp = nil
	}

	resp, err := s.observabilitySvc.GetOverview(r.Context(), ac, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
