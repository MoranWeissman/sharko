package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	initCmd.Flags().Bool("no-bootstrap", false, "Skip ArgoCD bootstrapping")
	initCmd.Flags().Bool("auto-merge", false, "Automatically merge the bootstrap PR instead of waiting for manual merge")
	rootCmd.AddCommand(initCmd)
}

// opSession mirrors the operations.Session fields we care about in the CLI.
type opSession struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	CurrentStep int       `json:"current_step"`
	WaitDetail  string    `json:"wait_detail,omitempty"`
	WaitPayload string    `json:"wait_payload,omitempty"` // PR URL when waiting
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	Steps       []opStep  `json:"steps"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type opStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the addons repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		noBootstrap, _ := cmd.Flags().GetBool("no-bootstrap")
		autoMerge, _ := cmd.Flags().GetBool("auto-merge")

		body := map[string]interface{}{
			"bootstrap_argocd": !noBootstrap,
			"auto_merge":       autoMerge,
		}

		fmt.Println("Initializing addons repository...")

		// POST /api/v1/init — returns 202 with operation_id or 200 for existing session.
		respBody, status, err := apiPost("/api/v1/init", body)
		if err != nil {
			return err
		}

		if status != 200 && status != 201 && status != 202 {
			return printAPIError(respBody, status)
		}

		var initResp struct {
			OperationID string `json:"operation_id"`
			Status      string `json:"status"`
			Resumed     bool   `json:"resumed"`
			WaitPayload string `json:"wait_payload,omitempty"`
		}
		if err := json.Unmarshal(respBody, &initResp); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		opID := initResp.OperationID
		if opID == "" {
			return fmt.Errorf("server did not return an operation_id")
		}

		if initResp.Resumed {
			fmt.Printf("  Resumed existing init operation: %s\n", opID)
		} else {
			fmt.Printf("  Operation started: %s\n", opID)
		}

		return watchOperation(opID)
	},
}

// watchOperation polls GET /api/v1/operations/{id} every 2 seconds, sends a
// heartbeat every 15 seconds, and prints live step progress until the operation
// reaches a terminal state.
func watchOperation(opID string) error {
	heartbeatPath := fmt.Sprintf("/api/v1/operations/%s/heartbeat", opID)
	statusPath := fmt.Sprintf("/api/v1/operations/%s", opID)

	lastPrinted := -1
	lastStatus := ""
	lastHeartbeat := time.Now()
	heartbeatInterval := 15 * time.Second

	for {
		// Send heartbeat if due.
		if time.Since(lastHeartbeat) >= heartbeatInterval {
			_, _, _ = apiPost(heartbeatPath, nil)
			lastHeartbeat = time.Now()
		}

		// Fetch current session state.
		body, httpStatus, err := apiGet(statusPath)
		if err != nil {
			return fmt.Errorf("polling operation: %w", err)
		}
		if httpStatus == 404 {
			return fmt.Errorf("operation %s not found", opID)
		}

		var sess opSession
		if err := json.Unmarshal(body, &sess); err != nil {
			return fmt.Errorf("invalid operation response: %w", err)
		}

		// Print any newly completed steps.
		for i, step := range sess.Steps {
			if i > lastPrinted && (step.Status == "completed" || step.Status == "failed") {
				icon := "\u2713"
				if step.Status == "failed" {
					icon = "\u2717"
				}
				detail := ""
				if step.Detail != "" {
					detail = " (" + step.Detail + ")"
				}
				fmt.Printf("  %s %s%s\n", icon, step.Name, detail)
				lastPrinted = i
			}
		}

		// Handle waiting state (PR not yet merged).
		if sess.Status == "waiting" && lastStatus != "waiting" {
			fmt.Println()
			if sess.WaitPayload != "" {
				fmt.Printf("  Pull Request: %s\n", sess.WaitPayload)
			}
			if sess.WaitDetail != "" {
				fmt.Printf("  %s\n", sess.WaitDetail)
			}
			fmt.Println("  Watching for merge... (Ctrl-C to stop watching; the operation continues server-side)")
			fmt.Println()
		}
		lastStatus = sess.Status

		// Check terminal states.
		switch sess.Status {
		case "completed":
			fmt.Println()
			if sess.Result != "" {
				fmt.Printf("  Done: %s\n", sess.Result)
			} else {
				fmt.Println("  Done.")
			}
			return nil
		case "failed":
			fmt.Println()
			if sess.Error != "" {
				return fmt.Errorf("init failed: %s", sess.Error)
			}
			return fmt.Errorf("init failed (check server logs)")
		case "cancelled":
			fmt.Println()
			return fmt.Errorf("init was cancelled")
		}

		time.Sleep(2 * time.Second)
	}
}
