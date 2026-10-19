package api

// argocd_https_docs_test.go — LO-7 R4 (epic video-leftovers, 2026-10-18).
//
// The ArgoCD runbook said Sharko "never" sends the token to an http address
// that sent it on to https. That is only true once the redirect was seen:
// before that, one real request can carry it (a path that only later starts
// redirecting, or a proxy that redirects only some methods or queries, which
// the token-free GET check does not see). The page now says exactly that.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArgoCDRunbook_SaysWhenTheTokenCanStillGoOverHTTP(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRootForDocsGuard(t), "docs/site/operator/argocd-upstream-unreachable.md"))
	if err != nil {
		t.Fatalf("read runbook: %v", err)
	}
	page := strings.Join(strings.Fields(string(raw)), " ")

	const old = "Sharko never sends the ArgoCD token to an `http://` address that sent it on to https."
	if strings.Contains(page, old) {
		t.Errorf("the runbook still has the old line, which is not true before the redirect is seen: %q", old)
	}
	for _, want := range []string{
		"Once an `http://` address has sent Sharko on to https, Sharko never sends the ArgoCD token to that host over plain http again.",
		"There are two cases where the token can still go out over plain http once, before Sharko sees the redirect:",
		"a path that answered over plain http when Sharko first asked, and only later starts sending Sharko on to https;",
		"a proxy that sends only some requests on to https, for example only writes (`POST`, `PUT`, `DELETE`) or only some query strings.",
		"`http://argocd.example.com/argocd` gives `https://argocd.example.com/argocd`.",
		"**Test ArgoCD** says `ArgoCD tried to send Sharko from https to plain http.`",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the runbook is missing: %q", want)
		}
	}
}
