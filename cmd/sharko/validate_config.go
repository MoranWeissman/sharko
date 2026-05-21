// V125-1-9 Story 9.5 — `sharko validate-config` CLI subcommand.
//
// This file is the operator-facing front end for the read-time JSON Schema
// validator that landed in Story 9.4 (internal/schema/validator.go). The
// command serves two related use cases:
//
//  1. Interactive: an operator authoring a managed-clusters.yaml or
//     addon-catalog.yaml file locally wants to know "is this valid?"
//     BEFORE they commit + push and discover the answer in a CI failure
//     three minutes later.
//
//  2. Automated: the validate-sharko-config GitHub Actions job (see
//     .github/workflows/ci.yml) shells out to this binary on every
//     changed YAML file in a PR diff. Per-file auto-skip of non-Sharko
//     YAML means the CI job doesn't have to maintain its own
//     allow/block list — the binary itself decides what's Sharko-owned.
//
// Naming: this command is deliberately NOT a rename of the existing
// `sharko validate` (cmd/sharko/validate.go), which is a legacy
// pre-envelope validator over the bare-YAML shape. The two coexist
// during V125 so users with old workflows don't break; the legacy
// command will be removed in V126 alongside the legacy reader path
// (per Story 9.6 migration runbook).
//
// Exit codes follow the standard CLI contract:
//
//	0 = all inputs validated (or were correctly skipped as non-Sharko)
//	1 = at least one input failed validation
//
// Any internal error (validator construction failure, unreadable path)
// is wrapped through cobra's RunE → Execute() → os.Exit(1) chain.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	sharkoschema "github.com/MoranWeissman/sharko/internal/schema"
)

// validateConfigQuiet is bound to the --quiet/-q flag. When true, the
// per-file "✓ <path>" pass lines are suppressed; only failures and the
// final summary are printed. Stored as a package var (rather than on a
// struct) to match the convention used by other Sharko CLI commands
// (e.g. validate.go's flat function literals).
var validateConfigQuiet bool

func init() {
	validateConfigCmd.Flags().BoolVarP(&validateConfigQuiet, "quiet", "q", false,
		"suppress per-file pass lines (only show failures + summary)")
	rootCmd.AddCommand(validateConfigCmd)
}

// validateConfigCmd is the Cobra registration. Long-form help text
// follows the spec §175 of epics-v125-1-9.md verbatim — keeping the
// wording in lockstep with the design doc means an operator reading
// the help and an operator reading the spec see the same words.
//
// SilenceErrors + SilenceUsage are set because runValidateConfig
// already prints per-file ✘ lines plus a summary line on the validation
// failure path; cobra's default error-printing would duplicate that
// information ("Error: validation failed") and emit the usage block
// (which is irrelevant for a validation failure). We still return the
// errValidationFailed sentinel from RunE so cobra's Execute exits 1.
var validateConfigCmd = &cobra.Command{
	Use:   "validate-config <file-or-directory>",
	Short: "Validate Sharko configuration YAML against committed JSON Schema",
	Long: `Validate Sharko configuration YAML against committed JSON Schema.

Usage:
  sharko validate-config <file>
  sharko validate-config <directory>
  sharko validate-config --quiet <directory>

Validates files whose top-level apiVersion is sharko.io/v1 against the
committed schemas at internal/schema/*.v1.json. Files without that
apiVersion are skipped (not Sharko-managed). Exits 0 if all files
validate or are skipped; exits 1 if any file fails validation.

Examples:
  # Validate a single file
  sharko validate-config managed-clusters.yaml

  # Validate every YAML in the bootstrap configuration
  sharko validate-config templates/bootstrap/configuration/

  # CI use: quiet mode in repo root
  sharko validate-config --quiet .`,
	Args:          cobra.ExactArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := runValidateConfig(cmd.OutOrStdout(), args[0], validateConfigQuiet)
		if errors.Is(err, errValidationFailed) {
			// The runner has already printed the per-file ✘ lines and
			// the "N file(s) failed validation" footer. Exiting
			// directly (rather than returning the error) prevents
			// Execute() from printing a redundant "validation failed"
			// line on stderr after our footer. Tests bypass cobra and
			// drive runValidateConfig directly, so they still see the
			// typed sentinel.
			os.Exit(1)
		}
		return err
	},
}

// fileVerdict captures the per-file outcome. The CLI prints one of
// {pass, skip, fail} per input file and aggregates the failures into a
// single exit code. Keeping this as a small struct (rather than three
// parallel slices) makes the summary loop readable and lets test code
// assert against a single ordered list.
type fileVerdict struct {
	path    string
	kind    string // verdict: "pass" | "skip" | "fail"
	reason  string // skip reason or failure summary
	details []string
}

