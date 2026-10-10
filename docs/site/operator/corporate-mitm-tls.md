# Corporate TLS MITM / SSL Inspection

Some company networks open up every outgoing HTTPS connection at a proxy,
look inside, and then sign it again with the company's own root
certificate (a private CA). Zscaler and Bluecoat work this way, and so do
many in-house gateways. This is called TLS inspection, or a "man in the
middle" (MITM) proxy.

The containers in your cluster do not know that company certificate. So
when `argocd-repo-server` fetches a Git repository or a Helm chart through
the proxy, the check of the certificate fails. ArgoCD Applications then
stay `Unknown` or `OutOfSync`, even though the addresses can be reached
from inside the cluster.

This is not a Sharko bug. The fix is to give ArgoCD the company
certificate, and that goes in **ArgoCD's own Helm values**. In short:
get the company root certificate, mount it into `argocd-repo-server`, and
restart it.

If your network does not inspect TLS, you do not need this page.

---

## Symptoms

What you see when this happens:

- ArgoCD Applications stay `Unknown` or `OutOfSync` after `sharko init` or
  after you turn on an addon. The sync error names the repository address
  (often `github.com`, or your own Helm chart registry).
- The `argocd-repo-server` pod logs have this exact line:

  ```
  x509: certificate signed by unknown authority
  ```

- Sharko shows the ArgoCD sync error on the cluster or addon card, word for
  word.
