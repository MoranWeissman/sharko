package api

// withLegacyOpenAuthForTests flips the package-private test seam that
// reproduces the historical "zero users = open router" behavior for unit
// tests that drive handlers through the full router without a login dance.
//
// This file is the ONLY place allowed to set Server.authDisabledForTests
// (it lives in a _test.go file, so the setter does not exist in production
// builds). Production behavior is fail-closed: zero users at request time
// means 401 — see basicAuthMiddleware and TestAuth_FailClosed_ZeroUsers.
//
// The seam is live-checked: the moment a test adds a user (AddUser /
// AddDemoUser), HasUsers() flips true and auth is enforced again, so the
// package's auth tests (session expiry, token lifecycle, audit-stream
// auth) still exercise the real 401 paths.
func withLegacyOpenAuthForTests(s *Server) *Server {
	s.authDisabledForTests = true
	return s
}
