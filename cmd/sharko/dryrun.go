package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cliFilePreview mirrors orchestrator.FilePreview — the file preview shape
// every dry-run response carries. Diff is an already-redacted unified diff
// (the server redacts every scalar leaf before it ever leaves the process —
// see internal/orchestrator/preview_diff.go). The CLI prints it verbatim.
type cliFilePreview struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Diff   string `json:"diff,omitempty"`
}

// cliVerification mirrors verify.Result's fields that matter to a dry-run
// preview — the connectivity check add-cluster runs before it opens a PR.
type cliVerification struct {
	Success      bool   `json:"success"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// cliDryRunResult mirrors orchestrator.DryRunResult — the one dry-run shape
// every write command returns. Every command with a --dry-run flag
// (add-cluster, adopt, unadopt-cluster, takeover, drop-legacy-labels,
// enable-addon, disable-addon, upgrade-clusters) decodes its response's
// dry_run field into this struct and hands it to printDryRun.
type cliDryRunResult struct {
	EffectiveAddons []string         `json:"effective_addons"`
	FilesToWrite    []cliFilePreview `json:"files_to_write"`
	PRTitle         string           `json:"pr_title"`
	SecretsToCreate []string         `json:"secrets_to_create"`
	Verification    *cliVerification `json:"verification,omitempty"`
}

// printDryRun renders a dry-run preview the same way everywhere: the PR
// title, the effective addon set, the secrets that would be created, the
// connectivity check (when there is one), and — per file — the action plus
// the exact diff content the server computed. Printing the diff is the
// whole point: a preview that only names the file and says "update"
// (add-cluster's old behavior) leaves the operator guessing at what
// actually changes.
func printDryRun(dr *cliDryRunResult) {
	if dr == nil {
		return
	}
	fmt.Println("Dry-run preview (no changes made):")
	if dr.PRTitle != "" {
		fmt.Printf("  PR:      %s\n", dr.PRTitle)
	}
	if len(dr.EffectiveAddons) > 0 {
		fmt.Printf("  Addons:  %s\n", strings.Join(dr.EffectiveAddons, ", "))
	}
	if len(dr.SecretsToCreate) > 0 {
		fmt.Printf("  Secrets: %s\n", strings.Join(dr.SecretsToCreate, ", "))
	}
	if dr.Verification != nil {
		if dr.Verification.Success {
			fmt.Println("  Verify:  passed")
		} else {
			fmt.Printf("  Verify:  FAILED [%s] %s\n", dr.Verification.ErrorCode, dr.Verification.ErrorMessage)
		}
	}
	if len(dr.FilesToWrite) > 0 {
		fmt.Println("  Files:")
		for _, f := range dr.FilesToWrite {
			fmt.Printf("    [%s] %s\n", f.Action, f.Path)
			if f.Diff != "" {
				for _, line := range strings.Split(f.Diff, "\n") {
					fmt.Printf("      %s\n", line)
				}
			}
		}
	}
}

// cliGitResult mirrors orchestrator.GitResult — the shape every v4 write
// command (enable-addon, disable-addon, upgrade-clusters) returns on
// success, and — with DryRun set instead of the other fields — on a
// --dry-run preview.
type cliGitResult struct {
	PRUrl      string           `json:"pr_url,omitempty"`
	PRID       int              `json:"pr_id,omitempty"`
	Branch     string           `json:"branch,omitempty"`
	Merged     bool             `json:"merged"`
	CommitSHA  string           `json:"commit_sha,omitempty"`
	ValuesFile string           `json:"values_file,omitempty"`
	DryRun     *cliDryRunResult `json:"dry_run,omitempty"`
}

// printGitResult prints a cliGitResult the same way every v4 write command
// does: the dry-run preview when there is one, otherwise the PR/branch the
// write produced.
func printGitResult(g *cliGitResult) {
	if g == nil {
		return
	}
	if g.DryRun != nil {
		printDryRun(g.DryRun)
		return
	}
	if g.PRUrl != "" {
		if g.Merged {
			fmt.Printf("  PR: %s (merged)\n", g.PRUrl)
		} else {
			fmt.Printf("  PR: %s (open — merge manually)\n", g.PRUrl)
		}
	} else if g.Branch != "" {
		fmt.Printf("  Branch: %s\n", g.Branch)
	}
	if g.ValuesFile != "" {
		fmt.Printf("  Values file: %s\n", g.ValuesFile)
	}
}

// unwrapAttribution undoes the server's withAttributionWarning wrapping.
// Some v4 write endpoints (takeover, upgrade-clusters) use a tiered Git
// credential resolver: when the caller has no per-user token configured and
// the request fell back to Sharko's shared service credential, the response
// body is wrapped as {"result": <the real payload>, "attribution_warning":
// "no_per_user_pat"} instead of being the payload directly. Every other
// response is unaffected. This unwraps that shape when present and returns
// the inner payload bytes plus a plain-English note to print, or the
// original body and an empty note otherwise.
func unwrapAttribution(body []byte) ([]byte, string) {
	var probe struct {
		AttributionWarning string          `json:"attribution_warning"`
		Result             json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return body, ""
	}
	if probe.AttributionWarning == "" || len(probe.Result) == 0 {
		return body, ""
	}
	return probe.Result, "the pull request was committed using Sharko's shared Git credential, not one tied to you — set up a personal access token to change that"
}
