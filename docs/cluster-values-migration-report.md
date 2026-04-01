# Cluster Values Migration Report
Generated: 2026-03-09

## Summary
- **Total clusters:** 48
- **Fully migrated:** 7 (feedlot-dev, devops-automation-dev-eks, devops-argocd-addons-dev-eks, sh-in-cloud-dev2-eks [datadog-only], swine-beta [datadog-only], sh-rp-mr-eks-dev [datadog-only], animo-staging-eks [datadog-only])
- **Partially migrated:** 39 (have datadog migrated, missing other addons like anodot, redisinsight-v2, keda, external-secrets, etc.)
- **Not migrated (empty new file or missing):** 2 (devops-automation, sh-in-cloud-dev2-eks has no new file)

## Legend
- :white_check_mark: Migrated -- values present in new cluster file
- :x: MISSING -- cluster-specific values exist in old but not in new
- :white_medium_square: No override needed -- old file was empty or values covered by global defaults
- :warning: Discrepancy -- values differ between old and new
- :new: New-only -- present in new file but not in old overrides

## Notes on Global Defaults
The following are handled by **global defaults** and do NOT need cluster overrides:
- **datadog**: `logLevel`, `logs.enabled`, `logs.containerCollectAll`, `logs.autoMultiLineDetection`, `collectEvents`, `orchestratorExplorer.enabled`, `fullnameOverride: "datadog"` are all in global
- **anodot**: `config.clusterName`, `config.clusterRegion` have defaults but MUST be overridden per-cluster; `serviceAccount.annotations` (IRSA) is cluster-specific
- **external-dns**: `provider.name: aws`, `policy: upsert-only`, `sources`, `registry: txt` are global defaults; IRSA and extra args are cluster-specific
- **keda**: Operator resources, metrics server, webhooks config are global; IRSA is cluster-specific
- **redisinsight-v2**: No meaningful global defaults -- all ingress values are cluster-specific
- **external-secrets**: IRSA is cluster-specific

---

## Per-Cluster Details

### allflex-connect-beta-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom, fullnameOverride | Present | :white_check_mark: | Matches old values |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "allflex-connect-redisinsight-v2-beta.mahi-techlabs.com"
    sgGroupName: "allflex-connect-alb-offices"
    svcPort: 38001
```

---

### allflex-connect-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom, fullnameOverride | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::206869499193:role/anodot-allflex-connect-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::206869499193:role/anodot-allflex-connect-dev-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "allflex-connect-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "allflex-connect-alb-offices"
    svcPort: 38001
```

---

### animo-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-animo-dev-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-animo-dev-eks-eu-central-1
```

---

### animo-prod-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::170767152514:role/anodot-animo-prod-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::170767152514:role/anodot-animo-prod-eks-eu-central-1
```

---

### animo-qa-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-animo-qa-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-animo-qa-eks-eu-central-1
```

---

### animo-staging-eks
**Status:** Fully migrated (datadog only)

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |

No additional values needed.

---

### aquafalcon-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | DD_TAGS, tags, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::028427307275:role/anodot-aquafalcon-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::028427307275:role/anodot-aquafalcon-dev-eu-west-1
```

---

