package serverrender

// bf12_banned_wording_test.go — a few sentences about the address rule are
// banned from the chart and the documentation, and this is what keeps them out.
//
// # The first banned sentence, and why banning is the right shape
//
// The chart refuses an address only when the user information sits in the host
// part of it. An "@" in the PATH is ordinary and always installs, as
// https://github.com/org/repo@v1 does. During the BF12 closure pass the
// documentation described the rule as refusing "anything before an" @, which
// promised a stricter chart than the one that ships, and the same wrong
// sentence was copied into four places before anybody noticed.
//
// So the wording is banned outright rather than corrected once. This project
// has repeatedly fixed a sentence in one file while an identical wrong one
// survived in another, and a reviewer reading the corrected copy has no way to
// see the survivor.
//
// The walk is a walk, not a list of files: a list goes stale the day somebody
// adds a page, which is the same failure in a different shape. Reading nothing
// is a failure, not a pass.
//
// # What BF13-4 changed here, and why it had to
//
// The control on this sweep used to be this:
//
//	planted := "the chart refuses " + bannedAddressWording + " `@`"
//	if !strings.Contains(strings.ToLower(planted), bannedAddressWording) {
//
// which proves that strings.Contains works. It proves nothing whatever about
// the walk. If the walk had descended into no directory, or read no file, or
// skipped every extension, that control would still have passed and the sweep
// would have reported the whole tree clean — and "clean" from a sweep that
// read nothing looks exactly like "clean" from a sweep that read everything.
// This is the same defect that was repaired elsewhere in BF12-4 and then put
// back in BF12-5, so it is not a hypothetical way for it to return.
//
// The control now plants a real file, holding the real banned sentences, into
// a directory the real walk really descends into, and requires the real walk
// to COME BACK WITH THEM. Only then is the same walk trusted to say the tree
// is clean. The planting, the finding and the removal all live inside
// sweepProvenToFindAPlantedCopy, which is also the only way to reach the sweep
// at all, so the control cannot be dropped without deleting the sweep with it.
// The planted file is removed on the way out and again by a cleanup that runs
// even when the test fails.
//
// # What BF13-5 changed here, and why it had to
//
// Two more sentences are banned now, both of them claims about how far the
// chart's copy of the address rule can be trusted:
//
//   - that the chart's rule IS internal/credsafe.ClassifyAddress. It is a
//     second copy of the same decision, and the two are not identical: they
//     read what is inside square brackets differently, so "[::1::2]:80" is
//     credential-free to the chart and unclassifiable to Go. Nothing in that
//     difference can carry a credential, but a sentence saying the two are one
//     rule is still a sentence nobody can rely on.
//   - that the day the two stop agreeing is the day a test fails. The tests
//     compare the two copies over two written-down lists. A disagreement about
//     an address in neither list turns nothing red, which is exactly how this
//     class of bug stayed invisible twice already.
//
// Both of those sentences were WRAPPED across a line break where they shipped,
// and the sweep used to read one line at a time, so it could not have found
// either of them. A ban the sweep cannot enforce is worse than no ban, so the
// reading changed with the ban: a file is now flattened into one run of words
// first, with line breaks, indentation and leading comment markers taken out,
// and the phrases are looked for in that. The planted control writes every
// banned phrase broken across a line break, with a comment marker on the
// second half, so the flattening itself is what the control proves.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannedWordings are the sentences that must not be anywhere a person reads.
//
// Each one is assembled from its words so that this file is not itself a copy
// of the thing it bans. They are matched against flattened text, so the words
// have to be written here the way they are read, one space apart.
var bannedWordings = []string{
	strings.Join([]string{"anything", "before", "an"}, " "),
	strings.Join([]string{"the", "same", "rule", "as", "internal/credsafe"}, " "),
	strings.Join([]string{"is", "the", "day", "a", "test", "fails"}, " "),
	// BANNERS-V4, 2026-08-26. The pre-release banners across README.md,
	// SECURITY.md, GOVERNANCE.md and the docs/site install pages all told the
	// reader to hold off — "don't install yet", "wait for v4", "no release to
	// install" — some of it wrapped in a markdown admonition title, some of it
	// inside a backtick around `v4`. The product owner ruled that meaning out:
	// the reader must be told what to do (install only published v4.0.0+
	// artifacts, v3 and earlier stay retired, not for production), never to
	// wait. These four are the phrases that actually shipped, banned so a
	// future edit cannot bring the "hold off" framing back under a new banner.
	strings.Join([]string{"don't", "install", "yet"}, " "),
	strings.Join([]string{"do", "not", "install", "yet"}, " "),
	strings.Join([]string{"wait", "for", "v4"}, " "),
	strings.Join([]string{"no", "release", "to", "install"}, " "),
	// TAGPREP-DOCS, 2026-08-26. The "published chart still installs v3.0.0"
	// family and the release-notes "not tagged yet ... written ahead of the
	// tag" wording both go false the moment the v4 chart publishes or the tag
	// is cut. Both were replaced with state-independent wording that reads
	// true before and after. Banning "chart still installs" (not the bare
	// "still installs") keeps this clear of docs/site/operator/git-native-config.md's
	// unrelated address-shape sentence, which legitimately uses "still installs".
	strings.Join([]string{"chart", "still", "installs"}, " "),
	strings.Join([]string{"written", "ahead", "of", "the", "tag"}, " "),
	strings.Join([]string{"not", "tagged", "yet"}, " "),
	// TAGPREP-DOCS amendment, 2026-08-26. The first commit's replacement
	// sentence — "any chart version below v4.0.0 installs the retired,
	// unsupported v3 line" — was itself wrong: a v2 chart installs the v2
	// line, not the v3 line. The product owner ruled it out even though the
	// safety advice (don't install below v4.0.0) was right. Replaced with a
	// version-line-agnostic sentence: "do not install any Sharko chart
	// version below v4.0.0, all earlier release lines are retired and
	// unsupported."
	strings.Join([]string{"installs", "the", "retired", "unsupported", "v3", "line"}, " "),
	// TAGCLOSE-DOCS, 2026-08-27. "v3.0.0, the only public release so far, and
	// every earlier tag remain retired and unsupported" named v3.0.0 as the
	// only public release, which stops being true the moment a later line
	// ships. Replaced with "v3.0.0 and earlier remain retired and
	// unsupported," which stays true regardless of what else gets released.
	strings.Join([]string{"only", "public", "release", "so", "far"}, " "),
	// TRUTHCLOSE-DOCS, 2026-08-27. The owner found seven more places still
	// telling the reader "hold off" or "nothing to install" after the v4.0.0
	// technical-preview line was ruled the durable status. Checked against
	// the whole extended corpus (this sweep's own roots, normalized) with
	// zero collisions, so none of these needed narrowing: docs/ historical
	// pages like design-history.md do not use this exact wording.
	strings.Join([]string{"no", "supported", "release"}, " "),
	strings.Join([]string{"is", "in", "active", "development"}, " "),
	strings.Join([]string{"is", "in", "development", "on"}, " "),
	strings.Join([]string{"nothing", "to", "install"}, " "),
	strings.Join([]string{"not", "proven", "by", "a", "release", "yet"}, " "),
	strings.Join([]string{"waiting", "on", "moran's", "word", "for", "the"}, " "),
	// V401-RECOVERY, 2026-08-28. The v4.0.0 tag was cut but its release gate
	// failed, so no v4.0.0 artifact was ever published and none ever will be.
	// Every install and support page that told the reader to install only
	// published v4.0.0-or-later artifacts was naming a floor nobody can
	// reach; they now say v4.0.1. Banned so the old floor cannot drift back.
	//
	// Three words, no comma: wordEdgeTrim strips backticks and punctuation
	// off each word's edges, so a stored phrase carrying its own comma would
	// never match the corpus. The corpus wrote this both with backticks
	// around the version and with a comma after "artifacts", and both reach
	// the comparison as these three plain words.
	//
	// Checked before adding: zero places in the swept roots — and zero in any
	// tracked file of any type — still carry this phrase, so it collides with
	// nothing. The historical record in docs/site/release-notes.md says
	// v4.0.0 was tagged but never published, which is a different sentence.
	// Note this guard cannot protect .goreleaser.yaml's release header, which
	// is not under wordingSweptRoots; releasesurface_meta_test.go pins that.
	strings.Join([]string{"published", "v4.0.0-or-later", "artifacts"}, " "),
	// V401-RECOVERY amendment 2, 2026-08-28. The owner ruled that a sentence
	// describing the CURRENT state must name no patch version at all, because
	// one that does goes stale at the next patch — which is the exact failure
	// this whole closure has been chasing. Every "Sharko v4.0.0 is a technical
	// preview" and "Sharko v4.0.0 is the technical-preview release line" now
	// reads "Sharko v4 is ...", which stays true for the whole line and needs
	// no edit at v4.0.2. These are the two shapes that actually shipped,
	// banned so neither can drift back.
	//
	// Both stored phrases are comma-free, so wordEdgeTrim's edge trimming
	// reaches them from corpus text written with backticks around the version
	// or with punctuation after it. "v4.0.0" survives the trim because its
	// dots are internal, not on the edges.
	//
	// DELIBERATELY NOT the shorter stem "sharko v4.0.0 is", even though it is
	// collision-free today and would cover both shapes at once. That stem
	// would also forbid an honest present-tense sentence about the failed tag
	// — "Sharko v4.0.0 is the tag that was never published" — and the v4.0.0
	// release-notes entry, the PRD and the design records all have to be able
	// to say what happened. A ban that forces a historical record to lie is
	// worse than no ban, so this stays narrow on purpose.
	//
	// Checked before adding, by reproducing flattenForWording exactly: zero
	// collisions for either phrase in the swept roots, and zero across every
	// tracked file of any type.
	strings.Join([]string{"sharko", "v4.0.0", "is", "a", "technical", "preview"}, " "),
	strings.Join([]string{"sharko", "v4.0.0", "is", "the", "technical-preview", "release", "line"}, " "),
	// V401-RECOVERY amendment 2, second pass, 2026-08-28. The first pass moved
	// the thirteen current-state openings to "Sharko v4 is ..." but left two
	// more in docs/site/release-notes.md reading "Sharko v4.0.1 is a technical
	// preview" — the same mistake one patch along, written by the same hand
	// that was cleaning it up. The owner's ruling is that the generic wording
	// applies to ALL current-state locations, so both now read "Sharko v4 is
	// ...", and the shape that shipped is banned here too.
	//
	// Same narrowness rule as above, and for the same reason: NOT the stem
	// "sharko v4.0.1 is", which is collision-free today but would forbid a
	// perfectly good future sentence like "Sharko v4.0.1 is the first
	// published version of the v4 line" — a statement of fact once it is one.
	//
	// Honest limitation, written down because the next person will hit it:
	// bannedWordings is a list of literal phrases, so it cannot express the
	// actual rule, which is "a current-state sentence names no patch version
	// at all". It only catches the openings that really shipped. If a v4.0.2
	// opening ever appears, this list will not stop it — a reviewer has to.
	//
	// Checked before adding, by reproducing flattenForWording exactly: zero
	// collisions in the swept roots and zero across every tracked file of any
	// type.
	strings.Join([]string{"sharko", "v4.0.1", "is", "a", "technical", "preview"}, " "),
	// V401-RECOVERY amendment 4, 2026-08-28. A reviewer proved a hole in the
	// pair above: the v4.0.0 entries cover BOTH shapes, but v4.0.1 only had
	// the banner one, and the release-line shape is what 5 of the 15 sites
	// actually use. Planting "Sharko v4.0.1 is the technical-preview release
	// line" in CONTRIBUTING.md walked straight past the sweep while the
	// v4.0.0 spelling in the same place was caught. Closing it.
	strings.Join([]string{"sharko", "v4.0.1", "is", "the", "technical-preview", "release", "line"}, " "),
	// INSTALL-PATHS, 2026-08-29. Every documented way to install the CLI was
	// broken, and one of them was dangerous rather than merely useless:
	//
	//   - "go install github.com/MoranWeissman/sharko/cmd/sharko@latest"
	//     silently installed v1.0.0. go.mod declares the module with no /vN
	//     suffix, which Go requires from major version 2 up, so the module proxy
	//     has never seen v2, v3 or v4 and @latest resolves inside the v1 line.
	//     That build predates the ArgoCD TLS fix — the exact defect that makes
	//     v3.0.0 retired and unsafe — and the reader got it with no error at all.
	//   - "brew install moranweissman/tap/sharko" pointed at a tap that does not
	//     exist and never has. Both spellings of the tap repository return 404,
	//     and .goreleaser.yaml has no brew section to publish a formula from.
	//   - the version-less archive names. goreleaser's name_template is
	//     "sharko_{{ .Version }}_{{ .Os }}_{{ .Arch }}", so a filename with no
	//     version in it cannot resolve for any release that has ever been cut.
	//
	// The go install phrase is stored WITHOUT the @latest suffix on purpose.
	// Phrases are matched as substrings of the flattened text, so the shorter
	// stem also catches @v1.0.0 and every other pin — all of them inside the v1
	// line, all of them below the v4.0.1 floor. It does NOT catch the path the
	// /v4 module migration will create, because "sharko/v4/cmd/sharko" does not
	// contain "sharko/cmd/sharko", so this ban cannot stand in the way of the
	// real fix when that lands.
	//
	// Two different failures need two different bans, which is why both the
	// filenames and the URL prefix are here:
	//
	//   - a version-less filename under a tagged URL is a 404. All four
	//     published platform names are banned, not only the linux/amd64 one that
	//     shipped, because the page now documents all four and a later edit could
	//     drop the version from any of them.
	//   - a VERSIONED filename under releases/latest/download/ is worse: it
	//     resolves while that release is the latest one and breaks at the next
	//     release, so it passes review and fails in front of a user.
	//     "latest/download/sharko" catches that, and because the stem stops
	//     before the separator it also catches the dashed sharko-linux-amd64
	//     shape the GitHub Actions example under templates/ was using — a name
	//     that has never been a release asset in any form.
	//
	// DELIBERATELY NOT banning "releases/latest/download" on its own.
	// latest/download/checksums.txt genuinely resolves, because checksums.txt is
	// the one release asset with no version in its name, so a ban on the bare
	// prefix would forbid a command that works. The archive names are the
	// problem, so only they are named.
	//
	// Honest limitation: this list holds literal phrases, so it cannot express
	// the actual rule, which is "an install command must name a version that
	// exists and must be able to reach v4.0.1 or later". It catches the shapes
	// that really shipped. A newly invented broken shape still needs a reviewer.
	//
	// Checked before adding, by reproducing flattenForWording exactly over every
	// tracked file of every type: the only hits for any of these seven phrases
	// were the two files the same change fixes, docs/site/cli/overview.md and
	// templates/examples/validate-action.yaml. Nothing else in the repository
	// carries them, including the historical records under docs/design.
	strings.Join([]string{"go", "install", "github.com/moranweissman/sharko/cmd/sharko"}, " "),
	strings.Join([]string{"brew", "install", "moranweissman/tap/sharko"}, " "),
	strings.Join([]string{"sharko", "linux", "amd64.tar.gz"}, "_"),
	strings.Join([]string{"sharko", "linux", "arm64.tar.gz"}, "_"),
	strings.Join([]string{"sharko", "darwin", "amd64.tar.gz"}, "_"),
	strings.Join([]string{"sharko", "darwin", "arm64.tar.gz"}, "_"),
	strings.Join([]string{"latest", "download", "sharko"}, "/"),
	// CREDS-CLAIM, 2026-08-29. "No credentials on developer laptops" was false,
	// and it was a SECURITY claim, which is the worst kind of sentence to get
	// wrong: a reader who believes it will not protect a file that is worth
	// protecting.
	//
	// What the code actually does, read out of cmd/sharko/client.go rather than
	// taken from anybody's summary: SharkoConfig has exactly two fields, server
	// and token. sharko login POSTs to /api/v1/auth/login and, only after that
	// succeeds, saves the returned session token into ~/.sharko/config at mode
	// 0600 inside a 0700 directory. Every later command reads it back and sends
	// it as "Authorization: Bearer". So the laptop holds a live credential — one
	// that is enough on its own to act as that user against the server for the
	// whole 24-hour session lifetime.
	//
	// The true statement has two halves and the corrected copy states both: the
	// PLATFORM credentials (ArgoCD token, Git token, secrets-provider access)
	// stay server-side, and the session token does not.
	//
	// The stored phrase carries "developer" on purpose, and that one word is
	// what keeps this ban narrow enough to be correct. The shorter stem
	// "credentials on developer laptops" also matches
	// docs/site/architecture/overview.md's "No ArgoCD tokens, Git tokens, or AWS
	// credentials on developer laptops" — an enumeration of platform credentials
	// only, which is TRUE and has to stay sayable. Banning the enumeration would
	// force an accurate sentence out of the docs, which is the failure this
	// guard exists to prevent, pointed the other way.
	//
	// Honest limitation, in the same spirit as the notes above: this is a
	// literal phrase, not the rule. The rule is "a sentence about where
	// credentials live must say which credentials it means". Nearby shapes that
	// this ban does NOT catch exist today in an unreferenced historical vision
	// record, docs/sharko-framework-vision.md — "No credentials on laptops" at
	// :211 and "Nobody stores credentials on their laptop" at :88 and :451.
	// Those are left alone deliberately: that file is a brainstorming record of
	// what was once intended, not a current-state page, and a ban that forces a
	// historical record to be rewritten is the thing this file has repeatedly
	// refused to do. If that page is ever promoted back into live documentation,
	// its sentences need this same treatment first.
	//
	// Checked before adding, by reproducing flattenForWording exactly over all
	// 2780 tracked files of every type: three hits, all of them the identical
	// false claim, all three fixed by this same change — README.md:153,
	// docs/architecture.md:35 and docs/site/cli/overview.md:7. Nothing else in
	// the repository carries it, including the records under docs/design and
	// .bmad.
	strings.Join([]string{"no", "credentials", "on", "developer", "laptops"}, " "),
	// PRESENTATION-CLEANUP, 2026-08-30. Sharko's front door read like a
	// security-review diary. The limitations page told the reader a technical
	// preview was safe enough to install somewhere that does not matter and to
	// experiment with; README.md and docs/site/index.md both opened on a list
	// of old defect categories; and the operator install page called its own
	// main recipe a production install directly under a callout forbidding
	// production use. All of that text is gone now. These five phrases stop the
	// retired shapes drifting back under a new heading.
	//
	// Two of the six things this guard now covers are deliberately NOT in this
	// list, and the reason is written out beside each test at the bottom of
	// this file. The "no known credential leaks, permission bypasses"
	// paragraph is legitimate history that has to stay sayable on
	// docs/site/developer-guide/security-review-history.md, which is inside
	// wordingSweptRoots — so it is checked per file instead of globally. And
	// "the README states its status once, not three times" is a count, which a
	// list of literal phrases cannot express at all.
	//
	// Checked before adding, by reproducing flattenForWording exactly (strip
	// each line, drop leading `#/*->`, trim `` `*.,:;!?()[]{}"' `` off each
	// word's edges, lowercase, join with single spaces) over all 2783 tracked
	// files of every type: ZERO hits for all five. So none of them collides
	// with anything, including the historical records under docs/design/ and
	// .bmad/, docs/site/release-notes.md, and
	// docs/site/developer-guide/security-review-history.md. Zero rather than
	// "only the files this change fixes" because the content work removed the
	// text first — a phrase cannot be banned while the tree still says it, or
	// the guard fails on its own tree the moment it is added.
	//
	// Every one of the five is longer than its obvious stem, and every one was
	// narrowed for a MEASURED reason rather than a guessed one:
	//
	//   - "somewhere that does not matter", not "does not matter". The short
	//     stem is ordinary English and is already written once in the tree, at
	//     internal/api/route_registry.go:28. That is a Go file and so outside
	//     wordingSweptExtensions today, but banning a run of words that plain
	//     prose uses innocently is how a guard starts failing for the wrong
	//     reason.
	//   - "experiment with and send us feedback about" — the whole
	//     introductory run that shipped, not the two words "experiment with".
	//     The product owner ruled out globally banning "experiment", and
	//     "experiment with" is ordinary English: a page may perfectly well tell
	//     somebody to experiment with a values file. Measured for the record:
	//     "experiment with" is at zero in the tree today, so the collision rule
	//     would have permitted it. It is left out on the owner's ruling and on
	//     ordinary-use grounds, NOT because it collides with anything.
	//   - "recommended production install", not "production install". The short
	//     stem has EIGHT hits, and they are the reason a bare ban was
	//     impossible: SECURITY.md:95, .goreleaser.yaml:89,
	//     catalog/addons.yaml:304,
	//     docs/design/2026-05-13-cluster-connectivity-test-redesign.md:20 and
	//     :91,
	//     docs/design/2026-07-21-operator-phase0-reconciler-coverage-matrix.md:97,
	//     docs/site/operator/argocd-account-token-expired.md:191, and
	//     tests/serverrender/releasesurface_meta_test.go:70. Two of those are
	//     historical design records; one is a runbook explaining that a bare
	//     production install is the WRONG way to wire ArgoCD. Every one of them
	//     is a correct sentence, and three of the files are inside this sweep's
	//     own roots, so a ban on the stem would have gone red immediately.
	//   - "install configure and run sharko in production", not "run sharko in
	//     production". The short stem measures zero today, so the collision
	//     rule would have permitted it, and it is still the wrong ban: it would
	//     forbid the honest prohibition "do not run Sharko in production".
	//     Sharko's support boundary has to stay sayable. The near neighbour "do
	//     not use Sharko in production" is written eight times in the tree —
	//     SECURITY.md:95, GOVERNANCE.md:122, CONTRIBUTING.md:457,
	//     .claude/team/product-manager.md:15, .goreleaser.yaml:89,
	//     docs/site/release-notes.md:23 and :56, and
	//     releasesurface_meta_test.go:70 — and the wider "sharko in production"
	//     has twelve hits, most of them inside this sweep's own roots. So only
	//     the affirmative capability claim that actually shipped is banned: the
	//     documentation table in README.md said the Operator Manual was for
	//     "Install, configure, and run Sharko in production".
	//   - "operators installing sharko in a production environment", not
	//     "installing sharko in a production environment". Same reason, same
	//     shape: the shorter form also reads naturally inside a prohibition
	//     ("we do not support installing Sharko in a production environment").
	//     Keeping "operators" ties the ban to the guide-audience framing that
	//     shipped at docs/site/operator/installation.md:3 — "This guide is for
	//     platform engineers and cluster operators installing Sharko in a
	//     production environment" — which is a claim about what the guide is
	//     for, not a limit on what Sharko is for.
	//
	// Substring check, because sweepProvenToFindAPlantedCopy requires every
	// phrase to be reported EXACTLY once in the planted page and a phrase
	// sitting inside another one would be counted twice: none of these five
	// contains another of them, and none contains or is contained by any of the
	// 31 entries above. Verified by comparing all 36 phrases against each other
	// pairwise, and by building plantedControlBody's output for all 36 and
	// confirming each is found once and only once in the flattened result.
	//
	// Honest limitation, in the same spirit as every note above: these are
	// literal phrases, not the rule. The rules are "do not tell the reader
	// their cluster is disposable", "do not frame the reader as a test
	// subject", and "do not promise production on a page whose own callout
	// says evaluation and staging". A future writer who invents a new way to
	// say any of those — "point it at a cluster you do not care about", "have a
	// play with it", "the production-grade recipe" — walks straight past this
	// list. Only a reviewer catches that.
	strings.Join([]string{"somewhere", "that", "does", "not", "matter"}, " "),
	strings.Join([]string{"experiment", "with", "and", "send", "us", "feedback", "about"}, " "),
	strings.Join([]string{"recommended", "production", "install"}, " "),
	strings.Join([]string{"install", "configure", "and", "run", "sharko", "in", "production"}, " "),
	strings.Join([]string{"operators", "installing", "sharko", "in", "a", "production", "environment"}, " "),
}

