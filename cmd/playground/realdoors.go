package main

// realdoors.go implements the "real doors" playground flow (v4 Wave 2,
// story w2-q7): once infrastructure is up (kind clusters + ArgoCD + Gitea +
// Sharko + an EMPTY git repo), ALL Sharko state is created through Sharko's
// own REST API — exactly like a real user would, never by writing
// Sharko-format files straight into git:
//
//  1. Seed-bootstrap: POST /api/v1/init opens a real PR with the v4 seed
//     files (internal/api/init.go). The playground merges that PR through
//     Gitea's own REST API — the way a human would click "Merge" in the
//     Gitea UI (Sharko itself exposes no merge-PR REST route of its own).
//     Sharko's own background poller then detects the merge and runs the
//     ArgoCD root-app bootstrap + wait-for-sync steps itself.
//  2. Cluster registration: POST /api/v1/clusters opens a real PR per spoke.
//     The playground merges each PR the same way, then the cluster
//     reconciler (internal/clusterreconciler) picks up the merged
//     fleet/connections.yaml entry on its own tick and creates the ArgoCD
//     cluster Secret.
//  3. Addon enable: POST /api/v1/v4/clusters/{name}/addons/{addon} opens a
//     real PR. The playground merges it, then the engine (ApplicationSet)
//     picks it up.
//
// Scope note: this real-doors flow only applies to the Gitea backend
// (PLAYGROUND_GIT_BACKEND=gitea, the default). The GitFake backend
// (PLAYGROUND_GIT_BACKEND=gitfake) keeps its existing SEED_CONTENT bake-in,
// because GitFake (tests/e2e/harness/gitfake) only implements the git
// smart-HTTP protocol (info/refs, upload-pack, receive-pack) — it has no
// PR/merge REST API for the playground to drive the same way. Extending
// GitFake with a PR/merge REST shim so it can run this same flow is
// tests/e2e/harness work, out of scope for cmd/playground.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// enableAddonName is the single addon the playground enables on the first
// spoke through the real v4 enable door, so the walkthrough has something
// concrete to watch the engine pick up. metrics-server is in Sharko's
// embedded curated catalog (catalog/addons.yaml) so it's enable-able
// immediately after the seed-bootstrap PR merges — no catalog-import step
// needed.
const enableAddonName = "metrics-server"

const giteaLocalAPIBase = "http://localhost:13000/api/v1"

// runGiteaRealDoorsFlow drives the entire post-infrastructure Sharko state
// through the real API doors against the Gitea backend: create the
// connection, seed-bootstrap, register every spoke, then enable one addon.
// client must already be logged in.
func runGiteaRealDoorsFlow(client *apiClient, kubeconfigPath string, numSpokes int, spokeNames []string, kubeconfigs []string, giteaURL, giteaToken string) error {
	fmt.Println("    Creating Gitea connection...")
	argocdToken, err := mintArgoCDToken(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("mint argocd token: %w", err)
	}
	argocdServerURL := fmt.Sprintf("http://argocd-server.%s.svc.cluster.local", ArgoCDNamespace)
	if err := client.createConnection(
		GiteaConnectionName, "gitea", giteaURL, giteaToken,
		argocdServerURL, ArgoCDNamespace, argocdToken,
	); err != nil {
		return fmt.Errorf("create gitea connection: %w", err)
	}
	fmt.Printf("    Gitea connection %q created and set as active\n", GiteaConnectionName)

	// Re-establish a port-forward to Gitea's REST API so the playground can
	// merge PRs the way a human would click "Merge" in the UI — Sharko
	// itself exposes no merge-PR REST route (internal/gitprovider/gitea.go
	// merges in-process, not over HTTP).
	fmt.Println("    Establishing Gitea port-forward for PR merges...")
	giteaPF, err := establishGiteaPortForward(kubeconfigPath)
	if err != nil {
		return err
	}
	defer func() { _ = killProcessGroup(giteaPF) }()

	if err := runInitFlow(client, giteaToken); err != nil {
		return fmt.Errorf("seed-bootstrap (POST /api/v1/init): %w", err)
	}

	for i := 0; i < numSpokes; i++ {
		if err := registerSpokeRealDoor(client, giteaToken, spokeNames[i], kubeconfigs[i]); err != nil {
			return fmt.Errorf("register %s: %w", spokeNames[i], err)
		}
	}

	if len(spokeNames) > 0 {
		if err := enableAddonRealDoor(client, giteaToken, spokeNames[0], enableAddonName); err != nil {
			return fmt.Errorf("enable addon %s on %s: %w", enableAddonName, spokeNames[0], err)
		}
	}

	fmt.Println("    All spokes registered through the real API doors")
	return nil
}

