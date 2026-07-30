{{/*
============================================================================
sharko-engine — shared helpers
============================================================================
Kept deliberately small: the merge helper is the only thing more than one
template needs, and it is the one piece of logic that must not drift between
call sites (design doc section 4.7, decision D5/D6/D12).
*/}}

{{/*
sharko-engine.mergedAddons returns the merged addon catalog: the shipped
defaults (.Values.curated.addons) with the user's own catalog/addons.yaml
delta (.Values.spec.addons) applied on top, field by field, list fields
replacing whole rather than merging (design doc section 4.7):

  merged = mergeOverwrite(deepCopy(curated.addons), spec.addons)

Sprig's mergeOverwrite takes precedence right to left — the rightmost
argument wins on any key both dicts share — so listing spec.addons last
means the user's delta always wins, exactly as section 2.3 promises ("your
file wins, field by field").

Usage: {{ include "sharko-engine.mergedAddons" . }} returns a YAML dict;
callers assign it with `fromYaml` since Helm has no way to return a live Go
value from a named template.

Null-entry guard (Wave 2 ride-along w2-q6 item 6): a user's own
catalog/addons.yaml can carry a bare `<addon>:` key with no value — YAML
parses that as null (a hand-edit mistake, or a "blank it out" habit carried
over from other tools). mergeOverwrite does NOT drop a nil value the way
Helm's OWN --values-file layering drops a null override; it keeps the key
and OVERWRITES whatever the curated side had there with that literal nil —
clobbering the curated definition, not just leaving it alone the way a
genuinely ABSENT delta entry would (verified empirically: an unfiltered nil
in $delta reaches $merged with that addon's key still present but nil,
wiping out the curated side's own repoURL/chart entirely for that addon.
Filtering nil
entries out of $delta before the merge — rather than only guarding against
nil AFTER the merge (appset.yaml's own `$addon | default dict` belt-and-
braces guard) — means a null delta entry behaves exactly like an ABSENT
one: the curated definition passes through untouched, matching design doc
D16 ("missing means empty").
*/}}
{{- define "sharko-engine.mergedAddons" -}}
{{- $curated := .Values.curated.addons | default dict -}}
{{- $rawDelta := .Values.spec.addons | default dict -}}
{{- $delta := dict -}}
{{- range $name, $entry := $rawDelta -}}
{{- if $entry -}}
{{- $_ := set $delta $name $entry -}}
{{- end -}}
{{- end -}}
{{- mergeOverwrite (deepCopy $curated) $delta | toYaml -}}
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
