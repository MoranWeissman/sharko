#!/bin/bash
# ============================================================================
# Version Management for aap-server
# Handles semantic versioning (major.minor.patch)
# ============================================================================

set -euo pipefail

VERSION_FILE="version.txt"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Check if VERSION file exists
if [[ ! -f "$VERSION_FILE" ]]; then
    log_error "VERSION file not found!"
    exit 1
fi

# Read current version
CURRENT_VERSION=$(cat "$VERSION_FILE")

# Parse semantic version
IFS='.' read -r major minor patch <<< "$CURRENT_VERSION"

# Function to increment patch version
increment_patch() {
    log_info "Current version: $CURRENT_VERSION" >&2
    NEW_PATCH=$((patch + 1))
    NEW_VERSION="${major}.${minor}.${NEW_PATCH}"
    echo "$NEW_VERSION" > "$VERSION_FILE"
    log_success "Version incremented: $CURRENT_VERSION → $NEW_VERSION" >&2
    echo "$NEW_VERSION"
}

# Function to increment minor version
increment_minor() {
    log_info "Current version: $CURRENT_VERSION" >&2
    NEW_MINOR=$((minor + 1))
    NEW_VERSION="${major}.${NEW_MINOR}.0"
    echo "$NEW_VERSION" > "$VERSION_FILE"
    log_success "Version incremented: $CURRENT_VERSION → $NEW_VERSION" >&2
    echo "$NEW_VERSION"
}

# Function to increment major version
increment_major() {
    log_info "Current version: $CURRENT_VERSION" >&2
    NEW_MAJOR=$((major + 1))
    NEW_VERSION="${NEW_MAJOR}.0.0"
    echo "$NEW_VERSION" > "$VERSION_FILE"
    log_success "Version incremented: $CURRENT_VERSION → $NEW_VERSION" >&2
    echo "$NEW_VERSION"
}

# Function to set specific version
set_version() {
    if [[ $# -ne 1 ]]; then
        log_error "Usage: set_version <version>"
        exit 1
    fi

    NEW_VERSION="$1"

    # Validate semantic version format
    if [[ ! "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        log_error "Invalid version format. Expected: major.minor.patch (e.g., 1.0.0)"
        exit 1
    fi

    echo "$NEW_VERSION" > "$VERSION_FILE"
    log_success "Version set: $CURRENT_VERSION → $NEW_VERSION" >&2
    echo "$NEW_VERSION"
}

# Function to get current version
get_version() {
    echo "$CURRENT_VERSION"
}

# Function to update Kubernetes deployment with new version
update_k8s_version() {
    local version="$1"
    local k8s_file="k8s/aap-server.yaml"

    if [[ ! -f "$k8s_file" ]]; then
        log_warning "Kubernetes deployment file not found: $k8s_file"
        return 1
    fi

    # Create a backup first
    cp "$k8s_file" "$k8s_file.backup"

    # Update image tag and labels using perl for better cross-platform compatibility
    perl -i -pe "s|image: aap-server:.*|image: aap-server:$version|g" "$k8s_file"
    perl -i -pe "s|app.kubernetes.io/version: \".*\"|app.kubernetes.io/version: \"$version\"|g" "$k8s_file"

    # If perl is not available, fall back to sed with different approach
    if [[ $? -ne 0 ]]; then
        log_warning "Perl not available, trying alternative sed approach..."
        cp "$k8s_file.backup" "$k8s_file"

        # Use a temporary file approach for macOS compatibility
        sed "s|image: aap-server:.*|image: aap-server:$version|g" "$k8s_file" > "$k8s_file.tmp"
        mv "$k8s_file.tmp" "$k8s_file"

        sed "s|app.kubernetes.io/version: \".*\"|app.kubernetes.io/version: \"$version\"|g" "$k8s_file" > "$k8s_file.tmp"
        mv "$k8s_file.tmp" "$k8s_file"
    fi

    # Remove backup file
    rm -f "$k8s_file.backup"

    log_success "Updated Kubernetes deployment with version $version" >&2
}

# Main command handling
case "${1:-}" in
    "patch" | "")
        NEW_VERSION=$(increment_patch)
        update_k8s_version "$NEW_VERSION"
        echo "$NEW_VERSION"
        ;;
    "minor")
        NEW_VERSION=$(increment_minor)
        update_k8s_version "$NEW_VERSION"
        echo "$NEW_VERSION"
        ;;
    "major")
        NEW_VERSION=$(increment_major)
        update_k8s_version "$NEW_VERSION"
        echo "$NEW_VERSION"
        ;;
    "set")
        NEW_VERSION=$(set_version "$2")
        update_k8s_version "$NEW_VERSION"
        echo "$NEW_VERSION"
        ;;
    "get")
        get_version
        ;;
    "help" | "-h" | "--help")
        echo "Version Management for aap-server"
        echo ""
        echo "Usage: $0 [COMMAND] [ARGS]"
        echo ""
        echo "Commands:"
        echo "  patch          Increment patch version (default)"
        echo "  minor          Increment minor version"
        echo "  major          Increment major version"
        echo "  set <version>  Set specific version"
        echo "  get            Get current version"
        echo "  help           Show this help"
        echo ""
        echo "Examples:"
        echo "  $0 patch       # 0.0.1 → 0.0.2"
        echo "  $0 minor       # 0.0.1 → 0.1.0"
        echo "  $0 major       # 0.0.1 → 1.0.0"
        echo "  $0 set 1.2.3   # Set to 1.2.3"
        ;;
    *)
        log_error "Unknown command: $1"
        log_info "Use '$0 help' for available commands"
        exit 1
        ;;
esac
