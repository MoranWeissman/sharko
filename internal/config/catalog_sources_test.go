package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// setEnvs sets multiple env vars for the duration of the test via t.Setenv
// (automatically reverted by the test framework). Passing "" clears the var
// for the test run.
func setEnvs(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadCatalogSourcesFromEnv_Empty(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs:            "",
		EnvCatalogRefreshInterval: "",
		EnvCatalogAllowPrivate:    "",
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("expected no error on empty env, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(cfg.Sources))
	}
	if cfg.RefreshInterval != DefaultRefreshInterval {
		t.Errorf("expected default refresh %s, got %s", DefaultRefreshInterval, cfg.RefreshInterval)
	}
	if cfg.AllowPrivate {
		t.Error("expected AllowPrivate=false")
	}
}

func TestLoadCatalogSourcesFromEnv_SingleHTTPS(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs:            "https://catalogs.example.com/addons.yaml",
		EnvCatalogRefreshInterval: "",
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(cfg.Sources))
	}
	if got, want := cfg.Sources[0].URL, "https://catalogs.example.com/addons.yaml"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestLoadCatalogSourcesFromEnv_MultipleHTTPS(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs: "https://a.example.com/cat.yaml , https://b.example.com/cat.yaml,https://c.example.com/cat.yaml",
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 3 {
		t.Fatalf("expected 3 sources, got %d: %+v", len(cfg.Sources), cfg.Sources)
	}
	wantURLs := []string{
		"https://a.example.com/cat.yaml",
		"https://b.example.com/cat.yaml",
		"https://c.example.com/cat.yaml",
	}
	for i, want := range wantURLs {
		if cfg.Sources[i].URL != want {
			t.Errorf("Sources[%d] = %q, want %q", i, cfg.Sources[i].URL, want)
		}
	}
}

func TestLoadCatalogSourcesFromEnv_RejectsHTTP(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs: "http://catalogs.example.com/cat.yaml",
	})

	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected error for http:// URL, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS-only") {
		t.Errorf("error should mention HTTPS-only; got: %v", err)
	}
}

func TestLoadCatalogSourcesFromEnv_RejectsFileScheme(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs: "file:///etc/passwd",
	})

	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected error for file:// URL")
	}
	if !strings.Contains(err.Error(), "HTTPS-only") {
		t.Errorf("error should mention HTTPS-only; got: %v", err)
	}
}

func TestLoadCatalogSourcesFromEnv_RejectsMalformed(t *testing.T) {
	cases := []string{
		"not-a-url",
		"://no-scheme",
		"https://",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			setEnvs(t, map[string]string{EnvCatalogURLs: raw})
			_, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatalf("expected error for %q, got nil", raw)
			}
		})
	}
}

func TestLoadCatalogSourcesFromEnv_RejectsPrivateIPLiteral(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantSub string
	}{
		{"rfc1918_10", "https://10.0.0.1/cat.yaml", "SSRF"},
		{"rfc1918_172_16", "https://172.16.5.1/cat.yaml", "SSRF"},
		{"rfc1918_192_168", "https://192.168.1.1/cat.yaml", "SSRF"},
		{"loopback_v4", "https://127.0.0.1/cat.yaml", "SSRF"},
		{"loopback_hostname", "https://localhost/cat.yaml", "loopback"},
		{"link_local_v4", "https://169.254.169.254/meta", "SSRF"},
		{"loopback_v6", "https://[::1]/cat.yaml", "SSRF"},
		{"ula_v6", "https://[fc00::1]/cat.yaml", "SSRF"},
		{"link_local_v6", "https://[fe80::1]/cat.yaml", "SSRF"},
		{"unspecified", "https://0.0.0.0/cat.yaml", "SSRF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnvs(t, map[string]string{EnvCatalogURLs: tc.url})
			_, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatalf("expected error for %q", tc.url)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantSub)) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadCatalogSourcesFromEnv_AllowPrivateOptOut(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs:         "https://10.0.0.1/cat.yaml,https://127.0.0.1/cat.yaml",
		EnvCatalogAllowPrivate: "true",
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("expected no error with AllowPrivate=true, got %v", err)
	}
	if !cfg.AllowPrivate {
		t.Error("expected AllowPrivate=true")
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(cfg.Sources))
	}
}

func TestLoadCatalogSourcesFromEnv_AllowPrivateRejectsBogusValue(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogAllowPrivate: "yes-please",
	})
	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected error for bogus allow-private value")
	}
	if !strings.Contains(err.Error(), EnvCatalogAllowPrivate) {
		t.Errorf("error should reference env var; got: %v", err)
	}
}

