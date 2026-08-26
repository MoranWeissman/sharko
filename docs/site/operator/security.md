# Security

This page documents Sharko's security posture and hardening recommendations for production deployments.

## Security Headers

Sharko sets the following HTTP security headers on every response:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | Restricts sources for scripts, styles, and frames |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` (HTTPS enforced for 1 year) |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |

HSTS is only effective when Sharko is served over HTTPS. Configure TLS termination at the ingress layer and ensure the ingress controller forwards the `X-Forwarded-Proto` header.

## Rate Limiting

Sharko applies rate limiting to both authentication endpoints and admin write endpoints:

| Scope | Limit |
|-------|-------|
| Auth endpoints (`/api/v1/auth/*`) | Per-IP burst limit |
| Write endpoints (admin POST/DELETE/PATCH) | 30 requests/minute per IP |

Rate limiting counts requests per client address. Which address that is depends on **trusted proxy** configuration.

If Sharko sits behind a reverse proxy or ingress controller, list that proxy's own address in `SHARKO_TRUSTED_PROXIES`. Several entries are separated by commas:

```yaml
extraEnv:
  - name: SHARKO_TRUSTED_PROXIES
    value: "192.0.2.10,2001:db8::10"
```

The chart has no value of its own for this. It goes in through the generic `extraEnv` list above, which is the only way to set it.

**Those two addresses are placeholders. Replace them.** `192.0.2.10` and
`2001:db8::10` come out of ranges the internet standards reserve for
writing examples, so no real machine anywhere has them. That is why they
are here: copy this block unchanged and you trust nothing, which is safe.
Put in the addresses your own ingress or reverse proxy actually answers on
— the ones you would see as the source if you looked at a connection
arriving at Sharko's port.

!!! danger "Upgrading? If you have `SHARKO_TRUSTED_PROXIES=*`, Sharko will not start"
    Older versions of this page told you that you could write `"*"` here
    to trust every proxy, and called it safe in a controlled environment.
    **It was never safe, and it now stops the server.**

    What you will see: Sharko exits during startup instead of serving. The
    pod log ends with

    ```
    SHARKO_TRUSTED_PROXIES: entry 1 trusts every address; list each proxy address or CIDR range explicitly
    ```

    and the pod goes into `CrashLoopBackOff`. No request is ever answered.

    What it means: `*` told Sharko to believe a forwarding header from
    **anybody** who could reach its port. Anyone who could open a
    connection could then claim to be a different client on every request
    and never hit the login rate limit. Sharko now refuses to run with a
    setting it cannot enforce, rather than run and pretend.

    What to put there instead: the addresses your ingress or reverse proxy
    answers on, exactly as in the block above. If you are not behind a
    proxy at all, delete the setting — leaving it out trusts no proxy,
    which is the right answer and needs no configuration. `0.0.0.0/0` and
    `::/0` are refused for the same reason as `*`; there is no spelling of
    "trust everyone" that Sharko will accept.

### Write the proxy's address, not the range it happens to sit in

This setting accepts CIDR ranges as well as single addresses, and a broad private range is the wrong thing to put in it.

In an ordinary Kubernetes cluster every pod gets an address out of the same private range. A value like `10.0.0.0/8` therefore does not name your ingress controller — it names **every pod on the cluster**. Anything running there, including a compromised addon or another team's workload, can reach Sharko's port directly, set its own `X-Forwarded-For`, and hand out a different address on every request. Sharko would believe each one, count each as a separate caller, and never refuse any of them. That is the login rate limit gone, and it is gone for the one caller you would most want it to stop.

So write the addresses your proxy actually answers on. Use a range only when you genuinely run several proxies inside it **and** nothing else on the network can reach Sharko's port — a dedicated ingress subnet, not the pod network.

The rules Sharko follows:

- Sharko starts from the address of whoever actually opened the connection. It never starts from a header.
- If that address is **not** on the list, `X-Forwarded-For` and `X-Real-IP` are ignored completely, and the caller is counted by the address it really came from.
- If it **is** on the list, Sharko reads the forwarded chain from your proxy inward and takes the first address that is not itself a listed proxy. The leftmost value in the header is never trusted just because it is there.
- `X-Real-IP` is read only when there is no `X-Forwarded-For` header at all, and then the **last** one wins — the same right-to-left rule as the chain. A request that carries `X-Forwarded-For` is answered from the chain and nothing else, even when the chain names no outside caller. That stops a caller behind your proxies from sending a chain of your own proxy addresses and then naming itself in the simpler header.
- Leaving the setting empty trusts no proxy at all. There is no wildcard, and loopback, private and Kubernetes ranges get no automatic trust — the list is exactly what you write.
- A value that is not an address or CIDR range stops the server at startup rather than starting with a setting that cannot be enforced. So does any value that would trust every address — `*`, `0.0.0.0/0` and `::/0` are all refused by name, because trusting everyone is the exact hole this setting closes. An older version of this page told you to use `"*"`; if you copied it, change it to your proxy's real address or range before you upgrade, or the server will not start.

The same resolved address is used for rate limiting and for the client address recorded in security log lines, so the two can never disagree. Note that the **audit log records no client address at all** — a refused request does not leave an audit entry naming the caller.

!!! warning
    List only proxies you control. A proxy you list is one you trust to overwrite whatever forwarding header its own client sent; if it passes the client's header through untouched, that client is choosing its own address again.

!!! tip
    Put an ingress in front of Sharko and terminate TLS there as well. That is worth doing on its own, but it is defense in depth — it does not replace setting `SHARKO_TRUSTED_PROXIES`, and Sharko will not believe an ingress it has not been told about.

## Authentication

### Admin Password

The initial admin password is randomly generated and stored as a bcrypt hash in a Kubernetes Secret. The plaintext is mirrored to the `sharko-initial-admin-secret` Secret (the same pattern ArgoCD uses with `argocd-initial-admin-secret`) so you can retrieve it after install:

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

Retrieve it once and change it immediately after first login.

### No Users Configured

There is no way to run Sharko with authentication off (outside demo mode). If Sharko starts and finds **zero** configured users — no chart-seeded accounts, no `SHARKO_BOOTSTRAP_ADMIN_PASSWORD`, no `SHARKO_AUTH_USER` / `SHARKO_AUTH_PASSWORD` env vars — it creates an `admin` user itself with a random password and tells you where to find it:

- **In a cluster:** the password lands in the `sharko-initial-admin-secret` Secret (retrieve it with the command above). Set `SHARKO_WRITE_INITIAL_ADMIN_SECRET=false` if you want the password to appear only in the startup log.
- **Outside a cluster (local binary):** the password is saved to `~/.sharko/initial-admin.json` (file mode 0600; override the path with `SHARKO_INITIAL_ADMIN_FILE`). The same password is reused on the next start.

Either way the password is also printed once on the server's startup output, and every API request requires authentication from the first moment. If the user store somehow ends up empty at request time, requests are refused with a 401 — the server never falls back to running open.

The only exception is **demo mode** (`sharko serve --demo`), which is loud about it: it seeds the fixed demo users `admin/admin` and `qa/sharko` and enforces login with those. Demo mode is for trying Sharko out with mock backends, never for real clusters.

Check user configuration:

```bash
kubectl get configmap sharko-users -n sharko -o yaml
```

### Login Sessions

A person logging in gets a session that **lasts 24 hours**. That window is the
default and it is not configurable. When it runs out the session is gone: the
next request comes back `401` and the person logs in again. There is no refresh
token and no "remember me", so a stolen session token is useful to an attacker
for at most 24 hours.

Sessions live in memory, so a pod restart signs everyone out. A background
sweep clears expired sessions every hour, but the sweep is only housekeeping —
every single request re-checks the expiry, so an expired session is refused
immediately whether or not the sweep has run.

### API Keys

API keys use bcrypt hashing — the server never stores plaintext keys. The plaintext key is shown only once at creation time. Treat API keys as secrets; store them in your CI/CD vault (e.g., GitHub Actions secrets, Vault).

**Keys expire after 90 days by default.** You can ask for anything from 1 to
365 days when you create one. A key past its date is refused with a `401` that
names the key and says it expired — the caller already holds that key, so
naming it gives nothing away. A key that was revoked or never existed gets a
flat `401` instead, so nobody can probe for real key names.

**Renewing** a key pushes its expiry out without changing the key value, so
pipelines holding it keep working with nothing to redeploy. **Revoking** takes
effect immediately, with no grace period.

**Keys created before expiry dates existed** have no expiry. Sharko does NOT
force-expire them — they keep working, and they show up in the key list as
`legacy-no-expiry` so you can spot them. Recreate them when convenient so they
pick up an expiry date.

Full request and response shapes are in the
[API endpoints reference](../api/endpoints.md#api-keys-tokens).

## Application Roles

Every user and every API key carries one of three roles. The role decides
which write actions (and a few read actions) are allowed — it is checked on
**every** request, not just at login.

| Role | Who it's for | Can do |
|------|---------------|--------|
| `viewer` | Anyone who should be able to look, not touch | See clusters, addons, connections, pull requests, the audit log, and metrics. Manage their own profile (change their own token, clear their own GitHub token). Cannot create, change, or delete anything shared. |
| `operator` | The people who run day-to-day operations | Everything a viewer can do, plus: register/adopt/test/diagnose clusters, enable/disable addons, restart a stuck sync, create/update connections, test connections and credential providers, edit the addon catalog, create API tokens and renew *their own* tokens, trigger the secrets reconciler, and run the first-time init wizard. |
| `admin` | Whoever owns the Sharko install | Everything an operator can do, plus the actions with blast radius beyond the caller's own work: delete a connection, remove or unadopt a cluster, remove an addon from the catalog, create/delete/change the role of other users, revoke any token, renew *someone else's* token, clear the audit log, change AI provider settings, save dashboard layouts, edit ArgoCD resource exclusions, create/delete addon secrets, delete a pull request, refresh third-party catalog sources, and flip the security-relevant settings toggles (probe mode, inline credentials, self-heal). |

**New users and new API tokens default to `viewer`** if no role is given —
the caller (an admin) has to deliberately opt a person up to `operator` or
`admin`, both at `POST /api/v1/users` and `POST /api/v1/tokens`.

**A token can never carry a role higher than the person who created it.** An
operator asking for an `admin` token is refused with a 403; only an admin can
create an admin token. Without that ceiling, "create your own API token"
would be a one-request way for an operator to hand themselves every
permission their own account is refused.

**Renewing a token follows who owns it.** An operator may renew a token they
created (or a token renewing itself, since an API key authenticates under its
own name); renewing anyone else's — including a token created before Sharko
started recording who asked for it — takes an admin. Renewing keeps a live
credential alive, so it sits on the same own/other line as revoking.

An action Sharko's code doesn't recognize is treated as **admin-only**
(fail-closed) rather than open to everyone — a bug that adds a new write path
without registering its action shows up as "nobody but admin can use this
yet," never as "anyone can use this."

### The honest limit: roles are coarse, not scoped

As of v4, permissions are **not** scoped to a specific cluster or a specific
addon. An `operator` who can enable addons can enable them on *any*
registered cluster — there is no "operator for cluster X only" or "operator
for addon Y only." The three roles are the entire permission model.

This is a known gap, not an oversight: finer-grained, per-cluster or
per-addon permissions are tracked as future work (see the
[roadmap](../community/roadmap.md)) and are intentionally out of scope for
v4. If your team needs cluster-level isolation today, the practical
workaround is separate Sharko installs per team/cluster-group rather than
one shared install with mixed trust levels.

### Where the mapping lives

The full action → role table is `internal/authz/authz.go`
(`authz.ActionRequirements`). It is enforced identically on two surfaces:

- **REST API** — every mutating handler (`POST`/`PUT`/`PATCH`/`DELETE`) calls
  `authz.RequireWithResponse` naming its action before doing anything else.
- **AI assistant tools** — the handful of tools that write (enable/disable an
  addon, bump a catalog version, trigger an ArgoCD sync/refresh) check the
  SAME action table before touching Git or ArgoCD, so a viewer asking the
  assistant to "enable datadog on prod" gets the identical refusal a direct
  API call would get.

Both surfaces are covered by a test that mechanically re-derives the route
and tool inventory from the source (rather than trusting a hand-maintained
list to stay in sync): `internal/api/authz_coverage_test.go` and
`internal/api/ai_tools_authz_parity_test.go`.

## Pod Security

Sharko's default security context enforces a hardened pod configuration:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1001
  runAsGroup: 1001
  fsGroup: 1001

securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop:
      - ALL
```

This is compliant with the Kubernetes **Restricted** Pod Security Standard. No privileged containers, no root access, no capability escalation.

## ArgoCD's own permissions

Everything under [RBAC](#rbac) below is *Kubernetes* permissions. ArgoCD keeps
a separate set of its own, in the `argocd-rbac-cm` ConfigMap, and **installing
Sharko does not change them.** The chart ships no job, no install hook and no
template that edits that ConfigMap. Sharko talks to ArgoCD as the account whose
token you configured, and what that account may do is settled by you inside
ArgoCD.

An earlier version of the chart did edit `argocd-rbac-cm` from an install hook,
granting itself a role. It has been removed rather than documented: an
installer for one product narrowing another product's shared policy is fragile
whichever way it is written, and this preview is not the place for it.

### What Sharko needs from ArgoCD

Reading, mostly. Applications, AppProjects and clusters — without those the
dashboard and the cluster list have nothing in them. Registering a cluster and
first-run setup also write: they add a cluster, an AppProject, a repository
connection and the bootstrap Application.

Sharko does **not** need permission to sync applications for addons to be
deployed. Sharko writes to your Git repository and opens a pull request; when
it merges, ArgoCD notices and applies it on its own schedule. Git holds the
desired state and ArgoCD enforces it — that path does not pass through Sharko
at all, so an account with no sync permission still runs a complete fleet.

### Letting Sharko restart a sync

One feature asks ArgoCD to sync an application directly: **Restart sync** on an
addon, and the same recovery Sharko runs by itself after one of its pull
requests merges. It exists for a single stuck situation — an ArgoCD operation
that started before your change and keeps failing — and it saves a trip to the
ArgoCD UI. It is not how addons get deployed.

It is unavailable unless you grant it. Sharko asks ArgoCD whether it is allowed
before it touches anything; when the answer is no, the action returns a message
saying so, nothing is terminated, nothing is retried, and nothing is recorded
as having worked.

To grant it, an ArgoCD administrator adds the following to `argocd-rbac-cm`,
replacing `sharko` with the ArgoCD account whose token Sharko uses:

```yaml
data:
  policy.csv: |
    p, role:sharko-sync, applications, sync, sharko-addons/*, allow
    g, sharko, role:sharko-sync
```

`sharko-addons` is the AppProject the Sharko engine chart creates for the addon
Applications it generates — `project.name` in
`charts/sharko-engine/values.yaml`. If you changed that name, use yours. The
`*` after it means every Application in that project and nothing outside it, so
this grants sync on Sharko's own addons and on nothing else in your ArgoCD.

The same permission also covers cancelling a running operation, which is the
first half of what Restart sync does. There is no separate verb for it.

**Do not write `applications, sync, */*`.** That is sync on every Application
in every project, including everything that has nothing to do with Sharko. It
is what the removed install hook used to grant itself, and it is the reason the
hook was removed rather than kept.

**Repositories still on the older layout.** A repository on the v3 layout gets
one AppProject per addon, each named after its addon, with no shared name to
match on. There is no narrow policy that covers that shape — the only pattern
wide enough is one that is wide enough for everything else too. On a v3
repository, do not grant this: use the ArgoCD UI for the stuck-sync case
instead.

## RBAC

Sharko's default install grants **no cluster-wide access to Secrets**. As of
v4 (Story 152.F), every Secret read the Sharko ServiceAccount does on the
host cluster is scoped to the one namespace it actually needs, not the
whole cluster. This is a real tightening from earlier versions, which
carried a cluster-wide `secrets: get,list` rule — `list` on Secrets hands
over their contents, so that rule was wider than any code path needed.

Sharko creates one `ClusterRole` (cluster-wide, but **not** for Secrets)
plus a small set of namespaced `Role`s:

```yaml
rbac:
  create: true
  argocdNamespace: argocd
  k8sSecretsProviderNamespaces: []
