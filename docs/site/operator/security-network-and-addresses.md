# Security: addresses, network and webhook

This page is part of the [Security](security.md) reference. It covers:

- which addresses may carry a token, and which may not
- the guard that stops Sharko fetching from inside your network
- a network policy to start from
- the Git webhook
- the HTTP security headers Sharko sends

## Which addresses may carry a token {#which-addresses-may-carry-a-token}

There is one place where an address may carry a token. Everywhere else, an
address must have no token in it.

**A catalog source address in `SHARKO_CATALOG_URLS` may carry a token, inside
its path.** A catalog source is an extra list of addons that the Marketplace
reads (see [Catalog sources](catalog-sources.md)). A private one is reached
through an address with the token in the path, like
`https://catalogs.example.com/private/<token>/catalog.yaml`. This is allowed
for one reason: `SHARKO_CATALOG_URLS` is an environment setting on the pod,
so it is never committed to Git. Sharko never shows that address. It is not
in any API answer, and every catalog source gets the same fixed label in
metrics. Only the path can hold the token. Sharko drops any name or password
written before the host, and any query string or `#` part, before it uses
the address.

**No other address may carry a token.** That means:

- the catalog sources file in Git (`configuration/marketplace-sources.yaml`),
  which takes only public addresses. See
  [Marketplace sources file](marketplace-sources-config.md).
- the chart repository address on a catalog entry, and any repository or
  chart address you send to the API or the CLI. These end up in a file in
  your Git repository. The rules for them are below.

## Catalog repository addresses carry no credentials {#catalog-repository-addresses-carry-no-credentials}

A catalog entry names the Helm repository its chart comes from. Sharko writes
that address into a YAML file and commits it to your Git repository. Git
keeps history. A token in a commit is in every clone, fork, CI cache and
backup, and a later edit does not remove it.

So Sharko refuses to save an address that has any place in it where a
credential could sit. This is the message you see:

> Catalog repository URLs in the technical preview must be ones Sharko can
> read in full: a host, an optional port, and an optional path. User
> information in the address, a query string, and a fragment are all refused,
> and so is an address Sharko cannot read. Use a credential-free base URL.

### The shapes that are refused

| Shape | Why |
|---|---|
| A name, or a name and password, written before the host and followed by `@` | The usual place a token goes. Sharko refuses it even when there is only a name and no password, because a token often sits in the name. |
| A query string, such as `https://charts.example/org/charts?ref=main` | A token can go in a query string. Sharko refuses **every** query string, even a harmless one. See below. |
| An empty query string, such as `https://charts.example/org/charts?` | Same reason. |
| A fragment, the part after `#` | A token can go there too. |
| An address Sharko cannot read, such as `https://charts.example:notaport/org/charts` | If Sharko cannot read the address, it cannot tell what is in it. "I could not tell" is not a yes. |

Some of these carry nothing secret. Sharko refuses them anyway, because it
checks the *shape* of the address, never whether the text looks like a
secret. A check that reads the text stops working the first time somebody
writes a credential in a shape nobody expected, and nobody notices. A check
on the shape cannot fail that way. The cost is that `?ref=main` is refused.
Take the query string off and the address is accepted.

The check cannot know whether a credential is really there, so the message
says the rule and never claims Sharko found one.

### The shapes that are accepted

The scheme (`https://`) is optional. An `@` inside the path is not user
information. All of these are accepted:

| Shape | Example |
|---|---|
| An ordinary address | `https://charts.example/org/charts` |
| No scheme | `charts.example/org/charts` |
| A host and a port | `localhost:8080` |
| An `@` in the path | `https://charts.example/org/charts@v1` |

### Where the rule is checked

Sharko checks the same rule everywhere a repository address comes in or goes
out:

- when you add or edit a catalog entry in the UI or the API, and when you
  paste an address to check it, so you hear about it while you are still
  typing
- in the CLI's `add-addon`
- every time Sharko writes the catalog file, whatever asked for the write
- when Sharko moves a repository from the older layout to the current one
- just before Sharko contacts a chart repository, so a refused address is
  never contacted, even if it is already in a file

### If your repository already has one

Sharko keeps running. The catalog file still loads and every other addon still
works. The one entry is marked as unusable, with the file and field named.
Sharko never shows the address, any part of it, or its length. It will not
deploy that addon, will not contact the address, and will not put the address
in any answer or log line.

While that entry is there, Sharko refuses every write to the catalog file,
because writing the file again would put the credential into a new commit.
Sharko will not quietly remove the address or quietly drop the entry. Either
would mean Sharko editing your repository behind your back.

**Fixing it takes three steps. The first one matters most:**

