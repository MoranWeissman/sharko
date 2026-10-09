package capabilities

import (
	"context"
	"errors"
	"testing"
)

func TestHubPlatformDetector_Classification(t *testing.T) {
	tests := []struct {
		name    string
		version string
		err     error
		want    string
	}{
		{name: "eks version string", version: "v1.29.3-eks-a5df8c2", want: HubPlatformEKS},
		{name: "eks version string different patch", version: "v1.31.0-eks-1234567", want: HubPlatformEKS},
		{name: "kind version string", version: "v1.29.2", want: HubPlatformUnknown},
		{name: "self-hosted rke2 style", version: "v1.28.5+rke2r1", want: HubPlatformUnknown},
		{name: "empty version", version: "", want: HubPlatformUnknown},
		{name: "version fetch error", version: "", err: errors.New("connection refused"), want: HubPlatformUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewHubPlatformDetector(func(ctx context.Context) (string, error) {
				return tt.version, tt.err
			})
			got := d.Detect(context.Background())
			if got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHubPlatformDetector_NilVersionFn_DegradesToUnknown(t *testing.T) {
	d := NewHubPlatformDetector(nil)
	got := d.Detect(context.Background())
	if got != HubPlatformUnknown {
		t.Errorf("Detect() = %q, want %q", got, HubPlatformUnknown)
	}
}

func TestHubPlatformDetector_CachesResult(t *testing.T) {
	calls := 0
	d := NewHubPlatformDetector(func(ctx context.Context) (string, error) {
		calls++
		return "v1.29.3-eks-a5df8c2", nil
	})

	for i := 0; i < 5; i++ {
		d.Detect(context.Background())
	}

	if calls != 1 {
		t.Errorf("versionFn called %d times, want exactly 1 (cached)", calls)
	}
}