// Why the published GitHub release page is allowed to say the banned sentence.
//
// .goreleaser.yaml's release header reads "Sharko {{ .Tag }} is a technical
// preview". When v4.0.1 publishes, that renders as "Sharko v4.0.1 is a
// technical preview" — word for word what the two entries above ban in prose.
// That is correct and intended, not an oversight:
//
//   - A release page is a record OF THAT RELEASE. Naming the version there is
//     true permanently, the same way the v4.0.0 release-notes entry names
//     v4.0.0. What goes stale is a CURRENT-STATE sentence on a page that
//     outlives the version it names — a README banner, a support table, a
//     governance note. Those are what moved to "Sharko v4".
//   - The header is a template, not a literal, so it cannot drift: it always
//     renders the tag actually being built. That is why the owner ruled
//     .goreleaser.yaml stays tag-specific.
//   - This sweep could not reach it anyway. .goreleaser.yaml is not under
//     wordingSweptRoots, and the file is YAML holding a Go template, so the
//     rendered sentence never exists on disk. The guard that pins that header
//     is TestPublishedReleaseBodyCarriesTheDurableWarning in
//     releasesurface_meta_test.go, and it asserts the {{ .Tag }} form on
//     purpose.

// wordingSweptExtensions are the kinds of file a person reads: prose, chart
// values, chart templates and the install notes. Go sources are deliberately
// out, so a test that has to name the phrases can name them.
var wordingSweptExtensions = map[string]bool{
	".md": true, ".yaml": true, ".yml": true, ".tpl": true, ".txt": true,
}

