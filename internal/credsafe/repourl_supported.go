package credsafe

// repourl_supported.go — what Sharko will accept as a chart or catalog
// repository address, and the one sentence it says when it will not.
//
// # Why there is a rule about this at all
//
// A repository address is routinely written with a token inside it. When that
// address is a CATALOG address, Sharko does not merely read it and log it — it
// writes it into a YAML file and commits that file to the operator's Git
// repository. Git is durable and replicated: once a token is in a commit it is
// also in every clone, every fork, every CI cache and every backup, and
// editing the file afterwards does not take it back.
//
// So a credential-bearing catalog address is not something to strip, mask or
// warn about. It is something to refuse BEFORE it is written, while the
// operator still has their hands on the keyboard and can use an address that
// carries no secret.
//
// # Why the address is never rewritten for the operator
//
// Quietly dropping the credential and saving the rest would save something the
// operator did not type, and the first they would hear of it is a catalog that
// no longer reaches its charts. Refusing says what is wrong, now.
//
// # Why the sentence does not claim a credential was found
//
// It cannot know that. The test underneath is ClassifyAddress, which is
// structural: it asks whether the address has somewhere for a credential to
// sit — user information in the authority, a query string, a fragment — never
// whether the text there looks secret, and it refuses outright an address it
// cannot read. A structural test is the only kind that does not fail on the
// first shape nobody predicted, and the price of it is that an ordinary
// "?ref=main" is refused too. So the sentence states the RULE, which is true,
// instead of an accusation, which would not be.
const UnsupportedRepoURLMessage = "Catalog repository URLs in the technical preview must be ones Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL."

// UnsupportedRepoURLError is the refusal, as a type.
//
// Callers decide what to do by asking errors.As for this type, never by
// reading the sentence — matching on message text is how a rename silently
// switches a safety check off.
//
// File and Field say WHERE the problem is, so an operator can go and fix it.
// Neither the address nor any part of it is carried: not the value, not a
// piece of it, not its length, not a mask of it. The whole point of refusing
// is that the value must not travel, and an error message travels further than
// the screen it was written for.
type UnsupportedRepoURLError struct {
	// File is the file the address was found in, e.g. "catalog.yaml".
	// Empty when the address came from a request rather than a file.
	File string
	// Field is the path to the field inside that file or request, e.g.
	// "addons.keda.repoURL". Empty when there is only one address in play.
	Field string
}

func (e *UnsupportedRepoURLError) Error() string {
	switch {
	case e.File != "" && e.Field != "":
		return e.File + ": " + e.Field + " — " + UnsupportedRepoURLMessage
	case e.Field != "":
		return e.Field + " — " + UnsupportedRepoURLMessage
	case e.File != "":
		return e.File + " — " + UnsupportedRepoURLMessage
	default:
		return UnsupportedRepoURLMessage
	}
}

// Is lets errors.Is(err, ErrRepoURLUnsupported) answer true for every
// file/field-tagged refusal, so a caller that only wants to know "was this
// refused for this reason" does not have to know about the type.
func (e *UnsupportedRepoURLError) Is(target error) bool { return target == ErrRepoURLUnsupported }

// ErrRepoURLUnsupported is the bare, untagged form of the refusal, and the
// sentinel every tagged one matches.
var ErrRepoURLUnsupported error = &UnsupportedRepoURLError{}

// ValidateSupportedRepoURL is the ONE rule about what may be saved as a
// catalog or chart repository address.
//
// Every door, every writer, every reader and the one place Sharko dials a
// chart repository calls this. There is no second copy: a rule written out
// twice is a rule that will disagree with itself, which is exactly how the
// query-parameter carrier slipped past the log sink before ClassifyAddress
// existed.
//
// An empty address is not this function's business — an entry that has not
// been filled in yet is incomplete, and other code says so in its own words.
func ValidateSupportedRepoURL(raw string) error {
	return ValidateSupportedRepoURLAt("", "", raw)
}

// ValidateSupportedRepoURLAt is ValidateSupportedRepoURL with the file and
// field the address came from, so the refusal can point at it.
func ValidateSupportedRepoURLAt(file, field, raw string) error {
	if raw == "" {
		return nil
	}
	// Only an explicit credential-free verdict opens the door. An address
	// Sharko could not read is refused here rather than committed to Git,
	// where a token would live on in every clone and every backup.
	switch ClassifyAddress(raw) {
	case AddressCredentialFree:
		return nil
	case AddressCarriesCredential, AddressUnclassifiable:
		return &UnsupportedRepoURLError{File: file, Field: field}
	default:
		// A verdict this build has never heard of is not a safe one.
		return &UnsupportedRepoURLError{File: file, Field: field}
	}
}
