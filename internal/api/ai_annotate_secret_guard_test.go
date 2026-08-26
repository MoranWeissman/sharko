// ai_annotate_secret_guard_test.go — the REAL 422 body, not the helper.
//
// POST /api/v1/addons/{name}/values/annotate is the one caller that puts
// SecretMatch.Field on a wire. The other three call sites of the guard hand
// the audit log only a count and the pattern names. So this file drives the
// whole handler — a local chart repository serving a values.yaml full of
// synthetic secrets, through the real fetch, the real guard, the real
// refusal — and then reads the bytes that would have gone to the browser.
//
// Every value here is synthetic. Nothing in this file is, or has ever been,
// a real credential.

package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// guardSentinel is planted in every synthetic value below. The sweep at the
// end of each test looks for it in the raw response bytes; the sweep's own
// positive control proves the sweep can find it when it really is there.
const guardSentinel = "PLANTEDSENTINELBF15MUSTFIND"

// guardValuesYAML is the chart values.yaml the fake repository serves. Each
// line is a value shaped so that a mask which only replaces the matched
// substring stops early and copies the rest of the line out verbatim.
func guardValuesYAML() string {
	return strings.Join([]string{
		"# a synthetic chart values file",
		"replicaCount: 2",
		// The control: entirely letters and digits. This one always masked
		// cleanly, so a green run cannot come from the guard never firing.
		"awsKey: AKIAIOSFODNN7EXAMPLE",
		"password: SYNTHETICVALUE0001." + guardSentinel + ".more",
		"apiKey: SYNTHETICVALUE0002!" + guardSentinel,
		"secret: SYNTHETICVALUE0003$" + guardSentinel,
		"apiToken: SYNTHETICVALUE0004 " + guardSentinel,
		"credential = SYNTHETICVALUE0005@" + guardSentinel + "/deeper",
		"tls:",
		"  key: |",
		"    -----BEGIN RSA PRIVATE KEY-----",
		"    MIIBOgIBAAJBA" + guardSentinel + "0123456789abcdef",
		"    -----END RSA PRIVATE KEY-----",
		"",
	}, "\n")
}

// guardSecretValues is every complete secret value in guardValuesYAML.
// Not one six-character run of any of these may appear in the 422 body.
func guardSecretValues() []string {
	return []string{
		"AKIAIOSFODNN7EXAMPLE",
		"SYNTHETICVALUE0001." + guardSentinel + ".more",
		"SYNTHETICVALUE0002!" + guardSentinel,
		"SYNTHETICVALUE0003$" + guardSentinel,
		"SYNTHETICVALUE0004 " + guardSentinel,
		"SYNTHETICVALUE0005@" + guardSentinel + "/deeper",
		"MIIBOgIBAAJBA" + guardSentinel + "0123456789abcdef",
	}
}

// chartTGZ builds a minimal Helm chart archive holding only values.yaml.
func chartTGZ(t *testing.T, chartName, values string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(values)
	if err := tw.WriteHeader(&tar.Header{
		Name: chartName + "/values.yaml",
		Mode: 0o644,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// localChartRepo stands up a real Helm-style chart repository on localhost:
// an index.yaml naming one version, and the .tgz it points at. The handler's
// helm.Fetcher walks both over HTTP exactly as it would in production.
func localChartRepo(t *testing.T, chartName, version, values string) string {
	t.Helper()
	tgz := chartTGZ(t, chartName, values)
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "apiVersion: v1\nentries:\n  %s:\n    - version: %s\n      urls:\n        - %s/%s-%s.tgz\n",
			chartName, version, base, chartName, version)
	})
	mux.HandleFunc(fmt.Sprintf("/%s-%s.tgz", chartName, version), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tgz)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv.URL
}

// guardRepoFiles is the v4 repo the handler reads: the engine pin plus a
// catalog whose one addon points at the local chart repository.
func guardRepoFiles(t *testing.T, addon, chart, version, repoURL string) map[string][]byte {
	t.Helper()
	catalogBody, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			addon: {RepoURL: repoURL, Chart: chart, Version: version, Namespace: addon},
		},
	})
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}
	return map[string][]byte{
		orchestrator.BootstrapRootAppPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:           catalogBody,
	}
}

