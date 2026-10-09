package notifications

// render.go — the ONE place a notification's words and its key are written.
//
// # What was wrong
//
// sanitizeNotification rebuilt the Description from the Code and the Reason,
// EXCEPT for three addon codes that were listed as "producer-owned": for those
// three, whatever prose the caller had typed was kept verbatim and persisted.
// The justification written next to the list was "no error is representable in
// checker.go's producer" — an argument from today's call graph, which the same
// file warned against two paragraphs earlier.
//
// And it never touched Title, ID or Type at all. Every one of those is a string
// (Title outright; Code, Reason and NotificationType are string types, so
// NotificationType(err.Error()) compiles), every one is persisted into the
// sharko-notifications ConfigMap, and every one is served on GET
// /notifications. Title is the line a person actually reads on the bell. That
// was a wider hole than the Description one, and it had no rule over it at all.
//
// # The model now
//
// Every write goes through the same boundary and nothing can step around it:
//
//   - The dynamic material a notification may carry is a Params value —
//     four named identifier fields, and no field of any other kind.
//   - Params is validated. An addon or cluster name must satisfy the one name
//     rule Sharko has (models.IsValidResourceName); a version must look like a
//     version. Anything else is not interpolated at all.
//   - ID, Type, Title and Description are then RENDERED by this file from the
//     Code and the validated Params. Whatever the caller put in those fields is
//     overwritten, always, without being read.
//   - A code with no template, or params that do not validate, gets the generic
//     safe sentence and a generic title.
//
// So the three addon alerts stay distinguishable — an upgrade, a major-version
// change and a drift still read differently — but the sentences are Sharko's,
// composed here, from identifiers Sharko checked. They are not exceptions to
// sanitisation; they are structured safe outcomes after it.
//
// # Why a version needs its own rule and not the name rule
//
// models.IsValidResourceName rejects the dots a version is made of, so a
// version checked against it would never render and all three addon alerts
// would collapse to the generic sentence — the exact loss of information the
// old exemption existed to avoid. isSafeVersion is the narrow companion: an
// optional leading v, then a DIGIT, then version characters only, capped short.
// Between them the two rules reject every shape a leak arrives in — an error
// sentence (spaces), a URL or a remote with credentials in it (":", "/", "@"),
// a padded base64 blob ("=", "/"), a key=value pair ("=") — and reject anything
// longer than an identifier plausibly is.
//
// # What the rules are NOT
//
// They are a second line, not the first. The first line is that Params has no
// free-text field: outside this package the only way to put dynamic detail on a
// notification is to name it Addon, Cluster, Version or CatalogVersion and have
// it survive the check. A determined caller could still put a short
// digit-leading string in Version that happened to be sensitive; nothing about
// a charset can tell. What the charset DOES guarantee is that no error text, no
// URL, no credential-bearing remote and no encoded blob can be interpolated,
// and that is the class of leak this package has actually produced twice.

import (
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TitleUnclassified is the title of a notification Sharko cannot describe —
// an unknown code, or parameters it would not vouch for. It says the honest
// thing rather than guessing, and it carries nothing that came from a caller.
const TitleUnclassified = "Sharko raised an alert"

// unclassifiedID is the id suffix (and, for an unknown code, the whole id) of a
// notification Sharko could not describe. A fixed word, never anything derived
// from what the caller passed.
const unclassifiedID = "unclassified"

// maxNameLength caps an addon or cluster name. models.ResourceNamePattern has
// no length bound of its own; a Kubernetes name never exceeds a DNS label, so
// anything longer is not the identifier it claims to be.
const maxNameLength = 63

// maxVersionLength caps a version string. Real chart versions are well under
// this even with a long pre-release suffix.
const maxVersionLength = 32

// Params is the ONLY dynamic material that may reach a rendered notification.
//
// Every field is an identifier of something Sharko is telling you about — never
// an explanation, never an error, never anything a backend said. There is no
// field for prose and there must never be one: TestBoundary_ParamsHasNoProseField
// fails on a field this list does not name.
//
// A zero Params is legitimate — every connection alert uses one, because which
// connection is broken is already carried by the Code.
type Params struct {
	// Addon is the addon's name, as the catalog spells it.
	Addon string
	// Cluster is the cluster's name, as managed-clusters.yaml spells it.
	Cluster string
	// Version is the version being announced: the newer one for an upgrade,
	// the one actually running for a drift.
	Version string
	// CatalogVersion is the version the catalog pins.
	CatalogVersion string
}

// paramField names one field of Params, so a template can say which fields it
// needs and the check can name the one that failed.
type paramField string

const (
	fieldAddon          paramField = "Addon"
	fieldCluster        paramField = "Cluster"
	fieldVersion        paramField = "Version"
	fieldCatalogVersion paramField = "CatalogVersion"
)

// isSafeName reports whether s is a name Sharko would accept anywhere else.
//
// It is models.IsValidResourceName — the ONE name rule in the tree — plus a
// length bound. Deliberately not a second, looser copy: the day a second name
// rule exists is the day the two disagree about what a cluster is called.
func isSafeName(s string) bool {
	return len(s) <= maxNameLength && models.IsValidResourceName(s)
}

// isSafeVersion reports whether s has the shape of a version.
//
// Optional leading "v", then a digit, then digits, letters, dots, dashes and
// pluses only. No spaces, no colons, no slashes, no "@", no "=".
func isSafeVersion(s string) bool {
	if s == "" || len(s) > maxVersionLength {
		return false
	}
	body := strings.TrimPrefix(s, "v")
	if body == "" {
		return false
	}
	if body[0] < '0' || body[0] > '9' {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c >= '0' && c <= '9',
			c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c == '.', c == '-', c == '+':
		default:
			return false
		}
	}
	return true
}

