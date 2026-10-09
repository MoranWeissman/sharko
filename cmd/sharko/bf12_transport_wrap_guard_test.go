package main

// bf12_transport_wrap_guard_test.go — BF12-2, the class rather than the line.
//
// # The class
//
// Every HTTP call in Sharko is two steps: build a request, then send it. BOTH
// steps hand back a *net/url.Error when they fail, and that type prints the
// address it was given. On the send it masks the password half of any user
// information and nothing else — a token written in the username position, the
// ordinary way a bare token is carried, comes straight back out. On the build
// it masks nothing at all.
//
// So `fmt.Errorf("...: %w", err)` on either of those errors is an address on
// whatever screen the error reaches, and the addresses in question are
// operator-supplied: the Sharko server address in the CLI config, the ArgoCD
// endpoint, a chart repository, an AI base URL.
//
// BF10 fixed one send. BF12-2 found the build two lines above it still wrapped
// raw. Fixing that one line and stopping would leave the same defect in every
// other file, so this guard walks the tree instead and holds a verdict on
// every site.
//
// # How a site is judged
//
// By TYPE, resolved with the go/packages type checker — never by the spelling
// of a variable. A site is:
//
//   - "described" — the error reaches the returned value only through a
//     function in internal/credsafe. That is the safe shape: credsafe reads
//     error types and sentinels, never a message, so nothing from the address
//     can travel.
//   - "dropped" — the error is not used at all in the branch.
//   - "raw" — anything else. The error itself, or its words, can reach the
//     caller.
//
// A "raw" site is not automatically a defect: many of them are unreachable
// from any surface a person or a third party sees. But every one of them is
// listed below WITH THE REASON, so the judgement is written down and a new one
// cannot appear quietly.
//
// # Every shape, not just the tidy one (BF13-2)
//
// The walk used to look only at assignments sitting directly in a statement
// list, and only at the "if" that followed one. Two ordinary shapes of Go fell
// outside it. A send written as
//
//	if resp, err := client.Do(req); err != nil { ... }
//
// puts its assignment in the if's own init slot, so it was never discovered at
// all — not misjudged, not reported as unlisted, and not counted, which meant
// the exact-number check below could not notice it either. And a branch that
// hands the error on from its ELSE arm was recorded as "dropped", so the guard
// got happier as the defect appeared.
//
// The walk now finds the CALL, wherever in the function it is written, and
// reads the syntax around it to decide what becomes of the error. A bare
// `return client.Do(req)` is a site (the *url.Error goes to the caller with
// the address still in it). A deferred or backgrounded call is a site whose
// verdict is "dropped", so turning one into a form that keeps the error shows
// up as a verdict change. Both arms of an if are judged, not just the body.
//
// # What makes this a list and not a count
//
// It fails when a site appears, when a listed site disappears, when a
// verdict changes, and when the total is not exactly the recorded number. A
// walk that finds nothing is fatal, not a pass.

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// bf12SweptDirs are the directories walked, relative to the repository root.
//
// cmd/playground is deliberately not here: it is a local development harness
// that never ships in the server or the CLI image, and it prints to a
// developer's own terminal. tests/ is not here for the same reason. Both are
// checked to exist all the same, so this comment cannot quietly become untrue
// after a rename.
var bf12SweptDirs = []string{"internal", "cmd/sharko"}

// bf12UnsweptButPresent are directories that DO contain HTTP calls and are
// deliberately out of the sweep. Listing them means a rename shows up here
// rather than silently shrinking what the guard covers.
var bf12UnsweptButPresent = []string{"cmd/playground", "tests"}

// bf12Verdict is what the walk concluded about one site.
type bf12Verdict string

const (
	bf12Described bf12Verdict = "described"
	bf12Dropped   bf12Verdict = "dropped"
	bf12Raw       bf12Verdict = "raw"
)

// bf12Site is one recorded judgement.
type bf12Site struct {
	verdict bf12Verdict
	// reason is required for every "raw" site: why the error's words cannot
	// reach a person, a log collector or a third party.
	reason string
}

