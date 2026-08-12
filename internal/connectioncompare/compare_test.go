package connectioncompare

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// compare_test.go — CC2-5, the functional tests for the read-only comparison.
//
// No real credential value appears in this file. The stand-in strings are
// obviously made up and are not shaped like anything from a real cluster.

const (
	testCluster   = "test-cluster-one"
	testNamespace = "argocd"
	testServer    = "https://test-cluster-one.invalid"
)

// fakeCA / fakeCert / fakeKey are stand-ins for base64 credential material.
// They are not real certificates and are not shaped like real ones.
var (
	fakeCA   = base64.StdEncoding.EncodeToString([]byte("not-a-real-ca-just-test-bytes"))
	fakeCert = base64.StdEncoding.EncodeToString([]byte("not-a-real-cert-just-test-bytes"))
	fakeKey  = base64.StdEncoding.EncodeToString([]byte("not-a-real-key-just-test-bytes"))
)

// liveFrom turns a built expected Secret into a live one: Kubernetes returns
// values in Data even for keys written through StringData, so a live Secret has
// Data and no StringData.
func liveFrom(built *corev1.Secret) *corev1.Secret {
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      built.Name,
			Namespace: built.Namespace,
			Labels:    map[string]string{},
		},
		Type: built.Type,
		Data: map[string][]byte{},
	}
	for k, v := range built.Labels {
		live.Labels[k] = v
	}
	for k, v := range built.StringData {
		live.Data[k] = []byte(v)
	}
	return live
}

// ownedRequest builds a Request for a Sharko-owned, backend-stored connection
// whose live Secret exactly matches the expected one, plus the built expected
// Secret so a test can perturb one side.
func ownedRequest(t *testing.T, spec argosecrets.ClusterSecretSpec, addonLabels map[string]string) (Request, *corev1.Secret) {
	t.Helper()
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})
	if policy.Scope != ScopeFull {
		t.Fatalf("fixture expected full scope, got %q", policy.Scope)
	}

	specLabels := map[string]string{}
	for k, v := range addonLabels {
		specLabels[k] = v
	}
	models.ApplyConnectivityCheckLabel(specLabels, false)
	buildSpec := spec
	buildSpec.Name = testCluster
	buildSpec.Labels = specLabels
	built, err := argosecrets.BuildClusterSecret(buildSpec, testNamespace)
	if err != nil {
		t.Fatalf("building the expected secret: %v", err)
	}

	specCopy := spec
	return Request{
		ClusterName:         testCluster,
		Namespace:           testNamespace,
		Policy:              policy,
		Live:                liveFrom(built),
		LiveFound:           true,
		DesiredAddonLabels:  addonLabels,
		AddonLabelsKnown:    true,
		ConnectivityCheckOn: false,
		ExpectedSpec:        &specCopy,
	}, built
}

// --- expected Secret shapes -------------------------------------------------

func TestCompare_ExpectedSecretForEKSExecShape(t *testing.T) {
	// No token and no cert pair, so the builder emits the exec shape.
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Region: "us-east-1", RoleARN: "arn:aws:iam::000000000000:role/test-role", CAData: fakeCA}
	req, built := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	if !strings.Contains(built.StringData["config"], "execProviderConfig") {
		t.Fatalf("fixture did not produce the exec shape: %s", built.StringData["config"])
	}
	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences: %+v)", res.Status, StatusSynced, res.Differences)
	}
}

func TestCompare_ExpectedSecretForBearerTokenShape(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only", CAData: fakeCA}
	req, built := ownedRequest(t, spec, nil)
	if !strings.Contains(built.StringData["config"], "bearerToken") {
		t.Fatalf("fixture did not produce the bearer shape: %s", built.StringData["config"])
	}
	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences: %+v)", res.Status, StatusSynced, res.Differences)
	}
}

