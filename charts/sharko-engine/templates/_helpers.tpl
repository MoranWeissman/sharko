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
*/}}
{{- define "sharko-engine.mergedAddons" -}}
{{- $curated := .Values.curated.addons | default dict -}}
{{- $delta := .Values.spec.addons | default dict -}}
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
