package audit

// enrich_completeness_test.go — the guard for the defect that made an entire
// ruling's handler-side work dead code.
//
// Fields gained Result and Changes. The audit middleware was wired to read
// them. Enrich — the ONLY way a handler puts anything into the in-flight
// Fields — was not updated, so every handler that set them wrote into a value
// that was discarded on return.
//
// Nothing failed. No build error, no test failure, no log line. A dropped
// field has no symptom at the call site, which is why it survived a green
// suite and shipped: an all-failed adoption still recorded "Cluster adopted ·
// success", and a repair that changed nothing still had no way to say so.
//
// So this test does not check the two fields that were missed. It walks
// Fields by REFLECTION and fails on any field Enrich does not carry — the
// next field added is covered before anybody thinks to write a test for it.

import (
	"context"
	"reflect"
	"testing"
)

// TestEnrich_CopiesEveryFieldOfFields sets every field of Fields to a
// distinct non-zero value, enriches, and reads back. Any field that does not
// survive the trip is named in the failure.
func TestEnrich_CopiesEveryFieldOfFields(t *testing.T) {
	// Build a Fields with every field non-zero, by reflection, so adding a
	// field to the struct automatically extends this test.
	sent := Fields{}
	v := reflect.ValueOf(&sent).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			t.Fatalf("Fields.%s is unexported — this test cannot verify it", typ.Field(i).Name)
		}
		switch f.Kind() {
		case reflect.String:
			// A distinct value per field so a mis-copy (field A landing in
			// field B) is caught as well as a dropped one.
			f.SetString("enrich-probe-" + typ.Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(7)
		default:
			t.Fatalf("Fields.%s has kind %s, which this test does not know how to probe — extend it",
				typ.Field(i).Name, f.Kind())
		}
	}

	ctx, got := WithEnrichment(context.Background())
	Enrich(ctx, sent)

	gotV := reflect.ValueOf(got).Elem()
	var dropped []string
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		want := v.Field(i).Interface()
		have := gotV.Field(i).Interface()
		if !reflect.DeepEqual(want, have) {
			dropped = append(dropped, name+": set "+toStr(want)+", read back "+toStr(have))
		}
	}
	if len(dropped) > 0 {
		t.Fatalf("Enrich silently dropped %d field(s) of Fields:\n  %s\n\n"+
			"A handler that sets one of these is writing into a value that is thrown away, with no\n"+
			"error anywhere. Add the field to Enrich, following the non-empty-wins pattern.",
			len(dropped), joinLines(dropped))
	}
	if typ.NumField() < 7 {
		t.Fatalf("Fields has only %d fields — this probe has lost its reach", typ.NumField())
	}
}

// TestEnrich_EmptyValuesDoNotOverwrite pins the other half of the contract:
// non-empty wins, and an empty field in a later Enrich call leaves the
// earlier value alone. Handlers rely on this — they enrich the event up
// front and the outcome later.
func TestEnrich_EmptyValuesDoNotOverwrite(t *testing.T) {
	ctx, f := WithEnrichment(context.Background())
	Enrich(ctx, Fields{Event: "first", Result: "failure", Changes: ChangesNone})
	Enrich(ctx, Fields{Detail: "added later"})

	if f.Event != "first" {
		t.Errorf("Event = %q, want the earlier value to survive an empty one", f.Event)
	}
	if f.Result != "failure" {
		t.Errorf("Result = %q, want failure — an empty Result must not clear an earlier one", f.Result)
	}
	if f.Changes != ChangesNone {
		t.Errorf("Changes = %q, want none — an empty Changes must not clear an earlier one", f.Changes)
	}
	if f.Detail != "added later" {
		t.Errorf("Detail = %q", f.Detail)
	}
}

// TestEnrich_ResultAndChangesReachTheFields is the reviewer's own probe, kept
// as a named regression test so the specific defect has a specific guard
// alongside the general one.
func TestEnrich_ResultAndChangesReachTheFields(t *testing.T) {
	ctx, f := WithEnrichment(context.Background())
	Enrich(ctx, Fields{Event: "cluster_adopted", Result: "failure", Changes: ChangesNone})

	if f.Result != "failure" {
		t.Errorf("Result = %q, want failure — this is the drop that made an all-failed adoption record success", f.Result)
	}
	if f.Changes != ChangesNone {
		t.Errorf("Changes = %q, want none — this is the drop that made \"No changes made\" unrenderable", f.Changes)
	}
}

func toStr(v interface{}) string {
	switch x := v.(type) {
	case string:
		return `"` + x + `"`
	case ChangeResult:
		return `"` + string(x) + `"`
	case AttributionMode:
		return `"` + string(x) + `"`
	case Tier:
		return `"` + string(x) + `"`
	}
	return "?"
}

func joinLines(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "\n  "
		}
		out += s
	}
	return out
}
