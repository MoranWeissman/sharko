package orchestrator

import (
	"strings"
	"testing"
)

func TestRedactValuesContent(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		content      []byte
		clusterPath  string
		globalPath   string
		expectChange bool
		mustNotFind  []string
	}{
		{
			name:         "values file with inline secret",
			path:         "configuration/addons-clusters-values/cluster1.yaml",
			content:      []byte("password: hunter2\ntoken: abc123\napi:\n  key: secret456"),
			clusterPath:  "configuration/addons-clusters-values",
			globalPath:   "configuration/addons-global-values",
			expectChange: true,
			mustNotFind:  []string{"hunter2", "abc123", "secret456"},
		},
		{
			name:         "global values file with secret",
			path:         "configuration/addons-global-values/addon1.yaml",
			content:      []byte("database:\n  password: supersecret\n  host: db.example.com"),
			clusterPath:  "configuration/addons-clusters-values",
			globalPath:   "configuration/addons-global-values",
			expectChange: true,
			mustNotFind:  []string{"supersecret"},
		},
		{
			name:         "catalog file unchanged",
			path:         "configuration/addons-catalog.yaml",
			content:      []byte("addons:\n  - name: nginx\n    version: 1.0.0"),
			clusterPath:  "configuration/addons-clusters-values",
			globalPath:   "configuration/addons-global-values",
			expectChange: false,
			mustNotFind:  nil,
		},
		{
			name:         "managed-clusters file unchanged",
			path:         "configuration/managed-clusters.yaml",
			content:      []byte("clusters:\n  - name: prod\n    region: us-east-1"),
			clusterPath:  "configuration/addons-clusters-values",
			globalPath:   "configuration/addons-global-values",
			expectChange: false,
			mustNotFind:  nil,
		},
		{
			name:         "unparseable values file",
			path:         "configuration/addons-clusters-values/bad.yaml",
			content:      []byte("not: valid: yaml: content"),
			clusterPath:  "configuration/addons-clusters-values",
			globalPath:   "configuration/addons-global-values",
			expectChange: true,
			mustNotFind:  []string{"valid: yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{
				paths: RepoPathsConfig{
					ClusterValues: tt.clusterPath,
					GlobalValues:  tt.globalPath,
				},
			}

			result := o.redactValuesContent(tt.path, tt.content)

			if tt.expectChange {
				if string(result) == string(tt.content) {
					t.Errorf("expected content to be redacted, but it was unchanged")
				}
				if !strings.Contains(string(result), "redacted") {
					t.Errorf("expected result to contain 'redacted', got: %s", string(result))
				}
			} else {
				if string(result) != string(tt.content) {
					t.Errorf("expected content to be unchanged, but it was modified")
				}
			}

			for _, forbidden := range tt.mustNotFind {
				if strings.Contains(string(result), forbidden) {
					t.Errorf("result contains forbidden value %q: %s", forbidden, string(result))
				}
			}
		})
	}
}

func TestBuildFileDiff(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		oldContent  []byte
		newContent  []byte
		action      string
		mustContain []string
		mustNotFind []string
	}{
		{
			name:        "create action shows all additions",
			path:        "configuration/addons-catalog.yaml",
			oldContent:  nil,
			newContent:  []byte("addons:\n  - name: nginx"),
			action:      "create",
			mustContain: []string{"+addons:", "+  - name: nginx"},
			mustNotFind: nil,
		},
		{
			name:        "delete action shows all removals",
			path:        "configuration/addons-clusters-values/cluster1.yaml",
			oldContent:  []byte("password: hunter2"),
			newContent:  nil,
			action:      "delete",
			mustContain: []string{"-password: <redacted>"},
			mustNotFind: []string{"hunter2"},
		},
		{
			name:        "update action shows mixed changes",
			path:        "configuration/addons-catalog.yaml",
			oldContent:  []byte("version: 1.0.0\nname: app"),
			newContent:  []byte("version: 2.0.0\nname: app"),
			action:      "update",
			mustContain: []string{"-version: 1.0.0", "+version: 2.0.0"},
			mustNotFind: nil,
		},
		{
			name:        "values file redacts secrets but reveals safe values",
			path:        "configuration/addons-clusters-values/cluster1.yaml",
			oldContent:  []byte("password: hunter2"),
			newContent:  []byte("password: newsecret\nregion: eu-west-1"),
			action:      "update",
			mustContain: []string{"+region: eu-west-1"}, // non-secret key revealed
			mustNotFind: []string{"hunter2", "newsecret"}, // secret values redacted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{
				paths: RepoPathsConfig{
					ClusterValues: "configuration/addons-clusters-values",
					GlobalValues:  "configuration/addons-global-values",
				},
			}

			result := o.buildFileDiff(tt.path, tt.oldContent, tt.newContent, tt.action)

			for _, expected := range tt.mustContain {
				if !strings.Contains(result, expected) {
					t.Errorf("diff missing expected content %q:\n%s", expected, result)
				}
			}

			for _, forbidden := range tt.mustNotFind {
				if strings.Contains(result, forbidden) {
					t.Errorf("diff contains forbidden value %q:\n%s", forbidden, result)
				}
			}

			// All diffs should have header lines
			if !strings.Contains(result, "---") || !strings.Contains(result, "+++") {
				t.Errorf("diff missing unified diff headers:\n%s", result)
			}
		})
	}
}