// runValidateConfig is the testable body of validateConfigCmd. Splitting
// it out of the cobra RunE closure lets the unit tests exercise the
// full flow against an in-memory writer without going through
// cobra.Command.Execute (which would also try to parse global flags
// like --server that aren't relevant here).
//
// Returns a typed sentinel (errValidationFailed) when one or more files
// fail validation, so cobra exits 1 without printing the Go error to
// stderr a second time (validation failures are already printed inline).
// Other returned errors signal genuine internal failures (unreadable
// path, validator construction failure) and surface verbatim.
func runValidateConfig(out interface{ Write([]byte) (int, error) }, target string, quiet bool) error {
	validator, err := sharkoschema.DefaultValidator()
	if err != nil {
		// Validator construction is a build-time invariant. Surface the
		// error verbatim so a packaging bug (missing embedded schema)
		// produces a clear "internal: ..." message rather than a silent
		// "no files validated" exit-0.
		return fmt.Errorf("internal: schema validator construction failed: %w", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot stat %q: %w", target, err)
	}

	var files []string
	if info.IsDir() {
		files, err = collectYAMLFiles(target)
		if err != nil {
			return fmt.Errorf("walking directory %q: %w", target, err)
		}
	} else {
		files = []string{target}
	}

	if len(files) == 0 {
		// Empty directory of YAMLs is a no-op success — same shape as
		// the GH Actions job's "no YAML changes in this PR" skip path.
		fmt.Fprintf(out, "no YAML files found under %s\n", target)
		return nil
	}

	verdicts := make([]fileVerdict, 0, len(files))
	failCount := 0
	for _, f := range files {
		v := validateSingleFile(validator, f)
		verdicts = append(verdicts, v)
		if v.kind == "fail" {
			failCount++
		}
	}

	printVerdicts(out, verdicts, quiet)

	if failCount > 0 {
		// Print the actionable summary on stderr-equivalent (still
		// `out` for test capture parity). Using `fmt.Fprintf` keeps the
		// output stream consistent so test fixtures can scan one
		// writer for both pass lines and the summary footer.
		fmt.Fprintf(out, "\n%d file(s) failed validation\n", failCount)
		return errValidationFailed
	}
	return nil
}

// errValidationFailed is the typed sentinel returned by runValidateConfig
// when one or more files fail validation. cobra prints any RunE error
// to stderr by default; we want the per-file ✘ lines + summary to be
// the only output, so the cobra command sets SilenceErrors+SilenceUsage
// when this sentinel surfaces. We still use a typed error (rather than
// returning nil + an exit code) so callers integrating the runner from
// Go code (e.g. unit tests) can distinguish "validation failed" from
// "everything passed".
var errValidationFailed = errors.New("validation failed")

// collectYAMLFiles walks dir and returns every regular file with a
// .yaml or .yml extension, sorted lexicographically so the per-file
// output (and the summary it feeds) is stable across runs. WalkDir
// (Go 1.16+) is preferred over filepath.Walk for the cheaper Lstat-only
// fast path.
//
// Hidden directories (anything starting with ".") are skipped — this
// keeps `sharko validate-config .` from descending into `.git` or
// `.github`, which would otherwise burn a few seconds enumerating
// workflow YAMLs only to skip every one of them.
func collectYAMLFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			// Don't apply the hidden-dir skip to the input dir itself —
			// `sharko validate-config .git` should still try to walk
			// it, even though the result will be all-skip; surprising
			// the user with "empty" would be worse than surprising
			// them with "12 files skipped". The check below only fires
			// on *descendants*, not the root.
			if path != dir && strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// filepath.WalkDir already yields lexicographic order, but be
	// explicit so the contract doesn't break under future Go runtime
	// changes — the per-file output stability is load-bearing for the
	// `--quiet` summary tests and the CI step's log grepability.
	return out, nil
}

