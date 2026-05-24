package remoteclient

import (
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// defaultRequestTimeout caps every K8s API call made through clients built
// by NewClientFromKubeconfig. Without it, an unreachable cluster (bad
// kubeconfig server URL, broken network) would hang the calling
// goroutine forever.
//
// Why 30s: short enough that unreachable clusters fast-fail with a clear
// error in well under a minute; long enough that a healthy-but-slow
// cluster is not falsely classified as unreachable. Discovery().ServerVersion()
// does not accept a ctx, so the rest.Config Timeout is the only knob
// that prevents it from hanging.
const defaultRequestTimeout = 30 * time.Second

// NewClientFromKubeconfig builds a temporary kubernetes.Interface from
// raw kubeconfig bytes. The caller should discard the client after use
// — no persistent connections. Every per-call request through the
// returned client is bounded by defaultRequestTimeout (30s) so an
// unreachable cluster cannot wedge the calling goroutine indefinitely.
func NewClientFromKubeconfig(kubeconfig []byte) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	// Per-call ceiling applied to every HTTP request issued by the
	// returned clientset (including Discovery().ServerVersion(), which
	// is not ctx-aware). Cap defensively so an unreachable cluster
	// cannot starve the request-serving goroutine pool.
	if restConfig.Timeout == 0 {
		restConfig.Timeout = defaultRequestTimeout
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return client, nil
}
