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
// credential blob cannot be compared at all: a freshly minted token differs
// from the live one every time with nothing having drifted. So the comparison
// was paying for a real credential, and tripping every log line on the minting
// path, to produce a value it then threw away.
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
	// It is never a minted one — see CredentialMintedPerFetch.
	Token string

	// CertData and KeyData are a stored client certificate pair, or empty. Set
	// together or not at all, same rule as Kubeconfig.
	CertData []byte
	KeyData  []byte

	// CredentialMintedPerFetch is true when the stored payload does not contain
	// a usable credential at all — it contains the metadata needed to CREATE
	// one, and a new one is created on every fetch. An EKS metadata payload is
	// the case that matters.
	//
	// When this is true, Token / CertData / KeyData are empty on purpose: there
	// is no stored credential to report, and this reader will not make one.
	// A caller comparing a connection must treat the credential blob as a field
	// it cannot honestly compare rather than fetching it some other way.
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
