// Command gen-provider-types reads the cluster-credentials provider factory
// switch in internal/providers/provider.go and emits a TypeScript `as const`
// literal at ui/src/generated/provider-types.ts so the UI cannot drift out of
// sync with the backend's accepted Type strings.
//
// Background — V125-1-13.7 / OQ #6:
//
//	V125-1-10.7 hand-fixed the Settings → SecretsProviderSection dropdown
//	after `argocd` was added to the backend factory but never propagated to
//	the UI's hardcoded option list. This generator + the matching CI check
//	("Provider Types Up To Date") makes that class of drift impossible:
//	any new arm in providers.New()'s switch + a regenerate run + the CI
//	gate catch stale generated files before they ship.
//
// Approach: parse provider.go via go/parser + walk the AST. Locate the
// FuncDecl named "New", find the *ast.SwitchStmt whose tag is the
// `cfg.Type` selector, and collect the string-literal value of every
// `case` clause. Skip the empty-string auto-default arm and the `default`
// arm. Sort + dedupe deterministically and emit the TS file.
//
// AST is preferred over regex because the switch lives inside a real Go
// program — comments, line breaks, formatting, or future moves of the
// switch body would break a regex but not an AST walker keyed on the
// FuncDecl name + tag selector.
//
// Usage:
//
//	go run ./cmd/gen-provider-types
//
// or via the Makefile:
//
//	make generate-provider-types
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultSourcePath = "internal/providers/provider.go"
	defaultOutputPath = "ui/src/generated/provider-types.ts"
	targetFuncName    = "New"
)

func main() {
	var (
		sourcePath string
		outputPath string
	)
	flag.StringVar(&sourcePath, "source", defaultSourcePath,
		"path to the Go file containing the provider factory")
	flag.StringVar(&outputPath, "output", defaultOutputPath,
		"path to the TypeScript file to (over)write")
	flag.Parse()

	if err := run(sourcePath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "gen-provider-types: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable entry point: it reads `sourcePath`, extracts the valid
// provider Type strings, and writes the rendered TypeScript file to
// `outputPath`. The output directory is created if it doesn't exist.
func run(sourcePath, outputPath string) error {
	types, err := extractProviderTypesFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("extract from %s: %w", sourcePath, err)
	}
	if len(types) == 0 {
		return fmt.Errorf("no provider types extracted from %s — switch parser found nothing", sourcePath)
	}

	rendered := renderTypeScript(types, sourcePath)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outputPath), err)
	}
	if err := os.WriteFile(outputPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	fmt.Printf("gen-provider-types: wrote %d types to %s\n", len(types), outputPath)
	return nil
}

// extractProviderTypesFromFile parses `sourcePath` and returns the sorted,
// deduped list of valid provider Type strings extracted from the
// `New(cfg Config)` switch.
func extractProviderTypesFromFile(sourcePath string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sourcePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return extractProviderTypes(file)
}

// extractProviderTypes walks `file`'s top-level declarations, finds the
// FuncDecl named `New`, locates the switch on `cfg.Type`, and returns the
// sorted/deduped string-literal case values, with the empty-string
// auto-default arm filtered out.
//
// Returns an error if the func or switch can't be located — the generator
// fails loudly rather than silently emitting an empty list (CI would catch
// the empty diff anyway, but a clear error is better than a silent regression).
func extractProviderTypes(file *ast.File) ([]string, error) {
	fn := findFuncDecl(file, targetFuncName)
	if fn == nil {
		return nil, fmt.Errorf("could not find FuncDecl %q at top level", targetFuncName)
	}
	if fn.Body == nil {
		return nil, fmt.Errorf("FuncDecl %q has no body", targetFuncName)
	}

	sw := findSwitchOnCfgType(fn.Body)
	if sw == nil {
		return nil, fmt.Errorf("could not find switch on cfg.Type inside %q", targetFuncName)
	}

	seen := make(map[string]struct{})
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		// `default:` clauses have List == nil — skip them.
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				// Non-string-literal case (shouldn't happen for cfg.Type
				// but be defensive); skip silently.
				continue
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				return nil, fmt.Errorf("unquote case literal %q: %w", lit.Value, err)
			}
			if val == "" {
				// The empty-string auto-default arm — not a user-selectable
				// type, so it is intentionally filtered out of the dropdown.
				continue
			}
			seen[val] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

// findFuncDecl returns the top-level *ast.FuncDecl named `name`, or nil.
// Receiver methods are ignored (the factory we care about is a free function).
func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv != nil {
			continue
		}
		if fn.Name != nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// findSwitchOnCfgType walks `body` and returns the first SwitchStmt whose
// Tag expression is the selector `cfg.Type`. Walking via ast.Inspect lets
// us tolerate the switch being wrapped in a deeper block in the future.
func findSwitchOnCfgType(body *ast.BlockStmt) *ast.SwitchStmt {
	var found *ast.SwitchStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		sel, ok := sw.Tag.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == "cfg" && sel.Sel != nil && sel.Sel.Name == "Type" {
			found = sw
			return false
		}
		return true
	})
	return found
}

// renderTypeScript renders the deterministic TS output. The format is
// pinned by tests and by the CI "Provider Types Up To Date" check — keep
// it byte-stable across refactors.
//
// `sourcePath` is normalized to forward slashes so the header is identical
// regardless of platform (filepath.ToSlash is a no-op on Unix; we also
// rewrite literal backslashes so a Windows-style path passed via flag
// produces the same header).
func renderTypeScript(types []string, sourcePath string) string {
	source := strings.ReplaceAll(filepath.ToSlash(sourcePath), `\`, "/")
	var b strings.Builder
	b.WriteString("// Code generated by cmd/gen-provider-types. DO NOT EDIT.\n")
	b.WriteString("// Source: ")
	b.WriteString(source)
	b.WriteString("::New\n")
	b.WriteString("// Run `make generate-provider-types` to refresh.\n")
	b.WriteString("//\n")
	b.WriteString("// This file is the single source of truth for the set of provider Type\n")
	b.WriteString("// strings the backend factory accepts. The Settings → SecretsProviderSection\n")
	b.WriteString("// dropdown imports VALID_PROVIDER_TYPES so it cannot drift from\n")
	b.WriteString("// internal/providers/provider.go's New() switch — see V125-1-13.7.\n")
	b.WriteString("\n")
	b.WriteString("export const VALID_PROVIDER_TYPES = [\n")
	for _, t := range types {
		b.WriteString("  ")
		b.WriteString(strconv.Quote(t))
		b.WriteString(",\n")
	}
	b.WriteString("] as const\n")
	b.WriteString("\n")
	b.WriteString("export type ProviderType = (typeof VALID_PROVIDER_TYPES)[number]\n")
	return b.String()
}
