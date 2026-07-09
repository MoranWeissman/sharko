# EKS Token Generation Failed

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. Both error
> emission sites are verified against
> `internal/providers/aws_auth.go`: the first at line 40 wraps
> `awsconfig.LoadDefaultConfig` failure with `slog.Error("[auth] EKS
> token generation failed", ...)`; the second at line 72 wraps
> `presignClient.PresignGetCallerIdentity` failure with the same
> error msg shape. Both are returned wrapped via `fmt.Errorf` and
> bubble up to the API layer through the AWS-SM provider's
> `buildFromStructured` at `aws_sm.go:238-242`. Re-verify when
> `getEKSToken`'s error wrapping or the STS presign flow changes.

A specific EKS cluster's credential fetch failed at the AWS STS
token-mint step. The cluster's AWS-SM secret is the structured JSON
shape (`{"clusterName":..., "host":..., "caData":..., "region":...,
"roleArn":...}`); Sharko fetched it successfully, then called
`getEKSToken` to mint a short-lived bearer token via a presigned
`GetCallerIdentity` request. That presign step failed.

The failure is per-cluster. Other EKS clusters whose IRSA / roleArn
chain is intact continue to reconcile normally. Bearer-token-only
clusters (no STS step in the chain) are completely unaffected. The
fix is either to repair the IRSA setup on the Sharko pod, repair the
target cluster's IAM role (the one referenced as `roleArn` in the
secret), or update the cluster's region in AWS-SM so STS routes
correctly.

This is distinct from **AWS-SM secret not found** (see
[`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md)) — the
secret was found here; the STS mint after the fetch failed. It's
also distinct from **AWS-SM AccessDenied on Search**
([`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md))
— that's IAM on the SM-side list call; this is IAM on the STS-side
mint call.

---

## Symptoms

What an operator sees when this fires:

- **API: `POST /api/v1/clusters/{name}/test`** for the affected EKS
  cluster returns 502 / 500 with the wrapped error:

  ```
  HTTP/1.1 502 Bad Gateway
  {"error":"generating EKS token for cluster \"prod-eu\": presigning GetCallerIdentity for cluster \"prod-eu\": ..."}
  ```

  The inner error (after the colon) carries the AWS SDK's
  identification — typically `AccessDenied`, `RegionDisabled`,
  `InvalidClientTokenId`, or a config-load failure.

- **Sharko logs two related error lines** (one from each `slog.Error`
  in `aws_auth.go`):

  Config load failure (line 40):
  ```
  {"time":"...","level":"ERROR","msg":"[auth] EKS token generation failed","request_id":"req-...","cluster":"prod-eu","region":"us-east-1","error":"loading AWS config for EKS token: ..."}
  ```

  Presign failure (line 72):
  ```
  {"time":"...","level":"ERROR","msg":"[auth] EKS token generation failed","request_id":"req-...","cluster":"prod-eu","region":"us-east-1","error":"presigning GetCallerIdentity for cluster \"prod-eu\": operation error STS: GetCallerIdentity, AccessDenied: User: arn:aws:sts::<account>:assumed-role/SharkoIRSARole/i-... is not authorized to perform: sts:GetCallerIdentity..."}
  ```

- **The cluster row in the dashboard** shows status **Test failed**
  with the wrapped error in the tooltip; other EKS and non-EKS
  clusters in the fleet show **Healthy**.

- **If the failure mode is the inner `AssumeRole` step (when
  `roleArn` is set in the secret), the error wraps a `stscreds`
  failure**:

  ```
  presigning GetCallerIdentity for cluster "prod-eu": operation error STS: AssumeRole, AccessDenied: ...
  ```

- **No specific Prometheus alert fires** for a single EKS cluster's
  token failure. Repeated per-cluster failures fan into
  [`SharkoClusterRegistrationFastBurn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  /
  [`SharkoAddonCycleFastBurn`](budget-burn-runbook.md#sharkoaddoncyclefastburn)
  when sustained.

If the symptom is **every EKS cluster** fails with this shape,
investigate fleet-wide IRSA misconfiguration — see
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md).

---

## Diagnosis

Four checks. Step 1 confirms per-cluster; Step 2 identifies which of
the two error-emission sites fired; Step 3 inspects the
secret's `roleArn` and the IRSA chain; Step 4 probes STS directly from
the pod.

### 1. Confirm the failure is per-cluster, not fleet-wide

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | select(.test_error | test("EKS token|presigning|GetCallerIdentity"; "i")) | {name, test_status, test_error}'
```

If only one cluster fails this shape, single-cluster mitigation
applies (Mitigation step 2+). If multiple clusters fail at once and
they share the same `roleArn`, the cross-account role is broken
(Mitigation step 3). If every EKS cluster fails (regardless of
roleArn), the Sharko pod's IRSA itself is broken (escalate to
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)).

