package config

import (
	"context"
	"errors"
	"testing"
)

// pathRoutedReader serves a different body per repo path, so a test can
// model a repo that has ONLY the v3 file, ONLY the v4 file, or both.
type pathRoutedReader struct {
	files map[string][]byte
	read  []string
}

func (p *pathRoutedReader) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	p.read = append(p.read, path)
	if body, ok := p.files[path]; ok {
		return body, nil
	}
	return nil, errors.New("file not found")
}

// v4ConnectionsYAML is the same ManagedClusters shape the v3 file uses —
// on a v4 repo only its location changed (design doc §2.4).
const v4ConnectionsYAML = `clusters:
  - name: prod-eu
    secretPath: clusters/prod/prod-eu-kubeconfig
    credsSource: eks-token
    roleArn: arn:aws:iam::000000000000:role/SharkoEKSRead
    labels: {}
`

// TestResolveCredentialRouting_V4Fallback — on a v4 repo the v3 registry
// file genuinely does not exist, so without a fallback the resolver
// returned (plain name, "", "") and Sharko fetched credentials by the raw
// cluster name against the wrong backend, ignoring the cluster's stored
// secretPath / credsSource / roleARN entirely.
func TestResolveCredentialRouting_V4Fallback(t *testing.T) {
	reader := &pathRoutedReader{files: map[string][]byte{
		V4ConnectionsPath: []byte(v4ConnectionsYAML),
	}}

	key, source, role := ResolveCredentialRouting(context.Background(), reader, "", "main", "prod-eu")

	if key != "clusters/prod/prod-eu-kubeconfig" {
		t.Errorf("lookupKey = %q, want the stored secretPath", key)
	}
	if source != "eks-token" {
		t.Errorf("credsSource = %q, want eks-token", source)
	}
	if role != "arn:aws:iam::000000000000:role/SharkoEKSRead" {
		t.Errorf("roleARN = %q, want the stored per-cluster role", role)
	}
	if len(reader.read) != 2 || reader.read[0] != DefaultManagedClustersPath || reader.read[1] != V4ConnectionsPath {
		t.Errorf("read order = %v, want the v3 path first then the v4 path", reader.read)
	}
}

// TestResolveCredentialRouting_V3Unchanged — when the v3 file resolves, the
// v4 path is never read, so a stray fleet/connections.yaml (e.g. mid
// migration) can never leak into a v3 repo's credential routing.
func TestResolveCredentialRouting_V3Unchanged(t *testing.T) {
	reader := &pathRoutedReader{files: map[string][]byte{
		DefaultManagedClustersPath: []byte(credLookupTestYAML),
		V4ConnectionsPath:          []byte(v4ConnectionsYAML),
	}}

	key, _, _ := ResolveCredentialRouting(context.Background(), reader, "", "main", "moran")
	if key != "sharko-smoke-target-1-kubeconfig" {
		t.Errorf("lookupKey = %q, want the v3 file's secretPath", key)
	}
	if len(reader.read) != 1 || reader.read[0] != DefaultManagedClustersPath {
		t.Errorf("read = %v, want only the v3 path", reader.read)
	}
}

// TestResolveCredentialRouting_NeitherFile — both paths absent still falls
// back to the plain cluster name, byte-identical to the pre-fix behaviour.
func TestResolveCredentialRouting_NeitherFile(t *testing.T) {
	reader := &pathRoutedReader{files: map[string][]byte{}}
	key, source, role := ResolveCredentialRouting(context.Background(), reader, "", "main", "ghost")
	if key != "ghost" || source != "" || role != "" {
		t.Errorf("got (%q, %q, %q), want (ghost, \"\", \"\")", key, source, role)
	}
}
