package orchestrator

import (
	"strings"
	"testing"
)

// These tests cover keys a reviewer proved were printed in clear in a
// preview diff, because the old matcher only knew exact names and the
// suffixes _token, _password, _secret and _key. Each case is checked on a
// v3 values path, both v4 values paths, and the same paths written with a
// leading "/" or "./" (a leading "/" used to skip redaction completely).

// redactedLine is the exact text a hidden value is shown as.
const redactedLine = "<redacted>"

func leakTestPaths() []string {
	return []string{
		"configuration/addons-clusters-values/prod-eu.yaml",
		V4ClusterValuesDir + "/prod-eu/datadog.yaml",
		V4GlobalValuesDir + "/datadog.yaml",
		"/" + V4ClusterValuesDir + "/prod-eu/datadog.yaml",
		"/" + V4GlobalValuesDir + "/datadog.yaml",
		"./" + V4ClusterValuesDir + "/prod-eu/datadog.yaml",
		"/configuration/addons-clusters-values/prod-eu.yaml",
		// A path this code does not know at all must be redacted too.
		"some/other/place/values.yaml",
	}
}

func TestBuildFileDiff_SecretKeysNeverLeak(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		secrets  []string
		wantLine string // the exact "+..." line the diff must hold
	}{
		{"adminPassword", "adminPassword: S3CRET9\n", []string{"S3CRET9"}, "+adminPassword: <redacted>"},
		{"clientSecret", "clientSecret: S3CRETA\n", []string{"S3CRETA"}, "+clientSecret: <redacted>"},
		{"secretKey", "secretKey: S3CRETB\n", []string{"S3CRETB"}, "+secretKey: <redacted>"},
		{"accessKey", "accessKey: S3CRETC\n", []string{"S3CRETC"}, "+accessKey: <redacted>"},
		{"appKey", "appKey: S3CRETD\n", []string{"S3CRETD"}, "+appKey: <redacted>"},
		{"client-secret", "client-secret: S3CRETE\n", []string{"S3CRETE"}, "+client-secret: <redacted>"},
		{"api-key", "api-key: S3CRETF\n", []string{"S3CRETF"}, "+api-key: <redacted>"},
		{"apiKeyValue", "apiKeyValue: S3CRETH\n", []string{"S3CRETH"}, "+apiKeyValue: <redacted>"},
		{"dotted db.password", "\"db.password\": S3CRETS\n", []string{"S3CRETS"}, "+\"db.password\": <redacted>"},
		{"dotted datadog.apiKey", "\"datadog.apiKey\": S3CRETI\n", []string{"S3CRETI"}, "+\"datadog.apiKey\": <redacted>"},
		{"dotted tls.key", "\"tls.key\": S3CRETJ\n", []string{"S3CRETJ"}, "+\"tls.key\": <redacted>"},
		{"nested tls key", "tls:\n  key: S3CRETK\n  crt: public-cert\n", []string{"S3CRETK"}, "+  key: <redacted>"},
		{"map under secret", "secret:\n  value: S3CRETG\n", []string{"S3CRETG", "value:"}, "+secret: <redacted>"},
		{"list under credentials", "credentials:\n  - S3CRETL\n  - S3CRETM\n", []string{"S3CRETL", "S3CRETM"}, "+credentials: <redacted>"},
		{"map under auth", "auth:\n  user: admin\n  pass: S3CRETN\n", []string{"S3CRETN", "user:"}, "+auth: <redacted>"},
		{"deep map under clientSecret", "oidc:\n  clientSecret:\n    inner:\n      v: S3CRETO\n", []string{"S3CRETO", "inner:"}, "+  clientSecret: <redacted>"},
		{"connectionString", "connectionString: Server=db;Pwd=S3CRETP\n", []string{"S3CRETP"}, "+connectionString: <redacted>"},
		{"dsn", "sentryDsn: https://S3CRETQ@sentry.example.com/1\n", []string{"S3CRETQ"}, "+sentryDsn: <redacted>"},
		{"privateKey", "privateKey: S3CRETR\n", []string{"S3CRETR"}, "+privateKey: <redacted>"},
		{"passwd", "db_passwd: S3CRETT\n", []string{"S3CRETT"}, "+db_passwd: <redacted>"},
		{"bearer", "bearerValue: S3CRETU\n", []string{"S3CRETU"}, "+bearerValue: <redacted>"},
		{"camel Key", "encryptionKey: S3CRETV\n", []string{"S3CRETV"}, "+encryptionKey: <redacted>"},
		{"client-key-data", "client-key-data: S3CRETW\n", []string{"S3CRETW"}, "+client-key-data: <redacted>"},
		{"spaces in key", "\"Admin Password\": S3CRETX\n", []string{"S3CRETX"}, "+\"Admin Password\": <redacted>"},
	}

	o := &Orchestrator{paths: RepoPathsConfig{
		ClusterValues: "configuration/addons-clusters-values",
		GlobalValues:  "configuration/addons-global-values",
	}}

	for _, tt := range tests {
		for _, p := range leakTestPaths() {
			t.Run(tt.name+" "+p, func(t *testing.T) {
				diff := o.buildFileDiff(p, nil, []byte(tt.content), "create")
				for _, s := range tt.secrets {
					if strings.Contains(diff, s) {
						t.Errorf("diff shows %q in clear:\n%s", s, diff)
					}
				}
				if !containsLine(diff, tt.wantLine) {
					t.Errorf("diff is missing the exact line %q:\n%s", tt.wantLine, diff)
				}
			})
		}
	}
}

