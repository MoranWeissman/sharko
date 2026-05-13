//go:build e2e

// Package lifecycle hosts the e2e suite's domain-level lifecycle tests
// (V2 Epic 7-1 stories 7-1.4 onward). 7-1.6 is the first lifecycle test
// to land here, so this file also seeds the package's dot-import contract:
// downstream stories add `*_test.go` siblings using the same harness import.
package lifecycle

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	. "github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestAddonAdmin covers the 10 custom-addon admin endpoints (12 minus
// the two addon-secrets ones that get their own top-level test below):
//
//   POST   /api/v1/addons                       (write — needs ArgoCD)
//   GET    /api/v1/addons/{name}                (read — needs ArgoCD)
//   PATCH  /api/v1/addons/{name}                (write — needs ArgoCD)
//   DELETE /api/v1/addons/{name}                (write — needs ArgoCD)
//   GET    /api/v1/addons/list                  (read — git-only, happy path)
//   GET    /api/v1/addons/catalog               (read — needs ArgoCD)
//   GET    /api/v1/addons/{name}/changelog      (read — git + helm registry)
//   GET    /api/v1/addons/version-matrix        (read — needs ArgoCD)
//   POST   /api/v1/addons/unwrap-globals        (write — needs ArgoCD)
//   POST   /api/v1/addons/upgrade-batch         (write — needs ArgoCD)
//
// IMPORTANT: the in-process harness (StartSharko + SetDemoGitProvider)
// does NOT seed an active ArgoCD connection. Eight of the ten endpoints
// resolve `s.connSvc.GetActiveArgocdClient()` and short-circuit with
// 502 ("no active ArgoCD connection") when no connection exists. This
// is by-design today — there is no Demo ArgoCD wiring in the harness
// foundation (StartSharko's wiring deliberately omits it; see
// tests/e2e/harness/sharko.go).
//
// We therefore split coverage into:
//
//   - happy path (only feasible for /addons/list — backed by the mock
//     git provider that StartGitMock injects via SetDemoGitProvider)
//
//   - validation contract (V124-4.3 / BUG-019): empty/invalid POST
//     bodies must return 400 BEFORE any upstream dial. Locks in the
//     fix that prevents an empty `{}` POST from burning ArgoCD/Git
//     quota on every retry.
//
//   - no-active-connection contract: well-formed write requests must
//     return 502 with a `no active ArgoCD connection` body so the UI
//     can render a "configure your connection first" banner instead
//     of a generic 500.
//
// When story 7-1.10 (or later) lands a Demo ArgoCD wiring this file
// can be expanded to cover the full happy path for every endpoint;
// the test names + structure are intentionally future-proof so the
// expansion is additive, not a rewrite.
func TestAddonAdmin(t *testing.T) {
	git := StartGitFake(t)
	mock := StartGitMock(t)
	sharko := StartSharko(t, SharkoConfig{
		Mode:        SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	SeedUsers(t, sharko, DefaultTestUsers())
	admin := NewClient(t, sharko)

	t.Run("ListAddons_HappyPath_EmptyCatalog", func(t *testing.T) {
		// Reset to a fresh empty catalog so this subtest is order-independent.
		// `applicationsets: []` is the canonical empty-state value the
		// parser yields for a non-existent file (see service.AddonService).
		mustSeedFile(t, mock, "configuration/addons-catalog.yaml", "applicationsets: []\n")

		out := admin.ListAdminAddons(t)
		if out.ApplicationSets == nil {
			// json-decoded nil slice is fine; the assertion below catches
			// "nil-vs-empty" semantic regressions in the handler.
			out.ApplicationSets = []map[string]any{}
		}
		if len(out.ApplicationSets) != 0 {
			t.Fatalf("ListAdminAddons (empty catalog): got %d entries, want 0", len(out.ApplicationSets))
		}
	})

	t.Run("ListAddons_HappyPath_PopulatedCatalog", func(t *testing.T) {
		// Seed two addons so the parser exercises the multi-entry path
		// AND so the response is sortable (handler defaults to name asc,
		// so cert-manager < metrics-server in the result).
		catalog := strings.Join([]string{
			"applicationsets:",
			"  - name: cert-manager",
			"    chart: cert-manager",
			"    version: v1.13.3",
			"    repo_url: https://charts.jetstack.io",
			"    namespace: cert-manager",
			"  - name: metrics-server",
			"    chart: metrics-server",
			"    version: 3.11.0",
			"    repo_url: https://kubernetes-sigs.github.io/metrics-server/",
			"    namespace: kube-system",
			"",
		}, "\n")
		mustSeedFile(t, mock, "configuration/addons-catalog.yaml", catalog)

		out := admin.ListAdminAddons(t)
		if got := len(out.ApplicationSets); got != 2 {
			t.Fatalf("ListAdminAddons: got %d entries, want 2; raw=%+v", got, out)
		}
		// Default sort is name ASC.
		if got, _ := out.ApplicationSets[0]["name"].(string); got != "cert-manager" {
			t.Fatalf("ListAdminAddons[0].name: got %q want cert-manager", got)
		}
		if got, _ := out.ApplicationSets[1]["name"].(string); got != "metrics-server" {
			t.Fatalf("ListAdminAddons[1].name: got %q want metrics-server", got)
		}
	})

	t.Run("AddCustomAddon_EmptyBody_400", func(t *testing.T) {
		// V124-4.3 / BUG-019: empty body must fail validation BEFORE any
		// upstream dial. If validation regresses, this returns 502 with
		// "no active ArgoCD connection" — the negative assertion below
		// catches that exact regression.
		resp := admin.AddAddonRaw(t, orchestrator.AddAddonRequest{})
		assertStatusBody(t, resp, http.StatusBadRequest, "addon name is required",
			"empty body must hit name-required validation",
			"no active argocd", "no active git")
	})

	t.Run("AddCustomAddon_PartialBody_400", func(t *testing.T) {
		// Name set but other required fields missing — the validator
		// walks fields in declaration order (name → chart → repo_url →
		// version) so a name-only body should fail with "chart is
		// required".
		resp := admin.AddAddonRaw(t, orchestrator.AddAddonRequest{Name: "kube-prometheus-stack"})
		assertStatusBody(t, resp, http.StatusBadRequest, "chart is required",
			"partial body must hit chart-required validation",
			"no active argocd")
	})

	t.Run("AddCustomAddon_FullBody_502_NoConnection", func(t *testing.T) {
		// Well-formed payload — validation passes, handler dials ArgoCD,
		// connection lookup fails, returns 502 "no active ArgoCD
		// connection: no active connection configured". Locks in the
		// gateway-error contract the UI relies on.
		req := orchestrator.AddAddonRequest{
			Name:    "fluent-bit",
			Chart:   "fluent-bit",
			RepoURL: "https://fluent.github.io/helm-charts",
			Version: "0.43.0",
		}
		resp := admin.AddAddonRaw(t, req)
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"well-formed body must surface gateway error", "")
	})

	t.Run("GetCustomAddon_ServiceUnavailable", func(t *testing.T) {
		// GET /addons/{name} requires ArgoCD; in-process harness has none,
		// so handler returns 503 (sanitized via writeServerError).
		resp := admin.GetAddonDetailRaw(t, "cert-manager")
		assertStatus(t, resp, http.StatusServiceUnavailable,
			"GET /addons/{name} should 503 without active ArgoCD")
	})

	t.Run("PatchCustomAddon_502_NoConnection", func(t *testing.T) {
		// PATCH validates only the path param — body validation is
		// looser (no required fields). The handler hits ArgoCD before
		// the orchestrator runs, so we expect 502.
		resp := admin.PatchAddonRaw(t, "cert-manager", orchestrator.ConfigureAddonRequest{
			Version: "v1.14.0",
		})
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"PATCH /addons/{name} should 502 without active ArgoCD", "")
	})

	t.Run("DeleteCustomAddon_DryRun_502", func(t *testing.T) {
		// Without ?confirm=true the handler ALSO needs ArgoCD (catalog
		// fetch for the impact report). 502 again.
		resp := admin.DeleteAddonRaw(t, "cert-manager", false)
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"DELETE /addons/{name} (dry-run) should 502 without active ArgoCD", "")
	})

	t.Run("DeleteCustomAddon_Confirmed_502", func(t *testing.T) {
		// With ?confirm=true, same gateway error — the contract is
		// identical regardless of confirm value when ArgoCD is down.
		resp := admin.DeleteAddonRaw(t, "cert-manager", true)
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"DELETE /addons/{name}?confirm=true should 502 without active ArgoCD", "")
	})

	t.Run("AddonsCatalog_503_NoConnection", func(t *testing.T) {
		resp := admin.GetAddonCatalogRaw(t)
		assertStatus(t, resp, http.StatusServiceUnavailable,
			"GET /addons/catalog should 503 without active ArgoCD")
	})

	t.Run("VersionMatrix_503_NoConnection", func(t *testing.T) {
		resp := admin.GetVersionMatrixRaw(t)
		assertStatus(t, resp, http.StatusServiceUnavailable,
			"GET /addons/version-matrix should 503 without active ArgoCD")
	})

	t.Run("AddonChangelog_InvalidSemver_400", func(t *testing.T) {
		// Changelog handler validates semver query params BEFORE any
		// upstream dial — same V124-4 validation-first pattern as
		// add-addon. A garbage `from=` returns 400 with "invalid 'from'
		// version" — locking in that the validation gate stays in
		// front of the helm registry call.
		resp := admin.GetAddonChangelogRaw(t, "cert-manager", "not-a-version", "")
		assertStatusBody(t, resp, http.StatusBadRequest, "invalid 'from' version",
			"semver validation must run before upstream dial", "")
	})

	t.Run("AddonChangelog_AddonNotInCatalog_404", func(t *testing.T) {
		// Seed an empty catalog so the lookup runs but returns no match.
		// Handler returns 404 with `addon "<name>" not found in catalog`.
		mustSeedFile(t, mock, "configuration/addons-catalog.yaml", "applicationsets: []\n")
		resp := admin.GetAddonChangelogRaw(t, "no-such-addon", "", "")
		assertStatusBody(t, resp, http.StatusNotFound, "not found in catalog",
			"changelog must 404 when addon name is unknown", "")
	})

	t.Run("UnwrapGlobals_502_NoConnection", func(t *testing.T) {
		resp := admin.UnwrapGlobalsRaw(t)
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"POST /addons/unwrap-globals should 502 without active ArgoCD", "")
	})

	t.Run("UpgradeBatch_EmptyBody_400", func(t *testing.T) {
		// V124-4.5 (BUG-019 class): empty `upgrades` map → 400 BEFORE
		// the upstream dial.
		resp := admin.UpgradeBatchRaw(t, map[string]string{})
		assertStatusBody(t, resp, http.StatusBadRequest, "at least one addon upgrade is required",
			"empty upgrade map must fail validation pre-dial",
			"no active argocd")
	})

	t.Run("UpgradeBatch_FullBody_502_NoConnection", func(t *testing.T) {
		resp := admin.UpgradeBatchRaw(t, map[string]string{
			"cert-manager":   "v1.14.0",
			"metrics-server": "3.12.0",
		})
		assertStatusBody(t, resp, http.StatusBadGateway, "no active ArgoCD connection",
			"well-formed batch must surface gateway error", "")
	})
}