// bf12Sites is the list. Its keys must match the walk EXACTLY.
//
// The key is "<file>::<function>::<call>#<n>", with no line number in it, so
// that ordinary edits above a site do not churn the list. The failure message
// prints the line.
var bf12Sites = map[string]bf12Site{
	// cmd/sharko/client.go:366
	"cmd/sharko/client.go::apiRequest::Client.Do": {bf12Described, ""},
	// cmd/sharko/client.go:341
	"cmd/sharko/client.go::apiRequest::NewRequest": {bf12Described, ""},
	// internal/advisories/artifacthub.go:166
	"internal/advisories/artifacthub.go::artifactHubSource.fetchPackage::Client.Do": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/advisories/artifacthub.go:160
	"internal/advisories/artifacthub.go::artifactHubSource.fetchPackage::NewRequestWithContext": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/advisories/artifacthub.go:121
	"internal/advisories/artifacthub.go::artifactHubSource.resolveRepoName::Client.Do": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/advisories/artifacthub.go:115
	"internal/advisories/artifacthub.go::artifactHubSource.resolveRepoName::NewRequestWithContext": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/advisories/artifacthub.go:223
	"internal/advisories/artifacthub.go::releaseNotesSource.Get::Client.Do": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/advisories/artifacthub.go:217
	"internal/advisories/artifacthub.go::releaseNotesSource.Get::NewRequestWithContext": {bf12Raw,
		"artifacthub.io is a literal in that file; the rest of the address is a " +
			"repository slug and a chart name from the catalog. The error goes back to " +
			"the advisories checker, which logs it - and the log sink replaces an error " +
			"value with credsafe.LogClass by type - and never into an API reply."},
	// internal/ai/agent.go:456
	"internal/ai/agent.go::Agent.callClaudeChat::Client.Do": {bf12Described, ""},
	// internal/ai/agent.go:446
	"internal/ai/agent.go::Agent.callClaudeChat::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/agent.go:620
	"internal/ai/agent.go::Agent.callCustomOpenAIChat::Client.Do": {bf12Described, ""},
	// internal/ai/agent.go:610
	"internal/ai/agent.go::Agent.callCustomOpenAIChat::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/agent.go:858
	"internal/ai/agent.go::Agent.callGeminiChat::Client.Do": {bf12Described, ""},
	// internal/ai/agent.go:849
	"internal/ai/agent.go::Agent.callGeminiChat::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/agent.go:402
	"internal/ai/agent.go::Agent.callOllamaChat::Client.Do": {bf12Described, ""},
	// internal/ai/agent.go:394
	"internal/ai/agent.go::Agent.callOllamaChat::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/agent.go:531
	"internal/ai/agent.go::Agent.callOpenAIChat::Client.Do": {bf12Described, ""},
	// internal/ai/agent.go:522
	"internal/ai/agent.go::Agent.callOpenAIChat::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:537
	"internal/ai/client.go::Client.chatClaude::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:527
	"internal/ai/client.go::Client.chatClaude::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:792
	"internal/ai/client.go::Client.chatCustomOpenAI::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:782
	"internal/ai/client.go::Client.chatCustomOpenAI::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:709
	"internal/ai/client.go::Client.chatGemini::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:700
	"internal/ai/client.go::Client.chatGemini::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:478
	"internal/ai/client.go::Client.chatOllama::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:470
	"internal/ai/client.go::Client.chatOllama::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:607
	"internal/ai/client.go::Client.chatOpenAI::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:598
	"internal/ai/client.go::Client.chatOpenAI::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:231
	"internal/ai/client.go::Client.claudeSummarize::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:221
	"internal/ai/client.go::Client.claudeSummarize::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:408
	"internal/ai/client.go::Client.customOpenAISummarize::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:398
	"internal/ai/client.go::Client.customOpenAISummarize::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:339
	"internal/ai/client.go::Client.geminiSummarize::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:330
	"internal/ai/client.go::Client.geminiSummarize::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:187
	"internal/ai/client.go::Client.ollamaSummarize::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:179
	"internal/ai/client.go::Client.ollamaSummarize::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/client.go:280
	"internal/ai/client.go::Client.openaiSummarize::Client.Do": {bf12Described, ""},
	// internal/ai/client.go:271
	"internal/ai/client.go::Client.openaiSummarize::NewRequestWithContext": {bf12Described, ""},
	// internal/ai/websearch.go:37
	"internal/ai/websearch.go::WebSearch::Client.Do": {bf12Described, ""},
	// internal/ai/websearch.go:28
	"internal/ai/websearch.go::WebSearch::NewRequestWithContext": {bf12Described, ""},
	// internal/api/catalog_project_readme.go:172
	"internal/api/catalog_project_readme.go::fetchProjectReadme::Client.Do": {bf12Dropped, ""},
	// internal/api/catalog_project_readme.go:160
	"internal/api/catalog_project_readme.go::fetchProjectReadme::NewRequestWithContext": {bf12Dropped, ""},
	// internal/api/users_me.go:235
	"internal/api/users_me.go::validateGitHubToken::Client.Do": {bf12Raw,
		"the address is the literal https://api.github.com/user, written in that " +
			"file. There is nothing operator-supplied in it for the error to reproduce."},
	// internal/api/users_me.go:227
	"internal/api/users_me.go::validateGitHubToken::NewRequestWithContext": {bf12Raw,
		"the address is the literal https://api.github.com/user, written in that " +
			"file. There is nothing operator-supplied in it for the error to reproduce."},
	// internal/argocd/client.go:514
	"internal/argocd/client.go::Client.doGet::Client.Do": {bf12Raw,
		"the error is handed to unreachableCallError, which builds Sharko's own " +
			"UnreachableError out of the verb, the endpoint and a classified code, and " +
			"drops the cause. The cause reaches only the log sink, which prints it " +
			"through credsafe.LogClass by type. See internal/argocd/unreachable.go."},
	// internal/argocd/client.go:502
	"internal/argocd/client.go::Client.doGet::NewRequestWithContext": {bf12Described, ""},
	// internal/argocd/client_write.go:176
	"internal/argocd/client_write.go::Client.doDelete::Client.Do": {bf12Raw,
		"the error is handed to unreachableCallError, which drops the cause and " +
			"returns Sharko's own facts. See internal/argocd/unreachable.go."},
	// internal/argocd/client_write.go:167
	"internal/argocd/client_write.go::Client.doDelete::NewRequestWithContext": {bf12Described, ""},
	// internal/argocd/client_write.go:114
	"internal/argocd/client_write.go::Client.doPost::Client.Do": {bf12Raw,
		"the error is handed to unreachableCallError, which drops the cause and " +
			"returns Sharko's own facts. See internal/argocd/unreachable.go."},
	// internal/argocd/client_write.go:105
	"internal/argocd/client_write.go::Client.doPost::NewRequestWithContext": {bf12Described, ""},
	// internal/argocd/client_write.go:145
	"internal/argocd/client_write.go::Client.doPut::Client.Do": {bf12Raw,
		"the error is handed to unreachableCallError, which drops the cause and " +
			"returns Sharko's own facts. See internal/argocd/unreachable.go."},
	// internal/argocd/client_write.go:136
	"internal/argocd/client_write.go::Client.doPut::NewRequestWithContext": {bf12Described, ""},
	// internal/catalog/artifacthub.go:306
	"internal/catalog/artifacthub.go::ArtifactHubClient.do::Client.Do": {bf12Raw,
		"the error is put inside an ArtifactHubError, and the one boundary that " +
			"shows that to a person - classifyAHError in " +
			"internal/api/catalog_reprobe.go - returns the fixed class string and never " +
			"the underlying text."},
	// internal/catalog/artifacthub.go:299
	"internal/catalog/artifacthub.go::ArtifactHubClient.do::NewRequestWithContext": {bf12Raw,
		"the error is put inside an ArtifactHubError, and the one boundary that " +
			"shows that to a person - classifyAHError in " +
			"internal/api/catalog_reprobe.go - returns the fixed class string and never " +
			"the underlying text."},
	// internal/catalog/scorecard.go:222
	"internal/catalog/scorecard.go::Scheduler.fetchScore::Client.Do": {bf12Raw,
		"api.scorecard.dev is a literal in that file and the owner and repository " +
			"come from the catalog. The error stays inside the background scorecard " +
			"scheduler and reaches only the log."},
	// internal/catalog/scorecard.go:217
	"internal/catalog/scorecard.go::Scheduler.fetchScore::NewRequestWithContext": {bf12Raw,
		"api.scorecard.dev is a literal in that file and the owner and repository " +
			"come from the catalog. The error stays inside the background scorecard " +
			"scheduler and reaches only the log."},
	// internal/catalog/signing/verify.go:363
	"internal/catalog/signing/verify.go::Verifier.fetchBundle::Client.Do": {bf12Raw,
		"the bundle address comes from a catalog entry's signature sidecar, so it " +
			"is catalog data rather than an operator credential. NOT traced all the way " +
			"to an API reply in the BF12-2 sweep - recorded so it is a known judgement " +
			"rather than a claim, and the next person to touch catalog signing should " +
			"close it."},
	// internal/catalog/signing/verify.go:356
	"internal/catalog/signing/verify.go::Verifier.fetchBundle::NewRequestWithContext": {bf12Raw,
		"the bundle address comes from a catalog entry's signature sidecar, so it " +
			"is catalog data rather than an operator credential. NOT traced all the way " +
			"to an API reply in the BF12-2 sweep - recorded so it is a known judgement " +
			"rather than a claim, and the next person to touch catalog signing should " +
			"close it."},
	// internal/catalog/sources/fetcher.go:945
	"internal/catalog/sources/fetcher.go::Fetcher.findSidecar::Client.Do": {bf12Dropped, ""},
	// internal/catalog/sources/fetcher.go:941
	"internal/catalog/sources/fetcher.go::Fetcher.findSidecar::NewRequestWithContext": {bf12Dropped, ""},
	// internal/catalog/sources/fetcher.go:757
	"internal/catalog/sources/fetcher.go::Fetcher.httpGet::Client.Do": {bf12Raw,
		"this is the group worth watching: a catalog source address is " +
			"operator-supplied and can carry a token. The error is stored as " +
			"SourceSnapshot.LastErr, which is read nowhere outside that package - no " +
			"handler puts it in a reply - and otherwise reaches only the log sink. If " +
			"LastErr is ever surfaced, describe these errors through credsafe first."},
	// internal/catalog/sources/fetcher.go:750
	"internal/catalog/sources/fetcher.go::Fetcher.httpGet::NewRequestWithContext": {bf12Raw,
		"this is the group worth watching: a catalog source address is " +
			"operator-supplied and can carry a token. The error is stored as " +
			"SourceSnapshot.LastErr, which is read nowhere outside that package - no " +
			"handler puts it in a reply - and otherwise reaches only the log sink. If " +
			"LastErr is ever surfaced, describe these errors through credsafe first."},
	// internal/catalog/sources/fetcher.go:881
	"internal/catalog/sources/fetcher.go::Fetcher.httpGetPinned::Client.Do": {bf12Raw,
		"this is the group worth watching: a catalog source address is " +
			"operator-supplied and can carry a token. The error is stored as " +
			"SourceSnapshot.LastErr, which is read nowhere outside that package - no " +
			"handler puts it in a reply - and otherwise reaches only the log sink. If " +
			"LastErr is ever surfaced, describe these errors through credsafe first."},
	// internal/catalog/sources/fetcher.go:874
	"internal/catalog/sources/fetcher.go::Fetcher.httpGetPinned::NewRequestWithContext": {bf12Raw,
		"this is the group worth watching: a catalog source address is " +
			"operator-supplied and can carry a token. The error is stored as " +
			"SourceSnapshot.LastErr, which is read nowhere outside that package - no " +
			"handler puts it in a reply - and otherwise reaches only the log sink. If " +
			"LastErr is ever surfaced, describe these errors through credsafe first."},
	// internal/gitprovider/azuredevops.go:55
	"internal/gitprovider/azuredevops.go::AzureDevOpsProvider.doGet::Client.Do": {bf12Described, ""},
	// internal/gitprovider/azuredevops.go:43
	"internal/gitprovider/azuredevops.go::AzureDevOpsProvider.doGet::NewRequest": {bf12Described, ""},
	// internal/gitprovider/azuredevops.go:89
	"internal/gitprovider/azuredevops.go::AzureDevOpsProvider.doRequest::Client.Do": {bf12Described, ""},
	// internal/gitprovider/azuredevops.go:80
	"internal/gitprovider/azuredevops.go::AzureDevOpsProvider.doRequest::NewRequest": {bf12Described, ""},
	// internal/helm/fetcher.go:300
	"internal/helm/fetcher.go::Fetcher.FetchValues::Client.Do": {bf12Described, ""},
	// internal/helm/fetcher.go:293
	"internal/helm/fetcher.go::Fetcher.FetchValues::NewRequestWithContext": {bf12Described, ""},
	// internal/helm/fetcher.go:379
	"internal/helm/fetcher.go::Fetcher.fetchChartYAML::Client.Do": {bf12Described, ""},
	// internal/helm/fetcher.go:372
	"internal/helm/fetcher.go::Fetcher.fetchChartYAML::NewRequestWithContext": {bf12Described, ""},
	// internal/helm/fetcher.go:521
	"internal/helm/fetcher.go::Fetcher.fetchGitHubRelease::Client.Do": {bf12Described, ""},
	// internal/helm/fetcher.go:511
	"internal/helm/fetcher.go::Fetcher.fetchGitHubRelease::NewRequestWithContext": {bf12Described, ""},
	// internal/helm/fetcher.go:121
	"internal/helm/fetcher.go::Fetcher.getIndex::Client.Do": {bf12Described, ""},
	// internal/helm/fetcher.go:107
	"internal/helm/fetcher.go::Fetcher.getIndex::NewRequestWithContext": {bf12Described, ""},
	// internal/orchestrator/chartprobe.go:179
	//
	// This one was invisible until BF13-2. It was written as a bare
	// `return client.Do(req)`, and the old walk only ever looked at
	// assignments, so the send was never discovered, never listed and never
	// counted. It is now described like the rest of the file.
	"internal/orchestrator/chartprobe.go::doManifestRequest::Client.Do": {bf12Described, ""},
	// internal/orchestrator/chartprobe.go:140
	//
	// These four were recorded as raw with the reason "the probe turns the
	// error into a fixed reachability verdict for its caller". That reason
	// was not true. chartRegistryUnreachableError prints the cause with %v,
	// CollectBootstrapFiles returns that sentence, and internal/api/init.go
	// puts its text into the init operation the browser polls — so the
	// address inside a *url.Error reached a person's screen. All four now
	// describe the failure through credsafe instead.
	"internal/orchestrator/chartprobe.go::doManifestRequest::NewRequestWithContext": {bf12Described, ""},
	// internal/orchestrator/chartprobe.go:223
	"internal/orchestrator/chartprobe.go::fetchAnonymousToken::Client.Do": {bf12Described, ""},
	// internal/orchestrator/chartprobe.go:218
	"internal/orchestrator/chartprobe.go::fetchAnonymousToken::NewRequestWithContext": {bf12Described, ""},
}

