package notifications

// boundary_test.go — the six proofs the product owner's ruling asks for, one
// test (or one clearly named group) each.
//
//  1. all three addon alerts stay distinguishable
//  2. a secret-shaped backend error injected into each path reaches no surface
//  3. an unknown code gets the generic safe sentence
//  4. no caller flag, field or classification can preserve raw text
//  5. removing the sink enforcement makes these tests fail (a break test, run
//     by hand — see the story report for the anchor counts)
//  6. the behaviour survives being written, reloaded and written again
//
// The sweeps here reuse the harness in sentinel_leak_test.go (sentinelSecret,
// sweep, storeWithCM, storedBytes, captureLogs) and the one in
// description_sanitise_test.go (leakSentinel, sweepForSentinel). Both prove
// themselves on unsanitised text first — a sweep that has never been seen to
// find anything proves nothing by staying quiet.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ── proof 1: the three addon alerts stay distinguishable ────────────────────

// TestBoundary_TheThreeAddonMessagesAreExact pins all six sentences as
// LITERALS.
//
// Not by calling the template and comparing the answer with itself, and not by
// rebuilding the expected string the way the code builds it — both of those
// agree with the code however wrong it is. These are the words a person reads,
// so they are typed out, and changing one has to be a deliberate edit here too.
func TestBoundary_TheThreeAddonMessagesAreExact(t *testing.T) {
	p := Params{Addon: "cert-manager", Cluster: "spoke-eu", Version: "2.0.0", CatalogVersion: "1.4.2"}

	cases := []struct {
		code            Code
		wantID          string
		wantTitle       string
		wantDescription string
		wantType        NotificationType
	}{
		{
			code:            CodeAddonUpgradeAvailable,
			wantID:          "upgrade-cert-manager-2.0.0",
			wantTitle:       "cert-manager 2.0.0 available",
			wantDescription: "Upgrade from 1.4.2 to 2.0.0",
			wantType:        "upgrade",
		},
		{
			code:            CodeAddonMajorUpdate,
			wantID:          "security-cert-manager-2.0.0",
			wantTitle:       "Major update: cert-manager 2.0.0",
			wantDescription: "Major version change from 1.4.2 — review for security patches",
			wantType:        "security",
		},
		{
			code:            CodeAddonVersionDrift,
			wantID:          "drift-cert-manager-spoke-eu",
			wantTitle:       "Version drift: cert-manager on spoke-eu",
			wantDescription: "Running 2.0.0, catalog has 1.4.2",
			wantType:        "drift",
		},
	}

	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			got := New(tc.code, "", p, time.Now())
			if got.ID != tc.wantID {
				t.Errorf("id:\n got %q\nwant %q", got.ID, tc.wantID)
			}
			if got.Title != tc.wantTitle {
				t.Errorf("title:\n got %q\nwant %q", got.Title, tc.wantTitle)
			}
			if got.Description != tc.wantDescription {
				t.Errorf("description:\n got %q\nwant %q", got.Description, tc.wantDescription)
			}
			if got.Type != tc.wantType {
				t.Errorf("type:\n got %q\nwant %q", got.Type, tc.wantType)
			}
		})
	}
}

