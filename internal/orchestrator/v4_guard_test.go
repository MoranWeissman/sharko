package orchestrator

import (
	"context"
	"strings"
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

func v4GuardCases() []v4GuardCase {
	return []v4GuardCase{
		{"AdoptClusters", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.AdoptClusters(ctx, AdoptClustersRequest{Clusters: []string{"prod-eu"}})
			return err
		}},
		{"UnadoptCluster", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UnadoptCluster(ctx, "prod-eu", UnadoptClusterRequest{})
			return err
		}},
		{"RemoveCluster", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.RemoveCluster(ctx, RemoveClusterRequest{Name: "prod-eu", Yes: true})
			return err
		}},
		{"UpdateClusterAddons", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UpdateClusterAddons(ctx, "prod-eu", "https://prod-eu.example.com", "",
				map[string]bool{"cert-manager": true}, nil, false)
			return err
		}},
	}
}

// TestV3RegistryWriters_RefuseOnV4Repo — each of the four operations that
// still writes configuration/managed-clusters.yaml must refuse outright on
// a v4 repo, BEFORE any git write. Writing a v3 registry into a v4 repo
// creates a rival registry file; the reconciler prefers the v3 one, so
// every v4-registered cluster becomes an orphan and has its ArgoCD Secret
// deleted.
func TestV3RegistryWriters_RefuseOnV4Repo(t *testing.T) {
	for _, tc := range v4GuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[EnginePinPath] = []byte(v4EnginePinYAML) // makes isV4Repo true
			orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			before := len(git.files)
			err := tc.call(context.Background(), orch)
			if err == nil {
				t.Fatalf("%s returned nil on a v4 repo — it would have written the v3 registry", tc.name)
			}
			if !IsV4RepoUnsupported(err) {
				t.Fatalf("%s error = %v, want an ErrV4RepoUnsupported refusal", tc.name, err)
			}
			if !strings.Contains(err.Error(), "not yet supported on a v4 repo") {
				t.Errorf("%s message = %q, want the plain-words v4 refusal", tc.name, err.Error())
			}

			// Zero writes: no new files, no branch, no PR.
			if len(git.files) != before {
				t.Errorf("%s wrote %d file(s) despite refusing", tc.name, len(git.files)-before)
			}
			if len(git.branches) != 0 {
				t.Errorf("%s created branches %v despite refusing", tc.name, git.branches)
			}
			if len(git.prs) != 0 {
				t.Errorf("%s opened %d PR(s) despite refusing", tc.name, len(git.prs))
			}
			if len(git.deletedFiles) != 0 {
				t.Errorf("%s deleted %v despite refusing", tc.name, git.deletedFiles)
			}
		})
	}
}

// TestV3RegistryWriters_UnchangedOnV3Repo is the other half: with no engine
// pin present the guard is inert, so none of the four operations returns a
// v4 refusal. (They may still fail for their own ordinary reasons — a
// missing adopted annotation, an empty ArgoCD cluster list — which is
// exactly the pre-existing behaviour this fix must not disturb.)
func TestV3RegistryWriters_UnchangedOnV3Repo(t *testing.T) {
	for _, tc := range v4GuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider() // no EnginePinPath => v3 repo
			orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			err := tc.call(context.Background(), orch)
			if IsV4RepoUnsupported(err) {
				t.Fatalf("%s refused a v3 repo as if it were v4: %v", tc.name, err)
			}
		})
	}
}
