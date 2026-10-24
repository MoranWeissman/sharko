package advisories

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EP3 dry run round S, S15: a login written into the chart repository
// address is never sent to ArtifactHub and never comes back in the error.
func TestArtifactHub_LoginInTheAddress_NotSentAndNotInTheError(t *testing.T) {
	const token = "ghp_FAKEroundS15token"
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	orig := artifactHubBaseURL
	setArtifactHubBaseURL(srv.URL)
	defer setArtifactHubBaseURL(orig)

	src := newArtifactHubSource(srv.Client())
	_, err := src.Get(context.Background(), "https://sharko:"+token+"@charts.example.com/stable", "nginx")
	if err == nil {
		t.Fatal("want an error: no repository was found")
	}
	if strings.Contains(asked, token) || !strings.Contains(asked, "charts.example.com") {
		t.Fatalf("search query = %q", asked)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Warn("advisories", "error", err)
	if strings.Contains(err.Error()+buf.String(), token) {
		t.Fatalf("the token leaked: %s", buf.String())
	}

	_, err = src.Get(context.Background(), "https://sharko:"+token+"@cha rts.example.com/stable", "nginx")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unreadable address: err = %v", err)
	}
}
