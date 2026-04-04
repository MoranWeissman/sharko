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

	// Step 5: Generate cluster values file and commit to Git.
	valuesContent := generateClusterValues(req.Name, req.Region, req.Addons)
	valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("register cluster %s", req.Name))
	if err != nil {
		// Partial success: ArgoCD registration succeeded, Git failed.
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

	// Step 2: Delete values file from Git.
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
	gitResult, err := o.commitChanges(ctx, nil, []string{valuesPath}, fmt.Sprintf("deregister cluster %s", name))
	if err != nil {
		// ArgoCD deletion succeeded but Git failed — partial success.
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

	// Step 2: Update values file in Git.
	valuesContent := generateClusterValues(name, region, addons)
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("update addons for cluster %s", name))
	if err != nil {
		// ArgoCD labels updated but Git failed — partial success.
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