// bf12MinimumSites is the exact number of construction and transport sites in
// the swept tree, compared with != so it fails in both directions. It is here
// to make a silent collapse of the walk impossible, not to approve of any
// particular number.
//
// It was 79 while the walk only looked at assignments sitting directly in a
// statement list. That number could not move when a site was written in a
// shape the walk did not look at, because it only ever counted what the walk
// found — so a whole syntactic form could sit outside the guard and every
// assertion here would still pass. BF13-2 widened the walk to find the call
// itself wherever it is written, and one more site came into view.
const bf12MinimumSites = 80

func TestEveryRequestBuildAndSendIsJudged(t *testing.T) {
	root := bf12RepoRoot(t)
	for _, dir := range bf12SweptDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("swept directory %q does not exist — the guard covers less than it claims: %v", dir, err)
		}
	}
	for _, dir := range bf12UnsweptButPresent {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Errorf("%q is recorded as deliberately unswept but no longer exists — "+
				"update the comment above bf12SweptDirs", dir)
		}
	}

	found := bf12Discover(t, root)

	if len(found) == 0 {
		t.Fatal("the walk found no request-building or request-sending site at all. " +
			"Sharko makes HTTP calls, so this is a broken walk and every assertion " +
			"below would pass vacuously.")
	}

	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var unlisted []string
	for _, k := range keys {
		recorded, ok := bf12Sites[k]
		if !ok {
			unlisted = append(unlisted, "\t"+bf12Quote(k)+": {"+string(found[k].verdict)+", \"\"},  // "+found[k].where)
			continue
		}
		if recorded.verdict != found[k].verdict {
			t.Errorf("%s (%s) is recorded as %q but the code now says %q — "+
				"say why the change is right and update the list",
				k, found[k].where, recorded.verdict, found[k].verdict)
		}
		if found[k].verdict == bf12Raw && strings.TrimSpace(recorded.reason) == "" {
			t.Errorf("%s (%s) hands the error's own words on and has no reason recorded. "+
				"Either describe the error through internal/credsafe, or write down why "+
				"its words cannot reach a person, a log collector or a third party.",
				k, found[k].where)
		}
	}
	if len(unlisted) > 0 {
		t.Errorf("%d request site(s) are not judged. Every one builds or sends an HTTP "+
			"request, and both steps hand back a *url.Error that prints the address. "+
			"Add each to bf12Sites with a verdict and, for a raw one, a reason:\n\n%s",
			len(unlisted), strings.Join(unlisted, "\n"))
	}

	for k := range bf12Sites {
		if _, ok := found[k]; !ok {
			t.Errorf("stale entry %q is judged here but the walk no longer finds it", k)
		}
	}

	if len(found) != bf12MinimumSites {
		t.Errorf("the swept tree has %d request sites, this guard was written against "+
			"exactly %d — update the list above and this number together",
			len(found), bf12MinimumSites)
	}
}

