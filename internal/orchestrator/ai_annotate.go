// Package orchestrator — AI annotation pass for the smart-values pipeline
// (v1.21 Epic V121-7, Stories 7.1 / 7.2 / 7.3).
//
// What this layer does
// ---------------------
// AnnotateValues takes a chart's upstream values.yaml plus an AI client
// and returns:
//
//   - The same YAML body, with one-line `# description` comments
//     injected above non-trivial scalar leaves. Existing comments and
//     key ordering are preserved verbatim — the LLM only adds, never
//     reorders or rewrites.
//   - An ADDITIONAL set of dotted paths the LLM thinks are
//     cluster-specific (network/scale/secret/identity-related) that the
//     orchestrator UNIONS with the heuristic's detection set. The LLM is
//     additive, never subtractive — the heuristic's results always win.
//
// Locked decisions (Moran, 2026-04-19):
//
//   - Single deterministic LLM call per (chart, version). The result is
//     cached in Git (the annotated file in the user's repo) — no
//     ConfigMap, no BoltDB, no Sharko-side store. NFR-V121-1.
//   - HARD-BLOCK on any secret-leak match before the call. See
//     ai_guard.go and the `SecretLeakError` typed return value. There is
//     no "send anyway" override.
//   - Token budget cap: input + output combined < ~50k tokens. We
//     approximate tokens with bytes/4 (the standard ~4 chars/token rule
//     of thumb for English-ish prose); if the values.yaml exceeds the
//     bound, we skip the LLM and return the original bytes + empty
//     additional-paths list. No partial-annotate fallback — the file
//     stays predictable.
//   - Latency budget: 30s per call (context.WithTimeout). Anything slower
//     is treated as "AI not available right now" — heuristic-only output
//     proceeds.
//
// Failure modes are graceful
// --------------------------
// AnnotateValues never fails the addon-add. On any of {AI not
// configured, secret guard fires, token cap exceeded, network timeout,
// LLM returned junk} the function returns the ORIGINAL valuesYAML bytes
// plus an empty additional-paths list and an error that the caller logs
// and continues past. The seed flow's contract with the orchestrator is
// "best-effort annotation; fall through cleanly to heuristic-only".
//
// The caller still surfaces the SecretLeakError specifically because
// that's the one the UI needs to render the dedicated banner — see the
// Story 7.4 wiring in `ui/src/components/ValuesEditor.tsx`.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/metrics"
)

// AnnotateMaxBytes is the upper bound on the upstream values.yaml byte
// size we'll send to the LLM. Approximates the ~50k-token ceiling per the
// epic AC (4 chars ≈ 1 token, ×4 to leave room for the response).
//
// Charts that exceed this bound get a heuristic-only result; the audit
// detail records `ai_skipped=oversize` so the operator can see why.
const AnnotateMaxBytes = 50_000

// AnnotateTimeout is the per-call latency cap. The LLM has 30s to
// respond; on timeout we fall through to heuristic-only output.
//
// 30s is a deliberate choice: long enough that a slow Gemini Flash call
// can complete (typical p99 is ~12s for our prompt size) but short
// enough that Add Addon doesn't feel hung from the operator's seat.
const AnnotateTimeout = 30 * time.Second

// AnnotateResult is what AnnotateValues returns. `AnnotatedYAML` is the
// upstream YAML with inline `# description` comments injected; on any
// graceful skip it's identical to the input. `AdditionalClusterPaths` is
// the set of dotted paths the LLM identified as cluster-specific that
// the splitter should UNION with the heuristic's set.
type AnnotateResult struct {
	AnnotatedYAML          []byte
	AdditionalClusterPaths []string
	// SkipReason is set when the function returned without making (or
	// completing) an LLM call — values are "not_configured",
	// "opted_out", "oversize", "timeout", "llm_error",
	// "parse_error", "secret_blocked". Empty string means the LLM call
	// succeeded and the result was applied.
	SkipReason string
}

