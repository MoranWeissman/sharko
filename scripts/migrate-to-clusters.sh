#!/bin/bash

# Migration script: Convert old addons-config/overrides/ structure to new clusters/ structure
# This script creates simplified cluster files with only datadog configuration

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OLD_DIR="$SCRIPT_DIR/configuration/addons-config/overrides"
NEW_DIR="$SCRIPT_DIR/configuration/clusters"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "======================================"
echo "Cluster Configuration Migration Script"
echo "======================================"
echo ""
echo "Old structure: configuration/addons-config/overrides/<cluster>/<addon>.yaml"
echo "New structure: configuration/clusters/<cluster>.yaml"
echo ""
echo "NOTE: This will OVERWRITE existing files to simplify them (datadog only)"
echo ""

# Check if old directory exists
if [ ! -d "$OLD_DIR" ]; then
    echo "Error: $OLD_DIR does not exist"
    exit 1
fi

# Count clusters to migrate
CLUSTER_COUNT=$(find "$OLD_DIR" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')
echo "Found $CLUSTER_COUNT clusters to migrate"
echo ""

# Track migration stats
MIGRATED=0
SKIPPED=0

# Process each cluster
for CLUSTER_PATH in "$OLD_DIR"/*; do
    if [ ! -d "$CLUSTER_PATH" ]; then
        continue
    fi

    CLUSTER_NAME=$(basename "$CLUSTER_PATH")
    NEW_FILE="$NEW_DIR/${CLUSTER_NAME}.yaml"

    # Skip feedlot-dev (it's our reference example)
    if [ "$CLUSTER_NAME" = "feedlot-dev" ]; then
        echo -e "${YELLOW}⚠ Skipping${NC} $CLUSTER_NAME (reference example file)"
        ((SKIPPED++))
        continue
    fi

    echo -e "${GREEN}✓ Migrating${NC} $CLUSTER_NAME"

    # Start with template header
    cat > "$NEW_FILE" <<'EOF'
# ================================================================ #
# Global Values (used by all addons)
# Define YAML anchors with & for reuse across addon configurations
# ================================================================ #
clusterGlobalValues:
  env: &env dev
  clusterName: &clusterName CLUSTER_NAME_PLACEHOLDER
  region: &region us-east-1
  projectName: CLUSTER_NAME_PLACEHOLDER

# ================================================================ #
# Addon-specific overrides
# These override the default values from global.yaml
# Use YAML anchors (*clusterName, *region, etc.) to reference clusterGlobalValues
# ================================================================ #

# ================================================================ #
# ------------------- Commented Examples ------------------- #
# ================================================================ #
# Uncomment and customize as needed for your addons

# otel addon - requires project name (cluster name)
# otel:
#   projectname: *clusterName

# istiod addon - enable CNI if using Istio
# istiod:
#   istio:
#     cni:
#       enabled: true

EOF

    # Replace cluster name placeholder
    sed -i '' "s/CLUSTER_NAME_PLACEHOLDER/$CLUSTER_NAME/g" "$NEW_FILE"

    # Check if datadog configuration exists
    DATADOG_FILE="$CLUSTER_PATH/datadog.yaml"
    if [ -f "$DATADOG_FILE" ]; then
        echo "  - Adding datadog configuration"

        # Add datadog section header
        cat >> "$NEW_FILE" <<'EOF'
# ================================================================ #
# ------------------- Datadog Configuration ------------------- #
# ================================================================ #
datadog:
EOF

        # Indent and append the datadog configuration (2 spaces)
        sed 's/^/  /' "$DATADOG_FILE" >> "$NEW_FILE"

        # Add blank line
        echo "" >> "$NEW_FILE"
    else
        echo "  - No datadog configuration found (file will have clusterGlobalValues only)"
    fi

    echo "  ✓ Created $NEW_FILE"
    echo ""
    ((MIGRATED++))
done

echo ""
echo "======================================"
echo "Migration Summary"
echo "======================================"
echo "✓ Migrated: $MIGRATED clusters"
echo "✓ Skipped: $SKIPPED clusters"
echo "✓ New files created in: $NEW_DIR/"
echo ""
echo "Next steps:"
echo "1. Review generated files in configuration/clusters/"
echo "2. Update clusterGlobalValues (env, region, projectName) for each cluster"
echo "3. Add YAML anchor references where appropriate (e.g., *clusterName, *region)"
echo "4. Add other addon configurations as needed (see feedlot-dev.yaml for examples)"
echo "5. Test with: helm template bootstrap/ -f configuration/addons-catalog.yaml -f configuration/cluster-addons.yaml -f configuration/global-values.yaml -f configuration/clusters/<cluster>.yaml"
echo "6. After confirming everything works, delete: configuration/addons-config/"
echo ""