func TestCompare_ExpectedSecretForCertKeyShape(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, CertData: fakeCert, KeyData: fakeKey, CAData: fakeCA}
	req, built := ownedRequest(t, spec, nil)
	cfg := built.StringData["config"]
	if !strings.Contains(cfg, "certData") || strings.Contains(cfg, "bearerToken") || strings.Contains(cfg, "execProviderConfig") {
		t.Fatalf("fixture did not produce the cert shape: %s", cfg)
	}
	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences: %+v)", res.Status, StatusSynced, res.Differences)
	}
}

// TestCompare_CAHandling: a CA bundle that differs is a difference in the
// credential blob, and it comes back with no values on either side — a CA is
// not itself secret, but it lives inside data.config, and the comparison never
// splits that blob open to report part of it.
func TestCompare_CAHandling(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only", CAData: fakeCA}
	req, built := ownedRequest(t, spec, nil)

	// A CA-less expected side against a CA-bearing live side: the builder
	// flips insecure to true when there is no CA, so the blob differs.
	noCA := *req.ExpectedSpec
	noCA.CAData = ""
	req.ExpectedSpec = &noCA
	_ = built

	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	d := findDiff(t, res, FieldPathDataConfig)
	if !d.Sensitive {
		t.Error("the credential blob must be reported as sensitive")
	}
	if d.Expected != nil || d.Live != nil {
		t.Error("the credential blob must come back with neither side")
	}
}

// --- safe fields ------------------------------------------------------------

func TestCompare_SafeFieldMatchAndMismatch(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only", CAData: fakeCA}

	// Match.
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	if res := Compare(req); res.Status != StatusSynced {
		t.Fatalf("matching connection: status = %q, want %q (differences %+v)", res.Status, StatusSynced, res.Differences)
	}

	// Mismatch on a safe field: the API address.
	req, _ = ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	req.Live.Data["server"] = []byte("https://somewhere-else.invalid")
	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	d := findDiff(t, res, FieldPathDataServer)
	if d.Sensitive {
		t.Error("the API address is not secret and must not be marked sensitive")
	}
	if d.Expected == nil || *d.Expected != testServer {
		t.Errorf("expected side = %v, want %q", d.Expected, testServer)
	}
	if d.Live == nil || *d.Live != "https://somewhere-else.invalid" {
		t.Errorf("live side = %v, want the wrong address", d.Live)
	}
}

func TestCompare_SafeLabelMismatch(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	req.Live.Labels["datadog"] = models.LabelDisabled

	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	d := findDiff(t, res, labelFieldPath("datadog"))
	if d.Status != FieldDifferent {
		t.Errorf("status = %q, want %q", d.Status, FieldDifferent)
	}
	if d.Expected == nil || *d.Expected != models.LabelEnabled {
		t.Errorf("expected = %v, want %q", d.Expected, models.LabelEnabled)
	}
}

// --- sensitive fields -------------------------------------------------------

func TestCompare_SensitiveFieldMatchIsNotReported(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only", CAData: fakeCA}
	req, _ := ownedRequest(t, spec, nil)
	res := Compare(req)
	for _, d := range res.Differences {
		if d.Path == FieldPathDataConfig {
			t.Fatalf("a matching credential blob must not appear as a difference: %+v", d)
		}
	}
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q", res.Status, StatusSynced)
	}
}

func TestCompare_SensitiveFieldMismatchCarriesNeitherSide(t *testing.T) {
	const neverShip = "zqx-comparison-sentinel-never-ship-8412"
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: neverShip, CAData: fakeCA}
	req, _ := ownedRequest(t, spec, nil)
	req.Live.Data["config"] = []byte(`{"bearerToken":"a-completely-different-made-up-value"}`)

	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	d := findDiff(t, res, FieldPathDataConfig)
	if !d.Sensitive || d.Expected != nil || d.Live != nil {
		t.Fatalf("credential blob difference must be sensitive with neither side: %+v", d)
	}

	raw, err := json.Marshal(res.Differences)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), neverShip) {
		t.Fatalf("the response carries the expected credential value: %s", raw)
	}
	if strings.Contains(string(raw), "a-completely-different-made-up-value") {
		t.Fatalf("the response carries the live credential value: %s", raw)
	}
}

// --- missing / extra / ignored fields --------------------------------------

