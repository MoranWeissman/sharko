# AWS Secrets Manager — Search AccessDenied

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The
> graceful-degradation pattern at
> `internal/providers/aws_sm.go:168-175` is verified against the
> source: `SearchSecrets` calls `searchSimilar`, and if `ListSecrets`
> fails (typically `AccessDeniedException`), the Warn log line
> `"[provider] SearchSecrets failed (likely AccessDenied,
> returning empty)"` fires (line 171) and the function returns an
> empty result set with nil error. The `searchSimilar` helper at
> line 179 wraps `secretsmanager:ListSecrets` with a per-page
> name-filter. Re-verify when SearchSecrets degradation behavior or
> the underlying paginator usage changes.

A single IAM role for the Sharko pod is missing the
`secretsmanager:ListSecrets` permission. Sharko's AWS-SM provider
calls `ListSecrets` from `searchSimilar` as a helper to surface
"similar secret name" suggestions when a `GetCredentials` lookup
fails. The primary registration / test flow (using `GetSecretValue`,
which needs only `secretsmanager:GetSecretValue`) is **unaffected** —
clusters that ARE at the expected secret path continue to work
normally.

What the operator loses: the cluster-discover endpoint
(`POST /api/v1/clusters/discover`) can't enumerate clusters in
AWS-SM because `ListSecrets` is the underlying call. And when a
`POST /clusters/{name}/test` fails on a not-found, the response is
missing the "similar secrets" suggestions field that would otherwise
help the operator diagnose. This is the canonical "degraded
discovery" failure — registration of named clusters works; discovery
of cluster inventory doesn't.

This is **not** an outage — fleet operations continue. But operators
notice it sharply: the discovery UI looks broken, the
not-found error responses lose their suggestions field, and the
cluster onboarding flow that previously surfaced "did you mean X?"
hints is silent. The fix is an IAM policy change to add
`secretsmanager:ListSecrets`. This runbook walks operators through
confirming AccessDenied is the cause and applying the policy update
safely.

---

## Symptoms

What an operator sees when this fires:

- **Sharko logs the warn line on every call that triggers
  SearchSecrets**:

  ```
  {"time":"...","level":"WARN","msg":"[provider] SearchSecrets failed (likely AccessDenied, returning empty)","query":"<cluster-name-or-prefix>","error":"operation error Secrets Manager: ListSecrets, https response error StatusCode: 400, RequestID: ..., AccessDeniedException: User: arn:aws:sts::<account>:assumed-role/SharkoIRSARole/i-... is not authorized to perform: secretsmanager:ListSecrets on resource: *"}
  ```

  The `error` field carries the AWS SDK's full message, including
  the IAM role ARN and the specific action denied. The warning fires
  on every SearchSecrets invocation; if many GetCredentials calls
  fail in a window, this log line repeats frequently.

- **`POST /api/v1/clusters/discover` returns an empty cluster list
  or 500**:

  ```sh
  curl -sS -X POST http://sharko/api/v1/clusters/discover \
    -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"provider":"aws-sm","region":"us-east-1"}'
  ```

  Returns `{"clusters":[]}` (when discovery falls back gracefully) or
  500 with the AccessDenied error (when discovery requires
  ListSecrets to enumerate).

- **`POST /api/v1/clusters/{name}/test`** for a not-found cluster
  returns the not-found error but WITHOUT the "Similar secrets:"
  field — the suggestion enrichment is silently empty:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"secret for cluster \"prod-eu\" not found in AWS Secrets Manager. Tried: clusters/prod-eu, prod-eu. Set secret_path on the cluster to specify the exact name"}
  ```

  (No "Similar secrets in your SM:" field in the body. Compare
  against
  [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md), which
  shows what the suggestions look like when ListSecrets is
  permitted.)

- **The Marketplace / discover UI** shows an empty cluster list when
  the operator clicks "Discover existing clusters in AWS-SM" with
  no actionable error — the empty result and the
  graceful-degradation pattern hide the IAM gap from the UI.

- **Cluster registration of a NAMED cluster continues to work** as
  long as the cluster's secret IS at the configured path. Operators
  who only ever do `sharko add-cluster <name>` (not discovery) may
  not notice the gap until they try discovery for the first time.

- **No specific Prometheus alert fires.** This degrades the
  discovery UX, not the registration / addon-cycle SLOs.

If the symptom is "GetCredentials itself fails with AccessDenied"
(NOT SearchSecrets), this is a different failure — IAM is missing
`secretsmanager:GetSecretValue` too, which is a bigger problem. See
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
for the fleet-wide variant.

---

## Diagnosis

Three checks. Step 1 confirms it's the SearchSecrets path
specifically. Step 2 reads off the role ARN from the AWS error
message. Step 3 inspects the role's attached policies to confirm
the missing action.

### 1. Confirm AccessDenied fires on SearchSecrets but GetCredentials works

```sh
# Trigger a discover and watch the log:
curl -sS -X POST http://sharko/api/v1/clusters/discover \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  -d '{"provider":"aws-sm","region":"us-east-1"}' &

