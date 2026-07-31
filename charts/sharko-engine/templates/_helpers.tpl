{{/*
============================================================================
sharko-engine — shared helpers
============================================================================
Kept deliberately small: the addon-list helper is the only thing more than
one template needs, and it is the one piece of logic that must not drift
between call sites.
*/}}

{{/*
sharko-engine.catalogAddons returns the org's approved addon list —
.Values.addons, exactly as the org's own catalog.yaml sets it (design doc
.bmad/output/architecture/2026-07-31-catalog-approved-model.md, decision 6:
"the engine chart reads catalog.yaml + clusters/ + values/ — nothing else").

Every entry is a FULL entry (repoURL, chart, version, namespace, settings,
...) — catalog.yaml is the org's approved list, not a delta against a
shipped default. This chart carries no baked/curated addon data of its own
any more: an empty or missing catalog.yaml means .Values.addons is an empty
map, and this helper returns an empty dict, which appset.yaml's range loop
turns into zero ApplicationSets — a fresh fleet runs nothing until an addon
is approved into the catalog (decision 3's day-zero promise). The shipped
curated set now lives only on the server, feeding the Marketplace — it never
reaches this chart.

Usage: {{ include "sharko-engine.catalogAddons" . }} returns a YAML dict;
callers assign it with `fromYaml` since Helm has no way to return a live Go
value from a named template.

Null-entry guard (relocated from the old sharko-engine.mergedAddons helper
this replaced — Wave 2 ride-along w2-q6 item 6 / sprint-wave2.5 landmine 6):
an org's own catalog.yaml can carry a bare `<addon>:` key with no value —
YAML parses that as null (a hand-edit mistake, or a "blank it out" habit
carried over from other tools). Left unfiltered, that nil reaches appset.
yaml's range loop as `$addon`, and `$addon.settings` on the very next line
panics the whole render ("nil pointer evaluating interface {}.settings") for
EVERY addon in the catalog, not just the null one — one bad line in the
org's catalog must not be able to take down the entire fleet's render.
Filtering the key out here — rather than only guarding against nil AFTER the
fact (appset.yaml's own `$addon | default dict` belt-and-braces guard) —
means a null catalog entry behaves exactly like an ABSENT one: no
ApplicationSet renders for it at all, matching design doc D16 ("missing
means empty").
*/}}
{{- define "sharko-engine.catalogAddons" -}}
{{- $raw := .Values.addons | default dict -}}
{{- $addons := dict -}}
{{- range $name, $entry := $raw -}}
{{- if $entry -}}
{{- $_ := set $addons $name $entry -}}
{{- end -}}
{{- end -}}
{{- $addons | toYaml -}}
{{- end -}}

{{/*
sharko-engine.commonLabels are applied to every resource this chart
generates, so `kubectl get -l app.kubernetes.io/managed-by=sharko` finds all
of it regardless of addon.
*/}}
{{- define "sharko-engine.commonLabels" -}}
app.kubernetes.io/managed-by: sharko
app.kubernetes.io/part-of: sharko-engine
{{- end -}}
