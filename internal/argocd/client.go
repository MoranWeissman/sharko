package argocd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ErrPermissionDenied is the sentinel returned (wrapped) when ArgoCD answers an
// API call with HTTP 403. Callers can detect it with errors.Is to distinguish a
// genuine "the token lacks RBAC permission" condition from other failures (a
// missing application, an unhealthy app, a network error). This matters because
// an admin apiKey token (sub "admin:apiKey") is subject to argocd-rbac-cm and
// gets a 403 — not ArgoCD's password-session superuser bypass — so a 403 should
// be surfaced as an RBAC problem, not mislabeled as a broken bootstrap.
var ErrPermissionDenied = errors.New("ArgoCD access denied — the token does not have permission for this operation")

// ErrTokenInvalid is the sentinel returned (wrapped) when ArgoCD answers an
// API call with HTTP 401. Callers can detect it with errors.Is to distinguish
// a genuinely bad/expired credential from other failures (permission denied,
// a missing application, a network error). Before this sentinel existed, a
// 401 fell into the same generic error path as every other ArgoCD failure,
// so the bootstrap-app probe could not tell "the token is dead" apart from
// "the app is unhealthy" — an expired token made a fully set-up install show
// the first-run wizard with a false "app already exists but is not healthy"
// message, because Sharko never actually got an answer from ArgoCD.
var ErrTokenInvalid = errors.New("invalid ArgoCD token — check that the token is correct and not expired")

// ErrTLSCertificateNotTrusted is the sentinel returned (wrapped) when the TLS
// handshake with the ArgoCD server fails certificate verification. The message
// carries the exact fix inline because a bare "handshake failed" sends
// operators hunting in the wrong place: the two honest options are trusting
// the CA, or explicitly opting this one connection out of verification.
//
// Sharko verifies the ArgoCD server's certificate BY DEFAULT (task #152
// adversarial finding). The connection to ArgoCD carries Sharko's ArgoCD
// bearer token — skipping verification would let anyone on the network path
// terminate the TLS session and read that token. The only way to skip
// verification is the operator's explicit argocd.insecure=true opt-in.
var ErrTLSCertificateNotTrusted = errors.New(
	"cannot verify the ArgoCD server's TLS certificate — Sharko refuses to send its ArgoCD token over a connection it cannot verify. " +
		"If this ArgoCD uses a self-signed or internal-CA certificate, either add that CA to the trust store of the Sharko pod, " +
		"or explicitly turn verification off for this connection: tick \"Skip TLS certificate verification\" in Settings → Connection, " +
		"or set the Helm value connection.argocd.insecure=true (env SHARKO_CONN_ARGOCD_INSECURE=true)")

// isCertVerificationError reports whether err (anywhere in its chain) is a
// TLS certificate verification failure — as opposed to a network error, a
// refused connection, or a plain-HTTP endpoint.
func isCertVerificationError(err error) bool {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return true
	}
	// Belt and braces: older wrapping shapes surface the x509 error directly.
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid)
}

// tlsExplainTransport wraps the real transport so that a failed certificate
// verification surfaces as ErrTLSCertificateNotTrusted (with the canned fix
// sentence) instead of a raw handshake error. Every request the client makes
// goes through here — one choke point, no per-call-site wrapping to forget.
type tlsExplainTransport struct {
	base http.RoundTripper
}

func (t *tlsExplainTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil && isCertVerificationError(err) {
		return nil, fmt.Errorf("%w [server: %s, detail: %v]", ErrTLSCertificateNotTrusted, req.URL.Host, err)
	}
	return resp, err
}

// ErrArgocdNeedsHTTPS is the sentinel returned (wrapped) when an ArgoCD
// address starting with http:// answers by sending Sharko on to https://. A
// default ArgoCD install does exactly this: TLS is on, and a plain http
// request on the same port gets a 307 to https.
//
// Sharko does NOT follow that redirect. Following it means the first request
// — the one carrying the ArgoCD token — already went out over plain http.
// Instead, before any token is sent to an http:// address, the client asks
// that address a credential-free question first (see plainHTTPGuardTransport)
// and stops here if the answer is "go to https". The error names the https
// address to use instead, built only from the scheme and host Sharko already
// had — never from the operator's whole address, which is where a credential
// can be written (see unreachable.go).
var ErrArgocdNeedsHTTPS = errors.New("ArgoCD answers only over https at this address — change the ArgoCD address to start with https://")

// ErrArgocdRedirectToHTTP is the sentinel returned (wrapped) when an https
// ArgoCD address sends Sharko on to plain http. Following it would send the
// ArgoCD token unencrypted, so Sharko refuses.
var ErrArgocdRedirectToHTTP = errors.New("ArgoCD tried to send Sharko from https to plain http — Sharko refuses to follow it, so the ArgoCD token never travels unencrypted. Check the ArgoCD address and any proxy in front of it")

