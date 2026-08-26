package notifications

// store_test.go — the store's behaviour: add, dedup, mark read, resolve, ring
// buffer, and ConfigMap-backed persistence.
//
// Two things changed here with the identifier work, and both are deliberate.
//
// 1. Every notification now carries a Code. Store.Add refuses one that does
//    not, so a test that omits it is not testing the store, it is testing the
//    refusal.
//
// 2. Dedup, resolve and the persistence merge no longer key on the TITLE.
//
// 3. A caller cannot choose a notification's ID, title, description or type at
//    all any more — the server renders all four from the code and a set of
//    checked identifiers (render.go). So these tests build their notifications
//    with New() and name the SUBJECT (which addon, which cluster), and they
//    read the resulting ID off the value rather than typing one in. The tests
//    that used to prove "the same alert survives a reworded title" are gone,
//    because a title cannot be supplied to be reworded.

import (
	"context"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestCMStore builds a cmstore.Store backed by a fake clientset, mirroring
// internal/prtracker's newTestTracker helper.
func newTestCMStore() *cmstore.Store {
	client := fake.NewSimpleClientset()
	return cmstore.NewStore(client, "default", "sharko-notifications")
}

// upgradeFor, driftFor and gitBroken build real notifications through the
// boundary — the only way to get a distinct one now. Each names a SUBJECT; the
// id, the title, the description and the type all come from render.go.
func upgradeFor(addon string) Notification {
	return New(CodeAddonUpgradeAvailable, "", Params{
		Addon: addon, Version: "2.0.0", CatalogVersion: "1.0.0",
	}, time.Now())
}

func driftFor(addon, cluster string) Notification {
	return New(CodeAddonVersionDrift, "", Params{
		Addon: addon, Cluster: cluster, Version: "2.0.0", CatalogVersion: "1.0.0",
	}, time.Now())
}

func gitBroken() Notification {
	return New(CodeGitConnectionBroken, ReasonUnreachable, Params{}, time.Now())
}

func TestStore_InMemory_AddAndList(t *testing.T) {
	s := NewStore(5, nil)

	first := upgradeFor("first")
	second := driftFor("second", "prod-eu")
	s.Add(first)
	s.Add(second)

	items := s.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(items))
	}
	// Newest first.
	if items[0].ID != second.ID {
		t.Errorf("expected newest first, got %s", items[0].ID)
	}
}

// TestStore_Deduplication — the same alert added twice is stored once.
func TestStore_Deduplication(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(upgradeFor("podinfo"))
	s.Add(upgradeFor("podinfo"))

	items := s.List()
	if len(items) != 1 {
		t.Errorf("expected 1 notification after dedup, got %d", len(items))
	}
}

// TestStore_TheSameSubjectIsOneAlert is the case the product owner ruled on:
// repeated reports of the same thing must not become several alerts.
//
// It used to add the same alert under three different titles to prove that the
// words were not what made an alert "new". A caller cannot supply a title now,
// so the words cannot make anything look new — the same guarantee, held
// structurally instead of tested for.
func TestStore_TheSameSubjectIsOneAlert(t *testing.T) {
	s := NewStore(10, nil)

	for i := 0; i < 3; i++ {
		s.Add(upgradeFor("podinfo"))
	}

	if got := len(s.List()); got != 1 {
		t.Errorf("re-raising one alert produced %d entries on the bell — dedup is not keying on the ID", got)
	}
}

// TestStore_DifferentSubjectsAreSeparateAlerts is the other half: sharing an
// identifier must NOT collapse genuinely different alerts. Two clusters
// drifting on the same addon are two problems, and both share the code
// addon_version_drift.
func TestStore_DifferentSubjectsAreSeparateAlerts(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(driftFor("podinfo", "prod-eu"))
	s.Add(driftFor("podinfo", "prod-us"))

	if got := len(s.List()); got != 2 {
		t.Errorf("two clusters drifting collapsed into %d alert(s) — dedup must key on the whole ID, not the code alone", got)
	}
}

