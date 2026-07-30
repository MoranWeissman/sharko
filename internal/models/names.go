package models

import (
	"regexp"
	"strings"
)

// ResourceNamePattern is the ONE regular expression Sharko accepts for a
// user-supplied cluster name or addon name — the two identifiers that get
// pasted straight into a git file path (clusters/<name>.yaml,
// values/clusters/<cluster>/<addon>.yaml) and into a Kubernetes label key
// (addons.sharko.dev/<addon>).
//
// Kept as an exported string constant, not just a compiled regexp, because
// three different consumers need the same literal and must never drift:
//
//   - internal/api's request-edge validation (validClusterNameRe),
//   - internal/orchestrator's path builders (belt-and-braces, v4paths.go),
//   - the generated JSON Schema's propertyNames constraint on the
//     AddonCatalogDelta addons map (internal/config/addon_catalog_delta.go's
//     JSONSchemaExtend), which is what protects a hand-authored
//     catalog/addons.yaml.
//
// Alphanumeric with hyphens, starting with an alphanumeric. Deliberately
// narrower than a DNS label so "." and "_" — both legal in a K8s label-key
// suffix — cannot appear either; every addon Sharko ships is already
// spelled this way, and a name that can contain neither "/" nor "\" nor
// "." cannot express a path traversal at all.
const ResourceNamePattern = `^[a-zA-Z0-9][a-zA-Z0-9-]*$`

var resourceNameRe = regexp.MustCompile(ResourceNamePattern)

// IsValidResourceName reports whether name is an acceptable cluster or
// addon name (see ResourceNamePattern). An empty name is never valid.
func IsValidResourceName(name string) bool {
	return resourceNameRe.MatchString(name)
}

// InvalidResourceNameMessage is the plain-English 400 body every caller
// returns for a rejected name, so the wording is identical no matter which
// endpoint the caller hit.
const InvalidResourceNameMessage = "must be letters, digits and hyphens only, starting with a letter or digit"

// LooksLikePathSegmentEscape reports whether name contains anything that
// could make it escape the directory it is about to be joined into: a
// forward slash, a backslash, or "..". This is the second, structural half
// of the defence — IsValidResourceName already excludes all three, so this
// only ever fires if a caller reached a path builder without going through
// the request-edge check.
func LooksLikePathSegmentEscape(name string) bool {
	return strings.ContainsAny(name, `/\`) || strings.Contains(name, "..")
}
