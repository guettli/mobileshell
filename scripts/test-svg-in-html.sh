#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Check for SVG elements in HTML files
# This linter fails if there are any <svg> tags in HTML files

# Search for SVG tags (case-insensitive)
# Pattern matches actual SVG tag openings: <svg followed by space, /, >, or end of line
if git ls-files -z '*.html' | xargs -0 grep -iE '<svg([[:space:]/>]|$)' 2>/dev/null; then
    echo ""
    echo "Error: SVG elements found in HTML files!"
    echo "Please remove SVG elements from the HTML files."
    exit 1
fi

echo "No SVG elements found in HTML files"
