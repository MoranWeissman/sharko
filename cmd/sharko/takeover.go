package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MoranWeissman/sharko/internal/fanout"
)

// takeoverOperation names what `sharko takeover` attempted, for the headline,
// the review warning and the exit message.
const takeoverOperation = "The takeover"

func init() {
	takeoverCmd.Flags().Bool("dry-run", false, "Preview what would happen without making changes")
	takeoverCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	takeoverCmd.Flags().StringArray("acknowledge", nil, "Finding id to acknowledge (repeatable)")
	takeoverCmd.Flags().Bool("preserve-legacy-labels", true, "Carry the previous owner's labels over to Sharko's connection (default true — only sent when you set this flag)")
	takeoverCmd.Flags().String("region", "", "Region to record on the fleet entry")
	takeoverCmd.Flags().Bool("auto-merge", false, "Auto-merge the takeover PR (overrides the server default; only sent when you set this flag)")
	rootCmd.AddCommand(takeoverCmd)

	rootCmd.AddCommand(takeoverPreflightCmd)

	dropLegacyLabelsCmd.Flags().StringArray("label", nil, "Label key to remove (repeatable; empty means every carried-over label)")
	dropLegacyLabelsCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	dropLegacyLabelsCmd.Flags().Bool("dry-run", false, "Preview what would be removed without making changes")
	dropLegacyLabelsCmd.Flags().StringArray("acknowledge", nil, "Warning id to acknowledge (repeatable)")
	rootCmd.AddCommand(dropLegacyLabelsCmd)

	rootCmd.AddCommand(unregisterConsequencesCmd)
}

// ─────────────────────────────────────────────────────────────────────────
// Shared preflight report types + printing (takeover.Report / Finding)
// ─────────────────────────────────────────────────────────────────────────

type preflightFinding struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Status          string   `json:"status"` // "ok", "warning", "blocked"
	Detail          string   `json:"detail"`
	WhatItMeans     string   `json:"what_it_means"`
	WhatToDo        string   `json:"what_to_do"`
	ApplicationSets []string `json:"application_sets,omitempty"`
	Applications    []string `json:"applications,omitempty"`
}

type preflightReport struct {
	Cluster              string             `json:"cluster"`
	Ready                bool               `json:"ready"`
	NeedsAcknowledgement bool               `json:"needs_acknowledgement"`
	Summary              string             `json:"summary"`
	Findings             []preflightFinding `json:"findings"`
	Server               string             `json:"server,omitempty"`
	LegacyLabels         map[string]string  `json:"legacy_labels,omitempty"`
}

func findingGlyph(status string) string {
	switch status {
	case "ok":
		return "✓"
	case "warning":
		return "⚠"
	case "blocked":
		return "✗"
	default:
		return "?"
	}
}

func printPreflightReport(r *preflightReport) {
	if r == nil {
		return
	}
	readiness := "NOT READY"
	if r.Ready {
		readiness = "ready"
	}
	fmt.Printf("Preflight for %s: %s\n", r.Cluster, readiness)
	if r.Summary != "" {
		fmt.Printf("  %s\n", r.Summary)
	}
	for _, f := range r.Findings {
		fmt.Printf("  %s [%s] %s\n", findingGlyph(f.Status), f.ID, f.Title)
		if f.Detail != "" {
			fmt.Printf("      %s\n", f.Detail)
		}
		if f.WhatItMeans != "" {
			fmt.Printf("      Means: %s\n", f.WhatItMeans)
		}
		if f.WhatToDo != "" && f.Status != "ok" {
			fmt.Printf("      Do:    %s\n", f.WhatToDo)
		}
	}
	if len(r.LegacyLabels) > 0 {
		var keys []string
		for k := range r.LegacyLabels {
			keys = append(keys, k)
		}
		fmt.Printf("  Legacy labels carried over by default: %s\n", strings.Join(keys, ", "))
	}
}