```

| Object | Scope | What it's for |
|---|---|---|
| `ClusterRole` (`sharko`) | cluster-wide | `get/list/watch` on ArgoCD CRDs (Applications, AppProjects, ApplicationSets) — read-only, no write access to the Kubernetes API. Also `get/list` on Nodes if `config.nodeAccess: true` (default). **No Secrets rule.** |
| `Role` (`sharko-argocd-secrets`), in `rbac.argocdNamespace` | one namespace | Full CRUD on **every** Secret in the ArgoCD namespace, not only the ones Sharko created — this is where Sharko reads and writes the ArgoCD cluster-connection Secrets (`internal/argosecrets`, `internal/clusterreconciler`). |
| `Role` (`sharko-secrets-provider`), one per namespace in `k8sSecretsProviderNamespaces` (the release namespace is always included, whether or not you list it) | one namespace each | `get/list` on **every** Secret in that namespace — there is no `resourceNames` restriction, so this is not limited to the Secrets Sharko manages. It exists for the **k8s-secrets** cluster-credential provider and/or the **k8s-secrets** addon-secret provider (`internal/providers/k8s_secrets.go`), but it is granted even when neither is configured. |
| `Role` (`sharko-auth`), in the release namespace | one namespace, mostly name-scoped | **Write** access to Sharko's own operational Secrets (auth store, connections, API tokens), restricted to specific `resourceNames`. Two rules in it are not name-scoped: `create` on Secrets (Kubernetes cannot scope `create` by name) and `get/list/create/update/delete` on ConfigMaps. |

**Read this row twice if you install Sharko into a shared namespace.**
Because the release namespace is always in the `sharko-secrets-provider`
list, Sharko can `get` and `list` every Secret in the namespace it runs in
— not just its own. Kubernetes documents `list` on Secrets as exposing
their contents, not only their names. So anything else parked in that
namespace is readable by Sharko, and by anyone who takes over the Sharko
pod. Give Sharko a namespace of its own and keep nothing else in it, and
keep unrelated Secrets out of every namespace you add to
`rbac.k8sSecretsProviderNamespaces`.

If you use the **k8s-secrets** backend for `connection.provider` or
`connection.addonSecretProvider` and leave its `namespace` field empty, the
provider's own default is the literal string `"sharko"` — **not** your
release namespace. If your release namespace isn't literally `sharko`, add
`"sharko"` to `rbac.k8sSecretsProviderNamespaces` (only if that namespace
already exists — Helm cannot create a Role in a namespace that isn't
there), or, better, set the namespace field explicitly to your release
namespace so there's nothing to keep in sync. Getting this wrong shows up
as a 403 rather than the old silent cluster-wide read — see
[K8s Secrets Provider — Secret Not Found in Namespace](k8s-secrets-not-found-in-namespace.md).

**Keeping the addon-secret identity and the cluster-credential identity
separate.** Point `connection.provider.namespace` and
`connection.addonSecretProvider.namespace` at two *different* namespaces
(each listed in `rbac.k8sSecretsProviderNamespaces`) and a Secret readable
by one Role is not readable by the other — a compromise of the addon-secret
read path does not also expose cluster-credential Secrets, and the reverse.
This is real, RBAC-enforced separation for the Kubernetes backend. For the
AWS Secrets Manager backend, the closest equivalent is two IRSA-role IAM
policy statements scoped to two different secret-name prefixes, under the
one pod identity — see [Scoped IRSA policy](#scoped-aws-irsa-policy) below,
and its documented limit.

Read access to Kubernetes Nodes (`get`, `list` on `v1/nodes`) is granted by default so the Dashboard node-count widget works out of the box. Node metadata is low-sensitivity — no pod, secret, or workload data is exposed. To disable it on clusters where cluster-wide node reads are restricted, set:

```yaml
config:
  nodeAccess: false
