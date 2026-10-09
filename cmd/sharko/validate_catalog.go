// `sharko validate-catalog` CLI subcommand — operator/contributor-facing
// front end for the curated catalog loader's existing validation
// (internal/catalog.LoadBytes, internal/catalog/loader.go).
//
// v4 wave 1 Story 3.5 ("contribution path"): a catalog-entry PR must be
// checked by CI before merge. catalog/addons.yaml is NOT an enveloped
// sharko.dev/v1 file (it's a bare `addons: [...]` list, no apiVersion/
// kind/metadata/spec), so `sharko validate-config` — the existing envelope
// validator — silently skips it (IsEnveloped returns false); it is the
// wrong tool for this file. `sharko validate` (validate.go) is also the
// wrong tool: it validates the v3 GitOps-repo deployed-catalog shape
// (`configuration/addons-catalog.yaml`, spec.applicationsets), a
// completely different file and format from the monorepo's own curated
// catalog/addons.yaml.
//
// This command deliberately adds ZERO new validation rules. It is a thin
// CLI wrapper over catalog.LoadBytes — the same function
// cmd/sharko/serve.go calls to load the embedded catalog at server boot,
// and the same function internal/catalog/loader_test.go exercises. A
// contributor's local run and CI's run both go through the exact rules
// the running server enforces: required fields, the `category` /
// `curated_by` allow-lists, security_score bounds, signature.bundle shape,
// duplicate-name detection. See catalog/schema.json for the human-readable
// reference and docs/site/community/contributing-catalog-entries.md for
// the plain-English guide.
//
// Exit codes:
//
//	0 = the file loaded and validated cleanly
//	1 = at least one validation error (message printed, names the entry)
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

func init() {
	rootCmd.AddCommand(validateCatalogCmd)
}

// validateCatalogCmd mirrors validateConfigCmd's SilenceErrors/SilenceUsage
// shape (validate_config.go) for the same reason: RunE already prints a
// clear FAIL line, so cobra's default "Error: ..." + usage block would only
// add noise to CI output.
var validateCatalogCmd = &cobra.Command{
	Use:   "validate-catalog <file>",
	Short: "Validate a curated catalog file against the loader's rules",
	Long: `Validate a curated catalog file (catalog/addons.yaml format) against
internal/catalog's existing loader rules — the same rules the running
Sharko server enforces when it embeds catalog/addons.yaml at build time.

This is a thin CLI front end; there is no separate validator. It checks:
  - required fields (name, description, chart, repo, default_namespace,
    license, maintainers, category, curated_by)
  - repo is an http(s) or oci:// URL
  - category is one of the allowed values
  - curated_by tags are all from the allowed set, with no duplicates
  - security_score, when set, is a number 0-10 or the literal "unknown"
  - signature.bundle, when set, is a non-empty https:// URL
  - no duplicate addon names in the file

Used by CI's "Catalog Format Validation" job to check contributor pull
requests that touch catalog/addons.yaml before merge. Safe to run the
exact same way locally:

  sharko validate-catalog catalog/addons.yaml
`,
	Args:          cobra.ExactArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "FAIL %s: %v\n", path, err)
			return errValidationFailed
		}
		if _, loadErr := catalog.LoadBytes(data); loadErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "FAIL %s: %v\n", path, loadErr)
			return errValidationFailed
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK   %s\n", path)
		return nil
	},
}

// errValidationFailed itself is defined once, in
// cmd/sharko/validate_config.go, and reused here — not redeclared.
