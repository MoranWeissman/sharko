# EKS Live-Test Runbook

> **Verified:** Not yet executed end-to-end (V2-cleanup-62.1, authored 2026-07-06). This page describes the procedure the harness script (`scripts/eks-live-test.sh`) is built to support. Update this notice with the date and outcome the first time it's actually walked against a real AWS account.

A hands-on, half-day procedure for proving Sharko's `eks-token` credential path against a **real** EKS cluster. This is **not** a reference doc — it's a checklist you walk yourself, on your own AWS account, when you decide it's worth spending an afternoon and a couple of dollars to close an honesty gap.

If you just want the subcommand list, run `./scripts/eks-live-test.sh help` or `./scripts/eks-live-test.sh <subcommand> --help`. This page is the walkthrough that ties the subcommands together into one pass.

---

## Why this exists

Sharko's docs and UI say it supports EKS clusters via IAM/STS tokens (`eks-token` credential source). That claim has only ever been tested with fakes — unit tests that fake out the AWS SDK, never a real EKS API server. That's a gap between what's claimed and what's been proven. This runbook is the honesty gate: walk it once, and the claim becomes something you've actually seen work, not something you're hoping works.

**What this proves, plainly:** that a real AWS STS token, minted the way Sharko's code mints it, is accepted by a real EKS cluster's API server — and that Sharko's own registration + credential-fetch code path can use that token to reach the cluster and deploy something. It does **not** prove every AWS auth style works (only the EKS access-entry / STS-token style), and it does not run automatically or repeatedly — this is a manual, maintainer-driven pass, not a CI job.

**What this does NOT test, and why that's fine:** the ArgoCD hub itself stays on your local kind cluster, unchanged. EKS is the one spoke under test. Nothing runs on EKS except the node(s) and one small addon — no ArgoCD, no ingress. ArgoCD dials *out* to the EKS public API endpoint; nothing on EKS accepts inbound traffic from you.

---

## Before you start

- **You drive every UI step.** This runbook tells you what to click and what to paste, but you're the one opening the browser and doing it — the script never touches the Sharko UI (see the [standing rule](personal-smoke-runbook.md) about the maintainer driving test flows; the same applies here).
- **This spends real money.** Rough estimate: EKS control plane ~$0.10/hr + one small node ~$0.02/hr ≈ $0.12/hr. A half-day pass (create, test, teardown within a few hours) costs roughly $1-2 total. `teardown` is not automatic — you run it yourself when you're done, and the script scans for anything still billing afterward.
- **One throwaway spoke, not a fleet.** The script refuses to create a second cluster if one already exists under the same name.

### One-time env var setup

The script refuses to do anything AWS-touching until you set your own account ID — this is the only guard standing between the script and accidentally hitting the wrong AWS account:

```bash
export SHARKO_EKS_TEST_ACCOUNT_ID=<your-12-digit-AWS-account-id>
```

No account number is ever written into this repo — this env var lives only in your shell. If `AWS_PROFILE` happens to be set to something matching a work-account naming pattern, the script refuses outright rather than guessing which account you meant.

---

## Part 1 — prove the baseline eks-token path

### Step 1 — preflight

```bash
./scripts/eks-live-test.sh preflight
```

Checks `eksctl` / `aws` / `kubectl` are installed, verifies your env var against the live AWS account, and prints the cost estimate + a "this spends real money" reminder. Fix anything it flags before continuing.

### Step 2 — create the cluster (~15-20 minutes)

```bash
./scripts/eks-live-test.sh create
```

Creates a cluster named `sharko-eks-live-test` in `eu-west-1`: one managed nodegroup, one small node (`t3.small` by default — the cheapest instance type that comfortably runs EKS's own system pods plus one small addon; pass `--spot` if you want the cheaper, interruptible option for a short test). The public API endpoint is left on (the default) since nothing needs a VPN or peering for a throwaway test.

The kubeconfig is written to a temp file, **never** merged into `~/.kube/config`, and your current `kubectl` context is untouched — you can run this from a laptop mid-way through a normal workday without disturbing anything else you're doing.

Grab a coffee. This step is the slow one.

### Step 3 — prove the token path directly

```bash
./scripts/eks-live-test.sh token-check
```

This runs the exact AWS call Sharko's `eks-token` code path runs under the hood (`internal/providers/aws_auth.go`'s `getEKSToken`), then proves the minted token is actually accepted by calling `kubectl get nodes` against the real API server with it. If this fails, nothing downstream will work — fix it here before touching the UI.

### Step 4 — set up the prerequisite Sharko-side config

Run:

```bash
./scripts/eks-live-test.sh register-help
```

This prints everything you need, grounded against the actual UI code (not guessed), including:

- The **Secrets Provider** connection needs to be type `aws-sm` (not `k8s-secrets` — verified in code, `k8s-secrets` doesn't support the STS-token path at all).
- The exact `aws secretsmanager create-secret` command to run, with the cluster's real API server address and CA data already filled in (assuming you've run `create`).
- The exact fields to paste into Sharko's **Register Cluster** dialog.

Do the AWS Secrets Manager step yourself (copy-paste the printed command), then open Sharko's Settings → Secrets Provider and switch it to `aws-sm` if it isn't already.

### Step 5 — register the cluster in Sharko (you drive this)

Open Sharko's UI → **Clusters** → **Register Cluster**. Paste in the fields `register-help` printed: Direct mode, cluster name, credential source "Amazon EKS — generate a token from cloud identity," region. Leave the Role ARN field blank for this leg — there's no role to assume yet; Part 2 covers where to put one once `role-setup` has printed it.

