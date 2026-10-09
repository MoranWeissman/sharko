package api

import (
	"errors"

	"github.com/MoranWeissman/sharko/internal/argocd"
)

// argocd_list_failure.go — the catalog of sentences Sharko says when a read
// against the host ArgoCD API fails, and the one classifier that picks between
// them.
//
// # What was wrong
//
// Five places said this:
//
//	writeError(w, http.StatusBadGateway, "failed to list ArgoCD clusters: "+err.Error())
//
// err comes from internal/argocd's HTTP client. On a transport failure that is
// a *url.Error, and a *url.Error's text embeds the FULL ArgoCD base URL — an
// internal address an unauthenticated caller has no business learning from an
// error message. On a non-2xx it is Sharko's own sentinel, which is safe, but
// nothing at the call site could tell the two apart.
//
// The five: clusters_discover.go, clusters_write.go twice, clusters_doctor.go
// twice. clusters_discover.go is the one that shows it was an oversight — the
// same block already classified the SAME error by sentinel to choose a canned
// Kubernetes event sentence, with a comment saying the event "carries no token
// or URL", and then put the raw text in the response body on the next line.
//
// # What replaces it
//
// Classification BY TYPE — errors.Is against the sentinels internal/argocd
// declares — picking one complete sentence from the closed set below. No
// substring matching on error words, and no parameter for raw text: the
// function takes an error and returns a finished sentence, and the only
// strings it can ever return are written in this file.
//
// # What an operator can still work out
//
// Deliberately not one flat sentence. The four outcomes are four different
// things to go and look at: a permission problem on the ArgoCD account, a
// token that needs replacing, a certificate to trust, or an ArgoCD that is not
// answering. Each sentence names its own next step. The raw text goes to the
// server log, found by the request id.

// argoReadSubject names the thing Sharko was trying to read, so one classifier
// can serve every ArgoCD read without the sentences going vague. These are
// Sharko's own words about Sharko's own call — never anything a server said.
type argoReadSubject string

const (
	argoReadClusters     argoReadSubject = "clusters"
	argoReadApplications argoReadSubject = "applications"
)

// argoListFailureSentence returns the catalog sentence for a failed ArgoCD
// read.
//
// Classification is by TYPE only. A substring search on the error's words
// would silently stop matching the day the client rephrased itself, which is
// how this bug class comes back.
func argoListFailureSentence(subject argoReadSubject, err error) string {
	noun := string(subject)
	switch {
	case errors.Is(err, argocd.ErrPermissionDenied):
		return "Sharko's ArgoCD account is not allowed to list " + noun + ". Grant that account permission to list " + noun + " in ArgoCD and try again."
	case errors.Is(err, argocd.ErrTokenInvalid):
		return "ArgoCD refused Sharko's token while listing " + noun + ". The token may be expired or wrong — replace it on the ArgoCD connection in Settings and try again."
	case errors.Is(err, argocd.ErrTLSCertificateNotTrusted):
		return "Sharko reached ArgoCD but could not verify its certificate, so it did not list " + noun + ". Add the certificate authority to the ArgoCD connection in Settings and try again."
	default:
		return "Sharko could not reach the ArgoCD API to list " + noun + ". Check that the ArgoCD server address on the connection is correct and that ArgoCD is reachable from Sharko, then try again."
	}
}
