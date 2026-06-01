# AWS Secrets Manager — Secret Not Found

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The error
> message `"secret for cluster %q not found in AWS Secrets Manager.
> Tried: %s. Set secret_path on the cluster to specify the exact
> name"` is verified against `internal/providers/aws_sm.go:150-152`
> in `GetCredentials`. The provider tries two paths: (1) the
> configured `prefix + clusterName`, and (2) the bare cluster name as
> an exact secret name. If both fail, this error returns with the
> `tried` list rendered for operator diagnostic clarity. Re-verify
> when the lookup loop (steps 1-2 in GetCredentials) changes order
> or introduces new fallback prefixes.

A single cluster's credential fetch failed because the AWS Secrets
Manager provider could not find the cluster's secret at any of the
paths it tried. Sharko's AWS-SM provider attempts the configured
prefix first (e.g. `clusters/<cluster-name>`), then the bare cluster
name as a fallback. When both lookups return `ResourceNotFoundException`,
the provider returns the "not found, tried: ..." error and the
operation fails (cluster registration, test, or addon-enable).

This is a per-cluster failure mode. Other clusters whose secrets ARE
at the expected paths continue to reconcile normally. The fix is
always one of: (a) move the secret to the expected path, (b) update
the cluster's `secret_path` override to point at the actual secret,
or (c) update the Sharko-wide prefix to match the existing layout.
The runbook walks the operator through deciding which lane to pick
based on whether the operator owns the SM layout or the secret layout
predates Sharko's deployment.

This is distinct from the **AccessDenied on Search** failure mode
(see [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md))
— that's an IAM permission issue degrading the `searchSimilar` /
`ListSecrets` helper that returns suggestions, not the primary
`GetSecretValue` lookup. If your error mentions "Tried: " with two
paths and no suggestions, this is the right runbook.

---

## Symptoms

What an operator sees when this fires:

- **API: `POST /api/v1/clusters/{name}/test`** (or any
  cluster-credential-needing operation) returns 502 / 500 with the
  exact error from `internal/providers/aws_sm.go:150`:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"secret for cluster \"prod-eu\" not found in AWS Secrets Manager. Tried: clusters/prod-eu, prod-eu. Set secret_path on the cluster to specify the exact name"}
  ```

- **Sharko logs the failure at error level**:

  ```
  {"time":"...","level":"ERROR","msg":"[provider] GetCredentials failed","request_id":"req-...","cluster":"prod-eu","step":"all-lookups","tried":"clusters/prod-eu, prod-eu"}
  ```

  The `tried` field renders the two paths the provider attempted, in
  order. If the configured prefix is empty, only one path appears.

- **The cluster row** in the dashboard shows **Test failed** with the
  not-found error in the tooltip. Other clusters whose secrets exist
  at expected paths show **Healthy** — this is per-cluster.

- **If `SearchSecrets` permissions are intact**, the API response
  might also include suggestions surfaced by
  `internal/providers/aws_sm.go:179-202` (`searchSimilar`). The
  handler (`handleTestCluster`) calls `SearchSecrets` separately
  after `GetCredentials` fails and renders the suggestions in the
  body. Suggestions are useful — they show similarly-named secrets
  in the SM account that might be the actual one for this cluster.

- **No specific Prometheus alert fires** for a single missing secret.
  Fleet-wide misconfiguration (every cluster's secret-path is wrong)
  fans into the
  [`SharkoClusterRegistrationFastBurn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  / `SharkoClusterRegistrationSlowBurn` alerts.

