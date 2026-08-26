package credsafe

import (
	"errors"
	"strings"
	"testing"
)

// The Sharko server address rule must say yes and no to exactly what the
// repository-address rule says yes and no to — it is the same structural test
// underneath, and a divergence here would mean a second classifier had grown.
func TestServerAddressAgreesWithTheRepositoryRule(t *testing.T) {
	for _, raw := range []string{
		"",
		"https://sharko.example",
		"https://sharko.example:8443/base",
		"http://localhost:8080",
		"localhost:8080",
		"https://token@sharko.example",
		"https://user:pass@sharko.example",
		"https://sharko.example?access_token=abc",
		"https://sharko.example?ref=main",
		"https://sharko.example#frag",
		"git@host:org/repo.git",
	} {
		t.Run(raw, func(t *testing.T) {
			repoErr := ValidateSupportedRepoURL(raw)
			srvErr := ValidateServerAddress(raw)
			if (repoErr == nil) != (srvErr == nil) {
				t.Fatalf("the two rules disagree about %q: repo=%v server=%v — they must share "+
					"ClassifyAddress, so a disagreement means a second classifier exists", raw, repoErr, srvErr)
			}
		})
	}
}

func TestValidateServerAddress_RefusesEveryCarrier(t *testing.T) {
	for name, raw := range map[string]string{
		"username slot":  "https://tok@sharko.example",
		"password slot":  "https://x-access-token:tok@sharko.example",
		"query string":   "https://sharko.example?access_token=tok",
		"fragment":       "https://sharko.example#tok",
		"ordinary query": "https://sharko.example?ref=main",
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateServerAddressAt("--server", raw)
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
			if !errors.Is(err, ErrServerAddressUnsupported) {
				t.Errorf("refused, but not with the sentinel: %v", err)
			}
			var typed *UnsupportedServerAddressError
			if !errors.As(err, &typed) {
				t.Fatalf("refused with the wrong type: %T", err)
			}
			if typed.Setting != "--server" {
				t.Errorf("Setting = %q, want %q", typed.Setting, "--server")
			}
			if strings.Contains(err.Error(), "tok") || strings.Contains(err.Error(), "sharko.example") {
				t.Errorf("the refusal carries part of the value: %q", err.Error())
			}
		})
	}
}

func TestValidateServerAddress_AcceptsOrdinaryAddresses(t *testing.T) {
	for _, raw := range []string{
		"",
		"https://sharko.example",
		"https://sharko.example:8443",
		"https://sharko.example/base/path",
		"http://127.0.0.1:8080",
		"localhost:8080",
	} {
		if err := ValidateServerAddress(raw); err != nil {
			t.Errorf("%q was refused: %v", raw, err)
		}
	}
}

// The untagged sentinel and a tagged refusal must render differently — the
// tagged one names the setting — but both must match errors.Is.
func TestUnsupportedServerAddressError_Rendering(t *testing.T) {
	bare := (&UnsupportedServerAddressError{}).Error()
	if bare != UnsupportedServerAddressMessage {
		t.Errorf("bare refusal = %q, want the plain message", bare)
	}
	tagged := (&UnsupportedServerAddressError{Setting: "--server"}).Error()
	if !strings.HasPrefix(tagged, "--server — ") {
		t.Errorf("tagged refusal does not lead with the setting: %q", tagged)
	}
	if !strings.HasSuffix(tagged, UnsupportedServerAddressMessage) {
		t.Errorf("tagged refusal does not carry the message: %q", tagged)
	}
	if !errors.Is(&UnsupportedServerAddressError{Setting: "x"}, ErrServerAddressUnsupported) {
		t.Error("a tagged refusal does not match the sentinel")
	}
}

// SafeServerAddressPhrase must never return the empty string, or a sentence
// built around it stops mid-air.
func TestSafeServerAddressPhrase_NeverEmpty(t *testing.T) {
	for _, raw := range []string{
		"",
		"git@host:org/repo.git",
		"://nonsense",
		"https://tok@sharko.example",
		"https://sharko.example",
	} {
		got := SafeServerAddressPhrase(raw)
		if got == "" {
			t.Errorf("SafeServerAddressPhrase(%q) returned the empty string", raw)
		}
		if strings.Contains(got, "tok@") {
			t.Errorf("SafeServerAddressPhrase(%q) kept the userinfo: %q", raw, got)
		}
	}
	// An ordinary address must reach the screen byte-for-byte as the
	// operator wrote it — including the scheme-less form, which is what a
	// local development config usually holds.
	for _, ordinary := range []string{
		"https://sharko.example:8443/base",
		"http://127.0.0.1:8080",
		"localhost:8080",
	} {
		if got := SafeServerAddressPhrase(ordinary); got != ordinary {
			t.Errorf("an ordinary address was changed on the way to the screen: got %q, want %q", got, ordinary)
		}
	}
	if got := SafeServerAddressPhrase(""); got != UnnamedServerPhrase {
		t.Errorf("empty address = %q, want %q", got, UnnamedServerPhrase)
	}
}
