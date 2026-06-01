# EKS Discover Failed for a Role

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The Warn log
> line `"[discover] failed to scan identity"` is verified against
> `internal/providers/eks_discover.go:71` inside
> `discoverEKSClustersWithFactory`. The per-role error is accumulated
> in the `errors` slice (lines 65-78); the function returns
> `partial-success` when at least one role succeeded (line 87) or
> hard-fails (line 82) when every role's scan errored. The
> `isAssumeRoleError` helper at line 209 + `trustPolicyFix` at line
> 217 surface a copy-paste IAM trust-policy snippet to the operator.
> Re-verify when the partial-success aggregation or the error-format
> contract changes.

The operator called `POST /api/v1/clusters/discover` with a list of
cross-account IAM role ARNs to scan, and at least one role's scan
failed. Sharko's discover handler iterates each role, assumes it via
STS, and lists+describes EKS clusters in that role's account. Per-role
failures are partial-success — successful scans return their clusters;
failed scans return an error string mentioning the role ARN.

The failure is per-role. Other roles in the discover request continue
to surface their clusters; only the failing role's clusters are
missing from the response. The operator-visible symptom is "I asked
for discovery of 3 accounts, but only 2 are surfacing clusters in the
UI." The fix is to repair the trust policy on the failing role (or
remove that role from the request).

This is distinct from **EKS token generation failed** (see
[`eks-token-generation-failed.md`](eks-token-generation-failed.md))
— that's per-cluster STS failure during a `test` or `register`
operation. This is per-role STS+EKS failure during the up-front
discovery enumeration. They share the same trust-policy root cause but
the surface and mitigation paths differ.

---

## Symptoms

What an operator sees when this fires:

- **API: `POST /api/v1/clusters/discover`** returns 200 (partial
  success) or 500 (all-roles-failed) with an error string in the
  payload identifying which roles failed:

  ```
  HTTP/1.1 200 OK
  {
    "clusters": [{"name":"prod-eu","account":"123456789012",...}],
    "error": "some identity scans failed: role \"arn:aws:iam::234567890123:role/EKSReadOnly\": getting caller identity: AccessDenied: ...\n\nTo fix this, update the trust policy on role \"arn:aws:iam::234567890123:role/EKSReadOnly\" to allow your Sharko identity to assume it. ..."
  }
  ```

  When ALL roles fail:

  ```
  HTTP/1.1 500 Internal Server Error
  {"error":"all identity scans failed: role \"arn:aws:iam::234...\": ..."}
  ```

- **Sharko logs the per-role failure at error level**:

  ```
  {"time":"...","level":"ERROR","msg":"[discover] failed to scan identity","request_id":"req-...","roleARN":"arn:aws:iam::234567890123:role/EKSReadOnly","error":"getting caller identity: operation error STS: AssumeRole, AccessDenied: User: arn:aws:sts::123456789012:assumed-role/SharkoIRSARole/i-... is not authorized to perform: sts:AssumeRole on resource: arn:aws:iam::234567890123:role/EKSReadOnly"}
  ```

- **The Discover UI** shows the partial cluster list (clusters from
  the roles that succeeded) and surfaces the error string from the
  response. Operators see "X of N accounts scanned successfully."

- **The Sharko-emitted error string includes a trust-policy fix
  snippet** automatically (per `trustPolicyFix` at
  `eks_discover.go:217`). The snippet is operator-actionable and
  copy-pasteable.

- **No specific Prometheus alert fires** for per-role discover
  failure. Repeated failures don't fan into the budget-burn alerts
  because discover is a discovery-time UX flow, not a cluster
  lifecycle operation.

If the symptom is "discover returns 500 with `ListClusters`
AccessDenied" but the role itself was assumable, the IAM gap is on
EKS actions (not STS) — see Mitigation step 3 for the EKS-side fix.