### aquafalcon-qa
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | DD_TAGS, tags, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::028427307275:role/anodot-aquafalcon-qa-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::028427307275:role/anodot-aquafalcon-qa-eu-west-1
```

---

### ark-beta-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::056822611329:role/anodot-ark-beta-eks-eu-west-1dukbh001 | Missing | :x: | Add anodot with IRSA |
| argo-events | SecretProviderClass, EventBus, RBAC resources | Missing | :x: | Complex -- extraObjects with SecretProviderClass, roles, rolebindings, EventBus, ClusterRoles |
| argo-rollouts | (empty file) | N/A | :white_medium_square: | Old file was empty |
| argo-workflows | server ingress, workflow SA IRSA, controller config, S3 artifact repo | Missing | :x: | Substantial config needed |
| cert-manager | (empty file) | N/A | :white_medium_square: | Old file was empty |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::056822611329:role/anodot-ark-beta-eks-eu-west-1dukbh001

# ================================================================ #
# ------------------- Argo Events Configuration ------------------ #
# ================================================================ #
argo-events:
  extraObjects:
    - apiVersion: secrets-store.csi.x-k8s.io/v1
      kind: SecretProviderClass
      metadata:
        name: workflow-secrets-aws-beta
        namespace: argo-workflows
      spec:
        provider: aws
        parameters:
          region: eu-west-1
          objects: |
            - objectName: "ark-beta-argo-workflow"
              objectType: "secretsmanager"
              jmesPath:
                - path: "AKUITY_ARGOCD_DEV_BUILDER_TOKEN"
                  objectAlias: "AKUITY_ARGOCD_DEV_BUILDER_TOKEN"
                - path: "AHM_OKTA_CLIENT_ID"
                  objectAlias: "AHM_OKTA_CLIENT_ID"
                - path: "AHM_OKTA_CLIENT_SECRET"
                  objectAlias: "AHM_OKTA_CLIENT_SECRET"
                - path: "ADO_GIT_PAT"
                  objectAlias: "ADO_GIT_PAT"
                - path: "ADO_API_PAT"
                  objectAlias: "ADO_API_PAT"
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: Role
      metadata:
        name: workflow-log-reader
        namespace: argo-workflows
      rules:
      - apiGroups: [""]
        resources: ["pods", "pods/log", "events"]
        verbs: ["get", "list", "watch"]
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: RoleBinding
      metadata:
        name: workflow-log-reader-binding
        namespace: argo-workflows
      subjects:
      - kind: ServiceAccount
        name: workflow-runner-sa
        namespace: argo-workflows
      roleRef:
        kind: Role
        name: workflow-log-reader
        apiGroup: rbac.authorization.k8s.io
    - apiVersion: argoproj.io/v1alpha1
      kind: EventBus
      metadata:
        name: default
        namespace: argo-workflows
      spec:
        nats:
          native:
            replicas: 3
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: ClusterRole
      metadata:
        name: clusterworkflowtemplate-reader
      rules:
      - apiGroups: ["argoproj.io"]
        resources: ["clusterworkflowtemplates"]
        verbs: ["get", "list", "watch"]
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: ClusterRoleBinding
      metadata:
        name: sensor-read-cwts
      subjects:
      - kind: ServiceAccount
        name: workflow-runner-sa
        namespace: argo-workflows
      roleRef:
        kind: ClusterRole
        name: clusterworkflowtemplate-reader
        apiGroup: rbac.authorization.k8s.io
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: ClusterRole
      metadata:
        name: argo-workflow-create
      rules:
        - apiGroups: ["argoproj.io"]
          resources: ["workflows"]
          verbs: ["create", "get", "list", "watch", "patch", "update"]
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: ClusterRoleBinding
      metadata:
        name: argo-workflow-create-binding
        namespace: argo-workflows
      subjects:
        - kind: ServiceAccount
          name: workflow-runner-sa
          namespace: argo-workflows
      roleRef:
        kind: ClusterRole
        name: argo-workflow-create
        apiGroup: rbac.authorization.k8s.io

# ================================================================ #
# ------------------- Argo Workflows Configuration --------------- #
# ================================================================ #
argo-workflows:
  server:
    authModes:
      - server
      - client
    ingress:
      enabled: true
      pathType: Prefix
      annotations:
        alb.ingress.kubernetes.io/actions.ssl-redirect: '{"Type": "redirect", "RedirectConfig": {"Protocol": "HTTPS", "Port": "443", "StatusCode": "HTTP_301"}}'
        alb.ingress.kubernetes.io/healthcheck-protocol: HTTPS
        alb.ingress.kubernetes.io/listen-ports: '[{"HTTP": 80}, {"HTTPS": 443}]'
        alb.ingress.kubernetes.io/scheme: internet-facing
        alb.ingress.kubernetes.io/success-codes: 200-399
        alb.ingress.kubernetes.io/target-type: ip
        alb.ingress.kubernetes.io/group.name: private
        alb.ingress.kubernetes.io/security-groups: sg_ark_alb_beta_private
      ingressClassName: alb
      hosts:
        - ark-beta-workflows.mahi-techlabs.com
      paths:
        - /
  workflow:
    serviceAccount:
      create: true
      name: "workflow-runner-sa"
      annotations:
        eks.amazonaws.com/role-arn: arn:aws:iam::056822611329:role/ark-beta-argo-workflow
  controller:
    configMap:
      create: true
    nodeEvents:
      enabled: true
    workflowEvents:
      enabled: true
    workflowDefaults:
      spec:
        serviceAccountName: workflow-runner-sa
        podGC:
          strategy: OnWorkflowCompletion
        ttlStrategy:
          secondsAfterSuccess: 604800
          secondsAfterFailure: 1209600
  useStaticCredentials: false
  artifactRepository:
    archiveLogs: true
    s3:
      bucket: ark-beta-argo-workflows-repository
      endpoint: s3.amazonaws.com
      region: eu-west-1
      keyFormat: "artifacts/{{workflow.namespace}}/{{workflow.name}}/{{pod.name}}"

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "ark-redisinsight-v2-beta.mahi-techlabs.com"
    sgGroupName: "sg_ark_alb_beta_private"
    svcPort: 38001
```

---

