package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/fanout"
	"github.com/spf13/cobra"
)

func init() {
	addClustersCmd.Flags().String("addons", "", "Comma-separated list of addons to enable")
	addClustersCmd.Flags().String("region", "", "Region for all clusters in the batch")
	addClustersCmd.Flags().Bool("from-provider", false, "Discover and register unregistered provider clusters")
	addClustersCmd.Flags().Bool("yes", false, "Do not ask before registering")
	addClustersCmd.Flags().Bool("dry-run", false, "Show what would happen and change nothing")
	rootCmd.AddCommand(addClustersCmd)
}

// batchRequest is the JSON body for POST /api/v1/clusters/batch.
type batchRequest struct {
	Clusters []batchClusterEntry `json:"clusters"`
}

type batchClusterEntry struct {
	Name   string          `json:"name"`
	Addons map[string]bool `json:"addons"`
	Region string          `json:"region"`
	// DryRun asks the server to check this cluster and change nothing. The
	// server already reads it for each entry of a batch.
	DryRun bool `json:"dry_run,omitempty"`
}

// batchResponse is the JSON response from POST /api/v1/clusters/batch.
type batchResponse struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	// Failed counts every cluster that did not fully succeed — hard
	// failures AND partials. That is what it has always meant on the wire
	// and it is deliberately unchanged, which is exactly why printing it as
	// "failed" on its own is a lie: a batch where both clusters merged a
	// pull request and then stopped halfway printed "0 succeeded, 2 failed".
	Failed int `json:"failed"`
	// Partial is how many of Failed were partials — clusters that changed
	// something real before stopping. A subset of Failed, not a third
	// bucket. Added by the server in R2-7; an older server omits it and it
	// reads as 0, which prints the exact line this CLI always printed.
	Partial int `json:"partial"`
	// Outcome is the accurate trio the server added in R2-9: how many
	// clusters fully completed, how many stopped part-way, and how many
	// failed outright. An older server omits it, and every count reads 0 —
	// which is why this CLI recounts from results[].status rather than
	// trusting it. Kept on the struct so the field is visible to anyone
	// reading what the wire carries.
	Outcome fanout.Outcome `json:"outcome"`
	Results []batchResult  `json:"results"`
}

type batchResult struct {
	Status  string `json:"status"`
	Cluster struct {
		Name   string `json:"name"`
		Server string `json:"server"`
	} `json:"cluster"`
	Git *struct {
		PRUrl  string `json:"pr_url"`
		Branch string `json:"branch"`
	} `json:"git"`
	Error   string `json:"error"`
	Message string `json:"message"`
	// CompletedSteps says how far the server got. "fetch_credentials" means
	// it found a login for the cluster, "skip_credentials" means it found
	// none and carried on without one.
	CompletedSteps []string         `json:"completed_steps"`
	DryRun         *cliDryRunResult `json:"dry_run"`
}

// batchStdin is where the "are you sure" answer is read from. A variable so a
// test can feed it.
var batchStdin io.Reader = os.Stdin

// maxBatchSize is how many clusters one call to the server may carry.
const maxBatchSize = 10

// loginState says whether the server found a login (a stored secret) for a
// cluster.
type loginState int

const (
	loginUnknown loginState = iota
	loginFound
	loginMissing
)

// loginStateOf reads it from the steps the server reports.
func loginStateOf(r batchResult) loginState {
	state := loginUnknown
	for _, step := range r.CompletedSteps {
		switch step {
		case "fetch_credentials":
			state = loginFound
		case "skip_credentials":
			return loginMissing
		}
	}
	return state
}