// TestAddonSecretsLifecycle exercises the addon-secret bindings endpoints.
// All three are pure in-memory operations on the *api.Server — no upstream
// dial, so the in-process harness covers the FULL happy path including
// duplicate-name semantics and the 404-on-missing path.
//
// Endpoints:
//   GET    /api/v1/addon-secrets                — list
//   POST   /api/v1/addon-secrets                — create / overwrite
//   DELETE /api/v1/addon-secrets/{addon}        — remove
//
// RBAC: addon-secret.list = viewer+; addon-secret.create/delete = admin.
// The bootstrap admin from StartSharko has the admin role, so all three
// run without an explicit role-elevation step.
func TestAddonSecretsLifecycle(t *testing.T) {
	git := StartGitFake(t)
	mock := StartGitMock(t)
	sharko := StartSharko(t, SharkoConfig{
		Mode:        SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	SeedUsers(t, sharko, DefaultTestUsers())
	admin := NewClient(t, sharko)

	t.Run("ListEmpty", func(t *testing.T) {
		// Fresh server — no addon-secret defs configured. Handler returns
		// an empty map (NOT a 404), matching the swagger contract.
		got := admin.ListAddonSecrets(t)
		if len(got) != 0 {
			t.Fatalf("ListAddonSecrets (fresh server): got %d defs want 0; raw=%+v", len(got), got)
		}
	})

	t.Run("Create_MissingFields_400", func(t *testing.T) {
		// Missing addon_name + secret_name + namespace + keys. Handler
		// emits a single combined message: "addon_name, secret_name,
		// namespace, and keys are required".
		resp := admin.CreateAddonSecretRaw(t, orchestrator.AddonSecretDefinition{})
		assertStatusBody(t, resp, http.StatusBadRequest,
			"addon_name, secret_name, namespace, and keys are required",
			"empty addon-secret POST must fail with combined-required message", "")
	})

	t.Run("Create_HappyPath_201", func(t *testing.T) {
		def := orchestrator.AddonSecretDefinition{
			AddonName:  "datadog",
			SecretName: "datadog-secrets",
			Namespace:  "datadog",
			Keys: map[string]string{
				"api-key": "secrets/datadog/api-key",
				"app-key": "secrets/datadog/app-key",
			},
		}
		got := admin.CreateAddonSecret(t, def)
		// Echo-back contract: response body == request body verbatim
		// (handler writes the def back so the UI can confirm the saved
		// shape without re-issuing a GET).
		if got.AddonName != def.AddonName ||
			got.SecretName != def.SecretName ||
			got.Namespace != def.Namespace ||
			len(got.Keys) != len(def.Keys) {
			t.Fatalf("CreateAddonSecret echo: got %+v want %+v", got, def)
		}

		// Confirm the def now appears in the list (proves the in-memory
		// write took effect, not just the response shape).
		listed := admin.ListAddonSecrets(t)
		if _, ok := listed["datadog"]; !ok {
			t.Fatalf("ListAddonSecrets after Create: 'datadog' missing; got keys=%v", keysOf(listed))
		}
	})

	t.Run("DuplicateName_OverwritesIdempotent", func(t *testing.T) {
		// Sharko's current contract is "POST = upsert" — there is NO
		// 409 on duplicate. The handler unconditionally writes into
		// the addonSecretDefs map, replacing any existing entry. This
		// test locks that contract in: a second POST with the same
		// addon_name AND a different namespace should succeed (201)
		// AND the listed value should reflect the SECOND write.
		//
		// Rationale for documenting this here: the dispatch brief
		// described "DuplicateName: POST same name twice — expect 4xx
		// on second", which would be a NEW behaviour. Implementing
		// that needs a product-code change (a map-existence check
		// before the write) which is OUT OF SCOPE for 7-1.6 (test-only
		// story per the dispatch). Filing the contract gap in the
		// final report is the right action; the test asserts the
		// CURRENT behaviour so a future-developer who flips it to 409
		// sees the contract change explicitly.
		first := orchestrator.AddonSecretDefinition{
			AddonName:  "vault",
			SecretName: "vault-keys-v1",
			Namespace:  "vault",
			Keys:       map[string]string{"unseal-key": "secrets/vault/v1"},
		}
		_ = admin.CreateAddonSecret(t, first)

		second := orchestrator.AddonSecretDefinition{
			AddonName:  "vault",
			SecretName: "vault-keys-v2",
			Namespace:  "vault-system", // changed
			Keys:       map[string]string{"unseal-key": "secrets/vault/v2"},
		}
		got := admin.CreateAddonSecret(t, second)
		if got.SecretName != "vault-keys-v2" || got.Namespace != "vault-system" {
			t.Fatalf("Second POST upsert: got %+v want secret_name=vault-keys-v2 namespace=vault-system", got)
		}

		listed := admin.ListAddonSecrets(t)
		if v, ok := listed["vault"]; !ok || v.SecretName != "vault-keys-v2" || v.Namespace != "vault-system" {
			t.Fatalf("Listed 'vault' after upsert: got %+v ok=%v want SecretName=vault-keys-v2 Namespace=vault-system",
				v, ok)
		}
	})

	t.Run("Delete_HappyPath", func(t *testing.T) {
		// Confirm the entry from Create_HappyPath is present before
		// delete so the test is order-tolerant if anyone reorders the
		// subtests.
		def := orchestrator.AddonSecretDefinition{
			AddonName:  "to-delete",
			SecretName: "to-delete-secret",
			Namespace:  "default",
			Keys:       map[string]string{"k": "v"},
		}
		_ = admin.CreateAddonSecret(t, def)

		admin.DeleteAddonSecret(t, "to-delete")

		listed := admin.ListAddonSecrets(t)
		if _, ok := listed["to-delete"]; ok {
			t.Fatalf("Delete: 'to-delete' still present in list; got keys=%v", keysOf(listed))
		}
	})

	t.Run("Delete_NotFound_404", func(t *testing.T) {
		resp := admin.DeleteAddonSecretRaw(t, "no-such-addon")
		assertStatusBody(t, resp, http.StatusNotFound,
			"no secret definition for addon: no-such-addon",
			"DELETE on missing addon-secret must 404 with addon-name in body", "")
	})
}

// ---------------------------------------------------------------------------
// helpers (test-local — not promoted to the harness package because they
// are 7-1.6 specific and the harness is intentionally minimal at this
// stage of the epic)
// ---------------------------------------------------------------------------

// mustSeedFile writes content into the mock git provider's main branch.
// Convenience wrapper that handles the t.Helper + nil-error contract.
func mustSeedFile(t *testing.T, mock *MockGitProvider, path, content string) {
	t.Helper()
	if err := mock.CreateOrUpdateFile(t.Context(), path, []byte(content), "main", "seed"); err != nil {
		t.Fatalf("seed mock file %q: %v", path, err)
	}
}

// assertStatus asserts resp.StatusCode equals want. Always closes the body.
func assertStatus(t *testing.T, resp *http.Response, want int, msg string) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: status=%d want=%d; body=%s", msg, resp.StatusCode, want, body)
	}
}