// schemeChangeError is what the client returns when ArgoCD redirects between
// http and https. httpsAddress is "https://<host>" when the redirect went to
// https on the same host name, and "" otherwise.
type schemeChangeError struct {
	toHTTP       bool
	httpsAddress string
}

func (e *schemeChangeError) Error() string {
	if e.toHTTP {
		return ErrArgocdRedirectToHTTP.Error()
	}
	if e.httpsAddress != "" {
		return "ArgoCD answers only over https at this address — change the ArgoCD address to " + e.httpsAddress
	}
	return ErrArgocdNeedsHTTPS.Error()
}

func (e *schemeChangeError) Is(target error) bool {
	if e.toHTTP {
		return target == ErrArgocdRedirectToHTTP
	}
	return target == ErrArgocdNeedsHTTPS
}

// isRedirectStatus reports whether an HTTP status is a redirect that carries
// a Location to follow.
func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// sameHostHTTPSAddress returns "https://<host>" when to is an https URL on the
// same host name as from, and "" otherwise. Only the host (and port) is used:
// never the user-info, path, query or fragment of either URL.
//
// It also returns "" when the redirect carries a user part, or a port that is
// not a number from 1 to 65535: Sharko does not hand an operator an address
// it would not use itself.
func sameHostHTTPSAddress(from, to *url.URL) string {
	if to == nil || from == nil || to.Scheme != "https" || to.Hostname() == "" {
		return ""
	}
	if to.User != nil {
		return ""
	}
	if !strings.EqualFold(to.Hostname(), from.Hostname()) {
		return ""
	}
	if !validPort(to.Port()) {
		return ""
	}
	return "https://" + to.Host
}

// validPort reports whether p is empty (the default port) or a number from 1
// to 65535.
func validPort(p string) bool {
	if p == "" {
		return true
	}
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == p
}

// cleanRedirectPathPrefix checks the path prefix discovery copies from a
// redirect's Location (ArgoCD running under server.rootpath). It returns the
// prefix with no trailing slash, and false when the prefix is not a plain
// path: a ".." part, a doubled slash, a user part ("@"), a backslash, or any
// other character outside letters, digits and "-._~/".
func cleanRedirectPathPrefix(prefix string) (string, bool) {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return "", true
	}
	if !strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "//") {
		return "", false
	}
	for _, part := range strings.Split(prefix[1:], "/") {
		if part == "." || part == ".." {
			return "", false
		}
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_', r == '~', r == '/':
		default:
			return "", false
		}
	}
	return prefix, true
}