- Sharko itself may fail too. Sharko also talks to your Git host, to Helm
  chart repositories and to catalog sources over HTTPS, so its own Git
  connection or catalog can show the same certificate error. See
  [Sharko needs the certificate too](#sharko-needs-the-certificate-too).
- No Sharko alert fires for this. You see it in the ArgoCD Application
  status, not in Sharko's metrics.

If the `x509: certificate signed by unknown authority` line is not there,
and you see `dial tcp: lookup` or `i/o timeout` instead, the problem is the
network or DNS, not TLS inspection. See
[`git-provider-unreachable.md`](git-provider-unreachable.md) and the network
policy part of that page.

---

## Diagnosis

These three checks tell you if the proxy is signing the connections again,
or if ArgoCD is broken in some other way. Run them in this order.

### 1. Is the x509 line in `argocd-repo-server` logs?

```sh
kubectl logs -n argocd deploy/argocd-repo-server --tail=500 | grep -i x509
```

When TLS inspection is the cause, you see at least one line with
`x509: certificate signed by unknown authority`. If you see a different
TLS error instead (`x509: cannot validate certificate for ... because it
doesn't contain any IP SANs`, `tls: handshake failure`,
`tls: server selected unsupported protocol`), this page is not your
problem. Those come from the server or the protocol. Stop here and follow
ArgoCD's own troubleshooting.

### 2. Does the proxy actually re-sign the cert?

Run this from inside the cluster. It shows which certificate the cluster
really gets:

```sh
kubectl run -n argocd --rm -i --restart=Never --image=alpine:3 \
  openssl-probe -- sh -c 'apk add --no-cache openssl >/dev/null && \
  openssl s_client -connect github.com:443 -showcerts < /dev/null 2>/dev/null \
  | grep -A1 "issuer="'
```

If the proxy signs again, the issuer is **your company's CA**. If the issuer
is a public CA (DigiCert, Let's Encrypt, Sectigo, Google Trust Services and
so on), nothing is inspecting the connection, and the cause is something
else.

### 3. Is the corporate CA already mounted in the pod?

```sh
kubectl exec -n argocd deploy/argocd-repo-server -- \
  ls -l /etc/ssl/certs/ 2>/dev/null | grep -i corporate
```

- No output: the fix was never applied. Carry on with Mitigation below.
- A `corporate-ca.crt` file is there, but the logs still show `x509`: the
  file is mounted but it is empty or the wrong certificate. Go to
  [step 5 (Verify)](#5-verify).

---

## Mitigation (try in order)

The fix goes in **your ArgoCD Helm values**, not in Sharko's. Sharko does
not install ArgoCD for you. You install ArgoCD first, on its own (see
[Installation](installation.md)). Sharko is tested with ArgoCD v3.3 to v3.5.
Sharko's `values.yaml` only has a comment that points to this page.

### 1. Capture the corporate root CA

Ask your IT team for the company root CA. If they cannot give it to you
soon, you can copy it from any HTTPS connection that goes through the
proxy:

```sh
openssl s_client -connect github.com:443 -showcerts < /dev/null 2>/dev/null \
  | awk '/-----BEGIN CERTIFICATE-----/,/-----END CERTIFICATE-----/' \
  > corporate-ca.crt
```

The last certificate in the chain is the root the proxy uses. Check that
you have the right one:

```sh
openssl x509 -in corporate-ca.crt -noout -issuer -subject
```

The issuer and the subject should both name your company's CA, not a
public CA. Make sure it covers every host you need: GitHub (or your Git
host), your Helm chart registry, and any internal Git server.

### 2. Create a ConfigMap in the `argocd` namespace

```sh
kubectl create configmap sharko-extra-ca-bundle -n argocd \
  --from-file=ca.crt=corporate-ca.crt
```

Put the ConfigMap in the namespace where ArgoCD is installed (usually
`argocd`).

### 3. Patch your ArgoCD Helm values

Add this to **your ArgoCD Helm values file**. That is the file you pass to
`helm upgrade argo-cd argo/argo-cd -f ...`. It is **not** Sharko's
`values.yaml`.

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

This puts the company certificate in the folder where the container keeps
its trusted certificates. `SSL_CERT_DIR` tells the programs in
`argocd-repo-server` to read that folder when they start.

### 4. Apply and restart

```sh
helm upgrade argo-cd argo/argo-cd -n argocd -f your-argocd-values.yaml
kubectl rollout restart deployment/argocd-repo-server -n argocd
```

`helm upgrade` may not restart the pod, so restart it yourself with the
second command.

### 5. Verify

Wait about 30 seconds for the new `argocd-repo-server` pods to start. Then
check the logs again:

```sh
kubectl logs -n argocd deploy/argocd-repo-server --tail=200 | grep -i x509
```

No output means the fix worked. If the line still shows up, check these:

- The ConfigMap is in the same namespace as `argocd-repo-server`.
- The key inside the ConfigMap is `ca.crt`, the same as the `subPath`.
- The file from step 1 really holds the company root certificate.
- The file is there inside the pod:

  ```sh
  kubectl exec -n argocd deploy/argocd-repo-server -- \
    ls -l /etc/ssl/certs/corporate-ca.crt
  ```

---

## Sharko needs the certificate too {#sharko-needs-the-certificate-too}

The steps above fix ArgoCD only. The Sharko pod also makes HTTPS calls
through the same proxy: to your Git host, to Helm chart repositories, and
to catalog sources. Those can fail with the same certificate error.

The Sharko Helm chart has no way today to add a company certificate to the
Sharko pod. If you need this, please
[open a GitHub issue](https://github.com/MoranWeissman/sharko/issues).

---

## Root-cause patterns

### Outbound TLS inspection re-signs server certs

The proxy opens the HTTPS connection, looks at what is inside, then sends
it on, signed with its own certificate. The certificate that reaches
`argocd-repo-server` is signed by the company root, not by the public CA
the real host uses. The container does not trust the company root, so the
check fails with `x509: certificate signed by unknown authority`. This is
by far the most common case, and it is the one the Mitigation steps fix.

### Egress proxy that requires `HTTPS_PROXY` env

Some networks do not sign connections again, but make all outgoing HTTPS go
through a forward proxy. If `HTTPS_PROXY` is not set on the
`argocd-repo-server` pod, it tries to connect directly and the firewall
drops the connection. This looks alike, but the log line is different
(`connection refused` or `i/o timeout`, not `x509`). It is **not** the case
this page fixes. Set `HTTPS_PROXY` with `repoServer.env` in the ArgoCD Helm
values instead.

### Bundling the CA only in `configs.tls.certificates`

A common mistake is to set `configs.tls.certificates` in the ArgoCD Helm
values. That fills `argocd-tls-certs-cm`, which ArgoCD uses to trust the
certificates of specific Git servers you name. It does **not** add a
certificate that `argocd-repo-server` trusts for every outgoing HTTPS call.
Use the ConfigMap and mount from the Mitigation steps.

---

## Prevention

- **Ask before you install.** Add "does our network inspect TLS?" to your own
  install checklist. If the answer is yes, apply this page before
  `sharko init`, not after.
- **Check after you install.** After an install, look for
  `x509: certificate signed by unknown authority` in the
  `argocd-repo-server` logs. If the line is there, stop and apply this page.
- **Build your own image, if mounting is hard.** Where mounting an extra
  certificate is awkward, you can build your own `argocd-repo-server` image
  with the company certificate already trusted. The cost: every certificate
  change and every ArgoCD upgrade needs a new image. Mounting is better, but
  this works for networks with no internet access.

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) — when the
  error is `i/o timeout` or `connection refused` instead of the `x509`
  line, Git cannot be reached for a different reason.
- [`catalog-parse-failure-on-startup.md`](catalog-parse-failure-on-startup.md)
  — when a catalog source address fails the TLS check, the failing pod is
  Sharko, not `argocd-repo-server`. See
  [Sharko needs the certificate too](#sharko-needs-the-certificate-too).
- [`installation.md`](installation.md) — the order to install ArgoCD and
  Sharko in. The fix on this page changes the ArgoCD Helm release, not the
  Sharko one.
- [`failure-mode-index.md`](failure-mode-index.md) — the full list of
  failures with a page of their own.

## Escalation

If the steps above do not fix it, please
[open a GitHub issue](https://github.com/MoranWeissman/sharko/issues).
Put in:

- a link to this page
- the output of `openssl x509 -in corporate-ca.crt -noout -issuer -subject`
  (the names of company CAs are not secret, but take out internal host
  names if your rules say so)
- the Sharko version (`sharko version`, or the Helm chart version)
- the ArgoCD Helm chart version
- the full `x509` line from the `argocd-repo-server` logs

Sharko is a technical preview, looked after by one person in their own
time. There is no on-call support.
