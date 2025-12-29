#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

git ls-files '*.sh' | xargs shellcheck

git ls-files '*.md' | xargs markdownlint

# shellcheck disable=SC2046
http_locations=$(rg -n 'https?://' $(git ls-files | grep -vP '\.md$' | grep -vP 'internal/server/static') | { grep -vP 'github.com/guettli/bash-strict-mode|http://%s:|Found string|example.com' || true; })
if [[ -n $http_locations ]]; then
    echo "Found string 'https://' in code. This should be avoided. All needed files should be embeded into the binary via go:embed"
    echo
    echo "$http_locations"
    exit 1
fi

# shellcheck disable=SC2046
absolute_links=$(rg -n '(href|src)="/' $(git ls-files | grep -vP 'internal/server/static') || true)
if [[ -n $absolute_links ]]; then
    echo "Found absolute links in code/html/templates. Use relative paths instead (e.g., './static/') so the application works when served behind a reverse proxy at a sub-path like https://myserver.example.com/mobileshell"
    echo
    echo "$absolute_links"
    exit 1
fi
golangci-lint run ./...