// refuseSchemeChange is the client's CheckRedirect. It refuses any redirect
// that changes between http and https, in either direction:
//
//   - https → http would send the token unencrypted;
//   - http → https means the configured address is wrong, and the operator
//     should be told the right one rather than have Sharko quietly use it.
//
// Same-scheme redirects keep the standard library's limit of 10.
func refuseSchemeChange(req *http.Request, via []*http.Request) error {
	prev := via[len(via)-1]
	if !strings.EqualFold(req.URL.Scheme, prev.URL.Scheme) {
		if strings.EqualFold(req.URL.Scheme, "http") {
			return &schemeChangeError{toHTTP: true}
		}
		return &schemeChangeError{httpsAddress: sameHostHTTPSAddress(prev.URL, req.URL)}
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// plainHTTPGuardTransport keeps the ArgoCD token away from a plain http
// address that sends Sharko on to https. Once such a redirect is seen, the
// token is never sent to that host over http again; before that, see "What
// this does not cover" below.
//
// A redirect cannot be refused before the request that triggered it was sent,
// and that request carries the token. So this transport follows two rules
// for every request to an http:// address that carries an Authorization
// header:
//
//  1. Once ANY request to a host over http was sent on to https — a check
//     request or a real one, on any path — the token is never sent to that
//     host over http again. The request stops with ErrArgocdNeedsHTTPS
//     before anything is sent.
//  2. Otherwise the request is first sent WITHOUT the token, as a GET with
//     no body, no query and no other header, to the same host and path. Only
//     when that answer is not a redirect to https does the real request go
//     out. The answer is remembered for that exact host and path, so the
//     check runs once per path per client, not once per call.
//
// Why per path and not per host: before, the check asked only /api/version,
// once per host. A proxy that sends only some paths to https would pass that
// check and then get the token over http on the first real call to one of
// the other paths.
//
// Why the check is a GET even when the real request is a POST or a DELETE:
// an ArgoCD with anonymous access turned on would carry out a write that
// arrives without a token. A GET changes nothing.
//
// A check that gets no answer at all stops the request too, so the token is
// never sent to a host whose answer is not known. The real request would
// have failed the same way.
//
// What this does not cover (LO-7 R4): the check is a GET with no query,
// asked once per path. So the token can still go out over plain http ONCE,
// on the real request, when a proxy sends only some methods (only POST, say)
// or only some query strings on to https, or when a path that answered the
// check over plain http only later starts sending Sharko on to https. That
// real request's answer is the redirect, so rule 1 stops every later one.
// The operator docs (docs/site/operator/argocd-upstream-unreachable.md) say
// the same.
//
// The state is per client (per transport), never package-level.
type plainHTTPGuardTransport struct {
	base http.RoundTripper

	mu sync.Mutex
	// plainOK holds "host + path" keys known to answer plain http with no
	// redirect to https.
	plainOK map[string]bool
	// httpsOnly holds every host name (lower case, no port) that has sent an
	// http request on to https, with the https address to name ("" when the
	// redirect went to another host).
	httpsOnly map[string]string
}

// maxPlainOKPaths caps how many paths one client remembers. Past it the list
// starts again empty, which only means a path is checked once more.
const maxPlainOKPaths = 1024

// plainHTTPCheckHeader marks the credential-free check request.
const plainHTTPCheckHeader = "X-Sharko-Plain-Http-Check"

func (t *plainHTTPGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.EqualFold(req.URL.Scheme, "http") {
		return t.base.RoundTrip(req)
	}
	if req.Header.Get("Authorization") != "" {
		if addr, ok := t.httpsOnlyAddress(req.URL); ok {
			return nil, &schemeChangeError{httpsAddress: addr}
		}
		if err := t.checkPlainHTTPPath(req); err != nil {
			return nil, err
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		// A real request can be sent on to https too (a proxy that changed
		// its mind). The token already went with this one; make sure it is
		// the last.
		t.noteRedirectToHTTPS(req.URL, resp)
	}
	return resp, err
}

func (t *plainHTTPGuardTransport) httpsOnlyAddress(u *url.URL) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	addr, ok := t.httpsOnly[strings.ToLower(u.Hostname())]
	return addr, ok
}

// noteRedirectToHTTPS remembers the host when resp sends an http request on
// to https. It reports whether it did.
func (t *plainHTTPGuardTransport) noteRedirectToHTTPS(from *url.URL, resp *http.Response) bool {
	if !isRedirectStatus(resp.StatusCode) {
		return false
	}
	loc, err := resp.Location()
	if err != nil || !strings.EqualFold(loc.Scheme, "https") {
		return false
	}
	host := strings.ToLower(from.Hostname())
	addr := sameHostHTTPSAddress(from, loc)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.httpsOnly == nil {
		t.httpsOnly = map[string]string{}
	}
	if old, seen := t.httpsOnly[host]; !seen || old == "" {
		t.httpsOnly[host] = addr
	}
	return true
}

func plainOKKey(u *url.URL) string {
	return strings.ToLower(u.Host) + u.EscapedPath()
}

func (t *plainHTTPGuardTransport) checkPlainHTTPPath(req *http.Request) error {
	key := plainOKKey(req.URL)
	t.mu.Lock()
	done := t.plainOK[key]
	t.mu.Unlock()
	if done {
		return nil
	}

	// Built from scheme, host and path only: no user-info, no query, no
	// fragment, no body, and no header of the real request.
	probeURL := &url.URL{Scheme: "http", Host: req.URL.Host, Path: req.URL.Path, RawPath: req.URL.RawPath}
	probe, err := http.NewRequestWithContext(req.Context(), http.MethodGet, probeURL.String(), nil)
	if err != nil {
		// Never the cause (BF12): a *url.Error's words are the address.
		return errors.New("Sharko could not build its plain-http check request for ArgoCD, so it did not send the ArgoCD token")
	}
	// Says what this request is, so a log on the far side can tell it apart.
	// It carries nothing secret.
	probe.Header.Set(plainHTTPCheckHeader, "1")
	resp, err := t.base.RoundTrip(probe)
	if err != nil {
		// No answer to the check, so the token is not sent. The caller
		// turns this into the usual "no answer" error.
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	if t.noteRedirectToHTTPS(probeURL, resp) {
		addr, _ := t.httpsOnlyAddress(probeURL)
		return &schemeChangeError{httpsAddress: addr}
	}

	t.mu.Lock()
	if t.plainOK == nil || len(t.plainOK) >= maxPlainOKPaths {
		t.plainOK = map[string]bool{}
	}
	t.plainOK[key] = true
	t.mu.Unlock()
	return nil
}

// Client is a REST API client for ArgoCD.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
	// basePath is the path of the ArgoCD address Sharko was given, with no
	// trailing slash: "/argocd" for http://host/argocd (ArgoCD running under
	// server.rootpath), "" for http://host. basePathOK is false when that
	// path is not a plain path (see cleanRedirectPathPrefix). Both are used
	// only to name the https address in the "needs https" sentence.
	basePath   string
	basePathOK bool
}

// addressBasePath returns the path of an ArgoCD address, cleaned, with no
// trailing slash, and false when the address cannot be read or its path is
// not a plain path. It never returns the user part, the query or the
// fragment.
func addressBasePath(serverURL string) (string, bool) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", false
	}
	return cleanRedirectPathPrefix(u.EscapedPath())
}

