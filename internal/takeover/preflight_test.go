package takeover

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// findingByID is the reader every test uses: a report is only useful if you
// can look one check up by its stable id.
func findingByID(t *testing.T, r Report, id string) Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("report has no finding %q; got %d findings", id, len(r.Findings))
	return Finding{}
}

// cleanInputs is a cluster that is ready to be taken over: an unclaimed
// connection, no ApplicationSets, nothing deployed, name free.
func cleanInputs() Inputs {
	return Inputs{
		Cluster:     "prod-eu",
		SecretFound: true,
		Server:      "https://prod-eu.example.org",
		SecretLabels: map[string]string{
			"argocd.argoproj.io/secret-type": "cluster",
		},
	}
}

func TestPreflight_AllFourChecksAlwaysPresent(t *testing.T) {
	r := Preflight(cleanInputs())
	for _, id := range []string{FindingSecretOwner, FindingAppSetSafety, FindingApplications, FindingNameCollision} {
		findingByID(t, r, id)
	}
	if len(r.Findings) != 4 {
		t.Errorf("want exactly the four checks, got %d", len(r.Findings))
	}
}

// TestPreflight_EveryFindingSpeaksPlainly is the story-6.2 guard: a person
// who is not a developer must be able to act on each finding without going
// to look anything up.
func TestPreflight_EveryFindingSpeaksPlainly(t *testing.T) {
	in := cleanInputs()
	in.ManagedBy = "flux"
	in.RegisteredClusters = []string{"prod-eu"}
	in.Applications = []string{"prod-eu-datadog"}
	in.ApplicationSets = []appsets.ApplicationSetInfo{{Name: "addons"}}

	r := Preflight(in)
	for _, f := range r.Findings {
		if f.Title == "" {
			t.Errorf("%s: no title", f.ID)
		}
		if f.Detail == "" {
			t.Errorf("%s: no detail", f.ID)
		}
		if f.WhatItMeans == "" {
			t.Errorf("%s: no plain-English meaning", f.ID)
		}
		if f.WhatToDo == "" {
			t.Errorf("%s: no action — even an ok finding must say there is nothing to do", f.ID)
		}
		// The jargon ban: no raw API field names in the words a person
		// reads. These are the ones this feature is most tempted by.
		for _, banned := range []string{"preserveResourcesOnDeletion", "applicationsSync", "matchLabels", "app.kubernetes.io/managed-by", "secret-type"} {
			for _, text := range []string{f.Detail, f.WhatItMeans, f.WhatToDo} {
				if strings.Contains(text, banned) {
					t.Errorf("%s: leaks the raw field name %q into text a person reads: %q", f.ID, banned, text)
				}
			}
		}
	}
	if r.Summary == "" {
		t.Error("the report needs a one-sentence summary")
	}
}

func TestPreflight_MissingSecretBlocks(t *testing.T) {
	in := cleanInputs()
	in.SecretFound = false
	r := Preflight(in)

	if r.Ready {
		t.Error("a cluster with no connection at all cannot be taken over")
	}
	f := findingByID(t, r, FindingSecretOwner)
	if f.Status != StatusBlocked {
		t.Errorf("status = %q, want blocked", f.Status)
	}
}

func TestPreflight_ForeignManagedByWarnsButDoesNotBlock(t *testing.T) {
	in := cleanInputs()
	in.ManagedBy = "flux"
	r := Preflight(in)

	if !r.Ready {
		t.Error("another tool's ownership marker is a warning, not a block — an operator can know it is retired")
	}
	if !r.NeedsAcknowledgement {
		t.Error("a warning must require an explicit acknowledgement")
	}
	f := findingByID(t, r, FindingSecretOwner)
	if f.Status != StatusWarning {
		t.Errorf("status = %q, want warning", f.Status)
	}
	if !strings.Contains(f.Detail, "flux") {
		t.Errorf("the finding must name the owner it found: %q", f.Detail)
	}
}

func TestPreflight_AlreadySharkoOwnedIsClean(t *testing.T) {
	in := cleanInputs()
	in.ManagedBy = "sharko"
	in.RegisteredClusters = []string{"prod-eu"}
	in.AlreadyTakenOver = true

	r := Preflight(in)
	if !r.Ready || r.NeedsAcknowledgement {
		t.Errorf("re-running the preflight on an already-taken-over cluster must be clean: ready=%v ack=%v", r.Ready, r.NeedsAcknowledgement)
	}
}