func TestStore_MaxItems(t *testing.T) {
	s := NewStore(3, nil)

	for i := 0; i < 5; i++ {
		s.Add(upgradeFor(string(rune('a' + i))))
	}

	items := s.List()
	if len(items) != 3 {
		t.Errorf("expected 3 items (maxItems), got %d", len(items))
	}
}

func TestStore_MarkAllRead(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(upgradeFor("one"))
	s.Add(driftFor("two", "prod-eu"))

	if s.UnreadCount() != 2 {
		t.Fatalf("expected 2 unread, got %d", s.UnreadCount())
	}

	s.MarkAllRead()

	if s.UnreadCount() != 0 {
		t.Errorf("expected 0 unread after MarkAllRead, got %d", s.UnreadCount())
	}
}

// TestStore_MarkRead is S3's (walk day 4) single-item mark-read: only the
// matching id flips to read, everything else is untouched.
func TestStore_MarkRead(t *testing.T) {
	s := NewStore(10, nil)

	one := upgradeFor("one")
	two := driftFor("two", "prod-eu")
	s.Add(one)
	s.Add(two)

	if !s.MarkRead(two.ID) {
		t.Fatalf("expected MarkRead(%q) to return true for an existing id", two.ID)
	}
	if s.UnreadCount() != 1 {
		t.Fatalf("expected 1 unread after marking one of two read, got %d", s.UnreadCount())
	}

	items := s.List()
	for _, n := range items {
		if n.ID == two.ID && !n.Read {
			t.Errorf("expected notification %q to be Read == true", n.ID)
		}
		if n.ID == one.ID && n.Read {
			t.Errorf("expected notification %q to remain Read == false", n.ID)
		}
	}
}

// TestStore_MarkRead_UnknownIDReturnsFalse — a 404 at the store layer.
func TestStore_MarkRead_UnknownIDReturnsFalse(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(upgradeFor("one"))

	if s.MarkRead("does-not-exist") {
		t.Errorf("expected MarkRead of an unknown id to return false")
	}
	if s.UnreadCount() != 1 {
		t.Errorf("expected the existing notification to stay unaffected, got %d unread", s.UnreadCount())
	}
}

// TestStore_MarkRead_AlreadyReadIsNoOpButStillTrue — marking an
// already-read notification again is harmless and still reports found.
func TestStore_MarkRead_AlreadyReadIsNoOpButStillTrue(t *testing.T) {
	s := NewStore(10, nil)
	one := upgradeFor("one")
	s.Add(one)

	if !s.MarkRead(one.ID) {
		t.Fatalf("expected first MarkRead to return true")
	}
	if !s.MarkRead(one.ID) {
		t.Errorf("expected re-marking an already-read id to still return true")
	}
	if s.UnreadCount() != 0 {
		t.Errorf("expected 0 unread, got %d", s.UnreadCount())
	}
}

// TestStore_CMStore_MarkReadPersists confirms MarkRead's flag flip survives
// a restart, not just an in-memory effect (mirrors TestStore_CMStore_ResolvePersists).
func TestStore_CMStore_MarkReadPersists(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(10, cm)
	one := upgradeFor("one")
	two := driftFor("two", "prod-eu")
	s1.Add(one)
	s1.Add(two)
	s1.MarkRead(one.ID)

	s2 := NewStore(10, cm)
	items := s2.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications to survive restart, got %d", len(items))
	}
	for _, n := range items {
		if n.ID == one.ID && !n.Read {
			t.Errorf("expected notification %q to remain Read == true after restart", n.ID)
		}
		if n.ID == two.ID && n.Read {
			t.Errorf("expected notification %q to remain Read == false after restart", n.ID)
		}
	}
}

func TestStore_DedupBlocksReAddAfterRead(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(upgradeFor("podinfo"))
	s.MarkAllRead()
	// Marking read is an acknowledgement, not permission to re-nag: the same
	// alert must still be deduplicated after the existing entry is read.
	s.Add(upgradeFor("podinfo"))

	items := s.List()
	if len(items) != 1 {
		t.Errorf("expected 1 notification (dedup regardless of read state), got %d", len(items))
	}
}