```

When disabled, the `/api/v1/cluster/nodes` endpoint returns an empty list with a `"Node info only available when running in-cluster"` style message and the Dashboard widget degrades gracefully.

## Secret Encryption

Connection credentials (ArgoCD tokens, Git tokens) stored in the `sharko-connections` Secret are encrypted at rest using **AES-256-GCM** with a randomly generated encryption key. The encryption key is stored in the Helm release Secret.

!!! tip
    To rotate the encryption key, update the `SHARKO_ENCRYPTION_KEY` env var and re-save all connections in the Settings UI.

### A stored credential only ever goes to its own address

`POST /connections/test-credentials` lets you re-test a saved connection
without re-typing its tokens: leave the token fields blank, name the
connection, and Sharko fills in what it has stored.

Sharko will only do that for the address the connection already points at. If
the request names a different Git repository URL, a different Git provider, or
a different ArgoCD server, the stored token is **not** used and the call is
refused with a 422 telling you to submit that address's credentials
explicitly. Otherwise "test this connection" would be a way to have Sharko
post its stored secrets to any address the caller cares to name. All four
connectivity tests (`/connections/test`, `/connections/test-credentials`,
`/providers/test`, `/providers/test-config`) also require `operator` — they
reach out with real credentials, which is not a read.

## Catalog repository addresses carry no credentials

A catalog entry names the Helm repository its chart comes from. That address is
written into a YAML file and committed to your Git repository, which makes it
the one place in Sharko where an address is *stored durably and replicated*.
Git keeps history: a token in a commit is in every clone, fork, CI cache and
backup, and a later edit does not remove it.

So Sharko refuses to save an address that has anywhere in it for a credential
to sit. The rule, in the words an operator sees:

> Catalog repository URLs in the technical preview must be ones Sharko can
> read in full: a host, an optional port, and an optional path. User
> information in the address, a query string, and a fragment are all refused,
> and so is an address Sharko cannot read. Use a credential-free base URL.

### The shapes that are refused

| Shape | Example | Why |
|---|---|---|
| User information, with a password | `https://user:pw@charts.example/org/charts` | The classic place a token goes. |
| User information, no password | `https://a-token@charts.example/org/charts` | A token in the username slot. `url.Redacted()` does not hide this — which is why Sharko does not use it. |
| A query string | `https://charts.example/org/charts?access_token=…` | A token as a parameter. |
| A fragment | `https://charts.example/org/charts#…` | Same. |
| Any query string at all | `https://charts.example/org/charts?ref=main` | **Refused on purpose.** See below. |
| An empty forced query | `https://charts.example/org/charts?` | Same. |
| An address Sharko cannot read | `https://charts.example:notaport/org/charts` | **Refused on purpose.** If Sharko cannot read the address it cannot tell what is in it, and "I could not tell" is not a yes. |

