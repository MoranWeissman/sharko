// Package audit provides a lightweight in-memory ring buffer for recording
// significant events that originate outside the API — webhooks, init runs,
// cluster registrations, and secret reconciliations.
package audit

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is a single audit log record.
type Entry struct {
	ID              string          `json:"id"`
	Timestamp       time.Time       `json:"timestamp"`
	Level           string          `json:"level"`    // info, warn, error
	Event           string          `json:"event"`    // cluster_registered, pr_created, etc.
	User            string          `json:"user"`     // username or "system"
	Action          string          `json:"action"`   // register, remove, update, test
	Resource        string          `json:"resource"` // cluster:prod-eu, addon:cert-manager
	Source          string          `json:"source"`   // ui, cli, api, reconciler, webhook
	Result          string          `json:"result"`   // success, failure, partial
	DurationMs      int64           `json:"duration_ms"`
	RequestID       string          `json:"request_id,omitempty"`
	Detail          string          `json:"detail,omitempty"`           // semantic detail set by handlers via Enrich
	AttributionMode AttributionMode `json:"attribution_mode,omitempty"` // how the resulting Git commit was attributed
	Tier            Tier            `json:"tier,omitempty"`             // attribution tier of the endpoint

	// Error is what a person reads about the failure, and it is NOT settable
	// by the code that creates the entry.
	//
	// SafeText's only field is unexported and package audit exports no
	// constructor, so no other package can build a non-zero one. Add rebuilds
	// this field from Reason on every entry, from a fixed catalog, before
	// anything is stored or streamed. There is no parameter, field or flag
	// through which a backend's own words can reach an audit record.
	//
	// WHY IT USED TO BE A STRING. Call sites wrote err.Error() here and set a
	// bool called CredentialFailure to tell Add whether that text was
	// dangerous. Add believed the bool. Sixteen of the seventeen sites
	// computed it with credsafe.Is on a live error, so the audit log was safe
	// exactly as far as every upstream author had remembered to mark their
	// errors — and the two ArgoCD sites in internal/remediation recorded an
	// error nothing marks, so they were guaranteed to store raw text. Safety
	// that depends on a caller-supplied flag is not safety; it is a list of
	// sites somebody has to keep correct forever.
	//
	// See reason.go for the whole design.
	Error SafeText `json:"error,omitzero" swaggertype:"string"`

	// Reason is WHAT KIND of thing went wrong, as a closed enum.
	//
	// It replaces the free-text error channel. A caller says the category —
	// usually as Classify(err), sometimes as a constant where the call site
	// knows better than any classifier could — and Add turns that category
	// into the catalog sentence in Error.
	//
	// An enum cannot carry a secret. The worst a wrong or missing Reason can
	// do is pick a less useful sentence; it can never leak, which is the whole
	// point of moving from a bool-plus-text to a category. Add blanks a value
	// that is not in the catalog, so an invented reason reaches no reader.
	//
	// Empty means "not a failure, or nobody said" — which is the right
	// answer for the entries that record an outcome rather than an error (a
	// PR closed without merging, a refused login, a deliberate skip). There
	// is deliberately NO guard forcing every failure-result entry to name a
	// reason, because those nine entries have no error to classify and
	// inventing a category for them would be a worse lie than saying
	// nothing.
	Reason Reason `json:"reason,omitempty" swaggertype:"string" enums:"credentials_source,secret_value_source,not_found,permission_denied,conflict,invalid_data,unreachable,tls,timed_out,canceled,not_converged,upstream_failure,unspecified"`

	// Changes says whether this entry's operation ACTUALLY changed anything.
	//
	// WHY IT EXISTS (ruling f, 2026-08-19). The activity feed rendered
	// "<title> · <outcome> · No changes made" with all three coming from
	// different places and nothing reconciling them — the third part was
	// invented in the browser from a static read-only flag in a title table,
	// so it could never agree with reality. It said "No changes made" on
	// operations that wrote, and stayed silent on the one case where it was
	// true. A surface cannot render the truth it was never told, so the
	// truth now travels on the entry.
	//
	// WHY A STRING ENUM AND NOT A BOOL. A bool has no way to say "this
	// question does not apply". A read-only check neither changed anything
	// nor failed to change anything, and forcing it to answer false is
	// exactly the lie being removed. A *bool could carry three states but
	// nil is ambiguous between "not applicable" and "nobody set it", and a
	// pointer on a struct that is reflected over, serialized and stored is a
	// footgun for one bit of information. A string enum states the case
	// plainly, serializes as itself, and can grow a value later without
	// changing the type.
	//
	// The four cases:
	//   ""                 unset — a writer that has not been updated, or a
	//                      stored entry from before this field existed. The
	//                      reader must render nothing, never "no changes".
	//   ChangesNotApplicable  read-only. Nothing was going to change.
	//   ChangesNone           an action ran and deliberately changed nothing.
	//   ChangesApplied        something really changed.
	//   ChangesMayBeApplied   an action stopped halfway with nothing rolled
	//                      back, so changes may be out there and we cannot
	//                      say for certain either way.
	//
	// Add normalizes an unrecognized value to "" so an uninterpretable word
	// can never reach a reader; the writer-coverage test is what catches the
	// writer that produced it.
	//
	// The swaggertype/enums tags are what put this field, and its three
	// values, into the published OpenAPI spec. GET /audit used to declare
	// map[string]interface{}, so the field was real on the wire and invisible
	// in the contract — a client author reading the spec had no way to learn
	// it exists. TestAuditSwagger_ChangesFieldIsInTheSpec fails if that
	// regresses.
	Changes ChangeResult `json:"changes,omitempty" swaggertype:"string" enums:"not_applicable,none,applied,may_be_applied"`
}

