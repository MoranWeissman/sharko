package helm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newIndexServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" || status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		fmt.Fprint(w, "apiVersion: v1\nentries:\n  podinfo:\n  - version: 6.8.0\n    urls: [podinfo-6.8.0.tgz]\n  - version: 6.7.1\n    urls: [podinfo-6.7.1.tgz]\n  certs:\n  - version: v1.16.3\n    urls: [certs-v1.16.3.tgz]\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// HasVersion keeps three answers apart: found, read-and-not-there, and
// could-not-read. The middle one is the caller's typo; the last one is not.
func TestHasVersion(t *testing.T) {
	srv := newIndexServer(t, http.StatusOK)
	f := NewFetcher()
	ctx := context.Background()

	for _, tc := range []struct {
		chart, version string
		wantFound      bool
		wantResolved   string
	}{
		{"podinfo", "6.8.0", true, "6.8.0"},
		{"podinfo", "99.0.0", false, ""},
		{"podinfo", "abc", false, ""},
		{"podinfo", "", false, ""},
		{"certs", "1.16.3", true, "v1.16.3"}, // a missing leading "v" still finds it
	} {
		resolved, found, err := f.HasVersion(ctx, srv.URL, tc.chart, tc.version)
		if err != nil {
			t.Fatalf("%s@%s: unexpected error %v", tc.chart, tc.version, err)
		}
		if found != tc.wantFound || resolved != tc.wantResolved {
			t.Errorf("%s@%q: found=%v resolved=%q, want found=%v resolved=%q", tc.chart, tc.version, found, resolved, tc.wantFound, tc.wantResolved)
		}
	}

	broken := newIndexServer(t, http.StatusInternalServerError)
	if _, found, err := NewFetcher().HasVersion(ctx, broken.URL, "podinfo", "6.8.0"); err == nil || found {
		t.Errorf("a repository that cannot be read must be an error, not \"not found\": found=%v err=%v", found, err)
	}

	if _, _, err := NewFetcher().HasVersion(ctx, "oci://registry.example/charts", "podinfo", "6.8.0"); !errors.Is(err, ErrOCIVersionCheckUnsupported) {
		t.Errorf("an oci:// registry must answer the unsupported sentinel, got %v", err)
	}
}

// Walk 4 finding 11: Analyze printed "<nil>" and "[map[name:x]]".
func TestFormatValue_ReadsLikeYAMLNotLikeGo(t *testing.T) {
	_, _, changed, err := DiffValues("a:\nb:\n  - name: x\nc: 1\n", "a: set\nb:\n  - name: y\nc: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range changed {
		got[d.Path] = d.OldValue + " -> " + d.NewValue
	}
	want := map[string]string{
		"a": "null -> set",
		"b": `[{"name":"x"}] -> [{"name":"y"}]`,
		"c": "1 -> 2",
	}
	for path, w := range want {
		if got[path] != w {
			t.Errorf("%s: got %q, want %q", path, got[path], w)
		}
	}
	for path, v := range got {
		for _, banned := range []string{"<nil>", "map["} {
			if contains(v, banned) {
				t.Errorf("%s still prints Go's own form %q: %q", path, banned, v)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
