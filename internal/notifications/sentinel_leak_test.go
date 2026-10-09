package notifications

// sentinel_leak_test.go — the positive-control leak test for the one leak in
// this round that survived a restart.
//
// A sweep that finds nothing proves nothing unless the same sweep can be shown
// to find something. So every test here plants a value that must never be
// shown to anybody, drives the REAL code path, and asserts — in the same run —
// that the sweep does find the sentinel where it legitimately is (in the error
// itself, or in the seeded ConfigMap before the migration runs). If the sweep
// ever stops working, the control fails and the file goes red rather than
// passing empty.
//
// The forms swept for are the ones the standing rule names: the raw value, its
// base64, its hashes, fragments of it, and anything whose LENGTH tracks it. A
// mask whose width follows the secret leaks the secret's length, so a
// fixed-width check is not enough.
//
// The surfaces swept are the ones that OUTLIVE the request:
//
//	the ConfigMap's own stored bytes  — the durable state, the whole point
//	the JSON the API serves           — Store.List() is the body of GET /notifications
//	the process's log output          — captured via slog.SetDefault
//	the process's Prometheus metrics  — gathered from the default registry
//
// and the ConfigMap is swept AGAIN after a second restart, because a fix that
// filters on read without writing back leaves the value on disk forever while
// passing every single-restart test.

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// sentinelSecret is the value that must never reach a durable surface. It is
// shaped like something a Git backend really would put in an error: a token in
// a remote URL.
const sentinelSecret = "ghp_SHARKOSENTINEL0123456789abcdefABCDEF"

// leakForms returns every form of secret the standing rule bans, each with a
// name so a failure says WHICH form was found rather than just "a leak".
func leakForms(secret string) map[string]string {
	sum256 := sha256.Sum256([]byte(secret))
	sum1 := sha1.Sum([]byte(secret))
	sum5 := md5.Sum([]byte(secret))
	return map[string]string{
		"raw value":            secret,
		"base64":               base64.StdEncoding.EncodeToString([]byte(secret)),
		"base64 (url safe)":    base64.URLEncoding.EncodeToString([]byte(secret)),
		"base64 (raw, no pad)": base64.RawStdEncoding.EncodeToString([]byte(secret)),
		"sha256":               hex.EncodeToString(sum256[:]),
		"sha1":                 hex.EncodeToString(sum1[:]),
		"md5":                  hex.EncodeToString(sum5[:]),
		// Fragments. The distinctive middle is what a partial mask would keep
		// ("ghp_SHAR…CDEF" style), so both ends are swept.
		"fragment (first 12)": secret[:12],
		"fragment (last 12)":  secret[len(secret)-12:],
		"fragment (middle)":   secret[16:32],
	}
}

