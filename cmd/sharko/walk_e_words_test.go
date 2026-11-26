package main

import (
	"net/http"
	"strings"
	"testing"
)

// ---- walk 9 finding 2, walk 8 L04: adopting what is already adopted ----

func TestAdopt_AlreadyAdopted_SaysSoInsteadOfWouldAdopt(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"results": []map[string]interface{}{{
				"name": "legacy-1", "status": "success", "already_done": true, "merged": true,
				"message": "legacy-1 is already adopted. Nothing to do, and no pull request was opened.",
			}},
		})
	})
	for _, flags := range []map[string]string{{"dry-run": "true"}, {"yes": "true"}} {
		out := runAdopt(t, flags, []string{"legacy-1"})
		if !strings.Contains(out, "  legacy-1: already adopted, nothing to do\n") {
			t.Errorf("flags %v: the line is missing:\n%s", flags, out)
		}
		for _, old := range []string{"would adopt", "legacy-1: adopted\n"} {
			if strings.Contains(out, old) {
				t.Errorf("flags %v: the old wording %q is back:\n%s", flags, old, out)
			}
		}
	}
}

// ---- walk 8 L02: refresh-secrets does not take a cluster name ----

func TestRefreshSecrets_GivenAName_SaysItTakesNone(t *testing.T) {
	err := refreshSecretsArgs(refreshSecretsCmd, []string{"nonexist"})
	want := "refresh-secrets does not take a cluster name. It refreshes the add-on secrets of every cluster. Run it with no name."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if err := refreshSecretsArgs(refreshSecretsCmd, nil); err != nil {
		t.Fatalf("no name must be fine: %v", err)
	}
}

// ---- walk 9 finding 6: a word from inside the code ----

func TestClusterStatusWord(t *testing.T) {
	cases := map[string]string{
		"":           "unknown",
		"not_in_git": "not managed yet",
		"Successful": "Successful",
		"missing":    "missing",
	}
	for in, want := range cases {
		if got := clusterStatusWord(in); got != want {
			t.Errorf("clusterStatusWord(%q) = %q, want %q", in, got, want)
		}
	}
	if clusterStatusWord("not_in_git") == "not_in_git" {
		t.Error("the word from inside the code is back")
	}
}

// ---- walk 1 L10: add-to-catalog for an addon that is already there ----

func TestAddToCatalog_AlreadyThere_SaysSoAndPrintsNoPullRequest(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"added":              []string{},
			"already_in_catalog": []string{"reloader"},
			"message":            "reloader is already in your catalog, with the same chart, version and namespace. Nothing to do, and no pull request was opened.",
		})
	})
	resetFlags(addToCatalogCmd)
	setFlags(t, addToCatalogCmd, map[string]string{"version": "2.2.21", "chart": "reloader", "repo": "https://stakater.github.io/stakater-charts"})
	out, err := captureStdoutT(t, func() error { return addToCatalogCmd.RunE(addToCatalogCmd, []string{"reloader"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reloader is already in your catalog, with the same chart, version and namespace. Nothing to do, and no pull request was opened.") {
		t.Errorf("the plain sentence is missing:\n%s", out)
	}
	for _, old := range []string{"added to catalog.yaml", "Review and merge", "done\n"} {
		if strings.Contains(out, old) {
			t.Errorf("old wording %q is back:\n%s", old, out)
		}
	}
}

// ---- walk 8 L03: validate said nothing was wrong after checking nothing ----

func TestValidate_FoundNeitherFile_IsNotASilentPass(t *testing.T) {
	dir := t.TempDir()
	err := validateCmd.RunE(validateCmd, []string{dir})
	want := "nothing was checked: neither configuration/addons-catalog.yaml nor configuration/managed-clusters.yaml was found. Give the folder that holds the configuration folder, not a file. For a repo in the v4 layout, use: sharko validate-config"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}