// ─────────────────────────────────────────────────────────────────────────
// takeover-preflight <cluster> — read-only
// ─────────────────────────────────────────────────────────────────────────

var takeoverPreflightCmd = &cobra.Command{
	Use:   "takeover-preflight <cluster>",
	Short: "Check whether a cluster can be taken over",
	Long: `Runs the read-only checks Sharko does before a takeover: who owns the
cluster's ArgoCD connection today, which ApplicationSets would react to it
changing owners, what is deployed there, and whether the name clashes with a
cluster Sharko already has. Writes nothing — safe to run as often as you
like.

Exits 0 when the cluster is ready to take over (warnings included — read
them), and 1 when something blocks it, so a script or CI gate can branch on
the result.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		respBody, status, err := apiGet("/api/v1/clusters/" + url.PathEscape(name) + "/takeover/preflight")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}
		var report preflightReport
		if err := json.Unmarshal(respBody, &report); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		printPreflightReport(&report)
		if !report.Ready {
			// A blocked report exits non-zero so scripts don't have to parse
			// the text. The findings above already say what to fix.
			return fmt.Errorf("%s is not ready to take over — fix the blocked check(s) above and run this again", name)
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────
// takeover <cluster>
// ─────────────────────────────────────────────────────────────────────────

type takeoverGitResult struct {
	PRUrl  string `json:"pr_url,omitempty"`
	Branch string `json:"branch,omitempty"`
	Merged bool   `json:"merged"`
}

type takeoverResponse struct {
	Cluster            string             `json:"cluster"`
	Status             string             `json:"status"`
	Server             string             `json:"server,omitempty"`
	PreservedLabels    map[string]string  `json:"preserved_labels,omitempty"`
	DroppedLabels      map[string]string  `json:"dropped_labels,omitempty"`
	SecretSwapped      bool               `json:"secret_swapped"`
	AlreadyOwned       bool               `json:"already_owned,omitempty"`
	ProtectionRepaired bool               `json:"protection_repaired,omitempty"`
	Git                *takeoverGitResult `json:"git,omitempty"`
	DryRun             *cliDryRunResult   `json:"dry_run,omitempty"`
	Preflight          *preflightReport   `json:"preflight,omitempty"`
	Warnings           []string           `json:"warnings,omitempty"`
	Message            string             `json:"message"`
}

// takeover409Body is the shape of the two different 409s the takeover call
// can return: "not ready yet" (blocked findings — UnacknowledgedFindings is
// empty) and "warnings not acknowledged" (UnacknowledgedFindings names the
// ones still missing from --acknowledge).
type takeover409Body struct {
	Error                  string           `json:"error"`
	UnacknowledgedFindings []string         `json:"unacknowledged_findings,omitempty"`
	Preflight              *preflightReport `json:"preflight,omitempty"`
}

var takeoverCmd = &cobra.Command{
	Use:   "takeover <cluster>",
	Short: "Take over a cluster ArgoCD already manages",
	Long: `Makes Sharko the owner of an existing cluster's ArgoCD connection, keeping
the same name, the same API address and — by default — every label the
previous owner left on it. Adds the cluster to Sharko's fleet through a pull
request and creates an empty addon file for it; no addon is turned on.

Always fetches and prints the preflight report first. Any warning finding
must be named with --acknowledge before the takeover is allowed to run —
run with --dry-run first to see the exact ids to pass.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		acks, _ := cmd.Flags().GetStringArray("acknowledge")
		region, _ := cmd.Flags().GetString("region")

		// The preflight is always fetched and shown first — it is the read
		// that makes the takeover's warnings legible before anything is
		// sent for confirmation.
		preRespBody, preStatus, err := apiGet("/api/v1/clusters/" + url.PathEscape(name) + "/takeover/preflight")
		if err != nil {
			return err
		}
		if preStatus != 200 {
			return printAPIError(preRespBody, preStatus)
		}
		var report preflightReport
		if err := json.Unmarshal(preRespBody, &report); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		printPreflightReport(&report)
		fmt.Println()

		if !dryRun && !yes {
			fmt.Printf("Take over cluster %q? Sharko becomes the owner of its existing ArgoCD connection. [y/N]: ", name)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
			yes = true
		}

		body := map[string]interface{}{}
		if yes {
			body["yes"] = true
		}
		if dryRun {
			body["dry_run"] = true
		}
		if len(acks) > 0 {
			body["acknowledged_findings"] = acks
		}
		if cmd.Flags().Changed("preserve-legacy-labels") {
			v, _ := cmd.Flags().GetBool("preserve-legacy-labels")
			body["preserve_legacy_labels"] = v
		}
		if region != "" {
			body["region"] = region
		}
		if cmd.Flags().Changed("auto-merge") {
			v, _ := cmd.Flags().GetBool("auto-merge")
			body["auto_merge"] = v
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing takeover of %s...\n", name)
		} else {
			fmt.Printf("Taking over cluster %s... ", name)
		}

		respBody, status, err := apiPost("/api/v1/clusters/"+url.PathEscape(name)+"/takeover", body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}

		if status == 409 {
			if !dryRun {
				fmt.Println("failed")
			}
			return handleTakeover409(respBody, name, acks, dryRun, region, cmd)
		}

		if status != 200 && status != 207 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		unwrapped, note := unwrapAttribution(respBody)

		var result takeoverResponse
		if err := json.Unmarshal(unwrapped, &result); err != nil {
			if !dryRun {
				fmt.Println("no readable answer")
			}
			return fmt.Errorf("invalid response: %w", err)
		}

		// The same one decision the fan-out commands take, on a fan-out of
		// one. "done" used to be printed the moment the HTTP call came back,
		// above a line that then said the takeover had only got part of the
		// way.
		outcome := fanout.SingleStatus(result.Status, status == 207)
		if !dryRun {
			fmt.Println(outcome.ProgressWord())
		}

		fmt.Println()
		if dryRun {
			printDryRun(result.DryRun)
		} else {
			if outcome.Completed() {
				fmt.Printf("Sharko now owns %s.\n", name)
			} else {
				fmt.Print(outcome.TroubleHeadline(takeoverOperation, name))
				fmt.Print(outcome.ReviewWarning())
			}
			if result.Server != "" {
				fmt.Printf("  Server: %s\n", result.Server)
			}
			if len(result.PreservedLabels) > 0 {
				fmt.Printf("  Preserved labels: %s\n", strings.Join(sortedKeys(result.PreservedLabels), ", "))
			}
			if len(result.DroppedLabels) > 0 {
				fmt.Printf("  Dropped labels:   %s\n", strings.Join(sortedKeys(result.DroppedLabels), ", "))
			}
			if result.Git != nil && result.Git.PRUrl != "" {
				if result.Git.Merged {
					fmt.Printf("  PR: %s (merged)\n", result.Git.PRUrl)
				} else {
					fmt.Printf("  PR: %s (open — merge manually)\n", result.Git.PRUrl)
				}
			}
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}
		if note != "" {
			fmt.Printf("  Note: %s\n", note)
		}
		// A preview takes over nothing, so it has no completion to confirm.
		if dryRun {
			return nil
		}
		// Used to be a bare `return nil`, so a takeover that stopped
		// half-way — Sharko's ownership labels already on the cluster, the
		// pull request still open — exited 0.
		return outcome.ExitError(takeoverOperation, name)
	},
}