// withBasePath takes what unreachableCallError returned. When it says the
// http address only answers over https on the same host, the https address
// it names gets the path of the address Sharko was given, so
// http://host/argocd gives https://host/argocd, not https://host. When that
// path is not a plain path, no address is named at all, and the sentence
// says to change the address so it starts with https:// instead.
func (c *Client) withBasePath(err error) error {
	var out *UnreachableError
	if errors.As(err, &out) && out.Code == UnreachableNeedsHTTPS && out.httpsAddress != "" {
		if c.basePathOK {
			out.httpsAddress += c.basePath
		} else {
			out.httpsAddress = ""
		}
	}
	return err
}

// NewClient creates an ArgoCD client with bearer token authentication.
// Use this for local development or personal access token (PAT) mode.
//
// TLS certificate verification is ON unless insecure is true. insecure must
// only ever be set from the operator's explicit connection-level opt-in
// (argocd.insecure) — never hardcoded true at a call site. A verification
// failure surfaces as ErrTLSCertificateNotTrusted, which names the fix.
func NewClient(serverURL, token string, insecure bool) *Client {
	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // #nosec G402 -- verification is ON by default; this branch only runs on the operator's explicit connection-level opt-in (argocd.insecure), never hardcoded — see ErrTLSCertificateNotTrusted
		}
	}

	basePath, basePathOK := addressBasePath(strings.TrimRight(serverURL, "/"))
	return &Client{
		baseURL: strings.TrimRight(serverURL, "/"),
		httpClient: &http.Client{
			Transport:     &tlsExplainTransport{base: &plainHTTPGuardTransport{base: transport}},
			CheckRedirect: refuseSchemeChange,
		},
		token:      token,
		basePath:   basePath,
		basePathOK: basePathOK,
	}
}

// NewInClusterClient creates an ArgoCD client for in-cluster use.
// It reads the ServiceAccount token from the standard mount path.
// If serverURL is empty, it discovers the ArgoCD server service via K8s DNS.
//
// TLS certificate verification is always ON here — this client carries the
// pod's ServiceAccount token, and there is no connection-level opt-in on this
// path. Callers that need to reach a self-signed https ArgoCD must go through
// a connection with argocd.insecure=true instead.
func NewInClusterClient(serverURL, namespace string) (*Client, error) {
	const saTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	tokenBytes, err := os.ReadFile(saTokenPath)
	if err != nil {
		return nil, fmt.Errorf("reading service account token: %w", err)
	}

	if serverURL == "" {
		if namespace == "" {
			namespace = "argocd"
		}
		// Try to discover ArgoCD server by looking for a service with port 443 in the namespace
		serverURL = discoverArgocdServer(namespace, false)
	}

	slog.Info("argocd in-cluster client initialized", "server", serverURL, "namespace", namespace)
	return NewClient(serverURL, strings.TrimSpace(string(tokenBytes)), false), nil
}

// DiscoverServerURL finds the ArgoCD server URL for the given namespace.
// Exported for use by connection service when token is provided but URL is not.
//
// insecure mirrors the connection's argocd.insecure opt-in: the discovery
// probes use the same TLS stance the real client will. One exception, on
// purpose: an http address that redirects to https is reported as the https
// address even when its certificate does not verify, because that IS the
// address — the client then explains the certificate problem.
func DiscoverServerURL(namespace string, insecure bool) string {
	return discoverArgocdServer(namespace, insecure)
}

// commonArgoServiceNames lists well-known ArgoCD server service names in priority order.
// Different Helm releases produce different names depending on the release name used.
var commonArgoServiceNames = []string{
	"argocd-server",         // default Helm chart (release name: argocd)
	"argo-cd-argocd-server", // release name: argo-cd
	"argocd-argocd-server",  // release name: argocd (some versions)
}

// discoverArgocdServer tries to find the ArgoCD server service in the given namespace.
// Priority: ARGOCD_SERVER_URL env var → K8s service listing with HTTP probe → common name probes → default name.
//
// For each candidate service, https is probed before http, so a discovered URL
// prefers TLS whenever a working https endpoint exists. With insecure=false
// (the default) the https probe verifies the certificate. When it does not
// verify, the http probe still finds out the truth: a TLS-on ArgoCD answers
// http with a redirect to https, and probeArgoCD reports the https address
// for it. The real client then names the certificate problem, instead of
// discovery handing back an http address that only redirects. With
// insecure=true (the operator's explicit connection-level opt-in) the probes
// skip verification exactly like the real client will.
func discoverArgocdServer(namespace string, insecure bool) string {
	// Check ARGOCD_SERVER_URL env var first
	if url := os.Getenv("ARGOCD_SERVER_URL"); url != "" {
		return url
	}

	// Try K8s API to list all services and find the ArgoCD server by probing.
	cfg, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(cfg)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			svcs, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
			if err == nil {
				// First pass: prefer well-known service names — probe to confirm they respond.
				svcByName := make(map[string]struct{}, len(svcs.Items))
				for _, svc := range svcs.Items {
					svcByName[svc.Name] = struct{}{}
				}
				for _, name := range commonArgoServiceNames {
					if _, ok := svcByName[name]; ok {
						if url := probeServiceSchemes(name, namespace, insecure); url != "" {
							slog.Info("discovered ArgoCD server service by name", "service", name, "url", url)
							return url
						}
					}
				}

				// Second pass: probe any service that exposes port 80 or 443 — handles
				// custom Helm release names.
				for _, svc := range svcs.Items {
					for _, port := range svc.Spec.Ports {
						if port.Port != 80 && port.Port != 443 {
							continue
						}
						if url := probeServiceSchemes(svc.Name, namespace, insecure); url != "" {
							slog.Info("discovered ArgoCD server service", "service", svc.Name, "url", url)
							return url
						}
					}
				}
			}
		}
	}

	return fallbackDiscovery(namespace, insecure)
}