// TestBoundary_TheThreeAddonMessagesStayDistinct is the ruling's first
// requirement stated as a property rather than as six literals: an upgrade, a
// major-version change and a drift must read differently to a person, and none
// of them may read like the generic fallback.
//
// This is what the old "producer-owned" exemption existed to protect. It is
// still true, and it is now true without any caller writing a sentence.
func TestBoundary_TheThreeAddonMessagesStayDistinct(t *testing.T) {
	p := Params{Addon: "cert-manager", Cluster: "spoke-eu", Version: "2.0.0", CatalogVersion: "1.4.2"}
	addonCodes := []Code{CodeAddonUpgradeAvailable, CodeAddonMajorUpdate, CodeAddonVersionDrift}

	generic := New(Code("something_undeclared"), "", Params{}, time.Now())

	seenTitles := map[string]Code{}
	seenDescriptions := map[string]Code{}
	for _, code := range addonCodes {
		n := New(code, "", p, time.Now())

		if other, dup := seenTitles[n.Title]; dup {
			t.Errorf("%s and %s render the SAME title %q — a person cannot tell the two alerts apart", code, other, n.Title)
		}
		seenTitles[n.Title] = code

		if other, dup := seenDescriptions[n.Description]; dup {
			t.Errorf("%s and %s render the SAME description %q — a person cannot tell the two alerts apart", code, other, n.Description)
		}
		seenDescriptions[n.Description] = code

		if n.Title == generic.Title {
			t.Errorf("%s renders the generic title %q — the distinction was lost, which is exactly what the exemption existed to prevent", code, n.Title)
		}
		if n.Description == generic.Description {
			t.Errorf("%s renders the generic description %q — the distinction was lost", code, n.Description)
		}
		if !strings.Contains(n.Title, p.Addon) {
			t.Errorf("%s does not name the addon in its title %q", code, n.Title)
		}
	}
	if len(seenTitles) != 3 || len(seenDescriptions) != 3 {
		t.Fatalf("expected three distinct titles and three distinct descriptions, got %d and %d",
			len(seenTitles), len(seenDescriptions))
	}
}

// ── proof 2: a secret-shaped backend error reaches no surface ───────────────

// TestBoundary_ASecretShapedErrorReachesNoSurfaceOnAnyCode is the leak proof,
// with its positive control in the same function.
//
// A raw backend error carrying a token in a remote URL is pushed into EVERY
// field a caller can reach — id, title, description, type, reason — on EVERY
// declared code, through the real Store.Add, with ConfigMap persistence wired.
// Then four surfaces are swept: the API's own JSON, the ConfigMap's stored
// bytes, the process log, and the in-memory record.
func TestBoundary_ASecretShapedErrorReachesNoSurfaceOnAnyCode(t *testing.T) {
	raw := sentinelBackendError().Error()

	// POSITIVE CONTROL, first and in the same run: the sweep must FIND the
	// sentinel in the unsanitised text it is about to be asked to miss.
	if leaks := findLeaks(raw, sentinelSecret); len(leaks) == 0 {
		t.Fatal("the sweep did not find the sentinel in the raw backend error itself — " +
			"its silence on the sanitised record below would prove nothing")
	}

	for _, code := range DeclaredCodes() {
		t.Run(code.String(), func(t *testing.T) {
			client, cm := storeWithCM(t)
			store := NewStore(10, cm)

			logs := captureLogs(t, func() {
				store.Add(Notification{
					ID:          "id-" + raw,
					Code:        code,
					Reason:      Reason(raw),
					Type:        NotificationType(raw),
					Title:       raw,
					Description: raw,
					Timestamp:   time.Now(),
				})
			})

			got := store.List()
			if len(got) != 1 {
				t.Fatalf("the probe was not stored (%d records) — this case proved nothing", len(got))
			}

			served, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshalling what the API would serve: %v", err)
			}

			sweep(t, "the JSON GET /notifications would serve", string(served))
			sweep(t, "the ConfigMap's stored bytes", storedBytes(t, client))
			sweep(t, "the process log", logs)
			sweep(t, "the in-memory record", got[0].ID+" "+got[0].Title+" "+got[0].Description+" "+string(got[0].Type)+" "+string(got[0].Reason))
		})
	}
}