// ChangeResult is the "did anything actually change" answer on an Entry.
type ChangeResult string

const (
	// ChangesNotApplicable is a read-only operation: a check, a comparison, a
	// read. It neither changed anything nor failed to — and that is a
	// different statement from "nothing changed".
	ChangesNotApplicable ChangeResult = "not_applicable"
	// ChangesNone is an action that ran to completion and deliberately wrote
	// nothing, because nothing needed writing. This is the ONE case where
	// "No changes made" is a true thing to render.
	ChangesNone ChangeResult = "none"
	// ChangesApplied is an action that really wrote something.
	ChangesApplied ChangeResult = "applied"
	// ChangesMayBeApplied is an action that stopped part-way with nothing
	// rolled back, where the code cannot honestly say whether anything
	// landed.
	//
	// It exists because "applied" and "none" are both false claims for a
	// fan-out operation in which an item came back "partial". A partial
	// USUALLY means real changes are out there — a pull request merged, an
	// ArgoCD connection was swapped — but not always: a registration whose
	// Git commit fails before a pull request exists is also recorded as
	// partial, and on a server with no ArgoCD Secret manager and no addon
	// secrets, that one really did leave nothing behind.
	//
	// So the honest answer for a part-way result is neither of the two
	// certainties. Saying "applied" overstates; saying "none" is the R2-7
	// defect, telling an operator nothing happened when a pull request had
	// merged. This says what is actually known.
	ChangesMayBeApplied ChangeResult = "may_be_applied"
)

// Valid reports whether c is one of the four defined answers. The empty
// value is deliberately NOT valid — it means "nobody said", which a writer
// should never leave behind on purpose.
func (c ChangeResult) Valid() bool {
	switch c {
	case ChangesNotApplicable, ChangesNone, ChangesApplied, ChangesMayBeApplied:
		return true
	}
	return false
}

// Fields contains semantic enrichment that handlers attach to the in-flight audit entry.
type Fields struct {
	Event           string
	Resource        string
	Detail          string
	AttributionMode AttributionMode
	Tier            Tier

	// Result overrides the outcome the audit middleware would otherwise
	// derive from the HTTP status code (ruling f, 2026-08-19).
	//
	// The status code is the wrong source for a batch endpoint. An adoption
	// where EVERY cluster failed returns 200, because 207 is only set when
	// there is at least one success — so the audit log recorded "Cluster
	// adopted · success" for a run that adopted nothing. A handler that
	// knows its own outcome says so here; empty keeps the status-derived
	// answer, so every existing handler is unaffected.
	Result string

	// Changes is the handler's answer to "did anything actually change".
	// Empty leaves the entry's field unset, which a reader renders as
	// nothing rather than as "no changes made".
	Changes ChangeResult

	// Reason is the handler's answer to "what KIND of thing went wrong",
	// carried through to the entry the audit middleware emits.
	//
	// A handler sets it as Classify(err) at the point where the error is
	// still a live typed value. The error itself never travels — only the
	// category — and the sentence a reader sees is chosen by the sink from
	// the catalog in reason.go.
	//
	// Empty means "not a failure, or nobody said", which is the right answer
	// for every handler that succeeds.
	Reason Reason
}

type ctxKey struct{}

// WithEnrichment attaches an empty Fields pointer to the context. The audit middleware
// must call this at the start of each mutating request so handlers can fill it in.
func WithEnrichment(ctx context.Context) (context.Context, *Fields) {
	f := &Fields{}
	return context.WithValue(ctx, ctxKey{}, f), f
}