### 2. Distinguish config-load vs presign failure

The two error-emission sites in `aws_auth.go` map to different
mitigation lanes:

```sh
REQ_ID=req-<id-from-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" \
    'select(.request_id == $id and .msg == "[auth] EKS token generation failed")' \
  | jq -r '.error'
```

If the error starts with `loading AWS config for EKS token:` — line
40 fired. The SDK couldn't even load credentials. This is almost
always a Sharko-pod IRSA failure (Mitigation step 1).

If the error starts with `presigning GetCallerIdentity for cluster`
— line 72 fired. The SDK loaded credentials fine but the STS call
itself was denied or routed wrong. This is either a `roleArn`
trust-policy issue (Mitigation step 3) or a region-routing issue
(Mitigation step 4).

### 3. Inspect the AWS-SM secret structure to confirm the chain

```sh
CLUSTER=<failing-cluster-name>
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
AWS_REGION=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AWS_REGION")].value}')
PREFIX=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_PROVIDER_PREFIX")].value}')

kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws --region "$AWS_REGION" secretsmanager get-secret-value \
  --secret-id "${PREFIX}${CLUSTER}" \
  --query 'SecretString' --output text \
  | jq '{clusterName, host, region, roleArn, hasCAData: (.caData != null)}'
```

Verify:

- `clusterName` matches the EKS cluster's actual name in AWS
  (`aws eks describe-cluster --name <name>` should resolve).
- `region` matches the EKS cluster's region.
- `roleArn` (if present) is a real role ARN.
- `host` is the cluster's HTTPS endpoint.

Mismatched values are root causes — a stale `region` field, a typo
in `roleArn`, a `clusterName` that doesn't exist in EKS will all
produce token-mint failures with slightly different downstream
errors.

### 4. Probe STS directly from the pod

If `roleArn` is empty (pod's IRSA role is the EKS auth identity),
verify the pod's own identity resolves:

```sh
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws --region "$AWS_REGION" sts get-caller-identity
```

Expected: returns the pod's assumed role ARN. If it fails
with `Unable to locate credentials`, IRSA isn't wired (Mitigation
step 1).

If `roleArn` is set, verify the pod can assume that role:

```sh
ROLE_ARN=<from-diagnosis-step-3>
kubectl -n <sharko-ns> exec "$SHARKO_POD" -- \
  aws --region "$AWS_REGION" sts assume-role \
  --role-arn "$ROLE_ARN" --role-session-name "sharko-diag"
```

Expected: returns temporary credentials. If it fails with
`AccessDenied`, the target role's trust policy doesn't permit the
Sharko pod's IRSA role to assume it (Mitigation step 3).

---

## Mitigation (try in order)

