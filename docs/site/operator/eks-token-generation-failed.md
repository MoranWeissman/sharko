# EKS Token Generation Failed

**Severity:** P1

> **Verified:** Re-authored 2026-08-11 against `fix/provider-error-leaks`
> after the provider-error hotfix. Both emission sites re-read in
> `internal/providers/aws_auth.go`: the config-load failure logs
> `step=load-aws-config` and the presign failure logs
> `step=presign-get-caller-identity`, and NEITHER logs the AWS SDK
> error's value. `internal/providers/aws_sm.go` logs `step=sts` on a
> mint failure with no error value and no `tokenPrefix`.
> `internal/providers/argocd_provider.go` returns
> `ArgoCDProviderError{Code: argocd_provider_iam_required}` whose
> `Detail` is Sharko's own sentence — the mint error is kept on the
> unexported `Cause`, which never serializes. The API response carries
> the safe sentence from `internal/credsafe`. Verified by
> `TestAWSSMProvider_LogsCarryNoRawErrorAndNoTokenPrefix`,
> `TestGetEKSToken_LogsCarryNoPresignedURLAndNoLength` and
> `internal/api/cred_error_sentinel_test.go`. Re-verify when the
> token-mint flow or the credsafe boundary changes.

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
  cluster returns a result whose reason is Sharko's own fixed
  sentence. The AWS SDK's own text is NOT in the response:

  ```
  HTTP/1.1 200 OK
  {"name":"prod-eu","reachable":false,"stage":"credentials","error_code":"ERR_AUTH",
   "error_message":"Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed."}
  ```

  For a cluster whose connection is the AWS-IAM shape, the same
  failure comes back as a 503 with a stable `error_code`:

  ```
  HTTP/1.1 503 Service Unavailable
  {"error_code":"argocd_provider_iam_required",
   "error":"Cluster \"prod-eu\" needs Sharko's own AWS identity (IRSA / EKS Pod Identity) to use this cluster's IAM-based connection, and minting an EKS sign-in token failed. The server log for this request id says which step failed."}
  ```

  **This changed, and it changed on purpose.** The response used to
  carry the wrapped AWS error, and operators were told to read
  `AccessDenied` / `RegionDisabled` / `InvalidClientTokenId` out of it.
  An AWS SDK error can carry credential material in its own message —
  a wrapped presigned URL, a token fragment, a credential a provider
  chain put into its text — so it is no longer returned. **Sharko never
  shows you the AWS error, anywhere.** What you work from instead is
  the four facts below, and they are enough:

  | Fact | Where it comes from |
  |---|---|
  | **request id** | the `request_id` on every log line for the request; also correlate by time |
  | **cluster** | the `cluster` field on the log line, and the name in your request |
  | **region** | the `region` field on the log line |
  | **step** | the `step` field — this is the one that tells you WHICH failure it was |

  Diagnosis below is built entirely on those four, plus AWS's own
  answers to `sts get-caller-identity` and `sts assume-role`, which you
  run yourself and which report AWS's real reason directly to you. That
  is a better source than Sharko relaying it: you get the current
  answer, from AWS, with your own eyes.

- **Sharko logs one error line per failed step, and no line carries the
  AWS SDK's error value.** Read the `step` field — it is what tells the
  three possible failures apart:

  Config load failed (`aws_auth.go`) — the SDK could not even load
  credentials:
  ```
  {"time":"...","level":"ERROR","msg":"[auth] EKS token generation failed","request_id":"req-...","cluster":"prod-eu","region":"us-east-1","step":"load-aws-config"}
  ```

  Presigning failed (`aws_auth.go`) — credentials loaded, the STS call
  itself was refused or misrouted:
  ```
  {"time":"...","level":"ERROR","msg":"[auth] EKS token generation failed","request_id":"req-...","cluster":"prod-eu","region":"us-east-1","step":"presign-get-caller-identity"}
  ```

  The mint failed as seen by the caller (`aws_sm.go` for an AWS-SM
  secret, `argocd_provider.go` for an AWS-IAM connection):
  ```
  {"time":"...","level":"ERROR","msg":"[provider] GetCredentials failed","cluster":"prod-eu","region":"eu-west-1","step":"sts"}
  {"time":"...","level":"ERROR","msg":"[provider] EKS token mint failed for argocd cluster — Sharko has no usable AWS identity for this cluster","cluster":"prod-eu","server":"https://...","eksClusterName":"prod-eu","region":"eu-west-1","step":"mint-eks-token"}
  ```

- **The cluster row in the dashboard** shows status **Test failed**
  with the safe sentence; other EKS and non-EKS clusters in the fleet
  show **Healthy**.

- **When `roleArn` is set on the secret, the failing step is still
  `presign-get-caller-identity`** — the AssumeRole hop happens inside
  the presign path. Diagnosis step 4's explicit `sts assume-role` is
  how you separate "cannot assume the role" from "assumed it, presign
  still refused", and it gives you AWS's own error directly.

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

Four checks, built on the four facts Sharko actually gives you —
**request id, cluster, region, and step**. Step 1 confirms whether it is
one cluster or the whole fleet; Step 2 reads the `step` field, which is
what picks your mitigation lane; Step 3 checks the secret's own fields;
Step 4 asks AWS directly, which is where you get AWS's real reason.

### 1. Confirm the failure is per-cluster, not fleet-wide