### ark-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom, apm, profiling | Present | :white_check_mark: | New file has expanded containerIncludeLogs with more namespaces (added akuity, service-sensehub-lt) |
| anodot | IRSA: arn:aws:iam::056822611329:role/anodot-ark-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| argo-events | (empty file) | N/A | :white_medium_square: | Old file was empty |
| argo-rollouts | (empty file) | N/A | :white_medium_square: | Old file was empty |
| argo-workflows | server ingress, workflow SA IRSA, controller config, S3 artifact repo | Missing | :x: | Substantial config needed |
| cert-manager | (empty file) | N/A | :white_medium_square: | Old file was empty |
| external-dns | provider, env region, policy, sources, IRSA, extraArgs | Missing | :x: | Full external-dns config needed |
| external-secrets | IRSA (empty string) | Missing | :white_medium_square: | Old value was empty IRSA -- likely no override needed |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs (old) vs containerIncludeLogs (new) | Present | :warning: | New adds `akuity`, `service-sensehub-lt` (replaces `service-sensehub-staging`), `provisioning-hub-api` -- intentional expansion |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::056822611329:role/anodot-ark-dev-eks-eu-west-1

# ================================================================ #
# ------------------- Argo Workflows Configuration --------------- #
# ================================================================ #
argo-workflows:
  server:
    authModes:
      - server
      - client
    ingress:
      enabled: true
      pathType: Prefix
      annotations:
        alb.ingress.kubernetes.io/actions.ssl-redirect: '{"Type": "redirect", "RedirectConfig": {"Protocol": "HTTPS", "Port": "443", "StatusCode": "HTTP_301"}}'
        alb.ingress.kubernetes.io/healthcheck-protocol: HTTPS
        alb.ingress.kubernetes.io/listen-ports: '[{"HTTP": 80}, {"HTTPS": 443}]'
        alb.ingress.kubernetes.io/scheme: internet-facing
        alb.ingress.kubernetes.io/success-codes: 200-399
        alb.ingress.kubernetes.io/target-type: ip
        alb.ingress.kubernetes.io/group.name: private
        alb.ingress.kubernetes.io/security-groups: sg_ark_alb_dev_private
      ingressClassName: alb
      hosts:
        - ark-dev-workflows.mahi-techlabs.com
      paths:
        - /
  workflow:
    serviceAccount:
      create: true
      annotations:
        eks.amazonaws.com/role-arn: arn:aws:iam::056822611329:role/ark-dev-argo-workflows
  controller:
    configMap:
      create: true
    nodeEvents:
      enabled: true
    workflowEvents:
      enabled: true
    workflowDefaults:
      spec:
        serviceAccountName: workflow-runner-sa
        ttlStrategy:
          secondsAfterSuccess: 604800
          secondsAfterFailure: 1209600
  useStaticCredentials: false
  artifactRepository:
    archiveLogs: true
    s3:
      bucket: ark-dev-argo-workflows-repository
      endpoint: s3.amazonaws.com
      region: eu-west-1
      keyFormat: "artifacts/{{workflow.namespace}}/{{workflow.name}}/{{pod.name}}"

# ================================================================ #
# ------------------- External DNS Configuration ----------------- #
# ================================================================ #
external-dns:
  provider:
    name: aws
  env:
    - name: AWS_DEFAULT_REGION
      value: eu-west-1
  policy: upsert-only
  txtOwnerId: external-dns
  sources:
    - ingress
  interval: 5m
  registry: noop
  extraArgs:
    - --aws-zone-type=private
    - --aws-prefer-cname
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::056822611329:role/ark-dev-external-dns

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::056822611329:role/ark-dev-keda"
  podIdentity:
    aws:
      irsa:
        enabled: true
        roleArn: "arn:aws:iam::056822611329:role/ark-dev-keda"
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "ark-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "sg_ark_alb_dev_private"
    svcPort: 38001
```

---

### devops-argocd-addons-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::627176949220:role/anodot-devops-argocd-addons-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |

> NOTE: There is also a `devops-argocd-addons-dev-eks.yaml` new file (with full IRSA-based Datadog config). The old `devops-argocd-addons-dev` directory maps to `devops-argocd-addons-dev.yaml` (not the -eks variant).

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/anodot-devops-argocd-addons-dev-eu-west-1
```

---

### devops-automation
**Status:** Needs migration

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| anodot | IRSA: arn:aws:iam::627176949220:role/anodot-devops-automation-eu-central-1 | Missing | :x: | New file is empty (no addon sections at all) |
| api-transformer-operator-is | secretsManager config, IRSA, image tag | Missing | :x: | **SEE SPECIAL NOTE BELOW** |
| external-secrets | (empty file) | N/A | :white_medium_square: | Old file was empty |
| kyverno | (comments only, no actual values) | N/A | :white_medium_square: | Old file was only comments about default behavior |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/anodot-devops-automation-eu-central-1
```

---

### devops-automation-dev-eks
**Status:** Fully migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| istio-base | (empty file) | N/A | :white_medium_square: | Old file was empty |
| istio-cni | (empty file) | N/A | :white_medium_square: | Old file was empty |
| istio-ingress | (empty file) | N/A | :white_medium_square: | Old file was empty |
| istiod | (empty file) | N/A | :white_medium_square: | Old file was empty |

> NOTE: The new file has anodot, datadog (with IRSA), and external-secrets sections that did NOT exist in old overrides. These are :new: additions in the new system.

No additional values needed -- all old overrides were empty.

---

### devops-crossplane-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::627176949220:role/anodot-devops-crossplane-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/anodot-devops-crossplane-dev-eu-west-1
```