1. **Revoke or replace that credential** wherever it was issued. The token is
   in your Git history, so it is in every clone, fork, build cache and
   backup. Treat it as public.
2. **Remove it from the history**, not just from the current file. For
   example, use `git filter-repo`. If you only edit the file, the token is
   still one `git log -p` away.
3. **Put an address with no credential in the entry**, and commit that.

### Private chart repositories

This preview has no supported way to use a chart repository that needs a
sign-in. The right answer is to let a catalog entry point at a Kubernetes
Secret or an AWS Secrets Manager entry, the same way cluster credentials do.
That is work for after the preview. If you need it, say so in a
[GitHub issue](https://github.com/MoranWeissman/sharko/issues).

## The guard against fetching inside your network {#ssrf-guard-on-url-fetching-endpoints}

Two parts of the **Add Addon** form fetch from an address you type: checking
a chart (`GET /api/v1/catalog/validate`) and listing the charts in a
repository (`GET /api/v1/catalog/repo-charts`). Both read the repository's
`index.yaml`.

A logged-in user could try to use this to make Sharko fetch something inside
your own network, like the Kubernetes API, ArgoCD, or the cloud provider's
metadata service. This trick is called server-side request forgery (SSRF).
To stop it, Sharko checks every such address before it fetches.

The address must start with `http://` or `https://`. Sharko then looks up
the host name. If **any** address it gets back is in one of these ranges,
the fetch is refused:

| Range | What it is |
|---|---|
| `127.0.0.0/8`, `::1` | This machine (loopback) |
| `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7` | Private networks |
| `169.254.0.0/16`, `fe80::/10` | Link-local. Cloud metadata services live here. |
| Multicast and unspecified addresses | Never a real chart repository |

A refused fetch still answers with HTTP 200. The reply has
`error_code: "ssrf_blocked"`, the same way the form's other failures are
reported.

Catalog sources in `SHARKO_CATALOG_URLS` and the catalog sources file have
their own check for private addresses. On a home lab you can turn that one
off with `SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true`. Do not do that on a
network you do not trust.

### Allowing only some hosts

To go further, set `SHARKO_URL_ALLOWLIST` to the only host names the form may
fetch from:

```yaml
extraEnv:
  - name: SHARKO_URL_ALLOWLIST
    value: "charts.jetstack.io,charts.bitnami.com,api.scorecard.dev"
```

When it is set, any host not on the list is refused with the reason
`not_in_allowlist`. A host on the list still goes through the range check
above. When it is not set, only the range check applies.

This guard does not replace a network policy. Use both.

## Network policy {#network-policy}

Sharko does not ship a NetworkPolicy. In a sensitive environment, write one
that lets in only your ingress controller, and lets out only what Sharko
really calls:

- DNS
- ArgoCD
- the Kubernetes API of the cluster Sharko runs on
- your Git host
- AWS Secrets Manager and AWS STS, if you use them
- the Kubernetes API of each managed cluster that Sharko pushes addon secrets
  to

A full example is in
[A hardened install, step by step](security-hardened-example.md#step-3-a-network-policy).

## Webhook Security {#webhook-security}

Your Git host can send push events to `POST /api/v1/webhooks/git`, so Sharko
notices a change sooner. This is the one route that needs no login and no API
token, so a shared secret is the only thing in front of it.

**It ships closed.** `secrets.webhookSecret` is empty by default, and while it
is empty Sharko refuses every call to this endpoint. No setting turns the
signature check off and leaves the endpoint answering. An empty value closes
the door. It does not open it.

To turn it on:

1. Make a random secret: `openssl rand -hex 32`
2. Give it to Sharko: `secrets.webhookSecret: "<secret>"` in the Helm values,
   or the `SHARKO_WEBHOOK_SECRET` environment setting
3. Put the same secret in your Git host's webhook settings

Sharko then checks the `X-Hub-Signature-256` header on every call. Anything
that does not match is refused with `401 Unauthorized`.

Every refusal reads the same: no secret set, no signature sent, or a wrong
signature. That is on purpose, so nobody can use the endpoint to learn how
your server is set up.

Nothing else stops working while the webhook is closed. Sharko still picks
up changes on its own. The webhook only makes it notice sooner.

## Security Headers {#security-headers}

Sharko sends these HTTP security headers on every response:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | Limits where scripts, styles and frames may come from |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` (browsers use HTTPS only, for 1 year) |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |

Sharko sends `Strict-Transport-Security` only when the request came over
HTTPS. If TLS ends at your ingress, the ingress must send
`X-Forwarded-Proto: https` to Sharko, or the header is left out.
