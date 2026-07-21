package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterAddonsSpec defines the desired addon assignments for a managed cluster.
// This is the ASSIGNMENT layer only — it specifies which addons are enabled/disabled
// and their versions. The addon catalog holds the chart definitions, Helm repos, and
// default values. Separating assignment from definition avoids duplication and keeps
// the catalog as the single source of truth for addon metadata.
type ClusterAddonsSpec struct {
	// Cluster is the name of the managed cluster this assignment applies to.
	// Must match a cluster name in the managed-clusters.yaml file.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Cluster string `json:"cluster"`

	// Addons is the list of addon assignments for this cluster.
	// Each entry specifies an addon name, optional version override, and enabled state.
	// +optional
	Addons []AddonAssignment `json:"addons,omitempty"`
}

// AddonAssignment represents a single addon's configuration for a cluster.
// The Name field is the only required field; Version and Enabled are optional overrides.
type AddonAssignment struct {
	// Name of the addon (must exist in the addon catalog).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Version is an optional version override for this cluster.
	// If not specified, the cluster uses the global version from the catalog.
	// +optional
	Version string `json:"version,omitempty"`

	// Enabled controls whether this addon is active on the cluster.
	// If nil, defaults to true (addon is enabled).
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// ClusterAddonsStatus defines the observed state of ClusterAddons.
// It tracks the reconciliation outcome and provides status for kubectl get commands.
type ClusterAddonsStatus struct {
	// ObservedGeneration is the most recent generation observed by the reconciler.
	// It corresponds to the ClusterAddons' metadata.generation, which is updated on
	// any changes to the spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastReconcileTime is the timestamp of the most recent reconciliation attempt.
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`

	// SyncedAddons is the count of addons successfully reconciled in the last run.
	// +optional
	SyncedAddons int `json:"syncedAddons,omitempty"`

	// Conditions represent the latest available observations of the ClusterAddons state.
	// Standard condition types: Ready, Progressing, Degraded.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// ClusterAddons is the Schema for the clusteraddons API.
// It defines a namespaced custom resource that represents the addon inventory
// for a single managed cluster. The Sharko operator watches ClusterAddons objects
// and generates corresponding ArgoCD ApplicationSet entries to deploy the addons.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ca
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="Synced",type=integer,JSONPath=`.status.syncedAddons`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ClusterAddons struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterAddonsSpec   `json:"spec,omitempty"`
	Status ClusterAddonsStatus `json:"status,omitempty"`
}

// ClusterAddonsList contains a list of ClusterAddons.
// +kubebuilder:object:root=true
type ClusterAddonsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAddons `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterAddons{}, &ClusterAddonsList{})
}
