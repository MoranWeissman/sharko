package audit

import (
	"fmt"
	"strings"
	"testing"
)

// webhook_share_test.go — a flood of webhook-sourced entries must not be able
// to erase the record of what people and tokens did.
//
// The ring drops its oldest entry when it is full. That is fine when every
// writer is on the inside; it is not fine for a writer anybody on the network
// can reach, because filling the ring is then a cheap way to make the log
// forget something. The cap in log.go is what stops it, and these tests are
// what stop the cap from being quietly removed.

func authEntry(n int) Entry {
	return Entry{Event: "login", User: fmt.Sprintf("person-%d", n), Source: "ui", Result: "success"}
}

func webhookEntry() Entry {
	return Entry{Event: "push", User: "webhook", Source: SourceWebhook, Result: "success"}
}

// TestWebhookFloodCannotEraseAuthenticatedEntries is the point of the cap.
//
// Fill the ring with entries from real actors, then send far more webhook
// entries than the ring can hold. Every original entry beyond the webhook
// share must still be there afterwards.
func TestWebhookFloodCannotEraseAuthenticatedEntries(t *testing.T) {
	const size = 100
	log := NewLog(size)

	for i := 0; i < size; i++ {
		log.Add(authEntry(i))
	}
	if got := len(log.List(0)); got != size {
		t.Fatalf("setup is broken: expected a full ring of %d, got %d", size, got)
	}

	// Fifty times the whole ring.
	for i := 0; i < size*50; i++ {
		log.Add(webhookEntry())
	}

	entries := log.List(0)

	var webhooks, authed int
	survivors := map[string]bool{}
	for _, e := range entries {
		if e.Source == SourceWebhook {
			webhooks++
			continue
		}
		authed++
		survivors[e.User] = true
	}

	wantWebhooks := size / webhookShareOfRing
	if webhooks != wantWebhooks {
		t.Errorf("webhook entries hold %d slots; the share is exactly %d", webhooks, wantWebhooks)
	}
	if authed != size-wantWebhooks {
		t.Errorf("%d entries from real actors survived; exactly %d must", authed, size-wantWebhooks)
	}

	// Say WHICH ones survived, not just how many — a count would pass if the
	// ring kept the wrong ones.
	for i := wantWebhooks; i < size; i++ {
		if !survivors[fmt.Sprintf("person-%d", i)] {
			t.Errorf("person-%d was pushed out by the webhook flood", i)
		}
	}
}

// TestWebhookShareIsExactlyATenthOfTheRing pins the number itself, with != so
// it cannot drift in either direction unnoticed.
func TestWebhookShareIsExactlyATenthOfTheRing(t *testing.T) {
	for _, tc := range []struct{ size, want int }{
		{1000, 100}, // the default
		{100, 10},
		{10, 1},
		{5, 1}, // rounds down to zero, floored at one
		{1, 1},
	} {
		t.Run(fmt.Sprintf("ring-of-%d", tc.size), func(t *testing.T) {
			log := NewLog(tc.size)
			if log.maxWebhook != tc.want {
				t.Fatalf("a ring of %d gives webhooks %d slots, want exactly %d",
					tc.size, log.maxWebhook, tc.want)
			}

			for i := 0; i < tc.want*10+20; i++ {
				log.Add(webhookEntry())
			}
			var webhooks int
			for _, e := range log.List(0) {
				if e.Source == SourceWebhook {
					webhooks++
				}
			}
			if webhooks != tc.want {
				t.Fatalf("after a flood the ring holds %d webhook entries, want exactly %d",
					webhooks, tc.want)
			}
		})
	}
}

// TestWebhookCountStaysHonestWhenOrdinaryEntriesTrim covers the bookkeeping.
//
// The running count of webhook entries is kept by hand. If it is not decreased
// when an ordinary trim carries a webhook entry off the end, it drifts upward
// forever and the share silently shrinks to nothing.
func TestWebhookCountStaysHonestWhenOrdinaryEntriesTrim(t *testing.T) {
	const size = 20
	log := NewLog(size)

	// Interleave, many times over, so webhook entries repeatedly reach the
	// tail and fall off through the ordinary trim rather than the share drop.
	for round := 0; round < 40; round++ {
		log.Add(webhookEntry())
		for i := 0; i < 5; i++ {
			log.Add(authEntry(round*100 + i))
		}
	}

	var counted int
	for _, e := range log.List(0) {
		if e.Source == SourceWebhook {
			counted++
		}
	}
	if log.webhookCount != counted {
		t.Fatalf("the running count says %d webhook entries, the ring actually holds %d — "+
			"the count has drifted and the share is no longer what it claims",
			log.webhookCount, counted)
	}

	// And the share still works after all that churn.
	for i := 0; i < 500; i++ {
		log.Add(webhookEntry())
	}
	var webhooks int
	for _, e := range log.List(0) {
		if e.Source == SourceWebhook {
			webhooks++
		}
	}
	if webhooks != log.maxWebhook {
		t.Fatalf("after churn a flood took %d slots, want exactly %d", webhooks, log.maxWebhook)
	}
}

// TestSourceWebhookConstantMatchesWhatWritersUse fails if the constant and the
// word used across the tree ever part company — the cap keys on the exact
// string, so a writer spelling it differently opts its entries out silently.
func TestSourceWebhookConstantMatchesWhatWritersUse(t *testing.T) {
	if SourceWebhook != "webhook" {
		t.Fatalf("SourceWebhook is %q; the audit Source column, the UI filter list and the "+
			"documented values all say \"webhook\"", SourceWebhook)
	}
	if strings.TrimSpace(SourceWebhook) != SourceWebhook {
		t.Fatalf("SourceWebhook has surrounding space: %q", SourceWebhook)
	}
}
