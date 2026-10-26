package clusterreconciler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// ep4 round R, R3. When the stored EKS details no longer build the connection
// ArgoCD has, the renewal used to mint a token first, then notice the change,
// then say "could not get a new sign-in token" — and mint again every 2
// minutes. Now a backend that can be read without a mint is read first, and a
// changed connection gets zero mints and its own sentence.

const movedServer = "https://eks-renew-moved.example.invalid"

// The two sentences, word for word.
const (
	wantRenewalFailedSentence = "Sharko could not get a new sign-in token for this cluster, so ArgoCD will lose its connection to it soon. " +
		"Check that Sharko can reach your secrets provider and AWS, then click Refresh EKS connection."
	wantRenewalChangedSentence = "This cluster's details in your secrets store no longer match the connection ArgoCD has, so Sharko did not make a new sign-in token. " +
		"ArgoCD will lose its connection to this cluster soon. " +
		"Click Refresh EKS connection to write the new details and a new token."
)

// factsVault is the EKS backend with the read-only StoredConnectionFacts the
// real AWS backend has. Reading facts never mints; GetCredentials does.
type factsVault struct {
	*mintingVault
	fmu         sync.Mutex
	factsServer string
	factsFail   bool
	factsReads  int
}

var _ providers.StoredConnectionFactsProvider = (*factsVault)(nil)

func (v *factsVault) StoredConnectionFacts(string) (*providers.StoredConnectionFacts, error) {
	v.fmu.Lock()
	defer v.fmu.Unlock()
	v.factsReads++
	if v.factsFail {
		return nil, errors.New("backend refused: SECRET-BACKEND-WORDS")
	}
	return &providers.StoredConnectionFacts{Server: v.factsServer, CredentialMintedPerFetch: true}, nil
}

func (v *factsVault) reads() int {
	v.fmu.Lock()
	defer v.fmu.Unlock()
	return v.factsReads
}

// newFactsRenewalRig is newRenewalRig with a backend that can be read
// without a mint.
func newFactsRenewalRig(t *testing.T) (*renewalRig, *factsVault) {
	t.Helper()
	g := newRenewalRig(t, "eks-token")
	fv := &factsVault{mintingVault: g.vault, factsServer: g.vault.server}
	g.r.deps.Vault = func() Vault { return fv }
	return g, fv
}

func renewalFailedAudits(g *renewalRig) []audit.Entry {
	var out []audit.Entry
	for _, e := range g.audits.Snapshot() {
		if e.Event == "cluster_connection_token_renewal_failed" {
			out = append(out, e)
		}
	}
	return out
}

func TestTokenRenewal_Sentences(t *testing.T) {
	if TokenRenewalFailedSentence != wantRenewalFailedSentence {
		t.Errorf("TokenRenewalFailedSentence:\n got: %q\nwant: %q", TokenRenewalFailedSentence, wantRenewalFailedSentence)
	}
	if TokenRenewalConnectionChangedSentence != wantRenewalChangedSentence {
		t.Errorf("TokenRenewalConnectionChangedSentence:\n got: %q\nwant: %q", TokenRenewalConnectionChangedSentence, wantRenewalChangedSentence)
	}
	// The old words named a button that does not exist ("Refresh"); the
	// button is "Refresh EKS connection".
	for _, s := range []string{TokenRenewalFailedSentence, TokenRenewalConnectionChangedSentence} {
		if strings.HasSuffix(s, "click Refresh.") || strings.Contains(s, "click Refresh.") {
			t.Errorf("a renewal sentence still says the old %q: %q", "click Refresh.", s)
		}
	}
	// A changed connection is not "could not get a token": Sharko chose not to.
	if strings.Contains(TokenRenewalConnectionChangedSentence, "could not get a new sign-in token") {
		t.Error("the connection-changed sentence says Sharko could not get a token, which is not what happened")
	}
}