// validateSingleFile reads one file, decides whether it's a Sharko
// envelope (apiVersion: sharko.io/v1) and either validates or skips it.
// Returns a fileVerdict so the caller can aggregate verdicts and pick
// an exit code at the end.
//
// Skip semantics: any file that isn't Sharko-enveloped (no apiVersion,
// or a different apiVersion like core/v1 for a K8s Pod) is reported as
// "skip" with a one-line reason. We deliberately do NOT treat a missing
// apiVersion as a validation failure — the CI hook runs over every
// changed YAML in a PR, and most YAML in this repo (workflows, Helm
// templates, kustomize, kind configs, etc.) is not Sharko-managed.
// Treating "not Sharko-owned" as "fail" would block every PR.
func validateSingleFile(v *sharkoschema.Validator, path string) fileVerdict {
	body, err := os.ReadFile(path)
	if err != nil {
		return fileVerdict{path: path, kind: "fail", reason: "read error", details: []string{err.Error()}}
	}

	enveloped, err := sharkoschema.IsEnveloped(body)
	if err != nil {
		// Malformed YAML at the envelope-detection step. We could
		// argue this is a "skip" (the file isn't even valid YAML so
		// can't be a Sharko envelope), but the operator's intent in
		// running validate-config is "tell me if my file is OK", and a
		// file that doesn't parse as YAML is clearly not OK. Fail
		// loudly with the parser error.
		return fileVerdict{
			path:    path,
			kind:    "fail",
			reason:  "YAML parse error",
			details: []string{err.Error()},
		}
	}
	if !enveloped {
		return fileVerdict{path: path, kind: "skip", reason: "not a Sharko-enveloped file"}
	}

	// Peek the kind so we can include the schema URL pointer in the
	// failure output ("→ for details: https://sharko.io/schemas/<kind>.v1.json").
	// The validator does the same peek internally via ValidateAutoDetect,
	// but we want the kind in hand for both the success and failure
	// paths to avoid a second decode.
	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(body, &header); err != nil {
		return fileVerdict{path: path, kind: "fail", reason: "YAML parse error", details: []string{err.Error()}}
	}

	if err := v.ValidateAutoDetect(body); err != nil {
		var failure *sharkoschema.ValidationFailure
		if errors.As(err, &failure) {
			return fileVerdict{
				path:    path,
				kind:    "fail",
				reason:  fmt.Sprintf("schema violations (kind: %s)", failure.Kind),
				details: append(failure.Violations, fmt.Sprintf("→ for details: %s", schemaURLForKind(failure.Kind))),
			}
		}
		// Non-ValidationFailure error: unknown kind, decode failure,
		// nil-validator. These are still validation problems from the
		// operator's perspective, but the error string is the most
		// useful thing we have.
		return fileVerdict{
			path:    path,
			kind:    "fail",
			reason:  "validator error",
			details: []string{err.Error()},
		}
	}
	return fileVerdict{path: path, kind: "pass"}
}

// schemaURLForKind maps a Sharko envelope kind to its canonical public
// schema URL — the same URL the generator embeds in each schema's $id
// (cmd/schema-gen/main.go) and that the bootstrap YAML files reference
// via the yaml-language-server directive. Operators who follow the
// link see the actual schema in their browser, which (combined with
// the violation list) is enough to fix most failures self-service.
//
// Unknown kinds fall back to the schemas index URL so the operator at
// least lands on a useful page rather than a 404. Should never happen
// in practice because ValidateAutoDetect rejects unknown kinds before
// we get here, but defensive default-case is cheap.
func schemaURLForKind(kind string) string {
	switch kind {
	case sharkoschema.KindManagedClusters:
		return sharkoschema.ManagedClustersSchemaID
	case sharkoschema.KindAddonCatalog:
		return sharkoschema.AddonCatalogSchemaID
	default:
		return "https://sharko.io/schemas/"
	}
}

// printVerdicts writes the per-file summary to out, honouring --quiet.
// Format:
//
//	✓ path/to/valid.yaml                       (pass; suppressed when quiet)
//	skip: path/to/non-sharko.yaml (reason)     (always shown; cheap signal)
//	✘ path/to/invalid.yaml: <reason>           (always shown)
//	   ✘ /spec/clusters/0: missing "name"      (per-violation indent)
//	   → for details: https://sharko.io/...    (schema URL pointer)
//
// Keeping the formatting helper isolated makes the test harness simpler:
// the tests assert against the exact lines emitted here without having
// to mock the validator + walker.
func printVerdicts(out interface{ Write([]byte) (int, error) }, verdicts []fileVerdict, quiet bool) {
	for _, v := range verdicts {
		switch v.kind {
		case "pass":
			if !quiet {
				fmt.Fprintf(out, "✓ %s\n", v.path)
			}
		case "skip":
			// Always show skip lines — they're the operator's signal
			// that "yes, the tool saw the file and decided it's not
			// Sharko-managed", which is different from "the tool
			// never looked at it". Quiet mode suppresses passes
			// (noise) but not skips (information).
			fmt.Fprintf(out, "skip: %s (%s)\n", v.path, v.reason)
		case "fail":
			fmt.Fprintf(out, "✘ %s: %s\n", v.path, v.reason)
			for _, d := range v.details {
				if strings.HasPrefix(d, "→ ") {
					// Schema URL pointer line — emit with a single
					// leading space-indent so it visually attaches to
					// the failing file but stays distinct from the
					// violation list.
					fmt.Fprintf(out, "   %s\n", d)
					continue
				}
				fmt.Fprintf(out, "   ✘ %s\n", d)
			}
		}
	}
}