// AnnotateValues runs the AI annotation pass on a chart's upstream
// values.yaml. Returns the annotated bytes + additional cluster paths.
//
// The caller MUST treat the SecretLeakError return value specially: it's
// the one error that surfaces to the UI as a banner, not a toast. All
// other errors are logged and the heuristic-only output proceeds.
//
// The function is intentionally synchronous — an addon-add that needs
// AI annotation waits for the LLM. The 30s timeout bounds the worst case;
// users who want async annotation can disable the global toggle and run
// the manual annotate action later.
func AnnotateValues(ctx context.Context, valuesYAML []byte, chartName, chartVersion string, aiClient *ai.Client) (AnnotateResult, error) {
	start := time.Now()
	res := AnnotateResult{AnnotatedYAML: valuesYAML}

	// Quick reject paths — short-circuit before any work.
	if aiClient == nil || !aiClient.IsEnabled() {
		res.SkipReason = "not_configured"
		recordAnnotate(res.SkipReason, time.Since(start))
		return res, nil
	}
	if len(valuesYAML) == 0 {
		res.SkipReason = "empty_input"
		recordAnnotate(res.SkipReason, time.Since(start))
		return res, nil
	}
	if len(valuesYAML) > AnnotateMaxBytes {
		res.SkipReason = "oversize"
		recordAnnotate(res.SkipReason, time.Since(start))
		slog.Info("ai annotate skipped: values.yaml exceeds token budget",
			"chart", chartName, "version", chartVersion,
			"bytes", len(valuesYAML), "max_bytes", AnnotateMaxBytes,
		)
		return res, nil
	}

	// HARD BLOCK: secret-leak guard. This runs BEFORE any network call.
	// The locked decision is no override — refuse to send to the LLM if
	// any pattern matches. Returns a typed error so the caller can render
	// the dedicated banner.
	if matches := ScanForSecrets(valuesYAML); len(matches) > 0 {
		res.SkipReason = "secret_blocked"
		recordAnnotate(res.SkipReason, time.Since(start))
		slog.Warn("ai annotate hard-blocked: secret-leak pattern matched",
			"chart", chartName, "version", chartVersion,
			"match_count", len(matches),
		)
		return res, &SecretLeakError{Matches: matches}
	}

	// Bound the call latency. The LLM gets 30s — anything slower and
	// we fall through to heuristic-only.
	callCtx, cancel := context.WithTimeout(ctx, AnnotateTimeout)
	defer cancel()

	prompt := buildAnnotatePrompt(chartName, chartVersion, valuesYAML)
	raw, err := aiClient.Summarize(callCtx, prompt)
	if err != nil {
		// Distinguish timeout from other errors for metrics — the
		// operator wants to know if their LLM is consistently slow vs.
		// returning errors.
		reason := "llm_error"
		if callCtx.Err() == context.DeadlineExceeded {
			reason = "timeout"
		}
		res.SkipReason = reason
		recordAnnotate(reason, time.Since(start))
		slog.Info("ai annotate fell through to heuristic-only",
			"chart", chartName, "version", chartVersion,
			"reason", reason, "error", err,
		)
		// Return nil error so the caller continues with heuristic-only.
		// The reason is captured in SkipReason for audit context.
		return res, nil
	}

	parsed, parseErr := parseAnnotateResponse(raw)
	if parseErr != nil {
		res.SkipReason = "parse_error"
		recordAnnotate(res.SkipReason, time.Since(start))
		slog.Info("ai annotate response parse failed; using heuristic only",
			"chart", chartName, "version", chartVersion, "error", parseErr,
		)
		return res, nil
	}

	annotated := injectAnnotations(valuesYAML, parsed.Descriptions)
	res.AnnotatedYAML = annotated
	res.AdditionalClusterPaths = parsed.ClusterSpecificPaths
	recordAnnotate("ok", time.Since(start))
	return res, nil
}

