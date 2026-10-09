package service

import (
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
)

const (
	testServiceTok = "service-tok-abc"
	testUserTok    = "user-tok-xyz"
)

func aliceIdentity() UserIdentity {
	return UserIdentity{
		Username:    "alice",
		DisplayName: "Alice Example",
		Email:       "alice@example.com",
	}
}

func TestPickToken_Tier1_AlwaysServiceToken(t *testing.T) {
	res, err := PickGitTokenForTier(audit.Tier1, aliceIdentity(), testServiceTok, func(string) (string, error) {
		t.Fatal("user lookup must NOT be called for Tier 1")
		return "", nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Token != testServiceTok {
		t.Errorf("Tier 1 should use service token, got %q", res.Token)
	}
	if res.AttributionMode != audit.AttributionCoAuthor {
		t.Errorf("Tier 1 with identity should use co_author mode, got %q", res.AttributionMode)
	}
	if res.AttributionFallback {
		t.Error("Tier 1 must never set AttributionFallback=true")
	}
	if res.Attribution.HasAuthor() {
		t.Error("Tier 1 must NOT override commit author")
	}
	if !res.Attribution.HasCoAuthor() {
		t.Error("Tier 1 with identity must set co-author trailer")
	}
}

func TestPickToken_Tier1_NoIdentity_PureService(t *testing.T) {
	res, err := PickGitTokenForTier(audit.Tier1, UserIdentity{}, testServiceTok, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.AttributionMode != audit.AttributionService {
		t.Errorf("expected service mode without identity, got %q", res.AttributionMode)
	}
	if res.Attribution.HasCoAuthor() {
		t.Error("no identity should mean no trailer")
	}
}

func TestPickToken_Tier2_HappyPath_PerUserPAT(t *testing.T) {
	res, err := PickGitTokenForTier(audit.Tier2, aliceIdentity(), testServiceTok, func(u string) (string, error) {
		if u != "alice" {
			t.Errorf("expected lookup for alice, got %q", u)
		}
		return testUserTok, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Token != testUserTok {
		t.Errorf("Tier 2 happy path must use per-user PAT, got %q", res.Token)
	}
	if res.AttributionMode != audit.AttributionPerUser {
		t.Errorf("expected per_user mode, got %q", res.AttributionMode)
	}
	if res.AttributionFallback {
		t.Error("happy path must NOT flag fallback")
	}
	if !res.Attribution.HasAuthor() {
		t.Error("Tier 2 happy path must set commit author to user")
	}
	// Trailer suppressed because author == co-author.
	if res.Attribution.HasCoAuthor() {
		t.Error("trailer must be suppressed when author == user")
	}
}

func TestPickToken_Tier2_FallbackWhenNoPAT(t *testing.T) {
	res, err := PickGitTokenForTier(audit.Tier2, aliceIdentity(), testServiceTok, func(string) (string, error) {
		return "", nil // no PAT configured
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Token != testServiceTok {
		t.Errorf("Tier 2 fallback must use service token, got %q", res.Token)
	}
	if res.AttributionMode != audit.AttributionCoAuthor {
		t.Errorf("expected co_author mode on fallback, got %q", res.AttributionMode)
	}
	if !res.AttributionFallback {
		t.Error("fallback path must set AttributionFallback=true")
	}
	if !res.Attribution.HasCoAuthor() {
		t.Error("fallback must add co-author trailer")
	}
	if res.Attribution.HasAuthor() {
		t.Error("fallback must NOT override author (commit appears as Sharko Bot)")
	}
}

func TestPickToken_Tier2_LookupErrorIsPropagated(t *testing.T) {
	_, err := PickGitTokenForTier(audit.Tier2, aliceIdentity(), testServiceTok, func(string) (string, error) {
		return "", errors.New("decryption boom")
	})
	if err == nil {
		t.Fatal("expected error from lookup")
	}
}

func TestPickToken_TierWebhook_ServiceMode(t *testing.T) {
	res, err := PickGitTokenForTier(audit.TierWebhook, UserIdentity{}, testServiceTok, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.AttributionMode != audit.AttributionService {
		t.Errorf("webhook with no identity should be service mode, got %q", res.AttributionMode)
	}
}