---

### df-srvc-beta-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-df-srvc-beta-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-df-srvc-beta-eks-eu-west-1
```

---

### df-srvc-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-df-srvc-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-df-srvc-dev-eks-eu-west-1
```

---

### feedlot-dev
**Status:** Fully migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs | Present (new structure with IRSA) | :white_check_mark: | New file uses IRSA-based Datadog (improved) |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-feedlot-dev-eu-west-1 | Present (using anchors) | :white_check_mark: | Uses `*awsAccountId`, `*clusterName`, `*region` anchors |
| external-secrets | IRSA: arn:aws:iam::298685015100:role/feedlot-secretsmanager-sa-dev | Present | :white_check_mark: | Uses `*awsAccountId`, `*env` anchors |
| redisinsight-v2 | host, sgGroupName, svcPort | Present | :white_check_mark: | Matches exactly |

No additional values needed. This is the reference/model cluster file.

---

### feedlot-lt
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-feedlot-lt-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::298685015100:role/anodot-feedlot-lt-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "feedlot-redisinsight-v2-lt.mahi-techlabs.com"
    sgGroupName: "feedlot-alb-offices"
    svcPort: 38001
```

---

### feedlot-qa
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-feedlot-qa-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::298685015100:role/anodot-feedlot-qa-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "feedlot-redisinsight-v2-qa.mahi-techlabs.com"
    sgGroupName: "feedlot-alb-offices"
    svcPort: 38001
```

---

### feedlot-staging
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-feedlot-staging-us-east-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::298685015100:role/anodot-feedlot-staging-us-east-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "feedlot-redisinsight-v2-staging.mahi-techlabs.com"
    sgGroupName: "feedlot-alb-offices"
    svcPort: 38001
```

---

### nms-core-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::637423520333:role/anodot-nms-core-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| kafka-ui | Full config: IRSA, MSK bootstrap, ingress, yamlApplicationConfig | Missing | :x: | Substantial -- includes MSK connection details, ingress, IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |
| secrets-store-csi-driver | (empty / comment "# Empty") | N/A | :white_medium_square: | Old file was empty |
| sscdp-aws | (empty / comment "# Empty") | N/A | :white_medium_square: | Old file was empty |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::637423520333:role/anodot-nms-core-dev-eks-eu-west-1

# ================================================================ #
# ------------------- Kafka UI Configuration --------------------- #
# ================================================================ #
kafka-ui:
  replicaCount: 1
  serviceAccount:
    create: true
    name: "kafka-ui"
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::637423520333:role/nms-core-dev-kafka-ui"
      eks.amazonaws.com/sts-regional-endpoints: "true"
  yamlApplicationConfig:
    delete:
      topic:
        enabled: true
    dynamic:
      config:
        enabled: true
    kafka:
      clusters:
        - name: "NMS Core DEV"
          bootstrapServers: "b-1.nmscoredevmsk.fcisvc.c3.kafka.eu-west-1.amazonaws.com:9098,b-2.nmscoredevmsk.fcisvc.c3.kafka.eu-west-1.amazonaws.com:9098,b-3.nmscoredevmsk.fcisvc.c3.kafka.eu-west-1.amazonaws.com:9098"
          properties:
            security.protocol: "SASL_SSL"
            sasl.mechanism: "AWS_MSK_IAM"
            sasl.client.callback.handler.class: "software.amazon.msk.auth.iam.IAMClientCallbackHandler"
            sasl.jaas.config: "software.amazon.msk.auth.iam.IAMLoginModule required;"
  env:
    - name: "DYNAMIC_CONFIG_ENABLED"
      value: "true"
  ingress:
    enabled: true
    annotations:
      alb.ingress.kubernetes.io/group.name: "private"
      alb.ingress.kubernetes.io/listen-ports: '[{"HTTPS": 443}]'
      alb.ingress.kubernetes.io/scheme: "internet-facing"
      alb.ingress.kubernetes.io/security-groups: "nms-core-dev-alb-offices"
      alb.ingress.kubernetes.io/target-type: "ip"
      alb.ingress.kubernetes.io/manage-backend-security-group-rules: "true"
    ingressClassName: "alb"
    path: "/"
    host: "nms-core-kafka-ui-dev.mahi-techlabs.com"
    pathType: "Prefix"
  resources:
    limits:
      cpu: 200m
      memory: 512Mi
    requests:
      cpu: 200m
      memory: 256Mi

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "nms-core-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "nms-core-dev-alb-offices"
    svcPort: 38001
```

