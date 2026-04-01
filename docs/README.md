# ArgoCD Cluster Addons Management

This solution provides a scalable way to manage addons across multiple clusters through ArgoCD. It utilizes ApplicationSets for dynamic addon deployment management, wrapped in an App of Apps pattern for the solution's components. The solution heavily leverages Helm templating for dynamic configuration generation.

> **Important:** This solution does NOT manage ArgoCD itself. ArgoCD installation and configuration are managed by [ArgoFleet](https://github.com/YOUR_ORG/ArgoFleet). This solution only manages addon deployments and cluster registration.

## Documentation

### Architecture & Design
- [Architecture](ARCHITECTURE.md) — system architecture, component relationships, data flow
- [Design](DESIGN.md) — design decisions, patterns, trade-offs
- [Bootstrap](BOOTSTRAP.md) — initial cluster bootstrap process
- [Bootstrap Flow](BOOTSTRAP-FLOW.md) — step-by-step bootstrap sequence
- [Values Guide](VALUES_GUIDE.md) — Helm values configuration layers and precedence

### Infrastructure
- [IAM Setup](IAM_SETUP.md) — IAM roles and policies for ESO and cluster access
- [IAM Roles Reference](IAM-ROLES-REFERENCE.md) — complete IAM role reference
- [EKS Auto Mode Infrastructure Nodes](EKS-AUTO-MODE-INFRASTRUCTURE-NODES.md) — EKS Auto Mode with infrastructure node pools
- [Infrastructure Node Separation](INFRASTRUCTURE-NODE-SEPARATION.md) — node separation strategy for system vs workload
- [Karpenter NodePools Deployment](KARPENTER-NODEPOOLS-DEPLOYMENT.md) — Karpenter node pool configuration and deployment

### Integrations
- [ArgoCD ESO Health Checks](ARGOCD_ESO_HEALTH_CHECKS.md) — custom health checks for External Secrets Operator resources

### Observability
- [Observability Overview](observability/README.md) — monitoring strategy and scope
- [Host Cluster Monitoring](observability/HOST_CLUSTER_MONITORING.md) — Datadog monitors and dashboard for the ArgoCD management cluster

### Migration
- [AVP to ESO Migration](MIGRATION_AVP_TO_ESO.md) — migration from ArgoCD Vault Plugin to External Secrets Operator

### Runbooks
- [Troubleshooting Cluster Connection Errors](runbooks/troubleshooting-cluster-connection-errors.md)

### Diagrams
- [Diagrams](diagrams/README.md) — architecture and flow diagrams (`.drawio` files)