var addClustersCmd = &cobra.Command{
	Use:   "add-clusters [cluster1,cluster2,...]",
	Short: "Register multiple clusters in a batch",
	Long: `Registers several clusters at once. Give the names as one comma-separated
list, or use --from-provider to find the clusters your cloud account has that
Sharko does not know yet and register them.

First it checks every name with the server, changing nothing, and shows you the
list. If a cluster has no login (secret) stored for it, it tells you by name:
that cluster would be registered with no connection. Then it asks before it
changes anything.

  --yes       do not ask, go ahead
  --dry-run   show what would happen and change nothing

A batch of 10 clusters can take a minute or two. If this command stops waiting,
the server may still be working. Do not run it again straight away: look at
"sharko pr list" or the Clusters page first.

Examples:
  sharko add-clusters prod-eu,prod-us --addons monitoring
  sharko add-clusters prod-eu,prod-us --dry-run
  sharko add-clusters --from-provider`,
	// The list of names is only needed when --from-provider is not used.
	// The old form (--from-provider plus a list that is ignored) still works.
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromProvider, _ := cmd.Flags().GetBool("from-provider")
		if !fromProvider && len(args) != 1 {
			return fmt.Errorf("give the cluster names as a comma-separated list (for example: sharko add-clusters prod-eu,prod-us), or use --from-provider")
		}
		addonsFlag, _ := cmd.Flags().GetString("addons")
		region, _ := cmd.Flags().GetString("region")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		addons := make(map[string]bool)
		if addonsFlag != "" {
			for _, a := range strings.Split(addonsFlag, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					addons[a] = true
				}
			}
		}

		var names []string

		if fromProvider {
			// Discover unregistered clusters from the provider.
			// /clusters/available now lists what ArgoCD knows instead;
			// the credentials-backend list moved to /providers/clusters.
			respBody, status, err := apiGet("/api/v1/providers/clusters")
			if err != nil {
				return err
			}
			if status != 200 {
				return printAPIError(respBody, status)
			}

			var discoverResp struct {
				Clusters []struct {
					Name       string `json:"name"`
					Region     string `json:"region"`
					Registered bool   `json:"registered"`
				} `json:"clusters"`
			}
			if err := json.Unmarshal(respBody, &discoverResp); err != nil {
				return fmt.Errorf("invalid discover response: %w", err)
			}

			for _, c := range discoverResp.Clusters {
				if !c.Registered {
					names = append(names, c.Name)
				}
			}

			if len(names) == 0 {
				fmt.Println("All provider clusters are already registered.")
				return nil
			}
		} else {
			// Parse comma-separated cluster names from argument.
			for _, n := range strings.Split(args[0], ",") {
				n = strings.TrimSpace(n)
				if n != "" {
					names = append(names, n)
				}
			}
			if len(names) == 0 {
				return fmt.Errorf("at least one cluster name is required")
			}
		}

		// Step 1: ask the server to check every name and change nothing.
		// This is what lets us name, before anything is changed, the
		// clusters that have no login behind them.
		fmt.Printf("Checking %d cluster(s) with the server (nothing is changed)...\n", len(names))
		preview, previewErr := runBatch(names, addons, region, true)
		if previewErr != nil {
			// The real call would hit the same problem (a wrong address, a
			// refused login, a server that is down), so stop here rather
			// than ask a question we cannot back up.
			var timedOut *requestTimedOutError
			if errors.As(previewErr, &timedOut) {
				return fmt.Errorf("the check did not finish in time (%s), so nothing was registered. Try again, or look at the Clusters page", previewErr)
			}
			return previewErr
		}
		printBatchPlan(names, preview)

		if dryRun {
			fmt.Println()
			fmt.Println("This was a dry run. Nothing was changed.")
			if n := countWouldFail(preview); n > 0 {
				return fmt.Errorf("the check found %d cluster(s) that would not be registered", n)
			}
			return nil
		}

		// Step 2: ask, unless --yes.
		if !yes {
			fmt.Printf("\nRegister %d cluster(s)? [y/N]: ", len(names))
			answer, _ := bufio.NewReader(batchStdin).ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted. Nothing was changed. (Use --yes to skip this question.)")
				return nil
			}
		}
		fmt.Println()

		// Step 3: do it.
		allResults, runErr := runBatch(names, addons, region, false)
		if runErr != nil {
			// Whatever finished before the problem is still true: say it.
			printBatchResults(allResults)
			var timedOut *requestTimedOutError
			if errors.As(runErr, &timedOut) {
				fmt.Println()
				fmt.Printf("Stopped waiting for the server after %s.\n", batchRequestTimeout)
				fmt.Println("The server may still be working on the clusters, so some may be registered by now.")
				fmt.Println("Do not run this again yet. To see what happened, run 'sharko pr list' or open the Clusters page.")
				return fmt.Errorf("stopped waiting for the server; it may still be working (check 'sharko pr list' or the Clusters page)")
			}
			return runErr
		}

		// One count, made from the per-cluster answers, drives the summary
		// line, the review warning and the exit code — so they cannot say
		// three different things.
		outcome := outcomeOf(allResults)

		// Print summary.
		fmt.Println()
		fmt.Print(outcome.SummaryLine(batchOperation))
		fmt.Print(outcome.ReviewWarning())
		fmt.Println()

		printBatchResults(allResults)

		// The command used to return nil here no matter what came back, so
		// `sharko add-clusters` exited 0 for a batch in which every single
		// cluster failed. A script checking the exit code had no way to
		// notice. Exit 0 now means what it says: every cluster completed.
		return outcome.ExitError(batchOperation)
	},
}

