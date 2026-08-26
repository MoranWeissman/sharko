package models

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// GitProviderType identifies which Git provider to use.
type GitProviderType string

const (
	GitProviderGitHub      GitProviderType = "github"
	GitProviderAzureDevOps GitProviderType = "azuredevops"
	GitProviderGitea       GitProviderType = "gitea"
)

// GitRepoConfig holds Git repository configuration.
type GitRepoConfig struct {
	Provider GitProviderType `json:"provider" yaml:"provider"`

	// URL-based input (parsed into owner/repo or org/project/repo)
	RepoURL string `json:"repo_url,omitempty" yaml:"repo_url,omitempty"`

	// GitHub fields
	Owner string `json:"owner,omitempty" yaml:"owner,omitempty"`
	Repo  string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Token string `json:"token,omitempty" yaml:"token,omitempty"`

	// Azure DevOps fields
	Organization string `json:"organization,omitempty" yaml:"organization,omitempty"`
	Project      string `json:"project,omitempty" yaml:"project,omitempty"`
	Repository   string `json:"repository,omitempty" yaml:"repository,omitempty"`
	PAT          string `json:"pat,omitempty" yaml:"pat,omitempty"`
}

// ParseRepoURL populates provider, owner/repo (or org/project/repo) from a Git URL.
// Supports:
//   - https://github.com/owner/repo
//   - https://github.example.com/owner/repo (GitHub Enterprise)
//   - https://dev.azure.com/org/project/_git/repo
//   - https://org.visualstudio.com/project/_git/repo
//
// Explicit-fields override. When the URL path cannot be parsed into
// the canonical owner/repo (or org/project/repo) shape but the caller
// has already populated the explicit identifier fields, the explicit
// fields win and the URL is treated as opaque (Provider is still set
// from host detection). Rejection only fires when BOTH path-parse fails
// AND the explicit identifier fields are empty. This unblocks
// self-hosted Gitea, corporate Git proxies, in-cluster gitfake URLs,
// and any other deployment whose URL shape doesn't match a public-SaaS
// layout.
func (g *GitRepoConfig) ParseRepoURL() error {
	if g.RepoURL == "" {
		return nil // nothing to parse, fields must be set directly
	}

	u, err := url.Parse(strings.TrimSuffix(g.RepoURL, ".git"))
	if err != nil {
		return fmt.Errorf("invalid git URL: %w", err)
	}

	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	// Azure DevOps: dev.azure.com/org/project/_git/repo
	if host == "dev.azure.com" {
		if len(parts) < 4 || parts[2] != "_git" {
			// Accept when the caller already populated the explicit
			// Azure DevOps identifier fields. Path parsing is best-
			// effort in that case — the explicit fields are the
			// source of truth.
			if g.Organization != "" && g.Project != "" && g.Repository != "" {
				g.Provider = GitProviderAzureDevOps
				if g.Token != "" && g.PAT == "" {
					g.PAT = g.Token
				}
				return nil
			}
			return fmt.Errorf("Azure DevOps URL must be https://dev.azure.com/org/project/_git/repo")
		}
		g.Provider = GitProviderAzureDevOps
		g.Organization = parts[0]
		g.Project = parts[1]
		g.Repository = parts[3]
		if g.Token != "" && g.PAT == "" {
			g.PAT = g.Token
		}
		return nil
	}

	// Azure DevOps: org.visualstudio.com/project/_git/repo
	if strings.HasSuffix(host, ".visualstudio.com") {
		g.Provider = GitProviderAzureDevOps
		g.Organization = strings.TrimSuffix(host, ".visualstudio.com")
		if len(parts) >= 3 && parts[1] == "_git" {
			g.Project = parts[0]
			g.Repository = parts[2]
		} else {
			// Accept when the caller already populated the explicit
			// Azure DevOps Project + Repository fields. Host gave us
			// Organization for free.
			if g.Project != "" && g.Repository != "" {
				if g.Token != "" && g.PAT == "" {
					g.PAT = g.Token
				}
				return nil
			}
			return fmt.Errorf("Azure DevOps URL must be https://org.visualstudio.com/project/_git/repo")
		}
		if g.Token != "" && g.PAT == "" {
			g.PAT = g.Token
		}
		return nil
	}

	// GitHub (github.com or any other host = GitHub Enterprise) OR Gitea.
	// Gitea URLs have the same owner/repo shape as GitHub, but the provider
	// must be set explicitly by the caller (a bare self-hosted URL cannot
	// self-identify Gitea vs GitHub-Enterprise).
	// Preserve an explicit Gitea provider; otherwise default to GitHub.
	if g.Provider != GitProviderGitea {
		g.Provider = GitProviderGitHub
	}
	if len(parts) < 2 {
		// Explicit-fields override. The URL path lacks the owner/repo
		// segments the path-parser needs, but if the caller already
		// populated Owner + Repo directly, treat the URL as opaque and
		// use the explicit fields as-is.
		if g.Owner != "" && g.Repo != "" {
			return nil
		}
		return fmt.Errorf("Git URL must contain owner/repo (got: %s)", u.Path)
	}
	g.Owner = parts[0]
	g.Repo = strings.Join(parts[1:], "/") // handle nested paths
	return nil
}