func TestPreflight_HardForeignOwnerWarns(t *testing.T) {
	in := cleanInputs()
	in.ForeignOwnerFound = true
	in.ForeignOwnerConfidence = "hard"
	in.ForeignOwnerAppName = "fleet-bootstrap"

	f := findingByID(t, Preflight(in), FindingSecretOwner)
	if f.Status != StatusWarning {
		t.Errorf("status = %q, want warning", f.Status)
	}
	if !strings.Contains(f.WhatItMeans, "fleet-bootstrap") {
		t.Errorf("must name the application: %q", f.WhatItMeans)
	}
}

func TestPreflight_UnsafeApplicationSetsAreNamedAndWarned(t *testing.T) {
	in := cleanInputs()
	in.ApplicationSets = []appsets.ApplicationSetInfo{
		{Name: "safe-addons", PreserveResourcesOnDeletion: true},
		{Name: "legacy-addons"},
		{Name: "hard-delete", ApplicationsSync: appsets.ApplicationsSyncCreateDelete},
	}

	r := Preflight(in)
	f := findingByID(t, r, FindingAppSetSafety)
	if f.Status != StatusWarning {
		t.Errorf("status = %q, want warning", f.Status)
	}
	if len(f.ApplicationSets) != 2 {
		t.Fatalf("want the two unsafe ones named, got %v", f.ApplicationSets)
	}
	for _, want := range []string{"legacy-addons", "hard-delete"} {
		if !strings.Contains(strings.Join(f.ApplicationSets, ","), want) {
			t.Errorf("%q missing from %v", want, f.ApplicationSets)
		}
	}
	if strings.Contains(strings.Join(f.ApplicationSets, ","), "safe-addons") {
		t.Error("a deletion-safe ApplicationSet must not be named as a risk")
	}
	if !r.Ready {
		t.Error("unsafe ApplicationSets warn — they do not block, because the takeover itself changes no labels")
	}
}

func TestPreflight_AllApplicationSetsSafeIsGreen(t *testing.T) {
	in := cleanInputs()
	in.ApplicationSets = []appsets.ApplicationSetInfo{
		{Name: "a", PreserveResourcesOnDeletion: true},
		{Name: "b", ApplicationsSync: appsets.ApplicationsSyncCreateUpdate},
	}
	f := findingByID(t, Preflight(in), FindingAppSetSafety)
	if f.Status != StatusOK {
		t.Errorf("status = %q, want ok", f.Status)
	}
}

// TestPreflight_UnreadableApplicationSetsSayIDoNotKnow — the honesty rule:
// failing to read must never look like "all clear".
func TestPreflight_UnreadableApplicationSetsSayIDoNotKnow(t *testing.T) {
	in := cleanInputs()
	in.ApplicationSetsError = "applicationsets.argoproj.io is forbidden"

	f := findingByID(t, Preflight(in), FindingAppSetSafety)
	if f.Status != StatusWarning {
		t.Errorf("status = %q, want warning", f.Status)
	}
	if !strings.Contains(f.WhatItMeans, "does not know") {
		t.Errorf("must say it does not know rather than implying safety: %q", f.WhatItMeans)
	}
}

func TestPreflight_ApplicationsAreListedInFull(t *testing.T) {
	in := cleanInputs()
	in.Applications = []string{"prod-eu-logging", "prod-eu-datadog"}

	f := findingByID(t, Preflight(in), FindingApplications)
	if f.Status != StatusOK {
		t.Errorf("listing what runs there is information, not a problem: status = %q", f.Status)
	}
	if len(f.Applications) != 2 {
		t.Fatalf("want both applications, got %v", f.Applications)
	}
	if f.Applications[0] != "prod-eu-datadog" {
		t.Errorf("applications must be sorted so a re-run reads identically: %v", f.Applications)
	}
	for _, name := range f.Applications {
		if !strings.Contains(f.Detail, name) {
			t.Errorf("%q missing from the sentence: %q", name, f.Detail)
		}
	}
}

