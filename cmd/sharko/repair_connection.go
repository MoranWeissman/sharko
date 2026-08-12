package main

// repair_connection.go — R3-3, the CLI door onto the repair.
//
// It goes through the PUBLIC endpoint, exactly like the web UI will. It does not
// talk to Kubernetes, it does not read a secrets backend, and it does not build a
// connection Secret. The three doors — API, CLI, web UI — all open onto the same
// room, so a rule enforced in the handler cannot be walked around by picking a
// different door.
//
// It also cannot skip the review step. The repair endpoint requires the commit
// the caller reviewed, so this command runs the read-only check FIRST, shows what
// it found, and sends that check's own commit back with the repair. A person
// repairing from the CLI is therefore repairing something they were just shown,
// the same as somebody clicking a button next to a diff.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	repairConnectionCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	repairConnectionCmd.Flags().Bool("check-only", false, "Run the read-only check and stop; change nothing")
	rootCmd.AddCommand(repairConnectionCmd)
}

// connectionCheckResult is the read-only comparison's response, as much of it as
// this command reads.
//
// A sensitive field carries no expected value and no live value — the server does
// not send them, so there is nothing here to hold them. The fields below are
// paths, statuses and fixed sentences.
type connectionCheckResult struct {
	Cluster        string `json:"cluster"`
	Status         string `json:"status"`
	Scope          string `json:"scope"`
	OwnershipMode  string `json:"ownership_mode"`
	LimitReason    string `json:"limit_reason"`
	FailureReason  string `json:"failure_reason"`
	Branch         string `json:"branch"`
	ComparedCommit string `json:"compared_commit"`
	Differences    []struct {
		Path      string `json:"path"`
		Status    string `json:"status"`
		Sensitive bool   `json:"sensitive"`
	} `json:"differences"`
	NotChecked []struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	} `json:"not_checked"`
	RepairAvailable bool   `json:"repair_available"`
	RepairScope     string `json:"repair_scope"`
}

// connectionRepairResult is the repair's response.
type connectionRepairResult struct {
	Cluster                  string                `json:"cluster"`
	Repaired                 bool                  `json:"repaired"`
	ScopeApplied             string                `json:"scope_applied"`
	FieldsRepaired           []string              `json:"fields_repaired"`
	PreservedForeignLabels   int                   `json:"preserved_foreign_labels"`
	PreservedForeignDataKeys int                   `json:"preserved_foreign_data_keys"`
	Branch                   string                `json:"branch"`
	RepairedAtCommit         string                `json:"repaired_at_commit"`
	Message                  string                `json:"message"`
	Comparison               connectionCheckResult `json:"comparison"`
}