// TestStore_ClearedNotificationDoesNotReappear is the regression test for the
// "newer version available" nag: a checker tick re-adds the identical alert
// every 30 minutes as long as the condition holds. Before that fix, marking
// the notification read let the next tick re-add it as fresh and unread,
// nagging the user forever until they upgraded. Add → MarkAllRead → Add must
// leave exactly one entry, and it must still be read.
func TestStore_ClearedNotificationDoesNotReappear(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(upgradeFor("podinfo"))
	s.MarkAllRead()
	s.Add(upgradeFor("podinfo"))

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 notification, got %d: %+v", len(items), items)
	}
	if !items[0].Read {
		t.Errorf("expected the surviving notification to remain Read == true, got Read == false")
	}
}

func TestStore_Resolve_RemovesReadAndUnread(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(gitBroken())
	s.Add(upgradeFor("other"))
	// Mark everything read so we prove Resolve removes a READ entry too, not
	// just unread ones. (The unread case is covered by
	// TestStore_CMStore_ResolvePersists, which resolves a freshly-added,
	// still-unread notification.)
	s.MarkAllRead()

	s.Resolve(CodeGitConnectionBroken)

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 notification left after resolve, got %d: %+v", len(items), items)
	}
	if items[0].Code != CodeAddonUpgradeAvailable {
		t.Errorf("Resolve removed the wrong notification; left %q", items[0].Code)
	}
}

// TestStore_Resolve_KeysOnTheCode proves Resolve clears an alert by its
// identifier. Resolving by title meant a recovered connection only cleared if
// the sentence matched exactly what it had been raised with.
func TestStore_Resolve_KeysOnTheCode(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(gitBroken())

	s.Resolve(CodeGitConnectionBroken)

	if got := len(s.List()); got != 0 {
		t.Errorf("the alert survived Resolve, so clearing it still depends on its wording: %+v", s.List())
	}
}

func TestStore_Resolve_UnknownCodeIsNoOp(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(upgradeFor("keepme"))

	// A declared code that nothing in the store carries.
	s.Resolve(CodeArgoForbidden)
	// And a value that is not declared at all.
	s.Resolve("not_a_real_code")

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Resolve of an absent code to be a no-op, got %d items", got)
	}
}

// TestStore_ResolveThenAdd_Readds is the recovered-then-recurred path: once an
// alert has been Resolved (the connection came back healthy, or whatever it
// was tracking cleared), a later Add for the same thing must succeed —
// Resolve already removed the old entry, so there is nothing left to dedup
// against.
func TestStore_ResolveThenAdd_Readds(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(gitBroken())
	s.Resolve(CodeGitConnectionBroken)
	s.Add(gitBroken())

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 notification after resolve+re-add, got %d: %+v", len(items), items)
	}
	if items[0].Read {
		t.Errorf("expected the re-added notification to be unread, got Read == true")
	}
}

func TestStore_Add_DedupStillWorks(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(gitBroken())
	s.Add(gitBroken())

	if got := len(s.List()); got != 1 {
		t.Fatalf("expected Add dedup on an unread duplicate, got %d items", got)
	}
}

// ---------------------------------------------------------------------------
// The identifier is required at the door
// ---------------------------------------------------------------------------

// TestStore_Add_RefusesAnUndeclaredCode is the server-side half of "an unknown
// identifier degrades visibly and safely". The store is the only thing the API
// reads from, so refusing here is what guarantees no undeclared identifier can
// reach a browser that would not know how to route it.
func TestStore_Add_RefusesAnUndeclaredCode(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(Notification{ID: "1", Code: "invented_on_the_spot", Title: "Something", Type: TypeUpgrade, Timestamp: time.Now()})
	if got := len(s.List()); got != 0 {
		t.Errorf("the store accepted an undeclared code: %+v", s.List())
	}

	s.Add(Notification{ID: "2", Title: "No code at all", Type: TypeUpgrade, Timestamp: time.Now()})
	if got := len(s.List()); got != 0 {
		t.Errorf("the store accepted a notification with no code: %+v", s.List())
	}
}