// fallbackDiscovery probes common ArgoCD service names when K8s API is not available.
func fallbackDiscovery(namespace string, insecure bool) string {
	for _, name := range commonArgoServiceNames {
		if url := probeServiceSchemes(name, namespace, insecure); url != "" {
			slog.Info("ArgoCD server responded to fallback probe", "service", name, "url", url)
			return url
		}
	}
	// Last resort: default name.
	return fmt.Sprintf("http://argocd-server.%s.svc.cluster.local", namespace)
}

// probeServiceSchemes probes a service's cluster DNS name over https first,
// then http, and returns the first base URL that answers as an ArgoCD server.
// Returns "" when neither scheme responds.
func probeServiceSchemes(serviceName, namespace string, insecure bool) string {
	for _, scheme := range []string{"https", "http"} {
		url := fmt.Sprintf("%s://%s.%s.svc.cluster.local", scheme, serviceName, namespace)
		if found := probeArgoCD(url, insecure); found != "" {
			return found
		}
	}
	return ""
}

// probeVersionPath is the endpoint the discovery probe asks.
const probeVersionPath = "/api/v1/version"

// probeArgoCD checks whether an ArgoCD server is reachable at the given base
// URL by sending a GET request to /api/v1/version with a 2-second timeout. It
// returns the base URL to use, or "" when nothing answered as ArgoCD.
//
// The probe never follows a redirect. A default ArgoCD install (TLS on, with a
// certificate it signed itself) answers plain http with a 307 to https on the
// same host. That means "the address is https", so the probe returns the
// https address — even when the certificate does not verify. The real client
// then reports the certificate problem in words the operator can act on,
// instead of discovery quietly handing back an http address that only
// redirects. Following the redirect here would also have tested the https
// certificate under the http label, which is how a TLS-on ArgoCD used to come
// back as "http://...".
//
// The probe sends NO credential — no Authorization header, no cookie. TLS
// certificate verification is ON by default; insecure mirrors the operator's
// explicit connection-level opt-in. It is never turned off here on its own.
func probeArgoCD(baseURL string, insecure bool) string {
	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // #nosec G402 -- credential-free discovery probe; verification is ON by default and this branch only runs on the same explicit argocd.insecure opt-in the real client uses
		}
	}
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(baseURL + probeVersionPath)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return baseURL
	}
	if !isRedirectStatus(resp.StatusCode) {
		return ""
	}
	// Only an http → https redirect on the same host name counts.
	from, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(from.Scheme, "http") {
		return ""
	}
	// The Location header exactly as sent. resp.Location() would resolve it
	// first, and resolving quietly takes ".." parts away, so a redirect that
	// carries them could not be refused. A move to https always needs a full
	// address, so a relative Location is not one anyway.
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !loc.IsAbs() {
		return ""
	}
	httpsBase := sameHostHTTPSAddress(from, loc)
	if httpsBase == "" {
		return ""
	}
	// Keep a path prefix if ArgoCD runs under one (server.rootpath). A
	// prefix that is not a plain path means the redirect is not one Sharko
	// trusts, so nothing is discovered.
	if prefix, ok := strings.CutSuffix(loc.Path, probeVersionPath); ok {
		clean, ok := cleanRedirectPathPrefix(prefix)
		if !ok {
			return ""
		}
		httpsBase += clean
	}
	return httpsBase
}

// TestConnection verifies that the client can reach the ArgoCD server
// AND that the token has valid permissions (uses an authenticated endpoint).
func (c *Client) TestConnection(ctx context.Context) error {
	// Use /api/v1/clusters which requires authentication,
	// unlike /api/version which is public and doesn't validate the token.
	_, err := c.doGet(ctx, "/api/v1/clusters")
	if err != nil {
		return err
	}
	return nil
}