// ---------------------------------------------------------------------------
// The walk.
// ---------------------------------------------------------------------------

// bf12Found is what the walk concluded about one site, plus where it is.
type bf12Found struct {
	verdict bf12Verdict
	where   string // "file:line"
}

// bf12Quote renders a key as a Go string literal for the paste-ready message.
func bf12Quote(s string) string { return "\"" + s + "\"" }

func bf12RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the repository root (no go.mod above the test's working " +
		"directory) — the guard would sweep nothing")
	return ""
}

// bf12Discover type-checks the tree and reports every place a request is built
// or sent, with what the branch below it does with the error.
func bf12Discover(t *testing.T, root string) map[string]bf12Found {
	t.Helper()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: root,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("type-checking the module: %v", err)
	}

	found := map[string]bf12Found{}
	seen := map[string]int{}

	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.TypesInfo == nil || len(p.Syntax) == 0 {
			return
		}
		for _, file := range p.Syntax {
			pos := p.Fset.Position(file.Pos())
			rel, relErr := filepath.Rel(root, pos.Filename)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			if strings.HasSuffix(rel, "_test.go") {
				continue
			}
			if !bf12InSweep(rel) {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				bf12ScanFunc(p, rel, bf12FuncName(fn), fn.Body, found, seen)
			}
		}
	})
	return found
}

