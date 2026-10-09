package main

// server_address_leak_test.go — BF10.
//
// The CLI keeps the Sharko server address on disk (~/.sharko/config, `server:`)
// and lets --server override it. That address is credential material: it can be
// written https://user:token@sharko.example, or with the token alone in the
// username position, or hung off a query string. Sharko used to hand it back:
// Go's HTTP transport quotes the address inside its own error and masks only
// the password half of the userinfo, and several commands printed the resolved
// address themselves, which put on screen even the half Go had masked.
//
// What these tests hold in place:
//
//   - every command in the CLI is accounted for, from a walk of the command
//     tree rather than a list somebody typed;
//   - every command that talks to the server refuses an unsafe saved address,
//     and refuses it before the first packet;
//   - all five carriers are refused, including an ordinary "?ref=main";
//   - the refusal says which setting to fix and nothing about the value;
//   - an ordinary, credential-free address still works exactly as before.
//
// # On proving a clean output is really clean
//
// A detector that has never been shown to fire proves nothing by not firing.
// So every test here that claims Sharko's output is clean first proves the
// sentinel IS present somewhere it should be — in the config file the test
// wrote, or in the bare Go transport error — using the same substring search
// that is then run against Sharko's output.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ---------------------------------------------------------------------------
// Sentinels — one per slot a credential can occupy.
// ---------------------------------------------------------------------------

const (
	sentinelUser  = "SENTINELUSERNAMESLOT"
	sentinelPass  = "SENTINELPASSWORDSLOT"
	sentinelQuery = "SENTINELQUERYSLOT"
	sentinelFrag  = "SENTINELFRAGMENTSLOT"

	// The same four slots again, in an address written with NO scheme in
	// front of it. BF12 measured these shapes reaching a rendered pod in
	// the clear, and the CLI suite had not one of them: every carrier here
	// used to start with "https://", so what it proved was "every carrier
	// when a scheme is present".
	sentinelNoSchemeUser  = "SENTINELNOSCHEMEUSERNAMESLOT"
	sentinelNoSchemePass  = "SENTINELNOSCHEMEPASSWORDSLOT"
	sentinelNoSchemeQuery = "SENTINELNOSCHEMEQUERYSLOT"
	sentinelNoSchemeFrag  = "SENTINELNOSCHEMEFRAGMENTSLOT"

	// An address written as a network-path reference, which is the third
	// way an operator can write one.
	sentinelNetworkPath = "SENTINELNETWORKPATHPASSWORDSLOT"

	// A sentinel in the PATH of an ORDINARY, credential-free address. It is
	// not a credential and is not pretending to be one: it is there so the
	// tests that claim Sharko never reproduces the address it was given
	// have something real to look for in a run the boundary actually lets
	// through. See TestRequestBuildError_IsSharkosOwnWords.
	sentinelPath = "SENTINELPATHSLOT"
)

// allSentinels is what every "is the output clean?" check scans for.
var allSentinels = []string{
	sentinelUser, sentinelPass, sentinelQuery, sentinelFrag,
	sentinelNoSchemeUser, sentinelNoSchemePass, sentinelNoSchemeQuery, sentinelNoSchemeFrag,
	sentinelNetworkPath, sentinelPath,
}

// carrier is one shape of address that must be refused.
type carrier struct {
	name      string
	address   string
	sentinels []string
}

// everyCarrier is the full set: the four slots a credential can sit in, plus
// an ordinary query string that carries no secret at all.
//
// The last one is refused on purpose. The rule underneath is structural — it
// asks whether the address has somewhere for a credential to sit, never
// whether the text there looks secret — and a structural rule is the only kind
// that does not fail on the first shape nobody predicted. The price is that
// "?ref=main" is refused too, and Sharko must not pretend it detected a
// credential in it: the sentence states the rule, not an accusation.
func everyCarrier() []carrier {
	return []carrier{
		{
			name:      "username slot",
			address:   "https://" + sentinelUser + "@sharko.example",
			sentinels: []string{sentinelUser},
		},
		{
			name:      "password slot",
			address:   "https://x-access-token:" + sentinelPass + "@sharko.example",
			sentinels: []string{sentinelPass},
		},
		{
			name:      "query string",
			address:   "https://sharko.example?access_token=" + sentinelQuery,
			sentinels: []string{sentinelQuery},
		},
		{
			name:      "fragment",
			address:   "https://sharko.example#" + sentinelFrag,
			sentinels: []string{sentinelFrag},
		},
		{
			name:      "an ordinary query that carries nothing secret",
			address:   "https://sharko.example?ref=main",
			sentinels: nil,
		},

		// The same carriers with NO scheme in front. These are the shapes
		// BF12 measured reaching a rendered pod in the clear, and until
		// BF12-4 not one of them was in this file: every row above starts
		// with "https://", so the suite proved "every carrier when a scheme
		// is present" and called it "every carrier".
		{
			name:      "username slot, written with no scheme",
			address:   sentinelNoSchemeUser + "@sharko.example",
			sentinels: []string{sentinelNoSchemeUser},
		},
		{
			name:      "password slot, written with no scheme",
			address:   "user:" + sentinelNoSchemePass + "@sharko.example/api",
			sentinels: []string{sentinelNoSchemePass},
		},
		{
			name:      "query string, written with no scheme",
			address:   "sharko.example/api?access_token=" + sentinelNoSchemeQuery,
			sentinels: []string{sentinelNoSchemeQuery},
		},
		{
			name:      "fragment, written with no scheme",
			address:   "sharko.example/api#" + sentinelNoSchemeFrag,
			sentinels: []string{sentinelNoSchemeFrag},
		},
		{
			name:      "a network-path reference with a password in it",
			address:   "//u:" + sentinelNetworkPath + "@sharko.example/api",
			sentinels: []string{sentinelNetworkPath},
		},
		{
			name:      "a port that is not a number, so the address cannot be read at all",
			address:   "https://sharko.example:notaport/api",
			sentinels: nil,
		},
	}
}

