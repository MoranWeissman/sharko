// Package signingtest mints real, fully verifiable Sigstore bundles for
// tests, with certificates whose Fulcio claims the test chooses.
//
// Why it exists. sigstore-go's own test CA (testing/ca.VirtualSigstore)
// can only mint a certificate with an email SAN and the old issuer
// extension, and nothing else. That is not enough to test the checks
// Sharko applies to its own embedded catalogue: those read the URI SAN,
// the current issuer extension, the workflow_ref claim and the
// source-commit claim. This package mints a leaf certificate with exactly
// the claims a test asks for, signs the payload with it, records it in the
// virtual transparency log, and returns bundle bytes that go through the
// same public VerifyBundleBytes path the release gate and `sharko serve`
// use. Nothing in the verifier is bypassed or stubbed.
//
// Everything is signed by keys generated fresh inside the test process,
// so a bundle made here can only verify against the TrustedMaterial of the
// same Sigstore value. It can never verify against the real public-good
// trust root.
//
// This package is imported by tests only. It deliberately does not import
// internal/catalog/signing, so the signing package's own tests can use it.
package signingtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

// Fulcio certificate extension OIDs used here. Values from
// sigstore-go/pkg/fulcio/certificate.
var (
	oidIssuerV1               = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}  // raw string
	oidGithubWorkflowSHA      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 3}  // raw string
	oidGithubWorkflowRef      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 6}  // raw string
	oidIssuerV2               = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}  // DER UTF8String
	oidSourceRepositoryDigest = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 13} // DER UTF8String
)

// IssuerEncoding says which Fulcio extension carries the issuer, or that
// the certificate has no issuer at all.
type IssuerEncoding int

const (
	// IssuerV2 puts the issuer in OID 1.3.6.1.4.1.57264.1.8, the current
	// Fulcio extension, DER-encoded. This is what real Fulcio certificates
	// carry today and is the default.
	IssuerV2 IssuerEncoding = iota
	// IssuerV1 puts the issuer in the older OID 1.3.6.1.4.1.57264.1.1 only,
	// as a raw string.
	IssuerV1
	// NoIssuer leaves both issuer extensions out.
	NoIssuer
)

// Claims is what the leaf certificate says about the signer.
type Claims struct {
	// SAN is the certificate's URI subject alternative name — the signer
	// identity. Must parse as a URI.
	SAN string
	// Issuer is the OIDC issuer. Ignored when IssuerEncoding is NoIssuer.
	Issuer         string
	IssuerEncoding IssuerEncoding
	// WorkflowRef is the GitHub workflow_ref claim (OID .1.6). Empty
	// leaves the extension out.
	WorkflowRef string
	// SourceCommit is written into both sourceRepositoryDigest (.1.13) and
	// githubWorkflowSHA (.1.3), as real Fulcio does. Empty leaves both out.
	SourceCommit string
}

// Sigstore is an in-process Sigstore: a Fulcio root and intermediate, a
// transparency log and a timestamp authority, all with fresh keys.
type Sigstore struct {
	vs              *ca.VirtualSigstore
	fulcio          *root.FulcioCertificateAuthority
	intermediate    *x509.Certificate
	intermediateKey *ecdsa.PrivateKey
	logIndex        int64
}

// New builds a fresh in-process Sigstore.
func New() (*Sigstore, error) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		return nil, fmt.Errorf("virtual sigstore: %w", err)
	}
	rootCert, rootKey, err := ca.GenerateRootCa()
	if err != nil {
		return nil, fmt.Errorf("fulcio root: %w", err)
	}
	interCert, interKey, err := ca.GenerateFulcioIntermediate(rootCert, rootKey)
	if err != nil {
		return nil, fmt.Errorf("fulcio intermediate: %w", err)
	}
	return &Sigstore{
		vs: vs,
		fulcio: &root.FulcioCertificateAuthority{
			Root:                rootCert,
			Intermediates:       []*x509.Certificate{interCert},
			ValidityPeriodStart: time.Now().Add(-5 * time.Hour),
			ValidityPeriodEnd:   time.Now().Add(time.Hour),
			URI:                 "https://fulcio.test.invalid",
		},
		intermediate:    interCert,
		intermediateKey: interKey,
		logIndex:        1000,
	}, nil
}

// TrustedMaterial returns the trust root that bundles from this Sigstore
// verify against. Pass it to signing.WithTrustedMaterial.
func (s *Sigstore) TrustedMaterial() root.TrustedMaterial {
	return trustedMaterial{VirtualSigstore: s.vs, fulcio: s.fulcio}
}

// trustedMaterial is the virtual Sigstore's transparency log and
// timestamp authority, with this package's own Fulcio CA in place of the
// virtual one (whose intermediate key is not reachable from outside).
type trustedMaterial struct {
	*ca.VirtualSigstore
	fulcio *root.FulcioCertificateAuthority
}

