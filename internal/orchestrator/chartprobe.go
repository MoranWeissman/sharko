// Package orchestrator — the init preflight chart probe (sprint-backlog-r1
// Story A1).
//
// InitRepo and CollectBootstrapFiles both write sharko-engine.yaml (the
// engine pin) pinning THIS build's engine chart version
// (internal/engineversion.BundledVersion) at an OCI registry
// (GitOpsConfig.EngineChartRepoURL, or DefaultEngineChartRepoURL). Neither
// used to check that version was actually published before opening the
// bootstrap PR. The live failure that motivated this file: the pin said
// 0.2.0, the registry only had 0.1.0 published — the seed PR opened
// anyway, and the user only found out something was wrong once
// ArgoCD-side bootstrap failed with a confusing, disconnected error.
//
// probeEnginePinChart closes that gap: it asks the OCI registry, before
// any git write happens, "does this exact chart:version exist?" — using
// the same anonymous (unauthenticated) pull path ArgoCD's own repo-server
// takes for a public chart. No credentials are read or required.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/engineversion"
)

// ChartProbeFunc checks whether chart:version is published and pullable at
// registryURL. It returns nil when the version is confirmed present, and a
// non-nil, plain-English error otherwise — the error text is safe to
// surface to a user verbatim (see chartVersionNotPublishedError /
// chartRegistryUnreachableError). Orchestrator.chartProbeFn is this type;
// New() defaults it to the real, network-backed probeOCIChartVersion, and
// tests override it via SetChartProbe to avoid any network access
// (go-expert.md per-instance test-seam convention — default real,
// override in test).
type ChartProbeFunc func(ctx context.Context, registryURL, chart, version string) error

// chartProbeTimeout bounds the whole probe (manifest request plus, when
// needed, the anonymous token round trip) so an unreachable registry can
// never hang InitRepo/CollectBootstrapFiles indefinitely. A few seconds is
// enough for a same-region OCI registry; this is a preflight check, not a
// pull of the chart itself.
const chartProbeTimeout = 8 * time.Second

// probeEnginePinChart is the method InitRepo and CollectBootstrapFiles
// call before writing anything: it resolves the same registry URL
// buildEnginePin would write into the pin (effectiveEngineChartRepoURL)
// and the same chart name + version the pin would name
// (internal/engineversion), and asks o.chartProbeFn to confirm that exact
// chart:version is published. A nil chartProbeFn (should not happen once
// New() has run, but is defensive against a zero-value Orchestrator in a
// test) falls back to the real network probe.
func (o *Orchestrator) probeEnginePinChart(ctx context.Context) error {
	probe := o.chartProbeFn
	if probe == nil {
		probe = probeOCIChartVersion
	}
	registryURL := effectiveEngineChartRepoURL(o.gitops)
	return probe(ctx, registryURL, engineversion.BundledChartName, engineversion.BundledVersion)
}

// probeOCIChartVersion is the real, network-backed ChartProbeFunc wired by
// New(). registryURL is host+path-prefix with no scheme (the same shape
// GitOpsConfig.EngineChartRepoURL / DefaultEngineChartRepoURL and
// buildEnginePin's repoURL field use, e.g. "ghcr.io/moranweissman/sharko");
// chart and version name the OCI reference's repository and tag
// (e.g. "sharko-engine", "0.4.0" — no "v" prefix, matching the release
// pipeline's `helm push` tag, .github/workflows/release.yml).
func probeOCIChartVersion(ctx context.Context, registryURL, chart, version string) error {
	host, prefix := splitOCIRegistryHost(registryURL)
	repoPath := chart
	if prefix != "" {
		repoPath = prefix + "/" + chart
	}
	return probeManifestAt(ctx, "https://"+host, repoPath, version, chart, registryURL)
}

// probeManifestAt is the scheme-flexible core of the probe, split out from
// probeOCIChartVersion so tests can point it at a local httptest server
// (http://127.0.0.1:port) instead of faking TLS. chartForError /
// registryForError are only used to build the plain-English error text —
// they never affect where the request goes.
//
// This performs the OCI Distribution API v2 manifest check
// (GET <baseURL>/v2/<repoPath>/manifests/<version>) and, if the registry
// answers 401 with a Bearer challenge (GHCR and most public OCI registries
// require this even for anonymous, unauthenticated pulls of public
// content — the exact path Helm/ArgoCD's own OCI puller takes), fetches a
// short-lived anonymous token from the realm named in the challenge and
// retries once. GET is used rather than HEAD because HEAD support is not
// universal across OCI-compatible registries; a manifest GET is cheap
// (kilobytes, not the chart itself).
func probeManifestAt(ctx context.Context, baseURL, repoPath, version, chartForError, registryForError string) error {
	probeCtx, cancel := context.WithTimeout(ctx, chartProbeTimeout)
	defer cancel()

	client := &http.Client{Timeout: chartProbeTimeout}
	manifestURL := baseURL + "/v2/" + repoPath + "/manifests/" + version

	resp, err := doManifestRequest(probeCtx, client, manifestURL, "")
	if err != nil {
		return chartRegistryUnreachableError(chartForError, version, registryForError, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		token, tokErr := fetchAnonymousToken(probeCtx, client, challenge)
		if tokErr != nil {
			return chartRegistryUnreachableError(chartForError, version, registryForError, tokErr)
		}
		resp2, err2 := doManifestRequest(probeCtx, client, manifestURL, token)
		if err2 != nil {
			return chartRegistryUnreachableError(chartForError, version, registryForError, err2)
		}
		defer resp2.Body.Close()
		resp = resp2
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return chartVersionNotPublishedError(chartForError, version, registryForError)
	default:
		return chartRegistryUnreachableError(chartForError, version, registryForError,
			fmt.Errorf("registry returned HTTP %d", resp.StatusCode))
	}
}

// doManifestRequest issues one manifest GET, optionally bearing an
// anonymous token obtained from fetchAnonymousToken.
func doManifestRequest(ctx context.Context, client *http.Client, manifestURL, bearerToken string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	// Accept every manifest media type a Helm OCI chart or its index might
	// be served as — we only care whether the registry has an entry at
	// this tag, not which exact manifest schema it uses.
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ", "))
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return client.Do(req)
}