// ArgocdConfig holds ArgoCD connection configuration.
type ArgocdConfig struct {
	ServerURL string `json:"server_url" yaml:"server_url"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Insecure  bool   `json:"insecure,omitempty" yaml:"insecure,omitempty"`
}

// ProviderConfig holds credentials-provider configuration stored in a connection.
// The provider fetches cluster kubeconfigs from an external secret store.
type ProviderConfig struct {
	Type      string `json:"type" yaml:"type"`                               // "aws-sm" or "k8s-secrets"
	Region    string `json:"region,omitempty" yaml:"region,omitempty"`       // AWS region (aws-sm only)
	Prefix    string `json:"prefix,omitempty" yaml:"prefix,omitempty"`       // Secret name prefix, e.g. "clusters/"
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // K8s namespace (k8s-secrets only)
	RoleARN   string `json:"role_arn,omitempty" yaml:"role_arn,omitempty"`   // default IAM role to assume for EKS token generation
}

// GitOpsSettings holds product-level GitOps preferences stored in a connection.
// These override env vars (which remain as a fallback for migration).
type GitOpsSettings struct {
	BaseBranch      string `json:"base_branch,omitempty" yaml:"base_branch,omitempty"`             // default: "main"
	BranchPrefix    string `json:"branch_prefix,omitempty" yaml:"branch_prefix,omitempty"`         // default: "sharko/"
	CommitPrefix    string `json:"commit_prefix,omitempty" yaml:"commit_prefix,omitempty"`         // default: "sharko:"
	PRAutoMerge     *bool  `json:"pr_auto_merge,omitempty" yaml:"pr_auto_merge,omitempty"`         // default: false
	HostClusterName string `json:"host_cluster_name,omitempty" yaml:"host_cluster_name,omitempty"` // cluster running ArgoCD (in-cluster)
	DefaultAddons   string `json:"default_addons,omitempty" yaml:"default_addons,omitempty"`       // comma-separated addon names
}

// Connection combines Git repo, ArgoCD, provider, and GitOps settings.
type Connection struct {
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	Git         GitRepoConfig   `json:"git" yaml:"git"`
	Argocd      ArgocdConfig    `json:"argocd" yaml:"argocd"`
	Provider    *ProviderConfig `json:"provider,omitempty" yaml:"provider,omitempty"`
	// AddonSecretProvider is the optional SEPARATE provider for addon-secret
	// material (V3-P1.1). When set, addon secrets are fetched from this
	// backend; the Provider field above is used ONLY for cluster-connectivity
	// tests. When nil, addon secrets fall back to Provider for backward
	// compatibility (pre-V3 connections).
	AddonSecretProvider *ProviderConfig `json:"addon_secret_provider,omitempty" yaml:"addon_secret_provider,omitempty"`
	GitOps              *GitOpsSettings `json:"gitops,omitempty" yaml:"gitops,omitempty"`
	IsDefault           bool            `json:"is_default" yaml:"default,omitempty"`
	CreatedAt           string          `json:"created_at,omitempty" yaml:"-"`
	UpdatedAt           string          `json:"updated_at,omitempty" yaml:"-"`
}

// ConnectionResponse is a connection with masked sensitive data for API responses.
type ConnectionResponse struct {
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	GitProvider       GitProviderType `json:"git_provider"`
	GitRepoIdentifier string          `json:"git_repo_identifier"`
	GitTokenMasked    string          `json:"git_token_masked"`
	ArgocdServerURL   string          `json:"argocd_server_url"`
	ArgocdTokenMasked string          `json:"argocd_token_masked"`
	ArgocdNamespace   string          `json:"argocd_namespace"`
	// ArgocdInsecure surfaces the connection's TLS-verification opt-out so the
	// Settings editor can prefill its checkbox instead of guessing (a wrong
	// guess would silently flip the stored value on the next save). It is a
	// setting, not a credential — safe to expose.
	ArgocdInsecure      bool            `json:"argocd_insecure"`
	Provider            *ProviderConfig `json:"provider,omitempty"`
	AddonSecretProvider *ProviderConfig `json:"addon_secret_provider,omitempty"`
	GitOps              *GitOpsSettings `json:"gitops,omitempty"`
	IsDefault           bool            `json:"is_default"`
	IsActive            bool            `json:"is_active"`
	CreatedAt           string          `json:"created_at,omitempty"`
	UpdatedAt           string          `json:"updated_at,omitempty"`
}

// ConnectionsListResponse is the API response for listing connections.
type ConnectionsListResponse struct {
	Connections      []ConnectionResponse `json:"connections"`
	ActiveConnection string               `json:"active_connection,omitempty"`
}

// CreateConnectionRequest is the API request to create a new connection.
//
// The `UseSaved` field is honored ONLY by the test-credentials endpoint
// (POST /connections/test-credentials). The Create / Update handlers ignore
// it — they always read credentials from the request body / saved
// store as usual. When `use_saved=true` is sent to test-credentials,
// the handler fetches the named connection's credentials server-side
// and tests with those, so the wizard can honor the "leave blank to
// keep, or enter new value to replace" placeholder end-to-end.
type CreateConnectionRequest struct {
	Name                string          `json:"name"`
	Description         string          `json:"description,omitempty"`
	Git                 GitRepoConfig   `json:"git"`
	Argocd              ArgocdConfig    `json:"argocd"`
	Provider            *ProviderConfig `json:"provider,omitempty"`
	AddonSecretProvider *ProviderConfig `json:"addon_secret_provider,omitempty"`
	GitOps              *GitOpsSettings `json:"gitops,omitempty"`
	SetAsDefault        bool            `json:"set_as_default"`
	// UseSaved, when true on a test-credentials request, instructs the
	// handler to look up the saved connection by Name and test using its
	// stored credentials instead of the request body. Returns 400 if no
	// saved connection with the given name exists. Ignored by Create /
	// Update.
	UseSaved bool `json:"use_saved,omitempty"`
}

// SetActiveConnectionRequest is the API request to set the active connection.
type SetActiveConnectionRequest struct {
	ConnectionName string `json:"connection_name"`
}

// FixedTokenMask is what a saved token looks like on the way out. It is a
// constant: the same eight characters for every token there has ever been.
const FixedTokenMask = "********"

// MaskToken reports whether a token is saved, and nothing else.
//
// # Why it gives back none of the token
//
// It used to show the first four and last four characters, with the middle
// starred out one star per character. Both halves of that were a leak:
//
//   - eight real characters of a secret are eight real characters of a
//     secret, and the first four of a token are usually its issuer prefix, so
//     what was left was the four that identify the account;
//   - the number of stars was the token's exact length, which narrows what a
//     guess has to cover and says which kind of token this is.
//
// Neither is worth anything to the person reading the screen. The only thing
// they need from this field is "yes, a token is saved here" — and that is
// exactly what a fixed mask says, on a response that every signed-in viewer
// can read.
//
// Empty in, empty out: "no token saved" is a different fact from "a token is
// saved", and the screen has to be able to tell them apart.
func MaskToken(token string) string {
	if token == "" {
		return ""
	}
	return FixedTokenMask
}

// SafeArgocdServerURL is the ArgoCD server address as a person may see it: the
// scheme, host, port and path, with any credential written into it removed.
//
// # Why an address needs this at all
//
// Operators write credentials into the ArgoCD address, and until BF8 nothing
// stopped them:
//
//	https://<token>@argocd.example            the token is the username
//	https://user:<token>@argocd.example       the token is the password
//	https://argocd.example?access_token=...   the token is a query parameter
//	https://argocd.example#<token>            the token is a fragment
//
// This field is on the connection list, and listing connections is something
// every signed-in viewer may do. So on a perfectly successful response, with
// nothing broken and no error anywhere, the credential came back to anyone
// with an account.
//
// # Why the address is not simply removed
//
// Because the host is not the secret and it is genuinely useful: an operator
// looking at Settings needs to see which ArgoCD this connection points at.
// credsafe.SafeRepoURL already draws that line for Git repository addresses —
// it drops the whole userinfo section, both halves, plus the query and the
// fragment, and keeps the rest. The same line is the right one here, and
// having one function draw it is what stops two copies of the rule from
// drifting apart.
//
// When the address cannot be taken apart with confidence, SafeRepoURL gives
// back "" and this field is blank. Blank means "Sharko will not vouch for any
// part of this", never "there is no address".
func SafeArgocdServerURL(raw string) string {
	return credsafe.SafeRepoURL(raw)
}

// ErrArgocdServerURLCarriesCredential is the refusal an operator gets when the
// ArgoCD server address they are saving has a credential written into it.
//
// # Why saving is refused rather than quietly cleaned up
//
// Silently stripping it would mean the operator's saved connection did not
// match what they typed, and the first they would hear of it is a connection
// that no longer works. Refusing says what is wrong while their hands are
// still on the keyboard.
//
// # Why the sentence never quotes the address back
//
// Because the address is the thing being refused for carrying a secret, and an
// error message is read in more places than the screen it was written for.
var ErrArgocdServerURLCarriesCredential = errors.New(
	`The ArgoCD server address must be the address only: a host, an optional port, and an optional path. Take out any user information, any query string and any fragment — Sharko signs in with the ArgoCD token you set separately, never with sign-in details written into the address. An address Sharko cannot read is refused too.`)

// ValidateArgocdServerURL is the ONE rule about what may be saved as an ArgoCD
// server address. Every writer calls this — the API create and update
// handlers by way of the connection request validator, and the Git-declared
// environment merge — so there is one rule and one sentence, not one per door.
//
// It asks credsafe.ClassifyAddress, which is the same structural reading
// SafeRepoURL uses: is there user information, a query or a fragment in an
// address Sharko can read all the way through. Structural, never a scan for
// text that looks secret — a scanner fails on the first shape nobody
// predicted. An address that cannot be read is refused rather than assumed
// harmless, which is the whole of BF12.
//
// An empty address is not this function's business; the connection is simply
// incomplete, and other code says so.
func ValidateArgocdServerURL(raw string) error {
	if raw == "" {
		return nil
	}
	// Saving is allowed only on the one explicitly safe verdict.
	switch credsafe.ClassifyAddress(raw) {
	case credsafe.AddressCredentialFree:
		return nil
	case credsafe.AddressCarriesCredential, credsafe.AddressUnclassifiable:
		return ErrArgocdServerURLCarriesCredential
	default:
		// A verdict this build has never heard of is not a safe one.
		return ErrArgocdServerURLCarriesCredential
	}
}

// ToResponse converts a Connection to a ConnectionResponse with masked tokens.
func (c *Connection) ToResponse(isActive bool) ConnectionResponse {
	repoID := ""
	token := ""
	switch c.Git.Provider {
	case GitProviderGitHub:
		repoID = c.Git.Owner + "/" + c.Git.Repo
		token = c.Git.Token
	case GitProviderAzureDevOps:
		repoID = c.Git.Organization + "/" + c.Git.Project + "/" + c.Git.Repository
		token = c.Git.PAT
	case GitProviderGitea:
		repoID = c.Git.Owner + "/" + c.Git.Repo
		token = c.Git.Token
	}

	return ConnectionResponse{
		Name:                c.Name,
		Description:         c.Description,
		GitProvider:         c.Git.Provider,
		GitRepoIdentifier:   repoID,
		GitTokenMasked:      MaskToken(token),
		ArgocdServerURL:     SafeArgocdServerURL(c.Argocd.ServerURL),
		ArgocdTokenMasked:   MaskToken(c.Argocd.Token),
		ArgocdNamespace:     c.Argocd.Namespace,
		ArgocdInsecure:      c.Argocd.Insecure,
		Provider:            c.Provider,
		AddonSecretProvider: c.AddonSecretProvider,
		GitOps:              c.GitOps,
		IsDefault:           c.IsDefault,
		IsActive:            isActive,
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
	}
}