// TestBoundary_ASecretShapedParamIsNotInterpolated is the other way in: a
// caller that puts the secret in a Params field rather than in the prose.
//
// The version and the name checks reject it, so it is not interpolated at all —
// and the alert degrades to the generic sentence rather than being emptied.
func TestBoundary_ASecretShapedParamIsNotInterpolated(t *testing.T) {
	raw := sentinelBackendError().Error()

	allAddonCodes := []Code{CodeAddonUpgradeAvailable, CodeAddonMajorUpdate, CodeAddonVersionDrift}

	cases := []struct {
		name  string
		p     Params
		codes []Code
	}{
		{"the whole error as an addon name", Params{Addon: raw, Version: "1.0.0", CatalogVersion: "0.9.0"}, allAddonCodes},
		{"the whole error as a version", Params{Addon: "cert-manager", Version: raw, CatalogVersion: "0.9.0"}, allAddonCodes},
		{"the secret alone as a version", Params{Addon: "cert-manager", Version: sentinelSecret, CatalogVersion: "0.9.0"}, allAddonCodes},
		{"the whole error as a catalog version", Params{Addon: "cert-manager", Version: "1.0.0", CatalogVersion: raw}, allAddonCodes},
		// Only the drift alert names a cluster, so only it can be attacked
		// through that field.
		{"the secret alone as a cluster name", Params{Addon: "cert-manager", Cluster: sentinelSecret, Version: "1.0.0", CatalogVersion: "0.9.0"}, []Code{CodeAddonVersionDrift}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, code := range tc.codes {
				n := New(code, "", tc.p, time.Now())
				sweep(t, code.String()+" title", n.Title)
				sweep(t, code.String()+" description", n.Description)
				sweep(t, code.String()+" id", n.ID)
				if n.Title != TitleUnclassified {
					t.Errorf("%s: a parameter that failed its check still produced a specific title %q — "+
						"the fallback is the generic sentence, not a partially filled one", code, n.Title)
				}
				if n.Description == "" {
					t.Errorf("%s: the description was emptied rather than replaced with the generic sentence", code)
				}
			}
		})
	}
}

// ── proof 3: an unknown code gets the generic safe sentence ─────────────────

func TestBoundary_AnUnknownCodeGetsTheGenericSentence(t *testing.T) {
	unknown := Code("a_code_from_a_newer_sharko")
	if unknown.IsDeclared() {
		t.Fatal("the invented code is declared — pick another or this proves nothing")
	}

	n := New(unknown, "", Params{Addon: "cert-manager", Version: "1.0.0", CatalogVersion: "0.9.0"}, time.Now())

	if n.Title != TitleUnclassified {
		t.Errorf("title = %q, want the generic safe title %q", n.Title, TitleUnclassified)
	}
	// The literal, not reasonSentences[ReasonUnspecified] — a fixture that
	// reads the same constant as the code moves with it and proves nothing.
	const wantGeneric = "Sharko could not work out what kind of problem this is. " +
		"The server log for this check says what happened."
	if n.Description != wantGeneric {
		t.Errorf("description:\n got %q\nwant %q", n.Description, wantGeneric)
	}
	if strings.Contains(n.Title, "cert-manager") || strings.Contains(n.Description, "cert-manager") {
		t.Errorf("an unknown code interpolated its parameters anyway: %q / %q", n.Title, n.Description)
	}

	// And it never reaches the store, so nothing serves it either.
	store := NewStore(10, nil)
	store.Add(n)
	if len(store.List()) != 0 {
		t.Errorf("an undeclared code was stored: %v", store.List())
	}
}

// TestBoundary_EveryCodeHasATemplate is the guard, BY NAME in both directions.
//
// A count would pass while a stale entry rots, and would get happier as the bug
// appeared.
func TestBoundary_EveryCodeHasATemplate(t *testing.T) {
	declared := DeclaredCodes()
	if len(declared) == 0 {
		t.Fatal("DeclaredCodes() is empty — this guard would pass vacuously")
	}

	inSet := map[Code]bool{}
	var missing []string
	for _, c := range declared {
		inSet[c] = true
		if _, ok := messageTemplates[c]; !ok {
			missing = append(missing, "  "+c.String())
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d declared code(s) have no template in render.go:\n%s\n"+
			"They render the generic sentence, so a person gets an alert that says nothing about what happened. "+
			"Give each one a title and a description built from checked identifiers.",
			len(missing), strings.Join(missing, "\n"))
	}

	var stale []string
	for c := range messageTemplates {
		if !inSet[c] {
			stale = append(stale, "  "+c.String())
		}
	}
	if len(stale) > 0 {
		t.Errorf("render.go has a template for %d code(s) that are no longer declared:\n%s\nDead prose.",
			len(stale), strings.Join(stale, "\n"))
	}

	// Every template must actually render something for a person to read.
	full := Params{Addon: "cert-manager", Cluster: "spoke-eu", Version: "2.0.0", CatalogVersion: "1.4.2"}
	for _, c := range declared {
		n := New(c, ReasonUnreachable, full, time.Now())
		if n.Title == "" || n.Description == "" || n.ID == "" {
			t.Errorf("%s renders a blank field: id=%q title=%q description=%q", c, n.ID, n.Title, n.Description)
		}
	}
}