// wordingSweptRoots are the trees an operator's copy of one of these sentences
// could live in. SECURITY.md and GOVERNANCE.md were added by BANNERS-V4: both
// carried the retired install-banner wording and neither is under docs/ or
// named README.md, so the walk would have missed them.
var wordingSweptRoots = []string{
	"docs", "charts", "templates", "README.md", "SECURITY.md", "GOVERNANCE.md",
	// TRUTHCLOSE-DOCS, 2026-08-27. These five live-status files carried the
	// same stale "no release right now" / "in development" family the sweep
	// was already banning elsewhere, but none of them sit under docs/ or
	// carry one of the names already listed above, so the walk was missing
	// them.
	"MAINTAINERS.md", "CONTRIBUTING.md", "CHANGELOG.md",
	".claude/team/product-manager.md", ".claude/team/project-manager.md",
}

// commentMarkers are the characters a line of prose can start with when it is
// sitting inside a comment or a list. They are dropped before the words are
// read, because a sentence wrapped inside a YAML comment has a "#" in the
// middle of it and is the same sentence.
const commentMarkers = "#/*->"

// wordEdgeTrim is the set of markdown and punctuation characters trimmed from
// each word's edges before it is compared. BANNERS-V4 added this: the real
// banned sentences wrote `v4` inside backticks and ended with a period —
// "wait for `v4`." — so a word-for-word phrase match needs "v4" to reach the
// comparison as "v4", not "`v4`.". Trimming only the edges (never the middle)
// is what keeps "don't" and "internal/credsafe" intact.
const wordEdgeTrim = "`*.,:;!?()[]{}\"'"