func bf12InSweep(rel string) bool {
	for _, dir := range bf12SweptDirs {
		if rel == dir || strings.HasPrefix(rel, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func bf12FuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return bf12TypeLabel(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func bf12TypeLabel(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return bf12TypeLabel(v.X)
	case *ast.Ident:
		return v.Name
	case *ast.IndexExpr:
		return bf12TypeLabel(v.X)
	}
	return "?"
}

// bf12ScanFunc finds every request build and every request send anywhere in a
// function body, whatever shape it is written in, and records what happens to
// the error.
//
// It used to walk statement LISTS and look only at assignments sitting
// directly in one. That missed a whole ordinary Go form:
//
//	if resp, err := client.Do(req); err != nil { return fmt.Errorf("%w", err) }
//
// The assignment there lives in the if statement's own init slot, not in any
// block's list, so the site was not misjudged — it was never seen at all. It
// did not show up as unlisted and it did not move the exact-count tripwire
// either, because the count only ever counted what the walk found.
//
// So the walk now finds the CALL itself, wherever it sits, and works out from
// the syntax around it what becomes of the error. Every call is judged; there
// is no shape that quietly falls outside.
func bf12ScanFunc(p *packages.Package, rel, fnName string, body ast.Node,
	found map[string]bf12Found, seen map[string]int) {

	var stack []ast.Node
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)

		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		label := bf12CallLabel(p, call)
		if label == "" {
			return true
		}

		key := rel + "::" + fnName + "::" + label
		seen[key]++
		if n := seen[key]; n > 1 {
			key = key + "#" + bf12Itoa(n)
		}
		found[key] = bf12Found{
			verdict: bf12JudgeSite(p, stack),
			where:   rel + ":" + bf12Itoa(p.Fset.Position(call.Pos()).Line),
		}
		return true
	})
}