// findLeaks returns the names of every banned form present in text.
func findLeaks(text, secret string) []string {
	var found []string
	for name, form := range leakForms(secret) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// variableWidthMask matches a run of mask characters long enough to be
// carrying a length. A fixed short mask ("***") is fine; a run that tracks the
// secret's length is not, because the width IS the length.
var variableWidthMask = regexp.MustCompile(`[*x•●]{8,}`)

// findLengthLeaks reports mask runs whose width equals the secret's length, and
// the secret's length stated as a number next to mask-ish words — the
// length-derived forms the rule bans alongside the value itself.
func findLengthLeaks(text, secret string) []string {
	var found []string
	for _, run := range variableWidthMask.FindAllString(text, -1) {
		if len(run) == len(secret) {
			found = append(found, fmt.Sprintf("a %d-character mask run, exactly the secret's length", len(run)))
		}
	}
	lengthWord := regexp.MustCompile(`\b` + strconv.Itoa(len(secret)) + `\b`)
	if lengthWord.MatchString(text) &&
		(strings.Contains(strings.ToLower(text), "length") ||
			strings.Contains(strings.ToLower(text), "char") ||
			strings.Contains(strings.ToLower(text), "bytes")) {
		found = append(found, fmt.Sprintf("the secret's length (%d) stated in words", len(secret)))
	}
	return found
}

// sweep runs both sweeps over one surface and reports every hit by name.
func sweep(t *testing.T, surface, text string) {
	t.Helper()
	if leaks := findLeaks(text, sentinelSecret); len(leaks) > 0 {
		t.Errorf("%s leaks the sentinel as %s\n  content: %s", surface, strings.Join(leaks, ", "), text)
	}
	if leaks := findLengthLeaks(text, sentinelSecret); len(leaks) > 0 {
		t.Errorf("%s leaks the sentinel's LENGTH — %s\n  content: %s", surface, strings.Join(leaks, ", "), text)
	}
}

// ── the harness ─────────────────────────────────────────────────────────────

// sentinelBackendError is the shape of error a Git provider really hands back
// when a token in a remote URL is rejected.
func sentinelBackendError() error {
	return errors.New(`Get "https://x-access-token:` + sentinelSecret +
		`@github.example.invalid/org/repo/info/refs": remote: Invalid username or password`)
}

// storeWithCM builds a Store over a fake clientset and hands back the raw
// clientset too, so a test can read the ConfigMap's own stored bytes rather
// than trusting the store's in-memory view of them.
func storeWithCM(t *testing.T) (*fake.Clientset, *cmstore.Store) {
	t.Helper()
	client := fake.NewSimpleClientset()
	return client, cmstore.NewStore(client, "default", "sharko-notifications")
}

// storedBytes returns the exact string persisted in the ConfigMap — the
// durable state, as a later Sharko (or anybody with kubectl) would read it.
func storedBytes(t *testing.T, client *fake.Clientset) string {
	t.Helper()
	cm, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "sharko-notifications", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the notifications ConfigMap: %v", err)
	}
	return cm.Data["state"]
}

// seedReleasedRecord writes a record in the shape a RELEASED Sharko really
// wrote, straight into the ConfigMap, bypassing Store.Add.
//
// It is built as a raw map rather than as a Notification, and that is the whole
// point. The released struct had exactly these six keys — id, type, title,
// description, timestamp, read — and no `code`, no `schema` and no `reason`,
// because Code and Schema were both added after the last release (see
// TestGuard_TheReleasedShapeHasNoCodeAndNoSchema). Marshalling today's struct
// with its zero values would write `"code":""` and `"schema":0`, which is a
// shape no build ever produced; this seeds what is genuinely out there.
//
// Its description is the old `lead + " Reason: " + detail` concatenation with a
// backend's own words on the end — the leak this whole file exists for.
func seedReleasedRecord(t *testing.T, cm *cmstore.Store, extra ...map[string]interface{}) {
	t.Helper()
	legacy := map[string]interface{}{
		"id":    "connection-" + TitleGitConnectionBroken + "-1755500000000000000",
		"type":  string(TypeConnection),
		"title": TitleGitConnectionBroken,
		"description": "Sharko uses this Git connection for every commit and pull request, and right now it can't reach it." +
			" Reason: " + sentinelBackendError().Error(),
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"read":      false,
	}
	all := append([]map[string]interface{}{legacy}, extra...)
	if err := cm.ReadModifyWrite(context.Background(), func(data map[string]interface{}) error {
		data[notificationsKey] = all
		return nil
	}); err != nil {
		t.Fatalf("seeding the configmap: %v", err)
	}
}

// seedTypedRecords writes present-day Notification values straight into the
// ConfigMap, bypassing Store.Add — for the shapes that need a real Code or
// Schema set.
func seedTypedRecords(t *testing.T, cm *cmstore.Store, records ...Notification) {
	t.Helper()
	if err := cm.ReadModifyWrite(context.Background(), func(data map[string]interface{}) error {
		return encodeNotifications(data, records)
	}); err != nil {
		t.Fatalf("seeding the configmap: %v", err)
	}
}

// captureLogs redirects the default slog logger into a buffer for the duration
// of fn and returns everything written. The store logs through package-level
// slog calls, so this is the process's real log output for that window.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	fn()
	return buf.String()
}

// gatheredMetrics renders every metric in the default Prometheus registry.
func gatheredMetrics(t *testing.T) string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	var b strings.Builder
	for _, f := range families {
		b.WriteString(f.String())
	}
	return b.String()
}

