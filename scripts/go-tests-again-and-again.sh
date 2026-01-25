#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Add randomness to expose flaky tests:
# - shuffle: randomize test execution order
# - rr --chaos: add scheduling chaos via rr record-and-replay
# - GODEBUG=asyncpreemptoff=1: disable Go's SIGURG (fixes rr incompatibility)
# Note: --chaos may occasionally crash on SIGCHLD from subprocesses, but it's worth the tradeoff

# Verify rr is installed
if ! command -v rr &>/dev/null; then
    echo "❌ rr is required but not found"
    echo "Install with: sudo apt install rr"
    exit 1
fi

# Check if perf_event_paranoid is configured correctly for rr
paranoid=$(cat /proc/sys/kernel/perf_event_paranoid)
if [ "$paranoid" -gt 1 ]; then
    echo "❌ rr requires /proc/sys/kernel/perf_event_paranoid <= 1, but it is $paranoid"
    echo ""
    echo "To fix permanently, run:"
    echo "  echo 'kernel.perf_event_paranoid = 1' | sudo tee /etc/sysctl.d/10-rr.conf"
    echo "  sudo sysctl -p /etc/sysctl.d/10-rr.conf"
    echo ""
    echo "Or temporarily (resets on reboot):"
    echo "  echo 1 | sudo tee /proc/sys/kernel/perf_event_paranoid"
    exit 1
fi

# Disable Go's async preemption (SIGURG) to avoid rr crash
export GODEBUG=asyncpreemptoff=1

# Set longer timeout for tests under rr --chaos (default is 5 seconds)
export TEST_TIMEOUT_SECONDS=60

i=0
while true; do
    if [[ i -gt 50 ]]; then
        break
    fi

    echo ==========================================================
    echo "Run: $i"
    date
    echo ==========================================================

    if ! rr record --chaos go test -shuffle=on -count=1 ./...; then
        echo ""
        echo "❌ Test failed on run $i"
        echo "Replay with: rr replay"
        exit 1
    fi

    echo
    sleep 1
    ((i++)) || true
done

echo "✓ All $i test runs passed"