// Enrich updates the audit fields attached to ctx. Safe to call from any handler.
// If no enrichment context is present (e.g. internal callers), it is a no-op.
func Enrich(ctx context.Context, fields Fields) {
	f, ok := ctx.Value(ctxKey{}).(*Fields)
	if !ok || f == nil {
		return
	}
	if fields.Event != "" {
		f.Event = fields.Event
	}
	if fields.Resource != "" {
		f.Resource = fields.Resource
	}
	if fields.Detail != "" {
		f.Detail = fields.Detail
	}
	if fields.AttributionMode != "" {
		f.AttributionMode = fields.AttributionMode
	}
	if fields.Tier != "" {
		f.Tier = fields.Tier
	}
	// Result and Changes follow the same non-empty-wins rule as everything
	// above.
	//
	// THESE TWO WERE MISSING, AND IT MADE THE HANDLER-SIDE HALF OF RULING (f)
	// DEAD CODE. The fields were added to Fields and read by the audit
	// middleware, but Enrich — the only way a handler ever puts anything INTO
	// the in-flight Fields — did not copy them, so every handler that set
	// them was writing into a value that was thrown away on return. An
	// all-failed adoption still recorded "Cluster adopted · success"; a
	// repair that changed nothing still could not say so.
	//
	// Nothing failed anywhere, which is exactly why it survived a green
	// suite: a dropped field has no symptom at the call site. That is what
	// TestEnrich_CopiesEveryFieldOfFields exists for — it walks Fields by
	// reflection and fails on any field this function does not carry, so the
	// next field added cannot be forgotten the same way.
	if fields.Result != "" {
		f.Result = fields.Result
	}
	if fields.Changes != "" {
		f.Changes = fields.Changes
	}
	if fields.Reason != "" {
		f.Reason = fields.Reason
	}
}

// CurrentFields returns the in-flight audit fields attached to ctx, or nil if
// the request was not started by the audit middleware. Used by the Git-write
// layer to record the resolved attribution mode without going through Enrich.
func CurrentFields(ctx context.Context) *Fields {
	f, _ := ctx.Value(ctxKey{}).(*Fields)
	return f
}

// AuditFilter holds optional filter criteria for ListFiltered.
type AuditFilter struct {
	User    string
	Action  string
	Source  string
	Result  string
	Since   time.Time
	Cluster string // matches "cluster:NAME" in Resource field
	Limit   int    // default 50
}

// Log is a thread-safe in-memory ring buffer of audit entries.
//
// NOT WRITTEN TO DISK, ON PURPOSE AND STILL OPEN (P3-F1). A restart loses
// every entry, so anything read off this log — the Managed Secrets page's
// "last repaired" per row, the audit table itself — only ever covers the
// window since this server started, and says so where it shows. Writing
// the log to disk is a real gap, but it belongs with the bigger
// "what survives a restart" design (reconciler records, applied
// revisions, and this ring all lose their memory the same way), not with
// a one-file change here. Do not bolt a file writer onto Add().
type Log struct {
	mu           sync.RWMutex
	entries      []Entry
	maxSize      int
	maxWebhook   int
	webhookCount int
	subscribers  []chan Entry
}

// SourceWebhook is the Source value on an entry that came in through an
// inbound webhook rather than through a person or a token.
//
// It is a named constant because the ring treats it differently — see
// maxWebhook below — so a writer that spells it by hand would quietly opt its
// entries out of that handling.
const SourceWebhook = "webhook"

// webhookShareOfRing is the fraction of the ring that webhook-sourced entries
// may occupy between them.
//
// WHY THERE IS A CAP AT ALL. The ring holds a fixed number of entries and the
// oldest fall off the end. Without a cap, a party that can reach a webhook
// endpoint can push entries into it until every record of what a person or a
// token did has aged out — the log stays full and says nothing, which is worse
// than the log being empty, because it looks healthy. That is a cheap way to
// erase the record of something else, and it should not depend on a signature
// check upstream holding forever.
//
// WHAT THE CAP ACTUALLY BUYS, stated honestly. Webhook entries can still push
// out the first tenth of the ring as they fill their share. What they cannot
// do is keep going: once their share is full, each new webhook entry drops the
// OLDEST WEBHOOK ENTRY instead of the oldest entry overall, so the other nine
// tenths are never touched however many calls arrive. A flood loses to itself.
const webhookShareOfRing = 10

// NewLog creates a Log that retains at most maxSize entries.
// When maxSize <= 0 it defaults to 1000.
func NewLog(maxSize int) *Log {
	if maxSize <= 0 {
		maxSize = 1000
	}
	maxWebhook := maxSize / webhookShareOfRing
	if maxWebhook < 1 {
		maxWebhook = 1
	}
	return &Log{
		entries:    make([]Entry, 0, maxSize),
		maxSize:    maxSize,
		maxWebhook: maxWebhook,
	}
}