// ── proof 4: no caller flag or classification can preserve raw text ─────────

// TestBoundary_ParamsHasNoProseField is the structural half. The check below
// can only be trusted if there is no field it does not look at.
func TestBoundary_ParamsHasNoProseField(t *testing.T) {
	allowed := map[string]paramField{
		"Addon":          fieldAddon,
		"Cluster":        fieldCluster,
		"Version":        fieldVersion,
		"CatalogVersion": fieldCatalogVersion,
	}

	typ := reflect.TypeOf(Params{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := allowed[name]; !ok {
			t.Errorf("Params has a field %q (%s) that nothing validates.\n"+
				"Every field here is interpolated into words a person reads and into a record that is persisted. "+
				"If it is a new identifier, give it a paramField and a check in Params.valid. "+
				"If it is prose, it is the leak this file exists to prevent — do not add it.",
				name, typ.Field(i).Type)
		}
	}
	for name := range allowed {
		found := false
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("this guard expects a Params field %q that no longer exists — the guard is out of date and covers less than it claims", name)
		}
	}

	// And every paramField name in the allowed list is one Params.valid
	// actually recognises: a name it does not know makes valid() return false,
	// which would silently generic-ise an alert nobody meant to generic-ise.
	filled := Params{Addon: "a", Cluster: "b", Version: "1.0.0", CatalogVersion: "1.0.0"}
	for name, f := range allowed {
		if !filled.valid([]paramField{f}) {
			t.Errorf("Params.valid does not recognise the field %q (%q) — every alert needing it would fall back to the generic sentence", name, f)
		}
	}
}

