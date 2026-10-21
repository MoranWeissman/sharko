package api

// docs_ep2take2_test.go — pins for the episode 2 take 2 docs stories.
//
//   - G3: every kubectl and helm line that talks to the ArgoCD cluster names
//     the cluster. On a laptop whose default context is a different cluster, a
//     line without the flag changes the wrong cluster.
//   - G4: no command prints the ArgoCD admin password, the Sharko admin
//     password or the new ArgoCD token. They go to files made with umask 077.
//     The two places that must show a secret say so in plain words.
//   - G5: "helm repo add" uses --force-update, so an old helm setup that
//     already has the repo does not get in the way.

import (
	"strings"
	"testing"
)

// bashCommands returns every command line from the ```bash blocks of a page,
// with "\" line continuations joined into one line.
func bashCommands(doc string) []string {
	var out []string
	in := false
	var cur string
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			if strings.TrimSpace(cur) != "" {
				out = append(out, strings.TrimSpace(cur)) // a block that ends on a "\\" line
			}
			in = t == "```bash"
			cur = ""
			continue
		}
		if !in || t == "" {
			continue
		}
		if strings.HasSuffix(t, "\\") {
			cur += strings.TrimSuffix(t, "\\") + " "
			continue
		}
		out = append(out, cur+t)
		cur = ""
	}
	return out
}

// section returns the text of doc from the start marker up to the end marker.
func section(t *testing.T, doc, start, end string) string {
	t.Helper()
	a := strings.Index(doc, start)
	if a < 0 {
		t.Fatalf("marker %q not found", start)
	}
	b := strings.Index(doc[a:], end)
	if b < 0 {
		t.Fatalf("marker %q not found after %q", end, start)
	}
	return doc[a : a+b]
}

