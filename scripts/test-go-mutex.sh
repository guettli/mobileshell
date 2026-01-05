#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Exclude internal/wshub since mutex is required for thread-safe WebSocket connection management
go_mutex=$(git ls-files '*.go' | grep -v 'internal/wshub/' | { xargs rg -n 'Mutex' || true; })
if [[ -n $go_mutex ]]; then
    echo "Found 'Mutex' in Go source code. Please avoid mutexes. This is stateless http. There should be a way to avoid mutexes"
    echo
    echo "$go_mutex"
    exit 1
fi
