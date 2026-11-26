package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Walk 3, serious finding: a per-cluster values file written as
// "podinfo:" with the settings underneath is accepted by Git and ArgoCD and
// ignored by Helm, and nothing said so. validate-config now says so.

func writeClusterValues(t *testing.T, cluster, addon, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "values", "clusters", cluster)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, addon+".yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateConfig_WrappedClusterValuesFileFails(t *testing.T) {
	path := writeClusterValues(t, "spoke-1", "podinfo", "podinfo:\n  replicaCount: 2\n  ui:\n    message: hi\n")
	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if err == nil {
		t.Fatalf("a wrapped values file passed; output:\n%s", buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		`none of the settings in this file take effect: they are all inside a block named "podinfo:"`,
		`Move the settings out of the "podinfo:" block so they start at the left edge of the file.`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
}

func TestValidateConfig_UnwrappedClusterValuesFileStillSkips(t *testing.T) {
	path := writeClusterValues(t, "spoke-1", "podinfo", "replicaCount: 2\n")
	var buf bytes.Buffer
	if err := runValidateConfig(&buf, path, false); err != nil {
		t.Fatalf("a normal values file failed: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "take effect") {
		t.Errorf("a normal file was called wrapped:\n%s", buf.String())
	}
}

func TestValidateConfig_CommentsOnlyClusterValuesFileIsNotWrapped(t *testing.T) {
	path := writeClusterValues(t, "spoke-1", "podinfo", "# Helm values for podinfo on spoke-1 only.\n# An empty file means no cluster-specific overrides.\n")
	var buf bytes.Buffer
	if err := runValidateConfig(&buf, path, false); err != nil {
		t.Fatalf("a comments-only file failed: %v\n%s", err, buf.String())
	}
}

// A file outside values/clusters/ that happens to start with a key named like
// its file is not a per-cluster values file and is left alone.
func TestValidateConfig_WrapCheckOnlyLooksAtClusterValuesFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "podinfo.yaml")
	if err := os.WriteFile(p, []byte("podinfo:\n  replicaCount: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runValidateConfig(&buf, p, false); err != nil {
		t.Fatalf("an unrelated file failed: %v\n%s", err, buf.String())
	}
}
