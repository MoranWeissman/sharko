# Security

Sharko is a technical preview. This page says how it protects itself
today, and what you should set up around it.

This page covers signing in, sessions, API keys, roles, running behind a
proxy, what Sharko may do in ArgoCD and in Kubernetes, and how Sharko keeps
the credentials you give it. The other security topics have their own pages,
listed at the [end of this page](#other-security-pages).

## Rate Limiting

Sharko limits how fast one client address can call it:

| What | Limit |
|------|-------|
| Signing in (`POST /api/v1/auth/login`) | 5 tries per minute for each client address |
| Every request that changes something (`POST`, `PUT`, `PATCH`, `DELETE`) | 30 requests per minute for each client address |

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

The same resolved address is used for rate limiting and for the client address recorded in security log lines, so the two can never disagree. Note that the **Activity History records no client address at all** — a refused request does not leave an Activity History entry naming the caller.

!!! warning
    List only proxies you control. A proxy you list is one you trust to overwrite whatever forwarding header its own client sent; if it passes the client's header through untouched, that client is choosing its own address again.

!!! tip
    Put an ingress in front of Sharko and terminate TLS there as well. That is worth doing on its own, but it is defense in depth — it does not replace setting `SHARKO_TRUSTED_PROXIES`, and Sharko will not believe an ingress it has not been told about.

## Authentication

### Admin Password

On first install Sharko makes a random admin password. It keeps only a
bcrypt hash of it (a one-way scramble that cannot be turned back into the
password). It also puts the plain password in the
`sharko-initial-admin-secret` Secret, the same way ArgoCD uses
`argocd-initial-admin-secret`, so you can read it once:

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

Read it once and change it right after your first login.

### No Users Configured

You cannot run Sharko with sign-in turned off, outside demo mode. Say
Sharko starts and finds **no** users at all: no accounts from the chart, no
`SHARKO_BOOTSTRAP_ADMIN_PASSWORD`, and no `SHARKO_AUTH_USER` /
`SHARKO_AUTH_PASSWORD`. Then it makes an `admin` user itself, with a random
password, and tells you where to find it:

- **In a cluster:** the password goes into the `sharko-initial-admin-secret` Secret. Read it with the command above. Set `SHARKO_WRITE_INITIAL_ADMIN_SECRET=false` if you want the password to show only in the startup log.
- **Outside a cluster (the `sharko` binary on your machine):** the password is saved to `~/.sharko/initial-admin.json`. Only your user can read that file. Change the path with `SHARKO_INITIAL_ADMIN_FILE`. The next start uses the same password.

Either way, the password is also printed once when the server starts, and
every API request needs a login from the very first moment. If the user list
is somehow empty when a request comes in, the request gets a 401. Sharko
never falls back to running open.

The one exception is **demo mode** (`sharko serve --demo`). It makes the
fixed demo users `admin/admin` and `qa/sharko` and asks you to log in with
them. Demo mode is for trying Sharko with fake backends. Never point it at
real clusters.

To see which users are set up:

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

Sharko keeps only a bcrypt hash of each API key, never the key itself. You
see the key once, when you create it. Treat it like a password and keep it
in your CI system's secret store, for example GitHub Actions secrets.

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

Every user and every API key has one of three roles. The role decides which
changes (and a few reads) are allowed. Sharko checks it on **every**
request, not just at login.

| Role | Who it's for | Can do |
|------|---------------|--------|
| `viewer` | Anyone who should be able to look, not touch | See clusters, addons, connections, pull requests, the Activity History, and metrics. Set or clear their own personal Git token. Cannot create, change, or delete anything shared. |
| `operator` | The people who run day-to-day operations | Everything a viewer can do, plus: register/adopt/test/diagnose clusters, enable/disable addons, restart a stuck sync, create/update connections, test connections and credential providers, edit the addon catalog, create API tokens and renew *their own* tokens, trigger the secrets reconciler, and run the first-time setup. |
| `admin` | Whoever owns the Sharko install | Everything an operator can do, plus the actions that reach past the caller's own work: delete a connection, remove or unadopt a cluster, remove an addon from the catalog, create/delete/change the role of other users, revoke any token, renew *someone else's* token, change AI provider settings, save dashboard layouts, edit ArgoCD resource exclusions, create/delete addon secrets, delete a pull request, refresh third-party catalog sources, and flip the security settings (probe mode, inline credentials, self-heal). |

**New users and new API tokens get `viewer`** if no role is given. An admin
has to choose `operator` or `admin` on purpose, both at `POST /api/v1/users`
and at `POST /api/v1/tokens`.

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

If Sharko meets an action it does not know, only an admin may run it. So a
new change that someone forgot to give a role shows up as "only an admin can
use this yet", never as "anyone can use this".

### The honest limit: roles are coarse, not scoped

Roles are **not** tied to one cluster or one addon. An `operator` who can
enable addons can enable them on *any* registered cluster. There is no
"operator for cluster X only" and no "operator for addon Y only". The three
roles are the whole permission model.

This is a known gap. Roles per cluster or per addon are on the
[roadmap](../community/roadmap.md), and are not part of v4. If your teams
need to be kept apart today, give each team (or each group of clusters) its
own Sharko install, rather than one shared install with mixed trust.

### Where the roles are checked

One table says which role each action needs. Sharko uses the same table in
two places:

- **The REST API.** Every request that changes something checks its action
  against the table before it does anything else.
- **The AI assistant.** The few assistant tools that change things (turn an
  addon on or off, bump a catalog version, start an ArgoCD sync or refresh)
  check the same table before they touch Git or ArgoCD. So a viewer who asks
  the assistant to "enable datadog on prod" is refused, the same as if they
  had called the API.

Tests build the list of routes and assistant tools from the code itself. A
new route or tool with no role fails the build.

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

### Smallest ArgoCD policy for Sharko

This policy gives Sharko exactly what it needs to run repository setup, register and manage clusters, and read cluster and application status:

```csv
p, role:sharko, repositories, create, *, allow
p, role:sharko, repositories, update, *, allow
p, role:sharko, applications, create, default/*, allow
p, role:sharko, clusters, update, *, allow
p, role:sharko, clusters, delete, *, allow
g, role:sharko, role:readonly
g, sharko, role:sharko
```

Each line does one thing:

- **repositories create and update** — first-run setup adds your Git repository to ArgoCD so it can read the files Sharko writes there
- **applications create in default/\*** — first-run setup creates the bootstrap Application that deploys the Sharko engine chart
- **clusters update and delete** — when you change which addons a cluster has, or remove a cluster, Sharko changes the labels on its ArgoCD cluster secret; removing a cluster also deletes it
- **role:readonly** — reads applications, clusters, application sets and projects
- The last two lines connect the account name `sharko` to the role

Sharko writes the cluster Secret itself through Kubernetes, not through the ArgoCD API, so registering a cluster also needs Sharko's Kubernetes service account to have rights to write Secrets in the ArgoCD namespace. The Sharko Helm chart already grants that in the default setup — see the `rbac.argocdNamespace` value in `charts/sharko/values.yaml`.

To apply the policy, edit the `argocd-rbac-cm` ConfigMap in the ArgoCD namespace. You can do it by hand with `kubectl edit`, or with a patch file:

```yaml
# sharko-rbac-patch.yaml
data:
  policy.csv: |
    p, role:sharko, repositories, create, *, allow
    p, role:sharko, repositories, update, *, allow
    p, role:sharko, applications, create, default/*, allow
    p, role:sharko, clusters, update, *, allow
    p, role:sharko, clusters, delete, *, allow
    g, role:sharko, role:readonly
    g, sharko, role:sharko
```

Apply it:

```bash
kubectl patch configmap argocd-rbac-cm -n argocd \
  --type merge --patch-file sharko-rbac-patch.yaml
```

If you already have other policy lines in `policy.csv`, add the new lines to the existing content instead of replacing it.

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

This section is about the cluster Sharko is installed in. For the clusters
your addons run on, see
[What access Sharko and ArgoCD need on your clusters](cluster-access.md).

Sharko's default install gives it **no cluster-wide access to Secrets**.
Every Secret the Sharko ServiceAccount reads on the main cluster sits in one
of the few namespaces it really needs, never across the whole cluster.

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
| `Role` (`sharko-argocd-secrets`), in `rbac.argocdNamespace` | one namespace | Full CRUD on **every** Secret in the ArgoCD namespace, not only the ones Sharko created — this is where Sharko reads and writes the ArgoCD cluster-connection Secrets. |
| `Role` (`sharko-secrets-provider`), one per namespace in `k8sSecretsProviderNamespaces` (the release namespace is always included, whether or not you list it) | one namespace each | `get/list` on **every** Secret in that namespace — there is no `resourceNames` restriction, so this is not limited to the Secrets Sharko manages. It exists for the **k8s-secrets** cluster-credential provider and/or the **k8s-secrets** addon-secret provider, but it is granted even when neither is configured. |
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

The connection tokens you save (ArgoCD tokens and Git tokens) are kept in the
`sharko-connections` Secret. Sharko encrypts them with **AES-256-GCM**, using
the key in `SHARKO_ENCRYPTION_KEY`. The chart makes that key at random on the
first install, keeps it in Sharko's own Secret, and keeps the same key on every
upgrade.

!!! tip "Changing the encryption key"
    Sharko cannot read tokens saved with an old key. If you change
    `SHARKO_ENCRYPTION_KEY`, enter the tokens for every connection again in
    the Settings page.

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

## Scoped AWS IRSA policy {#scoped-aws-irsa-policy}

If Sharko reads secrets from AWS Secrets Manager, the IAM policy for its own
AWS role is on
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md#c-sharkos-own-role-for-pushing-addon-secrets-and-running-tests).
That page also says the limit plainly: you can give cluster credentials and
addon secrets two different name prefixes, so each read can only reach its own
prefix. But both reads still run as the same AWS role, so this is not two
separate identities.

## Other security pages

The rest of the security topics are on their own pages:

<a id="ssrf-guard-on-url-fetching-endpoints"></a>

- [Addresses, network and webhook](security-network-and-addresses.md):
  which addresses may carry a token, the guard that stops Sharko fetching
  from inside your network, a network policy to start from, the Git webhook,
  and the HTTP security headers.
- [Secrets, threats and the AI guard](security-threat-model.md): what
  Sharko does with a secret value, what is not protected, advice for
  handling secrets (including pasted cluster credentials), who is named on
  Git commits, and the AI secret guard.
- [What access Sharko and ArgoCD need on your clusters](cluster-access.md):
  what Sharko does on the clusters your addons run on, and why it holds
  the same access as ArgoCD there.
- [A hardened install, step by step](security-hardened-example.md): the
  pod security settings, and one full example with Helm values, the AWS
  role and a network policy.
- To report a security problem, follow
  [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md).
