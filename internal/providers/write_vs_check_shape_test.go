package providers

// write_vs_check_shape_test.go — R3-14: the two reads a cluster's credentials can
// come through produce two DIFFERENT connection shapes for the same stored EKS
// payload, and only one of them mints.
//
// This is the level the bug actually lived at. A repair built its spec from the
// read-only stored-facts route, which returns no token for an EKS payload, so
// argosecrets.buildSecretConfig's precedence (cert pair > token > exec) fell
// through to the execProviderConfig shape — while every normal Sharko write for
// that same cluster minted a token and produced the bearerToken shape. Clicking
// repair silently changed how ArgoCD signs in.
//
// The two reads must stay different. The check must never mint; the write must.
// So this test pins BOTH halves against one payload, with one counter.

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
)

// specFromWriteRead assembles the credential half of a connection spec the way
// every WRITER does — the reconcile pass and an asked-for repair both go through
// clusterreconciler.ConnectionCredentialSpecForWrite, which maps a Kubeconfig
// onto these fields exactly like this.
//
// It is spelled out here rather than imported because internal/providers must not
// depend on a package that depends on it. What is being proved is that the read
// route decides the shape, and for that the mapping only has to be faithful — if
// it ever stops being faithful, the writer's own tests in clusterreconciler catch
// it.
func specFromWriteRead(cluster string, kc *Kubeconfig, region, roleARN string) argosecrets.ClusterSecretSpec {
	return argosecrets.ClusterSecretSpec{
		Name:     cluster,
		Server:   kc.Server,
		Region:   region,
		RoleARN:  roleARN,
		Token:    kc.Token,
		CertData: base64.StdEncoding.EncodeToString(kc.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(kc.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(kc.CAData),
	}
}

// specFromCheckRead assembles the same half from the read-only stored-facts
// route, the way the comparison endpoint does.
func specFromCheckRead(cluster string, facts *StoredConnectionFacts, region, roleARN string) argosecrets.ClusterSecretSpec {
	return argosecrets.ClusterSecretSpec{
		Name:     cluster,
		Server:   facts.Server,
		Region:   region,
		RoleARN:  roleARN,
		Token:    facts.Token,
		CertData: base64.StdEncoding.EncodeToString(facts.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(facts.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(facts.CAData),
	}
}

// configShape reports which authentication method a built connection Secret's
// data.config carries, and its top-level keys.
//
// It reads the SHAPE and never a value: the method name comes from which keys are
// present, and the key list is JSON field names. The token itself changes on
// every mint, so there is nothing to compare there anyway — and nothing to print.
func configShape(t *testing.T, built map[string]string) (method string, keys []string) {
	t.Helper()
	raw, ok := built["config"]
	if !ok {
		t.Fatal("the built connection Secret has no data.config")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("data.config is not JSON: %v", err)
	}
	for k := range cfg {
		keys = append(keys, k)
	}
	switch {
	case cfg["execProviderConfig"] != nil:
		method = "execProviderConfig"
	case cfg["bearerToken"] != nil:
		method = "bearerToken"
	default:
		method = "tlsClientConfig-only"
	}
	return method, keys
}

// TestEKSPayload_WriteReadMintsAndGivesBearerToken_CheckReadDoesNotAndGivesExec
// is the R3-14 shape proof.
func TestEKSPayload_WriteReadMintsAndGivesBearerToken_CheckReadDoesNotAndGivesExec(t *testing.T) {
	const (
		cluster = "prod-eu"
		region  = "eu-west-1"
		role    = "arn:aws:iam::000000000000:role/test-role"
	)

	// ── The WRITE read: the normal backend fetch, the same route the reconcile
	// pass uses. One provider, one mint counter.
	writeMint := &mintCounter{}
	writeProvider := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: structuredEKSPayloadWithSentinel()},
		eksTokenFn: writeMint.fn,
	}

	kc, err := GetCredentialsWithOptionalRole(writeProvider, cluster, role)
	if err != nil {
		t.Fatalf("the write route failed to fetch credentials: %v", err)
	}
	if writeMint.calls != 1 {
		t.Fatalf(`the write route minted %d sign-in token(s); it must mint exactly ONE.

A write needs credentials that work. For a stored EKS payload the backend holds metadata and not a credential, so a write has to mint one — but only one, per write.`, writeMint.calls)
	}
	if kc.Token == "" {
		t.Fatal("the write route returned no token; without one the connection falls through to the exec shape, which is the bug this test exists for")
	}

	writeBuilt, err := argosecrets.BuildClusterSecret(specFromWriteRead(cluster, kc, region, role), "argocd")
	if err != nil {
		t.Fatalf("building the connection from the write read: %v", err)
	}
	writeMethod, writeKeys := configShape(t, writeBuilt.StringData)

	// ── The CHECK read: the read-only stored-facts route. Its own counter, and
	// it must stay at zero.
	checkMint := &mintCounter{}
	checkProvider := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: structuredEKSPayloadWithSentinel()},
		eksTokenFn: checkMint.fn,
	}

	facts, err := checkProvider.StoredConnectionFacts(cluster)
	if err != nil {
		t.Fatalf("the check route failed to read stored facts: %v", err)
	}
	if checkMint.calls != 0 {
		t.Fatalf(`the read-only check route minted %d sign-in token(s); it must mint ZERO.

A minted EKS token is a real credential that can sign in as Sharko for as long as it lives. A read must not create one. If this is above zero, something on that path now reaches GetCredentials — find it and take it out; do not raise the expected count.`, checkMint.calls)
	}

	checkBuilt, err := argosecrets.BuildClusterSecret(specFromCheckRead(cluster, facts, region, role), "argocd")
	if err != nil {
		t.Fatalf("building the connection from the check read: %v", err)
	}
	checkMethod, checkKeys := configShape(t, checkBuilt.StringData)

	// ── The shapes.
	if writeMethod != "bearerToken" {
		t.Errorf(`the WRITE read produced the %q shape (top-level keys %v), want "bearerToken".

Every normal Sharko write for an EKS cluster mints a token and produces bearerToken. If a repair produces anything else, clicking repair changes how ArgoCD signs in to that cluster.`, writeMethod, writeKeys)
	}
	if checkMethod != "execProviderConfig" {
		t.Errorf(`the read-only CHECK read produced the %q shape (top-level keys %v), want "execProviderConfig".

This is not a shape anything writes — it is what the no-mint read falls through to, because it returns no token. That is exactly why a repair must not build its spec from this route.`, checkMethod, checkKeys)
	}
	if writeMethod == checkMethod {
		t.Errorf(`both reads produced the same shape (%q), so this test can no longer tell them apart and the R3-14 regression would pass unnoticed.

If the no-mint read has started returning a token, the read-only comparison is now minting credentials. If the write read has stopped returning one, every EKS write has changed its authentication method.`, writeMethod)
	}

	// The mint is once per write and does not grow — a repair runs a fresh
	// comparison after writing, and that comparison must add nothing to this
	// count.
	if _, err := checkProvider.StoredConnectionFacts(cluster); err != nil {
		t.Fatalf("the second check read failed: %v", err)
	}
	if checkMint.calls != 0 {
		t.Errorf("a second read-only read took the mint count to %d; the fresh comparison a repair runs afterwards must add nothing", checkMint.calls)
	}
	if writeMint.calls != 1 {
		t.Errorf("the write route's mint count moved to %d without a second write", writeMint.calls)
	}
}

// TestEKSPayload_NeitherRouteLeaksTheSentinelIntoTheBuiltShapeReport proves the
// shape reporting above cannot become a leak. configShape returns a method name
// and JSON field names; the sentinel lives in the payload's caData, and it must
// not appear in either.
func TestEKSPayload_NeitherRouteLeaksTheSentinelIntoTheBuiltShapeReport(t *testing.T) {
	mint := &mintCounter{}
	p := &AWSSecretsManagerProvider{
		client:     &fakeSMClient{value: structuredEKSPayloadWithSentinel()},
		eksTokenFn: mint.fn,
	}

	kc, err := GetCredentialsWithOptionalRole(p, "prod-eu", "")
	if err != nil {
		t.Fatalf("fetching credentials: %v", err)
	}
	built, err := argosecrets.BuildClusterSecret(specFromWriteRead("prod-eu", kc, "eu-west-1", ""), "argocd")
	if err != nil {
		t.Fatalf("building the connection: %v", err)
	}
	method, keys := configShape(t, built.StringData)

	assertNoEKSSentinel(t, "the reported authentication method", method)
	for _, k := range keys {
		assertNoEKSSentinel(t, "a reported top-level config key", k)
	}
}
