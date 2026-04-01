package models

// ArgocdCluster represents a cluster as reported by ArgoCD.
type ArgocdCluster struct {
	Name            string                 `json:"name"`
	Server          string                 `json:"server"`
	ConnectionState string                 `json:"connection_state,omitempty"`
	ServerVersion   string                 `json:"server_version,omitempty"`
	Namespaces      []string               `json:"namespaces,omitempty"`
	Info            map[string]interface{} `json:"info,omitempty"`
}

// ArgocdApplication represents an ArgoCD application.
type ArgocdApplication struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	Project              string            `json:"project"`
	SourceRepoURL        string            `json:"source_repo_url"`
	SourcePath           string            `json:"source_path,omitempty"`
	SourceTargetRevision string            `json:"source_target_revision"`
	DestinationServer    string            `json:"destination_server"`
	DestinationName      string            `json:"destination_name,omitempty"`
	DestinationNamespace string            `json:"destination_namespace"`
	SyncStatus           string            `json:"sync_status,omitempty"`
	HealthStatus         string            `json:"health_status,omitempty"`
	OperationState       string            `json:"operation_state,omitempty"`
	CreatedAt            string            `json:"created_at,omitempty"`
	SourceChart          string            `json:"source_chart,omitempty"`
	SourceHelmParameters []HelmParameter   `json:"source_helm_parameters,omitempty"`
	HealthLastTransition string            `json:"health_last_transition,omitempty"`
	ReconciledAt         string            `json:"reconciled_at,omitempty"`
	OperationPhase       string            `json:"operation_phase,omitempty"`
	OperationStartedAt   string            `json:"operation_started_at,omitempty"`
	OperationFinishedAt  string            `json:"operation_finished_at,omitempty"`
	OperationMessage     string            `json:"operation_message,omitempty"`
	History              []AppHistoryEntry `json:"history,omitempty"`
	Resources            []AppResource     `json:"resources,omitempty"`
	Conditions           []AppCondition    `json:"conditions,omitempty"`
}

// AppCondition represents an ArgoCD application condition (error/warning).
type AppCondition struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AppHistoryEntry represents a single deployment history entry.
type AppHistoryEntry struct {
	ID              int    `json:"id"`
	DeployedAt      string `json:"deployed_at"`
	DeployStartedAt string `json:"deploy_started_at"`
	Revision        string `json:"revision,omitempty"`
}

// AppResource represents a Kubernetes resource managed by an ArgoCD application.
type AppResource struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	Health    string `json:"health,omitempty"`
	Message   string `json:"message,omitempty"`
}

// HelmParameter is a key-value pair for Helm parameters.
type HelmParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ArgocdClustersResponse is the API response for listing ArgoCD clusters.
type ArgocdClustersResponse struct {
	Clusters []ArgocdCluster `json:"clusters"`
}

// ArgocdApplicationsResponse is the API response for listing ArgoCD applications.
type ArgocdApplicationsResponse struct {
	Applications []ArgocdApplication `json:"applications"`
}

// ArgocdConnectionTestResponse is returned when testing ArgoCD connectivity.
type ArgocdConnectionTestResponse struct {
	Status        string `json:"status"`
	Message       string `json:"message"`
	ClustersCount int    `json:"clusters_count,omitempty"`
}
