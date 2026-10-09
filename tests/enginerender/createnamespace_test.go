// Render-test coverage for the FIX-4 review fix (v4 Wave 1 R2):
// charts/sharko-engine/templates/appset.yaml's createNamespace rebuild
// logic. Before the fix, a fleet-wide `settings.syncOptions` that already
// listed "CreateNamespace=true" literally would defeat a per-cluster
// `createNamespace: false` override (the round-two rebuild re-emitted the
// literal from the baked base list regardless of the per-cluster value),
// and a cluster overriding BOTH `syncOptions` and `createNamespace` would
// silently drop `createNamespace` entirely (the `if hasKey $s
// "syncOptions"` branch matched first and never looked at it).
package enginerender

import (
	"strings"
	"testing"
)

// TestEngineChartCreateNamespaceRebuildStripsFleetWideLiteral proves the
// round-one half of the fix: appset.yaml strips any literal
// "CreateNamespace=true" out of $syncOptionsBase before that list is baked
// into either the typed template's default syncOptions or the round-two
// createNamespace-only rebuild branch — so createNamespace is the only
// thing that ever controls that flag's membership, never a passenger
// carried in from the fleet-wide list.
func TestEngineChartCreateNamespaceRebuildStripsFleetWideLiteral(t *testing.T) {
	// cert-manager's fleet-wide settings explicitly list CreateNamespace=true
	// in syncOptions — the exact "base list already contains
	// CreateNamespace=true" shape that used to defeat a per-cluster
	// createNamespace: false override.
	extra := `
addons:
  cert-manager:
    settings:
      syncOptions:
        - CreateNamespace=true
        - ServerSideApply=true
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")

	// Round one: the TYPED template's own default syncOptions block (no
	// per-cluster override in play) must still contain CreateNamespace=true
	// exactly once — createNamespace defaults true fleet-wide, so it
	// belongs in the output, just not duplicated by carrying both the
	// stripped-then-readded copy AND a leftover literal from the fleet
	// settings.
	// Search for the YAML list-item form ("- CreateNamespace=true", dash
	// prefixed) rather than the bare string, since the chart's own
	// explanatory comments legitimately mention "CreateNamespace=true" in
	// prose without a leading dash.
	typedBlock := extractBetween(t, doc, "syncPolicy:\n        automated:", "templatePatch:")
	if got := strings.Count(typedBlock, "- CreateNamespace=true"); got != 1 {
		t.Errorf("expected exactly one CreateNamespace=true list entry in the typed template's default syncOptions block, got %d.\n%s", got, typedBlock)
	}

	// Round two, createNamespace-only override branch: the rebuild base
	// list (fed from $syncOptionsBase) must NOT bake a CreateNamespace=true
	// list entry unconditionally — it must only appear behind the
	// round-two `{{ if $s.createNamespace }}` guard, or a per-cluster
	// createNamespace: false override would have no effect.
	// createNamespace-only is the LAST branch in the if/else-if chain, so
	// bound the extraction at the next major templatePatch section
	// (ignoreDifferences) rather than the literal "{{- end }}" string —
	// that string also closes the branch's own inner createNamespace
	// guard, which would otherwise truncate the extraction too early.
	rebuildBranch := extractBetween(t, doc,
		`{{- else if hasKey $s "createNamespace" }}`,
		`{{- if hasKey $s "ignoreDifferences" }}`)
	if got := strings.Count(rebuildBranch, "- CreateNamespace=true"); got != 1 {
		t.Errorf("expected exactly one CreateNamespace=true list entry in the createNamespace-only rebuild branch (the round-two conditional one), got %d — the fleet-wide base list is leaking an unconditional copy.\nbranch:\n%s", got, rebuildBranch)
	}
	if !strings.Contains(rebuildBranch, `{{- if $s.createNamespace }}`) {
		t.Errorf("createNamespace-only rebuild branch is missing the round-two createNamespace guard.\nbranch:\n%s", rebuildBranch)
	}
	// The CreateNamespace=true list entry must be inside the `if
	// $s.createNamespace` guard, i.e. it must appear BEFORE the fleet-wide
	// base list's own entries — $syncOptionsBase's `range` is a round-one
	// (Helm) construct, so it's already evaluated to its literal baked
	// items ("- ServerSideApply=true" for this fixture, CreateNamespace=true
	// stripped out per the round-one fix) rather than appearing as
	// template source text in the rendered output.
	guardEnd := strings.Index(rebuildBranch, `{{- if $s.createNamespace }}`)
	bakedBaseStart := strings.Index(rebuildBranch, "- ServerSideApply=true")
	createNSIdx := strings.Index(rebuildBranch, "- CreateNamespace=true")
	if bakedBaseStart < 0 {
		t.Fatalf("expected the fleet-wide base list's own baked entry (ServerSideApply=true) in the createNamespace-only rebuild branch.\nbranch:\n%s", rebuildBranch)
	}
	if !(guardEnd < createNSIdx && createNSIdx < bakedBaseStart) {
		t.Errorf("CreateNamespace=true is not positioned inside the round-two createNamespace guard, ahead of the fleet-wide base range.\nbranch:\n%s", rebuildBranch)
	}
}

// TestEngineChartCreateNamespaceWinsOverSyncOptionsBothSet proves the
// round-two half of the fix: when a cluster overrides BOTH syncOptions and
// createNamespace, createNamespace wins over whatever CreateNamespace=true
// membership the per-cluster syncOptions list itself carries, instead of
// createNamespace being silently ignored because the syncOptions branch
// matched first.
func TestEngineChartCreateNamespaceWinsOverSyncOptionsBothSet(t *testing.T) {
	rendered := renderEngineChart(t)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")

	for _, needle := range []string{
		`{{- if and (hasKey $s "syncOptions") (hasKey $s "createNamespace") }}`,
		`without $s.syncOptions "CreateNamespace=true"`,
		`{{- if $s.createNamespace }}
          - CreateNamespace=true`,
	} {
		if !strings.Contains(doc, needle) {
			t.Errorf("missing both-set createNamespace-wins rebuild fragment: %q\n%s", needle, doc)
		}
	}

	// The both-set branch must be checked BEFORE the syncOptions-only
	// branch in the if/else-if chain — otherwise a cluster that set both
	// keys would match the syncOptions-only branch first and
	// createNamespace would never be consulted (the exact bug this fix
	// closes).
	bothSetIdx := strings.Index(doc, `{{- if and (hasKey $s "syncOptions") (hasKey $s "createNamespace") }}`)
	syncOptionsOnlyIdx := strings.Index(doc, `{{- else if hasKey $s "syncOptions" }}`)
	if bothSetIdx < 0 || syncOptionsOnlyIdx < 0 || bothSetIdx >= syncOptionsOnlyIdx {
		t.Errorf("both-set branch must precede the syncOptions-only branch in the if/else-if chain.\n%s", doc)
	}
}