// ── the write path: a new notification cannot carry the probe's words ───────

// TestSentinel_NewNotificationNeverCarriesTheProbeWords drives the real
// classify → poller → store path with an error carrying the sentinel, exactly
// as the git health closure in cmd/sharko/serve.go does, and sweeps every
// durable surface.
func TestSentinel_NewNotificationNeverCarriesTheProbeWords(t *testing.T) {
	for _, marked := range []bool{false, true} {
		name := "unmarked backend error"
		if marked {
			name = "credentials-marked error"
		}
		t.Run(name, func(t *testing.T) {
			client, cm := storeWithCM(t)
			store := NewStore(50, cm)

			err := sentinelBackendError()
			if marked {
				err = credsafe.Mark(err)
			}

			// THE POSITIVE CONTROL, run before any assertion that expects
			// nothing. An unmarked error's own words carry the sentinel, so the
			// sweep must find it there. If it does not, the sweep is broken and
			// every "no leak" result below is worthless.
			if !marked {
				if got := findLeaks(err.Error(), sentinelSecret); len(got) == 0 {
					t.Fatal("POSITIVE CONTROL FAILED: the sweep could not find the sentinel in the error's own text, where it certainly is. The sweep proves nothing in this state.")
				}
			}

			logs := captureLogs(t, func() {
				// This is what serve.go's gitHealthFn does, line for line.
				result := UnhealthyResult(ClassifyReason(err))
				p := NewConnectionPoller(store, time.Minute, func(context.Context) HealthResult { return result },
					func(context.Context) HealthResult { return UndeterminedResult() })
				p.check()
			})

			raised := findByCode(store, CodeGitConnectionBroken)
			if raised == nil {
				t.Fatal("no alert was raised, so this test swept nothing")
			}

			wire, marshalErr := json.Marshal(store.List())
			if marshalErr != nil {
				t.Fatalf("marshalling the notification list: %v", marshalErr)
			}

			sweep(t, "the notification the store holds", fmt.Sprintf("%+v", *raised))
			sweep(t, "the JSON GET /notifications serves", string(wire))
			sweep(t, "the ConfigMap's stored bytes", storedBytes(t, client))
			sweep(t, "the notification package's log output", logs)
			sweep(t, "the process's Prometheus metrics", gatheredMetrics(t))

			// And the alert still says something. A cleaned notification that
			// communicates nothing trades one defect for another.
			if raised.Description != descriptionFor(CodeGitConnectionBroken, raised.Reason) {
				t.Errorf("the description is not the catalog pair for its code and reason: %q", raised.Description)
			}
			if !raised.Reason.Valid() {
				t.Errorf("the alert carries no usable reason: %q", raised.Reason)
			}
		})
	}
}

// TestSentinel_AConvertedReasonCannotCarryText is the hole a string-typed enum
// leaves open: Reason(err.Error()) compiles. Store.Add's sanitiser is what
// closes it, so this drives the conversion deliberately and sweeps.
func TestSentinel_AConvertedReasonCannotCarryText(t *testing.T) {
	client, cm := storeWithCM(t)
	store := NewStore(50, cm)

	smuggled := Reason(sentinelBackendError().Error())

	// Positive control: the sweep finds the sentinel in the value being
	// smuggled in, so the assertions below are meaningful.
	if got := findLeaks(string(smuggled), sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the sweep cannot find the sentinel in the value being smuggled in.")
	}

	logs := captureLogs(t, func() {
		store.Add(Notification{
			ID:        "connection-" + CodeGitConnectionBroken.String(),
			Code:      CodeGitConnectionBroken,
			Reason:    smuggled,
			Type:      TypeConnection,
			Title:     TitleGitConnectionBroken,
			Timestamp: time.Now(),
		})
	})

	stored := findByCode(store, CodeGitConnectionBroken)
	if stored == nil {
		t.Fatal("nothing was stored, so this test swept nothing")
	}
	if stored.Reason != ReasonUnspecified {
		t.Errorf("an undeclared reason must be replaced with ReasonUnspecified, got %q", stored.Reason)
	}

	wire, err := json.Marshal(store.List())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	sweep(t, "the stored notification", fmt.Sprintf("%+v", *stored))
	sweep(t, "the JSON GET /notifications serves", string(wire))
	sweep(t, "the ConfigMap's stored bytes", storedBytes(t, client))
	sweep(t, "the log output", logs)
}

