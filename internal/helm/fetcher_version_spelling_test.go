// fetcher_version_spelling_test.go — a chart version pinned without the
// leading "v" still finds the chart.
//
// Helm repositories disagree about the leading "v". The jetstack index
// publishes cert-manager as "v1.16.3" and carries no bare "1.16.x" entry at
// all, so an addon pinned to "1.16.3" used to miss the lookup entirely and
// the request failed with "version 1.16.3 not found for chart cert-manager".
// Both FetchValues and fetchChartYAML carried their own copy of the
// strict-equality loop, so the same miss existed twice.
//
// Everything here is served by httptest — a fake index.yaml plus fake chart
// archives — so no test in this file touches the real internet.
//
// The assertions are on the CHOSEN ARCHIVE, not on "the call succeeded". A
// success-only check cannot tell a correct choice from a lucky one when the
// index carries both spellings.
package helm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const spellingChartName = "spelling-chart"

// indexEntry is one version: entry in the fake index.yaml. archive is a
// short label; the archive's URL and its values.yaml body are both built
// from it, so a test can name the archive it expects and the returned
// values body proves which one was downloaded.
//
// An empty archive means the entry is published with an empty urls: list —
// the "nothing to download here" case the lookup has always skipped.
type indexEntry struct {
	version string
	archive string
}

// fakeRepo is an httptest-served Helm repository: one index.yaml and one
// .tgz per named archive, plus a record of every path that was requested.
type fakeRepo struct {
	url string

	mu       sync.Mutex
	requests []string
}

func (r *fakeRepo) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *fakeRepo) countPath(want string) int {
	var n int
	for _, p := range r.paths() {
		if p == want {
			n++
		}
	}
	return n
}

func archivePath(archive string) string {
	return "/archives/" + archive + ".tgz"
}

// valuesBodyFor is the values.yaml content baked into a named archive. The
// archive label is inside it, so the string FetchValues returns identifies
// which archive was downloaded.
func valuesBodyFor(archive string) string {
	return "downloadedArchive: " + archive + "\nreplicaCount: 1\n"
}

// chartYAMLBody deliberately points sources: and home: at addresses that
// are not GitHub. FetchReleaseNotes then finds no GitHub repository and
// returns its "not available" sentence without reaching out to
// api.github.com, which keeps this file free of real network calls.
func chartYAMLBody(archive string) string {
	return "name: " + spellingChartName + "\n" +
		"version: 0.0.0\n" +
		"home: https://charts.example.invalid/" + archive + "\n" +
		"sources:\n" +
		"- https://git.example.invalid/" + archive + "\n"
}

// makeChartArchive builds a .tgz holding <chart>/Chart.yaml and
// <chart>/values.yaml, which is the layout both fetch paths look for.
func makeChartArchive(t *testing.T, archive string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name, body string) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}

	write(spellingChartName+"/Chart.yaml", chartYAMLBody(archive))
	write(spellingChartName+"/values.yaml", valuesBodyFor(archive))

	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
	return buf.Bytes()
}