// ListClusters returns all clusters registered in ArgoCD.
func (c *Client) ListClusters(ctx context.Context) ([]models.ArgocdCluster, error) {
	body, err := c.doGet(ctx, "/api/v1/clusters")
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}

	var raw struct {
		Items []argocdClusterItem `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding clusters response: %w", err)
	}

	clusters := make([]models.ArgocdCluster, 0, len(raw.Items))
	for _, item := range raw.Items {
		clusters = append(clusters, item.toModel())
	}
	return clusters, nil
}

// ListApplications returns all applications managed by ArgoCD.
func (c *Client) ListApplications(ctx context.Context) ([]models.ArgocdApplication, error) {
	body, err := c.doGet(ctx, "/api/v1/applications")
	if err != nil {
		return nil, fmt.Errorf("listing applications: %w", err)
	}

	var raw struct {
		Items []argocdApplicationItem `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding applications response: %w", err)
	}

	apps := make([]models.ArgocdApplication, 0, len(raw.Items))
	for _, item := range raw.Items {
		apps = append(apps, item.toModel())
	}
	return apps, nil
}

// GetApplication returns a single ArgoCD application by name.
func (c *Client) GetApplication(ctx context.Context, name string) (*models.ArgocdApplication, error) {
	body, err := c.doGet(ctx, "/api/v1/applications/"+name)
	if err != nil {
		return nil, fmt.Errorf("getting application %q: %w", name, err)
	}

	var raw argocdApplicationItem
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding application response: %w", err)
	}

	app := raw.toModel()
	return &app, nil
}

// GetVersion returns ArgoCD server version information.
func (c *Client) GetVersion(ctx context.Context) (map[string]string, error) {
	body, err := c.doGet(ctx, "/api/version")
	if err != nil {
		return nil, fmt.Errorf("getting version: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding version response: %w", err)
	}

	result := make(map[string]string)
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result, nil
}

