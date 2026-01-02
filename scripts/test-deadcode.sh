#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# deadcode may report false positives for error interface methods
# Filter out contentTypeError.Error which is required for the error interface
deadcode_output=$(deadcode ./... | grep -v 'contentTypeError.Error' || true)
if [[ -n "$deadcode_output" ]]; then
    echo "Found unused code:"
    echo "$deadcode_output"
    exit 1
fi
