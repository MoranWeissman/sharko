package orchestrator

import (
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
)

// registerRegistryEntry builds the managed-clusters.yaml entry a register
// request adds. Used by both the dry-run preview and the real write in
// RegisterCluster so the two always describe the same entry.
//
// v4 never authors addon keys on the connection record (design doc §2.4),
// so on a v4 repo the labels stay empty. On v3 the labels use the canonical
// "enabled"/"disabled" vocabulary (V2-cleanup-20). Only the non-default
// "user" connection mode is written; Sharko-managed entries omit it.
func registerRegistryEntry(req RegisterClusterRequest, v4Repo, selfManaged bool, credsSource CredsSource) gitops.ClusterEntryInput {
	clusterLabels := make(map[string]string, len(req.Addons))
	if !v4Repo {
		for addon, enabled := range req.Addons {
			clusterLabels[addon] = models.AddonLabelValue(enabled)
		}
	}
	connMode := ""
	if selfManaged {
		connMode = models.ConnectionManagedByUser
	}
	return gitops.ClusterEntryInput{
		Name:                req.Name,
		Region:              req.Region,
		SecretPath:          req.SecretPath,
		Labels:              clusterLabels,
		ConnectionManagedBy: connMode,
		// The effective creds source routes later credential fetches per
		// cluster (V2-cleanup-60.4).
		CredsSource: string(credsSource),
		// Empty omits the field (V2-cleanup-62.2).
		RoleARN: req.RoleARN,
	}
}

// buildRegisterRegistryFile returns the full managed-clusters.yaml text a
// register writes. It is the ONE place that text is built: the dry-run
// preview and the real pull request both call it, so the preview can never
// show different lines from the ones the pull request writes (story G1 —
// the preview used to leave out the plain-English header of a new file).
//
// existing is the file as read from the base branch; exists says whether
// it was there at all. A missing file starts from an empty registry, and on
// a v4 repo it opens with managedClustersFileHeader (headers ride creation
// only — an existing file is never given one).
func buildRegisterRegistryFile(existing []byte, exists, v4Repo bool, entry gitops.ClusterEntryInput) ([]byte, error) {
	data := existing
	if !exists {
		data = []byte("clusters:\n")
	}
	updated, err := gitops.AddClusterEntry(data, entry)
	if err != nil {
		return nil, err
	}
	if v4Repo && !exists {
		updated = append([]byte(managedClustersFileHeader), updated...)
	}
	return updated, nil
}
