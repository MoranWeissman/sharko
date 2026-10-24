package sources

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// EP3 dry run round S, S15: an address that cannot be read never comes back
// in the error, so a token written into it is not in the error or the log.
func TestRuntimeSSRFCheck_Unreadable_TokenNeverInTheErrorOrTheLog(t *testing.T) {
	const token = "ghp_FAKEroundS15token"
	_, err := runtimeSSRFCheckResolvedIPs("https://sharko:" + token + "@cat alog.example.com/c.yaml")
	if err == nil || err.Error() != "the catalog source address cannot be read" {
		t.Fatalf("err = %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Warn("blocked", "err", err)
	if strings.Contains(err.Error()+buf.String(), token) {
		t.Fatalf("the token leaked: %s", buf.String())
	}
}
