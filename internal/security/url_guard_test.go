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

// TestValidateExternalURL_RejectsGitTransportSchemes pins the Git transport
// family specifically, separately from the scheme test above.
//
// Why it is its own test rather than four more strings in that list: the
// schemes above (ftp, file, gopher, javascript) are all things nothing in
// Sharko can speak, so the guard is the only thing that would ever have
// stopped them and a hole there fails loudly. The Git transports are
// different. golang.org/x/crypto/ssh IS linked into the shipped binary — it
// arrives through internal/gitprovider -> code.gitea.io/sdk/gitea, and a
// symbol dump of the release binary counted 169 of its symbols. So an ssh://
// address is the one shape where "refused" and "quietly dialled by a library
// that is already in the build" are both physically possible outcomes, and
// the difference between them is this check.
//
// Before this test, every scheme-rejection case in the repository used ftp or
// file. The only ssh:// address in the whole test corpus was in
// tests/serverrender/bf11_addresses_test.go, and that one is refused for the
// credential in its userinfo, not for its scheme — so it would still pass if
// the scheme check here were deleted. That is a guard with nothing watching
// the direction that matters.
//
// The bare hostname form (git@host:org/repo.git) is not here because it is not
// a URL and never reaches this function; credsafe refuses it as unreadable,
// and bf11 already pins that.
func TestValidateExternalURL_RejectsGitTransportSchemes(t *testing.T) {
	resetAllowlistForTest()
	for _, raw := range []string{
		"ssh://git.example.com/org/repo",
		"ssh://git.example.com/org/repo.git",
		"git://git.example.com/org/repo.git",
		"git+ssh://git.example.com/org/repo",
	} {
		err := ValidateExternalURL(raw)
		if err == nil {
			t.Errorf("ValidateExternalURL(%q) = nil, want error: an SSH or Git transport address "+
				"must never reach the network layer, because x/crypto/ssh is linked into this binary "+
				"and could carry it", raw)
			continue
		}
		if !IsSSRFError(err) {
			t.Errorf("ValidateExternalURL(%q) returned non-SSRF error %T", raw, err)
			continue
		}
		// The refusal must be about the scheme. A DNS failure would also
		// return an error here and would look like a pass while the scheme
		// check was gone, which is exactly the shape of hole this test
		// exists to close.
		if !strings.Contains(err.Error(), "scheme must be http or https") {
			t.Errorf("ValidateExternalURL(%q) was refused, but not for its scheme: %v. "+
				"An address refused for some other reason does not prove the scheme rule is in force.", raw, err)
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