// handleTakeover409 prints the takeover 409 body (either "not ready" or
// "warnings not acknowledged") and, for the second case, the exact rerun
// command including every --acknowledge flag still needed.
func handleTakeover409(body []byte, name string, alreadyAcked []string, dryRun bool, region string, cmd *cobra.Command) error {
	var b takeover409Body
	if err := json.Unmarshal(body, &b); err != nil {
		return printAPIError(body, 409)
	}
	if b.Preflight != nil {
		printPreflightReport(b.Preflight)
	}
	fmt.Println()
	if len(b.UnacknowledgedFindings) == 0 {
		// Blocked — nothing to acknowledge, something has to be fixed first.
		return fmt.Errorf("%s", b.Error)
	}

	// Combine what was already acknowledged with what is still missing so
	// the printed command is the complete, correct rerun — not just the
	// delta.
	combined := append([]string{}, alreadyAcked...)
	for _, id := range b.UnacknowledgedFindings {
		if !containsStr(combined, id) {
			combined = append(combined, id)
		}
	}

	fmt.Println(b.Error)
	fmt.Println()
	rerun := []string{"sharko", "takeover", name, "-y"}
	for _, id := range combined {
		rerun = append(rerun, "--acknowledge", id)
	}
	if dryRun {
		rerun = append(rerun, "--dry-run")
	}
	if region != "" {
		rerun = append(rerun, "--region", region)
	}
	if cmd.Flags().Changed("auto-merge") {
		v, _ := cmd.Flags().GetBool("auto-merge")
		rerun = append(rerun, "--auto-merge="+fmt.Sprint(v))
	}
	fmt.Printf("Rerun with: %s\n", strings.Join(rerun, " "))
	return fmt.Errorf("%s", b.Error)
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// ─────────────────────────────────────────────────────────────────────────
// drop-legacy-labels <cluster>
// ─────────────────────────────────────────────────────────────────────────

type dropLegacyLabelsResponse struct {
	Cluster    string   `json:"cluster"`
	Status     string   `json:"status"`
	Removed    []string `json:"removed,omitempty"`
	Remaining  []string `json:"remaining,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	WarningIDs []string `json:"warning_ids,omitempty"`
	Message    string   `json:"message"`
}

type drop409Body struct {
	Error                  string   `json:"error"`
	UnacknowledgedFindings []string `json:"unacknowledged_findings,omitempty"`
	Warnings               []string `json:"warnings,omitempty"`
	WarningIDs             []string `json:"warning_ids,omitempty"`
	Labels                 []string `json:"labels,omitempty"`
}

var dropLegacyLabelsCmd = &cobra.Command{
	Use:   "drop-legacy-labels <cluster>",
	Short: "Remove the labels a takeover carried over",
	Long: `Takes the previous owner's labels off a cluster's connection. Warns first,
by name, about any ApplicationSet that still picks clusters using one of
those labels — removing it is what makes this cluster fall out of that
ApplicationSet. Every warning has to be named with --acknowledge before the
removal runs; run with --dry-run first to see the exact ids.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		labels, _ := cmd.Flags().GetStringArray("label")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		acks, _ := cmd.Flags().GetStringArray("acknowledge")

		if !dryRun && !yes {
			fmt.Printf("Remove the legacy labels from %q's connection? [y/N]: ", name)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
			yes = true
		}

		body := map[string]interface{}{}
		if yes {
			body["yes"] = true
		}
		if dryRun {
			body["dry_run"] = true
		}
		if len(labels) > 0 {
			body["labels"] = labels
		}
		if len(acks) > 0 {
			body["acknowledged_findings"] = acks
		}

		if dryRun {
			fmt.Printf("Dry-run: previewing label removal for %s...\n", name)
		} else {
			fmt.Printf("Removing legacy labels from %s... ", name)
		}

		respBody, status, err := apiPost("/api/v1/clusters/"+url.PathEscape(name)+"/takeover/legacy-labels/drop", body)
		if err != nil {
			if !dryRun {
				fmt.Println("failed")
			}
			return err
		}

		if status == 409 {
			if !dryRun {
				fmt.Println("failed")
			}
			return handleDropLegacyLabels409(respBody, name, acks, dryRun)
		}

		if status != 200 {
			if !dryRun {
				fmt.Println("failed")
			}
			return printAPIError(respBody, status)
		}

		if !dryRun {
			fmt.Println("done")
		}

		var result dropLegacyLabelsResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println()
		for _, w := range result.Warnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
		switch result.Status {
		case "planned":
			fmt.Println("Dry-run preview (no changes made):")
			if len(result.Removed) > 0 {
				fmt.Printf("  Would remove: %s\n", strings.Join(result.Removed, ", "))
			}
		default:
			if len(result.Removed) > 0 {
				fmt.Printf("  Removed:   %s\n", strings.Join(result.Removed, ", "))
			}
			if len(result.Remaining) > 0 {
				fmt.Printf("  Remaining: %s\n", strings.Join(result.Remaining, ", "))
			}
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}
		return nil
	},
}

