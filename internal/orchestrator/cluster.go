package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
)

// ErrClusterAlreadyExists is returned when attempting to register a cluster
// that already exists in ArgoCD.
var ErrClusterAlreadyExists = errors.New("cluster already exists")

var validClusterName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// RegisterCluster orchestrates cluster registration:
//  1. Validate input
//  2. Check for duplicate cluster in ArgoCD (409 if exists)
//  3. Fetch credentials from provider
//  4. Register cluster in ArgoCD with addon labels
//  5. Generate and commit cluster values file via Git
//
// If Git commit fails after ArgoCD registration, a partial success is returned.
func (o *Orchestrator) RegisterCluster(ctx context.Context, req RegisterClusterRequest) (*RegisterClusterResult, error) {
	// Step 1: Validate input.
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if !validClusterName.MatchString(req.Name) {
		return nil, fmt.Errorf("invalid cluster name %q: must be alphanumeric with hyphens, starting with an alphanumeric character", req.Name)
	}

	// Step 1b: Merge default addons if no addons specified.
	if len(req.Addons) == 0 && len(o.defaultAddons) > 0 {
		req.Addons = make(map[string]bool)
		for k, v := range o.defaultAddons {
			req.Addons[k] = v
		}
	}

	// Step 2: Check for duplicate — cluster must not already exist in ArgoCD.
	clusters, err := o.argocd.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for existing cluster %q: %w", req.Name, err)
	}
	for _, c := range clusters {
		if c.Name == req.Name {
			return nil, fmt.Errorf("%w: %q in ArgoCD", ErrClusterAlreadyExists, req.Name)
		}
	}

	result := &RegisterClusterResult{
		Cluster: ClusterResult{
			Name:   req.Name,
			Addons: req.Addons,
		},
	}
	var steps []string

	// Step 3: Fetch credentials from provider.
	creds, err := o.credProvider.GetCredentials(req.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching credentials for cluster %q: %w", req.Name, err)
	}
	steps = append(steps, "fetch_credentials")
	result.Cluster.Server = creds.Server

	// Step 4: Register cluster in ArgoCD with addon labels.
	labels := make(map[string]string)
	for addon, enabled := range req.Addons {
		if enabled {
			labels[addon] = "enabled"
		} else {
			labels[addon] = "disabled"
		}
	}

	if err := o.argocd.RegisterCluster(ctx, req.Name, creds.Server, creds.CAData, creds.Token, labels); err != nil {
		return nil, fmt.Errorf("registering cluster %q in ArgoCD: %w", req.Name, err)
	}
	steps = append(steps, "argocd_register")

	// Step 5: Create addon secrets on remote cluster (if configured).
	secretNames, secretErr := o.createAddonSecrets(ctx, creds.Raw, req.Addons)
	if secretErr != nil {
		result.Status = "partial"
		result.CompletedSteps = steps
		result.Secrets = secretNames
		result.FailedStep = "create_secrets"
		result.Error = secretErr.Error()
		result.Message = "Cluster registered in ArgoCD but addon secret creation failed. PR not opened."
		return result, nil
	}
	if len(secretNames) > 0 {
		steps = append(steps, "create_secrets")
		result.Secrets = secretNames
	}

	// Step 6: Generate cluster values file and commit to Git.
	valuesContent := generateClusterValues(req.Name, req.Region, req.Addons)
	valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("register cluster %s", req.Name))
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = steps
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = "Cluster registered in ArgoCD and PR created, but auto-merge failed. Merge manually: " + gitResult.PRUrl
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = "Cluster registered in ArgoCD but values file commit failed. Manual Git commit required."
		return result, nil
	}
	steps = append(steps, "git_commit")

	gitResult.ValuesFile = valuesPath
	result.Status = "success"
	result.CompletedSteps = steps
	result.Git = gitResult
	return result, nil
}

