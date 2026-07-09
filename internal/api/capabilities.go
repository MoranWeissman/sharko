package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/capabilities"
)

// errNoHubK8sClient is returned by hubKubeVersion when no in-cluster
// Kubernetes client is wired (dev/local mode, or the reconciler branch in
// cmd/sharko/serve.go never ran). The hub-platform detector treats this the
// same as any other lookup failure — degrade to "unknown".
var errNoHubK8sClient = errors.New("no in-cluster kubernetes client available")

// systemCapabilitiesResponse is the response body for
// GET /api/v1/system/capabilities.
type systemCapabilitiesResponse struct {
	// AWS reports whether Sharko itself is running with an AWS identity,
	// and which mechanism supplied it.
	AWS capabilities.AWSIdentity `json:"aws"`
	// HubPlatform is Sharko's best-effort guess at whether the hub cluster
	// it runs on is EKS, derived from the hub's own Kubernetes server
	// version string. One of "eks" or "unknown" (capabilities.HubPlatformEKS
	// / capabilities.HubPlatformUnknown).
	HubPlatform string `json:"hub_platform"`
}

// handleGetSystemCapabilities godoc
//
// @Summary Get auto-detected system capabilities
// @Description Reports what Sharko has auto-detected about its own runtime: whether it is running with an AWS identity (EKS Pod Identity / IRSA / default credential chain) and, best-effort, whether the hub cluster looks like EKS. Detection is cached for the life of the process — sts:GetCallerIdentity is called at most once, never per-request. Any authenticated user may read this; the register-cluster screen needs it before the user has picked a role.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} systemCapabilitiesResponse "Detected capabilities"
// @Router /system/capabilities [get]
func (s *Server) handleGetSystemCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, systemCapabilitiesResponse{
		AWS:         s.getAWSDetector().Detect(r.Context()),
		HubPlatform: s.getHubPlatformDetector().Detect(r.Context()),
	})
}

// getAWSDetector lazily builds (and caches, via sync.Once) the Server's
// AWSDetector. Lazy rather than wired in NewServer so Server literals built
// directly in tests (a common pattern in this package) don't need to know
// about this dependency to exercise unrelated handlers. Tests in this
// package may also pre-set s.awsDetector directly (a same-package struct
// literal) before the first call — the nil check inside Do preserves that
// injected value instead of clobbering it, which is how
// capabilities_test.go swaps in a fake detector without a network call.
func (s *Server) getAWSDetector() *capabilities.AWSDetector {
	s.awsDetectorOnce.Do(func() {
		if s.awsDetector == nil {
			s.awsDetector = capabilities.NewAWSDetector()
		}
	})
	return s.awsDetector
}

// getHubPlatformDetector lazily builds (and caches) the Server's
// HubPlatformDetector, wired to hubKubeVersion. Same lazy-init and
// test-injection rationale as getAWSDetector.
func (s *Server) getHubPlatformDetector() *capabilities.HubPlatformDetector {
	s.hubPlatformDetectorOnce.Do(func() {
		if s.hubPlatformDetector == nil {
			s.hubPlatformDetector = capabilities.NewHubPlatformDetector(s.hubKubeVersion)
		}
	})
	return s.hubPlatformDetector
}

// hubKubeVersion resolves the hub cluster's own Kubernetes server version
// string via the same in-cluster k8s client already used for orphan-Secret
// ownership checks (k8sClientAndNamespace) — no new wiring needed. Returns
// an error when no in-cluster client is available (dev/local mode); the
// hub-platform detector degrades to "unknown" in that case, same as it
// would for any other failure.
func (s *Server) hubKubeVersion(_ context.Context) (string, error) {
	k8sClient, _, ok := s.k8sClientAndNamespace()
	if !ok {
		return "", errNoHubK8sClient
	}
	info, err := k8sClient.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return info.GitVersion, nil
}