// Add prepends entry (newest first) and trims to maxSize.
// A new UUID and current timestamp are assigned automatically when the entry
// does not already carry them.
//
// THE SANITIZE STEP IS HERE ON PURPOSE, AND IT CANNOT BE BYPASSED.
// This function does two things with the value: it appends it to the ring
// (which List and ListFiltered read) and it fans the same value out to every
// SSE subscriber (which GET /audit/stream marshals raw). Sanitizing before
// both means one fix covers the table and the live stream — and a future
// reader added downstream inherits it for free, because the unsafe text was
// never stored in the first place. A fix at either read side would have missed
// the other.
//
// IT ALSO CANNOT BE TURNED OFF BY THE CALLER. sanitize takes no flag and reads
// no flag. It rebuilds Error from Reason on every entry, out of a fixed
// catalog, whatever the caller did or did not do. See sanitize.go.
func (l *Log) Add(entry Entry) {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	entry = sanitize(entry)
	// An uninterpretable change answer must never reach a reader. Blank it
	// here; the writer-coverage test is what catches the writer.
	if entry.Changes != "" && !entry.Changes.Valid() {
		entry.Changes = ""
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = append([]Entry{entry}, l.entries...)
	if entry.Source == SourceWebhook {
		l.webhookCount++
		// Over its share: drop the oldest webhook entry rather than let the
		// trim below take the oldest entry of any kind. This is the whole
		// mechanism — a webhook entry beyond the share costs another webhook
		// entry, never a record of what a person or a token did.
		if l.webhookCount > l.maxWebhook {
			for i := len(l.entries) - 1; i >= 0; i-- {
				if l.entries[i].Source == SourceWebhook {
					l.entries = append(l.entries[:i], l.entries[i+1:]...)
					l.webhookCount--
					break
				}
			}
		}
	}
	if len(l.entries) > l.maxSize {
		for _, dropped := range l.entries[l.maxSize:] {
			if dropped.Source == SourceWebhook {
				l.webhookCount--
			}
		}
		l.entries = l.entries[:l.maxSize]
	}

	// Fan out to SSE subscribers (non-blocking).
	for _, ch := range l.subscribers {
		select {
		case ch <- entry:
		default: // drop if subscriber is slow
		}
	}
}

// List returns up to limit entries, newest first. If limit <= 0 all entries
// are returned.
func (l *Log) List(limit int) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	src := l.entries
	if limit > 0 && limit < len(src) {
		src = src[:limit]
	}
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// ListFiltered returns entries matching the given filter criteria, newest first.
func (l *Log) ListFiltered(filter AuditFilter) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	var out []Entry
	for _, e := range l.entries {
		if filter.User != "" && !strings.EqualFold(e.User, filter.User) {
			continue
		}
		if filter.Action != "" && !strings.EqualFold(e.Action, filter.Action) {
			continue
		}
		if filter.Source != "" && !strings.EqualFold(e.Source, filter.Source) {
			continue
		}
		if filter.Result != "" && !strings.EqualFold(e.Result, filter.Result) {
			continue
		}
		if !filter.Since.IsZero() && e.Timestamp.Before(filter.Since) {
			continue
		}
		if filter.Cluster != "" && !resourceMatchesCluster(e.Resource, filter.Cluster) {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// resourceMatchesCluster reports whether an audit entry's Resource names
// exactly the given cluster. Resources name a cluster as "cluster:<name>" —
// as the whole resource ("cluster:prod-eu"), with a sub-resource suffix
// ("cluster:prod-eu/addon:datadog"), or as a space-separated segment
// ("pr:12 cluster:prod-eu"). The name must match EXACTLY: filtering for
// "prod" must never also return "prod-eu" (the old substring match did —
// gap G5 in the connection-reconciliation epic).
func resourceMatchesCluster(resource, cluster string) bool {
	const marker = "cluster:"
	for i := 0; i+len(marker) <= len(resource); {
		j := strings.Index(resource[i:], marker)
		if j < 0 {
			return false
		}
		start := i + j
		// The marker must open the resource or follow a separator — a
		// segment boundary (space) or a sub-resource boundary (slash).
		if start == 0 || resource[start-1] == ' ' || resource[start-1] == '/' {
			name := resource[start+len(marker):]
			if k := strings.IndexAny(name, " /"); k >= 0 {
				name = name[:k]
			}
			if name == cluster {
				return true
			}
		}
		i = start + len(marker)
	}
	return false
}

// Subscribe returns a read-only channel that receives every new audit entry,
// and an unsubscribe function that removes the subscriber and closes the channel.
func (l *Log) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 64) // buffered to avoid blocking Add()
	l.mu.Lock()
	l.subscribers = append(l.subscribers, ch)
	l.mu.Unlock()
	unsub := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for i, s := range l.subscribers {
			if s == ch {
				l.subscribers = append(l.subscribers[:i], l.subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}
	return ch, unsub
}
