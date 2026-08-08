package providers

import (
	"errors"
	"fmt"
)

// Boundary refusals for addon-secret reads (task #152 story B).
//
// A secret-value fetch (GetSecretValue) must stay inside the area the
// operator configured for the provider connection: the secret-name prefix
// for AWS Secrets Manager, the namespace for the Kubernetes backend. The
// check lives HERE, in the provider, before any backend call — so every
// trigger that fetches a value (the scheduled reconciler, the "refresh
// now" API path, the cluster doctor) inherits the same boundary without
// having to remember to ask. Do not add a second copy of this check in a
// handler or a caller.
//
// The refusal sentences below are canned on purpose. They are the exact
// text an operator sees at every door — the UI, the API and the CLI all
// render the provider's error string verbatim — so the wording is pinned
// by TestBoundaryRefusal_CannedSentencesAreTheSameAtEveryDoor. They name
// the refused path and the configured boundary (both are Git/config
// metadata, never secret material) and they never carry a raw AWS or
// Kubernetes SDK error, because the refusal happens before the SDK is
// ever called.

// ErrSecretPathRefused marks a boundary refusal: the requested backend
// path is outside what this provider connection is allowed to read.
// Callers can detect the refusal with errors.Is.
var ErrSecretPathRefused = errors.New("secret path refused")

// awsNoPrefixRefusal is the refusal for an AWS Secrets Manager connection
// with no prefix configured. An empty prefix must never mean "the whole
// AWS account", so it is treated as a configuration error and every read
// is refused until the operator sets a prefix.
func awsNoPrefixRefusal(path string) error {
	return fmt.Errorf("%w: %q was not read because this AWS Secrets Manager connection has no secret prefix configured. An empty prefix would mean the whole AWS account, so Sharko treats it as a configuration error — set a prefix on the provider connection to say which secrets Sharko may read", ErrSecretPathRefused, path)
}

// awsOutsidePrefixRefusal is the refusal for a path that does not sit
// under the configured AWS Secrets Manager prefix.
func awsOutsidePrefixRefusal(path, prefix string) error {
	return fmt.Errorf("%w: %q is outside the prefix %q this AWS Secrets Manager connection is allowed to read. Sharko only reads addon secrets under the configured prefix", ErrSecretPathRefused, path, prefix)
}

// k8sNoNamespaceRefusal is the refusal for a Kubernetes secrets
// connection with no namespace configured. The public constructors always
// default the namespace, so in a normal build this cannot fire — it is a
// fail-closed guard against a future construction path that skips the
// default.
func k8sNoNamespaceRefusal(path string) error {
	return fmt.Errorf("%w: %q was not read because this Kubernetes secrets connection has no namespace configured, so Sharko cannot tell which namespace it is allowed to read", ErrSecretPathRefused, path)
}

// k8sOutsideNamespaceRefusal is the refusal for an explicit-namespace
// path that points outside the configured namespace.
func k8sOutsideNamespaceRefusal(path, requested, allowed string) error {
	return fmt.Errorf("%w: %q points at namespace %q, but this Kubernetes secrets connection is only allowed to read secrets in namespace %q", ErrSecretPathRefused, path, requested, allowed)
}
