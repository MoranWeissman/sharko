package lifecycleevents

// events_test.go — the wire values, written out.
//
// Every one of these is compared against a LITERAL, not against the constant
// it names. An assertion of the shape `ClusterSecretCreate ==
// ClusterSecretCreate` compares a symbol with itself and stays green forever
// while proving nothing, and this repository has shipped that mistake more
// than once.
//
// These bytes are the pin. Renaming an event is allowed — it just has to be
// deliberate, which means editing this file and the browser's feed table in
// the same change.

import (
	"sort"
	"testing"
)

func TestDeclared_WireValuesAreExactly(t *testing.T) {
	want := []string{
		"cluster_adopted",
		"cluster_connection_repair",
		"cluster_connection_repair_failed",
		"cluster_connection_repair_refused",
		"cluster_connection_repair_requested",
		"cluster_registered",
		"cluster_secret_create",
		"cluster_secret_create_failed",
		"cluster_secret_delete",
		"cluster_secret_delete_failed",
		"cluster_secret_managed_self_heal",
		"cluster_secret_managed_self_heal_failed",
		"cluster_secret_user_label_sync",
		"cluster_secret_user_label_sync_failed",
		"cluster_taken_over",
		"connection_credential_check_failed",
		"connection_credential_check_recovered",
		"connection_credential_drift_cleared",
		"connection_credential_drift_detected",
	}

	got := make([]string, 0, len(Declared()))
	for _, e := range Declared() {
		got = append(got, string(e))
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf(`the catalog holds %d events, this list names %d.

Adding one is fine — add it here too, and give it a title in the browser's
feed table (ui/src/views/connectionActivity.ts), or the feed will skip it.
Removing one is fine as well, as long as nothing still writes it.

got:  %q
want: %q`, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf(`event %d is %q, this list says %q.

A rename here silently empties part of the connection page's activity feed
unless the browser's table is renamed with it.`, i, got[i], want[i])
		}
	}
}

// TestDeclared_HasNoDuplicates. A duplicate would make the generator refuse to
// write, which is a confusing place to find out about it.
func TestDeclared_HasNoDuplicates(t *testing.T) {
	seen := map[Event]bool{}
	for _, e := range Declared() {
		if seen[e] {
			t.Errorf("event %q is declared twice", e)
		}
		seen[e] = true
	}
}

// TestDeclared_HandsBackACopy. The generator and several tests range over this
// slice; a caller that sorted it in place would change what every later caller
// sees, including what gets written into the browser's generated contract.
func TestDeclared_HandsBackACopy(t *testing.T) {
	first := Declared()
	if len(first) == 0 {
		t.Fatal("the catalog is empty, so this test would prove nothing")
	}
	original := first[0]
	first[0] = "mutated_by_a_caller"

	if Declared()[0] != original {
		t.Errorf("a caller writing to the returned slice changed the package's own catalog: it now starts with %q", Declared()[0])
	}
}