---

### nms-feedlot-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-nms-feedlot-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::298685015100:role/anodot-nms-feedlot-dev-eks-eu-west-1
```

---

### nms-feedlot-staging-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::298685015100:role/anodot-nms-feedlot-staging-eks-us-east-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::298685015100:role/anodot-nms-feedlot-staging-eks-us-east-1
```

---

### nms-lely-dev-eks-green
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-nms-lely-dev-eks-green-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-nms-lely-dev-eks-green-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "nms-lely-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "nms-lely-dev-alb-offices"
    svcPort: 38001
```

---

### nms-ps-dev-eks-green
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::242212801364:role/anodot-nms-ps-dev-eks-green-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::242212801364:role/anodot-nms-ps-dev-eks-green-eu-west-1
```

---

### nms-sh-beta-eks-green
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-nms-sh-beta-eks-green-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-nms-sh-beta-eks-green-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "nms-sh-redisinsight-v2-beta.mahi-techlabs.com"
    sgGroupName: "sg-02a728dd9b7e23726"
    svcPort: 38001
```

---

### nms-sh-dev-eks-green
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-nms-sh-dev-eks-green-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-nms-sh-dev-eks-green-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "nms-sh-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "sg-0211ff3fc7f323b1e"
    svcPort: 38001
```

---

### poc-automods-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| anodot | IRSA: arn:aws:iam::627176949220:role/anodot-poc-automods-dev-eu-central-1 | Missing | :x: | New file has no addon sections |
| argo-events | (empty file) | N/A | :white_medium_square: | Old file was empty |
| argo-workflows | server ingress, IRSA | Missing | :x: | Ingress + IRSA needed |
| cert-manager | (empty file) | N/A | :white_medium_square: | Old file was empty |
| karpenter | nodePools config, scheduler RBAC | Missing | :x: | Custom nodepool + scheduler config |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/anodot-poc-automods-dev-eu-central-1

# ================================================================ #
# ------------------- Argo Workflows Configuration --------------- #
# ================================================================ #
argo-workflows:
  server:
    authModes:
      - server
      - client
    serviceAccount:
      annotations:
        eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/argo-workflow-role
    ingress:
      enabled: true
      pathType: Prefix
      annotations:
        alb.ingress.kubernetes.io/actions.ssl-redirect: '{"Type": "redirect", "RedirectConfig": {"Protocol": "HTTPS", "Port": "443", "StatusCode": "HTTP_301"}}'
        alb.ingress.kubernetes.io/healthcheck-protocol: HTTPS
        alb.ingress.kubernetes.io/listen-ports: '[{"HTTP": 80}, {"HTTPS": 443}]'
        alb.ingress.kubernetes.io/scheme: internet-facing
        alb.ingress.kubernetes.io/success-codes: 200-399
        alb.ingress.kubernetes.io/target-type: ip
      ingressClassName: alb
      hosts:
        - argo-workflows-poc.mahi-techlabs.com
      paths:
        - /

