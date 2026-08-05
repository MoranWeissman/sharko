package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// secret_resource_test.go — the pins that matter for the live-Secret read
// (S3/S4). The headline one: NO input produces a response body carrying a
// data value. Everything else here exists so that pin cannot be satisfied
// by an endpoint that simply refuses to work.

// --- helpers -----------------------------------------------------------

// serverWithHubSecrets returns a Server whose "own cluster" client is a
// fake clientset holding the given objects in the argocd namespace — the
// same shape demo mode wires (a *fake.Clientset as the reconciler's
// ArgoClient), and the same accessor the handler uses.
func serverWithHubSecrets(t *testing.T, objs ...*corev1.Secret) *Server {
	t.Helper()
	client := k8sfake.NewSimpleClientset(toRuntimeObjects(objs)...)
	recon := clusterreconciler.New(clusterreconciler.Deps{
		ArgoClient: client,
		Namespace:  "argocd",
	})
	return &Server{clusterRecon: recon}
}

func toRuntimeObjects(secrets []*corev1.Secret) []runtime.Object {
	out := make([]runtime.Object, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, s)
	}
	return out
}

// serverWithRemoteSecrets returns a Server that resolves the named
// cluster's remote client to a fake clientset holding the given objects,
// plus one addon-values definition pointing at them.
func serverWithRemoteSecrets(cluster string, def orchestrator.AddonSecretDefinition, secrets ...*corev1.Secret) *Server {
	client := k8sfake.NewSimpleClientset(toRuntimeObjects(secrets)...)
	s := &Server{
		addonSecretDefs: map[string]orchestrator.AddonSecretDefinition{def.AddonName: def},
	}
	s.SetDemoRemoteClusterClient(func(_ context.Context, name string) (kubernetes.Interface, error) {
		if name != cluster {
			return nil, errors.New("no such cluster")
		}
		return client, nil
	})
	return s
}

func decodeView(t *testing.T, rw *httptest.ResponseRecorder) secretResourceView {
	t.Helper()
	var view secretResourceView
	if err := json.Unmarshal(rw.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v; body = %s", err, rw.Body.String())
	}
	return view
}

// --- S4: no input produces a data value in the response ----------------

// TestSecretResource_NeverReturnsAValue is the headline security pin. It
// feeds the view builder secrets whose values are, on purpose, things a
// naive "does the body look like a secret?" check would wave through:
// short words, a bare number, a boolean, a value identical to its own key
// name, an empty value. If ANY of them appears anywhere in the JSON body,
// the test fails.
func TestSecretResource_NeverReturnsAValue(t *testing.T) {
	cases := []struct {
		name   string
		data   map[string][]byte
		values []string
	}{
		{
			name:   "values that look like harmless strings",
			data:   map[string][]byte{"env": []byte("prod"), "enabled": []byte("true"), "replicas": []byte("3")},
			values: []string{"prod", "true", "3"},
		},
		{
			name:   "value equal to its own key name",
			data:   map[string][]byte{"api-key": []byte("api-key")},
			values: []string{"api-key"}, // the KEY must survive; the check below allows that
		},
		{
			name:   "a real-looking credential",
			data:   map[string][]byte{"token": []byte("sk-live-9f2b7c41e0a84d19")},
			values: []string{"sk-live-9f2b7c41e0a84d19"},
		},
		{
			name:   "an empty value",
			data:   map[string][]byte{"blank": []byte("")},
			values: []string{},
		},
		{
			name:   "no data at all",
			data:   nil,
			values: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
				Data:       tc.data,
			}
			view := newSecretResourceView(sec, "somewhere")

			if field, ok := secretResourceViewHasNoValue(view); !ok {
				t.Fatalf("view carries something other than the blank mask at %s", field)
			}

			// Every declared key must still be listed — a blank response
			// that lists nothing would pass a naive leak check while being
			// useless.
			if len(view.DataKeys) != len(tc.data) {
				t.Fatalf("data_keys = %d, want %d (key NAMES must survive blanking)", len(view.DataKeys), len(tc.data))
			}

			// Sweep the parts of the response that could carry content —
			// the key list, the labels, the annotations. The fixed
			// structural fields (kind, api_version, values_blanked) are
			// left out on purpose: "values_blanked":true would otherwise
			// match a secret whose value happens to be the word "true",
			// which is a false alarm about the test, not about the server.
			content, err := json.Marshal(struct {
				DataKeys    []secretResourceKeyView   `json:"data_keys"`
				Labels      []secretResourceLabelView `json:"labels"`
				Annotations []secretResourceLabelView `json:"annotations"`
			}{view.DataKeys, view.Labels, view.Annotations})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// The "value equal to its own key name" case would trip a
			// plain substring sweep on the key itself, so compare against
			// the content with every key name removed first.
			stripped := string(content)
			for k := range tc.data {
				stripped = strings.ReplaceAll(stripped, k, "")
			}
			if leaked, found := containsAny(stripped, tc.values); found {
				t.Fatalf("response content contains the secret value %q; content = %s", leaked, content)
			}
		})
	}
}

