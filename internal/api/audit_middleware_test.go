package api

import (
	"testing"
)

// TestResultFromStatus — V2-cleanup-85.2 regression coverage.
//
// 207 is inside the [200,300) range, so a naive range-first switch swallows
// it under the 2xx case before the `code == 207` arm is ever reached — every
// partial-success response (PR created but not merged, ArgoCD registered
// but Git failed) was mislabeled "success" in the audit log. resultFromStatus
// must test 207 before the 2xx catch-all.
func TestResultFromStatus(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{200, "success"},
		{201, "success"},
		{204, "success"},
		{207, "partial"},
		{400, "rejected"},
		{404, "rejected"},
		{409, "rejected"},
		{499, "rejected"},
		{500, "failure"},
		{502, "failure"},
		{503, "failure"},
	}
	for _, tc := range cases {
		if got := resultFromStatus(tc.code); got != tc.want {
			t.Errorf("resultFromStatus(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}