// bf12JudgeSite decides the verdict for one call, given the chain of nodes
// from the function body down to the call itself (the call is the last entry).
func bf12JudgeSite(p *packages.Package, stack []ast.Node) bf12Verdict {
	// Anything between the call and its statement that is itself a call into
	// internal/credsafe means the error never leaves this line as itself.
	stmtIdx := -1
	for i := len(stack) - 2; i >= 0; i-- {
		if _, ok := stack[i].(ast.Stmt); ok {
			stmtIdx = i
			break
		}
	}
	if stmtIdx < 0 {
		return bf12Raw
	}
	for i := stmtIdx + 1; i < len(stack)-1; i++ {
		if c, ok := stack[i].(*ast.CallExpr); ok && bf12IsCredsafeCall(p, c) {
			return bf12Described
		}
	}

	switch stmt := stack[stmtIdx].(type) {
	case *ast.DeferStmt, *ast.GoStmt, *ast.ExprStmt:
		// Nothing takes the results, so the error cannot be handed on. A
		// deferred or backgrounded send is discovered all the same, so that
		// changing one of these into a form that DOES keep the error shows up
		// here as a verdict change rather than as nothing.
		return bf12Dropped

	case *ast.ReturnStmt:
		// `return client.Do(req)` — the call's own results become the
		// function's results, so the *url.Error travels to the caller
		// untouched, with the address still inside it. Nothing on this line
		// describes it, so this is raw. It reaches here only when no credsafe
		// call was found above.
		return bf12Raw

	case *ast.AssignStmt:
		errName := bf12ErrorName(p, stmt)
		if errName == "" {
			// Assigned to _ , or the error result is not kept at all.
			return bf12Dropped
		}
		return bf12JudgeAfterAssign(p, stack, stmtIdx, stmt, errName)
	}
	return bf12Raw
}