// TestStore_LoadDropsUndeclaredCodes covers the read side of the same rule.
// The ConfigMap outlives the process: it holds whatever an older build wrote,
// including everything written before notifications had codes at all, and it
// is an ordinary Kubernetes object somebody can edit by hand.
func TestStore_LoadDropsUndeclaredCodes(t *testing.T) {
	cm := newTestCMStore()

	// Write a mixed state directly, bypassing Add's refusal — this is exactly
	// what an older build's ConfigMap looks like.
	//
	// Everything except the genuinely old record carries the current schema, so
	// this test isolates the CODE half of keepTrustworthy. The record with no
	// code has no schema either — that is the real released shape, and it fails
	// both halves at once (see sentinel_leak_test.go for the shape half on its
	// own).
	err := cm.ReadModifyWrite(context.Background(), func(data map[string]interface{}) error {
		return encodeNotifications(data, []Notification{
			{ID: "ok", Code: CodeAddonUpgradeAvailable, Title: "Fine", Type: TypeUpgrade, Timestamp: time.Now(), Schema: CurrentSchema},
			{ID: "legacy", Title: "Written before codes existed", Type: TypeUpgrade, Timestamp: time.Now()},
			{ID: "hand-edited", Code: "someone_typed_this", Title: "Hand-edited", Type: TypeUpgrade, Timestamp: time.Now(), Schema: CurrentSchema},
		})
	})
	if err != nil {
		t.Fatalf("seeding the configmap: %v", err)
	}

	s := NewStore(10, cm)
	items := s.List()
	if len(items) != 1 {
		t.Fatalf("expected only the declared notification to be restored, got %d: %+v", len(items), items)
	}
	if items[0].ID != "ok" {
		t.Errorf("the wrong notification survived the load: %+v", items[0])
	}
}

// ---------------------------------------------------------------------------
// ConfigMap-backed persistence (cmstore), fake clientset
// ---------------------------------------------------------------------------

func TestStore_CMStore_AddPersists(t *testing.T) {
	cm := newTestCMStore()
	s := NewStore(100, cm)

	s.Add(upgradeFor("p1"))
	s.Add(driftFor("p2", "prod-eu"))

	items := s.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(items))
	}

	data, err := cm.Read(context.Background())
	if err != nil {
		t.Fatalf("reading configmap state: %v", err)
	}
	stored := extractNotifications(data)
	if len(stored) != 2 {
		t.Fatalf("expected 2 notifications stored in configmap, got %d: %+v", len(stored), stored)
	}
	for _, n := range stored {
		if n.Code == "" {
			t.Errorf("a persisted notification lost its code on the way to the ConfigMap: %+v", n)
		}
	}
}

// TestStore_CMStore_RestartPersistence is the core V2-cleanup-82.1
// regression test: a new Store built over the SAME ConfigMap (simulating a
// pod restart against the same backing store) must see the prior
// notifications, including their read/cleared flags — not just an empty
// slice, and not the disk-file behavior this replaces.
func TestStore_CMStore_RestartPersistence(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(100, cm)
	s1.Add(upgradeFor("r1"))
	s1.Add(driftFor("r2", "prod-eu"))
	s1.MarkAllRead()

	// Simulate a pod restart: build a fresh Store over the same ConfigMap.
	s2 := NewStore(100, cm)
	items := s2.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 notifications to survive restart, got %d: %+v", len(items), items)
	}
	for _, n := range items {
		if !n.Read {
			t.Errorf("expected notification %q to remain Read == true after restart, got false", n.ID)
		}
		if n.Code == "" {
			t.Errorf("notification %q lost its code across the restart", n.ID)
		}
	}
}

// TestStore_CMStore_ResolvePersists confirms Resolve's removal is durable
// across a restart, not just an in-memory effect.
func TestStore_CMStore_ResolvePersists(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(10, cm)
	s1.Add(gitBroken())
	s1.Resolve(CodeGitConnectionBroken)

	s2 := NewStore(10, cm)
	if got := len(s2.List()); got != 0 {
		t.Errorf("expected resolved notification to stay gone after restart, got %d items", got)
	}
}