// TestBoundary_NoCallerCanOptOut walks the ways a caller might try to keep its
// own words and asserts each one fails.
func TestBoundary_NoCallerCanOptOut(t *testing.T) {
	const prose = "the provider said: " + leakSentinel

	t.Run("setting the fields directly", func(t *testing.T) {
		s := NewStore(10, nil)
		s.Add(Notification{
			ID: prose, Code: CodeAddonUpgradeAvailable, Type: NotificationType(prose),
			Title: prose, Description: prose, Timestamp: time.Now(),
		})
		got := s.List()
		if len(got) != 1 {
			t.Fatalf("nothing was stored — this case proved nothing")
		}
		for _, text := range []string{got[0].ID, got[0].Title, got[0].Description, string(got[0].Type)} {
			if found := sweepForSentinel(text); len(found) > 0 {
				t.Errorf("a directly-set field kept the caller's words as %v: %q", found, text)
			}
		}
	})

	t.Run("stamping the current schema by hand", func(t *testing.T) {
		// The schema stamp is what a later Sharko trusts a record by. A caller
		// that pre-stamps it must not thereby skip the render.
		s := NewStore(10, nil)
		s.Add(Notification{
			Code: CodeAddonMajorUpdate, Title: prose, Description: prose,
			Schema: CurrentSchema, Timestamp: time.Now(),
		})
		got := s.List()
		if len(got) != 1 {
			t.Fatalf("nothing was stored — this case proved nothing")
		}
		if found := sweepForSentinel(got[0].Title + " " + got[0].Description); len(found) > 0 {
			t.Errorf("pre-stamping the schema let a caller keep its words as %v", found)
		}
	})

	t.Run("inventing a code to fall outside the rules", func(t *testing.T) {
		// The old shape had a list of exempt codes, so adding a code was a way
		// to become exempt. Now an unknown code is the SAFEST case, not the
		// loosest one.
		n := Notification{Code: Code("my_own_code"), Title: prose, Description: prose}
		sanitizeNotification(&n)
		if found := sweepForSentinel(n.Title + " " + n.Description); len(found) > 0 {
			t.Errorf("an invented code kept the caller's words as %v", found)
		}
	})

	t.Run("converting raw text into the enums", func(t *testing.T) {
		// Code, Reason and NotificationType are all string types, so all three
		// conversions compile.
		n := Notification{
			Code:   Code(prose),
			Reason: Reason(prose),
			Type:   NotificationType(prose),
		}
		sanitizeNotification(&n)
		if found := sweepForSentinel(string(n.Reason) + " " + string(n.Type) + " " + n.Title + " " + n.Description + " " + n.ID); len(found) > 0 {
			t.Errorf("a converted string carried the caller's words through as %v", found)
		}
		// The Code itself is left alone deliberately — Store.Add refuses it,
		// and it is never rendered to a person.
		s := NewStore(10, nil)
		s.Add(n)
		if len(s.List()) != 0 {
			t.Errorf("a notification whose code was raw text was stored: %v", s.List())
		}
	})

	t.Run("running the boundary twice", func(t *testing.T) {
		// Idempotence, so that a record that goes through Add and then through
		// Add again (the checker re-raises every tick) cannot degrade.
		p := Params{Addon: "cert-manager", Cluster: "spoke-eu", Version: "2.0.0", CatalogVersion: "1.4.2"}
		once := New(CodeAddonVersionDrift, "", p, time.Now())
		twice := once
		sanitizeNotification(&twice)
		if once.ID != twice.ID || once.Title != twice.Title || once.Description != twice.Description || once.Type != twice.Type {
			t.Errorf("a second pass through the boundary changed the record:\n once %+v\ntwice %+v", once, twice)
		}
	})
}

// ── proof 6: it survives persistence and reload ─────────────────────────────

// TestBoundary_SurvivesWriteReloadAndWriteAgain drives the path a real pod
// takes: a store built in-memory, upgraded to ConfigMap-backed via
// AttachCMStore, and then read back by a fresh store — twice.
func TestBoundary_SurvivesWriteReloadAndWriteAgain(t *testing.T) {
	raw := sentinelBackendError().Error()
	client, cm := storeWithCM(t)

	// A pod starts in-memory-only (serve.go always does), raises an alert with
	// a caller trying every field, then gets its ConfigMap wired.
	first := NewStore(10, nil)
	first.Add(Notification{
		ID: "id-" + raw, Code: CodeAddonUpgradeAvailable, Type: NotificationType(raw),
		Title: raw, Description: raw, Timestamp: time.Now(),
	})
	if err := first.AttachCMStore(context.Background(), cm); err != nil {
		t.Fatalf("attaching the ConfigMap store: %v", err)
	}
	sweep(t, "the ConfigMap after AttachCMStore", storedBytes(t, client))

	// A restart: a brand-new store reads what is there.
	second := NewStore(10, cm)
	reloaded := second.List()
	if len(reloaded) != 1 {
		t.Fatalf("the record did not survive the reload (%d records) — this test proved nothing about reload", len(reloaded))
	}
	served, err := json.Marshal(reloaded)
	if err != nil {
		t.Fatalf("marshalling what the API would serve after a reload: %v", err)
	}
	sweep(t, "the JSON served after a reload", string(served))
	// The caller supplied no valid parameters, so what survives is the generic
	// safe title — not a half-filled one and not the caller's prose.
	if reloaded[0].Title != TitleUnclassified {
		t.Errorf("the reloaded title is not the generic safe one: %q", reloaded[0].Title)
	}

	// And again, so a fix that filters on read without writing back is caught.
	third := NewStore(10, cm)
	if len(third.List()) != 1 {
		t.Fatalf("the record did not survive a second reload (%d records)", len(third.List()))
	}
	sweep(t, "the ConfigMap after a second restart", storedBytes(t, client))
}

