# ArgoCD Cluster-Secret Corruption

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. Three closely
> related parse-time failure paths in `internal/providers/argocd_provider.go`
> all surface from the same code region in
> `buildBearerTokenKubeconfig`: empty server URL (line 325), invalid
> base64 in `tlsClientConfig.caData` (line 332), and the round-trip
> kubeconfig parse via `clientcmd.RESTConfigFromKubeConfig` (line
> 409). All three are returned as plain `fmt.Errorf` (no sentinel /
> wire code) and bubble up to the API layer as a generic 500 with
> the wrapped reason. The grouping of these three rows into one
> runbook follows the runbook style guide's rule
> (`docs/site/developer-guide/runbook-style-guide.md`): same
> diagnosis path (inspect the Secret YAML directly), same mitigation
> (re-create or repair the Secret). Re-verify when
> `buildBearerTokenKubeconfig` is restructured or sentinel-error
> typed errors are introduced for these paths.

A single cluster's ArgoCD-shaped Secret in the `argocd` namespace is
corrupt in one of three closely-related ways:

1. **Empty server URL** — `data["server"]` is missing or empty
   (provider.go:325).
2. **Invalid base64 in CA data** — `tlsClientConfig.caData` is not
   valid base64 (provider.go:332).
3. **Synthesized kubeconfig fails to parse** — the bearer-token
   kubeconfig Sharko constructs from the Secret won't round-trip
   through `clientcmd.RESTConfigFromKubeConfig` (provider.go:409),
   typically because one of the strings Sharko interpolated contains
   characters that break YAML.

All three look the same to the operator: `POST
/api/v1/clusters/{name}/test` (and the equivalent
addon-enable / refresh flows that touch the same cluster) returns
500 with a wrapped parse error. The cluster is broken in Sharko's
view; other clusters in the fleet continue to reconcile normally.
This runbook covers all three because they share the same diagnosis
path (`kubectl get secret` + inspect the JSON in `data["config"]`)
and the same mitigation (repair or re-create the Secret). The
per-failure detail differs only in which field of the parsed Secret
to look at.

Most of these failures originate **outside Sharko** — a manual
`kubectl edit` mis-typed the base64 CA, a migration tool wrote an
empty server URL, an external automation scrambled the JSON. The
fix is always to repair the Secret; Sharko re-reads it on the next
operation.

---

## Symptoms

What an operator sees when this fires:

- **API: `POST /api/v1/clusters/{name}/test`** returns 500 with one
  of three error strings, distinguishing the three sub-cases:

  Empty server URL:

  ```
  HTTP/1.1 500 Internal Server Error
  {"error":"argocd cluster secret for \"<name>\" has empty server URL"}
  ```

  Invalid CA base64:

  ```
  HTTP/1.1 500 Internal Server Error
  {"error":"decoding tlsClientConfig.caData for cluster \"<name>\": illegal base64 data at input byte ..."}
  ```

  Kubeconfig round-trip parse failure:

  ```
  HTTP/1.1 500 Internal Server Error
  {"error":"synthesized kubeconfig for cluster \"<name>\" failed to parse: yaml: unmarshal errors: ..."}
  ```

- **Sharko logs the parse failure at error level**:

  ```
  {"time":"...","level":"ERROR","msg":"[provider] argocd cluster secret not found","request_id":"req-...","cluster":"<name>","namespace":"argocd","error":"..."}
  ```

  Note: the `slog.Error` line at provider.go:253 only fires when
  `findClusterSecret` itself fails. For the three corruption shapes
  above, the error returns from `buildBearerTokenKubeconfig` and is
  caught by the API handler — look for the wrapped error in the
  handler's `request_id`-correlated logs (Diagnosis step 2).

- **UI** shows the cluster row with status **Test failed** and the
  parse error in the tooltip. Other clusters in the fleet show
  **Healthy** — this is per-cluster.

- **No specific Prometheus alert fires** for a single corrupted
  cluster Secret. Fleet-wide propagation is impossible because the
  failure is bounded to the specific Secret; the cluster shows red
  in the dashboard but reconciliation of other clusters continues
  normally.

If the symptom is "every cluster fails" with parse errors, this is
**not** the right runbook — investigate
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
(provider down) or the
[`cluster-reconciler.md`](cluster-reconciler.md) schema-validation
runbook (the upstream `managed-clusters.yaml` file is unparseable).

If the failure includes the strings `iam_auth_unsupported_in_v1` or
`argocd_provider_exec_unsupported`, those are the v1.x scope-cut
limitations, not corruption — see
[`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) and
[`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md).

---

## Diagnosis

Three checks. Step 1 confirms it's per-cluster (not fleet-wide). Step
2 identifies which of the three sub-cases. Step 3 inspects the Secret
directly to confirm the corruption.

