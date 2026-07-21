package api

import (
	"context"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// HomeClusterInfo holds basic information about the cluster where Sharko
// and ArgoCD run (the "home cluster"). Only available when running in-cluster.
type HomeClusterInfo struct {
	Available        bool   `json:"available"`
	Message          string `json:"message,omitempty"`
	KubernetesVersion string `json:"kubernetes_version,omitempty"`
	NodeCount         int    `json:"node_count,omitempty"`
	NodesReady        int    `json:"nodes_ready,omitempty"`
	NodesNotReady     int    `json:"nodes_not_ready,omitempty"`
}

// handleGetHomeCluster godoc
//
// @Summary Get home cluster info
// @Description Returns information about Sharko's home cluster (where Sharko and ArgoCD run): Kubernetes version, node count, and health summary. Only available when running in-cluster; gracefully degrades to a message when not available.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} HomeClusterInfo "Home cluster information"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /cluster/home [get]
func (s *Server) handleGetHomeCluster(w http.ResponseWriter, r *http.Request) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// Gracefully degrade when not running in-cluster (local dev, demo mode).
		writeJSON(w, http.StatusOK, HomeClusterInfo{
			Available: false,
			Message:   "Only available when running in-cluster",
		})
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Kubernetes client: "+err.Error())
		return
	}

	// Get Kubernetes version
	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get server version: "+err.Error())
		return
	}

	// Get node list for health summary
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		// Gracefully degrade when RBAC denies node access (config.nodeAccess=false).
		// Mirror the pattern from nodes.go: return 200 with available=false so
		// the UI shows a degraded state rather than an error.
		if apierrors.IsForbidden(err) {
			writeJSON(w, http.StatusOK, HomeClusterInfo{
				Available: false,
				Message:   "Node info disabled — set config.nodeAccess=true in Helm values to enable.",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list nodes: "+err.Error())
		return
	}

	// Count ready vs not-ready nodes (mirror the pattern from nodes.go)
	readyCount := 0
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				readyCount++
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, HomeClusterInfo{
		Available:         true,
		KubernetesVersion: version.GitVersion,
		NodeCount:         len(nodes.Items),
		NodesReady:        readyCount,
		NodesNotReady:     len(nodes.Items) - readyCount,
	})
}
