package argocd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
)

// Capability is the answer to "is this token allowed to do this in ArgoCD".
//
// It has three values on purpose. ArgoCD can say yes, it can say no, and it
// can fail to answer at all — an older server without the endpoint, a network
// blip, a body that does not parse. Folding "could not ask" into either "yes"
// or "no" is what produces the two failures this type exists to prevent: a
// silent no-op when the answer was really yes, and a refusal to do something
// the operator is perfectly entitled to do.
type Capability int

const (
	// CapabilityUnknown means ArgoCD did not give a usable answer. Callers
	// must go ahead and attempt the real call: the call itself is the
	// authority, and a 403 from it is still classified as ErrPermissionDenied.
	CapabilityUnknown Capability = iota
	// CapabilityAllowed means ArgoCD said yes.
	CapabilityAllowed
	// CapabilityDenied means ArgoCD said no. Callers must stop and say so.
	CapabilityDenied
)

func (c Capability) String() string {
	switch c {
	case CapabilityAllowed:
		return "allowed"
	case CapabilityDenied:
		return "denied"
	default:
		return "unknown"
	}
}

// canIResponse is ArgoCD's answer shape: {"value":"yes"} or {"value":"no"}.
type canIResponse struct {
	Value string `json:"value"`
}

// CanSyncApplication asks ArgoCD whether this token may sync the named
// application, using ArgoCD's own /api/v1/account/can-i endpoint.
//
// This is asked BEFORE the sync rather than inferred from a failure, so a
// Sharko action that needs a permission the operator never granted can say so
// up front instead of firing a request that gets refused. The permission being
// asked about is the one an operator grants deliberately in argocd-rbac-cm;
// installing Sharko does not grant it and Sharko never grants it to itself.
//
// The RBAC object for an application in ArgoCD is "<project>/<name>", which is
// why the project is a parameter: a policy written for Sharko's own AppProject
// is a different answer from a policy written for somebody else's.
func (c *Client) CanSyncApplication(ctx context.Context, project, appName string) Capability {
	if project == "" || appName == "" {
		// Nothing to ask about. Let the real call decide rather than
		// inventing an answer from a half-built question.
		return CapabilityUnknown
	}
	path := "/api/v1/account/can-i/applications/sync/" +
		url.PathEscape(project) + "/" + url.PathEscape(appName)

	body, err := c.doGet(ctx, path)
	if err != nil {
		// The error itself is deliberately not passed to the logger. doGet
		// has already written the endpoint and the status code, which is what
		// triage needs, and an ArgoCD error body is the one thing that must
		// not reach a log line as a plain string.
		slog.Warn("argocd can-i check did not answer; the sync call itself will decide",
			"app", appName, "project", project)
		return CapabilityUnknown
	}

	var answer canIResponse
	if jsonErr := json.Unmarshal(body, &answer); jsonErr != nil {
		slog.Warn("argocd can-i answer did not parse; the sync call itself will decide",
			"app", appName, "project", project)
		return CapabilityUnknown
	}
	switch strings.ToLower(strings.TrimSpace(answer.Value)) {
	case "yes":
		return CapabilityAllowed
	case "no":
		return CapabilityDenied
	default:
		return CapabilityUnknown
	}
}
