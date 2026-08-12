package clusterreconciler

// repair_credentials.go — the ONE route a WRITE takes to a cluster's
// credentials, shared by the periodic reconcile pass and by an asked-for repair.
//
// # Why a repair and a check read differently, and why that is right
//
// There are two questions, and they are not the same question:
//
//   - "What should this connection look like, so I can compare it?" A read-only
//     check asks that, and it must create nothing while answering. For a stored
//     EKS payload the backend holds metadata and not a credential, so the honest
//     answer is "there is no credential on the expected side" — and the check
//     reports the credential fields as not checked rather than minting one to
//     compare against and throwing it away. That route is
//     ClusterCredsRouter.StoredFactsIndependentOfArgoCDSecret, it deliberately
//     cannot reach the mint, and nothing here widens it.
//
//   - "What do I need to WRITE?" A repair asks that, and a write needs
//     credentials that actually work. For a stored EKS payload that means
//     minting a sign-in token, exactly as the normal write does — because the
//     thing being written has to let ArgoCD sign in afterwards.
//
// So the two paths need DIFFERENT reads. Sharing one read would either make the
// check mint (a read creating a real credential as a side effect) or make the
// repair write a spec with no credential in it.
//
// # What went wrong when the repair used the check's read
//
// The repair built its spec from the no-mint stored-facts read, which returns no
// token for an EKS payload. argosecrets.buildSecretConfig picks the connection
// shape by precedence — cert pair, then token, then exec — so with no token and
// no cert pair it fell through to the execProviderConfig (argocd-k8s-auth)
// shape, while every normal Sharko write for that same cluster produced the
// bearerToken shape. Clicking repair silently changed HOW ArgoCD signs in to that
// cluster. If argocd-k8s-auth is not usable from that ArgoCD's environment, the
// repair broke the connection it was asked to fix — and the fresh comparison
// afterwards would compare against the exec shape and call it correct.
//
// # The fix is one function, not two that agree
//
// ConnectionCredentialSpecForWrite below is the only place a write's credential
// half is assembled, and BOTH writers call it: the reconcile pass's createOne and
// the repair endpoint. Two functions that produce the same shape today drift
// apart the next time one of them is edited; one function cannot.

import (
	"encoding/base64"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ConnectionCredentialSpecForWrite fetches the named cluster's credentials the
// normal way and returns the CREDENTIAL HALF of the connection spec a write
// needs.
//
// It is for WRITES ONLY — the periodic pass and an asked-for repair. A read-only
// comparison must not call it: for a stored EKS payload this MINTS a real,
// usable, short-lived sign-in token, and a check that mints is a check with a
// blast radius. The comparison's own route is
// ClusterCredsRouter.StoredFactsIndependentOfArgoCDSecret, which cannot reach
// the mint at all.
//
// What it does NOT read: the live ArgoCD cluster Secret. The credentials come
// from the configured secrets backend, which is the whole point of the repair
// feature — building the expected connection out of the live one compares the
// Secret with itself and calls anything correct.
//
// Role precedence is the same one the normal write uses: the cluster's own
// roleArn from its managed-clusters.yaml entry, falling back to the
// connection-level default. That is the identity a cross-account cluster's token
// must be minted with, and the identity argocd-k8s-auth must assume if the
// connection ends up on the exec shape.
//
// The returned spec carries Name, Server, Region, RoleARN and every piece of
// credential material the backend gave back, so argosecrets.buildSecretConfig
// picks the connection shape on the SAME evidence whichever writer called this.
// Labels and Annotations are deliberately left empty: they are the caller's — the
// pass merges git's addon labels and the derived connectivity-check label, the
// repair does the same from its pinned commit, and provenance is stamped at write
// time.
//
// The error is returned unchanged, still typed and still carrying its credsafe
// marker, so the caller decides what to say. Nothing in here reads an error's
// words. A failure means the caller writes NOTHING — it must never fall back to
// a spec with no credential in it, which is exactly how a missing token turned
// into a changed sign-in method instead of a refusal.
func (r *Reconciler) ConnectionCredentialSpecForWrite(entry models.ManagedClusterEntry) (argosecrets.ClusterSecretSpec, error) {
	// SecretPath overrides Name for the backend lookup (shared resolver).
	credKey := entry.CredentialLookupKey()

	creds, err := providers.GetCredentialsWithOptionalRole(r.deps.Vault, credKey, entry.RoleARN)
	if err != nil {
		return argosecrets.ClusterSecretSpec{}, err
	}

	return argosecrets.ClusterSecretSpec{
		Name:    entry.Name,
		Server:  creds.Server,
		Region:  entry.Region,
		RoleARN: r.effectiveRoleARN(entry),
		// Every piece of credential material goes through, so
		// buildSecretConfig's precedence (cert pair > token > exec) decides on
		// the full picture:
		//   - CertData+KeyData set (a client-certificate kubeconfig — kind,
		//     kubeadm, on-prem): the plain-TLS shape.
		//   - Token set (a bearer-token kubeconfig, or a freshly minted EKS
		//     token): the bearerToken shape.
		//   - Neither: the exec shape, which is correct for an EKS/IAM cluster
		//     whose backend holds no credential to mint from.
		Token: creds.Token,
		// EncodeToString(nil) == "" so a cluster with no cert pair leaves these
		// empty and never takes the cert branch.
		CertData: base64.StdEncoding.EncodeToString(creds.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(creds.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(creds.CAData),
	}, nil
}

// effectiveRoleARN is the role this cluster's connection uses: its own, or the
// connection-level default when it has none of its own.
//
// It exists as a named function rather than four inline lines because the answer
// has to be the same for the token mint and for the exec shape's --role-arn
// argument, and those used to be two copies of the same three lines. Matches the
// DefaultRoleARN doc: it applies "for clusters whose entry does NOT specify one".
func (r *Reconciler) effectiveRoleARN(entry models.ManagedClusterEntry) string {
	if entry.RoleARN != "" {
		return entry.RoleARN
	}
	return r.deps.DefaultRoleARN
}