func TestDocsEp2Take2ContextOnEveryClusterLine(t *testing.T) {
	firstRun := readDocForKindPins(t, "docs/site/getting-started/first-run.md")
	kind := readDocForKindPins(t, "docs/site/getting-started/try-on-your-laptop.md")
	security := readDocForKindPins(t, "docs/site/operator/security.md")

	check := func(page, text, ctx, kubeCtx string) {
		seen := 0
		for _, c := range bashCommands(text) {
			isK := strings.HasPrefix(c, "kubectl ") || strings.Contains(c, "kubectl get secret")
			isH := strings.HasPrefix(c, "helm ")
			if strings.HasPrefix(c, "kubectl config ") || strings.HasPrefix(c, "helm repo ") {
				continue // these do not talk to a cluster
			}
			if isK {
				seen++
				if !strings.Contains(c, ctx) {
					t.Errorf("%s: kubectl line without %q: %s", page, ctx, c)
				}
			}
			if isH {
				seen++
				if !strings.Contains(c, kubeCtx) {
					t.Errorf("%s: helm line without %q: %s", page, kubeCtx, c)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s: found no kubectl or helm lines; the pin is not looking at anything", page)
		}
	}

	tokenSection := section(t, firstRun, "## Make the ArgoCD token first", "## Step 1: Welcome")
	check("first-run token section", tokenSection, "--context <argocd-context>", "--kube-context <argocd-context>")
	check("try-on-your-laptop", kind, "--context kind-", "--kube-context kind-hub")

	// The ArgoCD rights patch lives on the security page; the same flag.
	rbac := section(t, security, "kubectl patch configmap argocd-rbac-cm", "```")
	if !strings.Contains(rbac, "--context <argocd-context>") {
		t.Errorf("security page: the argocd-rbac-cm patch does not name the context")
	}

	// The two pages must use the same placeholder and the same wording.
	for _, want := range []string{
		"every `kubectl` line below names the cluster with `--context <argocd-context>`",
		"`kubectl config get-contexts` lists them",
	} {
		if !strings.Contains(firstRun, want) {
			t.Errorf("first-run: missing the exact text %q", want)
		}
	}
	if !strings.Contains(kind, "Use `kind-hub` as the ArgoCD context.") {
		t.Errorf("kind page: no longer tells the user which context to use for the token steps")
	}
}

func TestDocsEp2Take2NoSecretOnTheScreen(t *testing.T) {
	pages := map[string]string{
		"first-run":          readDocForKindPins(t, "docs/site/getting-started/first-run.md"),
		"try-on-your-laptop": readDocForKindPins(t, "docs/site/getting-started/try-on-your-laptop.md"),
	}

	for name, doc := range pages {
		secretCmds := 0
		for _, c := range bashCommands(doc) {
			readsSecret := strings.Contains(c, "base64 -d") || strings.Contains(c, "generate-token")
			if readsSecret {
				secretCmds++
				// It must go to a file that only the user can read.
				if !strings.Contains(c, "umask 077") || !strings.Contains(c, "> ~/") {
					t.Errorf("%s: a command that makes a password or token does not save it to a private file: %s", name, c)
				}
			}
			// A password must never be typed on a command line in the clear.
			if strings.Contains(c, "--password ") && !strings.Contains(c, `$(cat ~/`) {
				t.Errorf("%s: a login puts the password on the command line some way other than reading it from the private file: %s", name, c)
			}
			// The only "cat" of a secret file is the one the text calls out.
			if strings.HasPrefix(c, "cat ~/") && !strings.Contains(doc, "must show it on the screen") && !strings.Contains(doc, "shows it on the screen") {
				t.Errorf("%s: shows a secret file on the screen without saying so: %s", name, c)
			}
		}
		if secretCmds == 0 {
			t.Errorf("%s: found no command that makes a password or token; the pin is not looking at anything", name)
		}
		if !strings.Contains(doc, "ArgoCD shows it only once") {
			t.Errorf("%s: no longer says the token is shown only once and must be copied right away", name)
		}
		if !strings.Contains(doc, "**Settings**") || !strings.Contains(doc, "**Generate New**") {
			t.Errorf("%s: the web page way of making the token (Settings, Accounts, sharko, Tokens, Generate New) is missing", name)
		}
	}

	firstRun := pages["first-run"]
	for _, want := range []string{
		"~/argocd-admin-password.txt",
		"~/sharko-argocd-token.txt",
		"rm -f ~/argocd-admin-password.txt ~/sharko-argocd-token.txt",
		"copy the password, then clear the terminal with `clear`",
		"Copy the token, then clear the terminal with `clear`.",
		"so it is never printed on the screen",
		"**Option 1: the ArgoCD web page.**",
		"**Option 2: the command line.**",
		"kubectl port-forward svc/argocd-server 8081:80 -n argocd --context <argocd-context>",
		"**Whichever option you used, when the wizard has accepted the token,** delete the private files:",
	} {
		if !strings.Contains(firstRun, want) {
			t.Errorf("first-run: missing the exact text %q", want)
		}
	}

	kind := pages["try-on-your-laptop"]
	for _, want := range []string{
		"> ~/sharko-admin-password.txt",
		"rm ~/sharko-admin-password.txt",
		"Clear the terminal with `clear`",
		"the next command shows it on the screen",
	} {
		if !strings.Contains(kind, want) {
			t.Errorf("kind page: missing the exact text %q", want)
		}
	}
	// The web page way comes first, the command line way second.
	if i, j := strings.Index(firstRun, "Option 1"), strings.Index(firstRun, "Option 2"); i < 0 || j < 0 || i > j {
		t.Errorf("first-run: the web page way must come before the command line way")
	}
	// The delete step is after both options, so readers of option 1 see it.
	if i, j := strings.Index(firstRun, "**Option 2"), strings.Index(firstRun, "rm -f ~/argocd-admin-password.txt"); i < 0 || j < i {
		t.Errorf("first-run: the step that deletes the private files must come after both options")
	}
}

func TestDocsEp2Take2HelmRepoAddForceUpdate(t *testing.T) {
	for _, rel := range []string{
		"docs/site/getting-started/try-on-your-laptop.md",
		"docs/site/operator/install-argocd.md",
	} {
		doc := readDocForKindPins(t, rel)
		n := 0
		for _, c := range bashCommands(doc) {
			if strings.HasPrefix(c, "helm repo add ") {
				n++
				if !strings.Contains(c, "--force-update") {
					t.Errorf("%s: helm repo add without --force-update: %s", rel, c)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: found no helm repo add line", rel)
		}
		if !strings.Contains(doc, "already exists") || !strings.Contains(doc, "fine") {
			t.Errorf("%s: no longer says in plain words that the \"already exists\" message is fine", rel)
		}
	}
}
