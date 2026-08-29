# AWS Secrets Manager — Search AccessDenied

**Severity:** P1

> **Verified:** Re-authored 2026-08-11 after the provider-error fix.
> Searching AWS Secrets Manager still degrades gracefully — the search
> fails, the Warn line fires, and the search returns an empty result with
> no error, so the primary fetch path is untouched. What changed: that
> Warn line no longer logs the AWS SDK error's value. It now carries
> `query`, `prefix` and `step=list-secrets`.
>
> One more change worth knowing about on this page: the secret-name
> suggestions are now offered only when the secret is genuinely ABSENT,
> decided by a marker Sharko sets where AWS returned
> `ResourceNotFoundException`. Before, Sharko searched the error text for
> the words "not found", so an **AccessDenied whose message happened to
> contain those words offered suggestions too** — sending the operator to
> hunt a typo when the real problem was the missing IAM permission this
> page is about. That no longer happens, and it is an improvement.
> Reviewed 2026-08-29 — wording only; no step in this runbook changed.

A single IAM role for the Sharko pod is missing the
`secretsmanager:ListSecrets` permission. Sharko's AWS-SM provider
calls `ListSecrets` from `searchSimilar` as a helper to surface
"similar secret name" suggestions when a `GetCredentials` lookup
fails. The primary registration / test flow (using `GetSecretValue`,
which needs only `secretsmanager:GetSecretValue`) is **unaffected** —
clusters that ARE at the expected secret path continue to work
normally.

What the operator loses: `GET /api/v1/clusters/available` (which
cross-references the configured secrets backend against ArgoCD) still
enumerates clusters using the primary `GetSecretValue`/list-by-prefix
path, but when a `POST /clusters/{name}/test` fails on a not-found,
the response is missing the "similar secrets" suggestions field that
would otherwise help the operator diagnose. This is a degraded
diagnostics failure — registration and testing of named clusters
works; the "did you mean X?" hint on a not-found test doesn't.

This is **not** an outage — fleet operations continue. But operators
notice it: not-found error responses lose their suggestions field, and
the cluster onboarding flow that previously surfaced "did you mean X?"
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
  {"time":"...","level":"WARN","msg":"[provider] SearchSecrets failed (likely AccessDenied, returning empty)","query":"<cluster-name-or-prefix>","prefix":"clusters/","step":"list-secrets"}
  ```

  **The `error` field is gone, and that is the fix.** This line used to
  carry the AWS SDK's full message — which meant it also carried
  whatever else that message happened to contain, and an AWS SDK error
  can carry credential material in its own text. What you get instead
  is the `query` (a cluster name you already sent), the configured
  `prefix`, and `step=list-secrets`.

  The role ARN used to be read out of that error field. Diagnosis step 2
  below gets it from the pod itself instead, which is a better source
  anyway: it reports the identity Sharko is using RIGHT NOW, not the one
  in whichever log line you happened to grep.

  The warning fires on every SearchSecrets invocation; if many
  GetCredentials calls fail in a window, this line repeats.

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

- **Cluster registration of a NAMED cluster continues to work** as
  long as the cluster's secret IS at the configured path. Operators
  who only ever do `sharko add-cluster <name>` may not notice the gap
  until a test against a mistyped/missing cluster name comes back
  without suggestions.

- **No specific Prometheus alert fires.** This degrades a diagnostic
  hint, not the registration / addon-cycle SLOs.

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
# Trigger a test against a cluster name that doesn't exist at the
# configured secret path — this is what makes the handler call
# SearchSecrets to look for suggestions:
curl -sS -X POST http://sharko/api/v1/clusters/typo-cluster-name/test \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" &

# In another shell:
kubectl -n <sharko-ns> logs -l app=sharko --since=2m \
  | jq -c 'select(.msg | test("SearchSecrets|GetCredentials|ListSecrets|GetSecretValue"; "i"))' \
  | jq -c '{time, level, msg, cluster, query, prefix, step}'
```

(There is no `error` field on these lines any more — see Symptoms. The
fields above are what Sharko actually carries.)

Expected pattern:

- A `WARN` line for `SearchSecrets failed (likely AccessDenied,
  returning empty)` with `step: "list-secrets"` — confirms this runbook
  applies.
- No corresponding `ERROR` for the credential fetch itself (the primary
  path is healthy). A credential-fetch failure would show up as
  `[provider] GetCredentials failed` with its own `step`.

If the credential fetch is ALSO failing, the IAM gap is broader; see
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

### 2. Get the IAM role ARN from the pod, not from a log line

This step used to grep the role ARN out of the Warn line's `error`
field. That field is gone (see Symptoms), so ask the pod who it is —
which is a better answer anyway, because it is the identity Sharko is
using right now:

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- aws sts get-caller-identity
```

Expected output names the assumed role:

```json
{
  "UserId": "AROAEXAMPLEID:botocore-session-1234567890",
  "Account": "123456789012",
  "Arn": "arn:aws:sts::123456789012:assumed-role/SharkoIRSARole/botocore-session-1234567890"
}
```

Take the role name from between `assumed-role/` and the next `/`:

```sh
ROLE_NAME=$(kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws sts get-caller-identity --query Arn --output text \
  | sed -E 's#.*assumed-role/([^/]+)/.*#\1#')