The last three carry nothing secret. The two query ones are refused because the
check is about the *shape* of the address and never about whether the text in
it looks like a secret. A check that reads the text stops working the first
time somebody writes a credential in a shape nobody predicted, and it fails
silently. A check on the shape cannot. The cost is a refused `?ref=main`; take
the query string off the address and it is accepted.

Because the check cannot know whether a credential is really there, the message
states the rule and never claims Sharko found one.

### The shapes that are accepted

A scheme is optional, and an `@` inside the path is not user information. All
of these are accepted:

| Shape | Example |
|---|---|
| An ordinary address | `https://charts.example/org/charts` |
| No scheme | `charts.example/org/charts` |
| A host and a port | `localhost:8080` |
| An `@` in the path | `https://charts.example/org/charts@v1` |

### Where it is applied

The same one rule is asked at every point that matters, and nothing holds a
copy of it:

- the API doors that add or edit a catalog entry, and the paste-a-URL
  validator, so you are told while you are still typing;
- the CLI's `add-addon`;
- the two functions every catalog file write in Sharko funnels through, so a
  new caller cannot get around the doors;
- the migration from the older repository layout to the current one;
- the one place Sharko dials a chart repository, so a refused address is never
  contacted even if it is already in a file.

### If your repository already has one

Sharko keeps running. The catalog file still loads and every other addon still
works. The one entry comes back marked unusable with the file and field named
— never the address, never part of it, never its length. Sharko will not
deploy that addon, will not reach out using the address, and will not put the
address in any answer or log line.