1. **For "loading AWS config" failures (Diagnosis step 2 line 40),
   repair the Sharko pod's IRSA chain.** The pod has no AWS
   credentials at all; STS can't even start the mint.

   Verify the pod's SA annotation:

   ```sh
   SA=$(kubectl -n <sharko-ns> get pod -l app=sharko \
     -o jsonpath='{.items[0].spec.serviceAccountName}')
   kubectl -n <sharko-ns> get sa "$SA" \
     -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'
   ```

   Expected: ARN like
   `arn:aws:iam::<account-id>:role/SharkoIRSARole`. If empty:

   ```sh
   kubectl -n <sharko-ns> annotate sa "$SA" \
     "eks.amazonaws.com/role-arn=arn:aws:iam::<account-id>:role/SharkoIRSARole" \
     --overwrite
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   ```

   Success indicator: Diagnosis step 4's `sts get-caller-identity`
   succeeds; the cluster test then succeeds.

   If the SA IS annotated but the credential chain still doesn't
   load, the IAM role itself may be missing, the cluster's OIDC
   provider URL may have changed, or the role's trust policy may
   no longer trust the SA. See
   [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
   for the fleet-wide repair.

2. **For "presigning GetCallerIdentity" failures on a cluster
   without `roleArn` — the pod's IRSA role lacks the action.**
   `sts:GetCallerIdentity` is in the default AWS-managed policy
   set, but some restrictive policies omit it.

   Add the action via inline or managed policy:

   ```json
   {
     "Effect": "Allow",
     "Action": [
       "sts:GetCallerIdentity"
     ],
     "Resource": "*"
   }
   ```

   The action operates on the caller — there's no resource scoping.
   Verify by re-running Diagnosis step 4.

3. **For "presigning" failures on a cluster WITH `roleArn` — repair
   the cross-account trust policy.** The Sharko pod's IRSA role
   needs to assume `roleArn` (defined in the cluster's AWS-SM
   secret) in the target account.

   On the target account, fetch the role's current trust policy:

   ```sh
   aws iam get-role --role-name "$(basename $ROLE_ARN)" \
     --query 'Role.AssumeRolePolicyDocument' \
     --output json | jq
   ```

   Add a statement permitting the Sharko IRSA role (in the source
   account) to assume this role:

   ```json
   {
     "Effect": "Allow",
     "Principal": {
       "AWS": "arn:aws:iam::<source-account-id>:role/SharkoIRSARole"
     },
     "Action": "sts:AssumeRole"
   }
   ```

   Apply:

   ```sh
   aws iam update-assume-role-policy --role-name "$(basename $ROLE_ARN)" \
     --policy-document file:///tmp/updated-trust.json
   ```

   The Sharko IRSA role separately needs `sts:AssumeRole` on this
   role's ARN (Resource pattern). Add to the SOURCE account's
   policy:

   ```json
   {
     "Effect": "Allow",
     "Action": "sts:AssumeRole",
     "Resource": "<target-role-arn>"
   }
   ```

4. **If the secret's `region` is wrong, fix the AWS-SM record.** The
   `region` field in the structured JSON determines which STS
   endpoint Sharko routes to. A stale region (cluster was recreated
   in a different region; secret was copied from a different
   environment) produces presign failures that look like region
   issues.

   Update the secret with the correct region:

   ```sh
   CORRECT_REGION=<actual-cluster-region>
   CONFIG=$(aws secretsmanager get-secret-value \
     --secret-id "${PREFIX}${CLUSTER}" \
     --region "$AWS_REGION" \
     --query 'SecretString' --output text \
     | jq --arg r "$CORRECT_REGION" '.region = $r')

   aws secretsmanager update-secret \
     --secret-id "${PREFIX}${CLUSTER}" \
     --region "$AWS_REGION" \
     --secret-string "$CONFIG"
   ```

   Sharko re-reads the secret on the next operation.

5. **Last resort — switch this cluster to a bearer-token kubeconfig
   instead of the structured EKS-STS shape.** If you cannot repair
   the IRSA chain on a reasonable timeline and the cluster is
   critical-path, mint a long-lived ServiceAccount token on the
   target cluster and store it as a raw kubeconfig in AWS-SM (skips
   the STS mint entirely):

   ```sh
   # On the target cluster:
   kubectl create sa sharko-readonly -n kube-system
   kubectl create clusterrolebinding sharko-readonly \
     --clusterrole=view \
     --serviceaccount=kube-system:sharko-readonly
   TOKEN=$(kubectl create token sharko-readonly -n kube-system \
     --duration=8760h)

   # Build a kubeconfig:
   cat > /tmp/kc.yaml <<EOF
   apiVersion: v1
   kind: Config
   clusters:
   - cluster:
       server: <cluster-host>
       certificate-authority-data: <ca-b64>
     name: $CLUSTER
   contexts:
   - context:
       cluster: $CLUSTER
       user: $CLUSTER
     name: $CLUSTER
   current-context: $CLUSTER
   users:
   - name: $CLUSTER
     user:
       token: $TOKEN
   EOF

   # Replace the AWS-SM secret with the raw kubeconfig:
   aws secretsmanager update-secret \
     --secret-id "${PREFIX}${CLUSTER}" \
     --region "$AWS_REGION" \
     --secret-string "$(cat /tmp/kc.yaml)"
   ```

   Sharko's AWS-SM provider auto-detects raw-vs-structured (see
   `aws_sm.go:107`) and routes the raw-kubeconfig path, skipping
   `getEKSToken` entirely.

   Long-lived tokens are a security trade-off. Rotate on a cadence.

---

## Root-cause patterns

### Sharko pod's IRSA chain broken

The pod has no AWS credentials. The Service Account annotation is
missing, points at a non-existent role, or the role's trust policy
doesn't trust the cluster's OIDC provider. Every STS call fails at
config-load.

Diagnostic signature: Diagnosis step 2 line 40 fired
(`loading AWS config for EKS token`). Diagnosis step 4's
`sts get-caller-identity` fails.

Fix is Mitigation step 1 plus the broader fleet-wide repair if
needed.

### Cross-account roleArn trust policy is incorrect

The cluster's AWS-SM secret has a `roleArn` pointing at a role in a
different AWS account. The trust policy on that role doesn't permit
the Sharko pod's IRSA role to assume it (or the source account's
policy is missing `sts:AssumeRole` on the target role).

Diagnostic signature: Diagnosis step 2 line 72 fired
(`presigning GetCallerIdentity`); the error wraps
`AssumeRole` or `not authorized to perform: sts:AssumeRole`.
Diagnosis step 4's explicit `assume-role` fails.

Fix is Mitigation step 3 — update both directions of the trust.

