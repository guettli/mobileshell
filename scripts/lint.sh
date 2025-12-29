#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Error: Nix environment not active. Please run 'nix develop' first or use 'direnv allow'"
    exit 1
fi

git ls-files '*.sh' | xargs shellcheck
