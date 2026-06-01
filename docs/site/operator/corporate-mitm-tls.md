# Corporate TLS MITM / SSL Inspection

**Severity:** P2

> **Verified:** Authored 2026-06-01 against `main` HEAD as part of
> V2-4.4 (existing-runbook style compliance refresh). The
> `argocd-repo-server` mount path and ConfigMap shape are verified
> against the upstream argo-cd Helm chart values surface as shipped
> with ArgoCD 2.x. The `openssl s_client` capture and the
> `kubectl exec` mount probe were re-run from a smoke shell against a
> kind cluster. Re-verify before changing the ConfigMap name in
> Sharko's `values.yaml` informational comment block — the comment and
> this runbook share a name and an operator searching that comment will
> Ctrl-F the same string here.

Public Git fetches from `argocd-repo-server` are failing because your
corporate network is terminating outbound TLS at an inspection proxy
and re-signing certificates with a private root CA that the
`argocd-repo-server` container's trust store does not know. ArgoCD
Applications stay `Unknown` or `OutOfSync` even though the repo URLs
are reachable from inside the cluster. This is **not** a Sharko bug —
it is an environmental workaround that has to be applied to ArgoCD's
own Helm values. Severity is **P2** because the workaround is well
understood, the failure is reproducible per cluster, and the rest of
the fleet is unaffected; once applied, the fix is stable.

This page is short because the workaround is bounded: capture the
corporate root CA, mount it into `argocd-repo-server`, restart the
deployment. The same procedure handles Zscaler, Bluecoat, in-house
inspection gateways, and any other MITM proxy that re-signs outbound
TLS. If your environment does not run a TLS-inspecting proxy, you do
not need this runbook.

---

## Symptoms

What an operator sees when this fires:

- ArgoCD Applications stay `Unknown` or `OutOfSync` after `sharko init`
  or after enabling an addon, with the sync error pointing at the
  repo URL (typically `github.com` or your internal Helm chart
  registry).
- `argocd-repo-server` pod logs contain the exact line:

  ```
  x509: certificate signed by unknown authority
  ```

- Sharko UI surfaces this indirectly — the cluster card / addon card
  shows the ArgoCD sync error verbatim, but Sharko itself reports the
  Git connection as "active" (Sharko reaches Git fine; ArgoCD does
  not).
- `kubectl exec -n sharko deploy/sharko -- wget -qO- https://github.com`
  succeeds. `kubectl exec -n argocd deploy/argocd-repo-server -- wget -qO- https://github.com`
  fails with a TLS handshake error.
- No Sharko-specific alert fires. The alert that would surface this is
  upstream of Sharko — typically the ArgoCD Application status
  surface, not the Prometheus metric surface.

If the `x509: certificate signed by unknown authority` line is absent
and you instead see `dial tcp: lookup` or `i/o timeout`, the failure
is connectivity / DNS, not TLS interception — see
[`git-provider-unreachable.md`](git-provider-unreachable.md) and the
network-policy section there.

---

## Diagnosis

Where to look to confirm "the proxy is re-signing TLS" vs. "ArgoCD
itself is broken." Three checks, in this order.

### 1. Is the x509 line in `argocd-repo-server` logs?

```sh
kubectl logs -n argocd deploy/argocd-repo-server --tail=500 | grep -i x509
```

Expected on the failing path: at least one line containing
`x509: certificate signed by unknown authority`. If you see other
TLS errors (`x509: cannot validate certificate for ... because it
doesn't contain any IP SANs`, `tls: handshake failure`,
`tls: server selected unsupported protocol`), the failure is not
corporate MITM — those are server-side or protocol issues. Stop here
and follow ArgoCD's own troubleshooting.

### 2. Does the proxy actually re-sign the cert?

Run the OpenSSL probe from inside the cluster to capture what
`argocd-repo-server` is seeing:

