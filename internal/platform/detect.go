package platform

import (
	"context"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Mode represents the runtime environment.
type Mode string

const (
	ModeLocal      Mode = "local"
	ModeKubernetes Mode = "kubernetes"
)

// Detect determines whether the application is running inside a Kubernetes
// cluster or locally. It checks for the KUBERNETES_SERVICE_HOST environment
// variable which is automatically set by Kubernetes in every pod.
func Detect() Mode {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return ModeKubernetes
	}
	return ModeLocal
}

// DetectClusterName attempts to determine the name of the cluster that Sharko
// is running on. It tries the following strategies in order:
//
//  1. CLUSTER_NAME environment variable (set by some K8s distributions).
//  2. Node labels: node.kubernetes.io/cluster-name or eks.amazonaws.com/cluster-name.
//
// Returns an empty string if the cluster name cannot be determined.
func DetectClusterName() string {
	// 1. Environment variable (fastest path, works in many distributions).
	if name := os.Getenv("CLUSTER_NAME"); name != "" {
		slog.Info("cluster name resolved from CLUSTER_NAME env var", "name", name)
		return name
	}

	// 2. Read cluster name from node labels via in-cluster K8s API.
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return ""
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		return ""
	}

	node := nodes.Items[0]
	labels := node.Labels

	// EKS label takes precedence, then generic label.
	for _, labelKey := range []string{
		"eks.amazonaws.com/cluster-name",
		"node.kubernetes.io/cluster-name",
		"alpha.eksctl.io/cluster-name",
	} {
		if name, ok := labels[labelKey]; ok && name != "" {
			slog.Info("cluster name resolved from node label", "label", labelKey, "name", name)
			return name
		}
	}

	return ""
}
