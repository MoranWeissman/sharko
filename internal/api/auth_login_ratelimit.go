package api

// auth_login_ratelimit.go — the named handler behind POST /api/v1/auth/login.
//
// This is deliberately a method and not the func literal it used to be. A
// route registered with an anonymous function has no name, and every table
// that says who may call an endpoint, whether it is audited, and which tier it
// belongs to is keyed by handler name. B16's route guard refuses an anonymous
// mutating route for exactly that reason: an endpoint nothing can name is an
// endpoint nothing can classify.

import "net/http"

// handleLoginRateLimited applies the per-IP login rate limit and then runs the
// real login handler. The limit is 5 attempts per IP per minute; a caller over
// it gets 429 without the credential ever being checked.
//
// Audit and authz classification stay on handleLogin, which this delegates to:
// handleLogin emits login / login_failed itself and is the auth entry point,
// so both allowlists name this wrapper for the same written reasons.
func (s *Server) handleLoginRateLimited(w http.ResponseWriter, r *http.Request) {
	if s.loginRL != nil && !s.loginRL.Allow(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, please try again later")
		return
	}
	s.handleLogin(w, r)
}