# ================================================================ #
# ------------------- Karpenter Configuration -------------------- #
# ================================================================ #
karpenter:
  nodePools:
  - name: automation-maksim-temp
    intent: automation-maksim-temp
    schedule:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: "eks.amazonaws.com/instance-cpu"
          operator: In
          values: ["1", "2"]
        - key: "eks.amazonaws.com/instance-memory"
          operator: Gt
          values: ["255"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
        - key: eks.amazonaws.com/instance-category
          operator: In
          values: ["c", "m", "r", "t"]
      taints:
      - key: automationMaksimTempOnly
        value: "true"
        effect: NoSchedule
      nodeClassRef:
        group: eks.amazonaws.com
        kind: NodeClass
        name: default
      expireAfter: 24h
      terminationGracePeriod: 3m
    limits:
      cpu: 4
      memory: 4Gi
    disruption:
      consolidationPolicy: WhenEmptyOrUnderutilized
      consolidateAfter: 0s
  createScheduler: true
  rbacRules:
    - apiGroups: ["karpenter.sh"]
      resources: ["nodepools", "nodepools/status"]
      verbs: ["update", "patch", "get"]
    - apiGroups: [""]
      resources: ["nodes"]
      verbs: ["delete", "list"]
```

---

### poultrysense-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::153194394722:role/anodot-poultrysense-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::153194394722:role/anodot-poultrysense-dev-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::153194394722:role/poultrysense-sa-keda-dev"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "poultrysense-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "poultrysense-dev-alb-offices"
    svcPort: 38001
```

---

### poultrysense-sit
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::153194394722:role/anodot-poultrysense-sit-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::153194394722:role/anodot-poultrysense-sit-eu-central-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::153194394722:role/poultrysense-sa-keda-sit"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "poultrysense-redisinsight-v2-sit.mahi-techlabs.com"
    sgGroupName: "poultrysense-sit-alb-offices"
    svcPort: 38001
```

---

### poultrysense-staging
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | envFrom, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::242212801364:role/anodot-poultrysense-staging-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::242212801364:role/anodot-poultrysense-staging-eks-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::242212801364:role/poultrysense-sa-keda-staging"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "poultrysense-redisinsight-v2-staging.mahi-techlabs.com"
    sgGroupName: "sg_ps_alb_staging_private"
    svcPort: 38001
```

---

### sh-in-cloud-beta-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-beta-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon -- may not need migration |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-beta-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-beta.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-beta"
    svcPort: 38001
```

---

### sh-in-cloud-demo-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | DD_TAGS, tags, containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-demo-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-demo-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-demo.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-demo"
    svcPort: 38001
```

---

### sh-in-cloud-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-dev-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort, annotations (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName, annotations (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-dev-eks-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-dev"
    svcPort: 38001
```

---

### sh-in-cloud-dev2-eks
**Status:** NOT MIGRATED -- no new cluster file exists

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-dev-eks-eu-central-1 | No file | :x: | New cluster file missing entirely |
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | No file | :x: | New cluster file missing entirely |

> NOTE: The anodot IRSA in old overrides points to `anodot-sh-in-cloud-dev-eks-eu-central-1` (same as sh-in-cloud-dev-eks), which may be intentional or a copy-paste issue.

**Entire new file needed:** `addons-clusters-values/sh-in-cloud-dev2-eks.yaml`
```yaml
# ================================================================ #
# Global Values (used by all addons)
# Define YAML anchors with & for reuse across addon configurations
# ================================================================ #
clusterGlobalValues:
  env: &env dev
  clusterName: &clusterName sh-in-cloud-dev2-eks
  region: &region eu-central-1
  projectName: sh-in-cloud-dev2-eks

# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-dev-eks-eu-central-1

# ================================================================ #
# ------------------- Datadog Configuration ---------------------- #
# ================================================================ #
datadog:
  clusterAgent:
    envFrom:
    - secretRef:
        name: datadog-tags
  datadog:
    containerIncludeLogs: 'kube_namespace:sh-in-cloud|datadog|shc-report-engine|shc-algo|shc-datasync|cattle-system|cattle-impersonation-system|cattle-system'
    envFrom:
    - secretRef:
        name: datadog-tags
```

---

### sh-in-cloud-lt-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-lt-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-lt-eks-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-lt.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-lt"
    svcPort: 38001
```

---

### sh-in-cloud-pr-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-pr-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-pr-eks-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-pr.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-pr"
    svcPort: 38001
```

---

### sh-in-cloud-qa-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-qa-eks-eu-central-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-in-cloud-qa-eks-eu-central-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "shc-redisinsight-v2-qa.mahi-techlabs.com"
    sgGroupName: "SG-sh-in-cloud-redisinsight-qa"
    svcPort: 38001
```

---

### sh-rp-mr-eks-dev
**Status:** Fully migrated (datadog only)

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |

No additional values needed.

---

### sh-srvc-beta-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-srvc-beta-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-srvc-beta-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "cps-redisinsight-v2-beta.mahi-techlabs.com"
    sgGroupName: "SG-sh-srvc-beta-monitoring-alb"
    svcPort: 38001
```

---

### sh-srvc-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-srvc-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-srvc-dev-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "cps-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "SG-sh-srvc-dev-monitoring-alb"
    svcPort: 38001
```

---

### sh-srvc-dev2-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-srvc-dev2-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-srvc-dev2-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "cps-redisinsight-v2-dev2.mahi-techlabs.com"
    sgGroupName: "SG-sh-srvc-dev2-monitoring-alb"
    svcPort: 38001
```

---

### sh-srvc-lt-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::552292662766:role/anodot-sh-srvc-lt-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort (oldClusterVersionSupport: true) | Missing | :x: | Not in new file |
| redisinsight | host, sgGroupName (oldClusterVersionSupport: true) | Missing | :x: | Legacy addon |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::552292662766:role/anodot-sh-srvc-lt-eks-eu-west-1

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: true
    host: "cps-redisinsight-v2-lt.mahi-techlabs.com"
    sgGroupName: "SG-sh-srvc-lt-monitoring-alb"
    svcPort: 38001
```

---

### swine-beta
**Status:** Fully migrated (datadog only)

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs | Present | :white_check_mark: | Matches |

No additional values needed.

---

### swine-codeflex-dev-eks
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | containerIncludeLogs, envFrom, clusterAgent.admissionController, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::223595378141:role/anodot-swine-codeflex-dev-eks-eu-west-1 | Missing | :x: | Add anodot with IRSA |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::223595378141:role/anodot-swine-codeflex-dev-eks-eu-west-1
```

---

### swine-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent.admissionController, envFrom, containerIncludeLogs, envFrom | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::223595378141:role/anodot-swine-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA: arn:aws:iam::298685015100:role/swine-sa-keda-dev | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName (empty), svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::223595378141:role/anodot-swine-dev-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::298685015100:role/swine-sa-keda-dev"

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "swine-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: ""
    svcPort: 38001
```

---

### vence-dev
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, logLevel, logs, containerExcludeLogs, containerIncludeLogs, otlp | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::012886244013:role/anodot-vence-dev-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::012886244013:role/anodot-vence-dev-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::012886244013:role/vence-sa-keda-dev"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "vence-redisinsight-v2-dev.mahi-techlabs.com"
    sgGroupName: "vence-alb-offices"
    svcPort: 38001
```

---

### vence-lt
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | clusterAgent envFrom, envFrom, logLevel, logs, containerExcludeLogs, containerIncludeLogs, otlp | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::012886244013:role/anodot-vence-lt-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::012886244013:role/anodot-vence-lt-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::012886244013:role/vence-sa-keda-lt"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "vence-redisinsight-v2-lt.mahi-techlabs.com"
    sgGroupName: "vence-alb-offices"
    svcPort: 38001
```

---

### vence-qa
**Status:** Partially migrated

| Addon | Old Override | New File | Status | Details |
|-------|-------------|----------|--------|---------|
| datadog | DD_TAGS, tags, logLevel, logs, containerExcludeLogs, containerIncludeLogs, otlp | Present | :white_check_mark: | Matches |
| anodot | IRSA: arn:aws:iam::012886244013:role/anodot-vence-qa-eu-west-1 | Missing | :x: | Add anodot with IRSA |
| keda | IRSA, podIdentity, serviceAccount names, crds annotations | Missing | :x: | Add keda with IRSA |
| redisinsight-v2 | host, sgGroupName, svcPort | Missing | :x: | Not in new file |

**Values to add:**
```yaml
# ================================================================ #
# ------------------- Anodot Configuration ----------------------- #
# ================================================================ #
anodot:
  config:
    clusterName: *clusterName
    clusterRegion: *region
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::012886244013:role/anodot-vence-qa-eu-west-1

# ================================================================ #
# ------------------- KEDA Configuration ------------------------- #
# ================================================================ #
keda:
  serviceAccount:
    operator:
      name: keda-operator-sa
    metricServer:
      name: keda-metrics-server-sa
    webhooks:
      name: keda-webhook-sa
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::012886244013:role/vence-sa-keda-qa"
  podIdentity:
    aws:
      irsa:
        enabled: true
  crds:
    additionalAnnotations:
      argocd.argoproj.io/sync-options: ServerSideApply=true

# ================================================================ #
# ------------------- Redis Insight V2 Configuration ------------- #
# ================================================================ #
redis-insight-v2:
  ingress:
    oldClusterVersionSupport: false
    host: "vence-redisinsight-v2-qa.mahi-techlabs.com"
    sgGroupName: "vence-alb-offices"
    svcPort: 38001
```

---

## Addons Not In New Catalog

- **api-transformer-operator-is** (devops-automation) -- This addon has old override values including:
  - `secretsManager` config (secret_key_prefix, aws_secret_name, secret_names)
  - `controllerManager.serviceAccount.annotations` with IRSA: `arn:aws:iam::627176949220:role/api-transformer-secretsmanager-sa-dev`
  - `controllerManager.manager.image.tag: '0.0.31'`

  This addon does NOT exist in the new addons-catalog and cannot be migrated through the new system. It needs a separate migration plan or must be added to the catalog first.

- **redisinsight** (v1, legacy) -- Found in old overrides for sh-in-cloud-beta-eks, sh-in-cloud-demo-eks, sh-in-cloud-dev-eks, sh-in-cloud-lt-eks, sh-in-cloud-pr-eks, sh-in-cloud-qa-eks, sh-srvc-beta-eks, sh-srvc-dev-eks, sh-srvc-lt-eks. The new system only has `redisinsight-v2` global defaults. The old v1 `redisinsight` addon may be deprecated and likely does not need migration.

---

## Discrepancies Found

### ark-dev-eks datadog containerIncludeLogs
- **Old:** `'kube_namespace:datadog|iot-device-registration|service-sensehub|service-sensehub-staging|...|provisioning-hub-api image:^api-transformer-operator-is$'`
- **New:** `'kube_namespace:datadog|iot-device-registration|service-sensehub|service-sensehub-lt|...|akuity|provisioning-hub-api image:^api-transformer-operator-is$'`
- **Diff:** `service-sensehub-staging` changed to `service-sensehub-lt`; `akuity` namespace added. This appears intentional.

### allflex-connect-beta-eks region
- **Old overrides:** No region specified (the IRSA in the anodot role name suggests eu-west-1 for the dev cluster)
- **New file:** `region: &region us-east-1` -- Verify this is correct for a beta cluster

### Multiple clusters with `region: us-east-1` in new files
Many new cluster files have `region: &region us-east-1` as a default placeholder. However, the old IRSA role names reveal the actual regions:
- animo-dev-eks, animo-prod-eks, animo-qa-eks: `eu-central-1` (from IRSA role names)
- aquafalcon-dev, aquafalcon-qa: `eu-west-1`
- nms-lely-dev-eks-green, nms-sh-beta-eks-green, nms-sh-dev-eks-green: `eu-central-1`
- sh-in-cloud-* clusters: `eu-central-1` or `eu-west-1`
- All feedlot clusters: `eu-west-1` (except feedlot-staging which is `us-east-1`)

**ACTION REQUIRED:** Review and correct the `region` anchor in all new cluster files where it says `us-east-1` but the cluster actually runs in a different region. The anodot IRSA role names encode the real region.

---

## Global Values Recommendations

### Values that repeat identically across all clusters (candidates for global defaults):

1. **datadog `envFrom` secretRef**: Nearly every cluster has:
   ```yaml
   envFrom:
   - secretRef:
       name: datadog-tags
   ```
   Both at `datadog.envFrom` and `clusterAgent.envFrom`. Consider making this a global default.

2. **datadog `clusterAgent.admissionController.enabled: false`**: Multiple clusters disable this. If most clusters don't need it, make `false` the global default.

3. **keda `serviceAccount` names**: The pattern `keda-operator-sa`, `keda-metrics-server-sa`, `keda-webhook-sa` is identical in every keda override. These should be global defaults, with only the IRSA ARN as the cluster-specific override.

4. **keda `crds.additionalAnnotations`**: `argocd.argoproj.io/sync-options: ServerSideApply=true` is identical everywhere. Make it a global default.

5. **keda `podIdentity.aws.irsa.enabled: true`**: Same everywhere. Make it a global default.

6. **redisinsight-v2 `svcPort: 38001`**: Same in every cluster. Could be a global default.

---

## Migration Priority

### High Priority (cluster-specific IRSA roles -- security critical)
1. **Anodot IRSA** -- Missing in 37 clusters. Each has a unique IAM role ARN.
2. **KEDA IRSA** -- Missing in 8 clusters (ark-dev-eks, poultrysense-dev/sit/staging, swine-dev, vence-dev/lt/qa)
3. **External Secrets IRSA** -- Only feedlot-dev has been migrated

### Medium Priority (functionality)
4. **RedisInsight V2 ingress** -- Missing in 28 clusters. Each has unique hostname and security group.
5. **Kafka UI** -- Missing in nms-core-dev-eks (includes MSK connection details)
6. **Argo Workflows** -- Missing in ark-beta-eks, ark-dev-eks, poc-automods-dev (includes IRSA, ingress, S3 config)
7. **External DNS** -- Missing in ark-dev-eks (includes IRSA and zone config)

### Low Priority (empty/deprecated)
8. **Argo Events extraObjects** -- Only ark-beta-eks has actual content
9. **RedisInsight v1** -- Legacy, likely deprecated
10. **api-transformer-operator-is** -- Not in catalog, needs separate plan

---

## Summary Statistics

| Addon | Clusters with old override | Clusters migrated in new | Missing |
|-------|---------------------------|-------------------------|---------|
| datadog | 48 | 47 (all except devops-automation) | 1 |
| anodot | 37 | 2 (feedlot-dev, devops-automation-dev-eks) | 35 |
| redisinsight-v2 | 28 | 1 (feedlot-dev) | 27 |
| keda | 8 | 0 | 8 |
| external-secrets | 3 (feedlot-dev, ark-dev-eks empty, devops-automation empty) | 2 (feedlot-dev, devops-automation-dev-eks) | 0 effective |
| argo-workflows | 3 (ark-beta, ark-dev, poc-automods) | 0 | 3 |
| external-dns | 1 (ark-dev-eks) | 0 | 1 |
| kafka-ui | 1 (nms-core-dev-eks) | 0 | 1 |
| argo-events | 1 with content (ark-beta-eks) | 0 | 1 |
| karpenter | 1 (poc-automods-dev) | 0 | 1 |
| api-transformer-operator-is | 1 (devops-automation) | N/A | N/A (not in catalog) |
| redisinsight (v1) | 9 | N/A | N/A (deprecated) |
| kyverno | 1 (devops-automation, comments only) | N/A | 0 |
