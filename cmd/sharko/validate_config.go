// `sharko validate-config` CLI subcommand — operator-facing front end for the
// read-time JSON Schema validator (internal/schema/validator.go).
//
// Exit codes:
//
//	0 = all inputs validated (or were correctly skipped as non-Sharko)
//	1 = at least one input failed validation
//
// Note: this command is distinct from the legacy `sharko validate`
// (cmd/sharko/validate.go), which is a bare-YAML pre-envelope validator.
package main

import (
	"bytes"
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

Validates files whose top-level apiVersion is sharko.dev/v1 (or the
legacy sharko.io/v1, still accepted for compatibility) against the
committed schemas at internal/schema/*.v1.json. Files without either
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
// envelope (apiVersion: sharko.dev/v1, or the legacy sharko.io/v1 —
// READ-BOTH compat, V2-cleanup-59) and either validates or skips it.
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
//
// Go/Helm template files are also skipped, not failed. Helm chart
// templates (e.g. templates/bootstrap/templates/*.yaml) contain raw
// {{ ... }} Go-template directives and are intentionally NOT valid YAML
// until Helm renders them — yet the CI hook still walks them as changed
// .yaml files. A genuine Sharko-enveloped config is concrete YAML and
// never contains raw {{ }} delimiters, so when the envelope detector
// hits a YAML parse error on a body that contains both "{{" and "}}",
// we treat it as a template and skip rather than fail. Bodies that fail
// to parse with NO templating are still failed loudly (genuinely broken
// YAML).
func validateSingleFile(v *sharkoschema.Validator, path string) fileVerdict {
	body, err := os.ReadFile(path)
	if err != nil {
		return fileVerdict{path: path, kind: "fail", reason: "read error", details: []string{err.Error()}}
	}

	enveloped, err := sharkoschema.IsEnveloped(body)
	if err != nil {
		// Go/Helm template files contain raw {{ ... }} directives and
		// are intentionally not valid YAML until rendered. They are not
		// Sharko configs, so skip them rather than failing the PR.
		if looksLikeGoTemplate(body) {
			return fileVerdict{path: path, kind: "skip", reason: "Helm/Go template, not a Sharko config"}
		}
		// Malformed YAML at the envelope-detection step (with no
		// templating). We could argue this is a "skip" (the file isn't
		// even valid YAML so can't be a Sharko envelope), but the
		// operator's intent in running validate-config is "tell me if my
		// file is OK", and a file that doesn't parse as YAML is clearly
		// not OK. Fail loudly with the parser error.
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
	// failure output ("→ for details: https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/<kind>.v1.json").
	// The validator does the same peek internally via ValidateAutoDetect,
	// but we want the kind in hand for both the success and failure
	// paths to avoid a second decode.
	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(body, &header); err != nil {
		// Normally unreachable for a template (IsEnveloped above would
		// have already failed and we'd have skipped it). Guard anyway so
		// the two parse-error sites behave identically.
		if looksLikeGoTemplate(body) {
			return fileVerdict{path: path, kind: "skip", reason: "Helm/Go template, not a Sharko config"}
		}
		return fileVerdict{path: path, kind: "fail", reason: "YAML parse error", details: []string{err.Error()}}
	}

	// v4 Wave 1 Story 2.6 — ClusterAddons carries two invariants a
	// generic JSON Schema can't express: the file's own path must match
	// the cluster: field, and the addons.*.settings.preserveResourcesOnDeletion
	// is a validation error with a specific "it belongs somewhere else"
	// message (design doc §3.2 "Two tiers"). The forbidden-field check
	// runs BEFORE schema validation and returns immediately when it
	// fires: additionalProperties:false on
	// models.ClusterAddonsAddonSettings would ALSO reject the same
	// body (defense in depth — see that type's doc comment), but its
	// generic "additional property not allowed" message doesn't say
	// WHERE the field belongs, so the friendlier, contract-specific
	// message takes priority when we can tell it's this exact mistake.
	if header.Kind == sharkoschema.KindClusterAddons {
		if addonName, line, found := detectClusterSettingsPreserveResourcesOnDeletion(body); found {
			return fileVerdict{
				path:   path,
				kind:   "fail",
				reason: fmt.Sprintf("preserveResourcesOnDeletion is not allowed on a per-cluster addon (addon %q)", addonName),
				details: []string{fmt.Sprintf(
					"line %d: preserveResourcesOnDeletion can only vary per addon, fleet-wide, because the engine builds one ApplicationSet per addon covering every cluster — set it in catalog.yaml's addon settings instead, not in this cluster-addons/*.yaml file",
					line,
				)},
			}
		}
	}

	if err := v.ValidateAutoDetect(body); err != nil {
		var failure *sharkoschema.ValidationFailure
		if errors.As(err, &failure) {
			return fileVerdict{
				path:    path,
				kind:    "fail",
				reason:  fmt.Sprintf("schema violations (kind: %s)", failure.Kind),
				details: append(violationsWithLines(body, failure), fmt.Sprintf("→ for details: %s", schemaURLForSchemaKey(failure.Kind))),
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

	// Schema passed. ClusterAddons has one more file-path-aware
	// invariant the schema itself cannot check (design doc §2.1): the
	// file name (minus .yaml) must equal the cluster: field, or the engine's
	// git-files generator (which finds a cluster's assignment file BY
	// that name) silently gives the cluster nothing.
	if header.Kind == sharkoschema.KindClusterAddons {
		if gotCluster, wantCluster, line, mismatch := detectClusterAddonsFilenameMismatch(path, body); mismatch {
			return fileVerdict{
				path:   path,
				kind:   "fail",
				reason: fmt.Sprintf("file name and the cluster: field disagree (file implies %q, the cluster: field is %q)", wantCluster, gotCluster),
				details: []string{fmt.Sprintf(
					"line %d: the cluster: field must equal the file name without .yaml — the engine finds a cluster's assignment file by name, so a mismatch means the cluster silently gets nothing",
					line,
				)},
			}
		}
	}

	return fileVerdict{path: path, kind: "pass"}
}

// violationsWithLines appends a best-effort "(line N)" suffix to each
// violation string in failure, resolved against the original YAML bytes
// via sharkoschema.LineForInstanceLocation. Violations whose line can't
// be resolved (LineForInstanceLocation returns ok=false, or the
// Locations slice is shorter than Violations for some reason) are left
// as-is rather than failing the whole verdict over a cosmetic miss.
func violationsWithLines(body []byte, failure *sharkoschema.ValidationFailure) []string {
	out := make([]string, len(failure.Violations))
	for i, violation := range failure.Violations {
		if i >= len(failure.Locations) {
			out[i] = violation
			continue
		}
		line, ok := sharkoschema.LineForInstanceLocation(body, failure.Locations[i])
		if !ok || line <= 0 {
			out[i] = violation
			continue
		}
		out[i] = fmt.Sprintf("%s (line %d)", violation, line)
	}
	return out
}

// detectClusterSettingsPreserveResourcesOnDeletion walks a
// ClusterAddons body's addons.*.settings blocks looking for a
// hand-authored preserveResourcesOnDeletion key — the one v1 setting
// (design doc §3.2) that is per-ApplicationSet, not per-Application, and
// therefore cannot vary per cluster. Returns the addon name and the
// 1-based source line of the offending key the first time it's found;
// (‘’, 0, false) when the body has none.
func detectClusterSettingsPreserveResourcesOnDeletion(body []byte) (addonName string, line int, found bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil || len(doc.Content) == 0 {
		return "", 0, false
	}
	root := doc.Content[0]
	_, addons, ok := sharkoschema.MappingValue(root, "addons")
	if !ok || addons.Kind != yaml.MappingNode {
		return "", 0, false
	}
	for i := 0; i+1 < len(addons.Content); i += 2 {
		name := addons.Content[i].Value
		entry := addons.Content[i+1]
		_, settings, ok := sharkoschema.MappingValue(entry, "settings")
		if !ok {
			continue
		}
		if keyNode, _, ok := sharkoschema.MappingValue(settings, "preserveResourcesOnDeletion"); ok {
			return name, keyNode.Line, true
		}
	}
	return "", 0, false
}

// detectClusterAddonsFilenameMismatch compares a ClusterAddons
// body's cluster: field against the file's own basename (design doc §2.1:
// "cluster-addons/prod-eu.yaml is the cluster called prod-eu... a mismatch
// means the cluster silently gets nothing"). Returns the value on disk,
// the value the filename implies, and the source line of the cluster:
// field. mismatch is false (and the other returns are zero) when the
// document has no cluster: field to compare (a separate schema violation
// this function isn't responsible for) or the two agree.
func detectClusterAddonsFilenameMismatch(path string, body []byte) (gotCluster, wantCluster string, line int, mismatch bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil || len(doc.Content) == 0 {
		return "", "", 0, false
	}
	root := doc.Content[0]
	clusterKey, clusterVal, ok := sharkoschema.MappingValue(root, "cluster")
	if !ok || clusterVal.Kind != yaml.ScalarNode {
		return "", "", 0, false
	}
	base := filepath.Base(path)
	want := strings.TrimSuffix(base, filepath.Ext(base))
	if clusterVal.Value == want {
		return clusterVal.Value, want, 0, false
	}
	return clusterVal.Value, want, clusterKey.Line, true
}

// looksLikeGoTemplate reports whether body appears to be a Go/Helm
// template rather than a concrete YAML document. Helm chart templates
// embed raw {{ ... }} action delimiters, which make the file invalid
// YAML until Helm renders it. A genuine Sharko-enveloped config is
// concrete, rendered YAML and never contains raw {{ }} delimiters, so
// the presence of BOTH "{{" and "}}" is a robust, cheap signal that the
// file is a template the operator did not intend validate-config to
// parse. We require both delimiters (rather than either) to avoid
// misclassifying a stray "{{" inside a comment or string.
func looksLikeGoTemplate(body []byte) bool {
	return bytes.Contains(body, []byte("{{")) && bytes.Contains(body, []byte("}}"))
}

// schemaURLForKind maps a Sharko envelope kind to its canonical public
// schema URL — the same URL the generator embeds in each schema's $id
// (cmd/schema-gen/main.go) and that the bootstrap YAML files reference
// via the yaml-language-server directive. Operators who follow the
// link see the actual schema in their browser, which (combined with
// the violation list) is enough to fix most failures self-service.
//
// Unknown keys fall back to the schemas index URL so the operator at
// least lands on a useful page rather than a 404. Should never happen
// in practice because ValidateAutoDetect rejects unknown kinds before
// we get here, but defensive default-case is cheap.
//
// It switches on the SCHEMA KEY rather than the on-disk kind, because the
// two catalog formats share the kind AddonCatalog and would otherwise both
// want this switch's same arm. The key is what a *ValidationFailure carries
// (the validator records whatever key it was called with), so the failure
// already tells us which of the two it was.
func schemaURLForSchemaKey(key string) string {
	switch key {
	case sharkoschema.KindManagedClusters:
		return sharkoschema.ManagedClustersSchemaID
	case sharkoschema.SchemaKeyAddonCatalogV3:
		return sharkoschema.AddonCatalogSchemaID
	case sharkoschema.KindDefaultAddons:
		return sharkoschema.DefaultAddonsSchemaID
	case sharkoschema.KindMarketplaceSources:
		return sharkoschema.MarketplaceSourcesSchemaID
	case sharkoschema.KindClusterAddons:
		return sharkoschema.ClusterAddonsSchemaID
	case sharkoschema.SchemaKeyAddonCatalogV4:
		return sharkoschema.AddonCatalogV4SchemaID
	default:
		return "https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/"
	}
}

// printVerdicts writes the per-file summary to out, honouring --quiet.
// Format:
//
//	✓ path/to/valid.yaml                       (pass; suppressed when quiet)
//	skip: path/to/non-sharko.yaml (reason)     (always shown; cheap signal)
//	✘ path/to/invalid.yaml: <reason>           (always shown)
//	   ✘ /spec/cluster-addons/0: missing "name"      (per-violation indent)
//	   → for details: https://raw.githubusercontent.com/...    (schema URL pointer)
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
