// Package config — catalog source parsing.
//
// This file implements the env-var-driven parser for the third-party catalog
// URLs added in v1.23 (Subsystem A of docs/design/2026-04-20-v1.23-catalog-extensibility.md).
//
// The parser reads two env vars:
//
//   SHARKO_CATALOG_URLS             — comma-separated list of HTTPS URLs
//   SHARKO_CATALOG_REFRESH_INTERVAL — Go duration format, default 1h
//
// Validation rules (rejections produce a startup error — operator must fix
// the env and restart):
//
//  1. Scheme must be exactly "https". `http://`, `file://`, etc. are rejected.
//  2. URL must be well-formed and carry a host.
//  3. SSRF guard: the resolved host must not be in a private, loopback,
//     link-local, unspecified, or IPv6 unique-local range. Resolution is
//     done via net.LookupHost + net/netip classification. The guard can
//     be disabled by setting SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true
//     (for home-lab / dev scenarios — documented as unsafe on untrusted
//     networks).
//  4. Duplicates (case-insensitive host, trailing-slash-normalized path)
//     are collapsed to a single entry.
//
// Refresh interval bounds: minimum 1m (avoid hammering upstreams), maximum
// 24h (keep freshness sane). Default 1h when unset.
//
// Consumers read *CatalogSourcesConfig and build a fetch loop. This
// package is intentionally stateless — it parses once at startup and
// returns an immutable config.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
)

// CatalogSource is a single third-party catalog URL configured by the
// operator. Additional fields (e.g. optional sidecar URL, auth token ref)
// may be added later without breaking existing consumers — the contract
// here is intentionally minimal.
type CatalogSource struct {
	// URL is the canonical form of the HTTPS URL (lower-cased host,
	// trailing slash on bare paths stripped).
	URL string
}

// CatalogSourcesConfig is the parsed view of the SHARKO_CATALOG_* env set.
// An empty Sources slice means "no third-party catalogs configured — use the
// embedded catalog only"; it is NOT an error state.
type CatalogSourcesConfig struct {
	// Sources is the deduplicated list of configured URLs, in the order
	// they first appeared in the env var.
	Sources []CatalogSource

	// RefreshInterval is how often the fetcher should re-pull each source.
	// Bounded to [MinRefreshInterval, MaxRefreshInterval]. Defaults to
	// DefaultRefreshInterval when the env var is unset.
	RefreshInterval time.Duration

	// AllowPrivate records whether the SSRF guard was disabled via
	// SHARKO_CATALOG_URLS_ALLOW_PRIVATE. Consumers may use it for extra
	// logging / UI warnings; it has no functional effect after parsing
	// (enforcement happened during Load).
	AllowPrivate bool
}

// MarketplaceSourcesSpec is the spec body of an enveloped marketplace-sources.yaml.
// It holds the list of third-party catalog source URLs (V3-Phase-3 GitOps-native
// catalog sources). The env-var path remains available for tokened/private URLs
// that must not be committed to Git.
type MarketplaceSourcesSpec struct {
	// Sources is the list of catalog source URLs. Each source must be HTTPS
	// and will be validated against the same SSRF guards as env-sourced URLs.
	Sources []struct {
		URL string `json:"url" yaml:"url"`
	} `json:"sources" yaml:"sources"`

	// RefreshInterval is an optional Go duration string (e.g., "30m", "2h").
	// Defaults to 1h when absent. Must be between 1m and 24h.
	RefreshInterval string `json:"refreshInterval,omitempty" yaml:"refreshInterval,omitempty"`
}

// Env var names (exported so tests + docs have a single source of truth).
const (
	EnvCatalogURLs            = "SHARKO_CATALOG_URLS"
	EnvCatalogRefreshInterval = "SHARKO_CATALOG_REFRESH_INTERVAL"
	EnvCatalogAllowPrivate    = "SHARKO_CATALOG_URLS_ALLOW_PRIVATE"
)

// Refresh interval bounds.
const (
	DefaultRefreshInterval = 1 * time.Hour
	MinRefreshInterval     = 1 * time.Minute
	MaxRefreshInterval     = 24 * time.Hour
)

// lookupHostFn is a package var so tests can stub DNS resolution.
// Production always uses net.LookupHost.
var lookupHostFn = net.LookupHost

// LoadCatalogSourcesFromEnv parses the SHARKO_CATALOG_* env vars into a
// *CatalogSourcesConfig.
//
// Returns (empty-config, nil) when SHARKO_CATALOG_URLS is unset or empty —
// the caller should treat that as "embedded catalog only, no fetch loop".
//
// Returns (nil, error) when any URL fails validation or the refresh
// interval is out of bounds. Callers should log the error and exit
// non-zero; a broken catalog-sources config is a misconfiguration, not a
// runtime fault to silently skip.
func LoadCatalogSourcesFromEnv() (*CatalogSourcesConfig, error) {
	raw := strings.TrimSpace(os.Getenv(EnvCatalogURLs))
	allowPrivate, err := parseAllowPrivate(os.Getenv(EnvCatalogAllowPrivate))
	if err != nil {
		return nil, err
	}

	interval, err := parseRefreshInterval(os.Getenv(EnvCatalogRefreshInterval))
	if err != nil {
		return nil, err
	}

	cfg := &CatalogSourcesConfig{
		RefreshInterval: interval,
		AllowPrivate:    allowPrivate,
	}

	if raw == "" {
		// No third-party sources — embedded-only mode. Not an error.
		return cfg, nil
	}

	seen := make(map[string]struct{})
	for piece := range strings.SplitSeq(raw, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			// Tolerate stray commas (e.g. "a,,b" or trailing ",").
			continue
		}
		canon, err := validateAndCanonicalize(piece, allowPrivate, lookupHostFn)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvCatalogURLs, err)
		}
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		cfg.Sources = append(cfg.Sources, CatalogSource{URL: canon})
	}

	return cfg, nil
}