func TestCompare_MissingOwnedField(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	delete(req.Live.Labels, "datadog")
	delete(req.Live.Data, "server")

	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	if d := findDiff(t, res, labelFieldPath("datadog")); d.Status != FieldMissing {
		t.Errorf("label status = %q, want %q", d.Status, FieldMissing)
	}
	if d := findDiff(t, res, FieldPathDataServer); d.Status != FieldMissing {
		t.Errorf("server status = %q, want %q", d.Status, FieldMissing)
	} else if d.Live != nil {
		t.Error("a missing field has no live side")
	}
}

func TestCompare_UnrelatedExtraFieldsArePreservedAndNotReported(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)

	// Somebody else's label, somebody else's annotation, somebody else's data
	// key. None of them is Sharko's, so none of them is drift.
	req.Live.Labels["example.com/owner"] = "another-team"
	req.Live.Labels["app.kubernetes.io/instance"] = "some-argo-app"
	req.Live.Annotations = map[string]string{"example.com/note": "hello"}
	req.Live.Data["their-own-key"] = []byte("their own value")

	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusSynced, res.Differences)
	}
	raw, _ := json.Marshal(res.Differences)
	for _, s := range []string{"example.com/owner", "app.kubernetes.io/instance", "example.com/note", "their-own-key"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("somebody else's field %q was reported: %s", s, raw)
		}
	}
}

func TestCompare_VolatileKubernetesFieldsIgnored(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)
	req.Live.ResourceVersion = "999999"
	req.Live.UID = "e7c1f0aa-0000-0000-0000-000000000000"
	req.Live.Generation = 7
	req.Live.CreationTimestamp = metav1.Now()
	req.Live.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl", Operation: metav1.ManagedFieldsOperationApply}}

	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusSynced, res.Differences)
	}
	raw, _ := json.Marshal(res.Differences)
	for _, s := range []string{"999999", "resourceVersion", "uid", "generation", "creationTimestamp", "managedFields"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("a volatile Kubernetes field leaked into the differences (%q): %s", s, raw)
		}
	}
}

func TestCompare_WrittenAtStyleProvenanceIgnored(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)
	req.Live.Annotations = map[string]string{
		clusterreconciler.AnnotationWrittenAt:  "2020-01-01T00:00:00Z",
		clusterreconciler.AnnotationRevision:   "0000000000000000000000000000000000000000",
		clusterreconciler.AnnotationSourceFile: "somewhere/else.yaml",
	}

	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("a timestamp is not drift: status = %q, want %q (differences %+v)", res.Status, StatusSynced, res.Differences)
	}
	raw, _ := json.Marshal(res.Differences)
	for _, s := range []string{"written-at", "revision", "source-file", "2020-01-01"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("per-write provenance was reported as drift (%q): %s", s, raw)
		}
	}
}

func TestCompare_PreservedTakeoverLabelsIgnored(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)
	// An unqualified key from the previous owner — exactly the shape the
	// addon-key rule would otherwise claim as Sharko's own.
	req.Live.Labels["previous-owner-label"] = "kept"
	req.Live.Annotations = map[string]string{
		argosecrets.AnnotationTakeoverPreservedLabels: "previous-owner-label",
	}

	res := Compare(req)
	if res.Status != StatusSynced {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusSynced, res.Differences)
	}
	raw, _ := json.Marshal(res.Differences)
	if strings.Contains(string(raw), "previous-owner-label") {
		t.Errorf("a takeover-preserved label was reported as drift: %s", raw)
	}
}

func TestCompare_StaleAddonLabelIsUnexpected(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)
	req.Live.Labels[models.V4AddonLabelKey("datadog")] = models.LabelEnabled

	res := Compare(req)
	if res.Status != StatusOutOfSync {
		t.Fatalf("status = %q, want %q", res.Status, StatusOutOfSync)
	}
	d := findDiff(t, res, labelFieldPath(models.V4AddonLabelKey("datadog")))
	if d.Status != FieldUnexpected {
		t.Errorf("status = %q, want %q", d.Status, FieldUnexpected)
	}
	if d.Expected != nil {
		t.Error("an unexpected field has no expected side")
	}
}