// TestBoundary_AttachCMStoreStillReadsBeforeItWires re-pins the R2-2 fix, which
// this story's changes run straight through: a failed read must leave the store
// in-memory-only, or the next write destroys everything the ConfigMap held.
//
// It is here rather than only in sentinel_leak_test.go because the reload path
// is one of the six proofs and a regression in the ordering would make the
// reload proof above pass while losing data.
func TestBoundary_AttachCMStoreStillReadsBeforeItWires(t *testing.T) {
	client, cm := storeWithCM(t)

	// Seed something worth losing.
	seedTypedRecords(t, cm, New(CodeAddonUpgradeAvailable, "", Params{
		Addon: "cert-manager", Version: "2.0.0", CatalogVersion: "1.4.2",
	}, time.Now()))
	before := storedBytes(t, client)

	restore := failConfigMapReads(client)
	defer restore()

	store := NewStore(10, nil)
	store.Add(New(CodeAddonMajorUpdate, "", Params{
		Addon: "other-addon", Version: "3.0.0", CatalogVersion: "2.0.0",
	}, time.Now()))

	if err := store.AttachCMStore(context.Background(), cm); err == nil {
		t.Fatal("AttachCMStore returned no error although every read fails — this case proved nothing")
	}
	restore()

	// Nothing may have been written.
	store.Add(New(CodeAddonVersionDrift, "", Params{
		Addon: "third-addon", Cluster: "spoke-eu", Version: "1.0.0", CatalogVersion: "2.0.0",
	}, time.Now()))
	if after := storedBytes(t, client); after != before {
		t.Errorf("a failed read left persistence wired, and a later write overwrote the stored state:\nbefore %s\n after %s", before, after)
	}
}

// TestBoundary_NothingBuildsAParamFromAnExpression walks every `Params{...}` in
// production code and rejects any value that could be carrying raw text.
//
// Allowed: a string literal, a bare identifier, a package selector — the shapes
// a name or a version arrives in. Rejected: string concatenation, fmt.Sprintf,
// and any other call, which is what err.Error() is.
//
// The checks in Params.valid would refuse such a value anyway and fall back to
// the generic sentence, so this is not the thing standing between a secret and
// a screen. What it stops is the quieter failure: a producer that builds its
// parameters out of an error and therefore renders the generic sentence for
// every alert it raises, while nothing anywhere goes red.
//
// It is the same guard, in the same shape, as
// TestReason_NoRawTextIsPassedToAHealthResult — the two share safeHealthResultArg.
func TestBoundary_NothingBuildsAParamFromAnExpression(t *testing.T) {
	root := repoRoot(t)

	checked := 0
	for _, rel := range goFilesUnder(t, root) {
		if strings.HasSuffix(rel, "_test.go") {
			continue // tests pass deliberately awful values; that is their job
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			continue // the build catches unparseable files
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			switch typ := lit.Type.(type) {
			case *ast.Ident:
				if typ.Name != "Params" {
					return true
				}
			case *ast.SelectorExpr:
				if typ.Sel.Name != "Params" {
					return true
				}
			default:
				return true
			}
			checked++
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if safeHealthResultArg(kv.Value) {
					continue
				}
				key := ""
				if ident, ok := kv.Key.(*ast.Ident); ok {
					key = ident.Name
				}
				t.Errorf("%s:%d: the %s field of this Params is an expression that can carry raw text.\n"+
					"  Params fields are identifiers — an addon name, a cluster name, a version — and a value that "+
					"fails its check renders the generic sentence for every alert built from it.\n"+
					"  Pass the identifier itself and let render.go compose the sentence.",
					rel, fset.Position(kv.Value.Pos()).Line, key)
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("no Params literals were found anywhere in production code — this guard has lost its reach and would pass vacuously")
	}
}
