package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// The sentences for "this cluster's secret is not in your secrets store"
// (ep4 round S, S3). They name the secret names Sharko tried, so a person can
// see a typo at once. The names come from the cluster's own settings (its
// name, the store's name prefix, or its secret path) — never a value, and
// never anything AWS wrote.
//
// Before, with a name prefix and the least-privilege policy from the docs, a
// missing secret came out as "AWS turned down Sharko's login": the plain-name
// try is not allowed by that policy, and that refusal was taken as the
// cause. The login was fine; the secret was just missing.
//
// Pinned by exact text in connection_secret_not_found_test.go.
const (
	// secretNotFoundOneFmt: one name tried, and it is not there.
	secretNotFoundOneFmt = "Sharko could not find this cluster's secret in your secrets store. " +
		"It looked for %s, and it is not there. " +
		"Check the secret's name, or set this cluster's secret path to the exact name."
	// secretNotFoundManyFmt: more than one name tried, none of them there.
	secretNotFoundManyFmt = "Sharko could not find this cluster's secret in your secrets store. " +
		"It looked for %s, and none of them is there. " +
		"Check the secret's name, or set this cluster's secret path to the exact name."
	// secretNotFoundNotAllowedFmt: one name was not there, and Sharko was not
	// allowed to read the other one.
	secretNotFoundNotAllowedFmt = "Sharko could not find this cluster's secret in your secrets store. " +
		"It looked for %s, and it is not there. " +
		"Sharko is not allowed to read %s, so it could not look there. " +
		"Check the secret's name, or set this cluster's secret path to the exact name."
)

// secretNotFoundSentence is the finished sentence for err when err says the
// cluster's secret is not there (providers.SecretNotFoundNames, which reads
// the error by type only). ok is false for every other error.
func secretNotFoundSentence(err error) (string, bool) {
	tried, notAllowed, ok := providers.SecretNotFoundNames(err)
	if !ok {
		return "", false
	}
	switch {
	case len(notAllowed) > 0:
		return fmt.Sprintf(secretNotFoundNotAllowedFmt, quotedNames(tried), quotedNames(notAllowed)), true
	case len(tried) == 1:
		return fmt.Sprintf(secretNotFoundOneFmt, quotedNames(tried)), true
	default:
		return fmt.Sprintf(secretNotFoundManyFmt, quotedNames(tried)), true
	}
}

// quotedNames writes names as "a", "a" and "b", or "a", "b" and "c".
func quotedNames(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	if len(q) <= 1 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}
