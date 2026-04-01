# Architecture Diagrams

This folder contains draw.io diagrams for the secret distribution alternatives analysis.

## Diagrams

### 1. ESO Remote Cluster Architecture
**File**: `eso-remote-cluster-architecture.drawio`

**Shows**: How ESO (External Secrets Operator) works when installed on each remote cluster.

**Components**:
- ESO Operator (Helm chart)
- ClusterSecretStore with IRSA authentication
- ExternalSecret fetching from AWS Secrets Manager
- Kubernetes Secret creation in datadog namespace

**Use Case**: Alternative 1 - Installing ESO on every cluster for generic secret management

---

### 2. Datadog Secret Backend Architecture (RECOMMENDED)
**File**: `datadog-secret-backend-architecture.drawio`

**Shows**: How Datadog Agent uses Secret Backend to fetch API keys directly from AWS Secrets Manager.

**Key Features**:
- ✅ NO ESO required
- ✅ NO Kubernetes secrets
- ✅ Memory-only secrets (highest security)
- ✅ Auto-rotation on pod restart
- ✅ Simplest architecture (~2 hours setup vs 3 days for PushSecret)

**Use Case**: Alternative 2 - Recommended approach for Datadog specifically

---

## How to Use These Diagrams

### In Confluence
1. **Upload directly** (if draw.io plugin installed):
   - Attach `.drawio` file to Confluence page
   - It will render automatically

2. **Export as images**:
   - Open in https://app.diagrams.net
   - File → Export as → PNG (or SVG)
   - Download and insert into Confluence

3. **Use draw.io macro**:
   - In Confluence edit mode: `/draw`
   - Select "Embed draw.io diagram"
   - Upload the `.drawio` file

### In Presentations
1. Open in draw.io
2. Export as PNG with transparent background
3. Use in PowerPoint/Google Slides

### For Documentation
- These diagrams are referenced in `secret-distribution-alternatives.md`
- They provide visual representation of the architecture options
- Use them for technical discussions and decision-making

## Related Documentation

- **Main Analysis**: `../secret-distribution-alternatives.md`
- **Datadog Official Docs**: https://docs.datadoghq.com/agent/configuration/secrets-management/
- **ESO Documentation**: https://external-secrets.io/latest/

## Updating Diagrams

To edit these diagrams:
1. Open the `.drawio` file in https://app.diagrams.net
2. Make your changes
3. Save back to this folder
4. Commit to git

Or open directly in VS Code if you have the draw.io extension installed.
