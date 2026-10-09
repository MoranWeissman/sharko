package api

import "testing"

func TestComputeTargetPlatform(t *testing.T) {
	tests := []struct {
		name        string
		serverURL   string
		credsSource string
		want        string
	}{
		{
			name:      "eks server url",
			serverURL: "https://ABCDEF1234567890.gr7.us-east-1.eks.amazonaws.com",
			want:      targetPlatformEKS,
		},
		{
			name:      "eks server url uppercase host",
			serverURL: "https://ABCDEF.GR7.US-EAST-1.EKS.AMAZONAWS.COM",
			want:      targetPlatformEKS,
		},
		{
			name:        "eks-token creds source, non-eks-looking url",
			serverURL:   "https://custom-dns.internal.example.com:6443",
			credsSource: "eks-token",
			want:        targetPlatformEKS,
		},
		{
			name:      "kind cluster",
			serverURL: "https://kind-test-1:6443",
			want:      targetPlatformUnknown,
		},
		{
			name:      "aks cluster",
			serverURL: "https://my-cluster-dns.hcp.eastus.azmk8s.io:443",
			want:      targetPlatformUnknown,
		},
		{
			name:      "empty server url and creds source",
			serverURL: "",
			want:      targetPlatformUnknown,
		},
		{
			name:      "malformed server url falls back to raw string match",
			serverURL: "://not-a-valid-url.eks.amazonaws.com",
			want:      targetPlatformEKS,
		},
		{
			name:        "inline-kubeconfig creds source, non-eks url",
			serverURL:   "https://10.0.0.1:6443",
			credsSource: "inline-kubeconfig",
			want:        targetPlatformUnknown,
		},
		{
			name:      "eks substring in path only, not hostname - not eks",
			serverURL: "https://self-hosted.example.com/eks.amazonaws.com",
			want:      targetPlatformUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTargetPlatform(tt.serverURL, tt.credsSource)
			if got != tt.want {
				t.Errorf("computeTargetPlatform(%q, %q) = %q, want %q", tt.serverURL, tt.credsSource, got, tt.want)
			}
		})
	}
}