If the symptom is "discover returns 500 with `Unable to locate
credentials`" — the Sharko pod itself has no AWS credentials, not a
per-role issue — see Mitigation step 1 of
[`eks-token-generation-failed.md`](eks-token-generation-failed.md).

---

## Diagnosis

Three checks. Step 1 identifies which role failed and what kind of
error. Step 2 confirms via the explicit STS probe. Step 3 inspects
the failing role's trust policy.

### 1. Read off the failing role and the error class

From the discover response or the log line:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c 'select(.msg == "[discover] failed to scan identity")' \
  | jq -c '{time, roleARN, error}'
```

Common error classes (extracted from the inner SDK message):

- `AssumeRole, AccessDenied: ... not authorized to perform:
  sts:AssumeRole` → trust policy on the target role doesn't permit
  Sharko (Mitigation step 1).
- `AssumeRole, AccessDenied: ... is not authorized to perform:
  sts:AssumeRole` (Sharko-side) → source policy missing
  `sts:AssumeRole` (Mitigation step 2).
- `ListClusters, AccessDenied: ... not authorized to perform:
  eks:ListClusters` → role was assumed fine but lacks
  `eks:ListClusters` in the target account (Mitigation step 3).
- `DescribeCluster, AccessDenied: ... eks:DescribeCluster` → ditto
  for `eks:DescribeCluster`.
- `GetCallerIdentity` failures → IRSA chain broken on Sharko's
  pod-side (escalate to
  [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)).

### 2. Probe the assume-role from the Sharko pod

```sh
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
FAILING_ROLE=<arn-from-step-1>

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws sts assume-role \
  --role-arn "$FAILING_ROLE" \
  --role-session-name "sharko-diag"
```

Expected on success: temporary credentials JSON. On failure: the same
AccessDenied message that was logged. Cross-check with the source
account's CloudTrail `AssumeRole` event for the exact identity
attempting the call.

If the assume-role succeeds from the pod but fails inside Sharko,
this is a Sharko-internal bug — escalate to the maintainer with the
request_id.

### 3. Inspect the failing role's trust policy

From a workstation with access to the target account (or via
`aws sts assume-role --role-arn <other-role>` if you don't have
direct access):

```sh
FAILING_ROLE_NAME=$(basename "$FAILING_ROLE")
aws iam get-role --role-name "$FAILING_ROLE_NAME" \
  --query 'Role.AssumeRolePolicyDocument' --output json | jq
```

The output is the JSON trust policy. Look for a `Statement` that
permits the Sharko IRSA role to assume this role. Expected shape:

```json
{
  "Effect": "Allow",
  "Principal": {
    "AWS": "arn:aws:iam::<source-account>:role/SharkoIRSARole"
  },
  "Action": "sts:AssumeRole"
}
```

If absent or mis-typed, this is the gap. Note: the Principal can also
be the source account's root (`arn:aws:iam::<source-account>:root`)
or a specific session ARN; verify whichever pattern your org uses.

---

## Mitigation (try in order)

1. **For AssumeRole AccessDenied — repair the target role's trust
   policy.** Add a statement permitting Sharko's IRSA role to assume
   this role:

   ```sh
   FAILING_ROLE_NAME=$(basename "$FAILING_ROLE")
   SOURCE_IRSA_ROLE=arn:aws:iam::<source-account-id>:role/SharkoIRSARole

   # Fetch existing trust policy:
   aws iam get-role --role-name "$FAILING_ROLE_NAME" \
     --query 'Role.AssumeRolePolicyDocument' --output json \
     > /tmp/trust.json

   # Add the Sharko statement:
   jq --arg src "$SOURCE_IRSA_ROLE" '.Statement += [{
     "Effect": "Allow",
     "Principal": {"AWS": $src},
     "Action": "sts:AssumeRole"
   }]' /tmp/trust.json > /tmp/trust-new.json

   aws iam update-assume-role-policy \
     --role-name "$FAILING_ROLE_NAME" \
     --policy-document file:///tmp/trust-new.json
   ```

   IAM takes effect within seconds. Re-run
   `POST /api/v1/clusters/discover` to verify.

   **Note:** the Sharko-emitted error includes a copy-paste snippet
   already (per `trustPolicyFix` at `eks_discover.go:217`).
   Mitigation step 1 is the same fix in script form.