// TestEveryCarrierSetCoversEveryWayAnAddressCanBeWritten is the guard on the
// carrier table itself.
//
// The defect BF12-4 repaired was not a missing assertion, it was a table that
// only held one shape. A count would not catch that — an entry swapped for
// another leaves the count alone — so this classifies every row by how it is
// written and compares the exact numbers.
func TestEveryCarrierSetCoversEveryWayAnAddressCanBeWritten(t *testing.T) {
	var explicitScheme, networkPath, noScheme int
	for _, c := range everyCarrier() {
		switch {
		case strings.HasPrefix(c.address, "//"):
			networkPath++
		case regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`).MatchString(c.address):
			explicitScheme++
		default:
			noScheme++
		}
	}
	// Exact, compared with !=, so both a shrinking table and an accidental
	// duplicate are failures. Edit these on purpose when the table changes
	// on purpose.
	const (
		wantExplicitScheme = 6
		wantNetworkPath    = 1
		wantNoScheme       = 4
	)
	if explicitScheme != wantExplicitScheme {
		t.Errorf("%d carriers are written with an explicit scheme, expected exactly %d", explicitScheme, wantExplicitScheme)
	}
	if networkPath != wantNetworkPath {
		t.Errorf("%d carriers are written as a network-path reference, expected exactly %d", networkPath, wantNetworkPath)
	}
	if noScheme != wantNoScheme {
		t.Errorf("%d carriers are written with no scheme at all, expected exactly %d. Those are the "+
			"shapes measured leaking before BF12; a suite with none of them proves only half the rule.",
			noScheme, wantNoScheme)
	}
}

// unsafeAddress holds every slot at once, so a test that scans for its four
// sentinels is scanning for things that really are in the input.
const unsafeAddress = "https://" + sentinelUser + ":" + sentinelPass +
	"@sharko.example:8443?access_token=" + sentinelQuery + "#" + sentinelFrag

// unsafeSchemelessAddress is the same thing written the way an operator writes
// a host with no scheme in front of it. This is the shape BF12 measured
// reaching a rendered pod in the clear, and every "unsafe address" test in
// this file used to run only the scheme-ful one above.
const unsafeSchemelessAddress = sentinelNoSchemeUser + ":" + sentinelNoSchemePass +
	"@sharko.example:8443/api?access_token=" + sentinelNoSchemeQuery + "#" + sentinelNoSchemeFrag

// unsafeComposite is one whole-address fixture together with the sentinels
// that really are inside it. A positive control must prove the sentinels it is
// about to look for are in the input, and the two fixtures do not share any.
type unsafeComposite struct {
	name      string
	address   string
	sentinels []string
}

// everyUnsafeComposite is what the whole-command sweeps run over. Both shapes,
// every time, so "every command refuses an unusable address" is not quietly
// "every command refuses an unusable address that starts with https://".
func everyUnsafeComposite() []unsafeComposite {
	return []unsafeComposite{
		{
			name:      "written with a scheme",
			address:   unsafeAddress,
			sentinels: []string{sentinelUser, sentinelPass, sentinelQuery, sentinelFrag},
		},
		{
			name:      "written with no scheme",
			address:   unsafeSchemelessAddress,
			sentinels: []string{sentinelNoSchemeUser, sentinelNoSchemePass, sentinelNoSchemeQuery, sentinelNoSchemeFrag},
		},
	}
}

// ---------------------------------------------------------------------------
// Isolation. This must fail at SETUP, never after the fact.
// ---------------------------------------------------------------------------

// isolatedConfigDir points the CLI at a throwaway directory and PROVES the
// override took effect before the caller is allowed to do anything.
//
// The real ~/.sharko/config is a live file holding a live token. A test that
// could read or overwrite it is not a test that got lucky — it is a test that
// must not be able to run at all. So the assertions below run before any
// command does, and they are t.Fatalf, not t.Errorf.
func isolatedConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("SHARKO_CONFIG_DIR", dir)
	configHomeWarned = false

	got := configDir()
	if got != dir {
		t.Fatalf("SHARKO_CONFIG_DIR did not take effect: configDir() = %q, want %q — "+
			"refusing to run, because this test would otherwise read the real CLI config", got, dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		real := filepath.Join(home, ".sharko")
		if got == real || strings.HasPrefix(got+string(filepath.Separator), real+string(filepath.Separator)) {
			t.Fatalf("the isolated config dir %q is inside the real one %q — refusing to run", got, real)
		}
	}
	return dir
}

// writeRawConfig puts a config file on disk WITHOUT going through saveConfig.
//
// saveConfig now refuses an unsafe address, which is the point of half these
// tests — so it cannot be the thing that creates the legacy file. This writes
// the bytes directly, exactly as a config saved before the rule existed would
// look, and returns the file's contents so the caller can prove the sentinels
// really are in it.
func writeRawConfig(t *testing.T, dir, server, token string) string {
	t.Helper()
	body := fmt.Sprintf("server: %s\ntoken: %s\n", server, token)
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("writing the legacy config fixture: %v", err)
	}
	return body
}

// provePresent is the positive control. It asserts the given sentinels ARE in
// haystack, using the same search later used to claim Sharko's output is
// clean. If this fails, every "clean" claim built on it is worthless.
func provePresent(t *testing.T, what, haystack string, sentinels []string) {
	t.Helper()
	for _, s := range sentinels {
		if !strings.Contains(haystack, s) {
			t.Fatalf("positive control failed: %s does not contain %q, so finding it absent from "+
				"Sharko's output would prove nothing.\n\ngot: %s", what, s, haystack)
		}
	}
}

// proveAbsent scans Sharko's own output for every sentinel.
func proveAbsent(t *testing.T, what, haystack string) {
	t.Helper()
	for _, s := range allSentinels {
		if strings.Contains(haystack, s) {
			t.Errorf("%s leaks %q:\n\n%s", what, s, haystack)
		}
	}
}

// ---------------------------------------------------------------------------
// The command tree, walked — never a list somebody typed, never a count.
// ---------------------------------------------------------------------------

// cobraBuiltIns are the subcommands cobra adds by itself the first time
// rootCmd.Execute() runs. They are not Sharko's and they never touch a server
// address, so the walk skips them and their children — otherwise this file
// would pass or fail depending on whether some other test in the package had
// already called Execute().
var cobraBuiltIns = map[string]bool{"help": true, "completion": true}

// walkCommandTree returns the full command path of every Sharko command,
// sorted. It is the ONLY source of truth about which commands exist: nothing
// in this file may name a command that the walk did not produce, and nothing
// the walk produces may go unclassified.
func walkCommandTree() []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c != rootCmd {
			if cobraBuiltIns[c.Name()] {
				return
			}
			out = append(out, c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	sort.Strings(out)
	return out
}

// commandKind says what a command does with the Sharko server address.
type commandKind int

const (
	// reachesServer — the command resolves the server address and makes an
	// API call with it. Every one of these MUST refuse an unsafe address.
	reachesServer commandKind = iota
	// dialsDirectly — `sharko login`, the one command that builds its own
	// request instead of going through apiRequest, and whose address comes
	// from --server because there is no saved config yet. Covered by its
	// own test.
	dialsDirectly
	// neverTouchesTheServer — reads files, serves, or only prints help.
	neverTouchesTheServer
)

// commandUnderTest is one row of the classification: what the command does
// with the address, and the arguments needed to drive it far enough to find
// out.
type commandUnderTest struct {
	kind commandKind
	// argv is everything after the command path. Commands are driven with
	// these, so a wrong argv shows up as a failure rather than as a test
	// that quietly stopped short of the code it meant to exercise.
	argv []string
}

// classifiedCommands must have EXACTLY the same keys as walkCommandTree().
//
// Adding a command without classifying it fails. Leaving a row behind after a
// command is deleted fails. That is the whole reason this is a map checked
// against a walk instead of a list of things to test.
var classifiedCommands = map[string]commandUnderTest{
	"sharko add-addon":          {reachesServer, []string{"demo-addon", "--chart", "c", "--repo", "https://charts.example", "--version", "1.0.0"}},
	"sharko add-cluster":        {reachesServer, []string{"demo-cluster"}},
	"sharko add-clusters":       {reachesServer, []string{"a,b"}},
	"sharko add-to-catalog":     {reachesServer, []string{"demo-addon", "--chart", "c", "--repo", "https://charts.example", "--version", "1.0.0"}},
	"sharko adopt":              {reachesServer, []string{"demo-cluster", "--yes"}},
	"sharko configure-addon":    {reachesServer, []string{"demo-addon", "--version", "1.0.0"}},
	"sharko connect":            {neverTouchesTheServer, nil},
	"sharko connect list":       {reachesServer, nil},
	"sharko connect test":       {reachesServer, nil},
	"sharko describe-addon":     {reachesServer, []string{"demo-addon"}},
	"sharko disable-addon":      {reachesServer, []string{"demo-cluster", "demo-addon"}},
	"sharko drop-legacy-labels": {reachesServer, []string{"demo-cluster", "--yes"}},
	"sharko enable-addon":       {reachesServer, []string{"demo-cluster", "demo-addon"}},
	"sharko init":               {reachesServer, nil},
	"sharko list-addons":        {reachesServer, nil},
	"sharko list-clusters":      {reachesServer, nil},
	"sharko login":              {dialsDirectly, nil},
	"sharko pr":                 {neverTouchesTheServer, nil},
	"sharko pr list":            {reachesServer, nil},
	"sharko pr refresh":         {reachesServer, []string{"1"}},
	"sharko pr status":          {reachesServer, []string{"1"}},
	// `sharko pr wait` gets a short timeout on purpose. Its poll loop calls
	// os.Exit(2) when the deadline passes, and os.Exit from inside a test
	// kills the whole test binary with no "--- FAIL" line to say which
	// command did it. With the refusal in place the loop returns on the
	// first iteration and never reaches the deadline; the short timeout is
	// what keeps a regression noisy-but-quick instead of silent-and-slow.
	"sharko pr wait":                 {reachesServer, []string{"1", "--timeout", "5s"}},
	"sharko refresh-secrets":         {reachesServer, nil},
	"sharko remove-addon":            {reachesServer, []string{"demo-addon", "--confirm"}},
	"sharko remove-cluster":          {reachesServer, []string{"demo-cluster", "--yes"}},
	"sharko repair-connection":       {reachesServer, []string{"demo-cluster"}},
	"sharko reset-admin":             {neverTouchesTheServer, nil},
	"sharko secret-status":           {reachesServer, nil},
	"sharko serve":                   {neverTouchesTheServer, nil},
	"sharko status":                  {reachesServer, nil},
	"sharko takeover":                {reachesServer, []string{"demo-cluster", "--yes"}},
	"sharko takeover-preflight":      {reachesServer, []string{"demo-cluster"}},
	"sharko test-cluster":            {reachesServer, []string{"demo-cluster"}},
	"sharko token":                   {neverTouchesTheServer, nil},
	"sharko token create":            {reachesServer, []string{"--name", "t"}},
	"sharko token list":              {reachesServer, nil},
	"sharko token renew":             {reachesServer, []string{"t"}},
	"sharko token revoke":            {reachesServer, []string{"t"}},
	"sharko unadopt-cluster":         {reachesServer, []string{"demo-cluster", "--yes"}},
	"sharko unregister-consequences": {reachesServer, []string{"demo-cluster"}},
	"sharko update-cluster":          {reachesServer, []string{"demo-cluster", "--add-addon", "x"}},
	"sharko upgrade-addon":           {reachesServer, []string{"demo-addon", "--version", "1.0.0"}},
	"sharko upgrade-addons":          {reachesServer, []string{"demo-addon=1.0.0"}},
	"sharko upgrade-clusters":        {reachesServer, []string{"demo-addon", "--version", "1.0.0", "--cluster", "demo-cluster", "--yes"}},
	"sharko user":                    {neverTouchesTheServer, nil},
	"sharko user create":             {reachesServer, []string{"someone", "--role", "viewer"}},
	"sharko user delete":             {reachesServer, []string{"someone", "--yes"}},
	"sharko user list":               {reachesServer, nil},
	"sharko user update":             {reachesServer, []string{"someone", "--role", "viewer"}},
	"sharko validate":                {neverTouchesTheServer, nil},
	"sharko validate-catalog":        {neverTouchesTheServer, nil},
	"sharko validate-config":         {neverTouchesTheServer, nil},
	"sharko version":                 {reachesServer, nil},
}

// TestCommandTree_EveryCommandIsClassified is the guard that keeps the rest of
// this file honest.
//
// It compares the walked tree against the classification's keys BOTH ways, so
// it fails when a new command appears and when a stale row lingers. A count
// would catch neither reliably — one added and one removed leaves the count
// alone.
func TestCommandTree_EveryCommandIsClassified(t *testing.T) {
	walked := walkCommandTree()

	// Zero commands is a broken walk, not a clean sweep. Everything below
	// would pass vacuously.
	if len(walked) == 0 {
		t.Fatal("the command tree walk found no commands at all — this file would then prove nothing")
	}

	inTree := map[string]bool{}
	for _, path := range walked {
		inTree[path] = true
		if _, ok := classifiedCommands[path]; !ok {
			t.Errorf("new command %q is not classified — say whether it reaches the Sharko server "+
				"and, if it does, give the arguments that drive it there", path)
		}
	}
	for path := range classifiedCommands {
		if !inTree[path] {
			t.Errorf("stale entry %q is classified but no longer exists in the command tree", path)
		}
	}

	// An exact floor, compared with != so it fails in both directions. It
	// is here to make a silent collapse of the walk impossible, not to
	// approve of any particular number: when commands are added or removed
	// on purpose, this number is edited on purpose too.
	const commandsInTheCLI = 53
	if len(walked) != commandsInTheCLI {
		t.Errorf("the CLI has %d commands, this test was written against exactly %d — "+
			"update the classification above and this number together:\n%s",
			len(walked), commandsInTheCLI, strings.Join(walked, "\n"))
	}
}

// ---------------------------------------------------------------------------
// Driving a command with everything it writes captured.
// ---------------------------------------------------------------------------

// runCommand drives one CLI command through cobra exactly as a shell would,
// and returns everything it wrote plus the error it returned.
//
// os.Stdout and os.Stderr are swapped for a real file rather than a pipe: the
// commands write with fmt.Printf, which reads os.Stdout at call time, and a
// pipe would deadlock the moment a command wrote more than the pipe buffer.
//
// os.Stdin is swapped for an EMPTY file, and that is not a detail. Several
// commands ask "are you sure?" and read the answer from stdin. Left pointing
// at the real stdin, one of those blocks the whole test run forever with no
// output and nothing to show which command it was. Pointed at an empty file it
// reads EOF immediately, and a command that gets that far has already failed
// this test's real assertion anyway — the refusal is supposed to come first.
func runCommand(t *testing.T, path string, argv []string) (output string, err error) {
	t.Helper()

	args := append(strings.Fields(strings.TrimPrefix(path, "sharko ")), argv...)

	scratch := t.TempDir()
	sink, mkErr := os.CreateTemp(scratch, "cli-output-*")
	if mkErr != nil {
		t.Fatalf("creating the output sink: %v", mkErr)
	}
	defer sink.Close()

	emptyPath := filepath.Join(scratch, "empty-stdin")
	if wErr := os.WriteFile(emptyPath, nil, 0600); wErr != nil {
		t.Fatalf("creating the empty stdin: %v", wErr)
	}
	emptyStdin, oErr := os.Open(emptyPath)
	if oErr != nil {
		t.Fatalf("opening the empty stdin: %v", oErr)
	}
	defer emptyStdin.Close()

	origOut, origErr, origIn := os.Stdout, os.Stderr, os.Stdin
	os.Stdout, os.Stderr, os.Stdin = sink, sink, emptyStdin

	prevSilenceUsage, prevSilenceErrors := rootCmd.SilenceUsage, rootCmd.SilenceErrors
	rootCmd.SilenceUsage, rootCmd.SilenceErrors = true, true
	rootCmd.SetOut(sink)
	rootCmd.SetErr(sink)
	rootCmd.SetArgs(args)

	err = rootCmd.Execute()

	os.Stdout, os.Stderr, os.Stdin = origOut, origErr, origIn
	rootCmd.SilenceUsage, rootCmd.SilenceErrors = prevSilenceUsage, prevSilenceErrors
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	rootCmd.SetArgs(nil)
	resetEveryFlag()

	body, readErr := os.ReadFile(sink.Name())
	if readErr != nil {
		t.Fatalf("reading back what the command wrote: %v", readErr)
	}
	return string(body), err
}

// resetEveryFlag puts every flag in the tree back to its default. Each
// command's RunE is a closure over a package-level *cobra.Command, so a flag
// set by one run would otherwise still be set on the next.
func resetEveryFlag() {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		resetFlagSet(c)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	serverFlag = ""
}

func resetFlagSet(cmd *cobra.Command) {
	resetFlags(cmd)
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// ---------------------------------------------------------------------------
// Every command that talks to the server refuses an unsafe saved address.
// ---------------------------------------------------------------------------

func TestEveryServerReachingCommand_RefusesAnUnsafeSavedAddress(t *testing.T) {
	walked := walkCommandTree()
	if len(walked) == 0 {
		t.Fatal("the command tree walk found no commands at all")
	}

	exercised := 0
	for _, shape := range everyUnsafeComposite() {
		t.Run(shape.name, func(t *testing.T) {
			dir := isolatedConfigDir(t)
			onDisk := writeRawConfig(t, dir, shape.address, "test-token")

			// Positive control: the sentinels really are in the input, found
			// by the same search that is about to be run against each
			// command's output.
			provePresent(t, "the legacy config file this test wrote", onDisk, shape.sentinels)

			for _, path := range walked {
				row, ok := classifiedCommands[path]
				if !ok || row.kind != reachesServer {
					continue
				}
				exercised++
				t.Run(path, func(t *testing.T) {
					output, err := runCommand(t, path, row.argv)

					combined := output
					if err != nil {
						combined += "\n" + err.Error()
					}

					if err == nil {
						t.Fatalf("the command succeeded with an unusable server address; it must refuse.\n\noutput:\n%s", output)
					}
					if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
						t.Fatalf("the command failed, but not with the shared refusal — so it did not reach the "+
							"one place the address is checked. Fix the arguments in classifiedCommands, or the "+
							"command itself.\n\nerror: %v\n\noutput:\n%s", err, output)
					}
					proveAbsent(t, "command "+path, combined)
				})
			}
		})
	}

	// Zero exercised commands is fatal, not a pass.
	if exercised == 0 {
		t.Fatal("no server-reaching command was exercised — the loop above proved nothing")
	}
}

// TestUnsafeSavedAddress_IsRefusedBeforeAnyDial proves the refusal happens
// before the network, not after it.
//
// The address is pointed at a real listener that counts every connection. A
// check that ran after the dial would still produce a clean refusal message
// and would still pass every other test in this file; only the counter can
// tell the difference.
func TestUnsafeSavedAddress_IsRefusedBeforeAnyDial(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	// A real, reachable listener with a credential written into the address.
	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing the test server URL: %v", err)
	}
	unsafeButReachable := []string{
		parsed.Scheme + "://" + sentinelUser + ":" + sentinelPass + "@" + parsed.Host,
		// The same reachable listener, written with no scheme.
		sentinelNoSchemeUser + ":" + sentinelNoSchemePass + "@" + parsed.Host,
	}

	// Positive control: the same address WITHOUT the credential does reach
	// the listener. Otherwise a zero hit count would prove only that the
	// test server was never reachable.
	dir := isolatedConfigDir(t)
	writeRawConfig(t, dir, srv.URL, "test-token")
	if _, _, err := apiGet("/api/v1/health"); err != nil {
		t.Fatalf("positive control failed: a credential-free address could not reach the test server "+
			"either, so a zero hit count below would prove nothing: %v", err)
	}
	if atomic.LoadInt64(&hits) == 0 {
		t.Fatal("positive control failed: the credential-free request never arrived at the listener")
	}
	atomic.StoreInt64(&hits, 0)

	for _, address := range unsafeButReachable {
		writeRawConfig(t, dir, address, "test-token")
		_, _, err = apiGet("/api/v1/health")
		if err == nil {
			t.Fatal("an unusable address was allowed through to the network")
		}
		if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
			t.Fatalf("failed, but not with the shared refusal: %v", err)
		}
		if got := atomic.LoadInt64(&hits); got != 0 {
			t.Errorf("Sharko dialled the server %d time(s) before refusing the address; it must refuse first", got)
		}
		proveAbsent(t, "the refusal from apiRequest", err.Error())
	}
	if len(unsafeButReachable) == 0 {
		t.Fatal("no address was driven at the listener, so this guard proves nothing")
	}
}

// TestLogin_RefusesAnUnsafeServerFlag covers the one command that builds its
// own request rather than going through apiRequest, and whose address arrives
// on --server rather than from the config file.
//
// It must refuse before it prompts for a password, before it dials, and before
// it saves anything.
func TestLogin_RefusesAnUnsafeServerFlag(t *testing.T) {
	dir := isolatedConfigDir(t)

	prev := serverFlag
	t.Cleanup(func() { serverFlag = prev })

	for _, shape := range everyUnsafeComposite() {
		t.Run(shape.name, func(t *testing.T) {
			output, err := runCommand(t, "sharko login",
				[]string{"--server", shape.address, "--username", "u", "--password", "p"})
			if err == nil {
				t.Fatalf("login accepted an unusable server address.\n\noutput:\n%s", output)
			}
			if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
				t.Fatalf("login failed, but not with the shared refusal: %v", err)
			}
			proveAbsent(t, "sharko login", output+"\n"+err.Error())

			// And nothing was written.
			if _, statErr := os.Stat(filepath.Join(dir, "config")); !os.IsNotExist(statErr) {
				t.Errorf("login wrote a config file despite refusing the address (stat error: %v)", statErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Every carrier, at the shared boundary and at the write boundary.
// ---------------------------------------------------------------------------

func TestEveryCarrier_IsRefusedAtTheSharedBoundary(t *testing.T) {
	prev := serverFlag
	t.Cleanup(func() { serverFlag = prev })
	serverFlag = ""

	for _, c := range everyCarrier() {
		t.Run(c.name, func(t *testing.T) {
			// From the saved config.
			got, err := effectiveServer(c.address)
			if err == nil {
				t.Fatalf("effectiveServer accepted a %s address and returned %q", c.name, got)
			}
			if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
				t.Fatalf("refused, but not with the shared refusal: %v", err)
			}
			if got != "" {
				t.Errorf("effectiveServer returned an address alongside the refusal: %q", got)
			}
			if !strings.Contains(err.Error(), serverConfigSetting) {
				t.Errorf("the refusal does not name the config field: %q", err.Error())
			}

			// From --server. The flag wins over the saved value, so the
			// refusal must name the flag even though the saved address is
			// perfectly good.
			serverFlag = c.address
			got, err = effectiveServer("https://ordinary.example")
			serverFlag = ""
			if err == nil {
				t.Fatalf("effectiveServer accepted a %s address on --server and returned %q", c.name, got)
			}
			if !strings.Contains(err.Error(), serverFlagSetting) {
				t.Errorf("the refusal does not name --server: %q", err.Error())
			}

			// And the write boundary refuses it too.
			if saveErr := saveConfig(&SharkoConfig{Server: c.address, Token: "t"}); saveErr == nil {
				t.Errorf("saveConfig accepted a %s address", c.name)
			} else if !errors.Is(saveErr, credsafe.ErrServerAddressUnsupported) {
				t.Errorf("saveConfig refused, but not with the shared refusal: %v", saveErr)
			}
		})
	}
}

// TestSaveConfig_RefusesBeforeWriting proves the refusal is not a check that
// runs after the bytes are already on disk.
func TestSaveConfig_RefusesBeforeWriting(t *testing.T) {
	dir := isolatedConfigDir(t)
	path := filepath.Join(dir, "config")

	// Positive control: saveConfig CAN write here. Otherwise "no file
	// afterwards" would just mean the directory was unwritable.
	if err := saveConfig(&SharkoConfig{Server: "https://ordinary.example", Token: "t"}); err != nil {
		t.Fatalf("positive control failed: saveConfig cannot write to the isolated dir at all: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("clearing the control file: %v", err)
	}

	err := saveConfig(&SharkoConfig{Server: unsafeAddress, Token: "t"})
	if err == nil {
		t.Fatal("saveConfig persisted an unusable server address")
	}
	if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
		t.Fatalf("saveConfig refused, but not with the shared refusal: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("a config file exists after the refusal (stat error: %v)", statErr)
	}
	proveAbsent(t, "the saveConfig refusal", err.Error())
}

// ---------------------------------------------------------------------------
// What the refusal is allowed to say.
// ---------------------------------------------------------------------------

// TestRefusalMessage_SaysOnlyWhichSettingAndWhatToDo pins the sentence.
//
// It must carry two things and no third: which setting is unusable, and what
// the operator has to change. Not the value, not a piece of it, not its
// length, not a mask of it — a mask whose width follows the value is itself a
// measurement of the value.
func TestRefusalMessage_SaysOnlyWhichSettingAndWhatToDo(t *testing.T) {
	prev := serverFlag
	t.Cleanup(func() { serverFlag = prev })
	serverFlag = ""

	// The sentence must not vary with the value. Every carrier from the
	// config field produces the SAME words.
	var sentences []string
	for _, c := range everyCarrier() {
		_, e := effectiveServer(c.address)
		if e == nil {
			t.Fatalf("%s was not refused", c.name)
		}
		sentences = append(sentences, e.Error())
	}
	for i, s := range sentences {
		if s != sentences[0] {
			t.Errorf("the refusal changes with the address: sentence %d = %q but sentence 0 = %q", i, s, sentences[0])
		}
	}

	msg := sentences[0]

	// Says which setting.
	if !strings.Contains(msg, serverConfigSetting) {
		t.Errorf("the refusal does not name the setting to fix: %q", msg)
	}
	// Says what to do.
	if !strings.Contains(msg, "credential-free base URL") {
		t.Errorf("the refusal does not tell the operator what to change it to: %q", msg)
	}
	// Does NOT claim a credential was detected — it cannot know that, and
	// an ordinary "?ref=main" reaches this same sentence.
	for _, banned := range []string{"credential was", "token", "secret", "password", "detected"} {
		if strings.Contains(strings.ToLower(msg), banned) {
			t.Errorf("the refusal claims more than it can know (%q): %q", banned, msg)
		}
	}
	// Carries nothing of the value.
	proveAbsent(t, "the refusal sentence", msg)
	if strings.Contains(msg, "sharko.example") {
		t.Errorf("the refusal carries the host from the address: %q", msg)
	}
	for _, mask := range []string{"***", "xxx", "...", "REDACTED", "[redacted]"} {
		if strings.Contains(msg, mask) {
			t.Errorf("the refusal carries a mask of the value (%q): %q", mask, msg)
		}
	}
	// A length would be a measurement of the value.
	if strings.ContainsAny(msg, "0123456789") {
		t.Errorf("the refusal contains a number, which could be the value's length: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Sharko owns the transport error.
// ---------------------------------------------------------------------------

// TestTransportError_IsSharkosOwnWords is the leak that started BF10.
//
// The positive control is the important half: it builds the error Go itself
// would have produced for the same address and proves the sentinels are in it.
// Only then does finding them absent from Sharko's error mean anything.
func TestTransportError_IsSharkosOwnWords(t *testing.T) {
	dir := isolatedConfigDir(t)

	// An address pointing at a port nothing is listening on, WITHOUT any
	// userinfo, query or fragment — so it passes the structural check and
	// the request actually reaches the transport. This is the path that must
	// produce Sharko's own sentence rather than Go's.
	//
	// It carries sentinelPath in its PATH, and that is a deliberate
	// correction. This test used to certify Sharko's path with a completely
	// sentinel-free address, so the sentinels could not have appeared in
	// Sharko's message no matter what the code did — a clean result was
	// guaranteed rather than earned. The boundary refuses a credential
	// before this point, by design, so a credential cannot be planted here;
	// what CAN be planted is a piece of the address itself, and Sharko must
	// not reproduce that either. Said plainly rather than left as a
	// credential-free run standing in for a credential one.
	const deadPort = "http://127.0.0.1:1/" + sentinelPath

	// Positive control, part one: Go's own wrapped error for the same
	// address WITH credentials in it carries the sentinels.
	unsafeButSameShape := "http://" + sentinelUser + ":" + sentinelPass +
		"@127.0.0.1:1/" + sentinelPath + "/api/v1/health?q=" + sentinelQuery + "#" + sentinelFrag
	client := buildHTTPClient(false)
	req, err := http.NewRequest(http.MethodGet, unsafeButSameShape, nil)
	if err != nil {
		t.Fatalf("building the control request: %v", err)
	}
	client.Timeout = 3 * time.Second
	_, goErr := client.Do(req)
	if goErr == nil {
		t.Fatal("the control request unexpectedly succeeded; something IS listening on port 1")
	}
	goWrapped := fmt.Errorf("request failed: %w", goErr).Error()
	// Go masks the password half and nothing else — that asymmetry is the
	// whole reason url.Redacted() is not a fix, so pin it.
	provePresent(t, "Go's own wrapped transport error", goWrapped, []string{sentinelUser, sentinelQuery, sentinelFrag})
	if strings.Contains(goWrapped, sentinelPass) {
		t.Logf("note: Go no longer masks the password slot either; the control is only stronger")
	}
	var asURLErr *url.Error
	if !errors.As(goErr, &asURLErr) {
		t.Fatalf("the control error is not a *url.Error (%T), so it does not exercise the leaking type", goErr)
	}

	// Now Sharko's own path, with a credential-free address so the request
	// really is made and really does fail at the transport.
	writeRawConfig(t, dir, deadPort, "test-token")
	_, _, sharkoErr := apiGet("/api/v1/health")
	if sharkoErr == nil {
		t.Fatal("the request unexpectedly succeeded")
	}
	msg := sharkoErr.Error()

	// The raw *url.Error is neither wrapped nor exposed.
	var leaked *url.Error
	if errors.As(sharkoErr, &leaked) {
		t.Errorf("the raw *url.Error is still reachable from Sharko's error: %v", sharkoErr)
	}
	if strings.Contains(msg, "127.0.0.1:1") {
		t.Errorf("Sharko's transport error quotes the address it dialled: %q", msg)
	}
	// The planted piece of the address, found by the same search used above
	// on Go's own error. Go's version DOES carry it; Sharko's must not.
	provePresent(t, "Go's own wrapped transport error", goWrapped, []string{sentinelPath})
	proveAbsent(t, "Sharko's transport error", msg)
	if strings.Contains(msg, `Get "`) {
		t.Errorf("Sharko's transport error carries net/url's own rendering: %q", msg)
	}

	// It still carries the operation facts an operator needs.
	//
	// BF12-2 changed two words here. The sentence used to end with
	// credsafe.LogClass's answer, which appends the Go TYPE NAMES of the
	// error chain — "connection-refused chain=*url.Error>*net.OpError". That
	// belongs in a log line and not on an operator's screen, so the sentence
	// now ends with credsafe.PlainFailureReason's plain-English answer and
	// the type names must be absent.
	for _, want := range []string{"GET", "/api/v1/health", "the connection was refused"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Sharko's transport error dropped a useful, non-sensitive fact (%q): %q", want, msg)
		}
	}
	for _, unwanted := range []string{"chain=", "*url.Error", "*net.OpError"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("Sharko's transport error puts a Go type name (%q) on an operator's screen: %q", unwanted, msg)
		}
	}
}