// flatFile is one file read as a single run of lowercase words, with a record
// of which line each word came from.
//
// Reading a file this way is what lets the sweep see a sentence that was
// wrapped across a line break — which is how both of the sentences BF13-5
// banned were actually written.
type flatFile struct {
	text   string
	starts []int
	lines  []int
}

// flattenForWording drops line breaks, indentation and leading comment markers
// and returns what is left as one run of words.
func flattenForWording(body string) flatFile {
	var f flatFile
	var b strings.Builder
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), commentMarkers)
		for _, word := range strings.Fields(trimmed) {
			// BANNERS-V4: trim markdown/punctuation off the word's edges
			// (never the middle) so "`v4`." compares as "v4" and a banned
			// phrase written with markdown around it still matches.
			word = strings.Trim(word, wordEdgeTrim)
			if word == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			f.starts = append(f.starts, b.Len())
			f.lines = append(f.lines, i+1)
			b.WriteString(strings.ToLower(word))
		}
	}
	f.text = b.String()
	return f
}

// lineAt says which source line the word covering offset at came from. An
// offset past the last word reports the last line rather than nothing, so a
// report is never silently dropped.
func (f flatFile) lineAt(at int) int {
	line := 0
	for i, start := range f.starts {
		if start > at {
			break
		}
		line = f.lines[i]
	}
	return line
}

