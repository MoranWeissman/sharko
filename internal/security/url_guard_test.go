package security

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateExternalURL_RejectsBadScheme(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"ftp://example.com/file",
		"file:///etc/passwd",
		"gopher://nope",
		"javascript:alert(1)",
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want error", raw)
			continue
		}
		if !IsSSRFError(err) {
			t.Errorf("ValidateExternalURL(%q) returned non-SSRF error %T", raw, err)
		}
	}
}

func TestValidateExternalURL_RejectsMalformed(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"://nohost",
		"http://",
	} {
		if err := ValidateExternalURL(raw); err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want error", raw)
		}
	}
}

func TestValidateExternalURL_BlocksLoopback(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"http://127.0.0.1",
		"http://127.0.0.1:8080/api",
		"http://[::1]/x",
		"http://localhost", // resolves to loopback
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want SSRF error", raw)
			continue
		}
		var s *SSRFError
		if !errors.As(err, &s) {
			t.Fatalf("expected SSRFError, got %T", err)
		}
		if !strings.Contains(s.Reason, "loopback") && !strings.Contains(s.Reason, "private") {
			t.Errorf("Reason = %q, want loopback-related", s.Reason)
		}
	}
}

func TestValidateExternalURL_BlocksRFC1918(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"http://10.0.0.1",
		"http://10.255.255.255/",
		"http://172.16.0.1",
		"http://172.31.255.255/x",
		"http://192.168.1.1",
		"http://192.168.0.0:9999",
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want SSRF error", raw)
			continue
		}
		var s *SSRFError
		if !errors.As(err, &s) {
			t.Fatalf("expected SSRFError, got %T", err)
		}
		if s.Reason != "private_net" {
			t.Errorf("Reason = %q, want private_net", s.Reason)
		}
	}
}

func TestValidateExternalURL_BlocksLinkLocal(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"http://169.254.169.254", // AWS / GCP metadata
		"http://[fe80::1]",
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want SSRF error", raw)
			continue
		}
		var s *SSRFError
		if !errors.As(err, &s) {
			t.Fatalf("expected SSRFError, got %T", err)
		}
		if s.Reason != "link_local" {
			t.Errorf("Reason = %q, want link_local", s.Reason)
		}
	}
}

func TestValidateExternalURL_BlocksIPv6ULA(t *testing.T) {
	resetAllowlistForTest()
	err := ValidateExternalURL("http://[fc00::1]")
	if err == nil {
		t.Fatal("ULA address allowed, want SSRF error")
	}
	var s *SSRFError
	if !errors.As(err, &s) {
		t.Fatalf("expected SSRFError, got %T", err)
	}
	if s.Reason != "private_net" {
		t.Errorf("Reason = %q, want private_net", s.Reason)
	}
}

func TestValidateExternalURL_AllowsPublicLiterals(t *testing.T) {
	resetAllowlistForTest()
	// 8.8.8.8 / 1.1.1.1 are guaranteed public IP literals — no DNS lookup
	// happens, so the test is offline-safe.
	for _, raw := range []string{
		"http://8.8.8.8",
		"https://1.1.1.1/path",
		"https://1.1.1.1:443",
	} {
		if err := ValidateExternalURL(raw); err != nil {
			t.Errorf("ValidateExternalURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestValidateExternalURL_AllowlistEnforced(t *testing.T) {
	t.Setenv("SHARKO_URL_ALLOWLIST", "charts.jetstack.io,1.1.1.1")
	resetAllowlistForTest()
	t.Cleanup(resetAllowlistForTest)

	if err := ValidateExternalURL("https://1.1.1.1/x"); err != nil {
		t.Errorf("allowlisted host rejected: %v", err)
	}
	err := ValidateExternalURL("https://8.8.8.8/")
	if err == nil {
		t.Fatal("non-allowlisted host accepted, want SSRF error")
	}
	var s *SSRFError
	if !errors.As(err, &s) {
		t.Fatalf("expected SSRFError, got %T", err)
	}
	if s.Reason != "not_in_allowlist" {
		t.Errorf("Reason = %q, want not_in_allowlist", s.Reason)
	}
}

func TestValidateExternalURL_AllowlistEmptyMeansAllowAll(t *testing.T) {
	t.Setenv("SHARKO_URL_ALLOWLIST", "")
	resetAllowlistForTest()
	t.Cleanup(resetAllowlistForTest)
	if err := ValidateExternalURL("https://8.8.8.8/"); err != nil {
		t.Errorf("empty allowlist rejected public IP: %v", err)
	}
}

func TestIsSSRFError_OnlyMatchesSSRF(t *testing.T) {
	if IsSSRFError(errors.New("plain")) {
		t.Error("IsSSRFError(plain) = true, want false")
	}
	if !IsSSRFError(&SSRFError{URL: "x", Reason: "host"}) {
		t.Error("IsSSRFError(SSRFError) = false, want true")
	}
}
