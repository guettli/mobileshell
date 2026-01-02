#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

counter=1
while true; do
    if ! ./scripts/test.sh; then
        echo
        echo "test failed Counter: $counter"
        music
        break
    fi
    echo
    echo "✓ Test run $counter passed"
    date
    echo "Sleeping 3 seconds before run $((counter + 1))..."
    sleep 3
    counter=$((counter + 1))
done
