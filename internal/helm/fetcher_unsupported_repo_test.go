// fetcher_unsupported_repo_test.go — Sharko never dials an address it would
// not save.
//
// This is the "never connect using that address" half of the rule. It is
// proved by counting requests at a real HTTP server: the clean address must
// reach it (otherwise the counter proves nothing), and the address carrying
// sign-in details must not.
package helm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

const dialSecret = "P4ss-w0rd-the-operator-actually-stored-5m1v"
const dialSweptSecret = "P4ss-w0rd-the-operator-actually-stored-5m1v"

func TestDialSentinelsAgree(t *testing.T) {
	if dialSecret != dialSweptSecret || dialSecret == "" {
		t.Fatalf("planted %q and swept %q disagree, or are empty", dialSecret, dialSweptSecret)
	}
}

func TestFetcher_NeverDialsAnUnsupportedAddress(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte("entries:\n  keda:\n    - version: 2.13.0\n      urls: [\"https://example.invalid/keda.tgz\"]\n"))
	}))
	defer srv.Close()

	ctx := context.Background()

	// Positive control: the clean address DOES reach the server. Without
	// this, a counter that stays at zero would prove only that the test is
	// broken.
	if _, err := NewFetcher().ListVersions(ctx, srv.URL, "keda"); err != nil {
		t.Fatalf("the clean address failed for an unrelated reason, so the counter below proves nothing: %v", err)
	}
	if hits != 1 {
		t.Fatalf("the clean address produced %d requests, want exactly 1 — the counter is not measuring what this test thinks it is", hits)
	}

	// Every carrier, every public method that can reach the network.
	carriers := map[string]string{
		"userinfo with a password":                     "https://git-user:" + dialSecret + "@" + strings.TrimPrefix(srv.URL, "http://"),
		"userinfo without a password":                  "https://" + dialSecret + "@" + strings.TrimPrefix(srv.URL, "http://"),
		"a query string":                               srv.URL + "?access_token=" + dialSecret,
		"a fragment":                                   srv.URL + "#" + dialSecret,
		"an ordinary query string, refused on purpose": srv.URL + "?ref=main",
	}
	if len(carriers) != 5 {
		t.Fatalf("expected exactly 5 carriers, have %d", len(carriers))
	}

	methods := map[string]func(f *Fetcher, repo string) error{
		"ListVersions": func(f *Fetcher, repo string) error {
			_, err := f.ListVersions(ctx, repo, "keda")
			return err
		},
		"ListCharts": func(f *Fetcher, repo string) error {
			_, err := f.ListCharts(ctx, repo)
			return err
		},
		"FindNearestVersion": func(f *Fetcher, repo string) error {
			_, err := f.FindNearestVersion(ctx, repo, "keda", "2.13.0")
			return err
		},
		"FetchValues": func(f *Fetcher, repo string) error {
			_, err := f.FetchValues(ctx, repo, "keda", "2.13.0")
			return err
		},
		"FetchReleaseNotes": func(f *Fetcher, repo string) error {
			_, err := f.FetchReleaseNotes(ctx, repo, "keda", "2.13.0")
			return err
		},
	}
	if len(methods) != 5 {
		t.Fatalf("expected exactly 5 methods under test, have %d", len(methods))
	}

	before := hits
	for cname, repo := range carriers {
		for mname, run := range methods {
			t.Run(cname+"/"+mname, func(t *testing.T) {
				err := run(NewFetcher(), repo)
				if err == nil {
					t.Fatal("the call was allowed — Sharko reached out using this address")
				}
				var typed *credsafe.UnsupportedRepoURLError
				if !errors.As(err, &typed) {
					t.Fatalf("the refusal is not a *credsafe.UnsupportedRepoURLError: %v", err)
				}
				if strings.Contains(err.Error(), dialSweptSecret) || strings.Contains(err.Error(), "git-user") {
					t.Errorf("the refusal repeats the address: %s", err.Error())
				}
			})
		}
	}

	if hits != before {
		t.Errorf("the server was contacted %d more time(s) after the clean call — a refused address still reached the network", hits-before)
	}
}