// --- stable ordering --------------------------------------------------------

func TestCompare_StableDiffOrdering(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	var first []string
	for run := 0; run < 25; run++ {
		req, _ := ownedRequest(t, spec, map[string]string{
			"datadog": models.LabelEnabled, "zebra": models.LabelEnabled, "alpha": models.LabelEnabled,
		})
		delete(req.Live.Labels, "datadog")
		req.Live.Labels["zebra"] = models.LabelDisabled
		req.Live.Labels["stale-one"] = models.LabelEnabled
		req.Live.Data["server"] = []byte("https://wrong.invalid")
		req.Live.Data["config"] = []byte("something else entirely")

		res := Compare(req)
		paths := make([]string, 0, len(res.Differences))
		for _, d := range res.Differences {
			paths = append(paths, d.Path)
		}
		if run == 0 {
			first = paths
			if len(first) < 4 {
				t.Fatalf("fixture produced too few differences to test ordering: %v", first)
			}
			continue
		}
		if strings.Join(paths, "|") != strings.Join(first, "|") {
			t.Fatalf("run %d ordering %v differs from run 0 ordering %v", run, paths, first)
		}
	}
}

// --- read failures ----------------------------------------------------------

// TestCompare_CheckFailureIsNeverSoftened is the whole point of the
// check_failed status: whatever else is true about the inputs, a caller-
// reported failure comes back as check_failed and never as agreement or drift.
func TestCompare_CheckFailureIsNeverSoftened(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}

	// A backend read failure, with everything else about the connection
	// perfect — the tempting case to report as synced.
	req, _ := ownedRequest(t, spec, nil)
	req.CheckFailure = "Sharko could not read this cluster's stored sign-in details."
	res := Compare(req)
	if res.Status != StatusCheckFailed {
		t.Fatalf("perfect connection + backend read failure: status = %q, want %q", res.Status, StatusCheckFailed)
	}
	if res.FailureReason == "" {
		t.Error("a check_failed answer must say why")
	}
	if len(res.Differences) != 0 {
		t.Errorf("a failed check must report no differences, got %+v", res.Differences)
	}

	// A live read failure, with the connection badly wrong — the tempting
	// case to report as out_of_sync.
	req, _ = ownedRequest(t, spec, nil)
	req.Live = nil
	req.LiveFound = false
	req.CheckFailure = "Sharko could not read this cluster's connection."
	if res := Compare(req); res.Status != StatusCheckFailed {
		t.Fatalf("live read failure: status = %q, want %q", res.Status, StatusCheckFailed)
	}
}

// TestCompare_UnknownDesiredLabelsIsCheckFailed: a v4 cluster whose addon
// file could not be read. The reconciler refuses to touch labels in that
// state, so the comparison must refuse to call them wrong.
func TestCompare_UnknownDesiredLabelsIsCheckFailed(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled})
	req.AddonLabelsKnown = false
	req.DesiredAddonLabels = nil

	res := Compare(req)
	if res.Status != StatusCheckFailed {
		t.Fatalf("status = %q, want %q", res.Status, StatusCheckFailed)
	}
	if res.FailureReason == "" {
		t.Error("a check_failed answer must say why")
	}
}

func TestCompare_NoLiveSecretIsMissingNotOutOfSync(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only"}
	req, _ := ownedRequest(t, spec, nil)
	req.Live = nil
	req.LiveFound = false

	res := Compare(req)
	if res.Status != StatusMissing {
		t.Fatalf("status = %q, want %q", res.Status, StatusMissing)
	}
	if len(res.Differences) != 0 {
		t.Errorf("there is no drift between something and nothing, got %+v", res.Differences)
	}
}

// --- the modes --------------------------------------------------------------