// ApplicationSetStatus holds status information for an ArgoCD ApplicationSet.
type ApplicationSetStatus struct {
	Name       string `json:"name"`
	Conditions []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"conditions"`
	GeneratedApps int `json:"generated_apps"` // count from status.applicationStatus
}

// GetApplicationSet returns status information for a specific ArgoCD ApplicationSet.
func (c *Client) GetApplicationSet(ctx context.Context, name string) (*ApplicationSetStatus, error) {
	body, err := c.doGet(ctx, "/api/v1/applicationsets/"+name)
	if err != nil {
		return nil, fmt.Errorf("getting applicationset %q: %w", name, err)
	}

	var raw struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"conditions"`
			ApplicationStatus []struct {
				Application string `json:"application"`
			} `json:"applicationStatus"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding applicationset response: %w", err)
	}

	result := &ApplicationSetStatus{
		Name:          raw.Metadata.Name,
		GeneratedApps: len(raw.Status.ApplicationStatus),
	}
	for _, c := range raw.Status.Conditions {
		result.Conditions = append(result.Conditions, struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}{Type: c.Type, Status: c.Status, Message: c.Message})
	}
	return result, nil
}

// GetResourceTree returns the full resource tree for an ArgoCD application as raw JSON.
func (c *Client) GetResourceTree(ctx context.Context, appName string) (map[string]interface{}, error) {
	body, err := c.doGet(ctx, "/api/v1/applications/"+appName+"/resource-tree")
	if err != nil {
		return nil, fmt.Errorf("getting resource tree for %q: %w", appName, err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding resource tree response: %w", err)
	}
	return raw, nil
}

// GetManagedResource returns the live manifest of a specific managed resource.
// It filters out Secret kind to prevent leaking sensitive data.
func (c *Client) GetManagedResource(ctx context.Context, appName, namespace, resourceName, group, kind string) (map[string]interface{}, error) {
	if strings.EqualFold(kind, "Secret") {
		return nil, fmt.Errorf("refusing to return Secret resources for security reasons")
	}

	path := fmt.Sprintf("/api/v1/applications/%s/managed-resources?namespace=%s&resourceName=%s&group=%s&kind=%s",
		appName, namespace, resourceName, group, kind)

	body, err := c.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("getting managed resource for %q: %w", appName, err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding managed resource response: %w", err)
	}
	return raw, nil
}

// GetApplicationEvents returns recent Kubernetes events for an ArgoCD application.
func (c *Client) GetApplicationEvents(ctx context.Context, appName string) ([]map[string]interface{}, error) {
	body, err := c.doGet(ctx, "/api/v1/applications/"+appName+"/events")
	if err != nil {
		return nil, fmt.Errorf("getting events for %q: %w", appName, err)
	}

	var raw struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding events response: %w", err)
	}
	return raw.Items, nil
}

// GetPodLogs returns recent log lines for a pod managed by an ArgoCD application.
// ArgoCD proxies the log request to the remote cluster.
func (c *Client) GetPodLogs(ctx context.Context, appName, namespace, podName, container string, tailLines int) (string, error) {
	if tailLines <= 0 {
		tailLines = 50
	}
	path := fmt.Sprintf("/api/v1/applications/%s/logs?namespace=%s&podName=%s&tailLines=%d&follow=false",
		appName, namespace, podName, tailLines)
	if container != "" {
		path += "&container=" + container
	}

	body, err := c.doGet(ctx, path)
	if err != nil {
		return "", fmt.Errorf("getting logs for pod %q in app %q: %w", podName, appName, err)
	}

	return string(body), nil
}

// ListApplicationsSummary returns all applications with summary data (no history/resources).
// This is the same as ListApplications and is suitable for list views and health overviews.
func (c *Client) ListApplicationsSummary(ctx context.Context) ([]models.ArgocdApplication, error) {
	return c.ListApplications(ctx)
}

// doGet performs an authenticated GET request and returns the response body.
func (c *Client) doGet(ctx context.Context, path string) ([]byte, error) {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Never "%w" here (BF12). net/http returns a *url.Error whose text
		// is the whole address it was handed, unmasked, and the address is
		// c.baseURL — an operator-supplied value. The Do branch below has
		// been address-free since unreachable.go was written; this one was
		// the leftover. The verb and the endpoint are Sharko's own words.
		return nil, fmt.Errorf("the %s call to %s could not be built (%s)",
			http.MethodGet, safeEndpoint(path), credsafe.PlainFailureReason(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The address Sharko dialled does not travel out of here — see
		// unreachable.go. The cause still goes to the log sink, which prints
		// it through credsafe.LogClass by type.
		return nil, c.withBasePath(unreachableCallError(http.MethodGet, path, err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The response BODY is deliberately not logged (B9). ArgoCD quotes
		// whatever it was working on inside its error payloads, and that
		// routinely includes a repository address with the access token in it
		// — https://x-access-token:<token>@host/org/repo.git. A body is a
		// plain string by the time it reaches slog, so the log sink's
		// error-classifier cannot see a type to strip; the only safe place to
		// drop it is here. The endpoint and the status code say which call
		// failed and how, which is what triage actually needs.
		slog.Error("argocd call failed", "endpoint", safeEndpoint(path), "status", resp.StatusCode)
		// Translate common errors into user-friendly messages
		switch resp.StatusCode {
		case 401:
			// Wrap the sentinel so callers (e.g. the bootstrap-app probe) can
			// detect an invalid/expired token with errors.Is and surface an
			// honest "couldn't check" state instead of silently mislabeling
			// this as "bootstrap missing or unhealthy".
			return nil, fmt.Errorf("%w", ErrTokenInvalid)
		case 403:
			// Wrap the sentinel so callers (e.g. the bootstrap-app probe) can
			// detect permission-denied with errors.Is and surface an
			// RBAC-focused message instead of "bootstrap missing or unhealthy".
			return nil, fmt.Errorf("%w", ErrPermissionDenied)
		case 404:
			return nil, fmt.Errorf("ArgoCD endpoint not found (%s) — check the server URL", safeEndpoint(path))
		default:
			return nil, fmt.Errorf("ArgoCD returned status %d from %s", resp.StatusCode, safeEndpoint(path))
		}
	}

	return body, nil
}

// ---------- internal types for mapping ArgoCD API JSON ----------

// argocdClusterItem mirrors the nested JSON structure returned by the ArgoCD
// clusters API.
type argocdClusterItem struct {
	Name          string   `json:"name"`
	Server        string   `json:"server"`
	ServerVersion string   `json:"serverVersion"`
	Namespaces    []string `json:"namespaces"`
	Info          struct {
		ConnectionState struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"connectionState"`
		ServerVersion string `json:"serverVersion"`
	} `json:"info"`
}

func (c argocdClusterItem) toModel() models.ArgocdCluster {
	serverVersion := c.ServerVersion
	if serverVersion == "" {
		serverVersion = c.Info.ServerVersion
	}

	info := make(map[string]interface{})
	if c.Info.ConnectionState.Message != "" {
		info["connectionMessage"] = c.Info.ConnectionState.Message
	}

	return models.ArgocdCluster{
		Name:            c.Name,
		Server:          c.Server,
		ConnectionState: c.Info.ConnectionState.Status,
		ServerVersion:   serverVersion,
		Namespaces:      c.Namespaces,
		Info:            info,
	}
}