If the symptom is "every cluster fails with this error," the issue is
likely a Helm misconfiguration (wrong `secrets.prefix` value, wrong
`secrets.region`, wrong IAM role on Sharko's SA) — that fans up into
the `SharkoClusterRegistrationFastBurn` alert. Single-cluster failure
stays in this runbook.

If the error mentions "AccessDenied" instead of "not found," see
[`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md).

---

## Diagnosis

Four checks. Step 1 confirms it's per-cluster. Step 2 captures the
exact paths Sharko tried. Step 3 finds the actual secret in AWS-SM.
Step 4 decides whether the right fix is to move the secret, override
the per-cluster path, or update the Helm config.

### 1. Confirm the failure is per-cluster, not fleet-wide

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | {name, test_status, test_error}'
```

Expected: one cluster shows the not-found error; others are healthy
or have different errors. If many clusters show "secret not found"
all at once, the issue is Helm-side (Mitigation step 4 catches that
shape).

### 2. Read off the exact paths Sharko tried

The error message includes the two paths in the `Tried:` field. Note
both for the next step. If you don't have the response in front of
you, re-trigger and capture:

```sh
CLUSTER=<failing-cluster-name>
curl -sS -X POST "http://sharko/api/v1/clusters/$CLUSTER/test" \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  -H "Content-Type: application/json" -d '{}' \
  | jq '.'
```

Or pull from logs via `request_id` correlation (see
[`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)):

```sh
REQ_ID=req-<id-from-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" \
    'select(.request_id == $id and .msg == "[provider] GetCredentials failed")' \
  | jq -c '{cluster, tried, step}'
```

### 3. Find the actual secret in AWS-SM

Exec into the Sharko pod (so the IRSA chain matches what the provider
sees) and search for the secret by cluster-name substring:

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
AWS_REGION=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AWS_REGION")].value}')

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws --region "$AWS_REGION" secretsmanager list-secrets \
  --filters "Key=name,Values=$CLUSTER" \
  --query 'SecretList[].Name' --output table
```

Four outcomes:

- **One match at the expected path** — the path matches Diagnosis
  step 2's `Tried` list. The fix is operator-side cache / timing —
  check `kubectl -n <sharko-ns> logs --since=1m` for a successful
  fetch since you last triggered, OR force a retry by deleting the
  cached failure (Mitigation step 1).
- **One match at a different path** — the secret exists but under a
  name Sharko didn't try. Mitigation step 2 (override per-cluster
  path) or step 4 (update prefix) applies depending on whether this
  is a one-off or a pattern.
- **Multiple matches** — names overlap (`prod-eu`, `prod-eu-staging`,
  `prod-eu-old`). The suggestions in the API response (if
  SearchSecrets is permitted) tell you the same. Pick the right one
  and override (Mitigation step 2).
- **Zero matches** — the secret genuinely doesn't exist. Mitigation
  step 3 (create the secret) applies.

### 4. Confirm the Sharko-side prefix configuration

```sh
SHARKO_PREFIX=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_PROVIDER_PREFIX")].value}')
echo "Configured prefix: '${SHARKO_PREFIX:-<empty>}'"

# Also read the Helm value if present:
helm get values sharko -n <sharko-ns> | grep -A2 -E 'secrets:|prefix:'
```

If the prefix configured in Helm is `clusters/` and the actual secret
in AWS-SM is at `eks/<cluster-name>`, the operator-wide misconfig is
the cause; either move the secret or change the Helm prefix
(Mitigation step 4).

---

## Mitigation (try in order)

1. **If Diagnosis step 3 shows the secret IS at the expected path —
   the cluster was registered before the secret was created.**
   Trigger a re-fetch by re-running the operation. Sharko does not
   cache failure for long (no negative-cache), so the next
   `POST /clusters/{name}/test` re-resolves:

   ```sh
   curl -sS -X POST "http://sharko/api/v1/clusters/$CLUSTER/test" \
     -H "Authorization: Bearer ${SHARKO_TOKEN}"
   ```

   Success indicator: 200 with `{"reachable": true, "version": "..."}`.

2. **If the secret exists at a different path, override
   `secret_path` on the cluster.** This is the lightest-weight fix —
   no IAM changes, no SM layout migration. Run:

   ```sh
   CLUSTER=<failing-cluster-name>
   ACTUAL_PATH=<path-from-diagnosis-step-3>
   curl -sS -X PATCH "http://sharko/api/v1/clusters/$CLUSTER" \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     -H "Content-Type: application/json" \
     -d "{\"secret_path\":\"$ACTUAL_PATH\"}"
   ```

   The cluster's `secret_path` field overrides the prefix-based
   default for THIS cluster only. Other clusters continue to use
   the configured prefix. Sharko re-reads the override on the next
   `GetCredentials` call.

   Success indicator: re-run `POST /clusters/{name}/test` and see
   200.

3. **If the secret genuinely doesn't exist, create it.** Mint at the
   expected path (the first entry in `Tried:`, i.e.
   `<prefix><cluster-name>`):

   For a raw kubeconfig:

   ```sh
   PREFIX=$(kubectl -n <sharko-ns> get deployment sharko \
     -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_PROVIDER_PREFIX")].value}')
   CLUSTER=<failing-cluster-name>
   PATH_=${PREFIX}${CLUSTER}
   AWS_REGION=<region>

   kubectl --context "$CLUSTER" config view --raw \
     > /tmp/kubeconfig-$CLUSTER.yaml

   aws secretsmanager create-secret --region "$AWS_REGION" \
     --name "$PATH_" \
     --secret-string "$(cat /tmp/kubeconfig-$CLUSTER.yaml)"
   ```

   For an EKS structured-JSON secret (recommended, since the
   structured shape mints fresh STS tokens on every fetch — see the
   k8s-expert role file and
   [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md)):

   ```sh
   aws secretsmanager create-secret --region "$AWS_REGION" \
     --name "$PATH_" \
     --secret-string "$(jq -n \
       --arg name "$CLUSTER" \
       --arg host "https://<cluster-apiserver>" \
       --arg ca "$(kubectl --context $CLUSTER config view --raw \
         -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')" \
       --arg region "$AWS_REGION" \
       --arg role "arn:aws:iam::<account-id>:role/SharkoEKSAccess" \
       '{clusterName:$name, host:$host, caData:$ca, region:$region, roleArn:$role}')"
   ```

   Success indicator: re-run cluster test, see 200.

4. **If the Sharko-wide prefix is wrong (multiple clusters failing
   because the prefix doesn't match the layout), update the Helm
   value.** This is the fleet-wide fix when an operator deploys
   Sharko into an existing SM layout with a different convention:

   ```sh
   helm upgrade --reuse-values \
     --set "secrets.prefix=eks/" \
     sharko sharko/sharko -n <sharko-ns>
   ```

   The deployment rolls out; the new prefix is in effect on the next
   `GetCredentials` per cluster. Re-test all affected clusters.

5. **Last resort — manually fetch the secret from a different region
   if cross-region replication is configured.** If the cluster's
   secret was replicated to a secondary region but the primary region
   is the one Sharko is configured for, you can temporarily switch
   region:

   ```sh
   kubectl -n <sharko-ns> set env deployment/sharko \
     AWS_REGION=<secondary-region>
   kubectl -n <sharko-ns> rollout status deployment/sharko
   ```

   This affects EVERY cluster's secret lookup; only use as a
   stopgap if the primary region is also experiencing the broader
   provider failure (see
   [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)).

---

## Root-cause patterns

### Cluster registered before the secret was created

The most common cause in greenfield deployments. An operator adds a
cluster via `sharko add-cluster prod-eu`, expecting to mint the AWS-SM
secret afterward (because the registration step is non-destructive in
their view). The first `test` operation fires before the secret
exists. The secret is created shortly after — the failure self-heals
on the next test.

Diagnostic signature: Diagnosis step 3 shows the secret IS now at the
expected path, the failure timestamp predates the secret's
`CreatedDate`.

Fix is Mitigation step 1 — re-run the operation.

### Cluster name and SM secret name diverged

The operator named the cluster one thing in Sharko
(`sharko add-cluster prod-eu`) and a different thing in AWS-SM
(`clusters/prod-eu-v2` or `eks/prod-eu`). The two halves of the
identity weren't kept in sync.

Diagnostic signature: Diagnosis step 3 returns a similarly-named
secret at a different path. SearchSecrets suggestions (if permitted)
list the alternate name.

Fix is Mitigation step 2 — `secret_path` override per cluster.

### Sharko deployed into an existing SM layout with a different prefix convention

A platform team adopted Sharko into an account where AWS-SM secrets
were already organized under a different convention (e.g. `eks/` not
`clusters/`). The Helm default doesn't match.

Diagnostic signature: every cluster fails with the same not-found
shape, and the actual secrets are all under a different prefix.

Fix is Mitigation step 4 — update `secrets.prefix` to match the
existing convention.

### Secret was deleted out-of-band

An IAM cleanup, a CloudTrail-tracked deletion, or a Terraform
`destroy` removed the secret. The cluster reference still exists in
Sharko's GitOps state (the values file is still in the repo and
ArgoCD still has the cluster registered), but the credential source
is gone.

Diagnostic signature: Diagnosis step 3 returns zero matches; the
secret used to exist (CloudTrail `DeleteSecret` event), and the
cluster used to work (Sharko's previous tests succeeded).

Fix is Mitigation step 3 — recreate the secret. Audit who deleted it
and add the appropriate guardrail (deletion-protection on critical
secrets, OPA policy to require justification).

### Cross-region drift after a region failover

The cluster's secret was replicated to a secondary region during a
DR exercise but Sharko remained pointed at the primary region. The
primary region's secret was then deleted (cleanup); Sharko's lookup
fails because it's looking in the wrong region.

Diagnostic signature: Diagnosis step 3 in the primary region returns
zero matches; same query in the secondary region returns a match.

Fix is Mitigation step 5 (temporary) followed by Mitigation step 3
(recreate in the primary region) for durability.

---

## Prevention

- **Monitoring — per-cluster credential-fetch failure counter.** A
  V2-3.x follow-up metric `sharko_provider_get_credentials_errors_total{cluster,
  provider, reason}` with `reason="not_found"` would surface this
  failure mode directly. Alert on count > 0 sustained for >30min as
  a P2 ticket (operator can't repair without inspecting the cluster).

- **Gating — `sharko add-cluster` should pre-flight the secret
  existence.** A v2 follow-up: before creating the values file and
  registering with ArgoCD, the add-cluster handler calls
  `provider.GetCredentials(name)` and rejects the request if the
  secret is missing. Catches "registered before secret existed" at
  registration time instead of first-test time. Today, the add
  flow is fire-and-forget; the test loop catches the gap later.

- **Documentation — prefix convention in the install guide.** The
  installation guide should explicitly call out the default
  `secrets.prefix` and the operator pattern: secrets must exist at
  `<prefix><cluster-name>` (or override per-cluster). Many of the
  not-found failures trace to operators who didn't realize there
  was a prefix at all.

- **Gating — IAM deletion protection on cluster credential
  secrets.** Sharko-managed secrets should have
  `secretsmanager:DeleteSecret` denied for the operator role; deletes
  require manual escalation. Prevents "secret deleted out-of-band"
  failures.

- **Scheduled work — quarterly secret-existence audit.** A CronJob
  (or a periodic Sharko endpoint) that calls `GetCredentials` for
  every managed cluster and reports any not-found path. Catches
  drift before the first user-visible failure.

---

## Related runbooks

- [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md)
  — adjacent IAM failure on the SearchSecrets helper path (the API
  response loses the helpful "suggestions" field).
- [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
  — the sibling failure for the K8s-Secrets provider (same shape,
  different backend).
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — escalate here if EVERY cluster's secret lookup fails (provider
  itself is down, not per-cluster).
- [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
  — adjacent EKS-shape failure: the secret WAS found, but minting an
  EKS STS token from it failed.
- [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  — fleet-wide cluster-registration alert; this runbook is a
  sub-cause feeding it.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — request_id correlation pattern.

## Escalation

If Mitigation steps 1-3 don't restore the cluster's credential fetch
AND the cluster is on the critical path, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The cluster name and the `Tried:` paths from Diagnosis step 2
- The output of Diagnosis step 3 (similar secrets in SM)
- The output of Diagnosis step 4 (configured prefix)
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Most
not-found failures are operator-correctable via Mitigation steps 1-4;
escalation is rare.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (4 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (5 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] Alert names referenced (SharkoClusterRegistrationFastBurn)
-->
