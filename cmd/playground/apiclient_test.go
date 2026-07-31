package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAddToCatalog_FlatResponse covers the shape a handler that does NOT
// wrap via internal/api/tiered_git.go's withAttributionWarning returns:
// the catalogAddResult fields sit at the top level.
func TestAddToCatalog_FlatResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"pr_url":"https://gitea.local/org/repo/pulls/7","pr_id":7,"branch":"catalog/add-foo","merged":false,"added":["foo"]}`))
	}))
	defer srv.Close()

	c := &apiClient{baseURL: srv.URL, token: "t"}
	res, err := c.addToCatalog("foo", "1.2.3", false)
	if err != nil {
		t.Fatalf("addToCatalog: %v", err)
	}
	if res.PRID != 7 {
		t.Errorf("PRID = %d, want 7", res.PRID)
	}
	if res.PRUrl != "https://gitea.local/org/repo/pulls/7" {
		t.Errorf("PRUrl = %q, want the gitea PR URL", res.PRUrl)
	}
	if res.Merged {
		t.Errorf("Merged = true, want false")
	}
	if len(res.Added) != 1 || res.Added[0] != "foo" {
		t.Errorf("Added = %v, want [foo]", res.Added)
	}
}

// TestAddToCatalog_AttributionWrappedResponse covers the shape
// internal/api/catalog_org.go's handleAddToCatalog actually returns whenever
// the caller has no personal git token — always true for the playground's
// admin login (internal/api/tiered_git.go withAttributionWarning wraps the
// payload as {"result": {...}, "attribution_warning": "..."}). Before this
// fix, addToCatalog decoded the body flat and PRID came back 0, which made
// the playground's add-to-catalog step fail with "no PR id returned and
// add-to-catalog was not merged" even though the PR existed.
func TestAddToCatalog_AttributionWrappedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"result":{"pr_url":"https://gitea.local/org/repo/pulls/9","pr_id":9,"branch":"catalog/add-bar","merged":false,"added":["bar"]},"attribution_warning":"no_per_user_pat"}`))
	}))
	defer srv.Close()

	c := &apiClient{baseURL: srv.URL, token: "t"}
	res, err := c.addToCatalog("bar", "4.5.6", false)
	if err != nil {
		t.Fatalf("addToCatalog: %v", err)
	}
	if res.PRID != 9 {
		t.Errorf("PRID = %d, want 9 (attribution-fallback wrapper should have been unwrapped)", res.PRID)
	}
	if res.PRUrl != "https://gitea.local/org/repo/pulls/9" {
		t.Errorf("PRUrl = %q, want the gitea PR URL", res.PRUrl)
	}
	if len(res.Added) != 1 || res.Added[0] != "bar" {
		t.Errorf("Added = %v, want [bar]", res.Added)
	}
}

// TestAddToCatalog_MergedNoPRID covers the auto_merge=true case: no PR
// info at all (merged immediately), just merged:true. Both shapes should
// decode this the same way, and the flat decode must not be clobbered by a
// failed/empty wrapped retry.
func TestAddToCatalog_MergedNoPRID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"merged":true,"added":["baz"]}`))
	}))
	defer srv.Close()

	c := &apiClient{baseURL: srv.URL, token: "t"}
	res, err := c.addToCatalog("baz", "1.0.0", true)
	if err != nil {
		t.Fatalf("addToCatalog: %v", err)
	}
	if !res.Merged {
		t.Errorf("Merged = false, want true")
	}
	if res.PRID != 0 {
		t.Errorf("PRID = %d, want 0", res.PRID)
	}
}

// TestDecodePossiblyWrapped_WrappedFallsBackCleanly checks the helper
// directly: when the flat decode finds no PR info AND the wrapped decode
// also fails (malformed body), out keeps whatever the flat decode produced
// instead of erroring — callers rely on this to surface their own
// "no PR info" error message rather than a decode error.
func TestDecodePossiblyWrapped_WrappedFallsBackCleanly(t *testing.T) {
	body := []byte(`{"added":["nope"]}`) // no result wrapper, no PR fields
	var res catalogAddResult
	if err := decodePossiblyWrapped(body, &res, &res.gitResult); err != nil {
		t.Fatalf("decodePossiblyWrapped: %v", err)
	}
	if res.PRID != 0 || res.Merged || res.PRUrl != "" {
		t.Errorf("expected zero-value gitResult fields, got %+v", res.gitResult)
	}
	if len(res.Added) != 1 || res.Added[0] != "nope" {
		t.Errorf("Added = %v, want [nope]", res.Added)
	}
}