// runInitFlow calls POST /api/v1/init, merges the resulting seed-bootstrap
// PR through Gitea's REST API, then polls until Sharko's own background
// steps (ArgoCD root app + wait-for-sync) complete.
func runInitFlow(client *apiClient, giteaToken string) error {
	fmt.Println("==> Running Sharko seed-bootstrap (POST /api/v1/init)")

	opID, status, _, err := client.initRepo(false /* auto_merge — the playground merges it itself */)
	if err != nil {
		return err
	}
	fmt.Printf("    Init operation %s started (status=%s)\n", opID, status)

	prMerged := false
	deadline := time.Now().Add(90 * time.Second) // budget to reach "waiting" (PR opened)
	lastHeartbeat := time.Now()

	for {
		sess, err := client.getOperation(opID)
		if err != nil {
			return fmt.Errorf("poll init operation %s: %w", opID, err)
		}

		switch sess.Status {
		case "waiting":
			if !prMerged {
				prNumber, err := resolvePRNumber(sess.WaitPayload, giteaToken)
				if err != nil {
					return fmt.Errorf("determine init PR number: %w", err)
				}
				fmt.Printf("    Init PR #%d opened — merging via Gitea (like a human clicking Merge)...\n", prNumber)
				if err := giteaMergePR(giteaToken, GiteaAdminUser, GiteaRepoName, prNumber); err != nil {
					return fmt.Errorf("merge init PR #%d: %w", prNumber, err)
				}
				fmt.Println("    Init PR merged — waiting for Sharko to create the ArgoCD root app and sync...")
				prMerged = true
				// Reset the budget: the post-merge bootstrap steps
				// (AddRepository + BootstrapArgoCD + WaitForSync, capped at
				// 2m server-side) get their own window.
				deadline = time.Now().Add(4 * time.Minute)
			}
		case "completed":
			fmt.Println("    Seed-bootstrap complete (ArgoCD root app created + synced)")
			return nil
		case "failed":
			return fmt.Errorf("init operation failed: %s", sess.Error)
		case "cancelled":
			return fmt.Errorf("init operation was cancelled: %s", sess.Error)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("init operation did not reach completed within budget (last status=%s, detail=%s)", sess.Status, sess.WaitDetail)
		}

		// Heartbeat well under the server's 2-minute abandonment window
		// while a "waiting" session is in flight.
		if time.Since(lastHeartbeat) > 60*time.Second {
			if err := client.heartbeatOperation(opID); err != nil {
				fmt.Printf("      Warning: init heartbeat failed (non-fatal): %v\n", err)
			}
			lastHeartbeat = time.Now()
		}

		time.Sleep(5 * time.Second)
	}
}

// registerSpokeRealDoor registers one spoke via POST /api/v1/clusters,
// merges the resulting PR through Gitea if it wasn't already auto-merged,
// then gives the cluster reconciler a bounded window to pick it up.
func registerSpokeRealDoor(client *apiClient, giteaToken, name, kubeconfig string) error {
	fmt.Printf("    Registering %s (POST /api/v1/clusters)...\n", name)
	gr, err := client.registerClusterV4(name, kubeconfig, false /* auto_merge */)
	if err != nil {
		return err
	}

	if !gr.Merged {
		if gr.PRID == 0 {
			return fmt.Errorf("no PR id returned and registration was not merged")
		}
		fmt.Printf("      PR #%d opened for %s — merging via Gitea...\n", gr.PRID, name)
		if err := giteaMergePR(giteaToken, GiteaAdminUser, GiteaRepoName, gr.PRID); err != nil {
			return fmt.Errorf("merge register PR #%d: %w", gr.PRID, err)
		}
	}

	// Let the cluster reconciler (internal/clusterreconciler, ~30s tick)
	// pick up the merged fleet/connections.yaml entry and create the ArgoCD
	// cluster Secret. This is a courtesy wait, not a hard requirement — the
	// reconciler converges on its own regardless of whether we see it here.
	fmt.Printf("      Waiting for the cluster reconciler to pick up %s...\n", name)
	if err := waitForClusterVisible(client, name, 90*time.Second); err != nil {
		fmt.Printf("      Warning: %s not visible via GET /api/v1/clusters/%s within 90s — the reconciler's next tick will still pick it up: %v\n", name, name, err)
	} else {
		fmt.Printf("      %s is registered\n", name)
	}
	return nil
}

