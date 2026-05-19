package remoteclient

import (
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// defaultRequestTimeout caps every K8s API call made through clients built
// by NewClientFromKubeconfig. Pre-V125-1-13.y.3 there was NO timeout —
// an unreachable cluster (bad kubeconfig server URL, broken network, etc.)
// would hang the calling goroutine forever, eventually surfacing as an
// HTTP 502 from the wrapping handler only when the request-level timeout
// (typically 60s+) fired.
//
// Why 30s: short enough that unreachable clusters fast-fail with a clear
// error in well under a minute; long enough that a healthy-but-slow
// cluster (CPU-throttled control plane, single-node kind, large object
// list pulled by the secrets reconciler) is not falsely classified as
// unreachable. Production parity check: kubectl's default --request-timeout
// is 0 (no timeout); our per-call ceiling is intentionally tighter because
// Sharko is a long-running service where a hung goroutine starves real
// traffic, whereas kubectl is an interactive tool the operator can Ctrl-C.
//
// Discovery().ServerVersion() (the call that exposed this gap during the
// V125-1-13.y.3 / BUG-189-final triage) does not accept a ctx, so the
// rest.Config Timeout is the only knob that prevents it from hanging.
const defaultRequestTimeout = 30 * time.Second

// NewClientFromKubeconfig builds a temporary kubernetes.Interface from raw kubeconfig bytes.
// The caller should discard the client after use — no persistent connections.
//
// V125-1-13.y.3 / BUG-189-final: every per-call request through the
// returned client is bounded by defaultRequestTimeout (30s) so an
// unreachable cluster cannot wedge the calling goroutine indefinitely.
// Production behavior is unchanged for healthy clusters — the timeout
// only fires on TCP-connect / TLS-handshake hangs.
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