Every write to the catalog file is refused while that entry is there, because
writing the file again would put the credential into a fresh commit. Sharko
will not quietly strip the address and will not quietly drop the entry —
either would be Sharko editing your repository behind your back.

**Fixing it takes three steps, and the first one is the important one:**

1. **Revoke or rotate that credential** at whatever issued it. The token is in
   your Git history, so it is in every clone, fork, build cache and backup.
   Treat it as public.
2. **Remove it from the history**, not just from the current file — for
   example with `git filter-repo`. Editing the file leaves the token one
   `git log -p` away.
3. **Put a credential-free address in the entry** and commit that.

### Private chart repositories

There is no supported way to use a chart repository that needs a sign-in in
this preview. Naming a Kubernetes Secret or an AWS Secrets Manager entry from
the catalog entry — the same value-or-pointer shape the cluster credentials
use — is the right answer and is work for after the preview.

## Network Policy

Sharko does not ship a NetworkPolicy by default. For production, create one that restricts inbound traffic to your ingress controller and ArgoCD, and restricts outbound traffic to ArgoCD and your Git provider:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: sharko
  namespace: sharko
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: sharko
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd
    - ports:
        - port: 443
          protocol: TCP
```

## A worked example: locking down a production install

This section puts RBAC, the AWS IRSA policy, and a NetworkPolicy together
into one example, so you have something to copy and adapt rather than
piecing the three together yourself. Everything below uses placeholder
values — `123456789012` for an AWS account ID, `sharko.example.com` for a
hostname — swap in your own before applying anything.

The scenario: Sharko is installed in the `sharko` namespace, ArgoCD in the
`argocd` namespace, both AWS Secrets Manager backends (cluster credentials
and addon secrets) live under two different prefixes in the same account.

**1. Helm values — namespaced RBAC, no cluster-wide Secrets access:**

```yaml
rbac:
  create: true
  argocdNamespace: argocd
  # Only needed here because this example's connection.provider.namespace
  # and connection.addonSecretProvider.namespace stay empty — they're
  # unused with the aws-sm backend below. Left in to show the shape.
  k8sSecretsProviderNamespaces: []