// runBatch sends the names to the server in pages of 10 and gathers the
// per-cluster answers. With preview set, every entry asks for a dry run, so
// nothing is changed. On an error it returns the answers gathered so far
// together with the error.
func runBatch(names []string, addons map[string]bool, region string, preview bool) ([]batchResult, error) {
	var all []batchResult
	for i := 0; i < len(names); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(names) {
			end = len(names)
		}
		chunk := names[i:end]

		entries := make([]batchClusterEntry, len(chunk))
		for j, name := range chunk {
			entries[j] = batchClusterEntry{Name: name, Addons: addons, Region: region, DryRun: preview}
		}

		if !preview {
			batchNum := (i / maxBatchSize) + 1
			if len(names) > maxBatchSize {
				fmt.Printf("Processing batch %d (%d clusters)... ", batchNum, len(chunk))
			} else {
				fmt.Printf("Registering %d cluster(s)... ", len(chunk))
			}
		}

		respBody, status, err := apiRequestWithTimeout(http.MethodPost, "/api/v1/clusters/batch",
			batchRequest{Clusters: entries}, batchRequestTimeout)
		if err != nil {
			if !preview {
				var timedOut *requestTimedOutError
				if errors.As(err, &timedOut) {
					fmt.Println("no answer yet")
				} else {
					fmt.Println("no answer")
				}
			}
			return all, err
		}
		if status != 200 && status != 207 {
			if !preview {
				fmt.Println("refused")
			}
			return all, printAPIError(respBody, status)
		}

		var resp batchResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			if !preview {
				fmt.Println("no readable answer")
			}
			return all, fmt.Errorf("invalid response: %w", err)
		}

		if preview {
			// A server that does not understand "dry run" would have
			// registered the clusters for real. A success with no preview
			// in it is the sign of that, so stop and say so.
			for _, r := range resp.Results {
				if r.Status == "success" && r.DryRun == nil {
					return all, fmt.Errorf("the server did not do a dry run for %s, so it may have registered clusters. Check 'sharko pr list' or the Clusters page", r.Cluster.Name)
				}
			}
		}

		if !preview {
			// "done" is only true when this page of clusters really did all
			// finish.
			if outcomeOf(resp.Results).EverythingCompleted() {
				fmt.Println("done")
			} else {
				fmt.Println("not all clusters finished")
			}
		}
		all = append(all, resp.Results...)
	}
	return all, nil
}