// DeregisterCluster removes a cluster from ArgoCD and deletes its values file.
func (o *Orchestrator) DeregisterCluster(ctx context.Context, name string, serverURL string) (*RegisterClusterResult, error) {
	result := &RegisterClusterResult{
		Cluster: ClusterResult{Name: name, Server: serverURL},
	}

	// Step 1: Delete from ArgoCD.
	if err := o.argocd.DeleteCluster(ctx, serverURL); err != nil {
		return nil, fmt.Errorf("deleting cluster %q from ArgoCD: %w", name, err)
	}

	// Step 2: Delete Sharko-managed secrets from remote cluster (best-effort).
	if o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(name)
		if credErr == nil {
			o.deleteAllAddonSecrets(ctx, creds.Raw) // best-effort, don't fail deregister for this
		}
	}

	// Step 3: Delete values file from Git.
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
	gitResult, err := o.commitChanges(ctx, nil, []string{valuesPath}, fmt.Sprintf("deregister cluster %s", name))
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = []string{"delete_from_argocd"}
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = fmt.Sprintf("Cluster %s removed from ArgoCD and PR created, but auto-merge failed. Merge manually: %s", name, gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = []string{"delete_from_argocd"}
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Cluster %s removed from ArgoCD but values file deletion failed. The values file at %s may need manual cleanup.", name, valuesPath)
		return result, nil
	}

	result.Status = "success"
	result.Git = gitResult
	return result, nil
}

// UpdateClusterAddons updates addon labels in ArgoCD and the values file in Git.
func (o *Orchestrator) UpdateClusterAddons(ctx context.Context, name string, serverURL string, region string, addons map[string]bool) (*RegisterClusterResult, error) {
	result := &RegisterClusterResult{
		Cluster: ClusterResult{Name: name, Server: serverURL, Addons: addons},
	}

	// Step 1: Update ArgoCD cluster labels.
	labels := make(map[string]string)
	for addon, enabled := range addons {
		if enabled {
			labels[addon] = "enabled"
		} else {
			labels[addon] = "disabled"
		}
	}

	if err := o.argocd.UpdateClusterLabels(ctx, serverURL, labels); err != nil {
		return nil, fmt.Errorf("updating labels on cluster %q: %w", name, err)
	}

	// Step 2: Create/delete addon secrets on remote cluster (best-effort).
	if o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(name)
		if credErr == nil {
			enabledAddons := make(map[string]bool)
			for a, e := range addons {
				if e {
					enabledAddons[a] = true
				}
			}
			o.createAddonSecrets(ctx, creds.Raw, enabledAddons)

			disabledAddons := make(map[string]bool)
			for a, e := range addons {
				if !e {
					disabledAddons[a] = false
				}
			}
			o.deleteAddonSecrets(ctx, creds.Raw, disabledAddons)
		}
	}

	// Step 3: Update values file in Git.
	valuesContent := generateClusterValues(name, region, addons)
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("update addons for cluster %s", name))
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = []string{"update_argocd_labels"}
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = fmt.Sprintf("ArgoCD labels updated for cluster %s and PR created, but auto-merge failed. Merge manually: %s", name, gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = []string{"update_argocd_labels"}
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("ArgoCD labels updated for cluster %s but Git commit failed. Labels are active but values file at %s may be stale.", name, valuesPath)
		return result, nil
	}

	gitResult.ValuesFile = valuesPath
	result.Status = "success"
	result.Git = gitResult
	return result, nil
}

// RefreshClusterCredentials fetches fresh credentials from the provider
// and re-registers the cluster in ArgoCD.
func (o *Orchestrator) RefreshClusterCredentials(ctx context.Context, name string, serverURL string) error {
	creds, err := o.credProvider.GetCredentials(name)
	if err != nil {
		return fmt.Errorf("fetching fresh credentials for cluster %q: %w", name, err)
	}

	if err := o.argocd.RegisterCluster(ctx, name, creds.Server, creds.CAData, creds.Token, nil); err != nil {
		return fmt.Errorf("re-registering cluster %q with fresh credentials: %w", name, err)
	}

	return nil
}