connection:
  provider:
    type: "aws-sm"
    region: "us-east-1"
    prefix: "clusters/"
    roleArn: "arn:aws:iam::123456789012:role/sharko-hub-role"
  addonSecretProvider:
    type: "aws-sm"
    region: "us-east-1"
    prefix: "addon-secrets/"
    roleArn: "arn:aws:iam::123456789012:role/sharko-hub-role"

serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/sharko-hub-role"
```

Running `helm template` on this confirms what actually gets created — no
`ClusterRole` rule for Secrets, one namespaced `Role` in `argocd` for the
cluster-connection Secrets, and (since the backend here is `aws-sm`, not
`k8s-secrets`) no `-secrets-provider` Role is needed at all.

### Scoped AWS IRSA policy

Attach this policy to the `sharko-hub-role` IAM role referenced above (the
role the ServiceAccount annotation points at). It has two separate
statements — one for cluster-credential secrets, one for addon-secret
values — each scoped to its own resource-name prefix, matching the two
different `prefix` values set above:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadClusterCredentialSecrets",
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:us-east-1:123456789012:secret:clusters/*"
    },
    {
      "Sid": "ReadAddonSecretValues",
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:us-east-1:123456789012:secret:addon-secrets/*"
    },
    {
      "Sid": "AssumeSpokeRolesForTestAndSecretPush",
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": [
        "arn:aws:iam::123456789012:role/example-spoke-role"
      ]
    }
  ]
}
```

