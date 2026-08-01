package gitops

// v4 Wave 1 Story 2.5 — pins the minimal-diff contract for
// UpdateEnginePinVersion: the pin-bump PR must change ONLY the engine
// chart's targetRevision line inside sharko-engine.yaml. Nothing
// else — not comments, not the git-values source's own targetRevision
// (the branch, e.g. "main"), not formatting.

import (
	"strings"
	"testing"
)

// enginePinFixture mirrors the worked example in
// docs/design/2026-07-30-v4-data-file-format.md section 2.5: a real ArgoCD
// Application with two sources — the engine chart source (repoURL + chart
// + targetRevision) and the git values-ref source (repoURL + targetRevision
// + ref: values, no chart field). It also carries a comment and a
// non-default indent style on purpose, to prove those survive untouched.
const enginePinFixture = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
  namespace: argocd
  labels:
    app.kubernetes.io/managed-by: sharko
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  sources:
    - repoURL: ghcr.io/example-org/charts
      chart: sharko-engine
      targetRevision: 4.0.0
      helm:
        ignoreMissingValueFiles: true
        valueFiles:
          - $values/catalog.yaml
        parameters:
          - name: repo.url
            value: https://github.com/example-org/fleet-gitops.git
          - name: repo.revision
            value: main
    - repoURL: https://github.com/example-org/fleet-gitops.git
      targetRevision: main
      ref: values
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
`

func TestUpdateEnginePinVersion_ChangesOnlyThePinLine(t *testing.T) {
	updated, err := UpdateEnginePinVersion([]byte(enginePinFixture), "sharko-engine", "4.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oldLines := strings.Split(enginePinFixture, "\n")
	newLines := strings.Split(string(updated), "\n")

	if len(oldLines) != len(newLines) {
		t.Fatalf("line count changed: %d -> %d (expected byte-identical line count)", len(oldLines), len(newLines))
	}

	var changed []int
	for i := range oldLines {
		if oldLines[i] != newLines[i] {
			changed = append(changed, i)
		}
	}
	if len(changed) != 1 {
		t.Fatalf("expected exactly 1 changed line, got %d: %v\nold:\n%s\nnew:\n%s", len(changed), changed, enginePinFixture, string(updated))
	}

	wantOld := "      targetRevision: 4.0.0"
	wantNew := "      targetRevision: 4.1.0"
	if oldLines[changed[0]] != wantOld {
		t.Errorf("changed line (before) = %q, want %q", oldLines[changed[0]], wantOld)
	}
	if newLines[changed[0]] != wantNew {
		t.Errorf("changed line (after) = %q, want %q", newLines[changed[0]], wantNew)
	}

	// The git-values source's own targetRevision (the branch "main") must
	// survive untouched — this is the exact bug class D8/section 4.4 warns
	// about: two sources sharing a field name, only one of which should
	// ever move.
	if !strings.Contains(string(updated), "targetRevision: main") {
		t.Error("the git-values source's targetRevision (\"main\") was altered or removed")
	}

	// Comments/labels/finalizers must be byte-identical.
	for _, mustContain := range []string{
		"finalizers:",
		"resources-finalizer.argocd.argoproj.io",
		"app.kubernetes.io/managed-by: sharko",
		"ref: values",
	} {
		if !strings.Contains(string(updated), mustContain) {
			t.Errorf("expected output to still contain %q", mustContain)
		}
	}
}

func TestUpdateEnginePinVersion_PreservesQuoteStyle(t *testing.T) {
	quoted := strings.Replace(enginePinFixture, "targetRevision: 4.0.0", `targetRevision: "4.0.0"`, 1)

	updated, err := UpdateEnginePinVersion([]byte(quoted), "sharko-engine", "4.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(updated), `targetRevision: "4.1.0"`) {
		t.Errorf("expected quoted new version, got:\n%s", string(updated))
	}
}

func TestUpdateEnginePinVersion_PreservesTrailingComment(t *testing.T) {
	commented := strings.Replace(enginePinFixture, "targetRevision: 4.0.0", "targetRevision: 4.0.0 # pinned by ops", 1)

	updated, err := UpdateEnginePinVersion([]byte(commented), "sharko-engine", "4.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(updated), "targetRevision: 4.1.0 # pinned by ops") {
		t.Errorf("expected trailing comment to survive, got:\n%s", string(updated))
	}
}

func TestUpdateEnginePinVersion_ChartNotFound(t *testing.T) {
	_, err := UpdateEnginePinVersion([]byte(enginePinFixture), "some-other-chart", "4.1.0")
	if err == nil {
		t.Fatal("expected error when no source matches the given chart name")
	}
	if !strings.Contains(err.Error(), "no source with chart") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdateEnginePinVersion_RequiresArgs(t *testing.T) {
	if _, err := UpdateEnginePinVersion([]byte(enginePinFixture), "", "4.1.0"); err == nil {
		t.Error("expected error for empty engine chart name")
	}
	if _, err := UpdateEnginePinVersion([]byte(enginePinFixture), "sharko-engine", ""); err == nil {
		t.Error("expected error for empty new version")
	}
}

func TestEnginePinVersion_ReadsCurrentPin(t *testing.T) {
	got, err := EnginePinVersion([]byte(enginePinFixture), "sharko-engine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "4.0.0" {
		t.Errorf("EnginePinVersion() = %q, want %q", got, "4.0.0")
	}
}

func TestEnginePinVersion_ChartNotFound(t *testing.T) {
	if _, err := EnginePinVersion([]byte(enginePinFixture), "some-other-chart"); err == nil {
		t.Fatal("expected error when no source matches the given chart name")
	}
}

func TestUpdateEnginePinVersion_MalformedDocument(t *testing.T) {
	if _, err := UpdateEnginePinVersion([]byte("not: valid\n  application: [oops"), "sharko-engine", "4.1.0"); err == nil {
		t.Error("expected parse error for malformed YAML")
	}

	if _, err := UpdateEnginePinVersion([]byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"), "sharko-engine", "4.1.0"); err == nil {
		t.Error("expected error when spec is missing")
	}
}