// parseAllowPrivate reads the opt-out env var. Empty = false. Accepts the
// standard truthy strings Go's strconv.ParseBool accepts ("true", "1",
// "t", "TRUE", etc.). Anything non-parseable is a startup error so the
// operator notices the typo before shipping to prod.
func parseAllowPrivate(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s=%q: must be true/false (got unparseable value): %w",
			EnvCatalogAllowPrivate, raw, err)
	}
	return b, nil
}

// parseRefreshInterval enforces the [MinRefreshInterval, MaxRefreshInterval]
// bounds and returns DefaultRefreshInterval for empty input.
func parseRefreshInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultRefreshInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: invalid Go duration (e.g. 30m, 1h): %w",
			EnvCatalogRefreshInterval, raw, err)
	}
	if d < MinRefreshInterval {
		return 0, fmt.Errorf("%s=%s: below minimum %s (sub-minute refresh would hammer upstreams)",
			EnvCatalogRefreshInterval, d, MinRefreshInterval)
	}
	if d > MaxRefreshInterval {
		return 0, fmt.Errorf("%s=%s: above maximum %s (staler than 24h defeats the refresh loop)",
			EnvCatalogRefreshInterval, d, MaxRefreshInterval)
	}
	return d, nil
}

// validateAndCanonicalize runs the full validation pipeline on a single URL
// and returns its canonical form.
func validateAndCanonicalize(raw string, allowPrivate bool, lookupHost func(string) ([]string, error)) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%q: malformed URL: %w", raw, err)
	}
	if u.Scheme == "" {
		return "", fmt.Errorf("%q: missing scheme (HTTPS-only required)", raw)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("%q: scheme %q not allowed (HTTPS-only; http/file/other schemes are rejected to avoid plaintext catalog pulls)", raw, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%q: missing host", raw)
	}

	// Defensive: block magic hostnames regardless of DNS. "localhost" on
	// most systems resolves to 127.0.0.1 via hosts file + LookupHost
	// anyway, but we short-circuit it so tests + constrained
	// environments without /etc/hosts still behave predictably.
	if !allowPrivate && isMagicLocalHostname(host) {
		return "", fmt.Errorf("%q: host %q is loopback — rejected by SSRF guard (set %s=true to override)",
			raw, host, EnvCatalogAllowPrivate)
	}

	if !allowPrivate {
		if err := checkSSRF(host, lookupHost); err != nil {
			return "", fmt.Errorf("%q: %w (set %s=true to override — only safe on trusted networks)",
				raw, err, EnvCatalogAllowPrivate)
		}
	}

	return canonicalize(u), nil
}

// isMagicLocalHostname flags hostnames that conventionally resolve to
// loopback but wouldn't be caught by a net.LookupHost stub in tests.
func isMagicLocalHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" {
		return true
	}
	if strings.HasSuffix(h, ".localhost") {
		return true
	}
	return false
}

// checkSSRF resolves the host (literal IP or DNS) and fails if any resulting
// address is private, loopback, link-local, unspecified, or IPv6 ULA.
func checkSSRF(host string, lookupHost func(string) ([]string, error)) error {
	// Literal IP case — skip DNS.
	if addr, err := netip.ParseAddr(host); err == nil {
		if isPrivateAddr(addr) {
			return fmt.Errorf("host %s resolves to private/loopback/link-local address %s — rejected by SSRF guard", host, addr)
		}
		return nil
	}

	// Hostname case — resolve via DNS (or test stub) and check every IP.
	ips, err := lookupHost(host)
	if err != nil {
		// DNS failure is NOT an SSRF rejection — the fetcher can retry at
		// runtime. Fail-open on lookup error here; real fetch attempts
		// will hit the same resolver.
		return nil
	}
	for _, ip := range ips {
		addr, parseErr := netip.ParseAddr(ip)
		if parseErr != nil {
			continue
		}
		if isPrivateAddr(addr) {
			return fmt.Errorf("host %s resolves to private/loopback/link-local address %s — rejected by SSRF guard", host, addr)
		}
	}
	return nil
}