// occurrences returns every place phrase appears in the flattened text.
func (f flatFile) occurrences(phrase string) []int {
	var at []int
	for from := 0; from <= len(f.text)-len(phrase); {
		i := strings.Index(f.text[from:], phrase)
		if i < 0 {
			break
		}
		at = append(at, f.lineAt(from+i))
		from += i + 1
	}
	return at
}

// bannedHit is one banned sentence found in one place.
type bannedHit struct {
	wording string
	where   string
}

// sweepForBannedWording is the walk itself, and the ONLY reader of the tree.
// It returns what it found and how many files it looked in, and it has no
// opinion about either — the judging is done by its callers, and one of those
// callers is the control that proves this walk works at all.
func sweepForBannedWording(t *testing.T, root string) (found []bannedHit, read int) {
	t.Helper()
	for _, rel := range wordingSweptRoots {
		start := filepath.Join(root, rel)
		if _, err := os.Stat(start); err != nil {
			t.Fatalf("cannot reach %s, so this sweep did not read what it claims to read: %v", rel, err)
		}
		err := filepath.Walk(start, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !wordingSweptExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			read++
			rp, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rp = path
			}
			flat := flattenForWording(string(body))
			for _, wording := range bannedWordings {
				for _, line := range flat.occurrences(wording) {
					found = append(found, bannedHit{
						wording: wording,
						where:   filepath.ToSlash(rp) + ":" + itoa(line),
					})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", rel, err)
		}
	}
	return found, read
}

// plantedControlDir and plantedControlFile name what the control writes into
// the documentation tree and then takes away again. They are deliberately
// obvious: if one is ever left behind by a run that was killed part way, the
// name says what it is and that it should not be there.
//
// The file goes into a directory BELOW docs/ rather than into docs/ itself, so
// the control also fails when the walk stops descending. A control planted at
// the top of a tree cannot tell a walk that recurses from one that does not.
const (
	plantedControlDir  = "zz-bf12-banned-wording-control-do-not-commit"
	plantedControlFile = "planted.md"
)

// plantedControlBody writes every banned sentence into one page, and writes
// each of them BROKEN ACROSS A LINE BREAK with a comment marker on the second
// half.
//
// That is not decoration. Both sentences BF13-5 banned were wrapped exactly
// like this where they shipped, and the sweep that came before could only read
// one line at a time, so it could not have found either. Planting them whole
// on one line would prove the sweep works on the one shape that was never the
// problem.
func plantedControlBody() string {
	var b strings.Builder
	b.WriteString("# control file\n\n")
	for _, wording := range bannedWordings {
		words := strings.Fields(wording)
		split := len(words) / 2
		if split == 0 {
			split = 1
		}
		b.WriteString("The chart refuses " + strings.Join(words[:split], " ") + "\n")
		b.WriteString("# " + strings.Join(words[split:], " ") + " `@`.\n\n")
	}
	return b.String()
}

// sweepProvenToFindAPlantedCopy is the sweep, with the proof that it works
// wrapped around it, and it is the only way to reach the sweep.
//
// It plants a real file holding every real banned sentence in a directory
// below docs/, which is one of the trees the real walk descends into, and
// requires the real walk to report that exact file for every one of them. A
// walk that read nothing, descended nowhere, matched nothing, or lost a
// sentence at the line break fails HERE, loudly, instead of going on to report
// a clean tree. Then the planted file is taken away and the same walk runs
// again, and what that second run found is what the caller judges.
func sweepProvenToFindAPlantedCopy(t *testing.T) []bannedHit {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", plantedControlDir)
	planted := filepath.Join(dir, plantedControlFile)

	if len(bannedWordings) == 0 {
		t.Fatal("no wording is banned, so this sweep would report every tree clean whatever is in it")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("docs/%s is already there before this test planted it. A run that was killed part "+
			"way left it behind; delete it, because it holds the sentences this test bans.", plantedControlDir)
	}
	// Registered before anything can fail, so the planted tree goes away
	// whichever way this function leaves, including on a failure below.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot make docs/%s for the control copy: %v", plantedControlDir, err)
	}
	if err := os.WriteFile(planted, []byte(plantedControlBody()), 0o644); err != nil {
		t.Fatalf("cannot plant the control copy of the banned wording: %v", err)
	}

	withPlanted, readWithPlanted := sweepForBannedWording(t, root)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cannot take the planted control file away again: %v", err)
	}

	if readWithPlanted == 0 {
		t.Fatal("the walk read no file at all, so it could not have found anything and its silence " +
			"about the rest of the tree means nothing")
	}
	want := "docs/" + plantedControlDir + "/" + plantedControlFile
	for _, wording := range bannedWordings {
		seen := 0
		for _, hit := range withPlanted {
			if hit.wording == wording && strings.HasPrefix(hit.where, want+":") {
				seen++
			}
		}
		if seen != 1 {
			t.Fatalf("the planted page holds %q once and the walk reported it %d time(s) there. The "+
				"walk is not reading what it claims to read — most likely it lost the sentence at the "+
				"line break — so it would call this tree clean whether it was or not.\n"+
				"  it read %d files and reported: %v", wording, seen, readWithPlanted, withPlanted)
		}
	}

	found, read := sweepForBannedWording(t, root)
	if read == 0 {
		t.Fatal("the second walk read no file at all, so this guard proves nothing")
	}
	if read != readWithPlanted-1 {
		t.Fatalf("the walk read %d files with the control file planted and %d after it was taken away. "+
			"Those should differ by exactly the one planted file, so the two runs are not looking at "+
			"the same tree.", readWithPlanted, read)
	}
	return found
}

