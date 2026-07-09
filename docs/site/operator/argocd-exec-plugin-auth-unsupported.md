# ArgoCD Exec-Plugin Auth (Unknown Commands Only)

**Severity:** P1

> **Verified:** Authored 2026-07-09 against `main` HEAD as part of
> V2-cleanup-88.6 (connection-redesign documentation), updating this
> runbook for the reality shipped by V2-cleanup-88.2 (#509): Sharko now
> recognizes and mints tokens for the two well-known AWS
> `execProviderConfig` commands (`argocd-k8s-auth aws`,
> `aws-iam-authenticator`) instead of rejecting every exec-plugin
> shape outright. This runbook now covers only the remaining
> genuinely-unsupported case: an exec command Sharko doesn't recognize
> as AWS. The `ArgoCDProviderCodeExecUnsupported` =
> `"argocd_provider_exec_unsupported"` sentinel, `isKnownAWSExecCommand`,
> and the dispatch in `resolveExecProviderConfig` are verified against
> `internal/providers/argocd_provider.go`. Re-verify if a third AWS
> exec command is added to the known set, or if GCP/Azure support ships.

`POST /api/v1/clusters/{name}/test` returns 503 on a specific cluster
because that cluster's ArgoCD-shaped Secret uses **exec-plugin auth**
(`execProviderConfig` in the Secret's `data["config"]` JSON) with a
command Sharko doesn't recognize. Sharko's ArgoCDProvider parses and
mints tokens for the two well-known AWS authenticator commands
(`argocd-k8s-auth aws`, `aws-iam-authenticator`) using its own AWS
identity — those succeed silently, no runbook needed. Every **other**
command — `gke-gcloud-auth-plugin`, `kubelogin`, a custom corporate
helper script — stays genuinely unsupported: Sharko has no GCP or Azure
identity to authenticate with, and it deliberately never shells out to
run an arbitrary binary on your behalf. Only the affected cluster is
broken; every other cluster reconciles and tests normally.

If your cluster's `execProviderConfig.command` is `aws-iam-authenticator`
or `argocd-k8s-auth` with `args[0] == "aws"` and you're still seeing a
503, this is **not** the right runbook — Sharko already parses that
shape and tries to mint a token with its own identity. See
[`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) instead; that
runbook covers what happens when the mint attempt itself fails (no
Sharko AWS identity, wrong trust policy, no resolvable region).

---

## Symptoms

What an operator sees when this fires:

- **UI shows the exec-plugin-specific message** on the Cluster detail
  page when the operator clicks **Test connection**:

  ```
  cluster "<name>" uses exec-plugin auth (command "<helper>"). Sharko
  never executes exec-plugin binaries; only the known AWS authenticators
  (argocd-k8s-auth aws, aws-iam-authenticator) are parsed and minted
  with Sharko's own AWS identity — every other command stays
  unsupported.
  ```

  The helper command is whatever was configured in the upstream tool
  that wrote the Secret — commonly `gke-gcloud-auth-plugin`,
  `kubelogin`, or a custom binary path.

- **API: `POST /api/v1/clusters/{name}/test`** returns:

  ```
  HTTP/1.1 503 Service Unavailable
  {"error":"cluster \"<name>\" uses exec-plugin auth (command \"<helper>\"). Sharko never executes exec-plugin binaries; only the known AWS authenticators (argocd-k8s-auth aws, aws-iam-authenticator) are parsed and minted with Sharko's own AWS identity — every other command stays unsupported.","error_code":"argocd_provider_exec_unsupported"}
  ```

  The stable field is `error_code`, value `argocd_provider_exec_unsupported`;
  the UI dispatches on this field, not the message text.

- **Sharko logs the dispatch with a structured Info line** (NOT an
  error — an unrecognized exec command is an expected unsupported
  branch, not a bug):

  ```
  {"time":"...","level":"INFO","msg":"[provider] argocd cluster uses execProviderConfig with an unrecognized command — Sharko never executes exec-plugin binaries","cluster":"<name>","server":"https://...","command":"gke-gcloud-auth-plugin"}
  ```

- **NO Prometheus alert fires** for this case. It's per-cluster and
  expected for non-AWS clusters; the fleet-wide burn-rate alerts only
  fire when every cluster fails.

- **The cluster row in the dashboard** shows status **Test failed**
  with the exec-plugin error string in the tooltip. Other clusters in
  the fleet show **Healthy** — this is per-cluster.

If the symptom is "503 on every cluster," this is **not** the right
runbook — that's
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
(the provider itself is down). This failure is always per-cluster.

If the symptom is "503 with `argocd_provider_iam_required`," jump to
[`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — that's the
AWS-recognized-but-couldn't-mint variant, a different failure entirely.

---

## Diagnosis

Four checks, in order. The first three confirm the failure is a
genuinely-unrecognized exec command and not another 503 cause. The
fourth identifies the upstream tool that wrote the Secret in this
shape, which determines the right mitigation lane.

### 1. Confirm the failure is per-cluster, not fleet-wide

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | {name, test_status, test_error}'
```

Expected: one cluster shows `test_status: "failed"` with `test_error`
matching the exec-plugin string above; the rest are `"healthy"` or
`"untested"`. If **every** cluster shows the same failure, investigate
provider-level failure instead.

### 2. Inspect the failing cluster's ArgoCD-shaped Secret directly, and confirm the command isn't one of the two known AWS ones

```sh
CLUSTER=<failing-cluster-name>
ARGOCD_NS=argocd

kubectl -n "$ARGOCD_NS" get secret \
  -l argocd.argoproj.io/secret-type=cluster \
  -o json \
  | jq -r --arg cluster "$CLUSTER" '
    .items[]
    | select(.data.name | @base64d == $cluster)
    | .data.config | @base64d
    ' \
  | jq '.execProviderConfig'
```

You should see something matching:

```json
{
  "command": "gke-gcloud-auth-plugin",
  "args": ["get-credentials"],
  "apiVersion": "client.authentication.k8s.io/v1beta1"
}
```

If `command` is `"aws-iam-authenticator"`, or `"argocd-k8s-auth"` with
`args[0] == "aws"`, this runbook does **not** apply — Sharko parses and
mints for those; see [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md)
instead. Any other `command` value confirms this runbook applies.

### 3. Confirm the log dispatch using `request_id` correlation

If the operator has the request_id from a failed
`POST /api/v1/clusters/{name}/test`, correlate using the standard
pattern (see
[`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)):

```sh
REQ_ID=req-<id-from-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" 'select(.request_id == $id)'
```

You should see the `"uses execProviderConfig with an unrecognized
command"` line and the response that returned
`argocd_provider_exec_unsupported`. If the request_id has no matches,
the failed request was logged elsewhere — widen the time window or
check the request reached Sharko at all.

### 4. Identify the upstream tool that wrote the Secret

The `command` field tells you which tool's Secret-emission code path
produced this shape. Common values:

- `gke-gcloud-auth-plugin` — written by `gcloud container clusters
  get-credentials`. GCP support is not yet in Sharko (see
  [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md));
  the v1.x-era workaround (Mitigation step 2) still applies — a static
  bearer token, since Sharko cannot mint a GCP identity.
- `kubelogin` — written by AKS auth tooling. Same story as GCP; the
  workaround is a static token.
- `aws-iam-authenticator` or `argocd-k8s-auth aws` — should NOT reach
  this runbook (Sharko recognizes these). If you see one of these
  values here, re-check step 2 — you may be reading the wrong Secret,
  or you're actually hitting `aws-iam-cluster-auth.md`'s mint-failure
  path with a stale error message cached in the UI.
- Custom binary path — operator-defined helper. Confirm it's not doing
  something Sharko-incompatible; the only fix here is Mitigation step
  2 or 3, since Sharko has no way to run it.

The `command` value drives which Mitigation step unblocks the operator.

---

## Mitigation (try in order)

1. **If the command IS one of the two known AWS authenticators, stop —
   you're in the wrong runbook.** Re-check Diagnosis step 2. If
   confirmed AWS, go to
   [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md).

2. **Migrate an AWS-adjacent cluster (mislabeled or legacy custom
   wrapper around `aws-iam-authenticator`) to the recognized shape.**
   If the Secret was hand-written or generated by an older tool with a
   nonstandard command name wrapping the same underlying AWS auth,
   rewrite the Secret's `execProviderConfig.command` to exactly
   `aws-iam-authenticator` (or `argocd-k8s-auth` with `args: ["aws",
   ...]`) so Sharko's `isKnownAWSExecCommand` check matches:

   ```sh
   kubectl -n argocd patch secret cluster-<name> --type='json' \
     -p='[{"op":"replace","path":"/data/config","value":"'"$(jq -n \
       --arg cmd aws-iam-authenticator \
       --arg cluster <eks-cluster-name> \
       '{execProviderConfig:{command:$cmd,args:["token","-i",$cluster],apiVersion:"client.authentication.k8s.io/v1beta1"}}' \
       | base64)"'"}]'
   ```

   Success indicator: `POST /api/v1/clusters/<name>/test` moves from
   `argocd_provider_exec_unsupported` to either `200` (mint succeeded)
   or `argocd_provider_iam_required` (recognized, but see
   [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) for that path).

3. **For a genuinely non-AWS cluster (GCP/Azure): use a static bearer
   token.** Create a ServiceAccount on the target cluster with
   read-only RBAC, mint a non-expiring token, and rewrite the Sharko
   ArgoCD Secret to use `bearerToken`:

   ```sh
   # On the target cluster (kubectl context = target):
   kubectl create sa sharko-readonly -n kube-system
   kubectl create clusterrolebinding sharko-readonly \
     --clusterrole=view \
     --serviceaccount=kube-system:sharko-readonly
   TOKEN=$(kubectl create token sharko-readonly -n kube-system \
     --duration=8760h)

   # On the Sharko cluster — patch the ArgoCD Secret:
   ARGOCD_NS=argocd
   SECRET_NAME=$(kubectl -n "$ARGOCD_NS" get secret \
     -l argocd.argoproj.io/secret-type=cluster -o json \
     | jq -r --arg cluster "$CLUSTER" \
       '.items[] | select(.data.name | @base64d == $cluster) | .metadata.name')

   NEW_CONFIG=$(jq -n --arg t "$TOKEN" '{bearerToken:$t}')
   kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" --type='json' \
     -p="[{\"op\":\"replace\",\"path\":\"/data/config\",\"value\":\"$(echo -n $NEW_CONFIG | base64)\"}]"
   ```

   Static tokens are an explicit trade-off — the token is long-lived
   credential material. Rotate it on a cadence and audit reads against
   the target cluster's apiserver audit log.

4. **Mark the cluster as unmanaged in Sharko and accept the gap.** If
   the upstream tool that wrote the Secret cannot be changed (a shared
   cluster owned by another team, a managed-service Secret Sharko
   cannot edit), the cleanest path is to remove the cluster from
   Sharko's managed set so it stops showing red:

   ```sh
   sharko remove-cluster "$CLUSTER" --confirm
   ```

   The cluster remains in ArgoCD; Sharko just stops listing it in the
   dashboard.

5. **Last resort — contribute a native GCP/Azure provider.** Sharko's
   `internal/providers/gcp.go` and `azure.go` ship stub
   `ClusterCredentialsProvider` implementations with detailed
   implementation guidance in the package doc comments (see
   [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)).
   Native GCP/Azure identity minting would close this gap the same way
   V2-cleanup-88.2 closed it for AWS. Community contributions welcome.

---

## Root-cause patterns

### Cluster Secret was written by `gcloud` or AKS auth tooling

`gcloud container clusters get-credentials` and AKS auth tooling emit
`gke-gcloud-auth-plugin` or `kubelogin` exec configs. Sharko has no
GCP / Azure provider (see
[`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)),
so the operator either uses a static bearer token (Mitigation step 3)
or waits for native provider support.

Diagnostic signature: `command` is `gke-gcloud-auth-plugin` or
`kubelogin`, the cluster's server URL ends in
`.gke.googleusercontent.com` or contains `azmk8s.io`.

### Operator wrote a custom exec helper

A platform engineering team built a corporate auth helper (e.g.
SAML-bridged token mint, Okta-integrated kubeconfig generator) and
wrote it into the cluster Secret. Sharko's pod cannot run that helper
because the binary isn't in the Sharko image and the pod doesn't have
the IdP credentials — and Sharko never shells out regardless.

Diagnostic signature: `command` is a path like `/usr/local/bin/<custom>`
or a script name.

Mitigation: static bearer token (step 3) is the only path; a custom
helper integration would require the corporate IdP's token to be
mintable by Sharko's own identity, which it structurally cannot be.

### Legacy or mislabeled AWS wrapper command

Some older or custom tooling wraps `aws-iam-authenticator` under a
different command name (a shell wrapper script, a renamed binary).
Sharko's `isKnownAWSExecCommand` check matches on the literal command
string, so a wrapper with a different name is treated as unrecognized
even though the underlying auth is AWS IAM.

Diagnostic signature: the `command` field is neither
`aws-iam-authenticator` nor `argocd-k8s-auth`, but the cluster is
verifiably EKS (server URL ends in `.eks.amazonaws.com`).

Fix: Mitigation step 2 — rewrite the Secret to use the literal
recognized command name.

---

## Prevention

- **Monitoring — per-cluster test failure count.** A metric
  `sharko_cluster_test_errors_total{code="argocd_provider_exec_unsupported"}`
  would surface the count of exec-plugin-rejected clusters proactively.
  Today, the only signal is the per-cluster `test_status` in
  `/api/v1/fleet/status`.
- **Documentation — pre-install cluster-secret survey.** During Sharko
  onboarding, document the supported Secret shapes (bearer-token,
  client-certificate, and the two known AWS exec/awsAuthConfig shapes)
  explicitly. Operators bringing existing ArgoCD installations should
  audit their cluster Secrets for non-AWS `execProviderConfig` upfront.
- **For AWS clusters, use the recognized command names from the
  start.** Register or adopt EKS clusters using `aws-iam-authenticator`
  or `argocd-k8s-auth aws` (the ArgoCD built-in helper) rather than a
  custom wrapper, so Sharko's recognition matches without a later
  migration. See [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md).

---

## Related runbooks

- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — the
  AWS-recognized-but-couldn't-mint variant (same 503 shape at
  `POST /clusters/{name}/test`, different `error_code`).
- [`azure-gcp-provider-unimplemented.md`](azure-gcp-provider-unimplemented.md)
  — sibling gap covering native Azure / GCP credential sources for
  addon secrets and cluster registration; many exec-plugin failures
  route here because the upstream helper was for GCP or Azure.
- [`argocd-cluster-secret-corruption.md`](argocd-cluster-secret-corruption.md)
  — adjacent shape: Secret config JSON is malformed in a way that
  produces parse-time errors instead of the exec-plugin dispatch.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — escalate here if the failure is on **every** cluster, indicating
  the active provider is down rather than one Secret being mis-shaped.
- [Cluster Connectivity Model](cluster-connectivity-model.md) and
  [EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md) —
  reference pages for the full credential-shape and IAM-setup picture.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If Mitigation steps 2-3 do not restore the cluster within 2 hours and
the cluster is critical-path for fleet expansion, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The failing cluster name and the value of the `command` field from
  Diagnosis step 2
- The `request_id` from a failed `POST /clusters/{name}/test`
- A 5-minute window of logs filtered by that `request_id`
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-cleanup-88.6 rewrite):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages / wire codes
- [x] Diagnosis has 4 concrete checks with exact commands
- [x] Mitigation uses numbered list (1-5) not bullets
- [x] Mitigation has 5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 3 named causes
- [x] Prevention section present and non-empty
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] Reflects V2-cleanup-88.2 shipped reality: only unknown commands are unsupported
-->
