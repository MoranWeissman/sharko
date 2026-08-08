package orchestrator

import (
	"context"
	"testing"
)

// v4EnginePinYAML is the minimal engine pin body isV4Repo probes for — its
// mere presence (non-empty content at EnginePinPath) is the v4-repo signal
// CheckEnginePin and GetVersionMatrix already use.
const v4EnginePinYAML = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
`

// v4GuardCase is one v3 registry writer under test: the thing it is called,
// and a closure that invokes it on the given orchestrator.
type v4GuardCase struct {
	name string
	call func(ctx context.Context, o *Orchestrator) error
}

// TestAdoptUnadoptLabelPatchRemove_NoLongerRefuseOnV4Repo is the
// v4-coherence-closure guard test: adopt, unadopt, the addon label-patch
// (PATCH /clusters/{name}), and — as of lane K — removal (DELETE
// /clusters/{name}) all got their own v4 branches and must NOT answer a v4
// repo with ErrV4RepoUnsupported anymore. They may still fail for their
// own, v4-specific reasons (no in-cluster install to run the preflight, a
// missing adopted annotation, a cluster nobody registered), which is
// exactly what the per-operation v4 tests (adopt_v4_test.go,
// unadopt_v4_test.go, cluster_addons_v4_test.go, remove_v4_test.go) cover
// in detail. This test's only job is the negative one: none of the four
// routes through the old v3-registry refusal anymore.
//
// Lane D originally left RemoveCluster refusing here (see
// TestV3RegistryWriters_RefuseOnV4Repo in the prior revision of this file)
// with the reasoning that removal still wrote the v3 registry. Lane K gave
// RemoveCluster the same kind of v4 branch the other three already had —
// see remove.go / remove_v4_test.go — so that refusal is no longer true and
// the pin flips here to match.
func TestAdoptUnadoptLabelPatchRemove_NoLongerRefuseOnV4Repo(t *testing.T) {
	cases := []v4GuardCase{
		{"AdoptClusters", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.AdoptClusters(ctx, AdoptClustersRequest{Clusters: []string{"prod-eu"}})
			return err
		}},
		{"UnadoptCluster", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UnadoptCluster(ctx, "prod-eu", UnadoptClusterRequest{})
			return err
		}},
		{"UpdateClusterAddons", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UpdateClusterAddons(ctx, "prod-eu", "https://prod-eu.example.com", "",
				map[string]bool{"cert-manager": true}, nil, false)
			return err
		}},
		{"RemoveCluster", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.RemoveCluster(ctx, RemoveClusterRequest{Name: "prod-eu", Yes: true})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[EnginePinPath] = []byte(v4EnginePinYAML) // makes isV4Repo true
			orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			err := tc.call(context.Background(), orch)
			if IsV4RepoUnsupported(err) {
				t.Fatalf("%s still refuses a v4 repo the old way: %v", tc.name, err)
			}
		})
	}
}