// TestTheBannedAddressWordingIsNowhereInTheDocumentationOrTheChart is the
// judgement, over a walk that has just been shown to work.
func TestTheBannedAddressWordingIsNowhereInTheDocumentationOrTheChart(t *testing.T) {
	found := sweepProvenToFindAPlantedCopy(t)
	if len(found) != 0 {
		var lines []string
		for _, hit := range found {
			lines = append(lines, hit.where+" says "+hit.wording)
		}
		t.Errorf("banned wording about the address rule is back in %d place(s):\n  %s\n\n"+
			"The chart refuses user information in the HOST part of an address, and an \"@\" in the "+
			"path is ordinary and installs. The chart's rule is a SECOND COPY of the Go one, not the "+
			"same rule, and the tests compare the two over written-down lists rather than over every "+
			"address there is. Say what is actually true instead.",
			len(found), strings.Join(lines, "\n  "))
	}
}

// TestTheCLIInstallPageBuildsTheVersionedArchiveName pins the install command
// that works, by exact text.
//
// INSTALL-PATHS, 2026-08-29. Banning the three broken forms is only half of the
// guard, because a ban is also satisfied by a page with no install command on it
// at all, and that is its own kind of broken. This asserts the shape that was
// actually run against the published release is still on the page: the archive
// name built from the tag rather than typed out, the tagged download URL, the
// checksum file, and the -f on curl. Without -f a wrong name saves GitHub's 404
// page under the archive's name and the failure only shows up later, as a
// confusing tar error.
//
// This one reads the file as written rather than flattened, because what is
// being pinned is shell text, and the shell expansions have to survive exactly.
func TestTheCLIInstallPageBuildsTheVersionedArchiveName(t *testing.T) {
	page := filepath.Join(repoRoot(t), "docs", "site", "cli", "overview.md")
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("cannot read the CLI install page, so nothing about it is pinned: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		// The version comes from the tag, so the name can never go version-less.
		"sharko_${TAG#v}_darwin_${ARCH}.tar.gz",
		"sharko_${TAG#v}_linux_${ARCH}.tar.gz",
		// A tagged URL, not the latest/download one.
		"releases/download/${TAG}",
		// Fail on a bad name instead of saving the 404 page.
		"curl -fLO",
		// Integrity is checked, not assumed.
		"checksums.txt",
		// Both architectures are offered, so nobody is left without a command.
		"ARCH=arm64",
		"ARCH=amd64",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/site/cli/overview.md no longer contains %q. The install command on that "+
				"page is the only supported way to get the CLI and it was verified against a real "+
				"published release; if it changed shape, run it from an empty directory and prove the "+
				"new one works before changing this test.", want)
		}
	}
}

