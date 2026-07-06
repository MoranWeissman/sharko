# ArgoCD Exec-Plugin Auth Unsupported

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The
> `ErrArgoCDProviderExecUnsupported` sentinel error and the wire-code
> `argocd_provider_exec_unsupported` are verified against
> `internal/providers/argocd_provider.go` lines 119-137 (constants and
> sentinel) and 295-306 (the `case cfg.ExecProviderConfig != nil`
> dispatch in `GetCredentials`). The `slog.Info` line at provider line
> 296 is the canonical log signature. Re-verify when the wire-code
> constants are renamed or the dispatch branch is restructured (Story
> 10.x of the provider epic may lift this into a JSON envelope).

`POST /api/v1/clusters/{name}/test` returns 503 on a specific cluster
because that cluster's ArgoCD-shaped Secret uses **exec-plugin auth**
(`execProviderConfig` in the Secret's `data["config"]` JSON). Sharko's
ArgoCDProvider in v1.x can route bearer-token kubeconfigs and surface
typed errors for AWS-IAM (`awsAuthConfig`) and exec-plugin
(`execProviderConfig`) shapes, but it does not shell out to the named
exec helper to mint a token. Only the affected cluster is broken; every
bearer-token cluster reconciles normally and every test/refresh
operation against those clusters continues to succeed.

This is the adjacent failure to AWS IAM auth (see
[`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md)) — same v1.x
limitation pattern, different upstream shape. The fix is the same:
either migrate the cluster's Secret to a bearer-token shape that
Sharko's ArgoCDProvider can route, or wait for v2 (which lifts both
limitations). This page tells the on-call operator how to confirm the
failure is the exec-plugin path (not an unrelated 503) and how to
unblock the affected cluster without disturbing other clusters in the
fleet.

---

## Symptoms

What an operator sees when this fires:

- **UI shows the exec-plugin-specific message** on the Cluster detail
  page when the operator clicks **Test connection**:

  ```
  cluster "<name>" uses exec-plugin auth (command "<helper>").
  Exec plugins are not supported in v1.x; tracked for v2.
  ```

  The helper command is whatever was configured in the upstream tool
  that wrote the Secret (commonly `aws-iam-authenticator`,
  `gke-gcloud-auth-plugin`, `kubelogin`, or a custom binary).

- **API: `POST /api/v1/clusters/{name}/test`** returns:

  ```
  HTTP/1.1 503 Service Unavailable
  {"error":"argocd_provider_exec_unsupported","detail":"cluster \"<name>\" uses exec-plugin auth (command \"<helper>\"). Exec plugins are not supported in v1.x; tracked for v2."}
  ```

  The stable code in the response body is
  `argocd_provider_exec_unsupported`
  (`ArgoCDProviderCodeExecUnsupported` in the source); UI dispatch
  keys off this code.

- **Sharko logs the dispatch with a structured Info line** (NOT an
  error — exec-plugin auth is an expected unsupported branch, not a
  bug):

  ```
  {"time":"...","level":"INFO","msg":"[provider] argocd cluster uses execProviderConfig — exec-plugin auth not supported in v1.x","request_id":"req-...","cluster":"<name>","server":"https://...","command":"aws-iam-authenticator"}
  ```

- **NO Prometheus alert fires** for this case. It's per-cluster and
  expected per v1.x scope; the `SharkoClusterRegistrationFastBurn` /
  `SharkoAddonCycleFastBurn` alerts are fleet-wide and only fire when
  every cluster fails.

- **The cluster row in the dashboard** shows status **Test failed**
  with the exec-plugin error string in the tooltip. Other clusters in
  the fleet show **Healthy** — this is per-cluster.

If the symptom is "503 on every cluster," this is **not** the right
runbook — that's
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
(the provider itself is down). The exec-plugin failure is always
per-cluster.

If the symptom is "503 with `iam_auth_unsupported_in_v1`" or the
`argocd_provider_iam_required` code, jump to
[`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — that's the AWS
IAM variant.

---

## Diagnosis

Four checks, in order. The first three confirm the failure is the
exec-plugin path and not another 503 cause. The fourth identifies the
upstream tool that wrote the Secret in this shape, which determines
which mitigation lane to pick.

### 1. Confirm the failure is per-cluster, not fleet-wide

Get the cluster list and their `test` status from the dashboard
endpoint:

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | {name, test_status, test_error}'
```

Expected: one cluster shows `test_status: "failed"` with
`test_error` matching the exec-plugin string above; the rest are
`"healthy"` or `"untested"`. If **every** cluster shows the same
failure, this is not the right runbook — investigate provider-level
failure instead.

### 2. Inspect the failing cluster's ArgoCD-shaped Secret directly

The ArgoCDProvider reads `data["config"]` JSON from the cluster's
ArgoCD Secret. Dump and inspect:

```sh
CLUSTER=<failing-cluster-name>
ARGOCD_NS=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_ARGOCD_NAMESPACE")].value}')
ARGOCD_NS=${ARGOCD_NS:-argocd}

kubectl -n "$ARGOCD_NS" get secret \
  -l argocd.argoproj.io/secret-type=cluster \
  -o json \
  | jq -r --arg cluster "$CLUSTER" '
    .items[]
    | select(.data.name | @base64d == $cluster)
    | .data.config | @base64d
    ' \
  | jq '.'
```

You should see something matching:

```json
{
  "execProviderConfig": {
    "command": "aws-iam-authenticator",
    "args": ["token", "-i", "my-cluster"],
    "apiVersion": "client.authentication.k8s.io/v1beta1"
  },
  "tlsClientConfig": {
    "caData": "..."
  }
}
```

The presence of `execProviderConfig` (and absence of `bearerToken`)
confirms this runbook applies. The `command` field tells you which
upstream tool wrote the Secret.

### 3. Confirm the log dispatch using `request_id` correlation

If the operator has the request_id from a failed
`POST /api/v1/clusters/{name}/test`, correlate using the V2-2.2
pattern (see
[`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)):

```sh
REQ_ID=req-<id-from-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" 'select(.request_id == $id)'
```

You should see the
`"[provider] argocd cluster uses execProviderConfig"` line, the
`provider:argocd` namespace context, and the response that returned
the `argocd_provider_exec_unsupported` code. If the request_id has no
matches, the failed request was logged elsewhere — widen the time
window or check the request reached Sharko at all.

### 4. Identify the upstream tool that wrote the Secret

The `command` field in the exec config tells you which tool's
Secret-emission code path produced this shape. Common values:

- `aws-iam-authenticator` — written by older EKS tooling (e.g.
  `eksctl` with default `--write-kubeconfig`, or `aws eks
  update-kubeconfig` on older AWS CLI versions). Migrate to the
  argocd-k8s-auth structured-JSON shape (Mitigation step 1).
- `gke-gcloud-auth-plugin` — written by `gcloud container clusters
  get-credentials` on recent gcloud versions. GCP support is not yet
  in Sharko (see
  [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md));
  v1.x workaround is to use Workload Identity + a static bearer
  token (Mitigation step 3).
- `kubelogin` — written by AKS auth tooling. Same v1.x story as GCP;
  workaround is a static token.
- Custom binary path — operator-defined helper. Confirm it's not
  doing something Sharko-incompatible (writing time-bounded tokens
  per-call is fine; reaching out to a corporate IdP that Sharko's pod
  can't reach is not).

The `command` value drives which Mitigation step is most likely to
unblock the operator.

---

## Mitigation (try in order)

1. **Migrate the Secret to a bearer-token shape that ArgoCDProvider
   can route.** The simplest path: rewrite the cluster's ArgoCD Secret
   `data["config"]` to use `bearerToken` instead of
   `execProviderConfig`. This is non-destructive — the cluster's
   functional state in ArgoCD is unchanged; only Sharko's read path
   gets a routable shape.

   For an EKS cluster currently using `aws-iam-authenticator`, the
   migration target is the AWS-SM structured-JSON pattern (see
   [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) and the K8s
   expert role file): store a JSON descriptor in AWS Secrets Manager
   with `host`, `caData`, `region`, and optional `roleArn`, and let
   the AWS-SM provider mint a fresh EKS STS token per call. Sharko
   handles this shape natively; the cluster appears as bearer-token
   to the ArgoCDProvider downstream.

   Mint the AWS-SM secret (replace `<region>`, `<account-id>`,
   `<role>`):

   ```sh
   CLUSTER=<failing-cluster-name>
   REGION=<region>
   aws secretsmanager create-secret \
     --name "clusters/$CLUSTER" \
     --region "$REGION" \
     --secret-string "$(jq -n \
       --arg name "$CLUSTER" \
       --arg host "https://<cluster-api-server>" \
       --arg ca "$(kubectl --context $CLUSTER config view \
         --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')" \
       --arg region "$REGION" \
       --arg role "arn:aws:iam::<account-id>:role/SharkoEKSAccess" \
       '{clusterName:$name, host:$host, caData:$ca, region:$region, roleArn:$role}')"
   ```

   Then re-register the cluster through Sharko so the orchestrator
   re-writes the ArgoCD Secret using the AWS-SM path:

   ```sh
   sharko remove-cluster "$CLUSTER" --confirm
   sharko add-cluster "$CLUSTER" --secret-path "clusters/$CLUSTER"
   ```

   Success indicator: `POST /api/v1/clusters/<name>/test` returns 200
   with `{"reachable": true, "version": "v1.x.y"}`. The log line
   changes from "uses execProviderConfig" to "argocd bearer-token
   kubeconfig built".

2. **For a static bearer token (GCP/AKS or any cluster you can mint a
   long-lived token for).** Create a ServiceAccount on the target
   cluster with read-only RBAC, mint a non-expiring token, and rewrite
   the Sharko ArgoCD Secret to use `bearerToken`:

   ```sh
   # On the target cluster (kubectl context = target):
   kubectl create sa sharko-readonly -n kube-system
   kubectl create clusterrolebinding sharko-readonly \
     --clusterrole=view \
     --serviceaccount=kube-system:sharko-readonly
   TOKEN=$(kubectl create token sharko-readonly -n kube-system \
     --duration=8760h)

   # On the Sharko cluster — patch the ArgoCD Secret:
   ARGOCD_NS=<argocd-ns>
   SECRET_NAME=$(kubectl -n "$ARGOCD_NS" get secret \
     -l argocd.argoproj.io/secret-type=cluster -o json \
     | jq -r --arg cluster "$CLUSTER" \
       '.items[] | select(.data.name | @base64d == $cluster) | .metadata.name')

   CA_B64=$(kubectl --context $CLUSTER config view --raw \
     -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
   SERVER=$(kubectl --context $CLUSTER config view --raw \
     -o jsonpath='{.clusters[0].cluster.server}')

   NEW_CONFIG=$(jq -n \
     --arg t "$TOKEN" \
     --arg ca "$CA_B64" \
     '{bearerToken:$t, tlsClientConfig:{caData:$ca}}')

   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/config\",\"value\":\"$(echo -n $NEW_CONFIG | base64)\"}]"
   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/server\",\"value\":\"$(echo -n $SERVER | base64)\"}]"
   ```

   Success indicator: same as step 1 — `test` returns 200.

   Static tokens are an explicit trade-off — the token is long-lived
   credential material. Rotate it on a cadence and audit reads against
   the target cluster's apiserver audit log.

3. **Switch the cluster to `insecure-skip-tls-verify` ONLY for
   troubleshooting.** This is for the case where the operator wants to
   confirm the failure is the exec-plugin path and not a TLS issue —
   not a permanent mitigation. Re-write the Secret's config with
   `"tlsClientConfig": {"insecure": true}` and a bearer token; if test
   then succeeds, the exec-plugin shape was the only blocker. **Do
   NOT leave this in place** — revert to step 1 or step 2 with a CA
   cert as soon as the diagnosis is confirmed.

4. **Mark the cluster as unmanaged in Sharko and accept the v1.x gap.**
   If the upstream tool that wrote the Secret cannot be changed (a
   shared cluster owned by another team, a managed-service Secret
   Sharko cannot edit), the cleanest path is to remove the cluster
   from Sharko's managed set so it stops showing red:

   ```sh
   sharko remove-cluster "$CLUSTER" --confirm
   ```

   The cluster remains in ArgoCD; Sharko just stops listing it in the
   dashboard. Track it in a follow-up issue with the v2 milestone tag.

5. **Last resort — temporarily disable the `test` flow for this
   cluster.** Sharko has no per-cluster opt-out today; mitigation step
   4 is the operational equivalent. A v2 follow-up adds an explicit
   "unmanaged but still listed" badge so the dashboard distinguishes
   "broken" from "v1.x-limited."

---

## Root-cause patterns

### Cluster Secret was written by a legacy `eksctl` / older AWS CLI

The most common cause. `eksctl create cluster` and `aws eks
update-kubeconfig` on releases before mid-2024 wrote
`execProviderConfig` with `command: aws-iam-authenticator`. ArgoCD
itself routes this fine (it has the binary baked into the
argocd-image), but Sharko's ArgoCDProvider does not — by design,
shelling out to operator-defined binaries from inside Sharko is a
v1.x scope cut.

Diagnostic signature: the `command` field in the Secret's
`execProviderConfig` is `aws-iam-authenticator` and the cluster is
an EKS cluster. Mitigation step 1 is the right lane.

### Cluster Secret was written by `gcloud` or AKS auth tooling

Recent gcloud and AKS tooling emit `gke-gcloud-auth-plugin` or
`kubelogin` exec configs. Sharko has no GCP / Azure provider in
v1.x (see
[`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)),
so even after the exec-plugin issue is bypassed, native cloud-auth
isn't available — the operator either uses a static bearer token
(Mitigation step 2) or waits for the v2 provider work to land.

Diagnostic signature: `command` is `gke-gcloud-auth-plugin` or
`kubelogin`, the cluster's server URL ends in
`.gke.googleusercontent.com` or contains `azmk8s.io`.

### Operator wrote a custom exec helper

A platform engineering team built a corporate auth helper (e.g.
SAML-bridged token mint, Okta-integrated kubeconfig generator) and
wrote it into the cluster Secret. Sharko's pod cannot run that helper
because the binary isn't in the Sharko image and the pod doesn't have
the IdP credentials.

Diagnostic signature: `command` is a path like `/usr/local/bin/<custom>`
or a script name. The custom binary may have specific environment
requirements that the Sharko pod can't satisfy even if the binary
were available.

Mitigation: static bearer token (step 2) is the only v1.x path; the
custom helper integration is v2+ work.

---

## Prevention

- **Monitoring — per-cluster test failure count.** A V2-3.x follow-up
  metric `sharko_cluster_test_errors_total{code="argocd_provider_exec_unsupported"}`
  surfaces the count of exec-plugin-rejected clusters. Alert on
  count > 0 with a Warn-tier alert (P2) so operators see the gap
  proactively instead of from a customer report. Today, the only
  signal is the per-cluster `test_status` in `/api/v1/fleet/status`.

- **Documentation — pre-install cluster-secret survey.** During
  Sharko onboarding, document the supported Secret shapes
  (bearer-token + AWS-SM structured-JSON) explicitly. Operators
  bringing existing ArgoCD installations should audit their cluster
  Secrets for `execProviderConfig` upfront and plan migration before
  install rather than discovering the gap from a dashboard red badge.

- **Gating — startup audit of existing cluster Secrets.** A future
  Sharko init flow could scan `argocd.argoproj.io/secret-type=cluster`
  Secrets in the configured namespace and surface a warning summary
  ("3 of 12 clusters use exec-plugin auth; see runbook X"). Lands as
  part of the v2 provider work that closes this gap.

- **Migration path — bulk re-registration script.** Once an operator
  has the AWS-SM secrets seeded, the migration is `sharko
  remove-cluster X && sharko add-cluster X --secret-path
  clusters/X` per cluster. A future `sharko migrate-cluster-secrets`
  helper would batch this; tracked in the v2 provider work.

- **Scheduled work — quarterly review of static bearer tokens.**
  Operators who use Mitigation step 2 must rotate the static token
  on a cadence (the runbook recommends quarterly). Set a calendar
  reminder against the affected cluster names; the target cluster's
  audit log shows when the SA last produced tokens.

---

## Related runbooks

- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — adjacent v1.x
  limitation for `awsAuthConfig` Secrets (also surfaces as 503 at
  `POST /clusters/{name}/test`, also fixed by v2).
- [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)
  — sibling runbook covering native Azure / GCP credential sources.
  Many exec-plugin failures route here because the upstream
  helper was for GCP or Azure.
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — adjacent shape: Secret config JSON is malformed in a way that
  produces parse-time errors instead of the exec-plugin dispatch.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — escalate here if the failure is on **every** cluster (not
  per-cluster), indicating the active provider is down rather than
  one Secret being mis-shaped.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory
  for adjacent failure modes.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern used in Diagnosis step 3.

## Escalation

If Mitigation steps 1-2 do not restore the cluster within 2 hours and
the cluster is critical-path for fleet expansion, email the
maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The failing cluster name and the value of the `command` field from
  Diagnosis step 2
- The `request_id` from a failed `POST /clusters/{name}/test`
- A 5-minute window of logs filtered by that `request_id`
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages / wire codes
- [x] Diagnosis has 3+ concrete checks (4 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (3 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] No alert name applicable (per-cluster failure, no Prom alert)
-->