// TestStore_CMStore_DedupSurvivesRestart is #477's dedup guarantee carried
// through a restart: a read entry loaded from the ConfigMap still blocks a
// re-add.
func TestStore_CMStore_DedupSurvivesRestart(t *testing.T) {
	cm := newTestCMStore()

	s1 := NewStore(10, cm)
	s1.Add(upgradeFor("podinfo"))
	s1.MarkAllRead()

	s2 := NewStore(10, cm)
	s2.Add(upgradeFor("podinfo"))

	items := s2.List()
	if len(items) != 1 {
		t.Fatalf("expected dedup to survive restart, got %d items: %+v", len(items), items)
	}
	if !items[0].Read {
		t.Errorf("expected the surviving notification to remain Read == true after restart, got false")
	}
}

// TestStore_AttachCMStore_MergesInMemoryState covers the serve.go startup
// seam: the store is constructed in-memory only (client not available yet),
// something gets added, and then AttachCMStore wires the ConfigMap in. The
// in-memory addition must survive the attach and be persisted going forward.
func TestStore_AttachCMStore_MergesInMemoryState(t *testing.T) {
	s := NewStore(10, nil)
	early := upgradeFor("early")
	s.Add(early)

	cm := newTestCMStore()
	if err := s.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("AttachCMStore: %v", err)
	}

	items := s.List()
	if len(items) != 1 || items[0].ID != early.ID {
		t.Fatalf("expected the pre-attach notification to survive, got %+v", items)
	}

	// Further mutations must now persist through the ConfigMap.
	s.Add(driftFor("later", "prod-eu"))
	data, err := cm.Read(context.Background())
	if err != nil {
		t.Fatalf("reading configmap state: %v", err)
	}
	stored := extractNotifications(data)
	if len(stored) != 2 {
		t.Fatalf("expected 2 notifications persisted after attach, got %d: %+v", len(stored), stored)
	}
}

// TestStore_AttachCMStore_MergesByID pins the merge rule. The persisted copy
// must win over an in-memory duplicate so read/cleared flags are not clobbered.
func TestStore_AttachCMStore_MergesByID(t *testing.T) {
	cm := newTestCMStore()

	// A persisted, already-read alert.
	persisted := upgradeFor("podinfo")
	persisted.Read = true
	err := cm.ReadModifyWrite(context.Background(), func(data map[string]interface{}) error {
		return encodeNotifications(data, []Notification{persisted})
	})
	if err != nil {
		t.Fatalf("seeding the configmap: %v", err)
	}

	// The same alert accumulated in memory before the attach.
	s := NewStore(10, nil)
	s.Add(upgradeFor("podinfo"))

	if err := s.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("AttachCMStore: %v", err)
	}

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("the same alert was merged as a second entry — the merge is not keying on the ID: %+v", items)
	}
	if !items[0].Read {
		t.Error("the persisted read flag was clobbered by the in-memory copy")
	}
}

func TestStore_AttachCMStore_NilIsNoOp(t *testing.T) {
	s := NewStore(10, nil)
	s.Add(upgradeFor("solo"))

	if err := s.AttachCMStore(context.Background(), nil); err != nil {
		t.Fatalf("AttachCMStore(nil) should be a no-op, got error: %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("expected store to be unaffected by a nil AttachCMStore")
	}
}

// TestStore_Persistence_MissingClient_FallsBackToMemory mirrors the old
// missing-PVC-directory fallback test: a nil cmStore (no in-cluster k8s
// client — local/dev, or a unit test that never sets one up) must still work
// as a fully-functional in-memory store, with no error and no panic.
func TestStore_Persistence_MissingClient_FallsBackToMemory(t *testing.T) {
	s := NewStore(10, nil)

	s.Add(driftFor("m1", "prod-eu"))
	if s.UnreadCount() != 1 {
		t.Errorf("expected 1 unread in fallback memory mode, got %d", s.UnreadCount())
	}
}