// ── loading records an older Sharko already wrote ───────────────────────────

// TestGuard_TheReleasedShapeHasNoCodeAndNoSchema is the fact every test below
// rests on, asserted rather than assumed.
//
// A released Sharko's notification JSON has no `code` key and no `schema` key,
// so both fields decode to their zero values. That is why the trust gate is the
// only thing a real upgrade ever meets, and why a fixture with a declared code
// but no schema describes a record no shipped build could have written.
//
// If a future change makes the zero Code declared, or defaults Schema to
// CurrentSchema on decode, a genuinely old record would start being KEPT and
// served with a backend's own words in it. This fails first when that happens.
func TestGuard_TheReleasedShapeHasNoCodeAndNoSchema(t *testing.T) {
	releasedJSON := `[{"id":"connection-x-1","type":"connection","title":"t",` +
		`"description":"d","timestamp":"2026-01-01T00:00:00Z","read":false}]`

	var decoded []Notification
	if err := json.Unmarshal([]byte(releasedJSON), &decoded); err != nil {
		t.Fatalf("the released shape no longer decodes at all: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected one record, got %d", len(decoded))
	}
	if decoded[0].Code != "" {
		t.Errorf("a released record decoded with Code %q — it must be empty", decoded[0].Code)
	}
	if decoded[0].Code.IsDeclared() {
		t.Error("the empty Code is now DECLARED, so a released record would be kept and served with whatever its description holds")
	}
	if decoded[0].Schema != 0 {
		t.Errorf("a released record decoded with Schema %d — it must be 0", decoded[0].Schema)
	}
	if decoded[0].Schema == CurrentSchema {
		t.Error("a released record now claims the current schema, so the shape gate cannot tell it apart from a record Sharko wrote")
	}
}

// TestSentinel_ReleasedRecordIsDroppedOnLoad is the main event for the durable
// half. It seeds a record in the shape a released Sharko really wrote, carrying
// the sentinel, restarts, and sweeps every surface — including the ConfigMap's
// own bytes, which is what separates a real purge from a read-time filter.
func TestSentinel_ReleasedRecordIsDroppedOnLoad(t *testing.T) {
	client, cm := storeWithCM(t)
	seedReleasedRecord(t, cm)

	// THE POSITIVE CONTROL. The seeded ConfigMap really does hold the sentinel
	// right now, and the sweep really does find it. Everything below is
	// meaningless without this.
	before := storedBytes(t, client)
	if got := findLeaks(before, sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the sweep could not find the sentinel in the ConfigMap it was just written into. The sweep proves nothing in this state.")
	}

	// Restart.
	var store *Store
	logs := captureLogs(t, func() { store = NewStore(50, cm) })

	// The record is GONE, not repaired. Sharko could not vouch for it, so it
	// does not pretend to — the poller re-raises the alert on its next tick if
	// the problem is still real (TestDrop_ThePollerRaisesTheAlertAgain).
	if n := len(store.List()); n != 0 {
		t.Fatalf("a record Sharko cannot vouch for was kept: %d survived %+v", n, store.List())
	}

	wire, err := json.Marshal(store.List())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	sweep(t, "the JSON GET /notifications serves after a restart", string(wire))
	sweep(t, "the ConfigMap's stored bytes after the load", storedBytes(t, client))
	sweep(t, "the startup log output", logs)
	sweep(t, "the process's Prometheus metrics", gatheredMetrics(t))

	// And the drop is not silent.
	if !strings.Contains(logs, "dropped a saved notification") {
		t.Errorf("the record was dropped without saying so:\n%s", logs)
	}
}

