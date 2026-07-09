package capabilities

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// eksVersionMarker is the substring the Kubernetes server version string
// carries on EKS control planes (e.g. "v1.29.3-eks-a5df8c2").
const eksVersionMarker = "-eks-"

// Hub-platform values (systemCapabilitiesResponse.HubPlatform).
const (
	HubPlatformEKS     = "eks"
	HubPlatformUnknown = "unknown"
)

// HubVersionFn resolves the hub cluster's own Kubernetes server version
// string. Returns an error (or an empty string) when no in-cluster
// Kubernetes client is available — the detector degrades to
// HubPlatformUnknown in that case, mirroring the AWS detector's degrade
// stance. Defined here (rather than accepting a kubernetes.Interface
// directly) so this package stays free of the client-go dependency; the
// caller (internal/api) already holds a client and supplies this as a
// closure.
type HubVersionFn func(ctx context.Context) (version string, err error)

// HubPlatformDetector detects and caches whether the hub cluster Sharko
// itself runs on looks like EKS, from the Kubernetes server version string
// alone. The only I/O is the single cached version fetch — classification
// itself is a cheap substring check.
type HubPlatformDetector struct {
	once   sync.Once
	result string

	versionFn HubVersionFn
}

// NewHubPlatformDetector returns a detector that resolves the hub's
// Kubernetes version via versionFn. versionFn may be nil (e.g. no
// in-cluster client wired) — Detect then always returns HubPlatformUnknown
// without ever calling it.
func NewHubPlatformDetector(versionFn HubVersionFn) *HubPlatformDetector {
	return &HubPlatformDetector{versionFn: versionFn}
}

// Detect returns the hub platform classification, computing it on the
// first call and returning the cached result on every subsequent call.
func (d *HubPlatformDetector) Detect(ctx context.Context) string {
	d.once.Do(func() {
		d.result = d.detect(ctx)
	})
	return d.result
}

func (d *HubPlatformDetector) detect(ctx context.Context) string {
	if d.versionFn == nil {
		return HubPlatformUnknown
	}
	version, err := d.versionFn(ctx)
	if err != nil || version == "" {
		slog.Debug("[capabilities] hub platform detection unavailable", "error", err)
		return HubPlatformUnknown
	}
	if strings.Contains(version, eksVersionMarker) {
		return HubPlatformEKS
	}
	return HubPlatformUnknown
}