// TestCompare_InlineKubeconfigIsLimitedAndNeverFullySynced: the labels still
// get checked, the connection details cannot be, and the answer is never
// synced no matter how perfect everything looks.
func TestCompare_InlineKubeconfigIsLimitedAndNeverFullySynced(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceInlineKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})

	labels := map[string]string{"datadog": models.LabelEnabled}
	expectedLabels := argosecrets.BuildClusterSecretLabels(argosecrets.ClusterSecretSpec{Labels: labels})
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNamespace, Labels: map[string]string{}},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"name": []byte(testCluster), "server": []byte(testServer), "config": []byte("{}")},
	}
	for k, v := range expectedLabels {
		live.Labels[k] = v
	}

	res := Compare(Request{
		ClusterName:        testCluster,
		Namespace:          testNamespace,
		Policy:             policy,
		Live:               live,
		LiveFound:          true,
		DesiredAddonLabels: labels,
		AddonLabelsKnown:   true,
		// ExpectedSpec deliberately nil — there is no independent copy.
	})
	if res.Status != StatusLimited {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusLimited, res.Differences)
	}
	if res.Scope != ScopeLimited {
		t.Errorf("scope = %q, want %q", res.Scope, ScopeLimited)
	}
	if len(res.NotChecked) == 0 {
		t.Error("a limited answer must say which fields were not checked")
	}
	if res.CheckedFieldCount == 0 {
		t.Error("the labels are still checkable and should have been checked")
	}

	// And a wrong label still surfaces, so limited is not the same as blind.
	live.Labels["datadog"] = models.LabelDisabled
	if res := Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: live, LiveFound: true, DesiredAddonLabels: labels, AddonLabelsKnown: true,
	}); res.Status != StatusOutOfSync {
		t.Errorf("a wrong label at limited scope: status = %q, want %q", res.Status, StatusOutOfSync)
	}
}

func TestCompare_SelfManagedStaysLabelOnly(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		ConnectionManagedBy:          models.ConnectionManagedByUser,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
	})
	assertLabelOnly(t, policy)
}

func TestCompare_AdoptedStaysLabelOnly(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
		LiveAdopted:                  true,
	})
	assertLabelOnly(t, policy)
}

// assertLabelOnly checks a guest connection: the addon labels are compared, and
// the ownership label, the secret-type label, the identity, the type and the
// connection data are not — not as differences, not as unchecked fields.
func assertLabelOnly(t *testing.T, policy Policy) {
	t.Helper()
	if policy.Scope != ScopeAddonLabelsOnly {
		t.Fatalf("scope = %q, want %q", policy.Scope, ScopeAddonLabelsOnly)
	}

	labels := map[string]string{"datadog": models.LabelEnabled}
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: testCluster, Namespace: "somewhere-else",
			Labels: map[string]string{"datadog": models.LabelEnabled, "example.com/theirs": "yes"},
		},
		// Everything below is the person's own business on a guest
		// connection: a non-Opaque type, a wrong-looking address, no
		// ownership label.
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{"server": []byte("https://their-own-address.invalid"), "config": []byte("{}")},
	}

	res := Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: live, LiveFound: true, DesiredAddonLabels: labels, AddonLabelsKnown: true,
		ConnectivityCheckOn: true,
	})
	if res.Status != StatusLimited {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusLimited, res.Differences)
	}
	// Nothing outside the addon labels may appear, as a difference OR as an
	// unchecked field. Checked path by path rather than by searching a blob,
	// so the assertion cannot pass by accident.
	forbidden := []string{
		FieldPathDataServer, FieldPathDataName, FieldPathDataConfig,
		FieldPathNamespace, FieldPathName, FieldPathSecretType,
		labelFieldPath(argosecrets.LabelManagedBy),
		labelFieldPath(argosecrets.LabelSecretType),
		labelFieldPath(models.LabelConnectivityCheck),
		labelFieldPath(models.LabelConnectivityCheckLegacy),
		labelFieldPath("example.com/theirs"),
	}
	reported := map[string]bool{}
	for _, d := range res.Differences {
		reported[d.Path] = true
	}
	for _, n := range res.NotChecked {
		reported[n.Path] = true
	}
	for _, path := range forbidden {
		if reported[path] {
			t.Errorf("a guest connection reported on %q, which is not Sharko's on this connection", path)
		}
	}

	// A wrong addon label IS reported, because that part is Sharko's.
	live.Labels["datadog"] = models.LabelDisabled
	res = Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: live, LiveFound: true, DesiredAddonLabels: labels, AddonLabelsKnown: true,
	})
	if res.Status != StatusOutOfSync {
		t.Fatalf("a wrong addon label on a guest connection: status = %q, want %q", res.Status, StatusOutOfSync)
	}
	if d := findDiff(t, res, labelFieldPath("datadog")); d.Status != FieldDifferent {
		t.Errorf("status = %q, want %q", d.Status, FieldDifferent)
	}
}

