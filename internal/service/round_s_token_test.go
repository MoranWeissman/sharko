package service

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round S, S15: an address Sharko cannot read never comes back
// in the error, so a token written into it is not in the answer or the log.
const roundSFakeToken = "FAKEroundS15tokenValue"

func TestDeriveGiteaBaseURL_TokenNeverInTheErrorOrTheLog(t *testing.T) {
	_, err := deriveGiteaBaseURL("https://sharko:" + roundSFakeToken + "@git example.com/o/r")
	if err == nil || err.Error() != models.UnreadableRepoAddressSentence {
		t.Fatalf("err = %v, want %q", err, models.UnreadableRepoAddressSentence)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Error("connection", "error", err)
	if strings.Contains(err.Error()+buf.String(), roundSFakeToken) {
		t.Fatalf("the token leaked: %s", buf.String())
	}
}
