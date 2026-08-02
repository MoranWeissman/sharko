package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// argocd_sharko_token.go provisions a dedicated local ArgoCD account
// ("sharko") and mints it a non-expiring apiKey token — this is what fixes
// the playground's ArgoCD connection dying every 24h (see mintArgoCDToken's
// doc comment in cmd_up.go for the full story). Everything here is
// playground-only: it patches the ArgoCD instance THIS playground run just
// installed via install.yaml, never any chart/values shipped to real users.

// sharkoAccountName is the dedicated local ArgoCD account Sharko's
// connection authenticates as (instead of the built-in admin account, which
// cannot hold apiKey tokens — verified live).
const sharkoAccountName = "sharko"

// sharkoAccountCapability is the argocd-cm accounts.<name> capability that
// lets an account hold apiKey tokens (as opposed to "login", which is
// password/session-only and subject to the same 24h session expiry admin
// has).
const sharkoAccountCapability = "apiKey"

// sharkoRoleGrantLine is the argocd-rbac-cm policy.csv line granting the
// sharko account ArgoCD's built-in role:admin — same shape and same
// built-in role as adminRoleGrantLine (cmd_up.go), just a different
// account. See grantArgoCDAdminRBAC's doc comment for the full RBAC
// rationale.
const sharkoRoleGrantLine = "g, " + sharkoAccountName + ", role:admin"

// ensureSharkoRBACGrant ensures argocd-rbac-cm grants the sharko account
// role:admin. It shares ensureRBACGrantLine (cmd_up.go) with
// grantArgoCDAdminRBAC's admin grant — each call does its own fresh
// read-then-patch of policy.csv, so calling this before or after
// grantArgoCDAdminRBAC always composes: neither call can clobber the
// other's line.
func ensureSharkoRBACGrant(kubeconfigPath string) (changed bool, err error) {
	fmt.Println("    Granting sharko role:admin in argocd-rbac-cm...")

	changed, err = ensureRBACGrantLine(kubeconfigPath, sharkoRoleGrantLine)
	if err != nil {
		return false, err
	}
	if changed {
		fmt.Println("    argocd-rbac-cm patched — sharko granted role:admin")
	} else {
		fmt.Println("    argocd-rbac-cm already grants sharko role:admin")
	}
	return changed, nil
}

// ensureSharkoAccountCapability ensures argocd-cm declares the dedicated
// sharko local account with apiKey capability (accounts.sharko: apiKey).
// On a stock ArgoCD install this key is absent entirely (kubectl's jsonpath
// read returns an empty string, not an error — the ConfigMap itself always
// exists).
func ensureSharkoAccountCapability(kubeconfigPath string) (changed bool, err error) {
	fmt.Println("    Granting apiKey capability to sharko account in argocd-cm...")

	current, _, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"get", "configmap", "argocd-cm",
		"-o", `jsonpath={.data.accounts\.sharko}`)
	if err != nil {
		// argocd-cm always ships with install.yaml, so a read failure here
		// means something is genuinely wrong with the install — surface it
		// rather than silently skipping the grant.
		return false, fmt.Errorf("read argocd-cm: %w", err)
	}

	newValue, alreadyPresent := mergeSharkoAccountCapability(current)
	if alreadyPresent {
		fmt.Println("    argocd-cm already grants sharko apiKey capability")
		return false, nil
	}

	patch := map[string]map[string]string{
		"data": {"accounts.sharko": newValue},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return false, fmt.Errorf("marshal argocd-cm patch: %w", err)
	}

	if _, stderr, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"patch", "configmap", "argocd-cm",
		"--type", "merge",
		"-p", string(patchJSON)); err != nil {
		return false, fmt.Errorf("patch argocd-cm: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    argocd-cm patched — sharko granted apiKey capability")
	return true, nil
}

// mergeSharkoAccountCapability is the pure decision half of
// ensureSharkoAccountCapability: given the CURRENT value of argocd-cm's
// accounts.sharko field (empty string if the key is absent, as on a stock
// install), it reports the value that should be written and whether apiKey
// capability was already present. Split out so this parsing logic has a
// direct unit test that needs no cluster.
func mergeSharkoAccountCapability(current string) (newValue string, alreadyPresent bool) {
	trimmed := strings.TrimSpace(current)
	if trimmed == "" {
		return sharkoAccountCapability, false
	}

	for _, capability := range strings.Split(trimmed, ",") {
		if strings.TrimSpace(capability) == sharkoAccountCapability {
			return current, true
		}
	}

	return trimmed + ", " + sharkoAccountCapability, false
}

// restartArgoCDServerAndWait rolls out a restart of argocd-server and waits
// for the rollout to complete. Called only when ensureSharkoAccountCapability
// or ensureSharkoRBACGrant actually changed a ConfigMap — argocd-rbac-cm
// alone hot-reloads (see grantArgoCDAdminRBAC's doc comment in cmd_up.go),
// but this playground's verified-live fix needed the restart for the
// argocd-cm account-capability change to take effect.
func restartArgoCDServerAndWait(kubeconfigPath string) error {
	fmt.Println("    Restarting argocd-server to apply sharko account changes...")

	if _, stderr, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"rollout", "restart", "deployment/argocd-server"); err != nil {
		return fmt.Errorf("rollout restart argocd-server: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Waiting for argocd-server rollout to complete...")
	if _, stderr, err := runCmd(3*time.Minute, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"rollout", "status", "deployment/argocd-server", "--timeout=180s"); err != nil {
		return fmt.Errorf("wait for argocd-server rollout: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    argocd-server restarted and ready")
	return nil
}

// createArgoCDAccountToken calls ArgoCD's account-token API
// (POST /api/v1/account/{name}/token) to mint a new apiKey token for
// accountName, authenticated as bearerToken (an admin session token — admin
// can mint tokens for any account with apiKey capability).
//
// Requests NO expiry: the JSON body is deliberately empty ("{}"), with no
// "expiresIn" key at all. ArgoCD's CreateToken RPC takes expiresIn as a
// google.protobuf.Duration; leaving it unset (rather than sending an
// explicit zero) is what ArgoCD treats as "never expires" — sending a
// numeric zero risks a different code path in some ArgoCD versions, so the
// safest verified-equivalent is to omit the field entirely.
func createArgoCDAccountToken(client *http.Client, baseURL, bearerToken, accountName string) (string, error) {
	url := baseURL + "/api/v1/account/" + accountName + "/token"

	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("create account-token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read account-token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST %s: status %d: %s", url, resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("parse account-token response: %w", err)
	}
	if tokenResp.Token == "" {
		return "", fmt.Errorf("account-token response for %q had no token field", accountName)
	}

	return tokenResp.Token, nil
}