// TestEveryPlaceThatScopesCredentialsSaysTheSessionTokenIsLocal is the other
// half of the CREDS-CLAIM guard, and the half that actually protects the reader.
//
// Banning "no credentials on developer laptops" only forbids the false sentence.
// A page that simply deletes it satisfies the ban completely and leaves the
// reader knowing nothing at all about the file on their own machine — which is
// the same outcome the false sentence produced, reached by a different route. So
// the second half of the truth is pinned by exact text, in every place that
// makes a claim about where credentials live.
//
// Both halves have to be on the page, because either one alone misleads:
//
//   - the platform half alone ("the server holds the credentials") is what
//     turned into the false claim in the first place;
//   - the local half alone would suggest Sharko hands out platform credentials.
//
// Each pinned string has to be unique to the CORRECTED sentence, and getting
// that wrong is not hypothetical — the first version of this test failed exactly
// there. It pinned the three generic strings "session token", the config path
// and "platform credentials" on every page, and the break-test that reverted all
// four files reported only three of them. docs/site/cli/overview.md walked
// straight through, because its Authentication section already says "the CLI stores the
// server URL and session token locally in ~/.sharko/config" and the OLD bullet
// said "holds all platform credentials" — so all three strings were already on
// the page and the pin proved nothing about the one page the correction was most
// about. docs/architecture.md half-escaped the same way: an unrelated auth-flow
// step at :379 already said "session token".
//
// So each page now pins text that exists ONLY in its corrected sentence, and
// each entry was checked against that file's pre-correction copy to confirm it
// was absent there. The strings are still phrases rather than whole sentences,
// so a copy-editor can reword around them, but they are specific enough that
// deleting the correction cannot pass.
//
// docs/architecture.md is in this list even though README.md calls it legacy raw
// reference. It carried the false sentence word for word, it is public in the
// repository, and it is inside this sweep's own "docs" root — so the ban above
// cannot pass while it still says the old thing. Fixed rather than exempted.
func TestEveryPlaceThatScopesCredentialsSaysTheSessionTokenIsLocal(t *testing.T) {
	root := repoRoot(t)
	// The path is written with a placeholder for the tilde-slash prefix so that
	// this file's own copy cannot be mistaken for documentation prose, and is
	// assembled here into the exact text the pages carry.
	configPath := "~" + "/.sharko/config"
	for _, page := range []struct {
		rel  string
		want []string
	}{
		{"README.md", []string{
			"holds the platform credentials",
			"live Sharko session token to `" + configPath + "`",
		}},
		{filepath.Join("docs", "site", "cli", "overview.md"), []string{
			// The bold lead, because the owner's ruling is that a reader who
			// only skims must still learn a token sits on their machine.
			"Platform credentials stay on the server, your login does not",
			"live Sharko session token to `" + configPath + "`",
		}},
		{filepath.Join("docs", "site", "architecture", "overview.md"), []string{
			"Platform credentials stay on the cluster",
			"Sharko session token in `" + configPath + "`",
		}},
		{filepath.Join("docs", "architecture.md"), []string{
			"holds the platform credentials",
			"The CLI keeps only your own Sharko session token",
		}},
	} {
		body, err := os.ReadFile(filepath.Join(root, page.rel))
		if err != nil {
			t.Errorf("cannot read %s, so nothing about its credentials claim is pinned: %v", page.rel, err)
			continue
		}
		text := string(body)
		for _, want := range page.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s no longer contains %q.\n\n"+
					"That page makes a claim about where credentials live, so it has to say both "+
					"halves of the truth. The platform credentials — the ArgoCD token, the Git token "+
					"and secrets-provider access — stay server-side. The CLI session token does NOT: "+
					"sharko login writes a live one into the config file under the user's home "+
					"directory at mode 0600, and every later command sends it as a bearer token. A "+
					"reader who is not told that will not protect a file that is worth protecting. "+
					"Check cmd/sharko/client.go and cmd/sharko/login.go before changing this test.",
					page.rel, want)
			}
		}
	}
}

// TestTheOldDefectListDoesNotOpenTheFrontDoor is the third thing the
// PRESENTATION-CLEANUP guard has to catch, and it is checked PER FILE rather
// than added to bannedWordings. That is deliberate, and it is the whole point
// of the test.
//
// The paragraph is "There are no known credential leaks, permission bypasses,
// or places where Sharko says work finished when it did not". It opened
// README.md and it opened docs/site/index.md, and the product owner ruled it
// out of both: it is security-review history, not introductory product copy. A
// new reader who meets a list of old defect categories before the product is
// described reads it as a warning about the product, not as a record of work
// somebody did.
//
// It is not false and it is not being deleted. It now lives on
// docs/site/developer-guide/security-review-history.md along with the rest of
// that write-up, and quoting the old claim in the words it was made in is that
// page's entire job.
//
// So a phrase in bannedWordings could not work here. wordingSweptRoots includes
// the whole docs/ tree, the history page is inside it, and a global ban would
// make this guard fail on the project's own historical record — the one thing
// every note in this file has refused to do. Measured, not assumed: the natural
// stem "no known credential leaks" has exactly ONE hit across all 2783 tracked
// files, and it is that history page, at line 16. Narrowing the phrase would
// not fix it either, because the history page has to be able to write the claim
// out in full or it stops being a record. So this check names the two pages the
// paragraph must not open, and says nothing whatever about anywhere else.
//
// Both halves read the FLATTENED text rather than the raw file, because the
// docs/site/index.md copy was wrapped across lines 10-11 where it shipped and a
// line-by-line reader would have walked straight past it.
//
// The second half is here for the same reason
// TestEveryPlaceThatScopesCredentialsSaysTheSessionTokenIsLocal above has a
// second half: a ban is also satisfied by deleting the record, and that is its
// own kind of failure. So the history page is required to still carry the
// claim. If a copy-editor rewords it, update the expected text here to the new
// wording — do NOT delete this half, and do not answer a failure by taking the
// paragraph off the history page.
func TestTheOldDefectListDoesNotOpenTheFrontDoor(t *testing.T) {
	root := repoRoot(t)
	// Assembled from its words, the way bannedWordings is, so this file is not
	// itself a searchable copy of the paragraph it is moving.
	defectList := strings.Join([]string{"credential", "leaks", "permission", "bypasses"}, " ")
	saidItFinished := strings.Join([]string{
		"places", "where", "sharko", "says", "work", "finished", "when", "it", "did", "not",
	}, " ")

	for _, rel := range []string{"README.md", filepath.Join("docs", "site", "index.md")} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("cannot read %s, so nothing about how it opens is pinned: %v", rel, err)
			continue
		}
		flat := flattenForWording(string(body))
		for _, phrase := range []string{defectList, saidItFinished} {
			at := flat.occurrences(phrase)
			if len(at) == 0 {
				continue
			}
			var where []string
			for _, line := range at {
				where = append(where, filepath.ToSlash(rel)+":"+itoa(line))
			}
			t.Errorf("%s says %q at %s.\n\n"+
				"That is the security-review paragraph, and it is not introductory product copy. A "+
				"reader meeting a list of old defect categories before the product is described reads "+
				"it as a warning about the product. It is kept — in full, in its own words — on "+
				"docs/site/developer-guide/security-review-history.md, which is where the whole "+
				"review write-up lives and where saying it is correct. Link to that page from here "+
				"instead of restating it, and do not answer this failure by editing the history page.",
				filepath.ToSlash(rel), phrase, strings.Join(where, ", "))
		}
	}

	// The other half: the record still exists, so this ban cannot be satisfied
	// by deleting the history instead of moving it.
	history := filepath.Join("docs", "site", "developer-guide", "security-review-history.md")
	body, err := os.ReadFile(filepath.Join(root, history))
	if err != nil {
		t.Errorf("cannot read %s, so nothing proves the paragraph was moved rather than deleted: %v",
			history, err)
		return
	}
	claim := strings.Join([]string{"no", "known", "credential", "leaks"}, " ")
	if len(flattenForWording(string(body)).occurrences(claim)) == 0 {
		t.Errorf("%s no longer says %q.\n\n"+
			"The two pages above are forbidden from opening with that paragraph precisely because "+
			"this page keeps it. If it is gone from here as well, the project has quietly dropped a "+
			"true statement about what its own review did and did not cover, which is worse than the "+
			"presentation problem the ban was for. Put the claim back on this page, or reword this "+
			"expectation to match the new wording — do not delete this check.",
			filepath.ToSlash(history), claim)
	}
}

