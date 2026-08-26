package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/logging"
)

var version = "4.0.0-dev"
var commit = "dev"

var rootCmd = &cobra.Command{
	Use:     "sharko",
	Short:   "Addon management for Kubernetes clusters, built on ArgoCD",
	Version: version,
}

// serverFlag holds the value of the global --server flag, registered as a
// persistent flag on rootCmd so every subcommand inherits it uniformly.
//
// Resolution at call sites:
//   - When non-empty, this overrides the server URL stored in the saved CLI
//     config (~/.sharko/config).
//   - When empty, callers fall back to the saved config.
//
// `sharko login` is the one exception: it always requires --server because it
// runs before any saved config exists. The required-flag check lives in
// loginCmd's RunE rather than via cobra.MarkFlagRequired so we don't depend on
// init() ordering between root.go and login.go.
var serverFlag string

func init() {
	rootCmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification (for self-signed certs)")
	rootCmd.PersistentFlags().StringVar(&serverFlag, "server", "", "Sharko server URL (overrides saved config)")
}

// Names for the two settings a server address can come from. They appear in
// the refusal message so an operator knows which one to go and change, and
// they are fixed strings written here — never anything derived from the value.
const (
	serverFlagSetting   = "--server"
	serverConfigSetting = "the server field in the Sharko CLI config file"
)

// effectiveServer returns the server URL the CLI should use for an API call,
// or refuses it.
//
// Precedence:
//  1. --server flag (persistent on rootCmd) when non-empty
//  2. saved config (~/.sharko/config) value passed in by the caller
//
// Callers that have already loaded the saved config pass it as savedServer.
// Callers that have not (e.g. `sharko login`, where there is no saved config
// yet) should pass an empty string and treat the result as authoritative.
//
// # Why this returns an error now (BF10)
//
// This is the one place "flag or saved config" is decided, so it is also the
// only place that can guarantee an address is checked before anything is done
// with it. A Sharko server address is credential material — it can be written
// https://user:token@sharko.example — and the two things the CLI did with it
// next both handed it back to the operator's screen: Go's HTTP transport
// quotes the address inside its own error and masks only the password half,
// and some commands printed the resolved address themselves.
//
// Checking here, and returning an error rather than a cleaned-up string, makes
// the guarantee one the compiler enforces: a caller cannot get an address out
// of this function without also having to deal with the refusal, and there is
// no path that reaches the network with an address nobody looked at. The rule
// itself is credsafe's; this function only knows which setting to name.
//
// An address that carries no credential is returned exactly as it was given,
// so nothing changes for an operator whose config is ordinary.
func effectiveServer(savedServer string) (string, error) {
	raw, setting := savedServer, serverConfigSetting
	if serverFlag != "" {
		raw, setting = serverFlag, serverFlagSetting
	}
	if err := credsafe.ValidateServerAddressAt(setting, raw); err != nil {
		return "", err
	}
	return raw, nil
}

// newCLIHandler builds the log handler every non-`serve` command runs:
// redaction wrapped around a plain text handler that writes straight to
// standard error.
//
// The inner handler is a NEW one. It is deliberately NOT built from
// `slog.Default().Handler()`. Go's stock default handler does not write to a
// writer of its own — it writes through the standard `log` package, and that
// package asks `slog.Default()` for a handler again at write time. Once this
// wrapper had been installed as the default, that lookup returned this
// wrapper, so every line a command logged went round in a circle back into a
// lock it was already holding and the command stopped responding for good.
// Handing the inner handler its own writer gives the chain an end.
//
// Redaction stays exactly where it was: this wrapper is still the single
// place credential-shaped values are cleaned, and every non-serve command
// still goes through it. What changed is the format — these lines now come
// out in Go's standard `time=... level=... msg=...` text form instead of the
// stock default handler's shape. `sharko serve` is unaffected; it replaces
// this a moment later with the level-configured JSON chain from
// logging.NewHandler.
//
// There is one definition of this chain on purpose, so the test that proves
// a command still logs and still redacts is exercising the chain that ships
// rather than a copy of it.
func newCLIHandler() slog.Handler {
	inner := slog.NewTextHandler(os.Stderr, nil)
	return logging.NewRedactHandler(inner)
}

func Execute() {
	// Redaction is turned on for EVERY command, not just `serve` (B9).
	// Without this, a credentials or Git error logged from a library that
	// `sharko validate-config` or `sharko init` calls went to stderr with its
	// words intact. See newCLIHandler for what the chain is and why.
	slog.SetDefault(slog.New(newCLIHandler()))

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
