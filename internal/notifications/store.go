package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// NotificationType categorises what a notification is about.
type NotificationType string

const (
	TypeUpgrade    NotificationType = "upgrade"
	TypeSecurity   NotificationType = "security"
	TypeDrift      NotificationType = "drift"
	TypeConnection NotificationType = "connection"
)

// CurrentSchema is the shape version stamped on every notification Sharko
// writes. It is how a later Sharko decides whether it can vouch for a record it
// reads back out of the ConfigMap.
//
//	1 — no schema field at all. A connection alert's description could carry a
//	    backend's own error text, because the health probes handed that text in
//	    as a plain string (see reason.go). Every build that ever shipped wrote
//	    this shape.
//	2 — descriptions of connection alerts are built from the code and the
//	    reason, both enums, both looked up in a catalog. The reason is stored
//	    alongside as a structured field. The three addon alerts still kept
//	    whatever prose their producer typed, and NOTHING checked Title, ID or
//	    Type on any alert at all.
//	3 — every field a person can read is rendered by the server from the code
//	    and a set of checked identifiers (see render.go). No caller-supplied
//	    prose reaches any of them, on any code.
//
// Judging a record BY THIS NUMBER is the whole point: the alternative —
// searching stored descriptions for something that looks sensitive — requires
// knowing the secret, which is impossible in general and is a leak surface of
// its own. The shape is knowable; the values are not.
//
// A record whose schema is not this number is DROPPED on load, not repaired.
// See keepTrustworthy for why that is the honest answer and what makes it safe.
const CurrentSchema = 3