// TestRequestBuildError_IsSharkosOwnWords covers the carrier BF12 found and
// BF10 had left open: the error from http.NewRequest, before any dial.
//
// # Why this needs its own test
//
// The transport test above exercises client.Do. The error that leaked was
// built one line earlier, by http.NewRequest, and that error is worse: it is
// url.Parse's own *url.Error, which quotes the whole address with NOTHING
// masked — not even the password half that net/http hides once a dial has
// started. Nothing planted anything into that carrier, nothing asserted on it,
// and `sharko version` printed it in full.
//
// # How an address gets this far
//
// The structural check accepts "[::1]:8080" — it is a perfectly ordinary
// scheme-less IPv6 endpoint with no user information, no query and no
// fragment, and an operator is entitled to write it. net/url will not build a
// request from it. So this is a real, supported address that reaches the
// build boundary and fails there, which is exactly the path that leaked.
//
// # The positive control is planted into THIS carrier
//
// Not into client.Do, and not into a different address. The same URL string
// Sharko builds is handed to http.NewRequest directly, and the sentinel is
// proved present in the error Go hands back. Only then does finding it absent
// from Sharko's own sentence mean anything.
func TestRequestBuildError_IsSharkosOwnWords(t *testing.T) {
	dir := isolatedConfigDir(t)

	// A supported, credential-free address net/url cannot build a request
	// from, with a sentinel in its path.
	const unbuildable = "[::1]:8080/" + sentinelPath

	// The boundary must LET THIS THROUGH, or the test never reaches the
	// carrier it is about.
	if err := credsafe.ValidateServerAddress(unbuildable); err != nil {
		t.Fatalf("the address check refuses %q, so this test never reaches http.NewRequest and "+
			"proves nothing about it: %v", unbuildable, err)
	}
	if err := saveConfig(&SharkoConfig{Server: unbuildable, Token: "test-token"}); err != nil {
		t.Fatalf("saveConfig refused a supported scheme-less IPv6 endpoint: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config")); statErr != nil {
		t.Fatalf("the config this test needs was not written: %v", statErr)
	}

	// Positive control, in the SAME carrier: Go's own error for the same URL
	// carries the sentinel and quotes the address whole.
	_, goErr := http.NewRequest(http.MethodGet, unbuildable+"/api/v1/health", nil)
	if goErr == nil {
		t.Fatalf("http.NewRequest now builds %q, so this address no longer exercises the build "+
			"failure and this test is not testing what it says it is", unbuildable)
	}
	var asURLErr *url.Error
	if !errors.As(goErr, &asURLErr) {
		t.Fatalf("the control error is not a *url.Error (%T), so it does not exercise the leaking type", goErr)
	}
	goWrapped := fmt.Errorf("cannot create request: %w", goErr).Error()
	provePresent(t, "Go's own wrapped request-build error", goWrapped, []string{sentinelPath})
	if !strings.Contains(goWrapped, "[::1]:8080") {
		t.Fatalf("the control does not quote the address, so it is not the leak this test is about: %q", goWrapped)
	}

	// Now Sharko's own path.
	_, _, sharkoErr := apiGet("/api/v1/health")
	if sharkoErr == nil {
		t.Fatal("the request unexpectedly succeeded against an address net/url cannot build")
	}
	msg := sharkoErr.Error()

	var leaked *url.Error
	if errors.As(sharkoErr, &leaked) {
		t.Errorf("the raw *url.Error from http.NewRequest is still reachable from Sharko's error: %v", sharkoErr)
	}
	proveAbsent(t, "Sharko's request-build error", msg)
	for _, quoted := range []string{"[::1]:8080", "[::1]", `parse "`, "first path segment"} {
		if strings.Contains(msg, quoted) {
			t.Errorf("Sharko's request-build error reproduces the address or net/url's own rendering "+
				"(%q): %q", quoted, msg)
		}
	}
	// It still carries the operation facts an operator needs, and no Go type
	// names.
	for _, want := range []string{"GET", "/api/v1/health"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Sharko's request-build error dropped a useful, non-sensitive fact (%q): %q", want, msg)
		}
	}
	for _, unwanted := range []string{"chain=", "*url.Error"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("Sharko's request-build error puts a Go type name (%q) on an operator's screen: %q", unwanted, msg)
		}
	}
}