// TestSecretResource_BlanksTheSelfCopyingAnnotation pins the one
// annotation whose VALUE is a serialized copy of the object it sits on —
// kubectl writes it on every `kubectl apply`, so on a hand-applied Secret
// it carries every data value too. Showing annotations "as they are"
// without this gate would hand the browser everything this endpoint
// exists to blank.
func TestSecretResource_BlanksTheSelfCopyingAnnotation(t *testing.T) {
	const theValue = "sk-live-0000-leaked-through-an-annotation"
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secrets",
			Namespace: "datadog",
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"api-key":"` + theValue + `"}}`,
				"sharko.io/pushed-by":                              "the addon values engine",
			},
		},
		Data: map[string][]byte{"api-key": []byte(theValue)},
	}

	view := newSecretResourceView(sec, "somewhere")
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), theValue) {
		t.Fatalf("the last-applied-configuration annotation leaked the value; body = %s", body)
	}

	var sawBlanked, sawPlain bool
	for _, a := range view.Annotations {
		if a.Key == "kubectl.kubernetes.io/last-applied-configuration" {
			sawBlanked = a.Blanked && a.Value == blankedValue
		}
		if a.Key == "sharko.io/pushed-by" {
			sawPlain = a.Value == "the addon values engine"
		}
	}
	if !sawBlanked {
		t.Error("the self-copying annotation must still be LISTED, with its value blanked")
	}
	if !sawPlain {
		t.Error("an ordinary annotation must keep its real value")
	}
}

// TestSecretResource_ShowsLabelsInFull pins the deliberate exception: on a
// connection secret the addon labels ARE the useful content — they decide
// which addons run on that cluster — and they are not secret.
func TestSecretResource_ShowsLabelsInFull(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "argocd",
			Labels: map[string]string{
				"datadog":                      "enabled",
				"cert-manager":                 "enabled",
				clusterreconciler.LabelManagedBy: clusterreconciler.LabelValueSharko,
			},
		},
		Data: map[string][]byte{"config": []byte(`{"bearerToken":"secret"}`)},
	}
	view := newSecretResourceView(sec, "somewhere")

	got := map[string]string{}
	for _, l := range view.Labels {
		got[l.Key] = l.Value
	}
	if got["datadog"] != "enabled" || got["cert-manager"] != "enabled" {
		t.Fatalf("addon labels must be shown in full; got %+v", got)
	}
	// Sorted, so the panel renders the same order every time.
	for i := 1; i < len(view.Labels); i++ {
		if view.Labels[i-1].Key > view.Labels[i].Key {
			t.Fatalf("labels are not sorted: %+v", view.Labels)
		}
	}
}

// --- the connection-secret endpoint ------------------------------------

func TestConnectionSecretResource_ReadsTheLiveObject(t *testing.T) {
	created := time.Now().Add(-72 * time.Hour)
	s := serverWithHubSecrets(t, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-eu",
			Namespace:         "argocd",
			Labels:            map[string]string{"datadog": "enabled"},
			CreationTimestamp: metav1.NewTime(created),
		},
		Data: map[string][]byte{"config": []byte(`{"bearerToken":"a-real-token"}`), "server": []byte("https://x")},
		Type: corev1.SecretTypeOpaque,
	})

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod-eu/secret/resource", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	rw := httptest.NewRecorder()
	s.handleGetConnectionSecretResource(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "a-real-token") {
		t.Fatalf("the response body carries a secret value; body = %s", rw.Body.String())
	}

	view := decodeView(t, rw)
	if view.Kind != "Secret" || view.Name != "prod-eu" || view.Namespace != "argocd" {
		t.Fatalf("unexpected identity: %+v", view)
	}
	if !view.ValuesBlanked {
		t.Error("values_blanked must be true")
	}
	if len(view.DataKeys) != 2 {
		t.Fatalf("data_keys = %d, want 2 (config, server)", len(view.DataKeys))
	}
	if view.CreatedAt == "" {
		t.Error("created_at must be set so the panel can show an age")
	}
	if field, ok := secretResourceViewHasNoValue(view); !ok {
		t.Fatalf("view carries something other than the blank mask at %s", field)
	}
}

func TestConnectionSecretResource_MissingSecretSaysSoPlainly(t *testing.T) {
	s := serverWithHubSecrets(t) // no secrets at all

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/ghost/secret/resource", nil), "operator")
	req.SetPathValue("name", "ghost")
	rw := httptest.NewRecorder()
	s.handleGetConnectionSecretResource(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "does not exist") || !strings.Contains(msg, "ghost") {
		t.Errorf("error must say plainly that the secret does not exist, and name the cluster; got %q", msg)
	}
	// A failed read never falls back to content.
	if _, hasKeys := body["data_keys"]; hasKeys {
		t.Error("a failed read must return no resource content at all")
	}
}

func TestConnectionSecretResource_NoHubClientDoesNotInvent(t *testing.T) {
	s := &Server{} // no cluster reconciler wired

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod-eu/secret/resource", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	rw := httptest.NewRecorder()
	s.handleGetConnectionSecretResource(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "data_keys") {
		t.Error("a failed read must return no resource content at all")
	}
}

func TestConnectionSecretResource_ViewerForbidden(t *testing.T) {
	s := serverWithHubSecrets(t)
	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod-eu/secret/resource", nil), "viewer")
	req.SetPathValue("name", "prod-eu")
	rw := httptest.NewRecorder()
	s.handleGetConnectionSecretResource(rw, req)
	assert403(t, rw)
}

// --- the addon-values endpoint -----------------------------------------

func addonDef() orchestrator.AddonSecretDefinition {
	return orchestrator.AddonSecretDefinition{
		AddonName:  "datadog",
		SecretName: "datadog-secrets",
		Namespace:  "datadog",
		Keys:       map[string]string{"api-key": "secrets/datadog/api-key", "app-key": "secrets/datadog/app-key"},
	}
}

func TestAddonValuesSecretResource_ReadsTheLiveObject(t *testing.T) {
	def := addonDef()
	s := serverWithRemoteSecrets("prod-eu", def, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:              def.SecretName,
			Namespace:         def.Namespace,
			Labels:            map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-24 * time.Hour)),
		},
		Data: map[string][]byte{"api-key": []byte("dd-api-real"), "app-key": []byte("dd-app-real")},
		Type: corev1.SecretTypeOpaque,
	})

	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	req.SetPathValue("addon", "datadog")
	rw := httptest.NewRecorder()
	s.handleGetAddonValuesSecretResource(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	if leaked, found := containsAny(rw.Body.String(), []string{"dd-api-real", "dd-app-real"}); found {
		t.Fatalf("the response body carries the secret value %q; body = %s", leaked, rw.Body.String())
	}

	view := decodeView(t, rw)
	if view.Name != def.SecretName || view.Namespace != def.Namespace {
		t.Fatalf("unexpected identity: %+v", view)
	}
	if len(view.DataKeys) != 2 {
		t.Fatalf("data_keys = %d, want 2", len(view.DataKeys))
	}
	for _, k := range view.DataKeys {
		if k.Value != blankedValue {
			t.Fatalf("data key %q value = %q, want the blank mask", k.Key, k.Value)
		}
	}
	if !strings.Contains(view.ReadFrom, "prod-eu") {
		t.Errorf("read_from must name the cluster the object came from; got %q", view.ReadFrom)
	}
}

func TestAddonValuesSecretResource_NoDefinitionSaysSo(t *testing.T) {
	s := &Server{addonSecretDefs: map[string]orchestrator.AddonSecretDefinition{}}

	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	req.SetPathValue("addon", "nginx")
	rw := httptest.NewRecorder()
	s.handleGetAddonValuesSecretResource(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "nginx") {
		t.Errorf("the message must name the addon; body = %s", rw.Body.String())
	}
}

// TestAddonValuesSecretResource_UnreachableClusterIsHonest pins the
// no-fallback rule AND the no-raw-error rule: the cluster cannot be
// reached, the caller is told exactly that, and the provider's own error
// text — which could in principle echo secret material — never appears in
// the body.
func TestAddonValuesSecretResource_UnreachableClusterIsHonest(t *testing.T) {
	def := addonDef()
	s := &Server{addonSecretDefs: map[string]orchestrator.AddonSecretDefinition{def.AddonName: def}}
	s.SetDemoRemoteClusterClient(func(_ context.Context, _ string) (kubernetes.Interface, error) {
		return nil, errors.New("dial tcp 10.0.0.1:443: i/o timeout while presenting token sk-live-LEAK")
	})

	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	req.SetPathValue("addon", "datadog")
	rw := httptest.NewRecorder()
	s.handleGetAddonValuesSecretResource(rw, req)

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	if strings.Contains(body, "sk-live-LEAK") || strings.Contains(body, "i/o timeout") {
		t.Fatalf("the raw error text reached the caller; body = %s", body)
	}
	if !strings.Contains(body, "couldn't connect to cluster") {
		t.Errorf("the message must say plainly what failed; body = %s", body)
	}
	if strings.Contains(body, "data_keys") {
		t.Error("a failed read must return no resource content at all")
	}
}

func TestAddonValuesSecretResource_SecretGoneSaysSo(t *testing.T) {
	def := addonDef()
	s := serverWithRemoteSecrets("prod-eu", def) // cluster reachable, no secret on it

	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), "operator")
	req.SetPathValue("name", "prod-eu")
	req.SetPathValue("addon", "datadog")
	rw := httptest.NewRecorder()
	s.handleGetAddonValuesSecretResource(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "does not exist") {
		t.Errorf("the message must say plainly that the secret is not there; body = %s", rw.Body.String())
	}
}

func TestAddonValuesSecretResource_ViewerForbidden(t *testing.T) {
	def := addonDef()
	s := serverWithRemoteSecrets("prod-eu", def)
	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), "viewer")
	req.SetPathValue("name", "prod-eu")
	req.SetPathValue("addon", "datadog")
	rw := httptest.NewRecorder()
	s.handleGetAddonValuesSecretResource(rw, req)
	assert403(t, rw)
}