// assertStatusBody asserts the response status, that the body's "error"
// field contains wantSubstr (case-insensitive), and that NONE of the
// negativeSubstrs appear (used to lock in regressions like "validation
// must run BEFORE upstream dial — therefore the body must NOT contain
// 'no active argocd'"). Always closes the body. Empty negativeSubstrs
// values are skipped so callers can pass `""` as a placeholder when no
// negative assertion is needed.
func assertStatusBody(t *testing.T, resp *http.Response, wantStatus int, wantSubstr, msg string, negativeSubstrs ...string) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s: status=%d want=%d; body=%s", msg, resp.StatusCode, wantStatus, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("%s: body is not JSON: %v; raw=%s", msg, err, body)
	}
	errStr, _ := decoded["error"].(string)
	if !strings.Contains(strings.ToLower(errStr), strings.ToLower(wantSubstr)) {
		t.Fatalf("%s: body[\"error\"]=%q does not contain %q; full body=%s",
			msg, errStr, wantSubstr, body)
	}
	for _, neg := range negativeSubstrs {
		if neg == "" {
			continue
		}
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(neg)) {
			t.Fatalf("%s: body[\"error\"]=%q must NOT contain %q (regression guard); full body=%s",
				msg, errStr, neg, body)
		}
	}
}

// keysOf returns the sorted keys of a map. Used in failure messages so
// the surrounding test logs are deterministic across runs.
func keysOf(m map[string]orchestrator.AddonSecretDefinition) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