Sharko no longer puts AWS error text in `test_error`, so match on the
safe sentence and on the cluster's test status:

```sh
curl -sS http://sharko/api/v1/fleet/status \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  | jq '.clusters[] | select(.test_error | test("sign-in details"; "i")) | {name, test_status, test_error}'
```

If only one cluster fails this way, single-cluster mitigation applies
(Mitigation step 2+). If several fail at once, get their `roleArn`
values from Step 3 — a shared one means the cross-account role is broken
(Mitigation step 3). If EVERY EKS cluster fails, the Sharko pod's own
IRSA is broken (escalate to
[`secrets-provider-unreachable.md`](secrets-provider-unreachable.md)).

### 2. Read the `step` field — it picks your mitigation lane

This is the single most useful thing in the log, and it is there
precisely so you do not need the AWS error text to tell the failures
apart. Get the request id from the failed response's `request_id` (or
just filter by cluster and time):

```sh
REQ_ID=req-<id-from-the-failed-response>
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c --arg id "$REQ_ID" 'select(.request_id == $id)' \
  | jq -r 'select(.step != null) | "\(.step)\t\(.cluster // "-")\t\(.region // "-")\t\(.msg)"'
```

No request id to hand? Filter by cluster instead — every one of these
lines names it:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c 'select(.cluster == "prod-eu" and .step != null)' \
  | jq -r '"\(.time)\t\(.step)\t\(.region // "-")"'
```

What each `step` means, and where to go next:

| `step` | What failed | Go to |
|---|---|---|
| `load-aws-config` | The SDK could not load any AWS credentials at all. Almost always the Sharko pod's IRSA. | Mitigation step 1 |
| `presign-get-caller-identity` | Credentials loaded; the STS call itself was refused or misrouted. Either a `roleArn` trust-policy problem or a wrong region. | Mitigation step 3 or 4 |
| `sts` | The AWS-SM path saw the mint fail. Pair it with the `load-aws-config` / `presign-get-caller-identity` line from the same request id — that one says which half. | as above |
| `mint-eks-token` | The AWS-IAM connection path saw the mint fail. Same pairing rule. | as above |

**Sharko will not show you the AWS error, and you do not need it.**
`load-aws-config` versus `presign-get-caller-identity` already splits
"no identity at all" from "identity refused", which is the split that
decides what you fix. For AWS's own words, Step 4 asks AWS directly and
prints its answer straight to your terminal — fresher and more
trustworthy than anything Sharko could have relayed.

### 3. Inspect the AWS-SM secret structure to confirm the chain

```sh
CLUSTER=<failing-cluster-name>
SHARKO_POD=$(kubectl -n <sharko-ns> get pod -l app=sharko -o name | head -1)
AWS_REGION=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AWS_REGION")].value}')
PREFIX=$(kubectl -n <sharko-ns> get deployment sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_CONN_PROVIDER_PREFIX")].value}')

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

1. **For `step=load-aws-config` (Diagnosis step 2), repair the Sharko
   pod's IRSA chain.** The pod has no AWS
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

2. **For `step=presign-get-caller-identity` on a cluster WITHOUT
   `roleArn` — the pod's IRSA role lacks the action.**
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

3. **For `step=presign-get-caller-identity` on a cluster WITH `roleArn`
   — repair the cross-account trust policy.** The Sharko pod's IRSA role
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

Diagnostic signature: Diagnosis step 2 shows `step=load-aws-config`.
Diagnosis step 4's `sts get-caller-identity` fails.

Fix is Mitigation step 1 plus the broader fleet-wide repair if
needed.

### Cross-account roleArn trust policy is incorrect

The cluster's AWS-SM secret has a `roleArn` pointing at a role in a
different AWS account. The trust policy on that role doesn't permit
the Sharko pod's IRSA role to assume it (or the source account's
policy is missing `sts:AssumeRole` on the target role).

Diagnostic signature: Diagnosis step 2 shows
`step=presign-get-caller-identity`, and Diagnosis step 4's explicit
`sts assume-role` fails — that command is where you see AWS's own
`not authorized to perform: sts:AssumeRole` message, printed to you by
AWS rather than relayed by Sharko.

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

- **Monitoring — per-cluster STS mint failure counter.** Sharko does not
  export this metric today. The alert below is a design sketch for a
  future release, not something you can deploy now. The sketch:
  `sharko_provider_eks_token_errors_total{cluster, stage}` with stages
  `config_load` / `presign` / `assume_role`, surfacing this failure
  without any triage work. The stage label would be the same `step`
  value the log line already carries. Note the label set deliberately
  has no `reason` dimension taken from the AWS error: a metric label
  built from provider error text is both unbounded cardinality and a
  leak. Today the signal is the `step` field on the error log line plus
  the per-cluster `test_status` in `/api/v1/fleet/status`.

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
- The `step` value(s) from Diagnosis step 2, the request id, the
  cluster and the region (Sharko does not log the AWS error text, so
  these four are what it has)
- The exact output of Diagnosis step 4's `sts get-caller-identity` and
  `sts assume-role` — that is AWS's own reason, and it is the detail
  the maintainer needs
- The CloudTrail event ID for the most recent IAM policy / trust
  change (if any) on the source IRSA role and the target roleArn
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Most
token-mint failures are IAM-configuration issues fixable from the
operator's AWS account; escalation is rare.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date current (re-authored 2026-08-11, provider-error hotfix)
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
