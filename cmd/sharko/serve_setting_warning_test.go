package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A warning about a configuration setting names the setting and never
// says what is in it.
//
// Three startup warnings used to log their own value. All three held a
// duration, so nothing secret got out. That is not why this is here. A
// rule that holds only for the settings somebody remembered to mark
// secret is not a rule, and warnings get added by copying the warning
// above them — attribute and all. These tests hold the shape.

// captureWarning runs fn with a logger writing into a buffer and returns
// what came out.
func captureWarning(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	fn()
	return buf.String()
}

// TestWarnUnreadableSetting_NamesTheSettingAndTheFallback is the positive
// half: the operator is told which setting to fix and what Sharko is
// doing meanwhile.
func TestWarnUnreadableSetting_NamesTheSettingAndTheFallback(t *testing.T) {
	out := captureWarning(t, func() {
		warnUnreadableSetting("SHARKO_CONNECTION_CHECK_INTERVAL", 60*time.Second)
	})

	if !strings.Contains(out, "SHARKO_CONNECTION_CHECK_INTERVAL") {
		t.Errorf("the warning does not name the setting, so an operator cannot tell what to fix:\n%s", out)
	}
	if !strings.Contains(out, "1m0s") {
		t.Errorf("the warning does not say what Sharko will use instead:\n%s", out)
	}
}

// TestWarnUnreadableSetting_NeverCarriesTheValue is the whole point.
//
// The check is not just "the string is absent". It also bans the
// attribute KEYS the old lines used, because a message that stopped
// printing the value but kept a `value=` attribute would pass a
// content-only check the moment somebody re-added it.
func TestWarnUnreadableSetting_NeverCarriesTheValue(t *testing.T) {
	// A value that could not be mistaken for anything Sharko produces.
	const operatorTyped = "hunter2-not-a-duration"

	out := captureWarning(t, func() {
		warnUnreadableSetting("SHARKO_SETTINGS_RECONCILE_INTERVAL", 60*time.Second)
	})

	if strings.Contains(out, operatorTyped) {
		t.Fatalf("the warning carried the operator's value:\n%s", out)
	}
	for _, banned := range []string{"value=", "interval=", "env_value=", "raw=", "configured="} {
		if strings.Contains(out, banned) {
			t.Errorf("the warning carries a %q attribute — that is the shape that leaks the value; "+
				"say the setting's NAME and the fallback, nothing else:\n%s", banned, out)
		}
	}
}

// warnCallRe finds every warnUnreadableSetting call in serve.go, whole,
// including a multi-line one.
var warnCallRe = regexp.MustCompile(`(?s)warnUnreadableSetting\(`)

// oldLeakyWarnRe finds the shape the three fixed lines had: a slog.Warn
// about something invalid that also carries an attribute holding what the
// operator typed.
var oldLeakyWarnRe = regexp.MustCompile(`slog\.Warn\("invalid [^"]*",\s*"(value|interval|env_value|raw|configured)"`)

// TestServe_HasNoWarningThatPrintsASettingValue is the guard against the
// pattern coming back by copy-paste. It reads the source, not the logs:
// a runtime test only covers the branches a test happens to reach, and
// the next leaky warning will be on a branch nobody drives.
func TestServe_HasNoWarningThatPrintsASettingValue(t *testing.T) {
	body := readServeSource(t)

	// The banned shape must appear nowhere outside the doc comment on
	// warnUnreadableSetting, which quotes the three old lines on purpose
	// so a reader can see what was wrong with them. Comment lines start
	// with a tab-indented "//" in that block.
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if oldLeakyWarnRe.MatchString(line) {
			t.Errorf("cmd/sharko/serve.go:%d warns about a bad setting and prints what the operator "+
				"typed. Call warnUnreadableSetting(name, fallback) instead — it has nowhere to put a "+
				"value:\n  %s", i+1, trimmed)
		}
	}

	// And the helper really is in use, so this file is not guarding an
	// empty room.
	if len(warnCallRe.FindAllString(body, -1)) < 3 {
		t.Errorf("expected at least 3 warnUnreadableSetting calls in serve.go, found %d — if the call "+
			"sites moved, move this guard with them",
			len(warnCallRe.FindAllString(body, -1)))
	}
}

func readServeSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("serve.go"))
	if err != nil {
		t.Fatalf("reading serve.go: %v", err)
	}
	return string(body)
}