// TestVersionCommand_NeverPrintsAnAddressItCannotBuildARequestFrom is the same
// carrier, driven through the command that printed it.
//
// `sharko version` was the sharpest form of the BF12 leak: credsafe correctly
// declined to name the address, and then the wrapped build error printed it in
// full on the same line.
//
// # What this test is allowed to claim, exactly
//
// The "Server:" half of the line legitimately shows a credential-free address
// — that is what the command is for, and TestCredentialFreeAddress_Still
// DrivesARealCommandEndToEnd pins it. So this test reads ONLY the reason in
// the brackets, which is the half that came from the failure, and requires
// that half to be Sharko's own words with nothing of the address in it.
//
// With BF12-1 in place a credential-bearing address never gets this far — it
// is refused at effectiveServer and the command returns before printing
// anything. That is why the address here is credential-free with a sentinel in
// its path: it is the only shape that can actually reach the build failure,
// and reproducing it is still wrong, because a raw *url.Error here would
// reproduce whatever the operator wrote.
func TestVersionCommand_NeverPrintsAnAddressItCannotBuildARequestFrom(t *testing.T) {
	dir := isolatedConfigDir(t)
	const unbuildable = "[::1]:8080/" + sentinelPath

	// saveConfig, not a raw write: an address with brackets in it is not
	// valid unquoted YAML, and a hand-written "server: [::1]:8080/..." line
	// does not load — the command would then never reach the build boundary
	// and this test would report clean having exercised nothing.
	if err := saveConfig(&SharkoConfig{Server: unbuildable, Token: "test-token"}); err != nil {
		t.Fatalf("saveConfig refused a supported scheme-less IPv6 endpoint: %v", err)
	}
	onDisk, readErr := os.ReadFile(filepath.Join(dir, "config"))
	if readErr != nil {
		t.Fatalf("reading back the config this test wrote: %v", readErr)
	}
	provePresent(t, "the config file this test wrote", string(onDisk), []string{sentinelPath})

	output, err := runCommand(t, "sharko version", nil)
	if err != nil {
		t.Fatalf("sharko version returned an error instead of reporting the server as unreachable: %v\n\noutput:\n%s", err, output)
	}

	// Not vacuous: the command has to have got as far as trying, or there is
	// no build error for it to have printed or not printed.
	const marker = "(unreachable: "
	i := strings.Index(output, marker)
	if i < 0 {
		t.Fatalf("sharko version did not reach the point of trying to talk to the server, so this "+
			"test read nothing about the error it would have printed:\n%s", output)
	}
	reason := output[i+len(marker):]

	// The CLI version line is still printed.
	if !strings.Contains(output, "Sharko CLI:") {
		t.Errorf("the CLI version line disappeared: %q", output)
	}

	proveAbsent(t, "the reason sharko version printed for an unbuildable address", reason)
	for _, quoted := range []string{"[::1]", "8080", `parse "`, "first path segment"} {
		if strings.Contains(reason, quoted) {
			t.Errorf("sharko version's failure reason reproduces the address or net/url's own "+
				"rendering (%q): %q", quoted, reason)
		}
	}
}