// TestSentinel_SecondRestartIsAlsoClean is the requirement a read-time filter
// cannot meet. It asserts on the ConfigMap's OWN BYTES after the first restart
// — a filter that never writes back leaves the sentinel there, and a second
// restart would read it again, forever.
func TestSentinel_SecondRestartIsAlsoClean(t *testing.T) {
	client, cm := storeWithCM(t)
	seedReleasedRecord(t, cm)

	if got := findLeaks(storedBytes(t, client), sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the seeded ConfigMap does not contain the sentinel the sweep is looking for.")
	}

	// First restart — this is the one that must WRITE the surviving state back.
	first := NewStore(50, cm)
	if n := len(first.List()); n != 0 {
		t.Fatalf("expected the seeded record to be dropped, %d survived", n)
	}
	sweep(t, "the ConfigMap after the first restart", storedBytes(t, client))

	// Second restart, over the same ConfigMap, with a brand-new store. If the
	// first restart only filtered on read, the sentinel is still on disk and
	// this sweep of the durable bytes is what catches it.
	var secondLogs string
	var second *Store
	secondLogs = captureLogs(t, func() { second = NewStore(50, cm) })

	sweep(t, "the ConfigMap after the second restart", storedBytes(t, client))
	sweep(t, "the second restart's log output", secondLogs)

	wire, err := json.Marshal(second.List())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	sweep(t, "the JSON served after the second restart", string(wire))

	// The second restart had nothing to drop — the first one purged the durable
	// copy — so it must not have reported one.
	if strings.Contains(secondLogs, "dropped a saved notification") ||
		strings.Contains(secondLogs, "dropped saved notifications") {
		t.Errorf("the second restart dropped something again, so the first one did not persist its work:\n%s", secondLogs)
	}
}

// TestSentinel_AttachCMStorePurgesToo covers the path a real pod actually takes.
// serve.go always builds the store with a nil cmStore and upgrades it later via
// AttachCMStore, so a purge that only ran in NewStore would never run in
// production at all.
func TestSentinel_AttachCMStorePurgesToo(t *testing.T) {
	client, cm := storeWithCM(t)
	seedReleasedRecord(t, cm)

	if got := findLeaks(storedBytes(t, client), sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the seeded ConfigMap does not contain the sentinel.")
	}

	store := NewStore(50, nil) // in-memory, exactly as serve.go builds it
	var logs string
	logs = captureLogs(t, func() {
		if err := store.AttachCMStore(context.Background(), cm); err != nil {
			t.Fatalf("attaching the cmstore: %v", err)
		}
	})

	wire, err := json.Marshal(store.List())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	sweep(t, "the list after AttachCMStore", string(wire))
	sweep(t, "the ConfigMap after AttachCMStore", storedBytes(t, client))
	sweep(t, "AttachCMStore's log output", logs)

	// And a second attach, over the already-purged state, stays clean.
	second := NewStore(50, nil)
	if err := second.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("second attach: %v", err)
	}
	sweep(t, "the ConfigMap after a second AttachCMStore", storedBytes(t, client))
}

// TestDrop_ThePollerRaisesTheAlertAgain is the counterweight to dropping, and
// the mechanism that makes it safe rather than lossy.
//
// A dropped alert is not a lost fact. Nothing in the bell is a source of truth
// — every entry is derived from a live check — so the connection poller puts
// the alert straight back on its next tick if the problem is still real. This
// drives the REAL poller against a store whose seeded record was just dropped.
func TestDrop_ThePollerRaisesTheAlertAgain(t *testing.T) {
	client, cm := storeWithCM(t)
	seedReleasedRecord(t, cm)

	store := NewStore(50, cm)
	if n := len(store.List()); n != 0 {
		t.Fatalf("expected the seeded record to be dropped first, %d survived", n)
	}

	// One tick of the real poller, with Git still broken. The reason is what a
	// health closure hands in after classifying a backend error carrying the
	// sentinel — an enum, never the text (see reason.go).
	poller := NewConnectionPoller(store, DefaultConnectionCheckInterval,
		func(context.Context) HealthResult { return UnhealthyResult(ReasonCredentials) },
		func(context.Context) HealthResult { return UndeterminedResult() },
	)
	poller.check()

	back := findByCode(store, CodeGitConnectionBroken)
	if back == nil {
		t.Fatal("the poller did not raise the alert again, so dropping the saved copy really did lose it")
	}
	if back.Schema != CurrentSchema {
		t.Errorf("the re-raised alert carries schema %d, not the current one", back.Schema)
	}

	// And the alert that came back is built the safe way.
	sweep(t, "the re-raised alert", fmt.Sprintf("%+v", *back))
	sweep(t, "the ConfigMap after the poller re-raised it", storedBytes(t, client))
}