### Stale region in the secret

The cluster was recreated in a different region; the secret was
copied between environments without updating the region. STS routes
to the wrong endpoint; `GetCallerIdentity` lands in a region that
doesn't have the IAM role active.

Diagnostic signature: Diagnosis step 3 shows a `region` that doesn't
match `aws eks describe-cluster --name <cluster> --region <real>`'s
return value.

Fix is Mitigation step 4 — update the region field.

### IAM policy tightened by a security review

The pod's IRSA role used to have `sts:GetCallerIdentity` and lost it
in a policy cleanup. The cleanup didn't anticipate Sharko's need.

Diagnostic signature: Mitigation step 2's policy dump shows
`GetCallerIdentity` absent; CloudTrail `PutRolePolicy` event
correlates with the failure-start time.

Fix is Mitigation step 2.

### EKS cluster's authentication mode misaligned

The EKS cluster was migrated from `aws-auth` configmap authentication
to EKS Access Entries (or vice versa) and the Sharko IRSA role isn't
mapped on the new auth mode. The token mints successfully but the
apiserver rejects it as unauthenticated downstream (the token mint is
fine; the use of the token is what fails).

Diagnostic signature: Token mint succeeds but the subsequent
`/version` probe (when Sharko uses the kubeconfig) returns 401.
This is technically NOT this runbook's failure mode — it surfaces
as a different downstream error. Document for completeness so
operators don't misroute.

Fix: ensure the Sharko IRSA role is mapped in the EKS cluster's
auth (via aws-auth configmap OR EKS Access Entries depending on
the cluster's mode).

---

## Prevention

- **Monitoring — per-cluster STS mint failure counter.** A V2-3.x
  follow-up metric
  `sharko_provider_eks_token_errors_total{cluster, stage, reason}`
  with stages `config_load` / `presign` / `assume_role` would
  surface this failure with full triage detail. Today, the only
  signal is the slog.Error line and the per-cluster
  `test_status` in `/api/v1/fleet/status`.

- **Gating — `sharko add-cluster` should pre-flight the STS chain.**
  Before committing the cluster registration, the add handler could
  call `provider.GetCredentials(name)` and reject the request if the
  STS mint fails. Catches the misconfiguration at registration time
  instead of first-test time.

- **Documentation — IRSA + cross-account setup guide.** The
  install guide should ship the full IRSA + cross-account trust
  pattern as a copy-paste artifact: both directions of the trust
  policy, the source-account policy, the target-account role's
  policy, and `eks:DescribeCluster` permissions. Most of the
  presign failures trace back to one missing piece of this pattern.

- **Scheduled work — quarterly IRSA trust review.** The IRSA chain
  is the most bit-rot-prone surface. OIDC provider URLs change on
  cluster upgrades; trust policies get tightened in cleanups; roles
  get deleted in IAM audits. A quarterly verification job (or a
  CronJob that calls `GetCredentials` for every EKS-shape cluster)
  catches drift.

- **Failover — multi-region IRSA roles.** If your account has
  Sharko deployed in multiple regions, ensure the IRSA role and
  trust policies cover both. Region failover (Mitigation step 5 of
  [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md))
  only works if STS in the failover region also has valid IAM.

---

## Related runbooks

- [`aws-sm-secret-not-found.md`](aws-sm-secret-not-found.md) — the
  primary fetch failed before STS got involved.
- [`aws-sm-search-access-denied.md`](aws-sm-search-access-denied.md)
  — adjacent IAM failure on the SearchSecrets path.
- [`aws-iam-cluster-auth.md`](aws-iam-cluster-auth.md) — adjacent
  v1.x limitation: `awsAuthConfig` shape (no STS step here — different
  failure path).
- [`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)
  — fleet-wide escalation when every EKS cluster's STS mint fails.
- [`budget-burn-runbook.md#sharkoclusterregistrationfastburn`](budget-burn-runbook.md#sharkoclusterregistrationfastburn)
  — feature-budget alert if registration failures sustain.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md#correlation-ids)
  — request_id correlation pattern.

## Escalation

If Mitigation steps 1-4 don't restore the cluster AND the cluster is
critical, email the maintainer: `moran.weissman@gmail.com`. Include:

- This runbook URL
- The cluster name and the secret's structured fields from Diagnosis
  step 3 (REDACT `caData` — it's the cluster's CA cert, not a
  secret per se, but defensive redaction is the rule)
- The two error strings from Diagnosis step 2 (config-load vs
  presign)
- The CloudTrail event ID for the most recent IAM policy / trust
  change (if any) on the source IRSA role and the target roleArn
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Most
token-mint failures are IAM-configuration issues fixable from the
operator's AWS account; escalation is rare.

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
- [x] Alert names referenced (FastBurn)
-->