func TestTokenRenewal_ChangedConnectionMintsNothing(t *testing.T) {
	g, fv := newFactsRenewalRig(t)
	before := g.config(t)
	// The stored EKS details now point at another cluster address. Both ways
	// of reading them see it: the read-only one and the one that mints.
	fv.factsServer = movedServer
	g.vault.server = movedServer

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())
	// Several more tries, past the retry wait each time.
	for i := 0; i < 3; i++ {
		g.advance(renewalRetryAfter + time.Second)
		g.r.pollOnce(context.Background())
	}

	if got := g.vault.count(); got != 1 {
		t.Fatalf("a changed connection led to %d mints in total, want 1 (only the create)", got)
	}
	if fv.reads() == 0 {
		t.Fatal("the stored details were never read, so nothing here proves the check runs before the mint")
	}
	if g.config(t) != before {
		t.Fatal("a changed connection was rewritten by the renewal; that is Repair's job")
	}
	sentence, failing := g.r.TokenRenewalTrouble(renewalCluster)
	if !failing {
		t.Fatal("a changed connection is not marked as a problem — it would still show as Connected")
	}
	if sentence != wantRenewalChangedSentence {
		t.Errorf("sentence:\n got: %q\nwant: %q", sentence, wantRenewalChangedSentence)
	}
	if rec, _ := g.r.LastReconcile(renewalCluster); rec.Message == wantRenewalFailedSentence {
		t.Error("the last reconcile says Sharko could not get a token, for a connection that changed")
	}
	fails := renewalFailedAudits(g)
	if len(fails) != 1 {
		t.Fatalf("got %d renewal-failed activity lines, want 1 for the whole run", len(fails))
	}
	if fails[0].Reason != audit.ReasonInvalidData {
		t.Errorf("activity line reason = %q, want %q", fails[0].Reason, audit.ReasonInvalidData)
	}
}

func TestTokenRenewal_UnreadableDetailsMintNothing(t *testing.T) {
	g, fv := newFactsRenewalRig(t)
	fv.factsFail = true

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())
	g.advance(renewalRetryAfter + time.Second)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 1 {
		t.Fatalf("unreadable stored details led to %d mints in total, want 1 (only the create)", got)
	}
	sentence, failing := g.r.TokenRenewalTrouble(renewalCluster)
	if !failing || sentence != wantRenewalFailedSentence {
		t.Fatalf("TokenRenewalTrouble = (%q, %v), want the could-not-get-a-token sentence", sentence, failing)
	}
	auditJSON, _ := json.Marshal(g.audits.Snapshot())
	if strings.Contains(string(auditJSON), "SECRET-BACKEND-WORDS") {
		t.Error("the backend's own error text reached the audit trail")
	}
}

func TestTokenRenewal_SameConnectionStillRenewsOnce(t *testing.T) {
	g, fv := newFactsRenewalRig(t)

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 2 {
		t.Fatalf("mints = %d, want 2 (create + one renewal)", got)
	}
	if fv.reads() == 0 {
		t.Fatal("the stored details were not read before the mint")
	}
	if !strings.Contains(g.config(t), fakeEKSToken(*g.clock, "MINTED-TOKEN-2")) {
		t.Fatal("the check before the mint stopped a renewal that only changes the token")
	}
	if _, failing := g.r.TokenRenewalTrouble(renewalCluster); failing {
		t.Error("a renewal that worked left the cluster marked as failing")
	}
	// Young token now: the next pass reads nothing and mints nothing.
	reads := fv.reads()
	g.r.pollOnce(context.Background())
	if g.vault.count() != 2 || fv.reads() != reads {
		t.Errorf("a pass with a young token read the store (%d -> %d) or minted (%d)", reads, fv.reads(), g.vault.count())
	}
}

func TestTokenRenewal_FixedConnectionClearsTheMark(t *testing.T) {
	g, fv := newFactsRenewalRig(t)
	original := g.vault.server
	fv.factsServer = movedServer
	g.vault.server = movedServer
	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())
	if _, failing := g.r.TokenRenewalTrouble(renewalCluster); !failing {
		t.Fatal("setup: the changed connection was not marked")
	}

	// The store is put back the way ArgoCD has it: the next try renews.
	fv.factsServer = original
	g.vault.server = original
	g.advance(renewalRetryAfter + time.Second)
	g.r.pollOnce(context.Background())
	if _, failing := g.r.TokenRenewalTrouble(renewalCluster); failing {
		t.Fatal("the mark stayed after the connection matched again and the renewal worked")
	}
	if got := g.vault.count(); got != 2 {
		t.Fatalf("mints = %d, want 2 (create + the one renewal after the fix)", got)
	}
}