**The honest limit:** the two `GetSecretValue` statements above scope *what
each call can reach* to a disjoint prefix, but both calls still run as the
same `sharko-hub-role` principal — Sharko's cluster-credential provider and
its addon-secret provider don't assume two different IAM roles for a plain
Secrets Manager read (only cluster-credential EKS-token minting supports a
per-cluster `roleArn`; see
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md#c-sharkos-own-role-for-pushing-addon-secrets-and-running-tests)).
So this is real blast-radius reduction — a leaked policy edit or a bug that
sends the wrong prefix to `GetSecretValue` is refused by AWS, not just by
Sharko's own code — but it isn't full identity separation. Full separation
would need Sharko to assume a distinct IAM role per provider even for plain
value reads, which the current code doesn't do. Treat it as a known
follow-up, not a solved problem, if your threat model requires two fully
separate principals.

**2. NetworkPolicy — restrict Sharko's egress to what it actually calls:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: sharko
  namespace: sharko
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: sharko
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
  egress:
    # ArgoCD API (cluster-connection Secret reads/writes go through the
    # Kubernetes API, not a network call to ArgoCD, but Sharko also calls
    # ArgoCD's REST API for applications/projects).
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd
    # DNS
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
    # HTTPS out — your Git provider, AWS Secrets Manager / STS endpoints,
    # and any managed cluster's Kubernetes API Sharko pushes addon secrets
    # to. Narrow this to an IP allowlist if your platform supports it;
    # NetworkPolicy alone can't match on hostname.
    - ports:
        - port: 443
          protocol: TCP
```

This is a starting point, not a finished policy — the exact egress
destinations depend on which Git provider, which AWS region, and which
managed clusters you actually run. Test with `kubectl exec` into the
Sharko pod and a `curl` to each expected destination after applying it,
before relying on it in production.

## Webhook Security

`POST /api/v1/webhooks/git` accepts push events from your Git provider so
Sharko notices a change sooner. It is the one route that takes no login and no
API token, so a shared secret is the only thing in front of it.

**It ships closed.** `secrets.webhookSecret` is empty by default, and while it
is empty Sharko refuses every call to this endpoint. There is no setting that
turns the signature check off and leaves the endpoint answering — an empty
value closes the door rather than opening it.

To switch it on:

1. Generate a random secret: `openssl rand -hex 32`
2. Set it in Sharko: `secrets.webhookSecret: "<secret>"` (or the `SHARKO_WEBHOOK_SECRET` env var)
3. Set the same secret in your Git provider's webhook settings

Sharko then checks the `X-Hub-Signature-256` header on every call. Anything
that does not match is refused with `401 Unauthorized`.

Every refusal reads the same, whether no secret is set, no signature arrived,
or a signature did not match. That is deliberate: it means nobody can use the
endpoint to find out how your server is configured.

## Secrets Provider Security Model

Sharko's secrets reconciler uses a push-based model:

- Sharko fetches secrets from the provider (AWS SM or K8s Secrets) at reconcile time
- Values are **never cached** in memory or on disk between reconcile cycles
- Secrets are pushed directly to remote clusters via temporary kubeconfig connections
- All Sharko-managed secrets are labeled `app.kubernetes.io/managed-by: sharko`
- ArgoCD must exclude these secrets from management (see [Configuration](configuration.md#secrets-reconciler))

This means the blast radius of a Sharko compromise is limited to the window between reconcile cycles — there is no persistent plaintext store on the Sharko pod.

## Secrets Management Recommendations

- Use `existingSecret` with **Sealed Secrets** or **External Secrets Operator** instead of passing tokens as Helm values
- Enable **RBAC audit logging** in your cluster to track Sharko's API calls
- Rotate GitHub PATs and ArgoCD tokens periodically via the Settings UI
- Do not set `SHARKO_DEV_MODE=true` in production — it allows credential fallback via environment variables
- Set `SHARKO_WEBHOOK_SECRET` before you expect the webhook endpoint to do anything — until it is set, Sharko refuses every call to it
- **Leave "Allow legacy inline credentials" off** (`allow_inline_credentials`, Settings → same section as Connectivity Probe, default **false**). With the default in place, registration only accepts a pointer to an already-stored secret, an EKS token mint, or no credentials at all — GitOps-clean secret-store pointers for every cluster, and no path where sensitive kubeconfig bytes travel inside the request itself. Enabling the setting is the legacy escape hatch for installs that still depend on pasted registrations; know what it costs: a pasted credential exists only in the live ArgoCD cluster Secret and **cannot be recovered from Git** if that Secret is lost. To move existing pasted connections onto a supported provider, follow [Migrating Off Pasted (Inline) Credentials](migrate-inline-credentials.md). Once scoped RBAC ships (see the [roadmap](../community/roadmap.md)), this is planned to become a per-role permission rather than one server-wide switch — until then, it's all-or-nothing for every admin. Note that the switch only exists where a settings store does, which means in-cluster: run Sharko out of cluster and pasted registration is refused with no way to enable it, which is the correct fail-closed behaviour and is spelled out in [Connections → Allow legacy inline credentials](../user-guide/connections.md#allow-legacy-inline-credentials).

## Tiered Git Attribution (v1.20+)

Sharko classifies every mutating endpoint as **Tier 1** (operational) or **Tier 2** (configuration) and resolves the Git author accordingly:

| Tier | Examples | Token used | Commit author | Trailer |
|---|---|---|---|---|
| **Tier 1** | cluster register/remove, addon enable/disable, addon upgrade, PR refresh, connection CRUD, AI config | Service token | `Sharko Bot` | `Co-authored-by: <user>` |
| **Tier 2** | edit addon catalog metadata, edit values | Per-user PAT if configured, else service token | The user (per-user PAT) or `Sharko Bot` (fallback) | None (per-user) or `Co-authored-by: <user>` (fallback) |
| **Personal / Auth / Webhook** | login, set-own-PAT, inbound webhooks | n/a | n/a | n/a |

Each user can configure a personal GitHub PAT under **Settings → My Account**. PATs are stored encrypted at rest with `SHARKO_ENCRYPTION_KEY` (AES-256-GCM, the same key used by the connection store) under the `<username>.github_token` key in the auth Secret.

The audit log records the resolved attribution mode on every mutating entry:

| `attribution_mode` | Meaning |
|---|---|
| `service` | Service token used; no human identified on the commit (e.g. webhooks) |
| `co_author` | Service token used; user listed in `Co-authored-by:` trailer |
| `per_user` | Per-user PAT used; commit `Author` IS the user |

When a user performs a Tier 2 action without a personal PAT configured, the response includes `attribution_warning: "no_per_user_pat"` and the UI renders a banner pointing to **Settings → My Account**.

For the full design rationale and the V2.x roadmap that builds on this foundation, see `docs/design/2026-04-16-attribution-and-permissions-model.md`.

## v4 threat model

The threat model lives at [`docs/design/2026-08-08-threat-model-v4.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-08-08-threat-model-v4.md). It is a full rewrite for v4, written in plain language for an operator rather than a security-review committee — it replaces the older v2.0.0 document, which used a heavier STRIDE/OWASP/SLSA framework structure. It walks through the four checks that guard a secret value on its way through Sharko (one delivery path shared by the scheduled engine and "refresh now," the backend boundary, the destination TLS check, and the ownership gate that stops Sharko overwriting a Secret it doesn't own), where a raw secret value exists and for how long, what permissions Sharko holds and how to cut them down, the residual risk if the Sharko pod itself is compromised, and a plain list of what is honestly not built yet. The companion review-prep bundle scoped for an external security consultant against the older v2.0.0 baseline is kept for historical reference at `.bmad/output/reviews/v2-security-review-prep.md`.

## SSRF guard on URL-fetching endpoints

Several endpoints fetch from a user-supplied URL (e.g. `GET /api/v1/catalog/validate` pulls `<repo>/index.yaml` from a Helm repo URL the user pastes into the Marketplace). To prevent an authenticated user from coaxing the server into hitting cluster-internal addresses (the K8s API, ArgoCD, the cloud-provider metadata service), Sharko ships a built-in SSRF guard that runs in front of every such handler.

The guard rejects URLs that resolve to:

| Range | Reason |
|---|---|
| `127.0.0.0/8`, `::1` | Loopback |
| `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` | RFC1918 private |
| `169.254.0.0/16`, `fe80::/10` | Link-local (cloud metadata services) |
| `fc00::/7` | IPv6 ULA |
| Multicast / unspecified | Defense in depth |

A blocked request returns HTTP 200 with `error_code: "ssrf_blocked"` (matches the rest of the catalog-validate failure taxonomy so the UI's switch table doesn't need to branch on HTTP status).

### Optional allowlist

For higher-assurance deployments, set `SHARKO_URL_ALLOWLIST` to restrict outbound fetches to a fixed set of hostnames:

```yaml
extraEnv:
  - name: SHARKO_URL_ALLOWLIST
    value: "charts.jetstack.io,charts.bitnami.com,api.scorecard.dev"
```

When set, only the listed hostnames pass the guard — every other host is rejected with `ssrf_blocked: not_in_allowlist`. When unset, the guard falls back to the default deny-list above (RFC1918 + loopback + link-local + ULA), which is appropriate for self-hosted Sharko behind a network policy.

The guard runs in addition to (not instead of) any Kubernetes NetworkPolicy fronting the Sharko pod. Treat it as defense-in-depth — operators of production clusters should still apply egress NetworkPolicy that pins Sharko to its required external endpoints.

## Secret-leak guard on AI annotation

When AI annotation is enabled (V121-7), Sharko scans every upstream `values.yaml` for secret-like patterns (AWS keys, GitHub PATs, JWTs, PEM blocks, Slack tokens, Google API keys, generic API key/password assignments, high-entropy base64 blobs). On a match the LLM call is **hard-blocked** — there is no override.

Every block emits a dedicated audit-log entry with the event name `secret_leak_blocked` so security review can grep one stable token across the audit log:

```bash
curl -H "Authorization: Bearer $SHARKO_TOKEN" \
  "https://sharko.example.com/api/v1/audit?action=block&limit=200" \
  | jq '.[] | select(.event == "secret_leak_blocked")'
```

The audit `Detail` field carries the source handler (`addon_add`, `ai_annotate`, or `values_refresh`), the chart + version, the match count, and the deduplicated list of pattern names that fired. The actual matched bytes are never logged, never stored, and never returned in API responses.