func TestBuildFileDiff_BothSidesRedacted(t *testing.T) {
	o := &Orchestrator{
		paths: RepoPathsConfig{
			ClusterValues: "configuration/addons-clusters-values",
			GlobalValues:  "configuration/addons-global-values",
		},
	}

	// A values file where a key is added shows structural change
	oldContent := []byte("database:\n  password: hunter2")
	newContent := []byte("database:\n  password: hunter2\n  host: db.example.com")

	diff := o.buildFileDiff("configuration/addons-clusters-values/cluster1.yaml", oldContent, newContent, "update")

	// The diff should NOT show raw secret values
	if strings.Contains(diff, "hunter2") {
		t.Errorf("diff exposed raw secret value:\n%s", diff)
	}

	// The diff should show the structural change (added key)
	if !strings.Contains(diff, "+") {
		t.Errorf("diff should show additions (new key), but didn't:\n%s", diff)
	}
}

// TestRedactYAMLNode_LoosendRedaction tests W3b fix: non-secret values revealed, secrets redacted.
func TestRedactYAMLNode_LoosendRedaction(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		mustReveal  []string // values that should appear in clear
		mustRedact  []string // values that should be <redacted>
		mustNotFind []string // YAML type tags and secret values
	}{
		{
			name: "non-secret config revealed",
			input: `region: eu-west-1
enabled: true
replicas: 3
namespace: kube-system
podinfo:
  enabled: false
enabledAddonNamespaces:
  - default
  - kube-system`,
			mustReveal:  []string{"eu-west-1", "true", "3", "kube-system", "false", "default"},
			mustRedact:  nil,
			mustNotFind: []string{"!!str", "!!bool", "!!int", "!!null"},
		},
		{
			name: "secret keys redacted",
			input: `region: eu-west-1
token: abc123
password: hunter2
db_secret: supersecret
api_key: sk-1234567890`,
			mustReveal:  []string{"eu-west-1"},
			mustRedact:  []string{"<redacted>"}, // token, password, db_secret, api_key all redacted
			mustNotFind: []string{"abc123", "hunter2", "supersecret", "sk-1234567890", "!!str"},
		},
		{
			name: "JWT value redacted",
			input: `region: us-east-1
someField: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`,
			mustReveal:  []string{"us-east-1"},
			mustRedact:  []string{"<redacted>"},
			mustNotFind: []string{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", "!!str"},
		},
		{
			name: "base64 blob redacted",
			input: `region: eu-central-1
certData: ` + strings.Repeat("ABCD", 30), // 120 chars base64-like
			mustReveal:  []string{"eu-central-1"},
			mustRedact:  []string{"<redacted>"},
			mustNotFind: []string{strings.Repeat("ABCD", 30), "!!str"},
		},
		{
			name: "null value preserved without type tag",
			input: `region: eu-west-1
clusterGlobalValues: null`,
			mustReveal:  []string{"eu-west-1", "null"}, // null rendered as "null" string
			mustRedact:  nil,
			mustNotFind: []string{"!!null", "!!str"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{
				paths: RepoPathsConfig{
					ClusterValues: "configuration/addons-clusters-values",
				},
			}

			result := o.redactValuesContent("configuration/addons-clusters-values/test.yaml", []byte(tt.input))
			resultStr := string(result)

			for _, expected := range tt.mustReveal {
				if !strings.Contains(resultStr, expected) {
					t.Errorf("expected revealed value %q not found in:\n%s", expected, resultStr)
				}
			}

			for _, expected := range tt.mustRedact {
				if !strings.Contains(resultStr, expected) {
					t.Errorf("expected redacted placeholder %q not found in:\n%s", expected, resultStr)
				}
			}

			for _, forbidden := range tt.mustNotFind {
				if strings.Contains(resultStr, forbidden) {
					t.Errorf("forbidden value %q found in:\n%s", forbidden, resultStr)
				}
			}
		})
	}
}

// TestIsSensitiveKey verifies the key-based secret detection matches logging/redact.go.
func TestIsSensitiveKey(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		// Exact matches (case-insensitive)
		{"token", true},
		{"Token", true},
		{"TOKEN", true},
		{"password", true},
		{"PASSWORD", true},
		{"api_key", true},
		{"apikey", true},
		{"secret", true},
		{"kubeconfig", true},
		{"private_key", true},

		// Suffix matches
		{"db_password", true},
		{"argocd_token", true},
		{"webhook_secret", true},
		{"signing_key", true},

		// Non-secrets
		{"region", false},
		{"enabled", false},
		{"namespace", false},
		{"replicas", false},
		{"host", false},
		{"port", false},
		{"", false}, // empty key
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := isSensitiveKey(tt.key)
			if result != tt.expected {
				t.Errorf("isSensitiveKey(%q) = %v, want %v", tt.key, result, tt.expected)
			}
		})
	}
}

// TestShouldRedactValue verifies the value-shape secret detection (JWT, base64 blobs).
func TestShouldRedactValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		// JWTs
		{"valid JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", true},
		{"JWT-like substring in text", "check this JWT: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig and more", false}, // not anchored

		// Base64 blobs
		{"long base64 blob", strings.Repeat("ABCD", 30), true}, // 120 chars
		{"short base64-like string", "ABCD1234", false},        // <100 chars

		// Non-secrets
		{"plain text", "eu-west-1", false},
		{"boolean", "true", false},
		{"number", "42", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldRedactValue(tt.value)
			if result != tt.expected {
				t.Errorf("shouldRedactValue(%q) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}
}