// ---------------------------------------------------------------------------
// The regression that matters most: ordinary addresses still work.
// ---------------------------------------------------------------------------

func TestCredentialFreeAddresses_KeepWorkingExactlyAsBefore(t *testing.T) {
	for _, address := range []string{
		"https://sharko.example",
		"https://sharko.example:8443",
		"https://sharko.example/base/path",
		"http://localhost:8080",
		"localhost:8080",
		"",
	} {
		t.Run(address, func(t *testing.T) {
			prev := serverFlag
			t.Cleanup(func() { serverFlag = prev })
			serverFlag = ""

			got, err := effectiveServer(address)
			if err != nil {
				t.Fatalf("a credential-free address was refused: %v", err)
			}
			// Returned EXACTLY as given — not normalised, not rewritten.
			if got != address {
				t.Errorf("the address came back changed: got %q, want %q", got, address)
			}

			serverFlag = address
			got, err = effectiveServer("https://saved.example")
			if err != nil {
				t.Fatalf("a credential-free --server was refused: %v", err)
			}
			want := address
			if address == "" {
				want = "https://saved.example" // empty flag falls back
			}
			if got != want {
				t.Errorf("resolution changed: got %q, want %q", got, want)
			}
		})
	}
}

// TestCredentialFreeAddress_StillDrivesARealCommandEndToEnd is the same
// regression proved the whole way through: saved to disk by saveConfig, read
// back, resolved, dialled, and printed.
func TestCredentialFreeAddress_StillDrivesARealCommandEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"9.9.9","mode":"local"}`))
	}))
	t.Cleanup(srv.Close)

	dir := isolatedConfigDir(t)
	if err := saveConfig(&SharkoConfig{Server: srv.URL, Token: "test-token"}); err != nil {
		t.Fatalf("saveConfig refused an ordinary address: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config")); err != nil {
		t.Fatalf("saveConfig did not write the config: %v", err)
	}

	output, err := runCommand(t, "sharko version", nil)
	if err != nil {
		t.Fatalf("sharko version failed against an ordinary address: %v\n\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Server: "+srv.URL) {
		t.Errorf("the server address is no longer shown as it was: %q", output)
	}
	if !strings.Contains(output, "connected, 9.9.9, local") {
		t.Errorf("the health detail is no longer shown: %q", output)
	}
}

// TestVersionCommand_NeverPrintsAnUnsafeAddress covers the surface that
// printed the address raw — including the password slot, which Go had already
// masked one line lower.
func TestVersionCommand_NeverPrintsAnUnsafeAddress(t *testing.T) {
	for _, shape := range everyUnsafeComposite() {
		t.Run(shape.name, func(t *testing.T) {
			versionCommandRefuses(t, shape)
		})
	}
}

func versionCommandRefuses(t *testing.T, shape unsafeComposite) {
	t.Helper()
	dir := isolatedConfigDir(t)
	onDisk := writeRawConfig(t, dir, shape.address, "test-token")
	provePresent(t, "the legacy config file this test wrote", onDisk, shape.sentinels)

	output, err := runCommand(t, "sharko version", nil)
	if err == nil {
		t.Fatalf("sharko version reported on an unusable address instead of refusing.\n\noutput:\n%s", output)
	}
	if !errors.Is(err, credsafe.ErrServerAddressUnsupported) {
		t.Fatalf("sharko version failed, but not with the shared refusal: %v", err)
	}
	// The CLI version line is still printed; only the server line is gone.
	if !strings.Contains(output, "Sharko CLI:") {
		t.Errorf("the CLI version line disappeared: %q", output)
	}
	if strings.Contains(output, "Server:") {
		t.Errorf("a 'Server:' line was printed for an address Sharko declined to use: %q", output)
	}
	proveAbsent(t, "sharko version", output+"\n"+err.Error())
}