// argocdApplicationItem mirrors the nested JSON structure returned by the
// ArgoCD applications API.
type argocdApplicationItem struct {
	Metadata struct {
		Name              string `json:"name"`
		Namespace         string `json:"namespace"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Project string `json:"project"`
		Source  struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			TargetRevision string `json:"targetRevision"`
			Chart          string `json:"chart"`
			Helm           *struct {
				Parameters []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"parameters"`
			} `json:"helm"`
		} `json:"source"`
		Sources []struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			TargetRevision string `json:"targetRevision"`
			Chart          string `json:"chart"`
		} `json:"sources"`
		Destination struct {
			Server    string `json:"server"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"destination"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status             string `json:"status"`
			LastTransitionTime string `json:"lastTransitionTime"`
		} `json:"health"`
		ReconciledAt   string `json:"reconciledAt"`
		OperationState *struct {
			Phase      string `json:"phase"`
			StartedAt  string `json:"startedAt"`
			FinishedAt string `json:"finishedAt"`
			Message    string `json:"message"`
			SyncResult *struct {
				Resources []struct {
					Kind    string `json:"kind"`
					Name    string `json:"name"`
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"resources"`
			} `json:"syncResult"`
		} `json:"operationState"`
		History []struct {
			ID              int      `json:"id"`
			DeployedAt      string   `json:"deployedAt"`
			DeployStartedAt string   `json:"deployStartedAt"`
			Revision        string   `json:"revision"`
			Revisions       []string `json:"revisions"`
			Source          *struct {
				RepoURL        string `json:"repoURL"`
				Path           string `json:"path"`
				TargetRevision string `json:"targetRevision"`
			} `json:"source"`
		} `json:"history"`
		Conditions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"conditions"`
		Resources []struct {
			Group     string `json:"group"`
			Version   string `json:"version"`
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Health    *struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
			RequiresPruning bool `json:"requiresPruning"`
		} `json:"resources"`
	} `json:"status"`
}

func (a argocdApplicationItem) toModel() models.ArgocdApplication {
	// For multi-source apps, extract chart info from the first source that has a chart
	repoURL := a.Spec.Source.RepoURL
	path := a.Spec.Source.Path
	targetRevision := a.Spec.Source.TargetRevision
	chart := a.Spec.Source.Chart

	if repoURL == "" && len(a.Spec.Sources) > 0 {
		for _, src := range a.Spec.Sources {
			if src.Chart != "" {
				repoURL = src.RepoURL
				chart = src.Chart
				targetRevision = src.TargetRevision
				break
			}
		}
		// If no chart source found, use the first source
		if repoURL == "" {
			repoURL = a.Spec.Sources[0].RepoURL
			path = a.Spec.Sources[0].Path
			targetRevision = a.Spec.Sources[0].TargetRevision
		}
	}

	app := models.ArgocdApplication{
		Name:                 a.Metadata.Name,
		Namespace:            a.Metadata.Namespace,
		Project:              a.Spec.Project,
		SourceRepoURL:        repoURL,
		SourcePath:           path,
		SourceTargetRevision: targetRevision,
		SourceChart:          chart,
		DestinationServer:    a.Spec.Destination.Server,
		DestinationName:      a.Spec.Destination.Name,
		DestinationNamespace: a.Spec.Destination.Namespace,
		SyncStatus:           a.Status.Sync.Status,
		HealthStatus:         a.Status.Health.Status,
		CreatedAt:            a.Metadata.CreationTimestamp,
		HealthLastTransition: a.Status.Health.LastTransitionTime,
		ReconciledAt:         a.Status.ReconciledAt,
	}

	if a.Status.OperationState != nil {
		app.OperationState = a.Status.OperationState.Phase
		app.OperationPhase = a.Status.OperationState.Phase
		app.OperationStartedAt = a.Status.OperationState.StartedAt
		app.OperationFinishedAt = a.Status.OperationState.FinishedAt
		app.OperationMessage = a.Status.OperationState.Message
		// Check for per-resource sync failures in the operation result.
		if sr := a.Status.OperationState.SyncResult; sr != nil {
			for _, res := range sr.Resources {
				if res.Status == "SyncFailed" {
					app.HasSyncFailedResource = true
					break
				}
			}
		}
	}

	if a.Spec.Source.Helm != nil {
		for _, p := range a.Spec.Source.Helm.Parameters {
			app.SourceHelmParameters = append(app.SourceHelmParameters, models.HelmParameter{
				Name:  p.Name,
				Value: p.Value,
			})
		}
	}

	for _, h := range a.Status.History {
		app.History = append(app.History, models.AppHistoryEntry{
			ID:              h.ID,
			DeployedAt:      h.DeployedAt,
			DeployStartedAt: h.DeployStartedAt,
			Revision:        h.Revision,
		})
	}

	for _, r := range a.Status.Resources {
		res := models.AppResource{
			Group:     r.Group,
			Kind:      r.Kind,
			Namespace: r.Namespace,
			Name:      r.Name,
			Status:    r.Status,
		}
		if r.Health != nil {
			res.Health = r.Health.Status
			res.Message = r.Health.Message
		}
		app.Resources = append(app.Resources, res)
	}

	// Map conditions and override health if errors present
	hasError := false
	for _, c := range a.Status.Conditions {
		app.Conditions = append(app.Conditions, models.AppCondition{
			Type:    c.Type,
			Message: c.Message,
		})
		// ComparisonError, UnknownError, etc. mean the app is NOT healthy
		if strings.HasSuffix(c.Type, "Error") {
			hasError = true
		}
	}
	if hasError && (app.HealthStatus == "Healthy" || app.HealthStatus == "") {
		app.HealthStatus = "Error"
	}

	return app
}