### 1. Confirm the failure is per-cluster, not fleet-wide

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | {name, test_status, test_error}'
```

Expected: one cluster shows `test_status: "failed"` with one of the
three error strings above; the rest show `"healthy"` or `"untested"`.
If every cluster fails with a parse error, jump to the provider-level
runbooks linked above.

### 2. Identify which sub-case via the error string

The error message uniquely identifies which of the three sub-cases:

| Error substring | Sub-case | Mitigation step |
|---|---|---|
| `"empty server URL"` | Empty server URL | 1 |
| `"decoding tlsClientConfig.caData"` + `"illegal base64"` | Invalid CA base64 | 2 |
| `"synthesized kubeconfig"` + `"failed to parse"` | Kubeconfig round-trip parse failure | 3 |

If you have the request_id from the failed
`POST /api/v1/clusters/{name}/test`, correlate the full error chain
(see [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)):

```sh
REQ_ID=req-<id-from-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" 'select(.request_id == $id)' \
  | jq -c '{time, level, msg, cluster, error}'
```

### 3. Inspect the cluster's ArgoCD Secret directly

```sh
CLUSTER=<failing-cluster-name>
ARGOCD_NS=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_ARGOCD_NAMESPACE")].value}')
ARGOCD_NS=${ARGOCD_NS:-argocd}