// valid reports whether every field this template needs is present and is the
// kind of identifier it claims to be. A field the template does not need is not
// looked at — it is not going to be rendered either.
func (p Params) valid(needs []paramField) bool {
	for _, f := range needs {
		switch f {
		case fieldAddon:
			if !isSafeName(p.Addon) {
				return false
			}
		case fieldCluster:
			if !isSafeName(p.Cluster) {
				return false
			}
		case fieldVersion:
			if !isSafeVersion(p.Version) {
				return false
			}
		case fieldCatalogVersion:
			if !isSafeVersion(p.CatalogVersion) {
				return false
			}
		default:
			// A field name nothing knows about cannot be checked, so it
			// cannot be trusted.
			return false
		}
	}
	return true
}

// messageTemplate is what Sharko says about one Code, and how it keys it.
//
// Every function here is a pure function of the validated Params and the
// sanitised Reason. None of them is ever handed a caller's string.
type messageTemplate struct {
	// typ is the broad category. Derived from the code, never taken from the
	// caller — NotificationType is a string type, so it was a text channel too.
	typ NotificationType
	// needs lists the Params fields this template interpolates. If any of them
	// fails its check, nothing is interpolated and the generic sentence is
	// used instead.
	needs []paramField
	// id builds the notification's key. Called only when needs are satisfied.
	id func(p Params) string
	// title builds the line on the bell.
	title func(p Params) string
	// detail builds the explanation under it.
	detail func(p Params, r Reason) string
}

// connectionTemplate is the shape all five connection alerts share: a fixed
// title from the catalog, a description that is two catalog lookups, an ID
// built from the code, and no parameters at all.
func connectionTemplate(code Code, title string) messageTemplate {
	return messageTemplate{
		typ:    TypeConnection,
		needs:  nil,
		id:     func(Params) string { return "connection-" + code.String() },
		title:  func(Params) string { return title },
		detail: func(_ Params, r Reason) string { return descriptionFor(code, r) },
	}
}