# In another shell:
kubectl -n <sharko-ns> logs -l app=sharko --since=2m \
  | jq -c 'select(.msg | test("SearchSecrets|GetCredentials|ListSecrets|GetSecretValue"; "i"))' \
  | jq -c '{time, level, msg, error}'
```

Expected pattern:

- A `WARN` line for `SearchSecrets failed (likely AccessDenied,
  returning empty)` — confirms this runbook applies.
- No corresponding `ERROR` for `GetSecretValue` (the primary
  fetch path is healthy).

If you ALSO see `GetSecretValue` failing with AccessDenied, the IAM
gap is broader; see
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

### 2. Read off the IAM role ARN from the error

The Warn log's `error` field carries the AWS SDK's full AccessDenied
message, including the role ARN that was denied:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c 'select(.msg | test("SearchSecrets failed"))' \
  | jq -r '.error' \
  | head -1 \
  | grep -oE 'arn:aws:[^:]+:[^:]*:[0-9]+:[^ ]+'
```

You should see the role ARN like
`arn:aws:sts::123456789012:assumed-role/SharkoIRSARole/i-...`. Extract
just the role name (between `assumed-role/` and the next `/`):

```sh
ROLE_NAME=SharkoIRSARole  # the role between "assumed-role/" and "/i-..."
```

This is the IAM role assumed by the Sharko pod via IRSA — the role
that needs the policy update.

### 3. Inspect the role's policies to confirm `ListSecrets` is missing

From a workstation with AWS CLI access to the account:

```sh
# List attached managed policies:
aws iam list-attached-role-policies --role-name "$ROLE_NAME" \
  --query 'AttachedPolicies[].PolicyArn' --output text

# List inline policies:
aws iam list-role-policies --role-name "$ROLE_NAME" \
  --query 'PolicyNames' --output text

# For each policy, fetch the actions:
aws iam get-policy --policy-arn <arn> \
  --query 'Policy.DefaultVersionId' --output text
aws iam get-policy-version --policy-arn <arn> --version-id <v> \
  --query 'PolicyVersion.Document' --output json \
  | jq '.Statement[] | .Action'
```

You should see `secretsmanager:GetSecretValue` (since
GetCredentials works) but NOT `secretsmanager:ListSecrets`. If
ListSecrets IS present, the policy is correct but the role might
have a resource scope ("Resource": "arn:aws:secretsmanager:...:secret:specific/*")
that excludes the broader ListSecrets call (which operates on `*`).

---

## Mitigation (try in order)

1. **Add `secretsmanager:ListSecrets` to the IAM role's policy.**
   This is the cleanest fix. If the role has an inline policy named
   `sharko-secrets-manager-access` (the chart-default name), patch
   it:

   ```sh
   ROLE_NAME=SharkoIRSARole  # from Diagnosis step 2
   POLICY_NAME=sharko-secrets-manager-access

   # Build a corrected policy document:
   cat > /tmp/sharko-policy.json <<'EOF'
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": [
           "secretsmanager:GetSecretValue",
           "secretsmanager:DescribeSecret",
           "secretsmanager:ListSecrets",
           "eks:DescribeCluster"
         ],
         "Resource": "*"
       }
     ]
   }
   EOF

   aws iam put-role-policy --role-name "$ROLE_NAME" \
     --policy-name "$POLICY_NAME" \
     --policy-document file:///tmp/sharko-policy.json
   ```

   IAM changes take effect within seconds (no AWS-side cache). Verify
   by re-running the discover endpoint from Diagnosis step 1; the
   Warn log line stops firing.

2. **If the role's policy is managed by a separate IaC tool
   (Terraform, CloudFormation, CDK) — update the source-of-truth,
   not the role directly.** Otherwise the next IaC apply reverts
   your manual change.

   For Terraform:

   ```hcl
   data "aws_iam_policy_document" "sharko_secrets" {
     statement {
       effect = "Allow"
       actions = [
         "secretsmanager:GetSecretValue",
         "secretsmanager:DescribeSecret",
         "secretsmanager:ListSecrets",   # <-- add this line
         "eks:DescribeCluster",
       ]
       resources = ["*"]
     }
   }
   ```

   Apply via the team's normal Terraform pipeline. Document the
   change in your runbook.

3. **If you want to restrict `ListSecrets` to a specific path
   prefix, use a Resource constraint.** AWS-SM supports
   ARN-pattern-based Resource scoping. The risk is that
   `ListSecrets` operates on the account-wide secret list — most
   resource patterns either match all secrets (which is what you
   want) or none (which blocks discovery). The simplest correct
   pattern:

   ```json
   {
     "Effect": "Allow",
     "Action": "secretsmanager:ListSecrets",
     "Resource": "*"
   }
   ```

   `ListSecrets` doesn't accept per-secret ARN resource scoping in
   the same way `GetSecretValue` does — it's account-level. If your
   org's IAM standards forbid `Resource: "*"`, work with your IAM
   policy reviewer to confirm `ListSecrets` is a list-API exception.

