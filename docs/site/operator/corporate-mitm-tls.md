# Corporate TLS MITM / SSL Inspection

Workaround for environments where a corporate proxy intercepts TLS to inspect traffic, breaking `argocd-repo-server`'s outbound fetches of public Git repos.

## Symptom

`argocd-repo-server` fails to clone or pull public Git repos (e.g. `github.com`). The pod logs contain:

```
x509: certificate signed by unknown authority
```

The Sharko UI surfaces this indirectly — applications stay `Unknown` or `OutOfSync` with a sync error pointing at the repo URL, even though the URL is reachable from inside the cluster.

## Cause

Your corporate network terminates outbound TLS at a proxy (Zscaler, Bluecoat, an in-house gateway, etc.) and re-signs server certificates with a private corporate root CA. The default trust store inside the `argocd-repo-server` container does not include that root, so OpenSSL refuses the connection.

This is **not a Sharko bug** and **not an ArgoCD bug** — both are working as intended. The fix is to inject your corporate root CA into the trust path that `argocd-repo-server` reads.

## When you need this

Apply this workaround **only** if the symptom above matches. Most installs do not need it. If `sharko init` and the Sharko `--demo` install both fetch their Git repos cleanly, skip this page.

## Scope

Sharko does not package the ArgoCD Helm chart as a subchart — ArgoCD is installed separately, ahead of Sharko (see [Installation](installation.md)). The fix therefore lives in **your ArgoCD Helm values**, not in Sharko's. Sharko's `values.yaml` carries an informational comment block pointing here.

## Workaround

### Step 1 — Capture the corporate root CA

Ask your IT team for the corporate root CA bundle. If they cannot produce one quickly, you can extract it from any TLS handshake that goes through the proxy:

```bash
openssl s_client -connect github.com:443 -showcerts < /dev/null 2>/dev/null \
  | awk '/-----BEGIN CERTIFICATE-----/,/-----END CERTIFICATE-----/' \
  > corporate-ca.crt
```

The bottom-most certificate in the chain is the root your proxy is using. Verify it covers the hosts you need (GitHub, your Helm chart registry, etc.) before continuing.

### Step 2 — Create a ConfigMap in the `argocd` namespace

```bash
kubectl create configmap sharko-extra-ca-bundle -n argocd \
  --from-file=ca.crt=corporate-ca.crt
```

The ConfigMap lives in the namespace where ArgoCD is installed (default: `argocd`). Use a different namespace here only if your ArgoCD release is installed elsewhere.

### Step 3 — Patch your ArgoCD Helm values

Add the block below to **your ArgoCD Helm values file** — the one you pass to `helm upgrade argo-cd argo/argo-cd -f ...`, **not** Sharko's `values.yaml`:

```yaml
# values for the argo-cd Helm chart
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

The mount lands the corporate CA at a path OpenSSL already trusts. `SSL_CERT_DIR` is set explicitly so the Go HTTP client inside `argocd-repo-server` picks it up at startup.

### Step 4 — Apply and restart

```bash
helm upgrade argo-cd argo/argo-cd -n argocd -f your-argocd-values.yaml
kubectl rollout restart deployment/argocd-repo-server -n argocd
```

`rollout restart` is required even though `helm upgrade` triggers one — Helm restarts on template diffs, and a ConfigMap value change alone may not produce one.

### Step 5 — Verify

Wait ~30 seconds for the new repo-server pods to come up, then check the logs:

```bash
kubectl logs -n argocd deploy/argocd-repo-server | grep -i x509
```

Empty output = the workaround is applied. If the line still appears, double-check that:

- The ConfigMap is in the same namespace as `argocd-repo-server`
- The key inside the ConfigMap is `ca.crt` (matching `subPath` in the volume mount)
- The CA bundle in Step 1 actually contains the corporate root (`openssl x509 -in corporate-ca.crt -noout -issuer -subject` should show your corporate CA, not `github.com`'s real Sectigo / DigiCert root)

You can also confirm the mount inside the pod:

```bash
kubectl exec -n argocd deploy/argocd-repo-server -- \
  ls -l /etc/ssl/certs/corporate-ca.crt
```

## Why not bake the CA into a custom image

Building a custom `argocd-repo-server` image with the CA pre-trusted works, but:

- It couples your CA rotation cadence to your image-build pipeline
- It blocks ArgoCD upgrades behind an image rebuild
- It is opaque — operators reading your manifests do not see the CA injection

The ConfigMap + volume-mount approach above is upgrade-safe, visible in Git, and rotates with `kubectl create configmap --from-file=... -o yaml --dry-run=client | kubectl replace -f -`.

## Why not `configs.tls.certificates` on the argo-cd chart

The `configs.tls.certificates` value in the argo-cd chart populates `argocd-tls-certs-cm`, which ArgoCD uses for **inbound** TLS (presenting certs to API/UI clients) and for Git server certificate pinning. It does **not** add CAs to the OpenSSL trust store that `argocd-repo-server` uses for outbound HTTPS to public hosts. For that, the ConfigMap-backed extra volume above is the supported path.

## See also

- [`charts/sharko/values.yaml`](https://github.com/MoranWeissman/sharko/blob/main/charts/sharko/values.yaml) — informational block pointing here
- [Troubleshooting](troubleshooting.md) — general install/connectivity issues
- BUG-043 — the bug report this workaround closes
