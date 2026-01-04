#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# shellcheck disable=SC2046
http_locations=$(rg -n 'https?://' $(git ls-files | grep -vP '\.md$' | grep -vP 'internal/server/static|scripts/test-jsdom|jsdom.*.mjs') | { grep -vP 'github.com/guettli/bash-strict-mode|http://%s|Found string|example.com|http://"\s*\+\s*host|https://"\s*\+\s*host|xmlns="http://www.w3.org' || true; })
if [[ -n $http_locations ]]; then
    echo "Found string 'https://' in code. This should be avoided. All needed files should be embeded into the binary via go:embed"
    echo
    echo "$http_locations"
    exit 1
fi