// TestKeepTrustworthy_DropsEveryShapeItCannotVouchFor drives the gate itself
// over each shape, and proves the second run changes nothing — by shape, not by
// comparing text.
func TestKeepTrustworthy_DropsEveryShapeItCannotVouchFor(t *testing.T) {
	current := Notification{
		ID: "keep", Code: CodeAddonVersionDrift, Type: TypeDrift,
		Title: "Version drift: x on y", Description: "safe words", Schema: CurrentSchema,
	}

	for _, tc := range []struct {
		name string
		rec  Notification
	}{
		{
			// What every released build wrote.
			name: "no code and no schema",
			rec: Notification{ID: "a", Type: TypeConnection, Title: TitleGitConnectionBroken,
				Description: "old words " + sentinelSecret},
		},
		{
			// A code this build does not know — a hand-edit, or a rollback.
			name: "an undeclared code",
			rec: Notification{ID: "b", Code: Code("something_else"), Type: TypeConnection,
				Title: "?", Description: "old words " + sentinelSecret, Schema: CurrentSchema},
		},
		{
			// Only a build from between the two unreleased stories wrote this.
			// It is dropped anyway, so nothing in it has to be trusted.
			name: "a declared code with an older schema",
			rec: Notification{ID: "c", Code: CodeGitConnectionBroken, Type: TypeConnection,
				Title: TitleGitConnectionBroken, Description: "old words " + sentinelSecret, Schema: 1},
		},
		{
			// Written by a NEWER Sharko that was then rolled back. This build
			// can vouch for it no better than for an older one.
			name: "a schema from the future",
			rec: Notification{ID: "d", Code: CodeGitConnectionBroken, Type: TypeConnection,
				Title: TitleGitConnectionBroken, Description: "old words " + sentinelSecret,
				Schema: CurrentSchema + 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := keepTrustworthy([]Notification{tc.rec, current})
			if dropped != 1 {
				t.Fatalf("expected exactly one drop, got %d", dropped)
			}
			if len(kept) != 1 || kept[0].ID != "keep" {
				t.Fatalf("the gate kept the wrong records: %+v", kept)
			}
			for _, n := range kept {
				sweep(t, "a record the gate kept ("+tc.name+")", fmt.Sprintf("%+v", n))
			}

			// Running it again changes nothing — the dropped record is gone, so
			// there is nothing left to decide about.
			twice, secondDropped := keepTrustworthy(kept)
			if secondDropped != 0 {
				t.Errorf("the second run dropped %d more — the gate is not idempotent", secondDropped)
			}
			if len(twice) != len(kept) || twice[0] != kept[0] {
				t.Errorf("the second run changed the survivors:\n first: %+v\nsecond: %+v", kept, twice)
			}
		})
	}
}

// TestDrop_DoesNotLogWhatItDropped pins the log side on its own. A load that
// logged what it was throwing away would copy the very thing the drop exists to
// remove into the server log, where whatever collects those logs keeps it.
func TestDrop_DoesNotLogWhatItDropped(t *testing.T) {
	client, cm := storeWithCM(t)
	seedReleasedRecord(t, cm)

	if got := findLeaks(storedBytes(t, client), sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the seeded ConfigMap does not contain the sentinel.")
	}

	logs := captureLogs(t, func() { NewStore(50, cm) })

	sweep(t, "the load's log output", logs)

	// It must still say SOMETHING — a silent drop is not an auditable one.
	if !strings.Contains(logs, "dropped saved notifications") {
		t.Errorf("the load recorded no outcome at all:\n%s", logs)
	}
	if !strings.Contains(logs, "count=1") {
		t.Errorf("the load did not record how many records it dropped:\n%s", logs)
	}
}

