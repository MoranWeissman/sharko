# Security: a hardened install, step by step

This page is part of the [Security](security.md) reference. It shows the pod
settings Sharko ships with, and then one full example you can copy and change:
Helm values, the AWS role, and a network policy.

## Pod security {#pod-security}

The chart runs Sharko with these settings by default:

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
  runAsUser: 1001
  runAsGroup: 1001
  capabilities:
    drop:
      - ALL
```

This meets the Kubernetes **Restricted** Pod Security Standard, the strictest
of the built-in levels. Sharko does not run as root, cannot gain more rights,
cannot write to its own image's files, and has no extra Linux capabilities.
Keep these settings as they are.

## A worked example {#a-worked-example-a-hardened-installation}

Everything below uses placeholder values: `123456789012` for an AWS account,
`sharko.example.com` for a host name. Put in your own before you apply
anything.

The setup:

- Sharko is installed in the `sharko` namespace.
- ArgoCD is installed in the `argocd` namespace.
- Cluster credentials and addon secrets both live in AWS Secrets Manager, in
  the same account, under two different name prefixes.

### Step 1: Helm values {#step-1-helm-values}

```yaml
rbac:
  create: true
  argocdNamespace: argocd
  # Not used with the aws-sm backend below. Left in to show the shape.
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

Run `helm template` with these values to see what gets made. There is no
cluster-wide rule for Secrets, and there is one `Role` in `argocd` for the
ArgoCD cluster Secrets. The `sharko-secrets-provider` Role is still made for
the release namespace, as [RBAC](security.md#rbac) explains, even though the
`aws-sm` backend does not need it.

### Step 2: the AWS role {#step-2-the-aws-role}

The role named in the `eks.amazonaws.com/role-arn` annotation needs a policy
that lets it read the two prefixes, and nothing more. The policy, and what it
does and does not keep apart, are on
[EKS Hub-and-Spoke Identity](eks-hub-and-spoke-identity.md#c-sharkos-own-role-for-pushing-addon-secrets-and-running-tests).

### Step 3: a network policy {#step-3-a-network-policy}

This lets in only your ingress controller, and lets out only DNS, ArgoCD and
HTTPS:

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
    # ArgoCD's API.
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
    # HTTPS out: your Git host, AWS Secrets Manager and STS, and the
    # Kubernetes API of each managed cluster Sharko pushes addon secrets to.
    # A NetworkPolicy cannot match on host names, so narrow this to IP
    # ranges if your platform lets you.
    - ports:
        - port: 443
          protocol: TCP
```

This is a place to start, not a finished policy. Change it for your setup:

- Use the namespace and labels of your own ingress controller.
- Sharko also talks to the Kubernetes API of the cluster it runs on. On many
  clusters that is port 443. On some it is port 6443, so add that port if
  Sharko cannot reach the API after you apply the policy.
- If Prometheus scrapes Sharko's `/metrics`, let Prometheus in as well.

After you apply it, test each place Sharko must reach, from inside the
Sharko pod, before you rely on the policy. The other network settings are on
[Addresses, network and webhook](security-network-and-addresses.md).