func TestPreflight_NameCollisionBlocks(t *testing.T) {
	in := cleanInputs()
	in.RegisteredClusters = []string{"staging-us", "prod-eu"}

	r := Preflight(in)
	if r.Ready {
		t.Error("a name clash must block")
	}
	f := findingByID(t, r, FindingNameCollision)
	if f.Status != StatusBlocked {
		t.Errorf("status = %q, want blocked", f.Status)
	}
	if !strings.Contains(f.WhatToDo, "run this check again") {
		t.Errorf("a blocked finding must tell the person the check can be re-run: %q", f.WhatToDo)
	}
}

// TestPreflight_ReRunAfterFixGoesGreen is the second half of story 6.2.
func TestPreflight_ReRunAfterFixGoesGreen(t *testing.T) {
	in := cleanInputs()
	in.RegisteredClusters = []string{"prod-eu"}
	if Preflight(in).Ready {
		t.Fatal("precondition: the clash should block")
	}

	// The operator removes the stale cluster and runs it again.
	in.RegisteredClusters = nil
	after := Preflight(in)
	if !after.Ready {
		t.Errorf("after the fix the report must be ready: %s", after.Summary)
	}
	if findingByID(t, after, FindingNameCollision).Status != StatusOK {
		t.Error("the same finding id must come back green after the fix")
	}
}

func TestLegacyLabels_SplitsSharkosOwnFromTheOldOwners(t *testing.T) {
	labels := map[string]string{
		"argocd.argoproj.io/secret-type":  "cluster",
		"app.kubernetes.io/managed-by":    "sharko",
		models.V4AddonLabelPrefix + "kai": "enabled",
		models.LabelConnectivityCheck:     "enabled",
		"env":                             "prod",
		"team":                            "platform",
		"fleet.example.org/tier":          "gold",
	}
	got := LegacyLabels(labels)

	want := map[string]string{"env": "prod", "team": "platform", "fleet.example.org/tier": "gold"}
	if len(got) != len(want) {
		t.Fatalf("LegacyLabels = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q = %q, want %q", k, got[k], v)
		}
	}
	// The ArgoCD marker must never be a candidate — dropping it makes
	// ArgoCD forget the cluster exists.
	if _, ok := got[ArgoCDSecretTypeLabel]; ok {
		t.Error("ArgoCD's own secret-type marker must never be classified as a legacy label")
	}
	if LegacyLabels(map[string]string{"app.kubernetes.io/managed-by": "sharko"}) != nil {
		t.Error("a Secret with only Sharko's own labels has no legacy labels — want nil, not an empty map")
	}
}

func TestPreflight_LegacyLabelsAreShownAndMappedToTheirSelectors(t *testing.T) {
	in := cleanInputs()
	in.SecretLabels = map[string]string{
		"argocd.argoproj.io/secret-type": "cluster",
		"env":                            "prod",
		"team":                           "platform",
	}
	in.ApplicationSets = []appsets.ApplicationSetInfo{
		{Name: "env-based", ClusterSelectorLabels: []string{"env"}, PreserveResourcesOnDeletion: true},
		{Name: "also-env", ClusterSelectorLabels: []string{"env"}, PreserveResourcesOnDeletion: true},
		{Name: "unrelated", ClusterSelectorLabels: []string{"zone"}, PreserveResourcesOnDeletion: true},
	}

	r := Preflight(in)
	if len(r.LegacyLabels) != 2 {
		t.Fatalf("the labels being carried must be shown before the swap: %v", r.LegacyLabels)
	}
	got := r.LegacyLabelsSelectedBy["env"]
	if len(got) != 2 || got[0] != "also-env" || got[1] != "env-based" {
		t.Errorf("LegacyLabelsSelectedBy[env] = %v, want [also-env env-based] (sorted)", got)
	}
	if _, ok := r.LegacyLabelsSelectedBy["team"]; ok {
		t.Error("a label nothing selects on must not appear in the selected-by map")
	}
	// The owner finding names them too, so the list is visible even in a
	// text-only rendering.
	f := findingByID(t, r, FindingSecretOwner)
	if !strings.Contains(f.Detail, "env") || !strings.Contains(f.Detail, "team") {
		t.Errorf("the carried labels must be named in the finding: %q", f.Detail)
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]string{"b": "1", "a": "2", "c": "3"})
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("SortedKeys = %v, want [a b c]", got)
	}
}