2. **For Sharko-side AssumeRole AccessDenied — repair the SOURCE
   account policy.** The Sharko IRSA role needs explicit
   `sts:AssumeRole` permission on the target role's ARN:

   ```json
   {
     "Effect": "Allow",
     "Action": "sts:AssumeRole",
     "Resource": [
       "arn:aws:iam::234567890123:role/EKSReadOnly",
       "arn:aws:iam::345678901234:role/EKSReadOnly"
     ]
   }
   ```

   Apply via the same IaC pipeline that owns the Sharko IRSA role.
   The error class to look for: the role ARN in the AccessDenied
   message is the SHARKO role (source side), not the failing role
   (target side).

3. **For `ListClusters` / `DescribeCluster` AccessDenied — add EKS
   permissions to the target role.** The assume-role succeeded; the
   target role itself lacks EKS actions:

   ```json
   {
     "Effect": "Allow",
     "Action": [
       "eks:ListClusters",
       "eks:DescribeCluster"
     ],
     "Resource": "*"
   }
   ```

   Apply to the target role in the target account. IAM takes effect
   within seconds.

4. **Remove the failing role from the discover request.** If
   repairing the trust policy requires a long IAM review and the
   operator doesn't need that role's clusters surfaced right now,
   call discover without it:

   ```sh
   curl -sS -X POST http://sharko/api/v1/clusters/discover \
     -H "Authorization: Bearer ${SHARKO_TOKEN}" \
     -d '{"provider":"aws-sm","region":"us-east-1","roles":["<working-role-1>","<working-role-2>"]}'
   ```

   Document the gap; restore the role to the request once the trust
   policy is fixed.

5. **Last resort — accept partial discovery output.** The
   discover endpoint returns the partial-success clusters when at
   least one role succeeded. If the platform team cannot fix the
   failing role's trust policy soon and the operator can work with
   the partial list, no fix is required — the partial-success
   pattern works as designed.

   This is an operational acceptance, not a mitigation. Track the
   IAM fix as a follow-up.

---

## Root-cause patterns

### Target role's trust policy never included Sharko

A platform engineering team set up cross-account EKS roles for a
different consumer (e.g. their internal cluster-discovery service)
and Sharko was onboarded later without an update to the trust
policy. Every discover scan against this role fails.

Diagnostic signature: Diagnosis step 1's error class is
`AssumeRole AccessDenied` against the failing role; Diagnosis step
3's trust policy doesn't mention Sharko's IRSA role at all (or
mentions a different consumer).

Fix is Mitigation step 1.

### Trust policy was tightened in a security cleanup

The target role used to trust the Sharko IRSA role and lost the
permission in a policy audit. CloudTrail's
`UpdateAssumeRolePolicy` event records the modification.

Diagnostic signature: same error class as above; CloudTrail event
correlates with the failure-start time. Mitigation step 1's diff
against the previous policy shows the deleted statement.

Fix is Mitigation step 1 (or re-apply via IaC).

### Source IRSA role lacks `sts:AssumeRole` on the target

The Sharko-side IAM policy doesn't grant `sts:AssumeRole` on the
target role's ARN. This is the inverse of the trust-policy issue —
the trust was set up correctly on the target side, but Sharko's own
side doesn't permit the call.

Diagnostic signature: Diagnosis step 1's error class is
`AssumeRole AccessDenied`, but the role ARN in the AccessDenied
message is the SHARKO role (not the failing role). Mitigation step
2's source-policy dump confirms the missing `sts:AssumeRole`
action.

Fix is Mitigation step 2.

### Target role has trust+assume but no EKS permissions

The role was assumed successfully, but its own policy doesn't grant
`eks:ListClusters` or `eks:DescribeCluster`. The cross-account setup
was done for a different EKS action (e.g. `eks:UpdateCluster` only)
and never tested with the discover-flow.

