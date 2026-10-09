package main

import "testing"

// TestMergeAdminRoleGrant covers the pure decision logic behind
// grantArgoCDAdminRBAC (walk finding: recurring non-fatal 403 on
// app-refresh remediation, caused by a fresh ArgoCD install's empty
// argocd-rbac-cm policy.csv). No cluster required — this only exercises the
// line-matching/merge behavior that decides what gets written.
func TestMergeAdminRoleGrant(t *testing.T) {
	tests := []struct {
		name            string
		current         string
		wantPolicy      string
		wantAlreadyDone bool
	}{
		{
			name:            "empty policy.csv (stock install.yaml) gets exactly the grant line",
			current:         "",
			wantPolicy:      "g, admin, role:admin",
			wantAlreadyDone: false,
		},
		{
			name:            "whitespace-only policy.csv treated as empty",
			current:         "   \n  \n",
			wantPolicy:      "g, admin, role:admin",
			wantAlreadyDone: false,
		},
		{
			name:            "existing unrelated rules are preserved, grant appended",
			current:         "p, role:org-admin, applications, *, */*, allow\ng, someuser, role:org-admin",
			wantPolicy:      "p, role:org-admin, applications, *, */*, allow\ng, someuser, role:org-admin\ng, admin, role:admin",
			wantAlreadyDone: false,
		},
		{
			name:            "grant already present — no change, reported as already done",
			current:         "g, admin, role:admin",
			wantAlreadyDone: true,
		},
		{
			name:            "grant already present among other rules",
			current:         "p, role:org-admin, applications, *, */*, allow\ng, admin, role:admin\ng, someuser, role:org-admin",
			wantAlreadyDone: true,
		},
		{
			name:            "grant present but with incidental surrounding whitespace on its line still matches",
			current:         "p, role:x, applications, *, */*, allow\n  g, admin, role:admin  \n",
			wantAlreadyDone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPolicy, gotAlready := mergeAdminRoleGrant(tt.current)
			if gotAlready != tt.wantAlreadyDone {
				t.Errorf("alreadyGranted = %v, want %v", gotAlready, tt.wantAlreadyDone)
			}
			if !tt.wantAlreadyDone && gotPolicy != tt.wantPolicy {
				t.Errorf("newPolicy = %q, want %q", gotPolicy, tt.wantPolicy)
			}
			if tt.wantAlreadyDone && gotPolicy != tt.current {
				t.Errorf("newPolicy = %q, want unchanged %q when already granted", gotPolicy, tt.current)
			}
		})
	}
}
