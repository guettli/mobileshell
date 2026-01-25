#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Add randomness to expose flaky tests:
# - shuffle: randomize test execution order
# - libfiu: inject random delays into syscalls via LD_PRELOAD

# Verify libfiu.so exists
LIBFIU_PATH=/usr/lib/libfiu.so
if [[ ! -f "$LIBFIU_PATH" ]]; then
    echo "❌ libfiu.so not found at $LIBFIU_PATH"
    echo "Install with: sudo apt install libfiu0 fiu-utils"
    exit 1
fi

i=0
while true; do
    if [[ i -gt 50 ]]; then
        break
    fi

    echo ==========================================================
    echo "Run: $i"
    date
    echo ==========================================================

    # Enable libfiu with random delays on I/O operations
    export FIU_ENABLE=posix/io/*
    export FIU_CTRL_OPTS="probability=0.1,delay=1-10"
    export LD_PRELOAD="$LIBFIU_PATH"

    if ! go test -race -shuffle=on -count=1 ./...; then
        echo
        echo "❌ Test failed on run $i"
        exit 1
    fi

    echo
    sleep 1
    ((i++)) || true
done

echo "✓ All $i test runs passed"
