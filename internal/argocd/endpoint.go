package argocd

// endpoint.go — LO-8 R7 (epic video-leftovers, 2026-10-18).
//
// A request path is Sharko's own words, with one exception: DeleteCluster and
// UpdateClusterLabels put a CLUSTER SERVER ADDRESS into the path, encoded as
// one path segment (escapeServerURL). That address is a value someone typed,
// and like any address it can carry a credential in its user part, its query
// or its fragment. Encoded, it is no longer a URL to the log sink — it has no
// "@", "?" or "#" left — so the sink's address rule cannot see it.
//
// So the path is made safe here, before it becomes a log field or an error
// field. The request itself still goes out with the full path: ArgoCD needs
// the exact address to find the cluster.

import (
	"net/url"
	"strings"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// hiddenAddressSegment stands in for a path segment that holds an address
// Sharko cannot vouch for any part of.
const hiddenAddressSegment = "(address-hidden)"

// unnamedClusterPhrase is what a sentence says instead of the cluster server
// address when there is no part of it Sharko can vouch for.
const unnamedClusterPhrase = "the cluster"

// safeEndpoint returns the request path as it may be shown in a log line or
// an error. Every path segment that, once decoded, has a place a credential
// can live ("@", "?" or "#") is replaced by the address with the user part,
// query and fragment taken off (credsafe.SafeRepoURL), encoded again. The
// query after the path is Sharko's own (updateMask, refresh, namespace and
// such) and is kept.
func safeEndpoint(path string) string {
	p, query, hasQuery := strings.Cut(path, "?")
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			segs[i] = hiddenAddressSegment
			continue
		}
		if !credsafe.ContainsCredentialCarrierRune(decoded) {
			continue
		}
		if safe := credsafe.SafeRepoURL(decoded); safe != "" {
			segs[i] = escapeServerURL(safe)
		} else {
			segs[i] = hiddenAddressSegment
		}
	}
	out := strings.Join(segs, "/")
	if hasQuery {
		out += "?" + query
	}
	return out
}

// safeClusterServer returns the part of a cluster server address that may be
// shown inside a sentence, never the user part, query or fragment.
func safeClusterServer(serverURL string) string {
	if safe := credsafe.SafeRepoURL(serverURL); safe != "" {
		return safe
	}
	return unnamedClusterPhrase
}