// isPrivateAddr returns true for any address the SSRF guard should block.
// Covers (per stdlib netip classification):
//   - RFC1918 IPv4 private ranges (10/8, 172.16/12, 192.168/16) and
//     IPv6 ULA fc00::/7            — via Addr.IsPrivate
//   - Loopback (127/8, ::1)         — via Addr.IsLoopback
//   - Link-local (169.254/16, fe80::/10) — via Addr.IsLinkLocalUnicast
//   - Unspecified (0.0.0.0, ::)     — via Addr.IsUnspecified (defense in depth)
//   - IPv4-in-IPv6 forms are unwrapped so IPv6-mapped private ranges also match.
func isPrivateAddr(addr netip.Addr) bool {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsUnspecified()
}

// canonicalize returns a deduplicatable form of the URL:
//   - scheme lower-cased
//   - host lower-cased
//   - port preserved only if non-default
//   - query + fragment stripped (meaningless for a YAML pull)
//   - trailing slash on a bare-root path preserved; other paths have their
//     trailing slash stripped so "/cat.yaml" and "/cat.yaml/" collapse.
func canonicalize(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()

	var hostport string
	if port == "" || port == "443" {
		hostport = host
	} else {
		hostport = host + ":" + port
	}

	path := u.EscapedPath()
	if path == "" {
		path = "/"
	} else if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}

	return scheme + "://" + hostport + path
}

// MarketplaceSourcesPath is the canonical git path for the marketplace sources file.
const MarketplaceSourcesPath = "configuration/marketplace-sources.yaml"

// LoadMarketplaceSourcesFromFile parses a marketplace-sources.yaml body
// (enveloped) and returns a *CatalogSourcesConfig with the same shape as
// LoadCatalogSourcesFromEnv, so downstream fetcher wiring is unchanged.
//
// The file must be enveloped (apiVersion: sharko.dev/v1, kind:
// MarketplaceSources). Validation is schema-based via the runtime
// validator; the same SSRF + scheme checks that guard env-sourced URLs
// apply to file-sourced URLs. Deduplication is applied (case-insensitive
// host, trailing-slash-normalized path).
//
// Returns (nil, error) on validation or parsing failures. An empty
// sources list in the file is valid and returns a non-nil config with
// empty Sources slice.
//
// IMPORTANT TOKEN-LEAK GUARD: catalog URLs may encode an auth token in
// the path (e.g., https://catalogs.example.com/private/<token>/cat.yaml).
// A committed gitops file with such a URL would leak the token into Git.
// This file reader is intended for PUBLIC / TOKENLESS source URLs only.
// Tokened / private sources should remain in the SHARKO_CATALOG_URLS env
// fallback. The code does NOT log file-sourced URLs for this reason.
func LoadMarketplaceSourcesFromFile(body []byte) (*CatalogSourcesConfig, error) {
	return loadMarketplaceSourcesFromFileImpl(body, lookupHostFn)
}

// loadMarketplaceSourcesFromFileImpl is the testable core of
// LoadMarketplaceSourcesFromFile, with a stub-able DNS resolver.
func loadMarketplaceSourcesFromFileImpl(body []byte, lookupHost func(string) ([]string, error)) (*CatalogSourcesConfig, error) {
	// Check envelope.
	enveloped, err := schema.IsEnveloped(body)
	if err != nil {
		return nil, fmt.Errorf("checking envelope: %w", err)
	}
	if !enveloped {
		return nil, fmt.Errorf("marketplace-sources.yaml must be enveloped (apiVersion: sharko.dev/v1, kind: MarketplaceSources)")
	}

	// Validate against schema.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindMarketplaceSources, body); err != nil {
			return nil, fmt.Errorf("validating marketplace-sources.yaml: %w", err)
		}
	}

	// Parse the envelope.
	var doc schema.Envelope[MarketplaceSourcesSpec]
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("unmarshalling marketplace-sources.yaml: %w", err)
	}
	if doc.Kind != schema.KindMarketplaceSources {
		return nil, fmt.Errorf("wrong kind %q, expected %q", doc.Kind, schema.KindMarketplaceSources)
	}

	// Parse refresh interval from spec (or default).
	interval, err := parseRefreshInterval(doc.Spec.RefreshInterval)
	if err != nil {
		return nil, err
	}

	// AllowPrivate is NOT exposed in the file format — the file is for
	// public URLs only. Tokened URLs stay in the env var. We pass
	// allowPrivate=false to the validation pipeline below so the SSRF
	// guard rejects private IPs.
	allowPrivate := false

	cfg := &CatalogSourcesConfig{
		RefreshInterval: interval,
		AllowPrivate:    allowPrivate,
	}

	// Validate and canonicalize each source URL, reusing the SAME
	// validation pipeline as the env loader (no fork).
	seen := make(map[string]struct{})
	for _, src := range doc.Spec.Sources {
		raw := strings.TrimSpace(src.URL)
		if raw == "" {
			// Tolerate empty entries (YAML array with empty string).
			continue
		}
		canon, err := validateAndCanonicalize(raw, allowPrivate, lookupHost)
		if err != nil {
			return nil, fmt.Errorf("marketplace-sources.yaml: source URL %q: %w", raw, err)
		}
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		cfg.Sources = append(cfg.Sources, CatalogSource{URL: canon})
	}

	return cfg, nil
}
