package config

import (
	"context"
	"errors"
	"testing"
)

// V2-cleanup-55.1 — tests for the git-reading credential lookup-key
// resolver. Contract: stored secretPath wins; EVERY failure path (nil
// reader, read error, malformed YAML, unknown cluster, unset secretPath)
// falls back to the plain cluster name.

const credLookupTestYAML = `clusters:
  - name: moran
    secretPath: sharko-smoke-target-1-kubeconfig
    labels: {}
  - name: plain
    labels: {}
`

// fakeManagedClustersReader is a minimal ManagedClustersReader test double.
type fakeManagedClustersReader struct {
	data      []byte
	err       error
	lastPath  string
	lastRef   string
	callCount int
}

func (f *fakeManagedClustersReader) GetFileContent(_ context.Context, path, ref string) ([]byte, error) {
	f.callCount++
	f.lastPath = path
	f.lastRef = ref
	return f.data, f.err
}

func TestResolveCredentialLookupKeyFromData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		arg  string
		want string
	}{
		{"secretPath set — override wins", []byte(credLookupTestYAML), "moran", "sharko-smoke-target-1-kubeconfig"},
		{"secretPath unset — plain name", []byte(credLookupTestYAML), "plain", "plain"},
		{"cluster unknown — fallback to name", []byte(credLookupTestYAML), "ghost", "ghost"},
		{"nil data — fallback to name", nil, "moran", "moran"},
		{"malformed YAML — fallback to name", []byte(":\n\t- not yaml"), "moran", "moran"},
		{"empty name stays empty", []byte(credLookupTestYAML), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveCredentialLookupKeyFromData(tt.data, tt.arg); got != tt.want {
				t.Errorf("ResolveCredentialLookupKeyFromData(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestResolveCredentialLookupKey_ReadsStoredRecord(t *testing.T) {
	git := &fakeManagedClustersReader{data: []byte(credLookupTestYAML)}
	got := ResolveCredentialLookupKey(context.Background(), git, "", "", "moran")
	if got != "sharko-smoke-target-1-kubeconfig" {
		t.Errorf("resolved key = %q, want the stored secretPath", got)
	}
	if git.lastPath != DefaultManagedClustersPath {
		t.Errorf("read path = %q, want default %q", git.lastPath, DefaultManagedClustersPath)
	}
	if git.lastRef != "main" {
		t.Errorf("read ref = %q, want default main", git.lastRef)
	}
}

func TestResolveCredentialLookupKey_CustomPathAndBranch(t *testing.T) {
	git := &fakeManagedClustersReader{data: []byte(credLookupTestYAML)}
	got := ResolveCredentialLookupKey(context.Background(), git, "custom/mc.yaml", "develop", "plain")
	if got != "plain" {
		t.Errorf("resolved key = %q, want plain name", got)
	}
	if git.lastPath != "custom/mc.yaml" || git.lastRef != "develop" {
		t.Errorf("read (path=%q, ref=%q), want (custom/mc.yaml, develop)", git.lastPath, git.lastRef)
	}
}

func TestResolveCredentialLookupKey_FallbacksToName(t *testing.T) {
	t.Run("nil git reader", func(t *testing.T) {
		if got := ResolveCredentialLookupKey(context.Background(), nil, "", "", "moran"); got != "moran" {
			t.Errorf("resolved key = %q, want fallback moran", got)
		}
	})
	t.Run("git read error", func(t *testing.T) {
		git := &fakeManagedClustersReader{err: errors.New("boom")}
		if got := ResolveCredentialLookupKey(context.Background(), git, "", "", "moran"); got != "moran" {
			t.Errorf("resolved key = %q, want fallback moran", got)
		}
	})
	t.Run("cluster unknown", func(t *testing.T) {
		git := &fakeManagedClustersReader{data: []byte(credLookupTestYAML)}
		if got := ResolveCredentialLookupKey(context.Background(), git, "", "", "ghost"); got != "ghost" {
			t.Errorf("resolved key = %q, want fallback ghost", got)
		}
	})
}