// bf12JudgeAfterAssign works out which piece of syntax tests the error that
// was just assigned, and judges what that piece does with it.
func bf12JudgeAfterAssign(p *packages.Package, stack []ast.Node, stmtIdx int,
	assign *ast.AssignStmt, errName string) bf12Verdict {

	if stmtIdx == 0 {
		return bf12Raw
	}
	switch parent := stack[stmtIdx-1].(type) {

	case *ast.IfStmt:
		// if resp, err := client.Do(req); err != nil { ... } else { ... }
		// The assignment is the if's own init. Judge both arms: an else arm
		// that wraps the error raw is exactly as bad as a body that does, and
		// judging only the body made the guard report "dropped" — the error is
		// not used at all — for a branch that hands the whole address on.
		if parent.Init == ast.Stmt(assign) && bf12ChecksErr(parent.Cond, errName) {
			return bf12JudgeBranch(p, errName, parent.Body, parent.Else)
		}

	case *ast.ForStmt:
		// for resp, err := client.Do(req); err != nil; ... { ... }
		// The init is not repeated, so this is a one-shot send whose error is
		// read by the condition and the body. Init is left out of the judging
		// because the assignment itself names err.
		if parent.Init == ast.Stmt(assign) {
			return bf12JudgeBranch(p, errName, parent.Body, parent.Post)
		}

	case *ast.SwitchStmt:
		// switch resp, err := client.Do(req); { case err != nil: ... }
		if parent.Init == ast.Stmt(assign) {
			return bf12JudgeBranch(p, errName, parent.Body)
		}

	case *ast.TypeSwitchStmt:
		if parent.Init == ast.Stmt(assign) {
			return bf12JudgeBranch(p, errName, parent.Body)
		}

	case *ast.BlockStmt:
		return bf12JudgeFollowing(p, parent.List, assign, errName)
	case *ast.CaseClause:
		return bf12JudgeFollowing(p, parent.Body, assign, errName)
	case *ast.CommClause:
		return bf12JudgeFollowing(p, parent.Body, assign, errName)
	}
	return bf12Raw
}

