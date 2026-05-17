package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestDeriveProviderFromURL exercises the production whitelist plus the
// V125-1-13.x.3 SHARKO_E2E_GIT_HOSTS_ALLOWLIST test-only escape hatch.
//
// Each subtest uses t.Setenv (auto-cleaned at subtest end) so empty/unset
// cases see a clean environment.
func TestDeriveProviderFromURL(t *testing.T) {
	tests := []struct {
		name         string
		envAllowlist string // empty = unset (we explicitly clear it)
		repoURL      string
		wantProvider string
		wantErr      bool
		errContains  string // substring assertion on the error message
	}{
		// Production-path cases — env unset, behaviour must be identical to
		// pre-V125-1-13.x.3 code.
		{
			name:         "github.com → github (env unset)",
			envAllowlist: "",
			repoURL:      "https://github.com/foo/bar",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "github enterprise subdomain → github",
			envAllowlist: "",
			repoURL:      "https://ghe.github.com/foo/bar",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "dev.azure.com → azuredevops",
			envAllowlist: "",
			repoURL:      "https://dev.azure.com/foo/bar/_git/baz",
			wantProvider: string(models.GitProviderAzureDevOps),
		},
		{
			name:         "visualstudio.com legacy → azuredevops",
			envAllowlist: "",
			repoURL:      "https://foo.visualstudio.com/bar/_git/baz",
			wantProvider: string(models.GitProviderAzureDevOps),
		},
		{
			name:         "unknown host rejected when env unset",
			envAllowlist: "",
			repoURL:      "http://gitfake.default.svc.cluster.local/repo.git",
			wantErr:      true,
			errContains:  "unsupported git host",
		},
		{
			name:         "malformed URL rejected",
			envAllowlist: "",
			repoURL:      "://not-a-url",
			wantErr:      true,
			errContains:  "cannot parse git repo URL",
		},

		// V125-1-13.x.3 — env-var allowlist cases.
		{
			name:         "env allowlist: single host accepted",
			envAllowlist: "gitfake.default.svc.cluster.local",
			repoURL:      "http://gitfake.default.svc.cluster.local/repo.git",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "env allowlist: multi-host, second entry matches",
			envAllowlist: "host1,host2",
			repoURL:      "http://host2/repo.git",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "env allowlist: whitespace + empty entries tolerated",
			envAllowlist: " , host3 , , host4 ,",
			repoURL:      "http://host4/repo.git",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "env allowlist: only commas/spaces → still rejects",
			envAllowlist: "  ,, , ",
			repoURL:      "http://gitfake/repo.git",
			wantErr:      true,
			errContains:  "unsupported git host",
		},
		{
			name:         "env allowlist: case insensitive match",
			envAllowlist: "GitFake.Local",
			repoURL:      "http://gitfake.local/repo.git",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "env allowlist: does not divert already-allowed github",
			envAllowlist: "gitfake.local",
			repoURL:      "https://github.com/foo/bar",
			wantProvider: string(models.GitProviderGitHub),
		},
		{
			name:         "env allowlist: wildcards NOT supported",
			envAllowlist: "*.local",
			repoURL:      "http://gitfake.local/repo.git",
			wantErr:      true,
			errContains:  "unsupported git host",
		},
		{
			name:         "env allowlist: empty env is a true no-op",
			envAllowlist: "",
			repoURL:      "http://anything.invalid/repo.git",
			wantErr:      true,
			errContains:  "unsupported git host",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv handles per-subtest cleanup. Setting to "" replicates an
			// unset env from the function's perspective (os.Getenv returns "").
			t.Setenv("SHARKO_E2E_GIT_HOSTS_ALLOWLIST", tc.envAllowlist)

			got, err := deriveProviderFromURL(tc.repoURL)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got provider=%q nil err", got)
				}
				if !errors.Is(err, ErrValidation) {
					t.Errorf("error must wrap ErrValidation, got: %v", err)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error message %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantProvider {
				t.Errorf("provider = %q, want %q", got, tc.wantProvider)
			}
		})
	}
}
