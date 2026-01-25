#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

i=0
while go test -race -count=1 ./...; do
    if [[ i -gt 50 ]]; then
        break
    fi
    date
    sleep 1
    echo ==========================================================
    echo "Run: $i"
    echo ==========================================================
    echo
    ((i++)) || true
done
