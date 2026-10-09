package providers

// stored_facts.go — the read-only, NEVER-MINTING way to ask a secrets backend
// what a cluster's stored connection details say.
//
// WHY THIS EXISTS AT ALL.
//
// GetCredentials hands back credentials that WORK. For a cluster whose backend
// payload is structured EKS metadata, making them work means minting a
// brand-new short-lived STS sign-in token — a real credential, with a real
// blast radius if it escapes, created fresh on every single call.
//
// The read-only connection comparison does not want credentials that work. It
// wants to know what the connection SHOULD look like. And for an EKS cluster it
// has already decided, in internal/connectioncompare's mode policy, that the
// credential blob cannot be compared at all: the configured credentials source
// stores no credential, so there is nothing on the expected side to compare the
// live one against. So the comparison was paying for a real credential, and
// tripping every log line on the creation path, to produce a value it then
// threw away.
//
// This file is the fix. StoredConnectionFacts returns only what the backend has
// STORED — the API address, the CA bundle, and a fixed credential when and only
// when the payload actually contains one. Nothing here calls the token mint, on
// any branch, and the comparison never uses any other route.

// StoredConnectionFacts is what a secrets backend has stored for one cluster,
// read without creating anything.
//
// Every field is either a plain connection fact or a credential that was
// ALREADY sitting in the backend. Nothing in here was minted, generated, signed
// or requested from another service.
type StoredConnectionFacts struct {
	// Server is the cluster's API address as stored.
	Server string

	// CAData is the cluster's CA bundle, decoded to raw bytes (the same shape
	// Kubeconfig.CAData carries).
	CAData []byte

	// Token is a FIXED bearer token that was stored in the backend, or empty.
	// It is never one this code created — see CredentialMintedPerFetch.
	Token string

	// CertData and KeyData are a stored client certificate pair, or empty. Set
	// together or not at all, same rule as Kubeconfig.
	CertData []byte
	KeyData  []byte

	// CredentialMintedPerFetch is true when the stored payload contains no
	// usable credential at all — only the metadata needed to CREATE one. An EKS
	// metadata payload is the case that matters.
	//
	// The name is about GetCredentials, not about this reader. GetCredentials
	// hands back credentials that WORK, so on such a payload it creates a new
	// short-lived one every time it is called. That is exactly why a caller
	// that only wants to know what the connection should look like must come
	// through here instead: this reader creates nothing, on any branch.
	//
	// So when this is true, Token / CertData / KeyData are empty on purpose:
	// there is no stored credential to report, and none is made up. There is
	// no credential on the expected side, which means a comparison has nothing
	// to compare the live credential blob against — it reports that field as
	// one it did not check, and it does NOT reach for the credential by
	// another route.
	//
	// A WRITE is the other half of the story: repairing such a connection does
	// create a credential, once, at the moment it writes. A check creates
	// none.
	CredentialMintedPerFetch bool
}

// StoredConnectionFactsProvider is the optional capability for cluster-
// credential backends that can answer "what have you got stored for this
// cluster" WITHOUT minting anything.
//
// A backend that does not implement it is treated by the router as having no
// independent copy at all, rather than being asked through GetCredentials as a
// fallback. That is deliberate: a fallback to GetCredentials is exactly the
// mint this whole file exists to avoid, and a narrower comparison is a better
// answer than a real credential nobody wanted.
type StoredConnectionFactsProvider interface {
	StoredConnectionFacts(lookupKey string) (*StoredConnectionFacts, error)
}
