package orchestrator

import "encoding/json"

// secret_failure.go — the catalog of sentences Sharko says when it could not
// put an addon's secret on a cluster, and the type that makes saying anything
// else hard to write.
//
// # What was wrong
//
// createAddonSecrets used to build the per-secret message like this:
//
//	Error: fmt.Sprintf("fetching key %q from %q: %v", key, providerPath, fetchErr)
//
// fetchErr comes from SecretValueFetcher.GetSecretValue — the secrets backend.
// The string went onto SecretError.Error, which carries `json:"error"`, which
// rides out on RegisterClusterResult.FailedSecrets (`json:"failed_secrets"`)
// and is printed by `sharko cluster register`. So an AWS Secrets Manager or
// Kubernetes API error's own words reached an API response and an operator's
// terminal, and providerPath — the location of a secret inside the backend —
// went with them.
//
// # What replaces it
//
// The same shape internal/verify uses for connectivity checks:
//
//   - a closed set of reason codes (SecretFailureCode);
//   - one complete, Sharko-written sentence per code, declared below;
//   - a message type with an unexported field, so no package can forge one;
//   - constructors that take a code and Sharko's own configuration, and have
//     no parameter an error or an arbitrary string could arrive through.
//
// # What an operator can still work out
//
// Deliberately not "something went wrong". Each failure still names:
//
//   - WHICH secret (SecretError.Name — Sharko's own catalog says the name),
//   - WHICH addon and, on a fetch failure, WHICH key (both come from the addon
//     catalog in Git — Sharko's own configuration, not backend output),
//   - WHICH STEP failed: reading the value out of the secrets store, or
//     writing the secret onto the cluster. Those are two different things to
//     go and look at, and that is the whole point of keeping the distinction.
//
// What is gone is the backend's own words and providerPath. The path stays
// server-side: the log line above the failure already carries addon, key and
// path, and it is found by the request id.

// SecretFailureCode is the closed set of reasons an addon secret did not end
// up on a cluster. It is a code, not prose: the UI and the CLI can branch on
// it, and it never varies with what a backend said.
type SecretFailureCode string

const (
	// SecretFailureFetch — Sharko could not read the value out of the
	// configured secrets store, so it never had anything to write.
	SecretFailureFetch SecretFailureCode = "SECRET_FETCH_FAILED"

	// SecretFailureWrite — Sharko had the value and the write to the cluster
	// failed. This also covers the ownership gate refusing to overwrite a
	// Secret that Sharko did not create.
	SecretFailureWrite SecretFailureCode = "SECRET_WRITE_FAILED"
)

// secretFailureSentences is the catalog: one complete sentence per code.
// Every character an operator reads about a failed addon secret is declared
// right here.
//
// TestSecretFailureSentences_EveryDeclaredCodeHasOne parses the const block
// above and fails BY NAME on any code missing an entry — a LIST, not a count,
// so a new code cannot ship with a blank message.
var secretFailureSentences = map[SecretFailureCode]string{
	SecretFailureFetch: "Sharko could not read this addon's secret value from the configured secrets store, so nothing was written to the cluster. Check that the secrets store is reachable and that the path this addon's catalog entry points at still exists.",
	SecretFailureWrite: "Sharko read this addon's secret value but could not write the secret onto the cluster. Check that Sharko can reach the cluster and is allowed to write secrets in this namespace, and that a secret of this name that Sharko did not create is not already sitting there.",
}

// SafeSecretMessage is a finished sentence from the catalog above, and the
// only thing this package puts in front of a person about a failed secret.
//
// # Why it is a type and not a string
//
// SecretError.Error used to be a plain string, and what went into it was a
// backend error formatted with %v. Nothing in the type said that was wrong.
//
// SafeSecretMessage carries an unexported field, so no package outside this
// one can build one — not by literal, not by conversion. The only ways to get
// one are the constructors below, and they take a SecretFailureCode: a closed
// set of two constants with no room for raw text to ride in on. Putting a
// backend's words in front of an operator now takes code that visibly does
// that, instead of happening by accident.
//
// It marshals as a plain JSON string, so the wire shape of `failed_secrets`
// is unchanged for the UI and the CLI. There is deliberately no
// UnmarshalJSON: nothing in this repo decodes into SecretError, and adding
// one would hand back exactly the "any string becomes a message" door the
// unexported field just closed.
type SafeSecretMessage struct{ sentence string }

// String renders the sentence.
func (m SafeSecretMessage) String() string { return m.sentence }

// MarshalJSON emits the sentence as a plain JSON string.
func (m SafeSecretMessage) MarshalJSON() ([]byte, error) { return json.Marshal(m.sentence) }

// secretFailureMessage returns the catalog sentence for a code.
//
// It takes the CODE — not the error, not a string. An unrecognised code falls
// back to the fetch sentence rather than to an empty string, so no path can
// produce a blank message.
func secretFailureMessage(code SecretFailureCode) SafeSecretMessage {
	if sentence, ok := secretFailureSentences[code]; ok {
		return SafeSecretMessage{sentence: sentence}
	}
	return SafeSecretMessage{sentence: secretFailureSentences[SecretFailureFetch]}
}

// newSecretFetchFailure records "the value could not be read out of the
// secrets store" for one addon key.
//
// def and key are Sharko's own configuration — they come from the addon
// catalog in Git, not from a backend response — which is why they are safe to
// name. The provider path is NOT among them: it is the location of a secret
// inside the backend, it is only useful to somebody who can already reach the
// backend, and the server log line already carries it.
//
// There is no error parameter. That is the point.
func newSecretFetchFailure(def AddonSecretDefinition, key string) SecretError {
	return SecretError{
		Name:  def.SecretName,
		Addon: def.AddonName,
		Key:   key,
		Code:  SecretFailureFetch,
		Error: secretFailureMessage(SecretFailureFetch),
	}
}

// newSecretWriteFailure records "the value was read but the cluster write
// failed" for one addon secret. No error parameter, same reason as above.
func newSecretWriteFailure(def AddonSecretDefinition) SecretError {
	return SecretError{
		Name:  def.SecretName,
		Addon: def.AddonName,
		Code:  SecretFailureWrite,
		Error: secretFailureMessage(SecretFailureWrite),
	}
}
