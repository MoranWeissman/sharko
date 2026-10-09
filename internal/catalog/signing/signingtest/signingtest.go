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
// in-process transparency log, and returns bundle bytes that go through the
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
//
// It also deliberately does not import sigstore-go's testing/ca. That
// package pulls in golang.org/x/crypto/openpgp, which has a known,
// unfixed advisory. Because this is not a _test.go file, `govulncheck
// ./...` analyses it, and with openpgp in the program it also blamed
// shipped code. So the root CA, the Fulcio intermediate, the
// transparency-log key and the signed entry timestamp are all built here
// with the standard library, in the same shapes testing/ca uses. Only
// sigstore-go's pkg/root and pkg/tlog are imported, which shipped code
// already uses.
package signingtest

import (
	"bytes"
	"crypto"
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

// Sigstore is an in-process Sigstore: a Fulcio root and intermediate and
// a transparency log, all with fresh keys.
type Sigstore struct {
	fulcio          *root.FulcioCertificateAuthority
	intermediate    *x509.Certificate
	intermediateKey *ecdsa.PrivateKey
	rekorKey        *ecdsa.PrivateKey
	rekorLogs       map[string]*root.TransparencyLog
	logIndex        int64
}

// New builds a fresh in-process Sigstore.
func New() (*Sigstore, error) {
	rootCert, rootKey, err := generateRootCA()
	if err != nil {
		return nil, fmt.Errorf("fulcio root: %w", err)
	}
	interCert, interKey, err := generateFulcioIntermediate(rootCert, rootKey)
	if err != nil {
		return nil, fmt.Errorf("fulcio intermediate: %w", err)
	}
	rekorKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("transparency log key: %w", err)
	}
	logID, err := logIDOf(rekorKey.Public())
	if err != nil {
		return nil, err
	}
	return &Sigstore{
		rekorKey: rekorKey,
		rekorLogs: map[string]*root.TransparencyLog{
			logID: {
				BaseURL:             "https://rekor.localhost",
				ID:                  []byte(logID),
				ValidityPeriodStart: time.Now().Add(-time.Hour),
				ValidityPeriodEnd:   time.Now().Add(time.Hour),
				HashFunc:            crypto.SHA256,
				PublicKey:           rekorKey.Public(),
				SignatureHashFunc:   crypto.SHA256,
			},
		},
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
//
// It holds this Sigstore's Fulcio CA and transparency log and nothing
// else: no timestamp authority and no certificate-transparency log. The
// bundles made here carry no RFC 3161 timestamp and no SCT, and the
// verifier Sharko uses asks for neither (it asks for one transparency-log
// entry and one observer timestamp, which the log entry's integrated time
// gives).
func (s *Sigstore) TrustedMaterial() root.TrustedMaterial {
	return &trustedMaterial{fulcio: s.fulcio, rekorLogs: s.rekorLogs}
}

// trustedMaterial is the smallest root.TrustedMaterial that fits. The
// embedded BaseTrustedMaterial answers "none" for everything not
// overridden here.
type trustedMaterial struct {
	root.BaseTrustedMaterial
	fulcio    *root.FulcioCertificateAuthority
	rekorLogs map[string]*root.TransparencyLog
}

func (t *trustedMaterial) FulcioCertificateAuthorities() []root.CertificateAuthority {
	return []root.CertificateAuthority{t.fulcio}
}

func (t *trustedMaterial) RekorLogs() map[string]*root.TransparencyLog {
	return t.rekorLogs
}

// logIDOf is the transparency-log ID of a key: the hex SHA-256 of its
// PKIX encoding. Same as testing/ca's getLogID.
func logIDOf(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(der)
	return hex.EncodeToString(digest[:]), nil
}

// signSET makes the signed entry timestamp for a transparency-log entry:
// an ECDSA P-256 / SHA-256 signature over the RFC 8785 canonical JSON of
// the tlog.RekorPayload. tlog.VerifySET checks exactly this.
//
// The payload has four keys whose values are two strings (base64 and
// hex, so no characters that need escaping) and two integers. For that
// shape, encoding/json with sorted map keys and HTML escaping off gives
// the same bytes as RFC 8785, so no canonicalisation library is needed.
// If that ever stopped being true, VerifySET would refuse every bundle
// and all the S11 tests would fail, not pass.
func (s *Sigstore) signSET(p tlog.RekorPayload) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{
		"body":           p.Body,
		"integratedTime": p.IntegratedTime,
		"logIndex":       p.LogIndex,
		"logID":          p.LogID,
	}); err != nil {
		return nil, err
	}
	canonical := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	digest := sha256.Sum256(canonical)
	return ecdsa.SignASN1(rand.Reader, s.rekorKey, digest[:])
}

// generateRootCA makes a self-signed P-256 root, the same shape as
// testing/ca.GenerateRootCa.
func generateRootCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "sigstore",
			Organization: []string{"sigstore.dev"},
		},
		NotBefore:             time.Now().Add(-5 * time.Hour),
		NotAfter:              time.Now().Add(5 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	cert, err := createCertificate(tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// generateFulcioIntermediate makes a code-signing intermediate under
// parent, the same shape as testing/ca.GenerateFulcioIntermediate.
func generateFulcioIntermediate(parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "sigstore-intermediate",
			Organization: []string{"sigstore.dev"},
		},
		NotBefore:             time.Now().Add(-2 * time.Minute),
		NotAfter:              time.Now().Add(2 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	cert, err := createCertificate(tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func createCertificate(tmpl, parent *x509.Certificate, pub crypto.PublicKey, parentKey crypto.Signer) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
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
// records it in the in-process transparency log, and returns the serialized
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

	logIDHex, err := logIDOf(s.rekorKey.Public())
	if err != nil {
		return nil, err
	}
	logIDRaw, err := hex.DecodeString(logIDHex)
	if err != nil {
		return nil, err
	}
	integrated := time.Now().Unix()
	set, err := s.signSET(tlog.RekorPayload{
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