// fetchAnonymousToken implements the OCI/Docker Registry v2 anonymous
// bearer-token challenge-response: a 401 response carries a
// WWW-Authenticate header naming a realm, service, and scope; the client
// fetches a short-lived token from that realm with no credentials
// supplied, then the caller retries the original request with
// "Authorization: Bearer <token>". No registry credentials are read or
// required — this is exactly the anonymous path a public chart pull takes.
func fetchAnonymousToken(ctx context.Context, client *http.Client, challenge string) (string, error) {
	realm, params, ok := parseBearerChallenge(challenge)
	if !ok {
		return "", fmt.Errorf("registry did not present a recognizable bearer-token challenge")
	}

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parsing token realm %q: %w", realm, err)
	}
	q := tokenURL.Query()
	for k, v := range params {
		if k == "realm" {
			continue
		}
		q.Set(k, v)
	}
	tokenURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", fmt.Errorf("token endpoint response carried no token")
}

// parseBearerChallenge parses a WWW-Authenticate header of the form
// `Bearer realm="...",service="...",scope="..."` into the realm URL and
// the full set of key=value parameters (used verbatim as query params on
// the token request).
func parseBearerChallenge(header string) (realm string, params map[string]string, ok bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", nil, false
	}
	params = map[string]string{}
	for _, part := range splitChallengeParams(strings.TrimPrefix(header, prefix)) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		params[key] = val
	}
	realm, ok = params["realm"]
	return realm, params, ok && realm != ""
}

// splitChallengeParams splits a comma-separated list of key="value" pairs,
// respecting commas that appear inside quoted values.
func splitChallengeParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch r {
		case '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case ',':
			if inQuotes {
				cur.WriteRune(r)
			} else {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitOCIRegistryHost splits a registry path like
// "ghcr.io/moranweissman/sharko" (no scheme, host + path prefix combined —
// the shape GitOpsConfig.EngineChartRepoURL / DefaultEngineChartRepoURL
// and the pin's own repoURL field use) into the bare host ("ghcr.io") and
// the path prefix ("moranweissman/sharko") the OCI Distribution API needs
// as separate pieces of the manifest URL.
func splitOCIRegistryHost(registryURL string) (host, prefix string) {
	registryURL = strings.TrimPrefix(registryURL, "oci://")
	registryURL = strings.TrimSuffix(registryURL, "/")
	parts := strings.SplitN(registryURL, "/", 2)
	host = parts[0]
	if len(parts) == 2 {
		prefix = parts[1]
	}
	return host, prefix
}

// chartVersionNotPublishedError is returned when the registry positively
// answered "no such tag" (HTTP 404) — the registry is reachable, it just
// does not have this version. Names chart, version, registry, and what to
// do, verbatim-safe to surface to a user.
func chartVersionNotPublishedError(chart, version, registryURL string) error {
	return fmt.Errorf(
		"version %s of the %s chart is not published at %s — publish it or pin a published version",
		version, chart, registryURL)
}

// chartRegistryUnreachableError is returned for every failure mode that is
// NOT a confirmed 404 — DNS failure, connection refused, timeout, an
// unexpected HTTP status, or a broken auth challenge. It deliberately does
// NOT claim "not published": the registry may well have the version, we
// simply could not confirm it, and a seed PR pointing at an unverifiable
// chart is exactly the thing this preflight exists to prevent, so it
// refuses just the same as a confirmed 404 but says so honestly.
func chartRegistryUnreachableError(chart, version, registryURL string, cause error) error {
	return fmt.Errorf(
		"could not confirm %s chart version %s is published at %s: registry unreachable or timed out (%v) — "+
			"init is refusing rather than open a pull request for a chart version nobody could verify exists; check network access to the registry and retry",
		chart, version, registryURL, cause)
}