4. **Accept the degraded UX and skip discovery.** If the IAM
   change requires a long review cycle and your operators only ever
   register named clusters (no discover-then-pick UI), the current
   behavior is functional. Document the gap so operators know "the
   suggestions field in the not-found error is permanently empty"
   and "discovery returns empty" — both expected, not bugs.

   This is a deliberate trade-off, NOT a permanent recommendation.
   Track an issue to add the policy as soon as the IAM review
   clears.

5. **Last resort — switch to the K8s-Secrets provider for the
   credential read path.** If AWS-SM IAM is permanently restrictive
   and you can't add `ListSecrets`, but you can use a K8s-Secret
   alternative, the K8s-Secrets provider has different RBAC
   semantics (`list secrets` in a single namespace, NOT account-wide
   IAM). See [the k8s-expert role file](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/k8s-expert.md)
   for the K8s-Secrets provider config and
   [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
   for the sibling not-found failure mode.

---

## Root-cause patterns

### IAM role provisioned without `ListSecrets`

The most common cause. The IAM role for the Sharko pod was created
from an older Helm chart values reference (or hand-written by
following early documentation) that only granted `GetSecretValue`.
Discovery wasn't tested at install time; the gap surfaces the first
time an operator clicks "Discover."

Diagnostic signature: Diagnosis step 3's policy dump shows
`GetSecretValue` but not `ListSecrets`. The IAM role's
creation timestamp aligns with Sharko's initial install.

Fix is Mitigation step 1 (or 2 if Terraform-managed).

### IAM policy tightened by a security review

An org-wide IAM cleanup removed `ListSecrets` from the role because
the policy reviewer saw it as overly broad (it operates on `*`). The
reviewer didn't realize the discover UX depended on it.

Diagnostic signature: Mitigation step 1's pre-change policy dump
shows the action used to be present (CloudTrail
`PutRolePolicy` event shows the modification); discover used to
work.

Fix is Mitigation step 2 (re-add the action via IaC) plus a
documented conversation with the policy reviewer explaining the
discovery feature.

### Cross-account role missing `ListSecrets` in the target account

Operators with multi-account AWS setups sometimes assume an
account-B role from account A (Sharko's IRSA role assumes
`AccountBRole` to read account B's secrets). The cross-account
trust permits AssumeRole, but the `AccountBRole`'s policy in
account B doesn't include `ListSecrets`.

Diagnostic signature: Diagnosis step 2's ARN shows
`assumed-role/AccountBRole/...` — a different role from the
Sharko pod's IRSA role. Diagnosis step 3 against AccountBRole
confirms the missing action.

Fix is Mitigation step 1, applied to AccountBRole in account B (not
the Sharko IRSA role in account A).

---

## Prevention

- **Monitoring — AccessDenied counter on SearchSecrets path.** A
  V2-3.x follow-up metric
  `sharko_provider_search_errors_total{provider="aws-sm",
  reason="access_denied"}` would let operators see at a glance that
  IAM is degraded. Today, the only signal is the Warn log line,
  which silently fires on every call.

- **Gating — startup IAM-check probe.** At startup, Sharko could
  call `ListSecrets` once and emit a startup log warning if the
  call fails:
  `"[startup] AWS-SM SearchSecrets unavailable — discovery will
  return empty. Add secretsmanager:ListSecrets to the IRSA role."`
  Catches the misconfiguration before the operator notices the empty
  discover UI. This is a v2 follow-up.

- **Documentation — IAM policy in the install guide.** The Sharko
  install guide should ship the full IAM policy (all four actions:
  `GetSecretValue`, `DescribeSecret`, `ListSecrets`,
  `eks:DescribeCluster`) as a copy-paste block. Many of the
  degraded-discovery failures trace to operators who set up Sharko
  with the smaller "GetSecretValue only" policy from an older guide.

- **Gating — Helm chart documentation update.** The chart's
  values.yaml comments should explicitly call out which AWS actions
  are required for which feature ("GetSecretValue: registration,
  cluster test, refresh. ListSecrets: discovery, suggestions in
  error responses. eks:DescribeCluster: discover EKS clusters by
  ARN."). Operators picking a minimum policy then know the trade-off.

- **Scheduled work — semi-annual IAM review.** Once or twice a year,
  the platform team should verify the IRSA role still has all four
  actions. Drift catches via a documented check rather than a user
  report.

---

## Related runbooks

- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  adjacent runbook: when GetCredentials fails with not-found, this
  AccessDenied runbook explains why the suggestions field is empty.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — P0 escalation: every AWS-SM call fails, not just SearchSecrets.
- [`eks-discover-failed.md`](eks-discover-failed.md) — adjacent
  discovery failure: EKS discovery (not AWS-SM discovery) failed
  for a role.
- [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
  — sibling backend with different RBAC semantics.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If the IAM policy update requires escalation through a security
review and operators are blocked on discovery, file a follow-up
ticket internally; email the maintainer only if the
graceful-degradation behavior in the source seems wrong (e.g. discover
is returning 500 instead of empty when SearchSecrets fails):
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The role ARN and the IAM policy you propose to apply
- Whether GetCredentials (the primary path) is also failing
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. This failure
mode is operator-correctable in nearly all cases; escalation is rare.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (3 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (3 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email; redacted-role-arn placeholder
- [x] No alert names applicable (silent degradation)
-->