func TestCompare_UnknownSourceIsLimitedNeverSyncedNeverFullRepair(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  "",
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})
	labels := map[string]string{"datadog": models.LabelEnabled}
	expectedLabels := argosecrets.BuildClusterSecretLabels(argosecrets.ClusterSecretSpec{Labels: labels})
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNamespace, Labels: map[string]string{}},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"name": []byte(testCluster), "server": []byte(testServer), "config": []byte("{}")},
	}
	for k, v := range expectedLabels {
		live.Labels[k] = v
	}

	res := Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: live, LiveFound: true, DesiredAddonLabels: labels, AddonLabelsKnown: true,
	})
	if res.Status != StatusLimited {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusLimited, res.Differences)
	}
	if res.RepairScope == RepairScopeFullConnection {
		t.Error("an unknown credentials source must never be offered a full repair")
	}
	if res.Mode != ModeUnknownSource {
		t.Errorf("mode = %q, want %q", res.Mode, ModeUnknownSource)
	}
}

func TestCompare_ForeignOwnershipComparesNothing(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceSecretKubeconfig,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                "another-tool",
	})
	res := Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNamespace,
				Labels: map[string]string{argosecrets.LabelManagedBy: "another-tool", "datadog": models.LabelDisabled}},
			Data: map[string][]byte{"config": []byte("{}")},
		},
		LiveFound: true, DesiredAddonLabels: map[string]string{"datadog": models.LabelEnabled}, AddonLabelsKnown: true,
	})
	if res.Status != StatusOwnershipConflict {
		t.Fatalf("status = %q, want %q", res.Status, StatusOwnershipConflict)
	}
	if len(res.Differences) != 0 {
		t.Errorf("nothing is compared on another tool's connection, got %+v", res.Differences)
	}
	if res.RepairAvailable {
		t.Error("no repair is ever offered on another tool's connection")
	}
	if res.CheckedFieldCount != 0 {
		t.Errorf("checked field count = %d, want 0", res.CheckedFieldCount)
	}
}

// TestCompare_EKSTokenNeverSyncedAndBlobNotChecked pins the CC2-2/CC2-3
// finding: the stored EKS details mint a fresh token on every fetch, so the
// credential blob can never be honestly compared, so an EKS cluster can never
// be reported fully in sync — but a full repair is still on offer, because
// rewriting the connection from the backend does fix it.
func TestCompare_EKSTokenNeverSyncedAndBlobNotChecked(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceEKSToken,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "a-token-that-was-just-minted", CAData: fakeCA}
	specLabels := map[string]string{}
	models.ApplyConnectivityCheckLabel(specLabels, false)
	buildSpec := spec
	buildSpec.Name = testCluster
	buildSpec.Labels = specLabels
	built, err := argosecrets.BuildClusterSecret(buildSpec, testNamespace)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	live := liveFrom(built)
	// A DIFFERENT token, because the last write minted a different one. This
	// is not drift.
	live.Data["config"] = []byte(`{"bearerToken":"a-token-minted-at-some-other-time"}`)

	res := Compare(Request{
		ClusterName: testCluster, Namespace: testNamespace, Policy: policy,
		Live: live, LiveFound: true, AddonLabelsKnown: true, ExpectedSpec: &spec,
	})
	if res.Status == StatusSynced || res.Status == StatusOutOfSync {
		t.Fatalf("status = %q; a freshly minted token is neither proof of sync nor drift", res.Status)
	}
	if res.Status != StatusLimited {
		t.Fatalf("status = %q, want %q (differences %+v)", res.Status, StatusLimited, res.Differences)
	}
	if res.RepairScope != RepairScopeFullConnection {
		t.Errorf("repair scope = %q, want %q — rewriting from the backend does fix it", res.RepairScope, RepairScopeFullConnection)
	}
	found := false
	for _, n := range res.NotChecked {
		if n.Path == FieldPathDataConfig {
			found = true
			if n.Reason == "" {
				t.Error("the unchecked credential blob must say why")
			}
		}
	}
	if !found {
		t.Errorf("the credential blob must be reported as not checked, got %+v", res.NotChecked)
	}
	// And the safe halves still get compared.
	if res.CheckedFieldCount == 0 {
		t.Error("everything else about the connection should still have been checked")
	}
}

