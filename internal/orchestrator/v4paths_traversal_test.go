package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// traversalNames is the shared attack list: every shape a cluster or addon
// name could take that would let path.Join's own cleaning walk the write
// out of the v4 data folders. "../../engine.yaml" is the
// confirmed exploit — path.Join("clusters", "../../engine.yaml"
// + ".yaml") cleans to a path outside clusters/ entirely, so an
// enable-addon call could have rewritten the engine pin.
var traversalNames = []string{
	"..",
	"../evil",
	"../../engine/application",
	"../../engine.yaml",
	"a/../../b",
	"nested/name",
	`back\slash`,
	"..%2Fevil",       // already-decoded form is covered above; this one has a "%"
	"name with space", // not traversal, but still outside the allowed alphabet
	"",
}

func TestV4PathBuilders_RejectTraversalNames(t *testing.T) {
	for _, name := range traversalNames {
		t.Run("cluster="+name, func(t *testing.T) {
			if p, err := v4ClusterAddonsPath(name); err == nil {
				t.Errorf("v4ClusterAddonsPath(%q) = %q, want an error", name, p)
			}
			if p, err := v4ClusterValuesPath(name, "cert-manager"); err == nil {
				t.Errorf("v4ClusterValuesPath(%q, cert-manager) = %q, want an error", name, p)
			}
		})
		t.Run("addon="+name, func(t *testing.T) {
			if p, err := v4GlobalValuesPath(name); err == nil {
				t.Errorf("v4GlobalValuesPath(%q) = %q, want an error", name, p)
			}
			if p, err := v4ClusterValuesPath("prod-eu", name); err == nil {
				t.Errorf("v4ClusterValuesPath(prod-eu, %q) = %q, want an error", name, p)
			}
		})
	}
}

func TestV4PathBuilders_AcceptOrdinaryNames(t *testing.T) {
	got, err := v4ClusterAddonsPath("prod-eu")
	if err != nil || got != "clusters/prod-eu.yaml" {
		t.Errorf("v4ClusterAddonsPath(prod-eu) = (%q, %v), want (clusters/prod-eu.yaml, nil)", got, err)
	}
	got, err = v4GlobalValuesPath("cert-manager")
	if err != nil || got != "values/global/cert-manager.yaml" {
		t.Errorf("v4GlobalValuesPath(cert-manager) = (%q, %v), want (values/global/cert-manager.yaml, nil)", got, err)
	}
	got, err = v4ClusterValuesPath("prod-eu", "cert-manager")
	if err != nil || got != "values/clusters/prod-eu/cert-manager.yaml" {
		t.Errorf("v4ClusterValuesPath(prod-eu, cert-manager) = (%q, %v), want (values/clusters/prod-eu/cert-manager.yaml, nil)", got, err)
	}
}

// TestEnableAddonV4_TraversalName_WritesNothing is the end-to-end proof for
// the confirmed exploit: an attacker-controlled cluster name aimed at the
// engine pin must be refused with nothing written, no branch, and no PR.
func TestEnableAddonV4_TraversalName_WritesNothing(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	before := len(git.files)

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "../../engine/application",
		Addon:   "metrics-server",
		Values:  map[string]interface{}{"replicas": 99},
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected EnableAddonV4 to refuse a traversal cluster name")
	}
	if !strings.Contains(err.Error(), "invalid cluster name") {
		t.Errorf("error = %v, want it to name the offending cluster name", err)
	}
	if len(git.files) != before {
		t.Errorf("files written = %d, want none beyond the %d seeded", len(git.files)-before, before)
	}
	if _, ok := git.files[EnginePinPath]; ok {
		t.Fatalf("the engine pin at %s was written — the traversal succeeded", EnginePinPath)
	}
	if len(git.branches) != 0 {
		t.Errorf("branches created = %v, want none", git.branches)
	}
	if len(git.prs) != 0 {
		t.Errorf("PRs opened = %d, want none", len(git.prs))
	}
}

func TestDisableAddonV4_TraversalName_WritesNothing(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	before := len(git.files)

	_, err := orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "../../engine/application",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected DisableAddonV4 to refuse a traversal addon name")
	}
	if len(git.files) != before || len(git.branches) != 0 || len(git.prs) != 0 {
		t.Errorf("side effects on refusal: files=%d branches=%v prs=%d", len(git.files)-before, git.branches, len(git.prs))
	}
}

func TestAddInternalAddon_TraversalName_WritesNothing(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	before := len(git.files)

	_, err := orch.AddInternalAddon(context.Background(), AddInternalAddonRequest{
		Name:    "../../engine/application",
		RepoURL: "https://charts.example.com",
		Chart:   "evil",
		Version: "1.0.0",
	})
	if err == nil {
		t.Fatal("expected AddInternalAddon to refuse a traversal addon name")
	}
	if len(git.files) != before || len(git.branches) != 0 || len(git.prs) != 0 {
		t.Errorf("side effects on refusal: files=%d branches=%v prs=%d", len(git.files)-before, git.branches, len(git.prs))
	}
}
