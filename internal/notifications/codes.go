package notifications

// codes.go — the stable identifier every notification carries, and the closed
// set of values it may hold.
//
// # Why this exists
//
// The bell used to be routed by its own words. The browser decided which of
// two connection panels received a notification's detail by string-comparing
// the notification's TITLE against a copy of that title typed into browser
// code, and the server did the same thing to itself: the store deduplicated
// by title, resolved a recovered alert by title, merged persisted state by
// title, and the connection poller tracked which alert was open by
// remembering its title. Capitalising one letter of one sentence would have
// broken every one of those, silently, with no test failing and no error
// appearing anywhere.
//
// Product owner's ruling: titles and descriptions are display content only.
// Nothing may route, group, deduplicate, key a map, count, or otherwise
// behave differently because of the words a person reads.
//
// So every notification carries a Code. The Code is the contract. The title
// is prose, and prose may be rewritten at any time without telling anybody.
//
// # This is deliberately NOT the Type field
//
// Notification.Type (upgrade / security / drift / connection) already exists
// and stays. It is a broad category — five different connection alerts all
// share Type "connection", which is exactly why it could never be the routing
// key: the browser's whole problem is telling the Git connection panel apart
// from the ArgoCD one, and Type calls them the same thing. Code is the fine
// identifier: one value per distinct thing the server can tell you about.
//
// # The field is named `code`, not `kind`
//
// "kind" and "type" are synonyms, and a struct carrying both invites the
// reader to guess which is which. "code" says what it is — a machine value
// you look up, never show — and it matches the naming the codebase already
// uses for exactly this idea in verify.ErrorCode.
//
// # THESE VALUES ARE A WIRE CONTRACT
//
// They ship in the API response, in the OpenAPI spec, and in the generated
// TypeScript that the browser routes on. Renaming one breaks every consumer,
// exactly like renaming a JSON field. Add freely; rename only deliberately.
//
// # Adding a new notification
//
// 1. Declare a constant here and add it to declaredCodes.
// 2. Add a template for it in render.go — the words a person will read, and
//    which checked identifiers they are built from.
// 3. Build the notification with New(), passing that code.
//
// No step is optional and none is left to memory: the guards in codes_test.go
// fail BY NAME on a producer that does not go through New(), on a code that is
// declared but never emitted anywhere, and on two codes sharing one value, and
// TestBoundary_EveryCodeHasATemplate fails BY NAME on a code with no template
// and on a template for a code that is gone. Store.Add refuses to store a
// notification whose Code is not declared, so an undeclared value cannot reach
// the API even if every test were deleted; a declared code with no template
// renders the generic safe sentence rather than anything a caller wrote.

// Code is the stable identifier for what a notification is about. It is a
// named type rather than a bare string so the compiler helps: a plain string
// will not pass where a Code is wanted, and every value comes from the
// declared set below.
type Code string

const (
	// CodeGitConnectionBroken — Sharko cannot reach its own Git connection,
	// the one it uses for every commit and pull request.
	CodeGitConnectionBroken Code = "git_connection_broken"
	// CodeArgoRepoBroken — ArgoCD looked at the repo and cannot sync it.
	CodeArgoRepoBroken Code = "argocd_repo_broken"
	// CodeArgoAuthFailed — ArgoCD rejected the token Sharko authenticates
	// with. A credential problem, not a repo-sync problem.
	CodeArgoAuthFailed Code = "argocd_auth_failed"
	// CodeArgoUnreachable — Sharko got no usable answer from ArgoCD at all,
	// so it never established that anything is broken, only that it could
	// not check.
	CodeArgoUnreachable Code = "argocd_unreachable"
	// CodeArgoForbidden — ArgoCD accepted the token but refused it
	// permission to read applications. A permission problem.
	CodeArgoForbidden Code = "argocd_forbidden"

	// CodeAddonUpgradeAvailable — a newer major or minor chart version of an
	// addon exists than the catalog pins.
	CodeAddonUpgradeAvailable Code = "addon_upgrade_available"
	// CodeAddonMajorUpdate — the newer version is a MAJOR bump, which often
	// carries security fixes and always carries breaking changes.
	CodeAddonMajorUpdate Code = "addon_major_update"
	// CodeAddonVersionDrift — the version running on a cluster is not the
	// version the catalog pins.
	CodeAddonVersionDrift Code = "addon_version_drift"
)

// declaredCodes is the closed set. A Code outside it is not a Code Sharko
// owns, and Store.Add refuses to store one.
//
// It is a SLICE, not a map, and DeclaredCodes returns it in this order —
// so the generated TypeScript is byte-stable across runs without sorting,
// and so a guard can report which codes are missing BY NAME rather than
// telling somebody a count is wrong.
var declaredCodes = []Code{
	CodeGitConnectionBroken,
	CodeArgoRepoBroken,
	CodeArgoAuthFailed,
	CodeArgoUnreachable,
	CodeArgoForbidden,
	CodeAddonUpgradeAvailable,
	CodeAddonMajorUpdate,
	CodeAddonVersionDrift,
}

// DeclaredCodes returns every code Sharko may emit, in a stable order.
//
// SOURCE OF TRUTH for cmd/gen-notification-codes, which reads this at runtime
// and emits ui/src/generated/notification-codes.ts. The generator parses no Go
// source and holds no copy of these strings.
//
// It returns a copy so a caller cannot reach in and edit the closed set.
func DeclaredCodes() []Code {
	out := make([]Code, len(declaredCodes))
	copy(out, declaredCodes)
	return out
}

// IsDeclared reports whether c is one of the codes Sharko owns.
//
// The empty Code is not declared, which is what makes the field effectively
// required: a Notification built without one cannot be stored.
func (c Code) IsDeclared() bool {
	for _, declared := range declaredCodes {
		if c == declared {
			return true
		}
	}
	return false
}

// String makes a Code printable in logs without a conversion at every call
// site. It is not a display string — never show a Code to a person.
func (c Code) String() string { return string(c) }