// messageTemplates is the closed table: one entry per declared Code, and
// nothing renders without one.
//
// # THE THREE ADDON MESSAGES, WRITTEN OUT
//
// These are the three distinctions the product owner ruled must survive. They
// survive as three typed codes producing three approved server-owned templates,
// filled from checked identifiers:
//
//	addon_upgrade_available
//	  title  "<addon> <version> available"
//	  detail "Upgrade from <catalogVersion> to <version>"
//	addon_major_update
//	  title  "Major update: <addon> <version>"
//	  detail "Major version change from <catalogVersion> — review for security patches"
//	addon_version_drift
//	  title  "Version drift: <addon> on <cluster>"
//	  detail "Running <version>, catalog has <catalogVersion>"
//
// TestBoundary_TheThreeAddonMessagesAreExact pins those six sentences by their
// literal text, and TestBoundary_TheThreeAddonMessagesStayDistinct proves a
// person can still tell the three apart.
//
// A code missing from this table, or an entry for a code that no longer exists,
// is reported BY NAME by TestBoundary_EveryCodeHasATemplate.
var messageTemplates = map[Code]messageTemplate{
	CodeGitConnectionBroken: connectionTemplate(CodeGitConnectionBroken, TitleGitConnectionBroken),
	CodeArgoRepoBroken:      connectionTemplate(CodeArgoRepoBroken, TitleArgoRepoBroken),
	CodeArgoAuthFailed:      connectionTemplate(CodeArgoAuthFailed, TitleArgoAuthFailed),
	CodeArgoUnreachable:     connectionTemplate(CodeArgoUnreachable, TitleArgoUnreachable),
	CodeArgoForbidden:       connectionTemplate(CodeArgoForbidden, TitleArgoForbidden),

	CodeAddonUpgradeAvailable: {
		typ:   TypeUpgrade,
		needs: []paramField{fieldAddon, fieldVersion, fieldCatalogVersion},
		id:    func(p Params) string { return "upgrade-" + p.Addon + "-" + p.Version },
		title: func(p Params) string { return p.Addon + " " + p.Version + " available" },
		detail: func(p Params, _ Reason) string {
			return "Upgrade from " + p.CatalogVersion + " to " + p.Version
		},
	},
	CodeAddonMajorUpdate: {
		typ:   TypeSecurity,
		needs: []paramField{fieldAddon, fieldVersion, fieldCatalogVersion},
		id:    func(p Params) string { return "security-" + p.Addon + "-" + p.Version },
		title: func(p Params) string { return "Major update: " + p.Addon + " " + p.Version },
		detail: func(p Params, _ Reason) string {
			return "Major version change from " + p.CatalogVersion + " — review for security patches"
		},
	},
	CodeAddonVersionDrift: {
		typ:   TypeDrift,
		needs: []paramField{fieldAddon, fieldCluster, fieldVersion, fieldCatalogVersion},
		id:    func(p Params) string { return "drift-" + p.Addon + "-" + p.Cluster },
		title: func(p Params) string { return "Version drift: " + p.Addon + " on " + p.Cluster },
		detail: func(p Params, _ Reason) string {
			return "Running " + p.Version + ", catalog has " + p.CatalogVersion
		},
	},
}

// New builds a notification whose words Sharko owns.
//
// This is the ONLY way to put dynamic detail on a notification. A caller may
// still write a Notification struct literal by hand, but every field New sets
// here is overwritten by the boundary on the way into the store, so a
// hand-built one simply renders generic. There is no third option and no flag.
//
// The timestamp and the read flag are the caller's; they are not text.
func New(code Code, reason Reason, p Params, ts time.Time) Notification {
	n := Notification{
		Code:      code,
		Reason:    reason,
		Timestamp: ts,
		params:    p,
	}
	sanitizeNotification(&n)
	return n
}

// render fills in ID, Type, Title and Description from the code and the
// parameters, and reads nothing else off the notification it is given.
//
// It is called by sanitizeNotification, which Store.Add calls on every record,
// so there is no path into the store that skips it.
//
// Running it twice cannot change the answer: the parameters travel on the
// struct in an unexported field, and every step is a lookup or a concatenation
// of already-checked values.
func render(n *Notification) {
	tpl, known := messageTemplates[n.Code]
	if !known {
		// An undeclared code. Store.Add refuses these outright, so this is the
		// answer for a direct call — and it is the safe one: nothing the caller
		// wrote survives.
		//
		// The ID does NOT embed the code here, unlike the case below. Code is a
		// string type, so Code(err.Error()) compiles, and an undeclared value is
		// by definition one nothing has checked — putting it in the ID would put
		// a caller's text back into a field that gets persisted and served.
		n.Type = ""
		n.ID = unclassifiedID
		n.Title = TitleUnclassified
		n.Description = n.Reason.sentence()
		return
	}

	n.Type = tpl.typ

	if !n.params.valid(tpl.needs) {
		// Something arrived that is not the identifier it claims to be. It is
		// not interpolated, not echoed, not measured and not logged — the
		// alert degrades to the generic safe sentence.
		//
		// Every alert of one code whose parameters failed shares one ID, so
		// the store keeps the first and drops the rest. That is deliberate:
		// a record Sharko cannot describe is worth telling somebody about
		// once, not once per unnamed subject.
		// The code IS safe to embed here: it came out of messageTemplates, so
		// it is one of the declared values.
		n.ID = string(n.Code) + "-" + unclassifiedID
		n.Title = TitleUnclassified
		n.Description = n.Reason.sentence()
		return
	}

	n.ID = tpl.id(n.params)
	n.Title = tpl.title(n.params)
	n.Description = tpl.detail(n.params, n.Reason)
}