// printBatchPlan shows the list of names and what the check found for each,
// with the clusters that have no login named.
func printBatchPlan(names []string, preview []batchResult) {
	byName := make(map[string]batchResult, len(preview))
	for _, r := range preview {
		byName[r.Cluster.Name] = r
	}

	fmt.Println()
	fmt.Printf("Clusters (%d):\n", len(names))
	var noLogin, wouldFail []string
	for _, n := range names {
		r, ok := byName[n]
		if !ok {
			fmt.Printf("  - %s\n", n)
			continue
		}
		switch {
		case r.Status == "failed" || r.Status == "partial":
			why := r.Error
			if why == "" {
				why = r.Message
			}
			fmt.Printf("  - %s  -- would NOT be registered: %s\n", n, why)
			wouldFail = append(wouldFail, n)
			continue
		case loginStateOf(r) == loginMissing:
			fmt.Printf("  - %s  -- no login found\n", n)
			noLogin = append(noLogin, n)
		case loginStateOf(r) == loginFound:
			fmt.Printf("  - %s  -- login found\n", n)
		default:
			fmt.Printf("  - %s  -- could not tell if a login is stored\n", n)
		}
		if r.DryRun != nil {
			if r.DryRun.PRTitle != "" {
				fmt.Printf("      PR: %s\n", r.DryRun.PRTitle)
			}
			for _, f := range r.DryRun.FilesToWrite {
				fmt.Printf("      [%s] %s\n", f.Action, f.Path)
			}
		}
	}
	if len(noLogin) > 0 {
		fmt.Println()
		fmt.Printf("WARNING: no login (secret) was found for: %s\n", strings.Join(noLogin, ", "))
		fmt.Println("These would be registered with no connection. Sharko cannot reach them until a login is stored for each.")
	}
}

// countWouldFail counts the clusters the check says would not be registered.
func countWouldFail(preview []batchResult) int {
	n := 0
	for _, r := range preview {
		if r.Status == "failed" || r.Status == "partial" {
			n++
		}
	}
	return n
}

// printBatchResults prints one line per cluster, saying what really
// happened to it.
func printBatchResults(results []batchResult) {
	for _, r := range results {
		switch r.Status {
		case "success":
			line := fmt.Sprintf("  [ok] %s", r.Cluster.Name)
			if r.Cluster.Server != "" {
				line += fmt.Sprintf(" (%s)", r.Cluster.Server)
			}
			if r.Git != nil && r.Git.PRUrl != "" {
				line += fmt.Sprintf(" — PR: %s", r.Git.PRUrl)
			}
			if loginStateOf(r) == loginMissing {
				line += " — registered with no connection (no login was found)"
			}
			fmt.Println(line)
		case "partial":
			line := fmt.Sprintf("  [!!] %s — partial: %s", r.Cluster.Name, r.Message)
			if r.Git != nil && r.Git.PRUrl != "" {
				line += fmt.Sprintf(" — PR: %s", r.Git.PRUrl)
			}
			fmt.Println(line)
		case "failed":
			fmt.Printf("  [XX] %s — %s\n", r.Cluster.Name, r.Error)
		default:
			// A status this build does not know is shown, never skipped.
			fmt.Printf("  [??] %s — the server said %q, which this version does not know\n", r.Cluster.Name, r.Status)
		}
	}
}

// batchOperation names what `sharko add-clusters` attempted, for the summary
// line and the exit message.
const batchOperation = "Cluster registration"

// outcomeOf recounts the accurate trio from the per-cluster answers the
// server sent, using the same function the server counts with. Recounting
// rather than reading the server's `outcome` object means the summary and the
// exit code are right against an older server too, which does not send it.
func outcomeOf(results []batchResult) fanout.Outcome {
	statuses := make([]string, 0, len(results))
	for _, r := range results {
		statuses = append(statuses, r.Status)
	}
	return fanout.Count(statuses)
}