func handleDropLegacyLabels409(body []byte, name string, alreadyAcked []string, dryRun bool) error {
	var b drop409Body
	if err := json.Unmarshal(body, &b); err != nil {
		return printAPIError(body, 409)
	}
	fmt.Println()
	for i, w := range b.Warnings {
		id := "?"
		if i < len(b.WarningIDs) {
			id = b.WarningIDs[i]
		}
		fmt.Printf("  ⚠ [%s] %s\n", id, w)
	}
	fmt.Println()
	fmt.Println(b.Error)

	combined := append([]string{}, alreadyAcked...)
	for _, id := range b.UnacknowledgedFindings {
		if !containsStr(combined, id) {
			combined = append(combined, id)
		}
	}
	fmt.Println()
	rerun := []string{"sharko", "drop-legacy-labels", name, "-y"}
	for _, l := range b.Labels {
		rerun = append(rerun, "--label", l)
	}
	for _, id := range combined {
		rerun = append(rerun, "--acknowledge", id)
	}
	if dryRun {
		rerun = append(rerun, "--dry-run")
	}
	fmt.Printf("Rerun with: %s\n", strings.Join(rerun, " "))
	return fmt.Errorf("%s", b.Error)
}

// ─────────────────────────────────────────────────────────────────────────
// unregister-consequences <cluster> — read-only
// ─────────────────────────────────────────────────────────────────────────