```sh
kubectl run -n argocd --rm -i --restart=Never --image=alpine:3 \
  openssl-probe -- sh -c 'apk add --no-cache openssl >/dev/null && \
  openssl s_client -connect github.com:443 -showcerts < /dev/null 2>/dev/null \
  | grep -A1 "issuer="'
```

Expected on the MITM path: the issuer is **your corporate CA**, not
DigiCert / Let's Encrypt / Sectigo. If the issuer is a public CA
(DigiCert High Assurance / GTS / etc.), TLS is not being intercepted
— the failure has a different root cause.

### 3. Is the corporate CA already mounted in the pod?

```sh
kubectl exec -n argocd deploy/argocd-repo-server -- \
  ls -l /etc/ssl/certs/ 2>/dev/null | grep -i corporate
```

Expected if a previous attempt was made: a `corporate-ca.crt` symlink
or file. Empty output = the workaround was never applied (this
runbook is the right place). A file present but logs still show
`x509` = the mount is in place but the file is empty or the wrong CA
— jump to Mitigation step 5 (Verify).

---

## Mitigation (try in order)

The workaround lives in **your ArgoCD Helm values**, not in Sharko's.
Sharko does not package the argo-cd chart as a subchart — ArgoCD is
installed separately, ahead of Sharko (see
[Installation](installation.md)). Sharko's `values.yaml` carries an
informational comment block that points back here.

### 1. Capture the corporate root CA

Ask IT for the corporate root CA bundle. If they cannot produce one
quickly, extract it from any TLS handshake that goes through the
proxy:

```sh
openssl s_client -connect github.com:443 -showcerts < /dev/null 2>/dev/null \
  | awk '/-----BEGIN CERTIFICATE-----/,/-----END CERTIFICATE-----/' \
  > corporate-ca.crt
```

The bottom-most certificate in the chain is the root your proxy is
using. Verify it covers the hosts you need (GitHub, your Helm chart
registry, your internal Git server):

```sh
openssl x509 -in corporate-ca.crt -noout -issuer -subject
```

Expected: issuer and subject both point at your corporate CA, not a
public CA.

### 2. Create a ConfigMap in the `argocd` namespace

```sh
kubectl create configmap sharko-extra-ca-bundle -n argocd \
  --from-file=ca.crt=corporate-ca.crt
```

The ConfigMap lives in the namespace where ArgoCD is installed
(default: `argocd`). Use a different namespace only if your ArgoCD
release is installed elsewhere.

### 3. Patch your ArgoCD Helm values

Add the block below to **your ArgoCD Helm values file** — the one you
pass to `helm upgrade argo-cd argo/argo-cd -f ...`, **not** Sharko's
`values.yaml`:

```yaml
repoServer:
  volumes:
    - name: extra-ca-bundle
      configMap:
        name: sharko-extra-ca-bundle
  volumeMounts:
    - name: extra-ca-bundle
      mountPath: /etc/ssl/certs/corporate-ca.crt
      subPath: ca.crt
      readOnly: true
  env:
    - name: SSL_CERT_DIR
      value: /etc/ssl/certs
```

The mount lands the corporate CA at a path OpenSSL already trusts.
`SSL_CERT_DIR` is set explicitly so the Go HTTP client inside
`argocd-repo-server` picks it up at startup.

### 4. Apply and restart

```sh
helm upgrade argo-cd argo/argo-cd -n argocd -f your-argocd-values.yaml
kubectl rollout restart deployment/argocd-repo-server -n argocd
```

`rollout restart` is required even though `helm upgrade` triggers
one — Helm restarts on template diffs, and a ConfigMap value change
alone may not produce one.

### 5. Verify

Wait ~30 seconds for the new repo-server pods to come up. The
diagnosis steps above should now all flip green:

```sh
kubectl logs -n argocd deploy/argocd-repo-server --tail=200 | grep -i x509
```

Empty output = the workaround landed. If the line still appears,
double-check:

