package connectioncompare

// eks_no_credential_test.go — R4-2. The shape production actually produces for
// an EKS connection, tested instead of described in a comment.
//
// The story the code now tells: the configured credentials source stores EKS
// cluster metadata, not a credential. There is no credential on the expected
// side, so there is nothing to compare. A WRITE creates a sign-in token, once. A
// CHECK creates nothing.
//
// providers.StoredConnectionFacts is what the check reads through, and on an EKS
// payload it returns the server and the CA bundle with Token, CertData and
// KeyData all empty. The endpoint builds its expected spec straight from those
// fields, so the spec that reaches Compare has NO credential in it. That is the
// shape here.
//
// No real credential value appears in this file.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// eksNoCredentialSpec is the expected spec an EKS connection really produces:
// the stored metadata's server and CA bundle, and no credential of any kind.
// RoleARN is set because the write path carries it, and it is metadata rather
// than a credential.
func eksNoCredentialSpec() argosecrets.ClusterSecretSpec {
	return argosecrets.ClusterSecretSpec{
		Server:  testServer,
		CAData:  fakeCA,
		RoleARN: "arn:aws:iam::000000000000:role/made-up-for-this-test",
		// Token, CertData, KeyData deliberately empty — the stored payload
		// holds no credential, and the read-only path never makes one.
	}
}

// TestCompare_EKSStoredPayloadHasNoCredentialOnTheExpectedSide is the R4-2
// criterion-6 test.
//
// The credential fields must come back NOT CHECKED. Not `same`, which would
// claim Sharko verified something it never had a value for, and not
// `different`, which would report drift that is not there.
func TestCompare_EKSStoredPayloadHasNoCredentialOnTheExpectedSide(t *testing.T) {
	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceEKSToken,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})

	spec := eksNoCredentialSpec()

	// The live Secret carries a real credential blob, because the last write
	// created one. Nothing on the expected side corresponds to it.
	buildSpec := spec
	buildSpec.Name = testCluster
	specLabels := map[string]string{"datadog": models.LabelEnabled}
	models.ApplyConnectivityCheckLabel(specLabels, false)
	buildSpec.Labels = specLabels
	built, err := argosecrets.BuildClusterSecret(buildSpec, testNamespace)
	if err != nil {
		t.Fatalf("building the live side: %v", err)
	}
	live := liveFrom(built)
	live.Data["config"] = []byte(`{"bearerToken":"a-token-created-by-some-earlier-write"}`)

	res := Compare(Request{
		ClusterName:        testCluster,
		Namespace:          testNamespace,
		Policy:             policy,
		Live:               live,
		LiveFound:          true,
		DesiredAddonLabels: map[string]string{"datadog": models.LabelEnabled},
		AddonLabelsKnown:   true,
		ExpectedSpec:       &spec,
	})

	// data.config is not checked, and it says why.
	var notCheckedConfig *NotCheckedField
	for i := range res.NotChecked {
		if res.NotChecked[i].Path == FieldPathDataConfig {
			notCheckedConfig = &res.NotChecked[i]
		}
	}
	if notCheckedConfig == nil {
		t.Fatalf(`%q is not in NotChecked: %+v.

There is no credential on the expected side, so there is nothing to compare. Saying nothing about the field would leave the user to guess whether it was checked and passed.`, FieldPathDataConfig, res.NotChecked)
	}
	if notCheckedConfig.Reason == "" {
		t.Error("an unchecked credential field must carry a reason")
	}

	// And it is NOT reported as a difference — neither same-by-omission nor
	// different. A difference on this path would be drift that is not there.
	for _, d := range res.Differences {
		if d.Path == FieldPathDataConfig {
			t.Errorf(`%q came back as a difference with status %q.

There was no expected value to compare against, so neither "same" nor "different" is an honest answer. It is a field Sharko did not check.`, d.Path, d.Status)
		}
	}

	// A limited answer, never synced: Sharko did not check the whole
	// connection, so it cannot say the connection is right.
	if res.Status != StatusLimited {
		t.Errorf("status = %q, want %q (differences %+v)", res.Status, StatusLimited, res.Differences)
	}

	// The rest of the connection WAS checked — the point of a narrower scope is
	// that it still answers about the part it can see.
	if res.CheckedFieldCount == 0 {
		t.Error("nothing at all was checked; the identity, type, server and labels are all knowable without a credential")
	}

	// A full repair is still offered, because writing from the backend fixes
	// the connection whatever state it is in.
	if res.RepairScope != RepairScopeFullConnection {
		t.Errorf("repair scope = %q, want %q — rewriting from the backend does fix it", res.RepairScope, RepairScopeFullConnection)
	}
	if !res.RepairAvailable {
		t.Error("a repair must still be on offer for an EKS connection")
	}
}

// TestCompare_EKSNoCredentialAnswerCarriesNoCredentialMaterial is the security
// sentinel for this shape.
//
// One unique fake credential goes in on the LIVE side. It must not come back
// out of the comparison in any form: raw, base64, SHA-256, or as a prefix or
// suffix fragment. No length either — a length is information about a
// credential, and this answer never has a reason to carry one.
func TestCompare_EKSNoCredentialAnswerCarriesNoCredentialMaterial(t *testing.T) {
	const neverShip = "qvx-eks-live-credential-sentinel-never-ship-70413"

	policy := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceEKSToken,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})

	spec := eksNoCredentialSpec()
	buildSpec := spec
	buildSpec.Name = testCluster
	specLabels := map[string]string{}
	models.ApplyConnectivityCheckLabel(specLabels, false)
	buildSpec.Labels = specLabels
	built, err := argosecrets.BuildClusterSecret(buildSpec, testNamespace)
	if err != nil {
		t.Fatalf("building the live side: %v", err)
	}
	live := liveFrom(built)
	live.Data["config"] = []byte(`{"bearerToken":"` + neverShip + `"}`)

	res := Compare(Request{
		ClusterName:      testCluster,
		Namespace:        testNamespace,
		Policy:           policy,
		Live:             live,
		LiveFound:        true,
		AddonLabelsKnown: true,
		ExpectedSpec:     &spec,
	})

	raw, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		t.Fatalf("marshalling the answer: %v", marshalErr)
	}
	answer := string(raw)

	sum := sha256.Sum256([]byte(neverShip))
	forms := map[string]string{
		"raw":            neverShip,
		"base64":         base64.StdEncoding.EncodeToString([]byte(neverShip)),
		"base64 raw-url": base64.RawURLEncoding.EncodeToString([]byte(neverShip)),
		"sha-256 hex":    hex.EncodeToString(sum[:]),
		"sha-256 base64": base64.StdEncoding.EncodeToString(sum[:]),
		"prefix":         neverShip[:12],
		"suffix":         neverShip[len(neverShip)-12:],
	}
	for form, needle := range forms {
		if strings.Contains(answer, needle) {
			t.Errorf("the comparison answer carries the live credential in %s form", form)
		}
	}

	// No length either. The sentinel's length, and the length of the blob it
	// sits inside, are both information about a credential.
	for _, n := range []string{
		itoa(len(neverShip)),
		itoa(len(`{"bearerToken":"` + neverShip + `"}`)),
	} {
		if strings.Contains(answer, n) {
			t.Errorf(`the comparison answer contains %q, which is a length of the live credential.

Lengths are never reported. If this fired on a coincidental match — a field count that happens to equal the length — change the sentinel rather than the rule.`, n)
		}
	}
}

// itoa is strconv.Itoa under a local name so the import list of this file stays
// about credential handling.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