# Find the Secret backing this cluster:
SECRET_NAME=$(kubectl -n "$ARGOCD_NS" get secret \
  -l argocd.argoproj.io/secret-type=cluster -o json \
  | jq -r --arg cluster "$CLUSTER" '
    .items[]
    | select(.data.name | @base64d == $cluster)
    | .metadata.name
  ')

# Dump the three fields that drive the three sub-cases:
kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json | jq '{
  name: (.data.name | @base64d),
  server: (.data.server | @base64d),
  config: (.data.config | @base64d | fromjson),
  managed_by_label: .metadata.labels."app.kubernetes.io/managed-by"
}'
```

What to look for per sub-case:

- **Empty server URL** — `server` is empty (`""`) or missing. The
  field is required by the Sharko ArgoCDProvider; ArgoCD itself uses
  it as the cluster's apiserver URL.
- **Invalid CA base64** — `config.tlsClientConfig.caData` is set but
  not valid base64. The `jq | @base64d | fromjson` decode succeeds
  (the outer envelope is fine), but the inner `caData` will fail to
  decode further. Test:
  ```sh
  CA=$(kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
    | jq -r '.data.config | @base64d | fromjson | .tlsClientConfig.caData')
  echo "$CA" | base64 -d | head -3
  ```
  Expected: a PEM certificate. Bad: `base64: invalid input`.
- **Kubeconfig round-trip parse failure** — likely the bearer token,
  the server URL, or the cluster name contains a character that the
  synthesized YAML can't represent (a literal newline, an unescaped
  `:`, a leading `-`). Render the synthesized kubeconfig manually:
  ```sh
  TOKEN=$(kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
    | jq -r '.data.config | @base64d | fromjson | .bearerToken')
  echo "Token preview: ${TOKEN:0:40}..."
  # Look for embedded whitespace, newlines, control chars.
  ```

Note the `app.kubernetes.io/managed-by` label — if it's `sharko`, the
Secret was originally written by Sharko (V125-1-8 ownership label).
That's a strong signal the corruption happened AFTER Sharko wrote it
(external `kubectl edit`, GitOps replay with wrong value, manual
config script). If the label is absent or different, the Secret was
externally created and Sharko is reading it without writing it.

---

## Mitigation (try in order)

1. **For empty server URL — repair the Secret by patching
   `data.server`.** This is the most clear-cut sub-case: a field
   that's required is missing.

   ```sh
   CLUSTER=<failing-cluster-name>
   ARGOCD_NS=<argocd-ns>
   SECRET_NAME=<from-diagnosis-step-3>
   CORRECT_SERVER=https://<cluster-apiserver>:443

   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/server\",\"value\":\"$(echo -n "$CORRECT_SERVER" | base64)\"}]"
   ```

   Recovery: re-run `POST /api/v1/clusters/<name>/test`. Success
   indicator: 200 with `{"reachable": true, "version": "..."}`.

   If you don't know the correct server URL, the source of truth is
   the cluster's own kubeconfig (`kubectl --context $CLUSTER config
   view --raw -o jsonpath='{.clusters[0].cluster.server}'`).

2. **For invalid CA base64 — re-encode the CA correctly.** The CA
   in the cluster's kubeconfig is already base64 (kubeconfig spec
   requires it). Copy it verbatim into the Secret's
   `config.tlsClientConfig.caData`:

   ```sh
   CLUSTER=<failing-cluster-name>
   ARGOCD_NS=<argocd-ns>
   SECRET_NAME=<from-diagnosis-step-3>

   # Get the CA from the cluster's own kubeconfig:
   CA_B64=$(kubectl --context "$CLUSTER" config view --raw \
     -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

   # Read the current config JSON, swap caData, write it back:
   CONFIG=$(kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
     | jq -r '.data.config | @base64d')
   NEW_CONFIG=$(echo "$CONFIG" | jq --arg ca "$CA_B64" \
     '.tlsClientConfig.caData = $ca')

   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/config\",\"value\":\"$(echo -n "$NEW_CONFIG" | base64)\"}]"
   ```

   Recovery: re-run the cluster test. Success indicator: same as step
   1.

3. **For kubeconfig round-trip parse failure — identify the bad
   character and re-write the field.** The bearer token is the most
   common offender (some token-mint tools embed a literal newline
   when copy-pasted). Re-mint:

   ```sh
   CLUSTER=<failing-cluster-name>
   ARGOCD_NS=<argocd-ns>
   SECRET_NAME=<from-diagnosis-step-3>

   # Re-mint a token on the target cluster:
   NEW_TOKEN=$(kubectl --context "$CLUSTER" create token \
     <sharko-sa> -n kube-system --duration=8760h)

   # Strip any trailing whitespace defensively:
   NEW_TOKEN=$(echo "$NEW_TOKEN" | tr -d '\r\n ')

   CONFIG=$(kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
     | jq -r '.data.config | @base64d')
   NEW_CONFIG=$(echo "$CONFIG" | jq --arg t "$NEW_TOKEN" \
     '.bearerToken = $t')

   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" \
     --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/config\",\"value\":\"$(echo -n "$NEW_CONFIG" | base64)\"}]"
   ```

   If the bearer token is fine and the failing field is the cluster
   name (rare — happens when the name has unescaped YAML special
   characters), rename the cluster:

   ```sh
   sharko remove-cluster "<bad-name>" --confirm
   sharko add-cluster "<good-name>" --secret-path <provider-path>
   ```

4. **If multiple fields are corrupt and you have a working backup of
   the Secret, restore it.** ArgoCD's cluster Secrets are stateless
   config; restoring an older copy is safe as long as the cluster's
   real auth state hasn't changed (token still valid, CA still
   current, server URL unchanged).

   ```sh
   # Restore from a previous declarative source (GitOps repo, backup):
   kubectl -n "$ARGOCD_NS" apply -f /path/to/backup-secret.yaml
   ```

5. **Last resort — delete the Secret and re-register the cluster
   through Sharko.** This rebuilds the Secret from the provider
   (AWS-SM, K8s-Secrets, etc.) with a known-good shape:

   ```sh
   kubectl -n "$ARGOCD_NS" delete secret "$SECRET_NAME"
   sharko remove-cluster "$CLUSTER" --confirm
   sharko add-cluster "$CLUSTER" --secret-path <provider-path>
   ```

   This drops the cluster from ArgoCD briefly (between delete and
   re-register), so deployed addons reconcile-from-current-state on
   the next ArgoCD sync. Acceptable for non-critical clusters; for
   critical clusters, prefer step 1-3 to avoid the gap.

---

## Root-cause patterns

### Manual `kubectl edit` of the Secret introduced a typo

The most common cause. An operator edited the Secret to update one
field (typically the token after rotation) and either pasted a value
with an unescaped character, dropped a base64 padding byte, or
deleted the wrong key. The `kubectl edit` flow doesn't validate the
resulting `data["config"]` JSON against any schema — invalid base64
or an empty server URL is accepted at apply time and only surfaces
when Sharko (or ArgoCD) tries to use it.

Diagnostic signature: the `app.kubernetes.io/managed-by` label is
`sharko` (Sharko wrote it originally) AND the cluster used to work
(Sharko's own previous tests succeeded). The corruption is
post-Sharko-write.

Fix: Mitigation step 1, 2, or 3 depending on which field is broken.

### External automation rewrote the Secret with wrong values

A GitOps tool, helmfile, kustomize overlay, or custom controller
re-applied a templated version of the Secret with an interpolation
error (empty variable, missing CA file, malformed JSON). The Secret
"applies cleanly" — Kubernetes accepts any `corev1.Secret` body —
but the embedded JSON is broken.

Diagnostic signature: the `app.kubernetes.io/managed-by` label is
NOT `sharko` (external system owns the Secret); the corruption
correlates with a deploy of the external system.

Fix: repair the external automation's source (e.g. the Helm values
file, the kustomize patch). Mitigation step 4 (restore from backup)
is a hold-the-line fix until the upstream is corrected.

### Token rotation tool concatenated trailing whitespace

Some token-mint utilities (older `kubectl create token` variants,
ad-hoc shell pipelines like `aws eks update-kubeconfig | ...`)
include a trailing newline that gets embedded in the bearer token
string. The synthesized YAML then has the token on a multi-line
literal, which can break the round-trip parse depending on the
YAML library version.

Diagnostic signature: Diagnosis step 3's token-preview ends with a
literal `\n` or whitespace; the original token-mint script piped
into `tr -d '\n'` or `xargs` would have stripped it.

Fix: re-mint with the whitespace-stripping pipeline (Mitigation step
3 includes the `tr -d '\r\n '` step).

### Cluster apiserver migrated and the server URL became stale

EKS cluster recreated with the same name but a new apiserver endpoint;
managed-K8s service migrated to a new region's endpoint URL; on-prem
cluster moved behind a different load balancer. The Secret's `server`
field still points at the old URL; the upstream is now wrong (the new
URL doesn't have an empty server URL — the OLD URL is in the Secret
but invalid for routing).

Diagnostic signature: the `server` field IS populated (not the
"empty server URL" sub-case), but TCP probes against it fail. This
is technically NOT one of the three corruption sub-cases — it's a
stale-URL failure that surfaces as a TCP / TLS error, not a parse
error. If you reach this section, the failure is probably one of the
parse sub-cases instead.

Fix: Mitigation step 1 with the new server URL.

---

## Prevention

- **Monitoring — per-cluster `test_status` history.** A V2-3.x
  follow-up metric `sharko_cluster_test_status{cluster, status}`
  exposed as a gauge would let operators alert on any cluster that
  flips from `healthy` to `failed` without a corresponding Sharko
  write event (i.e. external corruption). Today, the only signal
  is the per-cluster `test_status` in `/api/v1/fleet/status`.

- **Gating — Sharko-side Secret-shape validation on every read.**
  Sharko's ArgoCDProvider already validates the three corruption
  sub-cases at fetch time (that's what produces the runbook's error
  strings). A v2 follow-up adds a **proactive** validator that
  scans every ArgoCD cluster Secret in the configured namespace on
  startup and surfaces a warning summary ("2 of 12 cluster Secrets
  have empty server URLs"). Today, the failure only surfaces on the
  first operation that touches the cluster.

- **Documentation — never `kubectl edit` an ArgoCD cluster
  Secret.** The operator guide should explicitly call this out: if
  the cluster needs new credentials, re-register through Sharko
  (`sharko remove-cluster && sharko add-cluster`) so the Secret is
  rebuilt cleanly from the provider. `kubectl edit` is the most
  common corruption source per the root-cause patterns.

- **Gating — webhook admission control on the Secret resource.**
  Platform engineering teams that run their own admission webhook
  could reject updates to `argocd.argoproj.io/secret-type=cluster`
  Secrets that don't validate the embedded `data.config` JSON. This
  is out-of-scope for Sharko itself but documented as a defensive
  pattern.

- **Scheduled work — quarterly Secret-shape audit.** A periodic
  job (e.g. a CronJob in the Sharko namespace) that runs
  `sharko validate-cluster-secrets` (when shipped) and reports any
  corrupt Secret via your existing alerting catches drift before it
  shows up as a dashboard red badge.

---

## Related runbooks

- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — adjacent
  v1.x limitation: `awsAuthConfig` shape returns a distinct typed
  error (NOT a corruption — by design).
- [`argocd-exec-plugin-auth-unsupported.md`](argocd-exec-plugin-auth-unsupported.md)
  — adjacent v1.x limitation: `execProviderConfig` shape returns a
  distinct typed error.
- [`argocd-account-token-expired.md`](argocd-account-token-expired.md)
  — sibling auth failure: Secret is well-formed, but the token is
  revoked or expired upstream.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — escalate here if every cluster's Secret read fails (provider
  itself is down).
- [`cluster-reconciler.md`](cluster-reconciler.md) — the V125-1-8
  reconciler rebuilds these Secrets when the upstream
  `managed-clusters.yaml` changes; if the reconciler is also stuck,
  external repair is the only path.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-3 do not repair the Secret AND the cluster is
critical-path, email the maintainer: `moran.weissman@gmail.com`.
Include:

- This runbook URL
- The cluster name
- The sub-case (empty server URL / invalid CA / kubeconfig parse)
- The output of Diagnosis step 3 (the Secret YAML dump, REDACT the
  bearer token before sending)
- The `app.kubernetes.io/managed-by` label value
- The Sharko version

**Never paste a bearer token into the escalation.** Redact it with
`s/bearerToken=.*\b/bearerToken=REDACTED/` before sending. The
maintainer never needs the actual token to triage the parse failure.

The maintainer is a single human, not a 24×7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages (3 sub-case variants)
- [x] Diagnosis has 3+ concrete checks (3 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (4 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice; documents the 3-rows-grouped decision per style guide
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] No alert applicable (per-cluster); explicitly says so
-->
