package api

// route_registry.go — the one place a route can come into existence, and the
// thing the permission, audit and tier guards enumerate (B16).
//
// # What went wrong before this file
//
// TestAuthzCoverage, TestAuditCoverage and TestTierCoverage all found the
// server's routes by parsing the package source for a literal
//
//	mux.HandleFunc("POST /api/v1/...", srv.handleSomething)
//
// call. That reads one SPELLING of registering a route, and a route has more
// than one spelling. A mutating endpoint registered through a one-line helper
//
//	registerPost(mux, "/api/v1/rc-evil2", srv.handleEvil)
//
// is a real, live, reachable endpoint that all three guards were blind to — no
// permission check, no audit entry, no tier, and three green tests saying the
// class was closed.
//
// # The fix, said plainly
//
// The guards no longer read source. They ask the router what it registered.
//
// Every route goes through routeMux, which records the pattern and the handler
// as it hands them to the real http.ServeMux. Recording happens at the moment
// of registration, so it does not matter whether the call was written inline,
// reached through a helper, made in a loop, or handed a handler out of a
// variable — if the route is served, it was recorded, and if it was not
// recorded it is not served. There is nothing left for a guard to be one
// spelling behind on.
//
// # Two structural rules keep it that way
//
//  1. routeMux is the only ServeMux this package builds. Its HandleFunc and
//     Handle SHADOW the embedded ones, so `mux.HandleFunc(...)` — all 177
//     existing calls, unchanged — now records. Reaching the unrecorded mux
//     means writing `mux.ServeMux.HandleFunc(...)` on purpose, and
//     route_registry_guard_test.go fails on both that spelling and on any
//     second http.NewServeMux in the package's shipping files.
//
//  2. A mutating route must be registered with a NAMED function. An anonymous
//     func literal has no name for a permission/audit/tier table to key on, so
//     the guard rejects it outright rather than letting it become an
//     unclassifiable route. That is why the rate-limited login route is a
//     method (handleLoginRateLimited) rather than the closure it used to be.

import (
	"net/http"
	"reflect"
	"runtime"
	"strings"
)

// RegisteredRoute is one route as the router actually registered it.
type RegisteredRoute struct {
	// Pattern is the full Go 1.22 ServeMux pattern, e.g.
	// "POST /api/v1/clusters/{name}/addons/{addon}".
	Pattern string
	// Method is the HTTP method token when the pattern carries one, else "".
	Method string
	// HandlerName is the unqualified Go function name the route resolves to
	// at RUNTIME — "handleRegisterCluster" for srv.handleRegisterCluster.
	// Empty when the handler is an anonymous func literal, which is what the
	// guard refuses for a mutating route.
	HandlerName string
	// Anonymous is true when the handler had no usable name.
	Anonymous bool
}

// routeMux wraps the real ServeMux and records every registration.
//
// It embeds *http.ServeMux so it is still an http.Handler and still answers
// every method the router needs; HandleFunc and Handle below shadow the
// embedded versions so registration cannot happen without being recorded.
type routeMux struct {
	*http.ServeMux
	routes []RegisteredRoute
}

func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux()}
}

// HandleFunc records the route, then registers it.
func (m *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.record(pattern, handler)
	m.ServeMux.HandleFunc(pattern, handler)
}

// Handle records the route, then registers it. Used for the swagger UI and
// the metrics endpoint, which are http.Handler values rather than funcs.
func (m *routeMux) Handle(pattern string, handler http.Handler) {
	m.record(pattern, handler)
	m.ServeMux.Handle(pattern, handler)
}

func (m *routeMux) record(pattern string, handler any) {
	name := handlerFuncName(handler)
	method := ""
	if fields := strings.Fields(pattern); len(fields) >= 2 && isHTTPMethodToken(fields[0]) {
		method = fields[0]
	}
	m.routes = append(m.routes, RegisteredRoute{
		Pattern:     pattern,
		Method:      method,
		HandlerName: name,
		Anonymous:   name == "",
	})
}

// isHTTPMethodToken reports whether the first field of a mux pattern is an
// HTTP method rather than the start of a path. ServeMux patterns are either
// "METHOD /path" or "/path"; a method token is all-uppercase ASCII letters.
func isHTTPMethodToken(tok string) bool {
	if tok == "" {
		return false
	}
	for _, r := range tok {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// handlerFuncName resolves a registered handler to the unqualified name of the
// Go function it actually is, using the runtime's own function table rather
// than the shape of the source that produced it.
//
// A method value written srv.handleRegisterCluster compiles to a wrapper whose
// runtime name is
//
//	github.com/MoranWeissman/sharko/internal/api.(*Server).handleRegisterCluster-fm
//
// which reduces to "handleRegisterCluster". An anonymous func literal reduces
// to something like "func3" — no name a table can be keyed on — and is
// reported as "" so the guard can refuse it.
func handlerFuncName(handler any) string {
	v := reflect.ValueOf(handler)
	if v.Kind() != reflect.Func {
		// An http.Handler value (the swagger UI, the metrics handler). These
		// are never mutating routes; they have no handler function to name.
		return ""
	}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return ""
	}
	name := strings.TrimSuffix(fn.Name(), "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	// A closure reduces to "funcN" / "funcN.1"; that is not a handler name.
	if strings.HasPrefix(name, "func") {
		return ""
	}
	return name
}

// routeInventory builds a throwaway router and returns every route it
// registered. This is what the coverage guards enumerate.
//
// It runs the real registration path — the same registerRoutes the server
// runs — against a zero Server. Nothing in registration dereferences a Server
// field: the handlers are taken as method VALUES (which only needs the
// pointer, never its contents) and are never called here.
func routeInventory() []RegisteredRoute {
	mux := newRouteMux()
	registerRoutes(&Server{}, mux, nil)
	return mux.routes
}
