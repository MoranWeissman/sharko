package catalog

// freshness_b14_test.go — the freshness scheduler's own sentences and its own
// log line (B14).
//
// fetchOne builds three plain-English sentences, all of which named the chart
// repository as the operator wrote it in catalog.yaml — where a token inside
// the address is the ordinary shape. One of those sentences is
// NoDataReason, which the browser renders verbatim. And the failure branch
// logged the whole address under the key "repo", which no redactor detector
// looked at.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// b14Sentinel stands in for the access token. It appears nowhere else in this
// repository, so finding it is proof rather than coincidence.
const b14Sentinel = "R7QX-freshness-repo-token-sentinel-3k9m2p-never-leaves-the-server-e5d1"

// The tokenised address and the safe one it must become. Both typed out as
// literals: reading them back off the code would pass whatever the code did.
const (
	b14TokenRepo = "https://" + b14Sentinel + "@charts.example/private/charts"
	b14SafeRepo  = "https://charts.example/private/charts"
)

// b14CatalogYAML is one entry whose repo carries the token.
const b14CatalogYAML = `
addons:
  - name: leaky
    description: an addon whose chart repo address carries a token.
    chart: leaky
    repo: ` + b14TokenRepo + `
    default_namespace: leaky
    maintainers: [example]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`

func b14Catalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := LoadBytes([]byte(b14CatalogYAML))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	return c
}

// b14Refresh runs one real scheduler pass with a scripted lister and returns
// the snapshot plus whatever the REAL redacting log handler wrote.
func b14Refresh(t *testing.T, versions []helm.ChartVersion, listErr error) (VersionSnapshot, string) {
	t.Helper()
	cat := b14Catalog(t)
	lister := newFakeVersionsLister()
	lister.set(b14TokenRepo, "leaky", versions, listErr)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(logging.NewRedactHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)))
	defer slog.SetDefault(prev)

	sched := NewFreshnessScheduler(cat, lister, nil, time.Hour)
	sched.refresh()

	snap, ok := sched.VersionSnapshot("leaky")
	if !ok {
		t.Fatal("no snapshot for leaky — the pass never reached fetchOne, so nothing below proves anything")
	}
	return snap, buf.String()
}

// TestB14Fixture_TheTokenisedRepoReallyCarriesIt keeps the fixtures honest.
func TestB14Fixture_TheTokenisedRepoReallyCarriesIt(t *testing.T) {
	if !strings.Contains(b14TokenRepo, b14Sentinel) {
		t.Fatal("the tokenised repo address does not carry the sentinel — every assertion in this file would be true for the wrong reason")
	}
	if strings.Contains(b14SafeRepo, b14Sentinel) {
		t.Fatal("the SAFE repo address carries the sentinel, so asserting its presence would also be asserting a leak")
	}
	cat := b14Catalog(t)
	if got := cat.Len(); got != 1 {
		t.Fatalf("the fixture catalog holds %d entries, want 1", got)
	}
}

// TestFreshness_FetchFailure_SentenceAndLogCarryNoToken is the failure branch:
// NoDataReason, VersionSnapshot.Err's neighbour, and the "repo" log line.
func TestFreshness_FetchFailure_SentenceAndLogCarryNoToken(t *testing.T) {
	snap, logs := b14Refresh(t, nil, errors.New("dial tcp: connection refused"))

	if !snap.Unknown {
		t.Fatal("expected Unknown=true after a failed fetch — the branch under test was never taken")
	}
	if strings.Contains(snap.NoDataReason, b14Sentinel) {
		t.Errorf("no_data_reason carries the repository token:\n%s", snap.NoDataReason)
	}
	if strings.Contains(logs, b14Sentinel) {
		t.Errorf("the log carries the repository token:\n%s", logs)
	}

	const want = "no freshness data for this source — " + b14SafeRepo + " lists no versions of leaky"
	_ = want // the empty-list branch asserts this one; see the test below.

	const wantFailure = "no freshness data for this source — Sharko could not read the version index at " + b14SafeRepo
	if snap.NoDataReason != wantFailure {
		t.Errorf("no_data_reason = %q,\nwant exactly       %q", snap.NoDataReason, wantFailure)
	}

	// The log line must still exist and must still name the repository, or a
	// fix that stopped logging would pass every sweep and leave an operator
	// with nothing to go on.
	if !strings.Contains(logs, "version fetch failed") {
		t.Fatalf("the failure log line was never written, so the sweep above swept nothing:\n%s", logs)
	}
	if !strings.Contains(logs, b14SafeRepo) {
		t.Errorf("the log line no longer names the repository at all — an operator cannot tell which one failed:\n%s", logs)
	}
}