func (t trustedMaterial) FulcioCertificateAuthorities() []root.CertificateAuthority {
	return []root.CertificateAuthority{t.fulcio}
}

// Certificate mints a leaf certificate with the given claims, chained to
// this Sigstore's Fulcio intermediate, and returns it with its key.
func (s *Sigstore) Certificate(c Claims) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	san, err := url.Parse(c.SAN)
	if err != nil {
		return nil, nil, fmt.Errorf("SAN %q is not a URI: %w", c.SAN, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	var exts []pkix.Extension
	switch c.IssuerEncoding {
	case IssuerV2:
		der, err := asn1.MarshalWithParams(c.Issuer, "utf8")
		if err != nil {
			return nil, nil, err
		}
		exts = append(exts, pkix.Extension{Id: oidIssuerV2, Value: der})
	case IssuerV1:
		exts = append(exts, pkix.Extension{Id: oidIssuerV1, Value: []byte(c.Issuer)})
	case NoIssuer:
	default:
		return nil, nil, fmt.Errorf("unknown issuer encoding %d", c.IssuerEncoding)
	}
	if c.WorkflowRef != "" {
		exts = append(exts, pkix.Extension{Id: oidGithubWorkflowRef, Value: []byte(c.WorkflowRef)})
	}
	if c.SourceCommit != "" {
		der, err := asn1.MarshalWithParams(c.SourceCommit, "utf8")
		if err != nil {
			return nil, nil, err
		}
		exts = append(exts,
			pkix.Extension{Id: oidSourceRepositoryDigest, Value: der},
			pkix.Extension{Id: oidGithubWorkflowSHA, Value: []byte(c.SourceCommit)},
		)
	}
	s.logIndex++
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(s.logIndex),
		NotBefore:       time.Now().Add(-1 * time.Minute),
		NotAfter:        time.Now().Add(10 * time.Minute),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		URIs:            []*url.URL{san},
		ExtraExtensions: exts,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, s.intermediate, &key.PublicKey, s.intermediateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create leaf certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// SignBundle signs payload with a fresh certificate carrying claims,
// records it in the virtual transparency log, and returns the serialized
// Sigstore bundle (media type version 0.1, which carries an inclusion
// promise rather than an inclusion proof).
func (s *Sigstore) SignBundle(payload []byte, c Claims) ([]byte, error) {
	cert, key, err := s.Certificate(c)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})

	// The hashedrekord v0.0.1 body, in canonical form: keys sorted, no
	// whitespace. encoding/json sorts map keys, so nested maps give that.
	body, err := json.Marshal(map[string]any{
		"apiVersion": "0.0.1",
		"kind":       "hashedrekord",
		"spec": map[string]any{
			"data": map[string]any{
				"hash": map[string]any{
					"algorithm": "sha256",
					"value":     hex.EncodeToString(digest[:]),
				},
			},
			"signature": map[string]any{
				"content": base64.StdEncoding.EncodeToString(sig),
				"publicKey": map[string]any{
					"content": base64.StdEncoding.EncodeToString(certPEM),
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	logIDHex, err := s.vs.RekorLogID()
	if err != nil {
		return nil, err
	}
	logIDRaw, err := hex.DecodeString(logIDHex)
	if err != nil {
		return nil, err
	}
	integrated := time.Now().Unix()
	set, err := s.vs.RekorSignPayload(tlog.RekorPayload{
		Body:           base64.StdEncoding.EncodeToString(body),
		IntegratedTime: integrated,
		LogIndex:       s.logIndex,
		LogID:          logIDHex,
	})
	if err != nil {
		return nil, fmt.Errorf("sign transparency log entry: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString
	bundle := map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle+json;version=0.1",
		"verificationMaterial": map[string]any{
			"x509CertificateChain": map[string]any{
				"certificates": []any{map[string]any{"rawBytes": b64(cert.Raw)}},
			},
			"tlogEntries": []any{map[string]any{
				"logIndex":          fmt.Sprint(s.logIndex),
				"logId":             map[string]any{"keyId": b64(logIDRaw)},
				"kindVersion":       map[string]any{"kind": "hashedrekord", "version": "0.0.1"},
				"integratedTime":    fmt.Sprint(integrated),
				"inclusionPromise":  map[string]any{"signedEntryTimestamp": b64(set)},
				"canonicalizedBody": b64(body),
			}},
		},
		"messageSignature": map[string]any{
			"messageDigest": map[string]any{"algorithm": "SHA2_256", "digest": b64(digest[:])},
			"signature":     b64(sig),
		},
	}
	return json.Marshal(bundle)
}