- The ConfigMap is in the same namespace as `argocd-repo-server`.
- The key inside the ConfigMap is `ca.crt` (matching the `subPath`).
- The CA bundle in step 1 actually contains the corporate root.
- The mount exists inside the pod:
  ```sh
  kubectl exec -n argocd deploy/argocd-repo-server -- \
    ls -l /etc/ssl/certs/corporate-ca.crt
  ```

---

## Root-cause patterns

### Outbound TLS inspection re-signs server certs

The proxy (Zscaler, Bluecoat, an in-house gateway) terminates TLS,
inspects plaintext, re-encrypts with its own cert chain, and forwards
on. The cert chain it presents to `argocd-repo-server` is signed by
the corporate root, not by the public CA the upstream host actually
uses. OpenSSL's default trust store does not include the corporate
root, so the handshake fails with `x509: certificate signed by
unknown authority`. This is the single most common variant of the
failure mode and the one the Mitigation section targets directly.

### Egress proxy that requires `HTTPS_PROXY` env

Some environments do not re-sign TLS but require all outbound HTTPS
to traverse a forward proxy. If `HTTPS_PROXY` / `https_proxy` is not
set on the `argocd-repo-server` pod, fetches go directly and the
egress firewall drops them. The symptom looks similar but the log
line is different (`connection refused` or `i/o timeout`, not
`x509`). This is **not** the corporate-MITM case — set
`HTTPS_PROXY` via `repoServer.env` on the argo-cd chart instead.

### Bundling the CA only in `configs.tls.certificates`

A common misconfiguration: operators try to fix this by setting
`configs.tls.certificates` on the argo-cd chart. That value
populates `argocd-tls-certs-cm`, which ArgoCD uses for **inbound**
TLS and for Git server certificate pinning. It does **not** add CAs
to the OpenSSL trust store that `argocd-repo-server` uses for
outbound HTTPS to public hosts. The ConfigMap + volume-mount approach
in the Mitigation section is the supported path.

---

## Prevention

How to make this failure mode less likely going forward.

- **Pre-install survey:** document corporate-proxy presence as a
  pre-install checklist item in your internal Sharko deployment guide.
  Operators who answer "yes" hit this page before `sharko init`, not
  after.
- **Helm values audit:** wire a small check into your pre-install CI
  that grep's for `x509: certificate signed by unknown authority` in
  `argocd-repo-server` logs during the post-install smoke pass. If
  the line appears, fail the install and surface this runbook.
- **Image build alternative for hardened environments:** for
  environments where mounting an external CA is operationally
  awkward, build a custom `argocd-repo-server` image with the
  corporate CA pre-trusted. This couples CA rotation to your image
  pipeline and blocks ArgoCD upgrades behind a rebuild — the
  Mitigation section above is preferred — but it is a documented
  alternative for air-gapped deployments.

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) — if
  the symptom is `i/o timeout` / `connection refused` rather than the
  `x509` line, Git connectivity is failing for a different reason.
- [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
  — when the third-party catalog URL itself fails TLS handshake the
  fix is the same (mount the CA), but the failing component is the
  Sharko pod, not `argocd-repo-server`.
- [`installation.md`](installation.md) — Sharko / ArgoCD install
  sequence; the workaround in this runbook patches values on the
  argo-cd Helm release, not on the sharko release.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory
  of operator-facing failures.

## Escalation

If the mitigations above do not resolve the failure within one
business day, email the maintainer: `moran.weissman@gmail.com`.
Include:

- The runbook URL you used (this page)
- The output of `openssl x509 -in corporate-ca.crt -noout -issuer -subject`
  (issuer + subject of the captured CA — names of corporate CAs are
  not sensitive, but redact internal hostnames if your policy
  requires)
- The Sharko version (`sharko version` or the Helm chart version)
- The ArgoCD Helm chart version
- The full `x509` log line from `argocd-repo-server`

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P2)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 5 steps in priority order
- [x] Root-cause patterns: 3 named causes
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (page is in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name from prometheusrules.yaml referenced — N/A (no Sharko alert)
-->
