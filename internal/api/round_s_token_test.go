package api

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round S, S15: an address Sharko cannot read never comes back
// in the error, so a token written into it is not in the answer or the log.
const roundSFakeToken = "ghp_FAKEroundS15token"

func roundSUnreadable() string {
	return "https://sharko:" + roundSFakeToken + "@git example.com/o/r"
}

func roundSNoTokenInLog(t *testing.T, err error) {
	t.Helper()
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Error("check", "error", err)
	if strings.Contains(err.Error()+buf.String(), roundSFakeToken) {
		t.Fatalf("the token leaked: %s", buf.String())
	}
}

func TestDeriveBaseURL_TokenNeverInTheErrorOrTheLog(t *testing.T) {
	_, err := deriveBaseURL(roundSUnreadable())
	if err == nil || err.Error() != models.UnreadableRepoAddressSentence {
		t.Fatalf("err = %v, want %q", err, models.UnreadableRepoAddressSentence)
	}
	roundSNoTokenInLog(t, err)
}

func TestValidateRepoURL_Unreadable_TokenNeverInTheErrorOrTheLog(t *testing.T) {
	err := validateRepoURL(roundSUnreadable())
	want := "Sharko cannot read the chart repository address. Check that it looks like https://host/path."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if strings.Contains(err.Error(), "malformed URL") {
		t.Fatalf("old wording: %q", err)
	}
	roundSNoTokenInLog(t, err)
}
