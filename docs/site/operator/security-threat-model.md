# Security: secrets, threats and the AI guard

This page is part of the [Security](security.md) reference. It covers:

- what Sharko does with a secret value, and what protects it
- what is not protected, said plainly
- advice for handling secrets, including pasted cluster credentials
- who is named on the Git commits Sharko makes
- the guard that stops secrets being sent to an AI provider

## What Sharko does with a secret value {#secrets-provider-security-model}

Sharko does not store addon secret values. It carries them. It reads a value
from a secrets backend you set up (AWS Secrets Manager or Kubernetes Secrets),
and writes it into a Kubernetes Secret on a managed cluster, so an addon there
can use it.

- Sharko reads the value from the backend each time it delivers it.
- It does **not** keep the value between deliveries, in memory or on disk.
- It sends the value straight to the managed cluster, over a short-lived
  connection.
- The value is never written to Git, never put into a manifest, and never sent
  to ArgoCD.
- No API answer and no Activity History entry carries the value. They name
  the backend path and the Secret, never what is inside.
- Every Secret Sharko writes there has the label
  `app.kubernetes.io/managed-by: sharko`.
- ArgoCD must be told to leave these Secrets alone. See
  [ArgoCD Resource Exclusions](configuration.md#argocd-resource-exclusions).

Four checks decide which value goes where:

| Check | What it stops |
|---|---|
| One delivery path | The scheduled delivery and **Refresh now** use the same code, so one cannot skip a check the other does. |
| Backend boundary | Reading a value outside the area you set up in the backend (for example, outside the name prefix or namespace you configured). |
| Destination check | Sending a value to a cluster whose connection does not check the TLS certificate. |
| Ownership gate | Overwriting a Kubernetes Secret that another tool owns. |

## What is not protected {#what-is-not-protected}

**If someone takes over the Sharko pod itself, they get what Sharko can
reach.** All the checks above are Sharko's own code. An attacker running code
inside the pod can go around them, and act as the Git token, the ArgoCD token,
the Kubernetes ServiceAccount and the AWS role the pod holds. Nothing in Sharko
can prevent that. What limits the damage is how little those identities may
do. So:

- keep the pod settings in
  [A hardened install](security-hardened-example.md#pod-security)
- give every identity Sharko holds as little access as you can (see
  [RBAC](security.md#rbac))
- treat "the Sharko pod was taken over" like "its tokens were stolen", because
  it is the same thing

**The destination check only covers writing a secret value.** Reads (listing
what is on a cluster, diagnose, discovery) and deletes (when you unadopt or
remove a cluster) still run against a cluster whose connection skips the
certificate check. None of those send a secret value. If a cluster's
connection does not check who it is talking to, treat everything Sharko does
there as unproven.

**A raw value sits in ordinary memory while it is delivered.** Nothing wipes
it afterwards. Someone who can dump the pod's memory could, in principle,
find it there for a while.

**Not built yet.** None of these exist in Sharko today. This list is not a
promise that they are coming:

- Sign-in through SSO, SAML or OIDC. Sharko has local users and API keys.
- Roles for one cluster or one addon. See
  [Application Roles](security.md#application-roles).
- More than one pod. Sharko runs as one pod only, and sessions and the
  Activity History are in memory, so a restart loses both. See
  [Activity History](audit-log.md).
- Encrypted traffic forced between Sharko and everything it talks to (mTLS).
  Set that up in your network if you want it.
- A hardware security module. Sharko's encryption key comes from an
  environment setting.
- A record of activity that cannot be changed and survives a restart.
- A rules engine where you write your own security rules.
- More secret backends. Today there are AWS Secrets Manager and Kubernetes
  Secrets. There is no Vault, Azure Key Vault or Google Secret Manager.
- A paid outside security review. The code has only been reviewed by its
  own maintainer.

To report a security problem, follow
[SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md).
For anything else, open a
[GitHub issue](https://github.com/MoranWeissman/sharko/issues).

## Advice for handling secrets {#secrets-management-recommendations}

- **Your Git and ArgoCD tokens are never Helm values.** You enter them in the
  Settings page or with `sharko connect`, and Sharko keeps them encrypted (see
  [Secret Encryption](security.md#secret-encryption)). There is nothing to
  hide in your values file for them.
- **Two secrets can go in Helm values. Keep both out of your values file.**
    - The first admin password: point `bootstrapAdmin.existingSecret.name`
      and `bootstrapAdmin.existingSecret.key` at a Secret you make yourself,
      for example with Sealed Secrets or the
      [External Secrets Operator](../glossary.md#eso-external-secrets-operator).
    - The webhook secret: leave `secrets.webhookSecret` empty, and give the
      pod `SHARKO_WEBHOOK_SECRET` from your own Secret instead:

      ```yaml
      extraEnv:
        - name: SHARKO_WEBHOOK_SECRET
          valueFrom:
            secretKeyRef:
              name: my-sharko-webhook
              key: secret
      ```

- **Turn on Kubernetes API audit logging** in your cluster, to keep a record
  of what Sharko's ServiceAccount does. Sharko's own Activity History is lost
  on every restart.
- **Change your Git and ArgoCD tokens from time to time,** in the Settings
  page.
- **Do not set `SHARKO_DEV_MODE`.** It is for working on Sharko itself, and
  it loosens the rules about which token is used for Git commits.
- **Set `SHARKO_WEBHOOK_SECRET`** (or `secrets.webhookSecret`) before you
  expect the webhook to work. Until it is set, Sharko refuses every call to it.
  See [Webhook Security](security-network-and-addresses.md#webhook-security).

### Leave "Allow legacy inline credentials" off {#allow-legacy-inline-credentials}

This setting decides if someone may register a cluster by pasting a
kubeconfig. It is off by default. You find it in Settings, next to the
connectivity probe, or set it with `settings.allowInlineCredentials` in the
Helm values. With it off, a cluster is registered with credentials from a
supported store: `connection.provider.type` can be `argocd`, `aws-sm` or
`k8s-secrets`. A pasted kubeconfig is kept only in ArgoCD's cluster Secret,
and Git has no copy, so if that Secret is lost, the credential is lost too.
To move pasted clusters to a store, follow
[Migrating Off Pasted (Inline) Credentials](migrate-inline-credentials.md).
When Sharko runs outside a cluster this setting does not exist, and pasting is
always refused. See
[Allow legacy inline credentials](../user-guide/connections.md#allow-legacy-inline-credentials).

## Who is named on a Git commit {#tiered-git-attribution}

Sharko sorts every change into one of two kinds, and that decides whose name
goes on the Git commit:

| Kind | Examples | Token used | Commit author | Extra line on the commit |
|---|---|---|---|---|
| **Everyday operations** | register or remove a cluster, turn an addon on or off, upgrade an addon, refresh a pull request, change a connection, change AI settings | Sharko's service token | `Sharko Bot` | `Co-authored-by: <user>` |
| **Configuration** | edit addon catalog details, edit values | Your personal Git token if you set one, else the service token | You (with your personal token), or `Sharko Bot` | None (with your personal token), or `Co-authored-by: <user>` |

Signing in, setting your own token, and incoming webhooks make no commit.

Each user can set a personal Git token under **Settings → My Account**. Sharko
keeps it encrypted with the same key as the connection tokens.

Every Activity History entry for a change says which way the commit was
signed, in `attribution_mode`:

| `attribution_mode` | Meaning |
|---|---|
| `service` | The service token was used. No person is named on the commit (for example, a webhook). |
| `co_author` | The service token was used. The person is named in a `Co-authored-by:` line. |
| `per_user` | The person's own token was used. They are the commit author. |

If you make a configuration change without a personal token, Sharko shows a
banner that points you to **Settings → My Account**. More on this in
[Git attribution](../user-guide/attribution.md).

## The AI secret guard {#secret-leak-guard-on-ai-annotation}

When AI annotation is on, Sharko reads an addon's upstream `values.yaml`
before it sends it to the AI provider. It looks for things that look like
secrets:

- AWS access keys
- GitHub tokens, both the classic and the fine-grained kind
- JWT tokens
- PEM private keys
- Slack tokens
- Google API keys
- lines that set an API key, token, password or secret to a long value

If it finds any, the AI call is **blocked**. There is no way to override it.

Each block writes an Activity History entry with the event name
`secret_leak_blocked`. So you can find every block, from any part of Sharko,
by that one name:

```bash
curl -H "Authorization: Bearer $SHARKO_TOKEN" \
  "https://sharko.example.com/api/v1/audit?action=block&limit=200" \
  | jq '.entries[] | select(.event == "secret_leak_blocked")'
```

The entry's `detail` field says which part of Sharko was blocked (`addon_add`,
`ai_annotate`, `catalog_add` or `values_refresh`), the chart and version, how
many matches there were, and which kinds of secret matched. The matched text
itself is never logged, never stored and never returned by the API.

When the block happens while you ask the AI to annotate an addon's values,
Sharko writes a second entry too, with the event name `ai_annotate_blocked`.
So for that action you see both entries.

If a block stops you, see
[AI annotation blocked by a secret](ai-annotation-secret-blocked.md).