// bodyFragmentsOf returns every run of n or more characters of `value` that
// also appears in `body`. The sweep knows nothing about how the mask works,
// so a future masking bug cannot hide from it by taking a shape the sweep
// did not anticipate.
func bodyFragmentsOf(body, value string, n int) []string {
	var found []string
	seen := map[string]struct{}{}
	for i := 0; i+n <= len(value); i++ {
		frag := value[i : i+n]
		if strings.TrimSpace(frag) == "" {
			continue
		}
		if !strings.Contains(body, frag) {
			continue
		}
		if _, dup := seen[frag]; dup {
			continue
		}
		seen[frag] = struct{}{}
		found = append(found, frag)
	}
	return found
}

// TestAnnotate422Sweep_FindsPlantedSentinel is the sweep's positive control.
// A response body that really does carry a secret must be reported as
// carrying it — otherwise the "clean" verdict in the test below is empty.
func TestAnnotate422Sweep_FindsPlantedSentinel(t *testing.T) {
	leaky := `{"code":"secret_detected_blocked","matches":[{"field":"password: ***.` + guardSentinel + `.more"}]}`
	value := "SYNTHETICVALUE0001." + guardSentinel + ".more"

	if got := bodyFragmentsOf(leaky, value, 6); len(got) == 0 {
		t.Fatal("the sweep found nothing in a body that demonstrably carries the value — the sweep is broken")
	}
	clean := `{"code":"secret_detected_blocked","matches":[{"field":"password: ***"}]}`
	if got := bodyFragmentsOf(clean, value, 6); len(got) != 0 {
		t.Fatalf("the sweep invented a leak in a clean body: %v", got)
	}
}

// TestAnnotate422_CarriesNoPartOfAnySecret is the acceptance test. It drives
// the real endpoint against a real local chart repository and reads the
// bytes of the real 422.
func TestAnnotate422_CarriesNoPartOfAnySecret(t *testing.T) {
	const addon, chart, version = "cert-manager", "cert-manager", "1.14.5"
	repoURL := localChartRepo(t, chart, version, guardValuesYAML())

	gp := newAnnotateV4Git(guardRepoFiles(t, addon, chart, version, repoURL))
	srv := serverWithFullV4Connection(t, gp)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/addons/"+addon+"/values/annotate", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	raw := w.Body.String()

	// No vacuous pass. If the guard never fired we did not test masking at
	// all, and a 200 or a 502 must be reported as a failure of this test's
	// own setup rather than counted as a clean sweep.
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — the guard did not fire, so this run proves nothing about masking. body: %s", w.Code, raw)
	}

	var body aiAnnotateBlockedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("422 body is not the blocked-response shape: %v (raw: %s)", err, raw)
	}
	if body.Code != "secret_detected_blocked" {
		t.Errorf("code = %q, want secret_detected_blocked", body.Code)
	}
	if len(body.Matches) == 0 {
		t.Fatal("the 422 carried no matches — nothing to check, so this run proves nothing")
	}

	// The control must be among them: the all-letters-and-digits AWS key is
	// the case that already masked cleanly, so seeing it proves the guard
	// really ran over the whole file.
	sawControl := false
	for _, m := range body.Matches {
		if m.Pattern == "AWS access key" {
			sawControl = true
		}
	}
	if !sawControl {
		t.Errorf("the positive control (AWS access key) did not appear in the matches: %+v", body.Matches)
	}

	// The planted sentinel must not survive anywhere in the response.
	if strings.Contains(raw, guardSentinel) {
		t.Errorf("the 422 body carries the planted sentinel verbatim: %s", raw)
	}
	// And no six-character run of any complete value may survive either.
	for _, v := range guardSecretValues() {
		if frags := bodyFragmentsOf(raw, v, 6); len(frags) > 0 {
			t.Errorf("the 422 body carries part of a secret value: fragments=%v body=%s", frags, raw)
		}
	}

	// Every field must be either a clean `key: ***` or the fixed generic
	// text — never anything else.
	for _, m := range body.Matches {
		if m.Field == orchestrator.SecretFieldUnavailable {
			continue
		}
		if !strings.HasSuffix(m.Field, ": ***") {
			t.Errorf("field %q is neither a plain `key: ***` nor the fixed generic text", m.Field)
		}
	}

	// The refusal must still be useful: it names the field the maintainer
	// has to go and look at, and gives them a line number.
	namedAField := false
	for _, m := range body.Matches {
		if strings.HasPrefix(m.Field, "password: ") && m.Line > 0 {
			namedAField = true
		}
	}
	if !namedAField {
		t.Errorf("the refusal no longer tells the maintainer where to look: %+v", body.Matches)
	}
}