// bf12JudgeFollowing handles the plain two-line form: the assignment, then an
// if that tests the error on the very next line.
func bf12JudgeFollowing(p *packages.Package, stmts []ast.Stmt,
	assign *ast.AssignStmt, errName string) bf12Verdict {

	for i, s := range stmts {
		if s != ast.Stmt(assign) {
			continue
		}
		if i+1 < len(stmts) {
			if ifStmt, ok := stmts[i+1].(*ast.IfStmt); ok && bf12ChecksErr(ifStmt.Cond, errName) {
				return bf12JudgeBranch(p, errName, ifStmt.Body, ifStmt.Else)
			}
		}
		return bf12Raw
	}
	return bf12Raw
}

// bf12CallLabel names the call when it is one of the three that hand back a
// *url.Error, and "" otherwise. It resolves the callee through the type
// checker; the spelling of the receiver is never consulted.
func bf12CallLabel(p *packages.Package, call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	obj, _ := p.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
	if obj == nil {
		return ""
	}
	sig, _ := obj.Type().(*types.Signature)

	if recv := sig.Recv(); recv != nil {
		if obj.Name() != "Do" {
			return ""
		}
		named := bf12NamedOf(recv.Type())
		if named == nil || named.Obj().Pkg() == nil {
			return ""
		}
		if named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "Client" {
			return "Client.Do"
		}
		return ""
	}

	if obj.Pkg() == nil || obj.Pkg().Path() != "net/http" {
		return ""
	}
	switch obj.Name() {
	case "NewRequest", "NewRequestWithContext":
		return obj.Name()
	}
	return ""
}

func bf12NamedOf(t types.Type) *types.Named {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

// bf12ErrorName returns the name the error result was assigned to, or "" when
// it was discarded. The result is identified by its TYPE.
func bf12ErrorName(p *packages.Package, assign *ast.AssignStmt) string {
	errIface := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		t := p.TypesInfo.TypeOf(ident)
		if t == nil {
			continue
		}
		if types.Implements(t, errIface) {
			return ident.Name
		}
	}
	return ""
}

func bf12ChecksErr(cond ast.Expr, errName string) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == errName {
			found = true
		}
		return true
	})
	return found
}

// bf12JudgeBranch decides what the given pieces of syntax do with the error,
// counting every mention of it and how many of those mentions are inside a
// call into internal/credsafe.
//
// It takes a list of nodes rather than one block because an if statement has
// two arms and both of them get to hand the error on. A nil node is skipped,
// so a missing else costs nothing.
func bf12JudgeBranch(p *packages.Package, errName string, nodes ...ast.Node) bf12Verdict {
	total := 0
	safe := 0

	var walk func(n ast.Node, insideCredsafe bool)
	walk = func(n ast.Node, insideCredsafe bool) {
		if n == nil {
			return
		}
		switch v := n.(type) {
		case *ast.Ident:
			if v.Name == errName {
				total++
				if insideCredsafe {
					safe++
				}
			}
			return
		case *ast.CallExpr:
			nowInside := insideCredsafe || bf12IsCredsafeCall(p, v)
			walk(v.Fun, insideCredsafe)
			for _, a := range v.Args {
				walk(a, nowInside)
			}
			return
		}
		for _, child := range bf12Children(n) {
			walk(child, insideCredsafe)
		}
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		walk(n, false)
	}

	if total == 0 {
		return bf12Dropped
	}
	if safe == total {
		return bf12Described
	}
	return bf12Raw
}

// bf12IsCredsafeCall reports whether a call resolves to a function in
// internal/credsafe.
func bf12IsCredsafeCall(p *packages.Package, call *ast.CallExpr) bool {
	var ident *ast.Ident
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		ident = fun.Sel
	case *ast.Ident:
		ident = fun
	default:
		return false
	}
	fn, _ := p.TypesInfo.ObjectOf(ident).(*types.Func)
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	return strings.HasSuffix(fn.Pkg().Path(), "/internal/credsafe")
}

// bf12Children returns the child nodes of n, using ast.Inspect one level deep.
func bf12Children(n ast.Node) []ast.Node {
	var out []ast.Node
	first := true
	ast.Inspect(n, func(c ast.Node) bool {
		if first {
			first = false
			return true
		}
		if c != nil {
			out = append(out, c)
		}
		return false
	})
	return out
}

func bf12Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