// waitForClusterVisible polls GET /api/v1/clusters/{name} until it appears
// or the timeout expires.
func waitForClusterVisible(client *apiClient, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		exists, err := client.clusterExists(name)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		time.Sleep(5 * time.Second)
	}
}

// enableAddonRealDoor enables one addon on one cluster via the v4 endpoint
// and merges the resulting PR through Gitea if it wasn't already
// auto-merged.
func enableAddonRealDoor(client *apiClient, giteaToken, clusterName, addonName string) error {
	fmt.Printf("==> Enabling addon %q on %s (POST /api/v1/v4/clusters/%s/addons/%s)\n", addonName, clusterName, clusterName, addonName)
	gr, err := client.enableAddonV4(clusterName, addonName, false /* auto_merge */)
	if err != nil {
		return err
	}

	if !gr.Merged {
		if gr.PRID == 0 {
			return fmt.Errorf("no PR id returned and addon-enable was not merged")
		}
		fmt.Printf("    PR #%d opened — merging via Gitea...\n", gr.PRID)
		if err := giteaMergePR(giteaToken, GiteaAdminUser, GiteaRepoName, gr.PRID); err != nil {
			return fmt.Errorf("merge addon-enable PR #%d: %w", gr.PRID, err)
		}
	}

	fmt.Println("    Addon PR merged — the engine (ApplicationSet) will pick it up on its own sync loop")
	return nil
}

// resolvePRNumber determines the Gitea PR number to merge for the init
// operation. It prefers parsing the operation's wait_payload (the PR URL,
// per internal/operations/store.go), falling back to listing the repo's
// (single, at this point in the flow) open PR if that fails for any reason.
func resolvePRNumber(waitPayload, giteaToken string) (int, error) {
	if waitPayload != "" {
		if n, err := parsePRNumberFromURL(waitPayload); err == nil {
			return n, nil
		}
	}
	return giteaListOpenPRNumber(giteaToken, GiteaAdminUser, GiteaRepoName)
}

// parsePRNumberFromURL extracts the trailing integer PR number from a Gitea
// PR URL of the form http://host/owner/repo/pulls/<number>.
func parsePRNumberFromURL(url string) (int, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(url), "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 || idx == len(trimmed)-1 {
		return 0, fmt.Errorf("cannot find PR number in URL %q", url)
	}
	numStr := trimmed[idx+1:]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("PR number segment %q in URL %q is not an integer: %w", numStr, url, err)
	}
	return n, nil
}

// giteaListOpenPRNumber returns the number of the (expected single) open PR
// on the playground repo, via Gitea's REST API.
func giteaListOpenPRNumber(token, owner, repo string) (int, error) {
	client := newHTTPClient()
	url := fmt.Sprintf("%s/repos/%s/%s/pulls?state=open&limit=1&sort=recentupdate", giteaLocalAPIBase, owner, repo)
	req := mustNewRequest("GET", url, "")
	req.Header.Set("Authorization", "token "+token)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list open PRs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("list open PRs: unexpected status %d", resp.StatusCode)
	}

	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return 0, fmt.Errorf("decode open PR list: %w", err)
	}
	if len(prs) == 0 {
		return 0, fmt.Errorf("no open PRs found")
	}
	return prs[0].Number, nil
}