// A secret in the OLD side of an update is hidden too.
func TestBuildFileDiff_SecretKeysNeverLeak_OldSide(t *testing.T) {
	o := &Orchestrator{}
	diff := o.buildFileDiff("/"+V4ClusterValuesDir+"/prod-eu/datadog.yaml",
		[]byte("adminPassword: OLDS3CRET\nsecret:\n  value: OLDS3CRETG\n"),
		[]byte("adminPassword: NEWS3CRET\n"), "update")
	for _, s := range []string{"OLDS3CRET", "OLDS3CRETG", "NEWS3CRET"} {
		if strings.Contains(diff, s) {
			t.Errorf("diff shows %q in clear:\n%s", s, diff)
		}
	}
	if !containsLine(diff, "-secret: <redacted>") {
		t.Errorf("diff is missing the exact line %q:\n%s", "-secret: <redacted>", diff)
	}
}

// The redaction placeholder, pinned by exact text.
func TestRedactedPlaceholder_ExactText(t *testing.T) {
	if redactedPlaceholder != redactedLine {
		t.Fatalf("redactedPlaceholder = %q, want %q", redactedPlaceholder, redactedLine)
	}
}

// Normal values next to the secrets are still shown, so the change stays
// readable.
func TestBuildFileDiff_PlainValuesStillShown(t *testing.T) {
	o := &Orchestrator{}
	diff := o.buildFileDiff("/"+V4ClusterValuesDir+"/prod-eu/datadog.yaml", nil,
		[]byte("region: eu-west-1\nreplicas: 3\nimage:\n  repository: nginx\n  tag: \"1.27\"\nadminPassword: S3CRET9\n"), "create")
	for _, line := range []string{"+region: eu-west-1", "+replicas: 3", "+  repository: nginx", "+adminPassword: <redacted>"} {
		if !containsLine(diff, line) {
			t.Errorf("diff is missing the exact line %q:\n%s", line, diff)
		}
	}
}

// The files Sharko knows hold no secrets are shown as they are, also when
// the path is written with a leading "/".
func TestRedactValuesContent_PlainFilesUnchanged(t *testing.T) {
	o := &Orchestrator{paths: RepoPathsConfig{
		Catalog:         "configuration/addons-catalog.yaml",
		ManagedClusters: "configuration/managed-clusters.yaml",
	}}
	content := []byte("# a comment\naddons:\n  - name: nginx\n    version: 1.0.0\n")
	for _, p := range []string{
		"catalog.yaml", "/catalog.yaml", "managed-clusters.yaml", "sharko-engine.yaml",
		"cluster-addons/prod-eu.yaml", "/cluster-addons/prod-eu.yaml",
		"configuration/addons-catalog.yaml", "configuration/managed-clusters.yaml", "README.md",
	} {
		if got := o.redactValuesContent(p, content); string(got) != string(content) {
			t.Errorf("%s: plain file was changed:\n%s", p, got)
		}
	}
	// Not plain: a deeper path under cluster-addons, or a path that only
	// looks like a plain file.
	for _, p := range []string{"cluster-addons/prod-eu/x.yaml", "catalog.yaml.bak", "x/catalog.yaml"} {
		if got := o.redactValuesContent(p, content); string(got) == string(content) {
			t.Errorf("%s: an unknown path was shown as it is", p)
		}
	}
}

func TestIsSensitiveKey_NewRules(t *testing.T) {
	for _, k := range []string{
		"adminPassword", "clientSecret", "secretKey", "accessKey", "appKey",
		"client-secret", "api-key", "apiKeyValue", "db.password", "datadog.apiKey",
		"tls.key", "connectionString", "sentryDsn", "privateKey", "passwd",
		"credentials", "auth", "oauth", "bearer", "encryptionKey", "client-key-data",
		"existingSecret", "Admin Password",
	} {
		if !isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{
		"region", "enabled", "namespace", "replicas", "host", "port", "image",
		"repository", "tag", "nodeSelector", "tolerations", "resources",
		"clusterName", "ingress.host", "path", "pattern", "",
	} {
		if isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = true, want false", k)
		}
	}
}

func containsLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}
