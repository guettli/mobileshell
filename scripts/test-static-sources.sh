#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Test: Verify all files in internal/server/static have a clear source
# Either downloaded via build.sh OR explicitly listed as custom/handwritten

STATIC_DIR="internal/server/static"

# Files that are copied from node_modules in build.sh
DOWNLOADED_FILES=(
    "bootstrap.min.css"
    "htmx.min.js"
    "idiomorph-ext.min.js"
    "xterm.min.css"
    "xterm.min.js"
    "xterm-addon-fit.min.js"
    "xterm-addon-web-links.min.js"
)

# Files that are custom/handwritten for this project
CUSTOM_FILES=(
    "url-links.js"
)

# Get all files in static directory
actual_files=$(find "$STATIC_DIR" -type f -printf "%f\n" | sort)

# Build expected files list
expected_files=$(printf "%s\n" "${DOWNLOADED_FILES[@]}" "${CUSTOM_FILES[@]}" | sort)

# Compare
if [[ "$actual_files" != "$expected_files" ]]; then
    echo "❌ Static files do not match expected sources!"
    echo ""
    echo "Expected files (downloaded + custom):"
    echo "$expected_files"
    echo ""
    echo "Actual files found:"
    echo "$actual_files"
    echo ""
    
    # Show files that are unexpected
    comm -13 <(echo "$expected_files") <(echo "$actual_files") > /tmp/unexpected_files
    if [[ -s /tmp/unexpected_files ]]; then
        echo "Unexpected files (not in downloaded or custom list):"
        cat /tmp/unexpected_files
        echo ""
        echo "Add these files to either DOWNLOADED_FILES or CUSTOM_FILES in $0"
    fi
    
    # Show files that are missing
    comm -23 <(echo "$expected_files") <(echo "$actual_files") > /tmp/missing_files
    if [[ -s /tmp/missing_files ]]; then
        echo "Missing files (expected but not found):"
        cat /tmp/missing_files
    fi
    
    exit 1
fi

# Verify downloaded files are in build.sh
for file in "${DOWNLOADED_FILES[@]}"; do
    if ! grep -q "cp.*$file" scripts/build.sh; then
        echo "❌ Downloaded file '$file' not found in scripts/build.sh copy commands"
        exit 1
    fi
done

echo "✓ All static files have clear sources (downloaded or custom)"