// TestSentinel_AnOldShapeWithAKnownCodeIsDroppedAndPurgedToo drives the OTHER
// branch of the gate with a sentinel-carrying record.
//
// This test exists because a break test found the hole. Every other sentinel
// test seeds the released shape, which has no code — so it leaves by the CODE
// branch, and the SHAPE branch's own log line was never swept by anything.
// Making that line print the description it was discarding stayed green across
// the whole package. Two branches throw records away; both have to be swept.
//
// The shape here is a declared code with an older schema. Only a build from
// between the two unreleased stories that added Code and Schema could have
// written it, so it is not what a real upgrade meets — but it is the exact
// shape the gate's second rule exists for, and it is reachable by hand-editing
// the ConfigMap, which anybody with kubectl can do.
func TestSentinel_AnOldShapeWithAKnownCodeIsDroppedAndPurgedToo(t *testing.T) {
	client, cm := storeWithCM(t)
	seedTypedRecords(t, cm, Notification{
		ID:    "connection-" + string(CodeGitConnectionBroken),
		Code:  CodeGitConnectionBroken,
		Type:  TypeConnection,
		Title: TitleGitConnectionBroken,
		Description: "Sharko uses this Git connection for every commit and pull request, and right now it can't reach it." +
			" Reason: " + sentinelBackendError().Error(),
		Timestamp: time.Now(),
		Schema:    CurrentSchema - 1,
	})

	if got := findLeaks(storedBytes(t, client), sentinelSecret); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the seeded ConfigMap does not contain the sentinel.")
	}

	// The production path: in-memory store, then the attach.
	store := NewStore(50, nil)
	var logs string
	logs = captureLogs(t, func() {
		if err := store.AttachCMStore(context.Background(), cm); err != nil {
			t.Fatalf("attaching the cmstore: %v", err)
		}
	})

	if n := len(store.List()); n != 0 {
		t.Fatalf("a record with an older shape was kept: %+v", store.List())
	}
	if !strings.Contains(logs, "older shape of Sharko") {
		t.Errorf("the shape branch dropped the record without saying which rule caught it:\n%s", logs)
	}

	wire, err := json.Marshal(store.List())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	sweep(t, "the list after an older-shape record was dropped", string(wire))
	sweep(t, "the ConfigMap after an older-shape record was dropped", storedBytes(t, client))
	sweep(t, "the log output of the SHAPE branch's drop", logs)

	// And the constructor path drops it the same way, writing the purge back.
	client2, cm2 := storeWithCM(t)
	seedTypedRecords(t, cm2, Notification{
		ID: "connection-" + string(CodeGitConnectionBroken), Code: CodeGitConnectionBroken,
		Type: TypeConnection, Title: TitleGitConnectionBroken,
		Description: "old words " + sentinelSecret, Timestamp: time.Now(), Schema: CurrentSchema - 1,
	})
	var logs2 string
	captureLogs(t, func() { NewStore(50, cm2) })
	logs2 = captureLogs(t, func() { NewStore(50, cm2) })
	sweep(t, "the ConfigMap after the constructor dropped an older-shape record", storedBytes(t, client2))
	if strings.Contains(logs2, "dropped a saved notification") {
		t.Errorf("the second restart dropped it again, so the first did not purge the durable copy:\n%s", logs2)
	}
}

// ── a failed read must not switch persistence on ────────────────────────────

// failConfigMapReads makes every ConfigMap GET on the fake clientset fail with
// a real API error — NOT a NotFound, which cmstore deliberately treats as
// "nothing stored yet" and reports as success. It returns a function that puts
// the clientset back to normal.
//
// This is the RBAC denial / API-server hiccup / pod-start timeout that E3 is
// about: the one case where cmstore.Read returns a non-nil error.
func failConfigMapReads(client *fake.Clientset) func() {
	var failing bool
	client.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !failing {
			return false, nil, nil
		}
		return true, nil, errors.New("configmaps is forbidden: User cannot get resource configmaps")
	})
	failing = true
	return func() { failing = false }
}