### Step 6 — deploy one small addon and verify green

Recommendation: **metrics-server**. It's the addon already used as the canonical example everywhere else in Sharko's docs, it has no dependencies, needs no persistent storage or ingress, and reaches Healthy/Synced in ArgoCD within a minute or two of syncing — about as fast and low-risk a real-world proof as you can ask for.

Add it to the cluster's addons (UI or `sharko add-addon`), let ArgoCD sync, and watch it go **Synced / Healthy**. If it sits **Degraded** or **Progressing** past a couple of minutes, check the pod logs — metrics-server's most common EKS hiccup is needing `--kubelet-insecure-tls` on a fresh cluster with self-signed kubelet certs; that's a metrics-server quirk, not a Sharko problem.

Green here is the proof: Sharko registered a real EKS cluster over the eks-token path, ArgoCD reached it, and a real workload deployed and became healthy.

### Step 7 — teardown and verify nothing is left billing

```bash
./scripts/eks-live-test.sh teardown
```

Deletes the cluster, then scans for CloudFormation stacks, EC2 instances, ELBs, and EIPs still tagged with this test's markers, and prints loud `LEFTOVER` warnings if it finds anything instead of silently reporting success. Re-run it if it reports leftovers — it's idempotent.

```bash
./scripts/eks-live-test.sh status
```

Confirms the cluster is gone. If you're paranoid (reasonable, it's your money), also glance at the AWS Billing console a day later — the script's cost estimate is rough, not authoritative.

---

## Part 2 (optional but recommended) — prove the assume-role hop

Part 1 proves the token-minting and ArgoCD-acceptance mechanics work. It does **not** prove the scenario every real user is actually in: an identity that did **not** create the cluster, reaching it through an assumed IAM role — exactly how cross-account EKS access works in practice. Sharko's own token-minting code (`getEKSToken`) assumes a role via `stscreds.NewAssumeRoleProvider` before presigning, matching ArgoCD's own `--role-arn` behavior. This part proves that hop, cheaply (IAM roles and EKS access entries are free — only the cluster itself costs anything, and it's already running from Part 1).

### Step 8 — create the throwaway role

```bash
./scripts/eks-live-test.sh role-setup
```

Creates a throwaway IAM role your own account can assume, then grants it cluster-admin access on the test cluster via an EKS access entry (the modern replacement for the old `aws-auth` ConfigMap). Prints the role's ARN.

### Step 9 — prove the assume-role hop directly

```bash
./scripts/eks-live-test.sh token-check --role-arn <arn from role-setup>
```

Same as Step 3, but this time the token is minted for the assumed role, not your own caller identity — and the script confirms the API server accepts it. If this goes green, the assume-role hop works end-to-end at the AWS/Kubernetes layer.

### Step 10 — prove it through Sharko itself (optional, advanced)

To exercise the same hop through Sharko's own registration/credential-fetch code (not just the script talking to AWS directly), give Sharko the assumed role's ARN either way:

- **Via the UI:** re-register (or edit) the cluster and paste the ARN into the Register Cluster dialog's "Role ARN" field. It's persisted as `role_arn` on the cluster's `managed-clusters.yaml` entry and used at token-mint time.
- **Via the secret:** update the AWS Secrets Manager secret you created in Step 4 to add the assumed role's ARN — no re-registration needed, Sharko re-reads the secret on every fetch:

```bash
aws secretsmanager put-secret-value --region eu-west-1 --secret-id "sharko-eks-live-test" \
  --secret-string '{ ..., "roleArn": "<arn from role-setup>" }'
```

**If both are set,** the secret's own `roleArn` wins, then the per-cluster `role_arn` from the UI, then the connection-level provider default. Use whichever leg you want to prove — the UI field now works end-to-end (PR #466 fixed the earlier bug where it was silently dropped) — then click **Test cluster** again in the UI, or trigger another addon sync.

### Step 11 — teardown (covers Part 2 too)

`./scripts/eks-live-test.sh teardown` also deletes the throwaway IAM role and its access entry — no separate cleanup step needed. Re-run `status` to confirm.

---

## A note on what "production-shaped" means here

One honest caveat, so this runbook doesn't overclaim: the *credential source* Sharko's pod uses on your local kind hub — however it's currently wired for you locally — is whatever the AWS SDK's standard credential chain finds (env vars, a shared config file, or IRSA/Pod Identity if actually running in a properly configured EKS-hosted pod). That chain is AWS SDK behavior, not Sharko's code, and this harness doesn't change or test it. What Sharko's code *does* own, and what this runbook actually proves live, is everything downstream of "we have some AWS credentials": minting the STS token in the right shape, assuming a role first when one is configured, handing that token to `kubectl`/ArgoCD, and having a real cluster accept it. Part 2's assume-role hop is the closest cheap proxy for "an identity that isn't the cluster's owner" without needing a second AWS account.

---

## Related pages

- [Personal Smoke Runbook](personal-smoke-runbook.md) — the local kind + ArgoCD smoke pass this test complements; same "you drive the UI" rule applies.
- [Cluster Connectivity Model](../operator/cluster-connectivity-model.md) — reference for the auth shapes Sharko supports.
- [AWS IAM Cluster Auth Test Unsupported](../operator/aws-iam-cluster-auth.md) — the adjacent v1.x IAM-auth limitation for the **Test cluster** button specifically (different code path from the `eks-token` registration path this runbook exercises).