// startFakeRepo serves an index.yaml built from entries, plus one archive
// per distinct non-empty archive label.
func startFakeRepo(t *testing.T, entries []indexEntry) *fakeRepo {
	t.Helper()

	var idx strings.Builder
	idx.WriteString("entries:\n  " + spellingChartName + ":\n")
	archives := map[string][]byte{}
	for _, e := range entries {
		// Versions are always quoted: unquoted "1.2" would parse as a
		// YAML float and stop being the string the lookup compares.
		fmt.Fprintf(&idx, "    - version: %q\n", e.version)
		if e.archive == "" {
			idx.WriteString("      urls: []\n")
			continue
		}
		fmt.Fprintf(&idx, "      urls:\n      - archives/%s.tgz\n", e.archive)
		if _, done := archives[e.archive]; !done {
			archives[e.archive] = makeChartArchive(t, e.archive)
		}
	}

	repo := &fakeRepo{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo.mu.Lock()
		repo.requests = append(repo.requests, r.URL.Path)
		repo.mu.Unlock()

		if r.URL.Path == "/index.yaml" {
			w.Header().Set("Content-Type", "text/yaml")
			_, _ = w.Write([]byte(idx.String()))
			return
		}
		for name, body := range archives {
			if r.URL.Path == archivePath(name) {
				w.Header().Set("Content-Type", "application/gzip")
				_, _ = w.Write(body)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	repo.url = srv.URL
	return repo
}

// TestFetchValues_VersionSpelling is the whole matching rule, one named
// case at a time. wantArchive is the archive the lookup must choose; an
// empty wantArchive means the request must be refused.
func TestFetchValues_VersionSpelling(t *testing.T) {
	tests := []struct {
		name         string
		index        []indexEntry
		requested    string
		wantArchive  string
		wantResolved string
	}{
		{
			// The v-prefixed entry is listed FIRST on purpose: a lookup
			// that compared trimmed strings and took the first hit would
			// return the wrong archive here and pass a success-only check.
			name:         "exact bare match beats the v-prefixed entry",
			index:        []indexEntry{{"v1.2.3", "vprefixed"}, {"1.2.3", "bare"}},
			requested:    "1.2.3",
			wantArchive:  "bare",
			wantResolved: "1.2.3",
		},
		{
			name:         "exact v-prefixed match beats the bare entry",
			index:        []indexEntry{{"1.2.3", "bare"}, {"v1.2.3", "vprefixed"}},
			requested:    "v1.2.3",
			wantArchive:  "vprefixed",
			wantResolved: "v1.2.3",
		},
		{
			// The original defect: this is the cert-manager case.
			name:         "bare requested finds the v-prefixed entry",
			index:        []indexEntry{{"v1.2.3", "vprefixed"}},
			requested:    "1.2.3",
			wantArchive:  "vprefixed",
			wantResolved: "v1.2.3",
		},
		{
			name:         "v-prefixed requested finds the bare entry",
			index:        []indexEntry{{"1.2.3", "bare"}},
			requested:    "v1.2.3",
			wantArchive:  "bare",
			wantResolved: "1.2.3",
		},
		{
			name:         "prerelease bare requested finds the v-prefixed prerelease",
			index:        []indexEntry{{"1.2.3", "release"}, {"v1.2.3-rc.1", "vprerelease"}},
			requested:    "1.2.3-rc.1",
			wantArchive:  "vprerelease",
			wantResolved: "v1.2.3-rc.1",
		},
		{
			name:         "prerelease v-prefixed requested finds the bare prerelease",
			index:        []indexEntry{{"1.2.3", "release"}, {"1.2.3-rc.1", "prerelease"}},
			requested:    "v1.2.3-rc.1",
			wantArchive:  "prerelease",
			wantResolved: "1.2.3-rc.1",
		},
		{
			// The prerelease suffix is never dropped, so a pinned rc does
			// not quietly become the finished release.
			name:        "prerelease never falls back to the plain release",
			index:       []indexEntry{{"1.2.3", "release"}, {"v1.2.3", "vrelease"}},
			requested:   "1.2.3-rc.1",
			wantArchive: "",
		},
		{
			name:         "build suffix bare requested finds the v-prefixed build",
			index:        []indexEntry{{"1.2.3", "release"}, {"v1.2.3+build.5", "vbuild"}},
			requested:    "1.2.3+build.5",
			wantArchive:  "vbuild",
			wantResolved: "v1.2.3+build.5",
		},
		{
			name:        "build suffix never falls back to the plain release",
			index:       []indexEntry{{"1.2.3", "release"}, {"v1.2.3", "vrelease"}},
			requested:   "1.2.3+build.5",
			wantArchive: "",
		},
		{
			// A capital V is a different spelling, not the same one in a
			// different case. Nothing folds case on this path.
			name:        "a capital V does not match the lowercase v entry",
			index:       []indexEntry{{"v1.2.3", "vprefixed"}, {"1.2.3", "bare"}},
			requested:   "V1.2.3",
			wantArchive: "",
		},
		{
			// Exactly one v is added or removed. Removing one from
			// "vv1.2.3" would land on "v1.2.3", a version nobody pinned.
			name:        "a doubled v matches neither spelling",
			index:       []indexEntry{{"1.2.3", "bare"}, {"v1.2.3", "vprefixed"}},
			requested:   "vv1.2.3",
			wantArchive: "",
		},
		{
			// The long-standing skip: an entry with an empty urls: list has
			// nothing to download, so the scan carries on past it — here to
			// the other spelling of the same version.
			name:         "an entry with no urls is skipped and the other spelling is used",
			index:        []indexEntry{{"1.2.3", ""}, {"v1.2.3", "vprefixed"}},
			requested:    "1.2.3",
			wantArchive:  "vprefixed",
			wantResolved: "v1.2.3",
		},
		{
			name:        "an entry with no urls is still skipped when it is the only one",
			index:       []indexEntry{{"1.2.3", ""}},
			requested:   "1.2.3",
			wantArchive: "",
		},
		{
			name:        "a version the repo does not publish is refused",
			index:       []indexEntry{{"9.9.9", "other"}, {"v8.8.8", "another"}},
			requested:   "1.2.3",
			wantArchive: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := startFakeRepo(t, tt.index)
			f := NewFetcher()

			got, err := f.FetchValues(context.Background(), repo.url, spellingChartName, tt.requested)

			if tt.wantArchive == "" {
				if err == nil {
					t.Fatalf("the request was allowed and returned %q; it must be refused", got)
				}
				// Exact text: the error shape callers and the API surface
				// already depend on, and an equality check is also the
				// strongest possible leak test — nothing else can be in
				// there.
				want := fmt.Sprintf("version %s not found for chart %s", tt.requested, spellingChartName)
				if err.Error() != want {
					t.Fatalf("error = %q, want exactly %q", err.Error(), want)
				}
				// Belt and braces on the leak rule: no repository
				// address, no archive address, no provider or
				// credential-store words.
				for _, banned := range []string{repo.url, "127.0.0.1", "http", ".tgz", "urls", "index.yaml", "provider", "credential", "secret", "token"} {
					if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(banned)) {
						t.Errorf("the refusal contains %q: %s", banned, err.Error())
					}
				}
				// Nothing was downloaded either.
				if p := repo.paths(); len(p) != 1 || p[0] != "/index.yaml" {
					t.Errorf("requests = %v, want only the index read", p)
				}
				return
			}

			if err != nil {
				t.Fatalf("FetchValues(%q) failed: %v", tt.requested, err)
			}
			if got != valuesBodyFor(tt.wantArchive) {
				t.Errorf("values body = %q, want the %q archive's body %q",
					got, tt.wantArchive, valuesBodyFor(tt.wantArchive))
			}
			// The chosen URL, read straight off the server's own log.
			if n := repo.countPath(archivePath(tt.wantArchive)); n != 1 {
				t.Errorf("the %q archive was requested %d time(s), want exactly 1; requests = %v",
					tt.wantArchive, n, repo.paths())
			}
			// And no OTHER archive was fetched.
			for _, p := range repo.paths() {
				if strings.HasSuffix(p, ".tgz") && p != archivePath(tt.wantArchive) {
					t.Errorf("an unexpected archive was fetched: %s", p)
				}
			}
			// The cache is keyed on the version the index publishes.
			wantKey := repo.url + "/" + spellingChartName + "/" + tt.wantResolved
			if _, ok := f.valuesCache[wantKey]; !ok {
				t.Errorf("values cache has no entry under the resolved key %q; keys = %v",
					wantKey, cacheKeys(f.valuesCache))
			}
		})
	}
}

func cacheKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestFetchValues_BothSpellingsShareOneCacheEntry pins the cache-key
// decision: the key holds the version the index publishes, so asking for
// "1.2.3" and "v1.2.3" against a v-prefixed index downloads the archive
// once and stores it once, instead of fetching the same bytes twice under
// two keys.
func TestFetchValues_BothSpellingsShareOneCacheEntry(t *testing.T) {
	repo := startFakeRepo(t, []indexEntry{{"v1.2.3", "vprefixed"}})
	f := NewFetcher()
	ctx := context.Background()

	first, err := f.FetchValues(ctx, repo.url, spellingChartName, "v1.2.3")
	if err != nil {
		t.Fatalf("FetchValues(v1.2.3): %v", err)
	}
	second, err := f.FetchValues(ctx, repo.url, spellingChartName, "1.2.3")
	if err != nil {
		t.Fatalf("FetchValues(1.2.3): %v", err)
	}
	if first != second {
		t.Errorf("the two spellings returned different bodies:\n%q\n%q", first, second)
	}

	if n := repo.countPath(archivePath("vprefixed")); n != 1 {
		t.Errorf("the archive was downloaded %d time(s), want 1; requests = %v", n, repo.paths())
	}
	if len(f.valuesCache) != 1 {
		t.Fatalf("values cache holds %d entries, want 1: %v", len(f.valuesCache), cacheKeys(f.valuesCache))
	}
	wantKey := repo.url + "/" + spellingChartName + "/v1.2.3"
	if _, ok := f.valuesCache[wantKey]; !ok {
		t.Errorf("the single cache key is %v, want %q", cacheKeys(f.valuesCache), wantKey)
	}
}

// TestFetchChartYAML_ThroughFetchReleaseNotes_UsesTheSameResolver drives the
// SECOND call site. fetchChartYAML is unexported and FetchReleaseNotes is
// its only caller, so this goes in through the caller — which is the point:
// if the two paths did not share the resolver, this one would still miss a
// bare-pinned version against a v-prefixed index.
//
// The Chart.yaml in the fake archive points sources: and home: at
// non-GitHub addresses, so FetchReleaseNotes finds no GitHub repository and
// returns its "not available" sentence without contacting api.github.com.
func TestFetchChartYAML_ThroughFetchReleaseNotes_UsesTheSameResolver(t *testing.T) {
	repo := startFakeRepo(t, []indexEntry{{"v1.2.3", "vprefixed"}})
	f := NewFetcher()

	notes, err := f.FetchReleaseNotes(context.Background(), repo.url, spellingChartName, "1.2.3")
	if err != nil {
		t.Fatalf("FetchReleaseNotes: %v", err)
	}
	const wantNotes = "Release notes not available (no GitHub repository found for this chart)."
	if notes != wantNotes {
		t.Errorf("notes = %q, want %q", notes, wantNotes)
	}

	// The proof that the resolver ran on this path: the v-prefixed archive
	// was actually downloaded for a bare-pinned request.
	if n := repo.countPath(archivePath("vprefixed")); n != 1 {
		t.Fatalf("the v-prefixed archive was requested %d time(s), want 1; requests = %v",
			n, repo.paths())
	}

	// And the Chart.yaml cache is keyed on the published spelling too.
	wantKey := repo.url + "/" + spellingChartName + "/v1.2.3"
	if _, ok := f.chartCache[wantKey]; !ok {
		keys := make([]string, 0, len(f.chartCache))
		for k := range f.chartCache {
			keys = append(keys, k)
		}
		t.Errorf("chart cache has no entry under the resolved key %q; keys = %v", wantKey, keys)
	}
	if _, ok := f.chartCache[repo.url+"/"+spellingChartName+"/1.2.3"]; ok {
		t.Errorf("chart cache also holds the requested spelling — the two spellings must share one entry")
	}
}

// TestFetchChartYAML_UnresolvableVersionKeepsTheErrorShape covers the
// second call site's refusal, which FetchReleaseNotes swallows on purpose.
func TestFetchChartYAML_UnresolvableVersionKeepsTheErrorShape(t *testing.T) {
	repo := startFakeRepo(t, []indexEntry{{"9.9.9", "other"}})
	f := NewFetcher()

	_, err := f.fetchChartYAML(context.Background(), repo.url, spellingChartName, "1.2.3")
	if err == nil {
		t.Fatal("the lookup was allowed; it must be refused")
	}
	want := "version 1.2.3 not found for chart " + spellingChartName
	if err.Error() != want {
		t.Fatalf("error = %q, want exactly %q", err.Error(), want)
	}
}

// TestAltVersionSpelling_TriesExactlyOneV is the rule stated on its own,
// away from any HTTP traffic: one leading lowercase v, added or removed,
// and nothing else is ever proposed.
func TestAltVersionSpelling_TriesExactlyOneV(t *testing.T) {
	tests := []struct {
		requested string
		wantAlt   string
		wantOK    bool
	}{
		{"1.2.3", "v1.2.3", true},
		{"v1.2.3", "1.2.3", true},
		{"1.2.3-rc.1", "v1.2.3-rc.1", true},
		{"v1.2.3-rc.1", "1.2.3-rc.1", true},
		{"1.2.3+build.5", "v1.2.3+build.5", true},
		// Two leading v's: removing one lands on a different version, so
		// there is no alternative spelling to try.
		{"vv1.2.3", "", false},
		{"vvv1.2.3", "", false},
		// A lone "v" has nothing after it.
		{"v", "", false},
		{"", "", false},
		// A capital V is not the lowercase one, so it is never removed.
		{"V1.2.3", "vV1.2.3", true},
	}

	for _, tt := range tests {
		gotAlt, gotOK := altVersionSpelling(tt.requested)
		if gotAlt != tt.wantAlt || gotOK != tt.wantOK {
			t.Errorf("altVersionSpelling(%q) = (%q, %v), want (%q, %v)",
				tt.requested, gotAlt, gotOK, tt.wantAlt, tt.wantOK)
		}
	}
}
