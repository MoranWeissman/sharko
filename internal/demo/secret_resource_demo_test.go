package demo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// secret_resource_demo_test.go — P3-F2's demo pins. The detail panel picks
// one of exactly five sentences to describe a secret, and a demo where two
// of them are unreachable teaches the maintainer a panel that does not
// exist. This walks the big estate and checks each one has a row that
// produces it, plus the two things the live read itself has to show: a
// half-written secret, and provenance annotations that survive the
// allow-list.

// secretResourceBody mirrors internal/api's secretResourceView — a local
// test-only re-declaration rather than importing unexported types.
type secretResourceBody struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	SecretType string `json:"secret_type"`
	CreatedAt  string `json:"created_at"`
	Labels     []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"labels"`
	Annotations []struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Blanked bool   `json:"blanked"`
	} `json:"annotations"`
	DataKeys []struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Path    string `json:"path"`
		Present bool   `json:"present"`
	} `json:"data_keys"`
	ReadFrom      string `json:"read_from"`
	ValuesBlanked bool   `json:"values_blanked"`
}

// TestBigEstate_EveryDiffSentenceIsReachable pins that the five panel
// sentences each have a row behind them in the demo estate. The panel
// picks by row state, so the check is on the states — see
// diffVerdictFor in ui/src/views/ManagedSecrets.tsx for the mapping:
//
//	in_sync      -> "These match ..."
//	out_of_sync  -> "These differ ..."
//	missing      -> "This secret was never created ..."
//	foreign      -> "Someone else created this secret ..."
//	unknown      -> "Sharko could not look at the cluster just now."
func TestBigEstate_EveryDiffSentenceIsReachable(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	states := map[string]int{}
	for _, row := range body.AddonValuesSecrets {
		states[row.State]++
	}
	for _, row := range body.ClusterConnectionSecrets {
		states[row.State]++
	}
	t.Logf("states across both tables: %v", states)

	for _, want := range []string{"in_sync", "out_of_sync", "missing", "foreign", "unknown"} {
		if states[want] == 0 {
			t.Errorf("no row in state %q — one of the panel's five sentences is unreachable on the demo estate; states=%v", want, states)
		}
	}
}

// TestBigEstate_LiveReadShowsProvenanceAndBlanksTheRest walks the live read
// the panel's right-hand card makes, and pins both halves of the P3-F2
// annotation flip: Sharko's own provenance notes come through as written,
// and everything else is masked with its key still listed.
func TestBigEstate_LiveReadShowsProvenanceAndBlanksTheRest(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	var sawProvenance, sawBlanked, sawHalfWritten bool
	reads := 0
	for _, row := range body.AddonValuesSecrets {
		// The panel fires no read for a row already known missing, and
		// neither does this walk.
		if row.State == "missing" {
			continue
		}
		res, ok := readLiveValuesSecret(t, router, token, row.Cluster, row.Addon)
		if !ok {
			continue
		}
		reads++

		if !res.ValuesBlanked {
			t.Fatalf("%s/%s: values_blanked = false", row.Cluster, row.Addon)
		}
		for _, a := range res.Annotations {
			switch a.Key {
			case "sharko.dev/addon", "sharko.dev/source", "sharko.dev/written-at":
				if a.Blanked {
					t.Errorf("%s/%s: provenance annotation %q was blanked — the panel has nothing to show", row.Cluster, row.Addon, a.Key)
				}
				sawProvenance = true
			default:
				if !a.Blanked {
					t.Errorf("%s/%s: annotation %q came through unmasked — the allow-list is not holding", row.Cluster, row.Addon, a.Key)
				}
				sawBlanked = true
			}
		}
		for _, k := range res.DataKeys {
			if k.Path == "" {
				t.Errorf("%s/%s: key %q has no store pointer — the key list has nothing to say about where it comes from", row.Cluster, row.Addon, k.Key)
			}
			if !k.Present {
				sawHalfWritten = true
			}
		}
	}

	if reads == 0 {
		t.Fatal("no live read succeeded on the big estate — the panel's right-hand card is empty everywhere")
	}
	if !sawProvenance {
		t.Error("no secret carried a sharko.dev provenance annotation — the demo cannot show what Sharko stamps on its own writes")
	}
	if !sawBlanked {
		t.Error("no secret carried a non-provenance annotation — the demo never walks the blanking path")
	}
	if !sawHalfWritten {
		t.Error("no secret is missing a declared key — the key list's \"not on the cluster\" line is unreachable on the demo estate")
	}
}

// TestBigEstate_LiveReadIsAudited pins P3-F1's live-read entry end to end:
// opening a secret object in demo mode leaves a line in the audit log.
func TestBigEstate_LiveReadIsAudited(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)
	body := getManagedSecrets(t, router, token)

	var cluster, addon string
	for _, row := range body.AddonValuesSecrets {
		if row.State == "in_sync" {
			cluster, addon = row.Cluster, row.Addon
			break
		}
	}
	if cluster == "" {
		t.Fatal("no in-sync values row to open")
	}

	before := len(srv.AuditLog().List(0))
	if _, ok := readLiveValuesSecret(t, router, token, cluster, addon); !ok {
		t.Fatalf("live read of %s/%s did not succeed", cluster, addon)
	}

	var found bool
	for _, e := range srv.AuditLog().List(0) {
		if e.Event == "secret_resource_read" && e.Resource == fmt.Sprintf("cluster:%s/addon:%s", cluster, addon) {
			found = true
			if e.Result != "success" {
				t.Errorf("result = %q, want success", e.Result)
			}
		}
	}
	if !found {
		t.Errorf("opening a live secret wrote no audit entry (log went from %d to %d entries)", before, len(srv.AuditLog().List(0)))
	}
}

func readLiveValuesSecret(t *testing.T, router http.Handler, token, cluster, addon string) (secretResourceBody, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/clusters/%s/addons/%s/secret/resource", cluster, addon), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		return secretResourceBody{}, false
	}
	var res secretResourceBody
	if err := json.Unmarshal(rw.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode live read of %s/%s: %v; body = %s", cluster, addon, err, rw.Body.String())
	}
	return res, true
}