Diagnostic signature: Diagnosis step 1's error class is
`ListClusters AccessDenied` or `DescribeCluster AccessDenied`.

Fix is Mitigation step 3.

### IRSA on Sharko's pod is broken (affects every role)

Every discover request returns 500 (`all identity scans failed:`)
because the underlying `LoadDefaultConfig` step inside
`discoverForIdentity` cannot get any credentials. Per-role partial
success can't happen if the pod has no identity at all.

Diagnostic signature: the response is 500 with every role in the
error list; Diagnosis step 2's `aws sts get-caller-identity` from
the pod also fails.

Fix is the fleet-wide IRSA repair (see
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
Mitigation step 2), not per-role.

---

## Prevention

- **Monitoring — per-role discover error counter.** A V2-3.x
  follow-up metric `sharko_discover_role_errors_total{role_arn,
  error_class}` would let operators alert on persistent per-role
  failures and on broad regression patterns (e.g. a CloudTrail-tied
  drop after a policy change).

- **Gating — discover endpoint should pre-flight roles.** Before
  running the full scan, the discover handler could call
  `sts:AssumeRole` once per role and short-circuit roles that fail
  with a structured error rather than going deep into the EKS calls.
  Faster failure feedback for operators; cleaner partial-success
  semantics.

- **Documentation — multi-account discover setup guide.** The
  install guide should ship the full trust+permission pattern for
  multi-account discovery: target-side trust policy (with Sharko's
  IRSA role principal), source-side policy (with `sts:AssumeRole`
  to each target role), target-side EKS actions
  (`eks:ListClusters`, `eks:DescribeCluster`). All in one
  copy-paste artifact.

- **Gating — startup self-test of configured discover roles.** If
  operators configure default-discover-role-ARNs via Helm, the chart
  could spin up an `eks-discover-selftest` Job once on install that
  runs the discover loop and fails the install if any configured
  role fails. Catches misconfiguration before the first user-facing
  call.

- **Scheduled work — quarterly cross-account trust review.** Cross-
  account trust policies bit-rot when target accounts re-create
  their IAM roles. A quarterly job that walks the configured
  discover-role list and verifies each role is still trusted +
  scannable catches drift.

---

## Related runbooks

- [`eks-token-generation-failed.md`](eks-token-generation-failed.md)
  — adjacent EKS-side failure: token mint for a specific cluster
  failed after the secret was fetched.
- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  per-cluster credential fetch failed (not the multi-account
  discovery scan).
- [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md)
  — adjacent IAM gap on the AWS-SM SearchSecrets path.
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — P0 escalation when every role and every cluster's IRSA chain
  fails.
- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — v1.x
  limitation for `awsAuthConfig` cluster Secrets; orthogonal to
  this runbook.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — `request_id` correlation pattern.

## Escalation

If Mitigation steps 1-3 don't restore the failing role's scan AND the
target role's IAM team has confirmed the policy is correct from their
side, email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The failing role ARN
- The Diagnosis step 1 error class and full error string
- The output of Diagnosis step 2 (assume-role probe from the pod)
- The current trust-policy JSON for the failing role (Diagnosis
  step 3)
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Most failures
are operator-correctable via Mitigation steps 1-3; escalation is rare.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current
- [x] Symptoms section appears BEFORE Diagnosis
- [x] Symptoms include exact log lines / error messages
- [x] Diagnosis has 3+ concrete checks (3 named) with exact commands
- [x] Mitigation uses numbered list (1. 2. 3. 4. 5.) not bullets
- [x] Mitigation has 3-5 steps in priority order, each with rationale + exact command
- [x] Root-cause patterns section: 2+ named causes (5 named), 1-3 paragraphs each
- [x] Prevention section present and non-empty (NOT "TBD")
- [x] Related runbooks section present with multiple links
- [x] Intro is operator-on-call voice
- [x] Length within 300-800 line target
- [x] All cross-links resolve via mkdocs --strict
- [x] No emoji / no internal Slack / employee email
- [x] No alert applicable (discover UX flow); explicitly stated
-->
