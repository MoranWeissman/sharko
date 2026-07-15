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
			name:        "values file diff never exposes secrets",
			path:        "configuration/addons-clusters-values/cluster1.yaml",
			oldContent:  []byte("password: hunter2"),
			newContent:  []byte("password: newsecret\nnewkey: newvalue"),
			action:      "update",
			mustContain: []string{}, // Structure changed (added key), but no values shown
			mustNotFind: []string{"hunter2", "newsecret", "newvalue"},
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
	if strings.Contains(diff, "db.example.com") {
		t.Errorf("diff exposed raw value:\n%s", diff)
	}

	// The diff should show the structural change (added key)
	if !strings.Contains(diff, "+") {
		t.Errorf("diff should show additions (new key), but didn't:\n%s", diff)
	}
}
