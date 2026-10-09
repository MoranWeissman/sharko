package main

import "testing"

// RC-1 — cmd/sharko/version_cmd.go always printed "(invalid health response)"
// against a real server because it decoded the health body into
// map[string]string, and internal/api/health.go always writes a bool
// cluster_test_available field which cannot unmarshal into a string map
// value. formatHealthDetail is the extracted piece that does the decode +
// formatting, so it can be tested without spinning up an HTTP server.
func TestFormatHealthDetail(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDetail string
		wantOK     bool
	}{
		{
			name: "realistic body with bool cluster_test_available field",
			// Captured from a live playground server (see story RC-1).
			body:       `{"cluster_test_available":true,"mode":"Kubernetes","status":"healthy","version":"3.0.0"}`,
			wantDetail: "connected, 3.0.0, Kubernetes",
			wantOK:     true,
		},
		{
			name:       "no mode field drops the mode from the line",
			body:       `{"status":"healthy","version":"3.0.0","cluster_test_available":false}`,
			wantDetail: "connected, 3.0.0",
			wantOK:     true,
		},
		{
			name:       "empty version falls back to unknown",
			body:       `{"status":"healthy","version":"","mode":"Local","cluster_test_available":true}`,
			wantDetail: "connected, unknown, Local",
			wantOK:     true,
		},
		{
			name:       "missing version field falls back to unknown",
			body:       `{"status":"healthy","cluster_test_available":true}`,
			wantDetail: "connected, unknown",
			wantOK:     true,
		},
		{
			name:       "body that is not valid JSON at all",
			body:       `not json`,
			wantDetail: "",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail, ok := formatHealthDetail([]byte(tt.body))
			if ok != tt.wantOK {
				t.Fatalf("formatHealthDetail(%q) ok = %v, want %v", tt.body, ok, tt.wantOK)
			}
			if detail != tt.wantDetail {
				t.Errorf("formatHealthDetail(%q) = %q, want %q", tt.body, detail, tt.wantDetail)
			}
		})
	}
}