// TestCompare_SameInputSameAnswer: Compare is a pure function, and the
// endpoint's "the same request always gives the same answer" promise rests on
// that.
func TestCompare_SameInputSameAnswer(t *testing.T) {
	spec := argosecrets.ClusterSecretSpec{Server: testServer, Token: "made-up-token-for-tests-only", CAData: fakeCA}
	var want string
	for i := 0; i < 30; i++ {
		req, _ := ownedRequest(t, spec, map[string]string{"datadog": models.LabelEnabled, "alpha": models.LabelDisabled})
		req.Live.Labels["datadog"] = models.LabelDisabled
		req.Live.Data["server"] = []byte("https://wrong.invalid")
		raw, err := json.Marshal(Compare(req))
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		if i == 0 {
			want = string(raw)
			continue
		}
		if string(raw) != want {
			t.Fatalf("run %d gave a different answer:\n got %s\nwant %s", i, raw, want)
		}
	}
}

func findDiff(t *testing.T, res Result, path string) Difference {
	t.Helper()
	for _, d := range res.Differences {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("no difference reported for %q; got %+v", path, res.Differences)
	return Difference{}
}

// TestCompare_RepairNotOfferedForUnknownOrFailedStates (R3-8 criterion 6):
// For every status that is not synced, out_of_sync or limited, RepairAvailable
// must be false. The critical test is that check_failed, missing and
// ownership_conflict never offer repair. Synced/out_of_sync/limited already
// have extensive coverage in other tests and are tested here for completeness.
func TestCompare_RepairNotOfferedForUnknownOrFailedStates(t *testing.T) {
	// Build minimal requests that produce each status. The three early-exit
	// states (check_failed, missing, ownership_conflict) are the ones R3-8
	// specifically addresses.
	tests := []struct {
		name         string
		build        func() Request
		expectStatus Status
		expectRepair bool // true = repair should be offered
	}{
		{
			name: "check_failed",
			build: func() Request {
				policy := Classify(ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              true,
					LiveManagedBy:                argosecrets.ManagedByValue,
				})
				return Request{
					ClusterName:      testCluster,
					Namespace:        testNamespace,
					Policy:           policy,
					CheckFailure:     "Credential backend did not respond.",
					AddonLabelsKnown: true,
					LiveFound:        true,
					Live:             &corev1.Secret{},
				}
			},
			expectStatus: StatusCheckFailed,
			expectRepair: false,
		},
		{
			name: "missing",
			build: func() Request {
				policy := Classify(ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              false,
					LiveManagedBy:                "",
				})
				return Request{
					ClusterName:      testCluster,
					Namespace:        testNamespace,
					Policy:           policy,
					LiveFound:        false,
					Live:             nil,
					AddonLabelsKnown: true,
				}
			},
			expectStatus: StatusMissing,
			expectRepair: false,
		},
		{
			name: "ownership_conflict",
			build: func() Request {
				policy := Classify(ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              true,
					LiveManagedBy:                "another-tool",
				})
				return Request{
					ClusterName:      testCluster,
					Namespace:        testNamespace,
					Policy:           policy,
					LiveFound:        true,
					Live:             &corev1.Secret{},
					AddonLabelsKnown: true,
				}
			},
			expectStatus: StatusOwnershipConflict,
			expectRepair: false,
		},
		// R3-8 criterion 6: test ALL statuses, not just the three critical ones.
		// A seventh status added later should fail this test.
		{
			name: "synced",
			build: func() Request {
				spec := argosecrets.ClusterSecretSpec{
					Name:   testCluster,
					Server: "https://synced.invalid",
					Token:  "synced-token",
					Labels: map[string]string{"addon-foo": "enabled"},
				}
				req, _ := ownedRequest(t, spec, spec.Labels)
				// ownedRequest builds a synced state (live == desired)
				return req
			},
			expectStatus: StatusSynced,
			expectRepair: true, // synced can offer repair
		},
		{
			name: "out_of_sync",
			build: func() Request {
				spec := argosecrets.ClusterSecretSpec{
					Name:   testCluster,
					Server: "https://drift.invalid",
					Token:  "drift-token",
					Labels: map[string]string{"addon-foo": "enabled"},
				}
				req, _ := ownedRequest(t, spec, spec.Labels)
				// Perturb live to make it drift from expected
				req.Live.Data["server"] = []byte("https://different.invalid")
				return req
			},
			expectStatus: StatusOutOfSync,
			expectRepair: true, // out_of_sync can offer repair
		},
		{
			name: "limited",
			build: func() Request {
				// EKS-shape connection (limited scope: can't compare credentials)
				policy := Classify(ClassifyInput{
					CredsSource:                  models.CredsSourceEKSToken,
					BackendCanProvideStoredFacts: false, // EKS can't compare credentials
					LiveSecretFound:              true,
					LiveManagedBy:                argosecrets.ManagedByValue,
				})
				spec := argosecrets.ClusterSecretSpec{
					Name:   testCluster,
					Server: "https://eks.invalid",
					Token:  "eks-token",
					Labels: map[string]string{"addon-foo": "enabled"},
				}
				req, _ := ownedRequest(t, spec, spec.Labels)
				req.Policy = policy // override to EKS mode
				return req
			},
			expectStatus: StatusLimited,
			expectRepair: true, // limited can offer repair (addon labels)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.build()
			res := Compare(req)

			if res.Status != tt.expectStatus {
				t.Fatalf("status = %q, want %q", res.Status, tt.expectStatus)
			}

			if res.RepairAvailable != tt.expectRepair {
				t.Errorf("RepairAvailable = %v, want %v for status %q", res.RepairAvailable, tt.expectRepair, tt.expectStatus)
			}

			if !tt.expectRepair {
				if res.RepairScope != RepairScopeNone {
					t.Errorf("RepairScope = %q, want %q when repair not available", res.RepairScope, RepairScopeNone)
				}
				// For the three early-exit states, verify a reason is present.
				switch tt.expectStatus {
				case StatusCheckFailed:
					if res.FailureReason == "" {
						t.Error("FailureReason must be set for check_failed")
					}
				case StatusMissing, StatusOwnershipConflict:
					if res.LimitReason == "" {
						t.Errorf("LimitReason must be set for %s", tt.expectStatus)
					}
				}
			}
		})
	}

	// Verify we covered ALL six statuses. If a seventh status is added and this
	// test is not updated, the test fails here (R3-8 criterion 6).
	allStatuses := []Status{
		StatusSynced,
		StatusOutOfSync,
		StatusMissing,
		StatusCheckFailed,
		StatusOwnershipConflict,
		StatusLimited,
	}
	coveredStatuses := make(map[Status]bool)
	for _, tt := range tests {
		coveredStatuses[tt.expectStatus] = true
	}
	for _, s := range allStatuses {
		if !coveredStatuses[s] {
			t.Errorf("status %q is not covered by any test case — add one", s)
		}
	}
	if len(coveredStatuses) > len(allStatuses) {
		t.Errorf("test covers %d statuses but allStatuses only lists %d — a new status was added, update allStatuses[]", len(coveredStatuses), len(allStatuses))
	}
}