// giteaMergePR merges an open Gitea PR by number, retrying the same way
// internal/gitprovider/gitea.go's MergePullRequest does: Gitea computes PR
// mergeability asynchronously after creation, so an immediate merge attempt
// can 405 ("not ready") even after the mergeable flag flips true (the race
// fixed for Sharko's own merges in PR #617). Idempotent — returns nil
// immediately if the PR is already merged.
func giteaMergePR(token, owner, repo string, prNumber int) error {
	const maxAttempts = 10
	const pollDelay = 3 * time.Second

	client := newHTTPClient()
	base := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", giteaLocalAPIBase, owner, repo, prNumber)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		getReq := mustNewRequest("GET", base, "")
		getReq.Header.Set("Authorization", "token "+token)
		getResp, err := client.Do(getReq)
		if err != nil {
			if attempt == maxAttempts {
				return fmt.Errorf("get PR #%d failed after %d attempts: %w", prNumber, maxAttempts, err)
			}
			fmt.Printf("      get PR #%d failed (attempt %d/%d): %v — retrying\n", prNumber, attempt, maxAttempts, err)
			time.Sleep(pollDelay)
			continue
		}

		var pr struct {
			Mergeable bool `json:"mergeable"`
			Merged    bool `json:"merged"`
		}
		decodeErr := json.NewDecoder(getResp.Body).Decode(&pr)
		statusCode := getResp.StatusCode
		getResp.Body.Close()

		if statusCode != http.StatusOK {
			if attempt == maxAttempts {
				return fmt.Errorf("get PR #%d: status %d after %d attempts", prNumber, statusCode, maxAttempts)
			}
			fmt.Printf("      get PR #%d returned status %d (attempt %d/%d) — retrying\n", prNumber, statusCode, attempt, maxAttempts)
			time.Sleep(pollDelay)
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode PR #%d: %w", prNumber, decodeErr)
		}

		if pr.Merged {
			return nil // idempotent — already merged
		}
		if !pr.Mergeable {
			if attempt == maxAttempts {
				return fmt.Errorf("PR #%d still not mergeable after %d attempts", prNumber, maxAttempts)
			}
			fmt.Printf("      PR #%d not yet mergeable (attempt %d/%d) — retrying\n", prNumber, attempt, maxAttempts)
			time.Sleep(pollDelay)
			continue
		}

		mergeReq := mustNewRequest("POST", base+"/merge", `{"Do":"merge"}`)
		mergeReq.Header.Set("Authorization", "token "+token)
		mergeReq.Header.Set("Content-Type", "application/json")
		mergeResp, err := client.Do(mergeReq)
		if err != nil {
			if attempt == maxAttempts {
				return fmt.Errorf("merge PR #%d failed after %d attempts: %w", prNumber, maxAttempts, err)
			}
			fmt.Printf("      merge PR #%d failed (attempt %d/%d): %v — retrying\n", prNumber, attempt, maxAttempts, err)
			time.Sleep(pollDelay)
			continue
		}
		mergeStatus := mergeResp.StatusCode
		mergeResp.Body.Close()

		if mergeStatus >= 200 && mergeStatus < 300 {
			fmt.Printf("      PR #%d merged\n", prNumber)
			return nil
		}
		if mergeStatus == http.StatusMethodNotAllowed {
			if attempt == maxAttempts {
				return fmt.Errorf("PR #%d still not ready (405) after %d attempts", prNumber, maxAttempts)
			}
			fmt.Printf("      PR #%d not ready yet (405, attempt %d/%d) — retrying\n", prNumber, attempt, maxAttempts)
			time.Sleep(pollDelay)
			continue
		}
		return fmt.Errorf("merge PR #%d: unexpected status %d", prNumber, mergeStatus)
	}

	return fmt.Errorf("merge PR #%d: exhausted %d attempts", prNumber, maxAttempts)
}

// establishGiteaPortForward starts a kubectl port-forward to the in-cluster
// Gitea service on localhost:13000, retrying a few times to absorb flaky
// port-forward startup, and waits until the Gitea API answers before
// returning. Shared by deployGitea (initial repo creation) and
// runGiteaRealDoorsFlow (PR merges).
func establishGiteaPortForward(kubeconfigPath string) (*exec.Cmd, error) {
	if isLocalPortInUse(13000) {
		return nil, fmt.Errorf("local port 13000 is already in use — the playground needs it to reach Gitea. Stop whatever is using it and re-run")
	}

	var pfCmd *exec.Cmd
	for attempt := 1; attempt <= 3; attempt++ {
		var startErr error
		pfCmd, startErr = startBackground("kubectl", "--kubeconfig", kubeconfigPath,
			"--context", ContextHub, "-n", Namespace,
			"port-forward", "svc/gitea", "13000:3000")
		if startErr != nil {
			fmt.Printf("      gitea port-forward start attempt %d/3 failed: %v\n", attempt, startErr)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := waitForGiteaAPI("http://localhost:13000", 25*time.Second); err != nil {
			fmt.Printf("      gitea port-forward not ready (attempt %d/3): %v — retrying\n", attempt, err)
			_ = killProcessGroup(pfCmd)
			pfCmd = nil
			time.Sleep(2 * time.Second)
			continue
		}
		return pfCmd, nil
	}
	return nil, fmt.Errorf("gitea port-forward did not become ready after 3 attempts")
}