// TestFreshness_EmptyVersionList_SentenceCarriesNoToken is the SUCCESS branch:
// the repository answered, and published nothing.
func TestFreshness_EmptyVersionList_SentenceCarriesNoToken(t *testing.T) {
	snap, logs := b14Refresh(t, []helm.ChartVersion{}, nil)

	if !snap.Unknown {
		t.Fatal("expected Unknown=true for an empty version list — the branch under test was never taken")
	}
	if strings.Contains(snap.NoDataReason, b14Sentinel) {
		t.Errorf("no_data_reason carries the repository token:\n%s", snap.NoDataReason)
	}
	if strings.Contains(logs, b14Sentinel) {
		t.Errorf("the log carries the repository token:\n%s", logs)
	}

	const want = "no freshness data for this source — " + b14SafeRepo + " lists no versions of leaky"
	if snap.NoDataReason != want {
		t.Errorf("no_data_reason = %q,\nwant exactly       %q", snap.NoDataReason, want)
	}
}

// TestFreshness_OCIUnsupported_SentenceCarriesNoToken is the third sentence.
func TestFreshness_OCIUnsupported_SentenceCarriesNoToken(t *testing.T) {
	snap, _ := b14Refresh(t, nil, fmt.Errorf("wrapped: %w", helm.ErrOCIVersionCheckUnsupported))

	if !snap.Unknown {
		t.Fatal("expected Unknown=true for the oci:// sentinel")
	}
	const want = "no freshness data for this source — " + b14SafeRepo + " has no version index Sharko can read"
	if snap.NoDataReason != want {
		t.Errorf("no_data_reason = %q,\nwant exactly       %q", snap.NoDataReason, want)
	}
	if strings.Contains(snap.NoDataReason, b14Sentinel) {
		t.Errorf("no_data_reason carries the repository token:\n%s", snap.NoDataReason)
	}
}

// TestFreshness_UnparseableRepo_SentenceStaysWhole is the case that made
// SafeRepoURLPhrase necessary. SafeRepoURL returns "" when it cannot be sure
// which part of a string is the credential, and dropped straight into a
// sentence that leaves the sentence hanging.
//
// It comes in through the org's OWN catalog.yaml, not the curated one: the
// curated loader rejects a repo that is not an http(s) or oci URL, and the
// approved-addons path has no such check, so this is the shape that really
// reaches fetchOne.
func TestFreshness_UnparseableRepo_SentenceStaysWhole(t *testing.T) {
	const scpStyle = "git@charts.example:private/charts.git"
	lister := newFakeVersionsLister()
	lister.set(scpStyle, "leaky", []helm.ChartVersion{}, nil)

	sched := NewFreshnessScheduler(loadTestCatalog(t), lister, nil, time.Hour).
		WithApprovedAddons(func(context.Context) ([]ApprovedAddon, error) {
			return []ApprovedAddon{{Name: "leaky", RepoURL: scpStyle, Chart: "leaky"}}, nil
		})
	sched.refresh()

	snap, ok := sched.VersionSnapshot("leaky")
	if !ok {
		t.Fatal("no snapshot — the approved-addons pass never reached fetchOne")
	}
	const want = "no freshness data for this source — the chart repository lists no versions of leaky"
	if snap.NoDataReason != want {
		t.Errorf("no_data_reason = %q,\nwant exactly       %q.\n\nA sentence with an empty gap where the repository should be reads as a bug, not as a redaction.", snap.NoDataReason, want)
	}
}