func TestLoadCatalogSourcesFromEnv_Deduplicates(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs: strings.Join([]string{
			"https://catalogs.example.com/cat.yaml",
			"https://CATALOGS.example.com/cat.yaml",     // case variation on host
			"https://catalogs.example.com/cat.yaml/",    // trailing slash
			"https://catalogs.example.com:443/cat.yaml", // explicit default port
			"https://other.example.com/cat.yaml",        // genuine second entry
		}, ","),
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("expected 2 dedup'd sources, got %d: %+v", len(cfg.Sources), cfg.Sources)
	}
	if cfg.Sources[0].URL != "https://catalogs.example.com/cat.yaml" {
		t.Errorf("first source: %q", cfg.Sources[0].URL)
	}
	if cfg.Sources[1].URL != "https://other.example.com/cat.yaml" {
		t.Errorf("second source: %q", cfg.Sources[1].URL)
	}
}

func TestLoadCatalogSourcesFromEnv_TolerantOfStrayCommas(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogURLs: ",https://a.example.com/cat.yaml,,https://b.example.com/cat.yaml,",
	})
	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(cfg.Sources))
	}
}

func TestLoadCatalogSourcesFromEnv_RefreshIntervalOverride(t *testing.T) {
	setEnvs(t, map[string]string{
		EnvCatalogRefreshInterval: "30m",
	})

	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RefreshInterval != 30*time.Minute {
		t.Errorf("RefreshInterval = %s, want 30m", cfg.RefreshInterval)
	}
}

func TestLoadCatalogSourcesFromEnv_RefreshIntervalRejectsSubMinute(t *testing.T) {
	cases := []string{"0s", "30s", "59s"}
	for _, d := range cases {
		t.Run(d, func(t *testing.T) {
			setEnvs(t, map[string]string{EnvCatalogRefreshInterval: d})
			_, err := LoadCatalogSourcesFromEnv()
			if err == nil {
				t.Fatalf("expected error for %s", d)
			}
			if !strings.Contains(err.Error(), "below minimum") {
				t.Errorf("error should explain minimum bound; got: %v", err)
			}
		})
	}
}

func TestLoadCatalogSourcesFromEnv_RefreshIntervalRejectsAbove24h(t *testing.T) {
	setEnvs(t, map[string]string{EnvCatalogRefreshInterval: "48h"})
	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected error for 48h")
	}
	if !strings.Contains(err.Error(), "above maximum") {
		t.Errorf("error should explain maximum bound; got: %v", err)
	}
}

func TestLoadCatalogSourcesFromEnv_RefreshIntervalRejectsGarbage(t *testing.T) {
	setEnvs(t, map[string]string{EnvCatalogRefreshInterval: "not-a-duration"})
	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected error for unparseable duration")
	}
}

// TestLoadCatalogSourcesFromEnv_HostnameResolvesToPrivate exercises the
// hostname-based SSRF guard by stubbing lookupHostFn. This is the case
// flagged as "tricky" in the story spec — covered here because the stub
// is small and keeps the check hermetic.
func TestLoadCatalogSourcesFromEnv_HostnameResolvesToPrivate(t *testing.T) {
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })

	lookupHostFn = func(host string) ([]string, error) {
		if host == "private-host.example.invalid" {
			return []string{"10.4.5.6"}, nil
		}
		return nil, errors.New("unexpected lookup " + host)
	}

	setEnvs(t, map[string]string{
		EnvCatalogURLs: "https://private-host.example.invalid/cat.yaml",
	})
	_, err := LoadCatalogSourcesFromEnv()
	if err == nil {
		t.Fatal("expected SSRF error for hostname resolving to RFC1918")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("error should mention SSRF; got: %v", err)
	}
}

// TestLoadCatalogSourcesFromEnv_HostnameResolvesToPublic confirms the
// positive path through the stubbed resolver: a public IP passes the guard.
func TestLoadCatalogSourcesFromEnv_HostnameResolvesToPublic(t *testing.T) {
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })

	lookupHostFn = func(host string) ([]string, error) {
		if host == "catalogs.example.com" {
			return []string{"203.0.113.10"}, nil
		}
		return nil, errors.New("unexpected lookup " + host)
	}

	setEnvs(t, map[string]string{
		EnvCatalogURLs: "https://catalogs.example.com/cat.yaml",
	})
	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(cfg.Sources))
	}
}

// TestLoadCatalogSourcesFromEnv_DNSFailureIsFailOpen documents the
// deliberate design choice: DNS errors at startup DO NOT block config
// load (the runtime fetcher will retry). SSRF-positive matches do block.
func TestLoadCatalogSourcesFromEnv_DNSFailureIsFailOpen(t *testing.T) {
	origLookup := lookupHostFn
	t.Cleanup(func() { lookupHostFn = origLookup })

	lookupHostFn = func(host string) ([]string, error) {
		return nil, errors.New("dns unreachable")
	}

	setEnvs(t, map[string]string{
		EnvCatalogURLs: "https://some-domain.example.com/cat.yaml",
	})
	cfg, err := LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("DNS failure should not block config load, got %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(cfg.Sources))
	}
}