// buildAnnotatePrompt is the deterministic prompt sent to the LLM. We
// keep it short and prescriptive — the LLM has one job, return JSON in
// the documented shape. Free-form prose responses are caught by the
// parser and treated as "llm returned junk → heuristic-only".
//
// The prompt explicitly tells the LLM:
//   - Don't annotate trivial fields (defaults, basic CRDs, secret-related).
//   - Production hints welcome.
//   - Don't emit secrets back; don't quote the original values back at us.
//   - Output JSON only — no prose.
func buildAnnotatePrompt(chartName, chartVersion string, valuesYAML []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are annotating a Helm chart values.yaml for chart %q version %q.\n\n", chartName, chartVersion)
	b.WriteString("Tasks:\n")
	b.WriteString("  1. For each NON-TRIVIAL scalar field, write a one-line description (<= 90 chars) explaining what it does. Skip default values, basic CRD toggles, and secret-related fields. Production hints are welcome (e.g. \"For HA, set replicas to 2+\").\n")
	b.WriteString("  2. Identify fields that are typically VARIED PER CLUSTER (network endpoints, scale/sizing, scheduling, secrets, cluster identity) and return them as dotted paths.\n\n")
	b.WriteString("Constraints:\n")
	b.WriteString("  - Do NOT echo any secret-like values back. If a field looks like a credential, skip it.\n")
	b.WriteString("  - Do NOT modify the YAML structure or reorder keys — return descriptions only.\n")
	b.WriteString("  - Output JSON ONLY (no prose, no markdown). The schema is:\n")
	b.WriteString("      {\n")
	b.WriteString("        \"descriptions\": { \"<dotted.path>\": \"<one-line description>\", ... },\n")
	b.WriteString("        \"cluster_specific_paths\": [ \"<dotted.path>\", ... ]\n")
	b.WriteString("      }\n\n")
	b.WriteString("values.yaml:\n")
	b.WriteString("```yaml\n")
	b.Write(valuesYAML)
	if !strings.HasSuffix(string(valuesYAML), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// annotateResponse is the parsed shape of the LLM's JSON reply.
type annotateResponse struct {
	Descriptions         map[string]string `json:"descriptions"`
	ClusterSpecificPaths []string          `json:"cluster_specific_paths"`
}

// parseAnnotateResponse is forgiving about LLM noise. Some providers
// wrap JSON in ```json fences; we strip those. Any extra prose before/
// after the JSON object is also tolerated by scanning for the first `{`
// and the last `}`. Anything beyond that is a parse error and the
// caller falls through to heuristic-only.
func parseAnnotateResponse(raw string) (annotateResponse, error) {
	var out annotateResponse
	s := strings.TrimSpace(raw)

	// Strip ```json / ``` fences if present (Claude/Gemini sometimes
	// wrap the response even when told not to).
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// Locate the JSON object boundaries — robust to leading/trailing
	// prose. If we can't find a `{...}` window, treat as junk.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return out, fmt.Errorf("no JSON object found in response")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return out, fmt.Errorf("decoding annotate JSON: %w", err)
	}
	return out, nil
}

// injectAnnotations rewrites the values.yaml with one-line `# <desc>`
// comments inserted ABOVE every line whose dotted path is in the
// descriptions map. Existing comments and structure are preserved
// verbatim — we only add lines.
//
// The function is textual (no YAML round-trip) for the same reason the
// splitter is textual: yaml.Marshal strips comments and reorders keys.
// We reuse the smart-values frame stack so the dotted-path computation
// matches the splitter exactly — a description applied here for path
// `controller.replicaCount` will end up commenting the same line the
// splitter sees as cluster-specific.
//
// Skip rules:
//   - Lines with a `<key>:` already preceded immediately by a comment
//     that starts with the same description are NOT duplicated. This
//     keeps repeat annotate calls idempotent (e.g. when the user
//     re-runs annotate after editing values).
//   - Descriptions are sanitized: newlines collapsed to spaces, length
//     capped at 200 chars. We never inject a multi-line comment.
func injectAnnotations(valuesYAML []byte, descriptions map[string]string) []byte {
	if len(descriptions) == 0 {
		return valuesYAML
	}

	var b strings.Builder
	var stack []smartFrame
	lines := strings.Split(string(valuesYAML), "\n")

	for i, raw := range lines {
		line := raw
		trim := strings.TrimSpace(line)
		// Pass-through for blanks and comments. We do NOT pop the stack
		// on these — yaml indentation is the source of truth.
		if trim == "" || strings.HasPrefix(trim, "#") {
			b.WriteString(line)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}

		indent := leadingSpaces(line)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		// List children pass through (path computed for the splitter
		// stays the same; we don't annotate list items individually
		// because the per-cluster template carries the sub-tree below).
		if strings.HasPrefix(trim, "-") {
			b.WriteString(line)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}

		colonIdx := strings.Index(trim, ":")
		if colonIdx == -1 {
			// Continuation of a multi-line scalar — pass through.
			b.WriteString(line)
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}
		key := strings.TrimSpace(trim[:colonIdx])
		value := strings.TrimSpace(trim[colonIdx+1:])
		full := framePathWith(stack, key)

		// Look up a description for this exact dotted path. If found,
		// emit the `# <desc>` line BEFORE the field — preserving the
		// field's original indentation.
		if desc, ok := descriptions[full]; ok {
			cleanDesc := sanitizeDescription(desc)
			if cleanDesc != "" && !alreadyAnnotated(lines, i, cleanDesc) {
				pad := strings.Repeat(" ", indent)
				b.WriteString(pad)
				b.WriteString("# ")
				b.WriteString(cleanDesc)
				b.WriteString("\n")
			}
		}

		// Emit the original line verbatim, then push a frame if this is
		// a map parent.
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
		if value == "" || value == "{}" || value == "[]" {
			stack = append(stack, smartFrame{indent: indent, key: key})
		}
	}
	return []byte(b.String())
}

// sanitizeDescription collapses newlines, trims, and caps length so an
// adversarial LLM response can't smuggle multi-line content into the
// emitted YAML (which would change the file's structure).
func sanitizeDescription(d string) string {
	d = strings.ReplaceAll(d, "\r", " ")
	d = strings.ReplaceAll(d, "\n", " ")
	d = strings.TrimSpace(d)
	if len(d) > 200 {
		d = d[:197] + "..."
	}
	return d
}

// alreadyAnnotated returns true when the line immediately above index
// `i` is a comment carrying the same description we're about to inject.
// Keeps repeat annotate calls idempotent on the same file.
func alreadyAnnotated(lines []string, i int, desc string) bool {
	if i == 0 {
		return false
	}
	prev := strings.TrimSpace(lines[i-1])
	if !strings.HasPrefix(prev, "#") {
		return false
	}
	prev = strings.TrimSpace(strings.TrimPrefix(prev, "#"))
	return prev == desc
}

// recordAnnotate updates the Prometheus metrics for the AI annotate
// pass. `outcome` is one of: "ok", "not_configured", "empty_input",
// "oversize", "secret_blocked", "timeout", "llm_error", "parse_error",
// "opted_out". `latency` is the wall-clock time from AnnotateValues
// entry to return.
func recordAnnotate(outcome string, latency time.Duration) {
	metrics.AIAnnotateTotal.WithLabelValues(outcome).Inc()
	metrics.AIAnnotateLatencySeconds.WithLabelValues(outcome).Observe(latency.Seconds())
}
