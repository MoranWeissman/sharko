package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// TestListClusterAddonsSpecs_ConcurrentFanOut_PerfM2 pins the output of the
// N+1 fix: listClusterAddonsSpecs used to read cluster-addons/*.yaml one
// file at a time; it now fans the reads out concurrently (bounded by
// listClusterAddonsSpecsConcurrency), but the returned map must be
// identical either way — every cluster present, keyed by its Cluster
// field, addon data intact.
func TestListClusterAddonsSpecs_ConcurrentFanOut_PerfM2(t *testing.T) {
	want := map[string]models.ClusterAddonsSpec{
		"prod-eu": {
			Cluster: "prod-eu",
			Addons:  map[string]models.ClusterAddonsAddon{"cert-manager": {Enabled: true}},
		},
		"staging-us": {
			Cluster: "staging-us",
			Addons:  map[string]models.ClusterAddonsAddon{},
		},
		"prod-apac": {
			Cluster: "prod-apac",
			Addons:  map[string]models.ClusterAddonsAddon{"external-dns": {Enabled: true}},
		},
	}

	files := map[string][]byte{
		// A non-YAML entry (the real repo layout's .gitkeep) must be
		// skipped, not treated as a cluster file.
		orchestrator.V4ClustersDir + "/.gitkeep": {},
	}
	for name, spec := range want {
		data, err := models.SaveClusterAddons(spec)
		if err != nil {
			t.Fatalf("building %s assignment: %v", name, err)
		}
		files[orchestrator.V4ClustersDir+"/"+name+".yaml"] = data
	}

	gp := &fakeGitProvider{files: files}
	got, err := listClusterAddonsSpecs(context.Background(), gp, "main")
	if err != nil {
		t.Fatalf("listClusterAddonsSpecs: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d specs, want %d: %+v", len(got), len(want), got)
	}
	for name, wantSpec := range want {
		gotSpec, ok := got[name]
		if !ok {
			t.Errorf("missing cluster %q in result", name)
			continue
		}
		if len(gotSpec.Addons) != len(wantSpec.Addons) {
			t.Errorf("cluster %q: got %d addons, want %d", name, len(gotSpec.Addons), len(wantSpec.Addons))
		}
		for addonName, wantAddon := range wantSpec.Addons {
			if gotAddon, ok := gotSpec.Addons[addonName]; !ok || gotAddon.Enabled != wantAddon.Enabled {
				t.Errorf("cluster %q addon %q = %+v, want %+v", name, addonName, gotAddon, wantAddon)
			}
		}
	}
}

// erroringGitProvider wraps a *fakeGitProvider and returns failErr for one
// specific path, delegating everything else — used to prove a single
// file's read failure still fails the whole listClusterAddonsSpecs call
// even though the reads now happen concurrently (errgroup.Wait must still
// surface the first error, matching the original sequential behavior).
type erroringGitProvider struct {
	*fakeGitProvider
	failPath string
	failErr  error
}

func (e *erroringGitProvider) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	if path == e.failPath {
		return nil, e.failErr
	}
	return e.fakeGitProvider.GetFileContent(ctx, path, ref)
}

func TestListClusterAddonsSpecs_ErrorPropagates_PerfM2(t *testing.T) {
	okData, err := models.SaveClusterAddons(models.ClusterAddonsSpec{Cluster: "prod-eu"})
	if err != nil {
		t.Fatalf("building prod-eu assignment: %v", err)
	}

	badPath := orchestrator.V4ClustersDir + "/staging-us.yaml"
	sentinel := fmt.Errorf("simulated transient read failure: %w", gitprovider.ErrFileNotFound)

	gp := &erroringGitProvider{
		fakeGitProvider: &fakeGitProvider{
			files: map[string][]byte{
				orchestrator.V4ClustersDir + "/prod-eu.yaml":    okData,
				orchestrator.V4ClustersDir + "/staging-us.yaml": okData, // content unused — GetFileContent errors first
			},
		},
		failPath: badPath,
		failErr:  sentinel,
	}

	_, err = listClusterAddonsSpecs(context.Background(), gp, "main")
	if err == nil {
		t.Fatalf("expected an error when one file in the fan-out fails, got nil")
	}
}
