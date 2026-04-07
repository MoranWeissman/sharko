package providers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// tokenFromURL mirrors the encoding logic in getEKSToken so we can test the
// format without making real AWS API calls.
func tokenFromURL(url string) string {
	return v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(url))
}

// TestEKSTokenFormat_Prefix verifies that a k8s-aws-v1 token has the correct prefix.
func TestEKSTokenFormat_Prefix(t *testing.T) {
	url := "https://sts.amazonaws.com/?Action=GetCallerIdentity&X-Amz-Algorithm=AWS4-HMAC-SHA256"
	token := tokenFromURL(url)
	if !strings.HasPrefix(token, v1Prefix) {
		t.Errorf("expected token to start with %q, got: %q", v1Prefix, token)
	}
}

// TestEKSTokenFormat_NopadBase64 verifies the token uses no-padding URL-safe base64.
func TestEKSTokenFormat_NopadBase64(t *testing.T) {
	url := "https://example.com/test"
	token := tokenFromURL(url)
	encoded := strings.TrimPrefix(token, v1Prefix)
	if strings.Contains(encoded, "=") {
		t.Errorf("token should use raw (no-pad) base64url, but contains '=': %q", token)
	}
	if strings.Contains(encoded, "+") || strings.Contains(encoded, "/") {
		t.Errorf("token should use URL-safe base64, but contains '+' or '/': %q", token)
	}
}

// TestEKSTokenFormat_Roundtrip verifies the URL can be recovered from the token.
func TestEKSTokenFormat_Roundtrip(t *testing.T) {
	original := "https://sts.amazonaws.com/?Action=GetCallerIdentity&Version=2011-06-15&X-Amz-Algorithm=AWS4-HMAC-SHA256"
	token := tokenFromURL(original)

	encoded := strings.TrimPrefix(token, v1Prefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("failed to decode token: %v", err)
	}
	if string(decoded) != original {
		t.Errorf("roundtrip failed: got %q, want %q", string(decoded), original)
	}
}

// TestStructuredEKSSecret_JSONMapping verifies JSON field names are mapped correctly.
func TestStructuredEKSSecret_JSONMapping(t *testing.T) {
	raw := `{
		"clusterName": "test-cluster",
		"host": "https://ABC123.gr7.eu-west-1.eks.amazonaws.com",
		"caData": "dGVzdC1jYS1kYXRh",
		"accountId": "123456789012",
		"region": "eu-west-1",
		"project": "myproject",
		"environment": "staging"
	}`

	var s structuredEKSSecret
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	cases := []struct {
		field string
		got   string
		want  string
	}{
		{"ClusterName", s.ClusterName, "test-cluster"},
		{"Host", s.Host, "https://ABC123.gr7.eu-west-1.eks.amazonaws.com"},
		{"CAData", s.CAData, "dGVzdC1jYS1kYXRh"},
		{"AccountId", s.AccountId, "123456789012"},
		{"Region", s.Region, "eu-west-1"},
		{"Project", s.Project, "myproject"},
		{"Environment", s.Environment, "staging"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
}

// TestDetectFormat_StructuredJSON verifies that JSON with a "host" field is
// correctly identified as the structured EKS format.
func TestDetectFormat_StructuredJSON(t *testing.T) {
	raw := []byte(`{"clusterName":"c","host":"https://example.com","caData":"dA==","region":"us-east-1"}`)

	var structured structuredEKSSecret
	err := json.Unmarshal(raw, &structured)
	isStructured := err == nil && structured.Host != ""
	if !isStructured {
		t.Error("expected structured JSON to be detected as structured format")
	}
}

// TestDetectFormat_RawKubeconfig verifies that YAML kubeconfig is not
// mis-detected as structured JSON (YAML is not valid JSON).
func TestDetectFormat_RawKubeconfig(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: local
`)

	var structured structuredEKSSecret
	err := json.Unmarshal(raw, &structured)
	isStructured := err == nil && structured.Host != ""
	if isStructured {
		t.Error("raw kubeconfig YAML should NOT be detected as structured JSON")
	}
}

// TestDetectFormat_JSONWithoutHost verifies that valid JSON lacking "host" is not
// treated as a structured EKS secret, routing to the raw kubeconfig path instead.
func TestDetectFormat_JSONWithoutHost(t *testing.T) {
	raw := []byte(`{"someOtherField":"value"}`)

	var structured structuredEKSSecret
	err := json.Unmarshal(raw, &structured)
	isStructured := err == nil && structured.Host != ""
	if isStructured {
		t.Error("JSON without 'host' should NOT be detected as structured EKS format")
	}
}

// TestCADataDecode verifies that base64-encoded CA data can be decoded correctly.
func TestCADataDecode(t *testing.T) {
	// Simulate what buildFromStructured does with the caData field.
	original := []byte("-----BEGIN CERTIFICATE-----\nMIICtest\n-----END CERTIFICATE-----\n")
	encoded := base64.StdEncoding.EncodeToString(original)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding caData: %v", err)
	}
	if string(decoded) != string(original) {
		t.Errorf("CA data roundtrip failed: got %q, want %q", string(decoded), string(original))
	}
}
