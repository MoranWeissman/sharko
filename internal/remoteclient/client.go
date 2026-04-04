package remoteclient

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientFromKubeconfig builds a temporary kubernetes.Interface from raw kubeconfig bytes.
// The caller should discard the client after use — no persistent connections.
func NewClientFromKubeconfig(kubeconfig []byte) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return client, nil
}