// Notification is a single notification item.
//
// Code is the stable identifier and the only field anything may branch on.
// Reason is the structured, non-sensitive category of a connection failure —
// an enum, empty on notifications that are not about a failure. Title and
// Description are display content: prose, rewritable at any time, and never a
// key, a route, or a comparison. See codes.go and reason.go for why.
//
// ID, Type, Title and Description are all WRITTEN BY THE SERVER. Whatever a
// caller puts in them is overwritten by render() on the way into the store,
// without being read. The only dynamic material that survives is the params
// field below, and only after every identifier in it has been checked.
//
// WHAT IS PERSISTED, AND WHY EACH FIELD IS ALLOWED TO BE:
//
//	ID          — built by the server from the code and the checked
//	              identifiers. Machine values only.
//	Code        — the stable identifier, from a closed declared set.
//	Reason      — structured failure category, from a closed declared set.
//	Type        — the broad category (upgrade/security/drift/connection),
//	              derived from the code, never taken from the caller.
//	Title       — server-owned prose from render.go, plus checked identifiers.
//	Description — server-owned prose from render.go, plus checked identifiers.
//	Timestamp   — when it was raised.
//	Read        — whether the user has acknowledged it.
//	Schema      — the record shape version, see CurrentSchema.
//
// No backend's own words are in that list, and there is no field left for them
// to arrive in.
type Notification struct {
	ID          string           `json:"id"`
	Code        Code             `json:"code"`
	Reason      Reason           `json:"reason,omitempty"`
	Type        NotificationType `json:"type"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Timestamp   time.Time        `json:"timestamp"`
	Read        bool             `json:"read"`
	Schema      int              `json:"schema"`

	// params carries the identifiers the rendered words are built from.
	//
	// It is UNEXPORTED on purpose, and that is a large part of the boundary:
	// no package outside this one can set it by writing a struct literal, so
	// the only way in is New(), which takes a typed Params and hands it
	// straight to the checks. It is also invisible to encoding/json, so
	// nothing here is written to the ConfigMap or served by the API — the
	// rendered words are, and they were built from checked values.
	params Params
}

// notificationsKey is the field name under which the notifications slice is
// stored inside the ConfigMap's JSON state (see internal/cmstore).
const notificationsKey = "notifications"

// Store is a thread-safe in-memory ring buffer for notifications. When
// cmStore is non-nil, every mutation is also persisted into a Kubernetes
// ConfigMap via cmstore so notifications survive pod restarts — the
// in-memory slice remains the working copy that handlers read. When
// cmStore is nil (no k8s client available — local/dev mode, or a unit test
// without a fake clientset), the store runs in-memory only, matching the
// old file-absent fallback.
type Store struct {
	mu            sync.RWMutex
	notifications []Notification
	maxItems      int
	cmStore       *cmstore.Store // nil = in-memory only
}

// NewStore creates a Store that retains at most maxItems notifications.
// cmStore controls persistence: pass nil for in-memory only, or a
// cmstore.Store (conventionally backed by a "sharko-notifications"
// ConfigMap — see cmd/sharko/serve.go) to persist across pod restarts. When
// cmStore is non-nil, any previously-persisted notifications are loaded
// immediately so a restart restores prior read/cleared state.
func NewStore(maxItems int, cmStore *cmstore.Store) *Store {
	s := &Store{
		notifications: make([]Notification, 0),
		maxItems:      maxItems,
		cmStore:       cmStore,
	}

	if cmStore != nil {
		if err := s.loadFromCMStore(context.Background()); err != nil {
			slog.Warn("could not load persisted notifications", "error", err, "component", "notifications")
		}
	}

	return s
}

// AttachCMStore wires ConfigMap-backed persistence onto a Store that was
// constructed before the in-cluster k8s client was available. serve.go
// builds the Server (and its notification Store) well before it builds the
// in-cluster clientset used for the PR tracker's cmstore, so the notification
// store starts in-memory-only and this method upgrades it once the same
// clientset is ready — mirroring SetPRTracker/SetObservationsStore.
//
// It loads any state already persisted in the ConfigMap and merges it with
// whatever accumulated in memory since construction (e.g. an early
// notification-checker tick that ran before this call), deduplicated by
// ID. The persisted copy wins on an ID collision so read/cleared flags
// are never clobbered by an in-memory duplicate. The merged result is
// persisted immediately so all future mutations flow through cmStore.
// No-op if cmStore is nil.
//
// The merge used to deduplicate by TITLE — one more place where the words a
// person reads decided what the software did. Rewording a sentence between
// two releases would have made a restored alert and a freshly-raised one look
// like two different problems and shown both.
//
// THIS IS THE PATH A REAL POD TAKES. serve.go always builds the store with a
// nil cmStore and upgrades it here, so this is where records written by an
// older Sharko are actually read — and therefore where keepTrustworthy has to
// run and where the surviving state has to be written back. Once the read
// succeeds the persist at the end is unconditional, so the ConfigMap always
// ends up holding exactly the records that survived the gate.
//
// # A FAILED READ MUST NOT SWITCH PERSISTENCE ON
//
// The read happens BEFORE s.cmStore is assigned, and a read failure returns
// with the store still in-memory-only. That ordering is the whole fix for a
// data-loss bug, so it must not be rearranged for tidiness:
//
// cmstore.Read returns (empty, nil) when the ConfigMap simply does not exist
// yet, so an error here means a REAL API failure — an RBAC denial, an
// API-server hiccup, a timeout during pod start. If that error left cmStore
// wired, the store would be ConfigMap-backed while holding only what happened
// to be in memory, and the very next Add/MarkRead/MarkAllRead/Resolve would
// call persistLocked and overwrite the stored list with that much smaller one.
// Every saved notification, and every read/cleared state an operator had set,
// would be gone — destroyed by a transient error the operator never caused.
//
// Failing this way instead leaves the ConfigMap untouched and byte-identical,
// which is the direction to fail in: nothing is written when Sharko does not
// know what is already there. The pod keeps its notifications in memory for
// the rest of its life and the next restart gets a fresh attempt. It also makes
// serve.go's log line — "continuing in-memory only" — literally true, which it
// was not before.
//
// A read failure is deliberately NOT fatal. This runs during pod start, and a
// momentary API-server error would otherwise crash-loop the whole server over
// its notification bell, which no other part of Sharko depends on. Losing
// persistence for one pod's lifetime is the proportionate cost; losing the
// server is not.
func (s *Store) AttachCMStore(ctx context.Context, cmStore *cmstore.Store) error {
	if cmStore == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// READ FIRST. Only wire persistence once the read has actually succeeded —
	// see the "A FAILED READ MUST NOT SWITCH PERSISTENCE ON" note above.
	data, err := cmStore.Read(ctx)
	if err != nil {
		return err
	}
	s.cmStore = cmStore

	persisted, dropped := keepTrustworthy(extractNotifications(data))
	logDropped(dropped)

	merged := persisted
	for _, n := range s.notifications {
		exists := false
		for _, p := range persisted {
			if p.ID == n.ID {
				exists = true
				break
			}
		}
		if !exists {
			merged = append(merged, n)
		}
	}
	if len(merged) > s.maxItems {
		merged = merged[:s.maxItems]
	}
	s.notifications = merged

	return s.persistLocked(ctx)
}

// Add inserts a notification at the front. If a notification with the same
// ID already exists — read or unread — it is silently dropped
// (deduplication).
//
// A notification whose Code is not declared is REFUSED, not stored. That
// refusal is the server half of "an unknown identifier degrades visibly and
// safely": the store is the only thing the API reads from, so a code outside
// the declared set cannot reach a browser that would not know how to route
// it. It is logged at error level with the offending value, because a
// notification Sharko meant to raise and then dropped is a bug in Sharko, not
// a condition an operator can fix.
func (s *Store) Add(n Notification) {
	if !n.Code.IsDeclared() {
		slog.Error("refusing to store a notification with an undeclared code — nothing was shown to the user",
			"code", n.Code.String(), "id", n.ID, "component", "notifications")
		return
	}
	sanitizeNotification(&n)

	s.mu.Lock()
	defer s.mu.Unlock()
	// Deduplicate by ID regardless of read state. Marking a notification
	// read is an acknowledgement, not an invitation to re-nag: the periodic
	// checker (checker.go) re-scans and calls Add with the same ID every
	// tick as long as the underlying condition (e.g. a newer version) still
	// holds. If dedup only blocked unread duplicates, "mark all as read"
	// would flip the existing entry to read, and the very next tick would
	// see no unread match and re-add the identical alert — resurrecting
	// something the user just cleared. A genuinely new development (e.g. an
	// even newer version) produces a different ID, so it is unaffected and
	// still gets through. Alerts that must re-fire after being cleared (e.g.
	// a connection that recovers and later breaks again) go through Resolve
	// first, which removes the old entry so a later Add is not a duplicate.
	//
	// This used to key on Title, which made the dedup rule depend on the
	// exact words a person reads: rewording one sentence would have split
	// one alert into two, and two alerts into one, with nothing failing.
	// Every ID is built by the server from the code plus the checked
	// identifiers the alert is about (see render.go) — machine values only,
	// no prose anywhere in them, and nothing a caller chose.
	for _, existing := range s.notifications {
		if existing.ID == n.ID {
			return
		}
	}
	s.notifications = append([]Notification{n}, s.notifications...)
	if len(s.notifications) > s.maxItems {
		s.notifications = s.notifications[:s.maxItems]
	}
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after add", "error", err, "component", "notifications")
	}
}

// sanitizeNotification is the mandatory safety boundary. Store.Add runs it on
// every record on the way in, and there is no switch, no flag, no per-code
// exemption and no argument that turns any part of it off.
//
// Three things happen, in this order:
//
//   - The reason is checked against the declared set. Reason is a string type,
//     so Reason(err.Error()) compiles; an undeclared value becomes
//     ReasonUnspecified here and never reaches the ConfigMap or the API.
//   - ID, Type, Title and Description are RENDERED by render() from the code
//     and the checked identifiers on the record. Whatever the caller wrote in
//     those four fields is overwritten without being read.
//   - The record is stamped with the current schema, so a later Sharko can
//     tell by shape that it does not need migrating.
//
// # There used to be three exceptions, and they were exceptions in the security sense
//
// Three addon codes were listed as "producer-owned": for those, the caller's
// Description was kept verbatim. The reason given was that no error could reach
// that producer today — an argument from the current call graph, which is
// exactly the argument the same file warned against.
//
// The three distinctions those exceptions protected are real and they survive.
// They survive as three typed codes with three server-owned templates, filled
// from identifiers Sharko checked (see render.go). An upgrade, a major-version
// change and a drift still read differently to a person. What changed is WHERE
// the sentence is composed: here, from a table, never at the call site.
//
// Title, ID and Type were not covered by any rule at all before this, which was
// the wider hole — Title is the line a person actually reads, and it was
// caller-composed prose that got persisted. All three are inside the boundary
// now.
//
// Running it twice cannot degrade a record: the parameters travel on the struct
// and every step is a lookup or a concatenation of already-checked values, so
// the second run computes the same answer as the first.
func sanitizeNotification(n *Notification) {
	n.Reason = n.Reason.sanitised()
	render(n)
	n.Schema = CurrentSchema
}

// List returns a snapshot of all notifications, newest first.
func (s *Store) List() []Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Notification, len(s.notifications))
	copy(result, s.notifications)
	return result
}

// MarkAllRead marks every notification as read.
func (s *Store) MarkAllRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notifications {
		s.notifications[i].Read = true
	}
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after mark all read", "error", err, "component", "notifications")
	}
}

// MarkRead marks the single notification with the given id as read. Returns
// true if a notification with that id was found (and marked read — marking
// an already-read notification again is a no-op that still returns true),
// false if no notification with that id exists.
func (s *Store) MarkRead(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notifications {
		if s.notifications[i].ID == id {
			s.notifications[i].Read = true
			if err := s.persistLocked(context.Background()); err != nil {
				slog.Warn("could not persist after mark read", "error", err, "component", "notifications")
			}
			return true
		}
	}
	return false
}

// Resolve removes every notification carrying the given code, regardless of
// read/unread state. It is how a previously-reported problem clears itself
// once the underlying condition recovers (e.g. a broken connection that comes
// back healthy). A code with no matches is a no-op.
//
// It used to take a TITLE. That meant a recovered connection cleared its
// alert by re-typing the same sentence the alert was raised with — so
// rewording the sentence in one place and not the other would have left the
// alert stuck on the bell forever, with the connection healthy and nothing
// reporting a problem.
//
// Note the scope: this clears ALL notifications with that code. Every code
// the connection poller resolves has at most one open alert at a time, which
// is what makes that the right behaviour. A code that can be open for many
// subjects at once (the addon codes, one per addon or cluster) must not be
// passed here without first deciding that clearing all of them is meant.
func (s *Store) Resolve(code Code) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.notifications[:0]
	for _, n := range s.notifications {
		if n.Code != code {
			kept = append(kept, n)
		}
	}
	s.notifications = kept
	if err := s.persistLocked(context.Background()); err != nil {
		slog.Warn("could not persist after resolve", "error", err, "component", "notifications")
	}
}

// UnreadCount returns how many notifications have not yet been read.
func (s *Store) UnreadCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, n := range s.notifications {
		if !n.Read {
			count++
		}
	}
	return count
}

// persistLocked writes the current in-memory notifications slice into the
// ConfigMap. Must be called with s.mu held (read or write — JSON marshal
// does not mutate state, but every current caller holds the write lock).
// It is a no-op when cmStore is nil (in-memory only).
func (s *Store) persistLocked(ctx context.Context) error {
	if s.cmStore == nil {
		return nil
	}
	snapshot := s.notifications
	return s.cmStore.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		return encodeNotifications(data, snapshot)
	})
}

// loadFromCMStore reads persisted notifications from the ConfigMap into the
// store. Called once from NewStore when cmStore is supplied at construction
// time (no lock needed — the store is not yet visible to other goroutines).
func (s *Store) loadFromCMStore(ctx context.Context) error {
	data, err := s.cmStore.Read(ctx)
	if err != nil {
		return err
	}
	loaded, dropped := keepTrustworthy(extractNotifications(data))
	if len(loaded) > s.maxItems {
		loaded = loaded[:s.maxItems]
	}
	s.notifications = loaded

	// WRITE THE SURVIVING STATE BACK. Filtering on read and stopping there
	// would leave the dropped record sitting in the ConfigMap for every future
	// restart to read again — a fix that passes a single-restart test and
	// fails the actual requirement. TestSentinel_SecondRestartIsAlsoClean
	// asserts on the ConfigMap's own bytes for exactly this reason.
	//
	// The trigger is "did anything get dropped", NOT "did anything get
	// rewritten". Those used to be different questions, and the gate asked the
	// wrong one: dropping a record did not count as a cleanup, so the record
	// that had actually been thrown out of memory was left on disk and thrown
	// out again on the next start, forever. Whatever it carried never left the
	// ConfigMap by this path at all.
	if dropped > 0 {
		logDropped(dropped)
		if err := s.persistLocked(ctx); err != nil {
			slog.Warn("could not save the notifications that were kept — the dropped ones are still stored and will be dropped again on the next start",
				"error", err, "component", "notifications")
		}
	}
	return nil
}

// keepTrustworthy drops every restored notification Sharko cannot vouch for,
// and reports how many it dropped.
//
// # The one question
//
// The ConfigMap outlives the process. It holds whatever an older build wrote,
// including everything written before notifications had codes or a shape
// version at all, and it is an ordinary Kubernetes object somebody can edit by
// hand. So every record read back out of it has to answer one question before
// anything is done with it: can this Sharko vouch for what is in it?
//
// It can, only if BOTH are true:
//
//   - the code is one Sharko declares, so the browser knows where to route it
//     and the store is not serving an identifier nothing understands;
//   - the shape is the current one, so the description was built the safe way —
//     from the code and the reason, both enums, both looked up in a catalog —
//     rather than by pasting in whatever a backend said.
//
// Anything else is dropped. Not repaired, not rewritten, not partially kept.
//
// # Why dropping, and not rewriting
//
// This used to be two passes with two different answers: an unknown code was
// dropped, while a known code with an old shape had its description replaced by
// a stock sentence. That distinction never existed in the field. Codes and the
// shape version arrived in the SAME unreleased story, so every record any
// shipped Sharko ever wrote has neither — it is dropped by the first rule and
// the rewriting pass could not reach it. The rewriting pass, its per-code
// catalog of stock sentences, and the tests that exercised it all described a
// record that no released build could produce.
//
// Dropping is also the stronger answer for the case the rewriting was meant to
// cover. A rewritten record keeps its ID, its title and its read flag while its
// description is thrown away and replaced — so a person is left holding an
// alert whose text no longer came from the event that raised it. Dropping says
// the true thing instead: Sharko could not vouch for this saved record, so it
// is gone.
//
// # Why that is safe, and not data loss
//
// Nothing here is a source of truth. Every notification is DERIVED — the
// connection poller re-raises any connection alert that is still real on its
// next tick (a minute at most), and the addon checker re-raises any upgrade,
// security or drift alert on its next one. Dropping a stored copy of a
// still-true fact costs one tick; keeping a record whose contents Sharko cannot
// account for costs whatever is in it.
//
// The one thing genuinely lost is the read/cleared flag on a dropped record, so
// a problem the operator had acknowledged can come back unread. That is the
// right trade for a record Sharko cannot vouch for: if the problem is still
// real the operator is told again, and if it is not, nothing is raised at all.
//
// # Forward, not just backward
//
// The shape test is `!= CurrentSchema`, not `<`. A record stamped with a HIGHER
// version was written by a newer Sharko that somebody then rolled back, and this
// build can vouch for its contents no better than for an older one. Both
// directions are unknown, so both are dropped.
//
// This is a deliberate, standing policy and not an accident of one migration:
// Sharko does not carry notification records across a change of shape, it
// re-derives them. A future CurrentSchema = 4 therefore drops today's records
// on upgrade, and that is the intended behaviour.
//
// # It is not silent
//
// Each drop is logged with its code and ID — machine values, both. Nothing that
// was dropped BECAUSE Sharko could not vouch for it is copied into the log: the
// description is never read, measured, sampled or compared, only discarded.
func keepTrustworthy(loaded []Notification) ([]Notification, int) {
	kept := make([]Notification, 0, len(loaded))
	dropped := 0
	for _, n := range loaded {
		switch {
		case !n.Code.IsDeclared():
			slog.Warn("dropped a saved notification with a code this Sharko does not know — it will be raised again on the next check if the problem is still there",
				"code", n.Code.String(), "id", n.ID, "component", "notifications")
			dropped++
		case n.Schema != CurrentSchema:
			slog.Warn("dropped a saved notification written in an older shape of Sharko — it will be raised again on the next check if the problem is still there",
				"code", n.Code.String(), "id", n.ID, "schema", n.Schema, "component", "notifications")
			dropped++
		default:
			kept = append(kept, n)
		}
	}
	return kept, dropped
}

// logDropped records the OUTCOME of a load — a count and nothing else.
//
// Not the descriptions it discarded, not their length, not a sample. A load
// that logged what it was throwing away would have copied the very thing the
// drop exists to remove into the server log, where it would then survive in
// whatever collects those logs.
func logDropped(dropped int) {
	if dropped == 0 {
		return
	}
	slog.Info("dropped saved notifications this Sharko cannot vouch for",
		"count", dropped, "component", "notifications")
}

// extractNotifications reads the notifications slice out of the ConfigMap's
// generic JSON state map. Mirrors internal/prtracker's extractPRs pattern.
func extractNotifications(data map[string]interface{}) []Notification {
	raw, ok := data[notificationsKey]
	if !ok {
		return nil
	}

	// data comes from a generic JSON unmarshal into map[string]interface{},
	// so re-marshal and unmarshal into the typed slice.
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	var result []Notification
	if err := json.Unmarshal(b, &result); err != nil {
		slog.Warn("failed to unmarshal notifications from state", "error", err, "component", "notifications")
		return nil
	}
	return result
}

// encodeNotifications writes the notifications slice back into the
// ConfigMap's generic JSON state map. Mirrors internal/prtracker's
// encodePRs pattern.
func encodeNotifications(data map[string]interface{}, notifications []Notification) error {
	b, err := json.Marshal(notifications)
	if err != nil {
		return err
	}
	var raw interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	data[notificationsKey] = raw
	return nil
}