type unregisterConsequence struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	WhatItMeans string `json:"what_it_means"`
	Severity    string `json:"severity"`
}

type unregisterConsequencesResponse struct {
	Cluster              string                  `json:"cluster"`
	Summary              string                  `json:"summary"`
	ConfirmationRequired string                  `json:"confirmation_required"`
	Consequences         []unregisterConsequence `json:"consequences"`
}

var unregisterConsequencesCmd = &cobra.Command{
	Use:   "unregister-consequences <cluster>",
	Short: "List what happens if you unregister a cluster",
	Long: `Reads out, one by one, what unregistering this cluster will do — what
leaves the repo, what happens to its ArgoCD connection, what is deployed
there today, and which ApplicationSets may react to labels a takeover
carried over. Writes nothing and deletes nothing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		respBody, status, err := apiGet("/api/v1/clusters/" + url.PathEscape(name) + "/unregister/consequences")
		if err != nil {
			return err
		}
		if status != 200 {
			return printAPIError(respBody, status)
		}
		var result unregisterConsequencesResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Printf("Unregistering %s:\n", name)
		fmt.Printf("  %s\n", result.Summary)
		fmt.Println()
		for _, c := range result.Consequences {
			glyph := "-"
			if c.Severity == "warning" {
				glyph = "⚠"
			}
			fmt.Printf("  %s %s\n", glyph, c.Title)
			if c.Detail != "" {
				fmt.Printf("      %s\n", c.Detail)
			}
			if c.WhatItMeans != "" {
				fmt.Printf("      Means: %s\n", c.WhatItMeans)
			}
		}
		fmt.Println()
		fmt.Println(result.ConfirmationRequired)
		return nil
	},
}