echo "$ROLE_NAME"
```

Cross-check against the Service Account annotation, which is where the
role is actually configured:

```sh
SA=$(kubectl -n <sharko-ns> get pod -l app=sharko \
  -o jsonpath='{.items[0].spec.serviceAccountName}')
kubectl -n <sharko-ns> get sa "$SA" \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'
```

These two should name the same role. If they disagree, the pod has not
been restarted since the annotation changed — restart it before
spending time on IAM policy.

You can also confirm the denial yourself, and get AWS's own reason
first-hand rather than through Sharko:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws secretsmanager list-secrets --max-results 1
```

An `AccessDeniedException` naming `secretsmanager:ListSecrets` confirms
this runbook applies, and it names the role in AWS's own words.

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
           "secretsmanager:ListSecrets"
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
   by re-running the test from Diagnosis step 1; the Warn log line
   stops firing and the not-found response includes suggestions.

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
   want) or none (which blocks the suggestion lookup). The simplest
   correct pattern:

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

4. **Accept the degraded diagnostics and move on.** If the IAM
   change requires a long review cycle, the current behavior is
   functional — clusters at their expected secret path continue to
   register and test normally. Document the gap so operators know
   "the suggestions field on a not-found test is permanently empty"
   is expected, not a bug.

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
reviewer didn't realize the cluster-test suggestions feature depended
on it.

Diagnostic signature: Mitigation step 1's pre-change policy dump
shows the action used to be present (CloudTrail
`PutRolePolicy` event shows the modification); the suggestions field
used to populate.

Fix is Mitigation step 2 (re-add the action via IaC) plus a
documented conversation with the policy reviewer explaining the
suggestions feature.

### Cross-account role missing `ListSecrets` in the target account

Operators with multi-account AWS setups sometimes assume an
account-B role from account A (Sharko's IRSA role assumes
`AccountBRole` to read account B's secrets). The cross-account
trust permits AssumeRole, but the `AccountBRole`'s policy in
account B doesn't include `ListSecrets`.

Diagnostic signature: Diagnosis step 2's `sts get-caller-identity`
shows `assumed-role/AccountBRole/...` — a different role from the one on
the pod's Service Account annotation. Diagnosis step 3 against AccountBRole
confirms the missing action.

Fix is Mitigation step 1, applied to AccountBRole in account B (not
the Sharko IRSA role in account A).

---

## Prevention

- **Monitoring — AccessDenied counter on SearchSecrets path.** Sharko
  does not export this metric today. The alert below is a design sketch
  for a future release, not something you can deploy now. The sketch:
  `sharko_provider_search_errors_total{provider="aws-sm",
  reason="access_denied"}`, letting operators see at a glance that IAM
  is degraded. Today the only signal is the Warn log line, which
  silently fires on every call. Note the label set has no dimension
  taken from the AWS error text — a metric label built from provider
  error text is both unbounded cardinality and a leak.

- **Gating — startup IAM-check probe.** At startup, Sharko could
  call `ListSecrets` once and emit a startup log warning if the
  call fails:
  `"[startup] AWS-SM SearchSecrets unavailable — not-found test
  results will have no suggestions. Add secretsmanager:ListSecrets
  to the IRSA role."`
  Catches the misconfiguration before the operator notices the empty
  suggestions field. This is a v2 follow-up.

- **Documentation — IAM policy in the install guide.** The Sharko
  install guide should ship the full IAM policy (`GetSecretValue`,
  `DescribeSecret`, `ListSecrets`) as a copy-paste block. Many of the
  degraded-suggestions failures trace to operators who set up Sharko
  with the smaller "GetSecretValue only" policy from an older guide.

- **Gating — Helm chart documentation update.** The chart's
  values.yaml comments should explicitly call out which AWS actions
  are required for which feature ("GetSecretValue: registration,
  cluster test, refresh. ListSecrets: not-found suggestions in test
  error responses."). Operators picking a minimum policy then know
  the trade-off.

- **Scheduled work — semi-annual IAM review.** Once or twice a year,
  the platform team should verify the IRSA role still has both
  actions. Drift catches via a documented check rather than a user
  report.

---

## Related runbooks

- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  adjacent runbook: when GetCredentials fails with not-found, this
  AccessDenied runbook explains why the suggestions field is empty.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — P0 escalation: every AWS-SM call fails, not just SearchSecrets.
- [`k8s-secrets-not-found-in-namespace.md`](k8s-secrets-not-found-in-namespace.md)
  — sibling backend with different RBAC semantics.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If the IAM policy update requires escalation through a security
review, file a follow-up ticket internally; email the maintainer only
if the graceful-degradation behavior in the source seems wrong (e.g.
a cluster test is returning 500 instead of a normal not-found error
when SearchSecrets fails): `moran.weissman@gmail.com`. Include:

- This runbook URL
- The role ARN and the IAM policy you propose to apply
- The output of Diagnosis step 2's `sts get-caller-identity` and the
  `list-secrets` probe — that is AWS's own reason, first-hand
- Whether GetCredentials (the primary path) is also failing
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. This failure
mode is operator-correctable in nearly all cases; escalation is rare.
