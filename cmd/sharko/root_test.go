package main

import (
	"strings"
	"testing"
)

// V124-3.5 / BUG-010 — assert that --server is registered as a persistent
// flag on rootCmd so every subcommand (list-clusters, status, version, etc.)
// inherits it without redefining.
//
// Pre-fix behaviour: --server was registered only on `loginCmd` and a couple
// of other commands. `sharko list-clusters --server URL` therefore failed
// with "unknown flag: --server" because the flag did not exist on that
// subcommand. Promoting it to rootCmd's persistent flag set fixes this for
// every present and future subcommand at once.

func TestRootCmd_ServerFlag_IsPersistent(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("server")
	if flag == nil {
		t.Fatal("rootCmd persistent flag --server is not registered (BUG-010 regression)")
	}
	if flag.Name != "server" {
		t.Errorf("flag name = %q, want %q", flag.Name, "server")
	}
	// Defaults to empty so callers fall back to the saved-config server URL.
	if flag.DefValue != "" {
		t.Errorf("flag default = %q, want empty (saved-config fallback)", flag.DefValue)
	}
}

// TestRootCmd_ServerFlag_InheritedBySubcommands confirms that subcommands
// see --server as an inherited persistent flag rather than reporting
// "unknown flag". We sample a representative read-only command (`version`)
// because it doesn't require auth state to construct.
func TestRootCmd_ServerFlag_InheritedBySubcommands(t *testing.T) {
	// Find each subcommand registered on rootCmd and verify the inherited
	// flag set exposes --server. Skipping the root itself.
	for _, sub := range rootCmd.Commands() {
		t.Run(sub.Name(), func(t *testing.T) {
			inherited := sub.InheritedFlags()
			if inherited == nil {
				t.Fatalf("subcommand %q has nil InheritedFlags", sub.Name())
			}
			if inherited.Lookup("server") == nil {
				t.Errorf("subcommand %q does not inherit --server flag", sub.Name())
			}
		})
	}
}

// TestLoginCmd_DoesNotRedefineServerFlag confirms loginCmd no longer
// registers its own --server flag; it relies on the inherited persistent one.
// Pre-fix loginCmd defined --server locally, which shadowed the persistent
// flag and forced every other command to define its own — the root cause of
// BUG-010.
func TestLoginCmd_DoesNotRedefineServerFlag(t *testing.T) {
	if loginCmd.Flags().Lookup("server") != nil &&
		loginCmd.LocalFlags().Lookup("server") != nil {
		t.Error("loginCmd has a local --server flag; it must rely on rootCmd's persistent flag (BUG-010)")
	}
}

// TestEffectiveServer_PrefersFlagOverConfig pins the resolution order used by
// apiRequest and version_cmd. The flag, when set, MUST override the
// saved-config server URL; when empty, the saved value MUST be returned
// unchanged (saved-config fallback — explicit AC requirement).
func TestEffectiveServer_PrefersFlagOverConfig(t *testing.T) {
	// Save and restore the package-level flag to keep tests hermetic.
	prev := serverFlag
	t.Cleanup(func() { serverFlag = prev })

	// Flag empty → saved value wins (existing behaviour, must keep working).
	serverFlag = ""
	if got := effectiveServer("https://saved.example.com"); got != "https://saved.example.com" {
		t.Errorf("with empty flag, want saved value, got %q", got)
	}

	// Flag set → flag wins over saved.
	serverFlag = "https://override.example.com"
	if got := effectiveServer("https://saved.example.com"); got != "https://override.example.com" {
		t.Errorf("with flag set, want override, got %q", got)
	}

	// Both empty → empty (login uses this to detect "required" error).
	serverFlag = ""
	if got := effectiveServer(""); got != "" {
		t.Errorf("with both empty, want empty, got %q", got)
	}
}

// TestLoginCmd_RequiresServer asserts the login command refuses to run
// without --server. Implementation note: the check moved out of cobra's
// MarkFlagRequired into loginCmd's RunE because --server is now a persistent
// flag on rootCmd, and we don't want to force --server on commands that have
// a saved-config fallback.
func TestLoginCmd_RequiresServer(t *testing.T) {
	prev := serverFlag
	t.Cleanup(func() { serverFlag = prev })
	serverFlag = ""

	err := loginCmd.RunE(loginCmd, nil)
	if err == nil {
		t.Fatal("expected error when --server is not set; got nil")
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("error should mention the missing flag, got: %v", err)
	}
}
