package schema

import "testing"

func TestIsEnveloped(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		want    bool
		wantErr bool
	}{
		{
			name: "enveloped sharko.dev/v1 returns true",
			body: `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`,
			want:    true,
			wantErr: false,
		},
		{
			// READ-BOTH compat (V2-cleanup-59): files authored before the
			// group rename keep routing to the enveloped reader path.
			name: "legacy enveloped sharko.io/v1 returns true",
			body: `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`,
			want:    true,
			wantErr: false,
		},
		{
			name: "legacy bare yaml without apiVersion returns false",
			body: `clusters:
  - name: prod-eu
    server: https://prod-eu-api.example.com
`,
			want:    false,
			wantErr: false,
		},
		{
			name: "k8s-style apiVersion v1 is not sharko envelope",
			body: `apiVersion: v1
kind: ConfigMap
metadata:
  name: foo
`,
			want:    false,
			wantErr: false,
		},
		{
			name: "future sharko.dev/v2 is treated as legacy by this v1 reader",
			body: `apiVersion: sharko.dev/v2
kind: ManagedClusters
metadata:
  name: managed-clusters
spec: {}
`,
			want:    false,
			wantErr: false,
		},
		{
			name: "old-domain future group sharko.io/v2 is not recognised either",
			body: `apiVersion: sharko.io/v2
kind: ManagedClusters
metadata:
  name: managed-clusters
spec: {}
`,
			want:    false,
			wantErr: false,
		},
		{
			name:    "empty body returns false without error",
			body:    "",
			want:    false,
			wantErr: false,
		},
		{
			name:    "whitespace-only body returns false without error",
			body:    "   \n  \t\n",
			want:    false,
			wantErr: false,
		},
		{
			name:    "malformed yaml returns error",
			body:    "apiVersion: sharko.dev/v1\n  kind: : : :\n\tbad indent\n",
			want:    false,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := IsEnveloped([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("IsEnveloped(%q) want error, got nil (returned %v)", tc.name, got)
				}
			} else {
				if err != nil {
					t.Fatalf("IsEnveloped(%q) unexpected error: %v", tc.name, err)
				}
			}
			if got != tc.want {
				t.Errorf("IsEnveloped(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
