package security

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// EP3 dry run round S, S15: the SSRF guard's error never carries a login
// written into the address, nor the parser's own text (which repeats it).
const roundSFakeToken = "ghp_FAKEroundS15token"

func TestSSRFError_TokenNeverInTheErrorOrTheLog(t *testing.T) {
	for _, raw := range []string{
		"https://sharko:" + roundSFakeToken + "@git example.com/o/r",
		"ftp://sharko:" + roundSFakeToken + "@charts.example.com/o/r",
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Fatalf("%q was let through", raw)
		}
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Error("blocked", "error", err)
		if strings.Contains(err.Error()+buf.String(), roundSFakeToken) {
			t.Fatalf("the token leaked: %s", buf.String())
		}
	}
	if got := (&SSRFError{URL: "https://sharko:" + roundSFakeToken + "@charts.example.com/x", Reason: "host"}).Error(); got != `URL "https://charts.example.com/x" blocked by SSRF guard: host` {
		t.Fatalf("Error() = %q", got)
	}
	if got := (&SSRFError{URL: "https://a b/", Reason: "malformed"}).Error(); got != "the address was blocked by SSRF guard: malformed" {
		t.Fatalf("Error() = %q", got)
	}
}