var repairConnectionCmd = &cobra.Command{
	Use:   "repair-connection <cluster>",
	Short: "Make a cluster's ArgoCD connection match what Sharko intends",
	Long: `Checks a cluster's ArgoCD connection against what Sharko means it to be, shows
you what does not match, and then puts it right.

What Sharko is allowed to change depends on the connection. Where Sharko owns
the connection and can read the cluster's sign-in details from your secrets
backend, it rewrites the whole connection. Where Sharko is only a guest — a
connection you maintain yourself, or one Sharko adopted — it re-applies the
addon labels and never touches your connection details. A connection another
tool owns is left completely alone.

Anything on the connection that is not Sharko's survives: other tools' labels,
other annotations, connection settings Sharko does not model, and labels carried
over by a takeover.

The change is made in place. Sharko never deletes and recreates a connection,
never writes to git, and never changes the self-heal setting. Sign-in details
are read fresh from your secrets backend, never read back out of the connection
being repaired, and no value is ever printed.

The check runs first and the repair is sent with that check's own commit, so
what gets written is what you were just shown. If your git branch moves in
between, the repair is refused and you can look again.

Run with --check-only to look without changing anything.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		checkOnly, _ := cmd.Flags().GetBool("check-only")
		escaped := url.PathEscape(name)

		// STEP 1 — the read-only check. This is what supplies the reviewed
		// commit, and what the person actually gets to look at.
		checkBody, checkStatus, err := apiGet("/api/v1/clusters/" + escaped + "/connection-comparison")
		if err != nil {
			return err
		}
		if checkStatus != 200 {
			return printAPIError(checkBody, checkStatus)
		}
		var check connectionCheckResult
		if err := json.Unmarshal(checkBody, &check); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}
		printConnectionCheck(&check)

		if checkOnly {
			return nil
		}

		// Nothing to do, or nothing Sharko may do.
		if !check.RepairAvailable {
			fmt.Println()
			fmt.Println("Sharko will not change this connection.")
			if check.LimitReason != "" {
				fmt.Printf("  %s\n", check.LimitReason)
			}
			return nil
		}
		if check.Status == "check_failed" {
			fmt.Println()
			fmt.Println("The check did not finish, so Sharko will not change anything.")
			if check.FailureReason != "" {
				fmt.Printf("  %s\n", check.FailureReason)
			}
			// Exit non-zero: a script must be able to tell "could not look" from
			// "looked and it was fine".
			return fmt.Errorf("the connection check for %q did not finish", name)
		}
		if check.ComparedCommit == "" {
			fmt.Println()
			fmt.Println("Sharko cannot tell which commit your git branch is on, so it will not rewrite this connection.")
			fmt.Println("Sharko only makes this change when it can name the exact commit it is matching.")
			return fmt.Errorf("no commit could be confirmed for %q", name)
		}

		// STEP 2 — confirm, naming what will be touched.
		if !yes {
			fmt.Println()
			switch check.RepairScope {
			case "full_connection":
				fmt.Printf("Repair %q's whole ArgoCD connection to match commit %s? [y/N]: ", name, shortCommit(check.ComparedCommit))
			default:
				fmt.Printf("Re-apply %q's addon labels to match commit %s? [y/N]: ", name, shortCommit(check.ComparedCommit))
			}
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Aborted. Nothing was changed.")
				return nil
			}
		}

		// STEP 3 — the repair, carrying the commit that was just reviewed.
		fmt.Printf("Repairing %s... ", name)
		repairBody, repairStatus, err := apiPost(
			"/api/v1/clusters/"+escaped+"/connection-repair?reviewed_commit="+url.QueryEscape(check.ComparedCommit), nil)
		if err != nil {
			fmt.Println("failed")
			return err
		}
		if repairStatus != 200 {
			fmt.Println("failed")
			// The server's sentences are already safe and already plain — a
			// provider's own words never reach them. Print them as they are
			// rather than inventing a second wording here.
			return printAPIError(repairBody, repairStatus)
		}
		fmt.Println("done")

		var result connectionRepairResult
		if err := json.Unmarshal(repairBody, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		fmt.Println()
		if result.Repaired {
			fmt.Printf("Changed %d part(s) of %s's connection:\n", len(result.FieldsRepaired), name)
			for _, f := range result.FieldsRepaired {
				fmt.Printf("  %s\n", f)
			}
		} else {
			fmt.Printf("%s's connection already matched what Sharko intends. Nothing was changed.\n", name)
		}
		if result.PreservedForeignLabels > 0 || result.PreservedForeignDataKeys > 0 {
			fmt.Printf("  Left alone: %d label(s) and %d connection setting(s) that are not Sharko's.\n",
				result.PreservedForeignLabels, result.PreservedForeignDataKeys)
		}
		if result.Message != "" {
			fmt.Printf("  %s\n", result.Message)
		}

		// The fresh check the server ran after the repair — what it achieved,
		// rather than just "done".
		fmt.Println()
		fmt.Println("After the repair:")
		printConnectionCheck(&result.Comparison)

		return nil
	},
}

// printConnectionCheck prints a comparison in plain words.
//
// It prints field PATHS and statuses only. A sensitive field is named and its
// state is given; its value is not printed, because the server never sends one.
func printConnectionCheck(c *connectionCheckResult) {
	if c == nil {
		return
	}
	fmt.Printf("Connection check for %s: %s\n", c.Cluster, plainStatus(c.Status))
	if c.Branch != "" {
		if c.ComparedCommit != "" {
			fmt.Printf("  Compared with git branch %s at commit %s\n", c.Branch, shortCommit(c.ComparedCommit))
		} else {
			fmt.Printf("  Compared with git branch %s (Sharko could not confirm which commit)\n", c.Branch)
		}
	}
	if c.FailureReason != "" {
		fmt.Printf("  %s\n", c.FailureReason)
	}
	if c.LimitReason != "" {
		fmt.Printf("  %s\n", c.LimitReason)
	}
	if len(c.Differences) > 0 {
		fmt.Println("  Does not match:")
		for _, d := range c.Differences {
			if d.Sensitive {
				// Named, with its state, and no value — Sharko never prints
				// sign-in details, and the server never sent any.
				fmt.Printf("    %s — %s (sign-in details, value not shown)\n", d.Path, plainFieldStatus(d.Status))
				continue
			}
			fmt.Printf("    %s — %s\n", d.Path, plainFieldStatus(d.Status))
		}
	}
	if len(c.NotChecked) > 0 {
		fmt.Println("  Not checked:")
		for _, n := range c.NotChecked {
			fmt.Printf("    %s — %s\n", n.Path, n.Reason)
		}
	}
}

// plainStatus turns a status word into something a person reads.
func plainStatus(status string) string {
	switch status {
	case "synced":
		return "matches what Sharko intends"
	case "out_of_sync":
		return "does not match"
	case "missing":
		return "there is no connection yet"
	case "check_failed":
		return "Sharko could not finish the check"
	case "ownership_conflict":
		return "another tool owns this connection"
	case "limited":
		return "everything Sharko could check matches, but it could not check all of it"
	default:
		return status
	}
}

// plainFieldStatus turns a field's status into plain words.
func plainFieldStatus(status string) string {
	switch status {
	case "different":
		return "different from what Sharko intends"
	case "missing":
		return "missing"
	case "unexpected":
		return "there but no longer wanted"
	case "same":
		return "the same"
	default:
		return status
	}
}

// shortCommit trims a commit to the first 8 characters for reading. The full
// value still goes to the server.
func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return strings.TrimSpace(commit)
}