// TestTheReadmeSaysItsStatusOnceInItsOpening is the sixth thing the
// PRESENTATION-CLEANUP guard has to catch, and it is a COUNT rather than a
// phrase, because bannedWordings cannot express "at most one".
//
// README.md used to carry three warning blocks: two blockquotes back to back at
// the top and a third under the Quick Start heading, each restating that Sharko
// is a technical preview and must not be used in production. Nothing about any
// one of those sentences was bannable on its own. The defect was that there
// were three of them, and a reader told the same thing three times before the
// product is described reads a product that is apologising for itself.
//
// "The README's top block" means every line above the first line beginning with
// "## ". That boundary is written down here on purpose, so this check cannot
// quietly drift into meaning something else later. It is the right boundary for
// this file because README.md's title is an HTML `<h1 align="center">` rather
// than a markdown heading, so "## " is the FIRST structural break anywhere in
// the file and everything above it is what a reader meets before any section
// starts. Today that is lines 1-35: the logo, the badges, a horizontal rule,
// the one status note at line 20, and the product description.
//
// Two counts, because either one alone is escapable:
//
//   - Exactly one blockquote BLOCK in the top block. A block is a run of
//     consecutive lines beginning with ">", so a note wrapped over several
//     lines still counts once. This is the count that catches a SECOND warning
//     written in different words. It is deliberately not a count over the whole
//     file: the README having exactly one blockquote anywhere in it is true
//     today, but a whole-file count would also fail on a perfectly good
//     blockquote added far below, in a section about something else, and that is
//     not what is being protected.
//   - Exactly one copy of the status sentence itself, counted over the whole
//     file. This is the count that catches the note being repeated further down,
//     which is exactly what the third warning block did. It also fails if the
//     note is DELETED — a README with no status note at all satisfies "not
//     twice" completely, and that is worse than the thing being fixed. The
//     sentence names no patch version, so it does not go stale at v4.0.2, and
//     it deliberately reads "Sharko v4", not "Sharko v4.0.1", so it does not
//     collide with the bannedWordings entries that ban the patch-pinned forms.
//
// The sentence carries the boundary, not only the status: it says Sharko is not
// supported in production. That half is the load-bearing half. An earlier round
// of this cleanup replaced "do not use Sharko in production" with "currently
// intended for evaluation and staging environments" on all five reader pages at
// once, and the result was a repository where SECURITY.md forbade production use
// and every page a user actually reads no longer said so. If this sentence is
// reworded again, keep a plain prohibition in it, and keep the same wording on
// README.md, docs/site/index.md, docs/site/technical-preview.md,
// docs/site/getting-started/installation.md, docs/site/getting-started/quickstart.md
// and docs/site/operator/installation.md, so one search still checks all of them.
//
// Honest limitation: neither count catches a second status warning written as a
// plain bold paragraph rather than a blockquote, in words that are not the
// pinned sentence. There is no structural mark to count for that shape. A
// reviewer still has to read the opening.
func TestTheReadmeSaysItsStatusOnceInItsOpening(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("cannot read README.md, so nothing about its opening is pinned: %v", err)
	}
	text := string(body)
	lines := strings.Split(text, "\n")

	// Where the opening ends. Both failures below are fatal because a boundary
	// that silently means "the whole file" or "nothing" would make the count
	// pass whatever the README said.
	top := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			top = i
			break
		}
	}
	if top < 0 {
		t.Fatal("README.md has no \"## \" heading anywhere in it, so this test cannot tell the " +
			"opening from the rest of the file and any count it reported would be meaningless")
	}
	if top == 0 {
		t.Fatal("README.md begins at a \"## \" heading, so its top block is empty and this count " +
			"would pass whatever the file said")
	}

	blocks := 0
	inBlock := false
	for _, line := range lines[:top] {
		quoted := strings.HasPrefix(strings.TrimSpace(line), ">")
		if quoted && !inBlock {
			blocks++
		}
		inBlock = quoted
	}
	if blocks != 1 {
		t.Errorf("README.md's opening — the %d lines above its first \"## \" heading — holds %d "+
			"blockquote block(s), not 1.\n\n"+
			"The opening carries ONE status note. It used to carry three warnings, and being told "+
			"the same thing three times before the product is described is what made the README "+
			"read like an apology. If there is more than one now, fold them into the single note. "+
			"If there is none, the status has gone missing entirely, which is the opposite mistake.",
			top, blocks)
	}

	// The sentence itself, counted over the whole file, so a second copy lower
	// down is caught as well.
	note := "Sharko v4 is a technical preview, for evaluation and staging environments. " +
		"It is not supported in production."
	if n := strings.Count(text, note); n != 1 {
		t.Errorf("README.md says %q %d time(s), not once.\n\n"+
			"More than one means the status warning has been repeated somewhere below the opening, "+
			"which is what the third warning block used to do. None means the README no longer "+
			"states its status at all — say it once, in the opening. If the sentence itself is being "+
			"reworded, change it here in the same commit, and keep it free of a patch version so it "+
			"does not go stale at the next release.", note, n)
	}
}