// TestAttachCMStore_AFailedReadDoesNotSwitchPersistenceOn is the regression test
// for a data-loss bug.
//
// AttachCMStore used to assign s.cmStore BEFORE reading, so a failed read
// returned with persistence wired over an in-memory list that had nothing in
// it. serve.go logged "continuing in-memory only" — which was false — and the
// next Add/MarkRead/MarkAllRead/Resolve called persistLocked and overwrote
// every saved notification, and every read/cleared state an operator had set,
// with that empty list.
//
// The ConfigMap here holds a good record. The read fails. Then a write happens.
// The good record must still be there.
func TestAttachCMStore_AFailedReadDoesNotSwitchPersistenceOn(t *testing.T) {
	client, cm := storeWithCM(t)

	saved := Notification{
		ID: "connection-" + string(CodeArgoAuthFailed), Code: CodeArgoAuthFailed,
		Type: TypeConnection, Title: TitleArgoAuthFailed,
		Description: "a description built the safe way", Read: true, Schema: CurrentSchema,
	}
	seedTypedRecords(t, cm, saved)
	before := storedBytes(t, client)

	store := NewStore(50, nil) // in-memory, exactly as serve.go builds it

	restore := failConfigMapReads(client)
	err := store.AttachCMStore(context.Background(), cm)
	restore()

	if err == nil {
		t.Fatal("AttachCMStore reported success even though the ConfigMap read failed")
	}

	// serve.go logs "continuing in-memory only" on this error. That sentence
	// has to be TRUE: a later write must not reach the ConfigMap.
	store.Add(Notification{
		ID: "connection-" + string(CodeGitConnectionBroken), Code: CodeGitConnectionBroken,
		Type: TypeConnection, Title: TitleGitConnectionBroken,
	})
	store.MarkAllRead()
	store.Resolve(CodeGitConnectionBroken)

	after := storedBytes(t, client)
	if after != before {
		t.Fatalf("a write after the failed read changed the stored state — the saved notifications were destroyed by an error the operator never caused\n before: %s\n  after: %s", before, after)
	}

	// And the in-memory half still works, so the pod is not crippled.
	if store.UnreadCount() != 0 {
		t.Errorf("the in-memory store stopped behaving normally after the failed attach")
	}

	// A later attach, once the API is healthy again, picks the saved record up
	// and persistence works from then on.
	if err := store.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("the retry after the API recovered also failed: %v", err)
	}
	if got := findByCode(store, CodeArgoAuthFailed); got == nil {
		t.Fatal("the saved record did not come back after the API recovered — it really was lost")
	} else if !got.Read {
		t.Error("the saved record came back unread, so the operator's cleared state was not preserved")
	}
}

// TestNewStore_AFailedReadLeavesTheStoreUsable covers the same failure on the
// constructor path. NewStore cannot report an error to its caller, so it logs
// and carries on; what it must not do is leave a half-loaded store behind.
func TestNewStore_AFailedReadLeavesTheStoreUsable(t *testing.T) {
	client, cm := storeWithCM(t)
	seedTypedRecords(t, cm, Notification{
		ID: "keep", Code: CodeAddonVersionDrift, Type: TypeDrift,
		Title: "Version drift: x on y", Description: "safe words", Schema: CurrentSchema,
	})
	before := storedBytes(t, client)

	restore := failConfigMapReads(client)
	var store *Store
	logs := captureLogs(t, func() { store = NewStore(50, cm) })
	restore()

	if !strings.Contains(logs, "could not load persisted notifications") {
		t.Errorf("the failed load was not reported at all:\n%s", logs)
	}
	if n := len(store.List()); n != 0 {
		t.Errorf("the store came up holding %d records it never managed to read", n)
	}
	if got := storedBytes(t, client); got != before {
		t.Errorf("the failed load changed the stored state\n before: %s\n  after: %s", before, got)
	}
}
