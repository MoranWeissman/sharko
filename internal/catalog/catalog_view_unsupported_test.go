// catalog_view_unsupported_test.go — the READING half of the technical-preview
// rule about catalog repository addresses.
//
// The writing half refuses outright: a credential-bearing address never
// reaches Git. That does nothing for a catalog file already sitting in an
// operator's repository from before the rule existed. This is what happens
// then, and it is deliberately not a refusal:
//
//   - the file still loads, so Sharko keeps running;
//   - every other addon in it still works;
//   - the one entry comes back unusable, with the field named;
//   - the address itself is never part of the answer.
package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

const viewSecret = "P4ss-w0rd-the-operator-actually-stored-3q7t"
const viewSweptSecret = "P4ss-w0rd-the-operator-actually-stored-3q7t"
const viewBadRepoURL = "https://git-user:" + viewSecret + "@charts.example/org/charts"
const viewCleanRepoURL = "https://charts.example/org/charts"

func TestViewSentinelsAgree(t *testing.T) {
	if viewSecret != viewSweptSecret || viewSecret == "" {
		t.Fatalf("planted %q and swept %q disagree, or are empty", viewSecret, viewSweptSecret)
	}
}

// specWithOneBadEntry is a catalog with three addons: one whose address
// carries sign-in details, one perfectly fine, one half-written.
func specWithOneBadEntry() config.AddonCatalogSpec {
	return config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
		"keda":         {RepoURL: viewBadRepoURL, Chart: "keda", Version: "2.13.0"},
		"cert-manager": {RepoURL: viewCleanRepoURL, Chart: "cert-manager", Version: "1.14.5"},
		"halfwritten":  {RepoURL: viewCleanRepoURL},
	}}
}

func TestBuildCatalogView_MarksOnlyTheOneEntryUnusable(t *testing.T) {
	view := BuildCatalogView(nil, specWithOneBadEntry())

	if len(view) != 3 {
		t.Fatalf("the view has %d addons, want all 3 — one bad entry must not remove the others", len(view))
	}

	bad, ok := view["keda"]
	if !ok {
		t.Fatal("the entry with the unusable address was dropped from the view. Dropping it silently would delete the addon from the operator's file on the next write.")
	}
	if bad.Deployable {
		t.Error("the entry came back deployable — the rest of Sharko checks that flag before enabling an addon or migrating a repo, so it would go on using the address")
	}
	if len(bad.UnsupportedFields) != 1 || bad.UnsupportedFields[0] != "repoURL" {
		t.Errorf("unsupported_fields is %v, want exactly [repoURL] — the field path, and nothing else", bad.UnsupportedFields)
	}
	if len(bad.MissingFields) != 0 {
		t.Errorf("the entry was also reported as missing %v — it is filled in, it is just not usable", bad.MissingFields)
	}

	good := view["cert-manager"]
	if !good.Deployable {
		t.Errorf("the perfectly good addon came back unusable too: missing %v unsupported %v", good.MissingFields, good.UnsupportedFields)
	}
	if len(good.UnsupportedFields) != 0 {
		t.Errorf("the good addon has unsupported_fields %v", good.UnsupportedFields)
	}

	half := view["halfwritten"]
	if half.Deployable {
		t.Error("the half-written entry came back deployable")
	}
	if len(half.UnsupportedFields) != 0 {
		t.Errorf("the half-written entry was reported as unsupported %v — it is blank, which is a different problem with a different message", half.UnsupportedFields)
	}
}

func TestBuildCatalogView_NamesAnExtraSourceByPosition(t *testing.T) {
	spec := config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{
		"keda": {
			RepoURL: viewCleanRepoURL, Chart: "keda", Version: "2.13.0",
			AdditionalSources: []models.AddonSource{
				{RepoURL: viewCleanRepoURL, Chart: "a"},
				{RepoURL: viewBadRepoURL, Chart: "b"},
			},
		},
	}}
	got := BuildCatalogView(nil, spec)["keda"]
	if got.Deployable {
		t.Error("an unusable extra source left the entry deployable")
	}
	want := "additionalSources[1].repoURL"
	if len(got.UnsupportedFields) != 1 || got.UnsupportedFields[0] != want {
		t.Errorf("unsupported_fields is %v, want exactly [%s]", got.UnsupportedFields, want)
	}
}

// TestBuildCatalogView_TheAnswerCarriesNothingFromTheValue plants the secret,
// proves the sweep finds it in the input, and only then requires it to be
// absent from everything the view produces.
func TestBuildCatalogView_TheAnswerCarriesNothingFromTheValue(t *testing.T) {
	spec := specWithOneBadEntry()

	// Positive control first. A sweep that cannot find a planted secret
	// proves nothing about one it did not find.
	if !strings.Contains(spec.Addons["keda"].RepoURL, viewSweptSecret) {
		t.Fatal("the fixture does not carry the planted secret — every absence check below would pass for the wrong reason")
	}

	view := BuildCatalogView(nil, spec)
	entry := view["keda"]

	// The response copy is the thing that actually leaves the server.
	safe := entry.SafeForResponse()
	body, err := json.Marshal(safe)
	if err != nil {
		t.Fatalf("marshalling the response copy: %v", err)
	}

	surfaces := map[string]string{
		"the response JSON":          string(body),
		"the unsupported field list": strings.Join(entry.UnsupportedFields, " "),
		"the missing field list":     strings.Join(entry.MissingFields, " "),
	}
	if len(surfaces) != 3 {
		t.Fatalf("expected exactly 3 surfaces to sweep, have %d", len(surfaces))
	}
	for what, s := range surfaces {
		if s == "" && what == "the response JSON" {
			t.Fatalf("%s is empty — there is nothing to sweep", what)
		}
		if strings.Contains(s, viewSweptSecret) {
			t.Errorf("%s carries the planted secret: %s", what, s)
		}
		if strings.Contains(s, viewSweptSecret[:8]) {
			t.Errorf("%s carries the first eight characters of the planted secret: %s", what, s)
		}
		if strings.Contains(s, "git-user") {
			t.Errorf("%s carries the user half of the address: %s", what, s)
		}
	}
}

// TestValidateCatalogEntry_RefusesAnUnusableAddress is the write gate the
// add, edit and migrate paths already call.
func TestValidateCatalogEntry_RefusesAnUnusableAddress(t *testing.T) {
	err := ValidateCatalogEntry("keda", config.AddonCatalogEntry{
		RepoURL: viewBadRepoURL, Chart: "keda", Version: "2.13.0",
	})
	if err == nil {
		t.Fatal("the write gate allowed it")
	}

	var typed *credsafe.UnsupportedRepoURLError
	if !errors.As(err, &typed) {
		t.Fatalf("the refusal is not a *credsafe.UnsupportedRepoURLError: %v", err)
	}
	if typed.File != config.AddonCatalogPath {
		t.Errorf("the refusal names file %q, want %q", typed.File, config.AddonCatalogPath)
	}
	if typed.Field != "addons.keda.repoURL" {
		t.Errorf("the refusal names field %q, want %q", typed.Field, "addons.keda.repoURL")
	}
	if strings.Contains(err.Error(), viewSweptSecret) || strings.Contains(err.Error(), "git-user") {
		t.Errorf("the refusal repeats the address: %s", err.Error())
	}
}

func TestValidateCatalogEntry_LetsACleanEntryThrough(t *testing.T) {
	if err := ValidateCatalogEntry("keda", config.AddonCatalogEntry{
		RepoURL: viewCleanRepoURL, Chart: "keda", Version: "2.13.0",
	}); err != nil {
		t.Errorf("a perfectly ordinary entry was refused: %v", err)
	}
}
