#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Cleanup script for stale mobileshell nohup test processes
#
# This script removes orphaned test processes that were not properly cleaned up
# after test runs. These processes accumulate over time and consume resources.
#
# INSTALLATION:
# 1. Ensure the script is executable (already done in git):
#    chmod +x scripts/cleanup-stale-processes.sh
#
# 2. Add to crontab to run every hour:
#    crontab -e
#    Then add this line (adjust path as needed):
#    0 * * * * $HOME/mobileshell/scripts/cleanup-stale-processes.sh
#
# 3. Or install via command:
#    SCRIPT_PATH="$HOME/mobileshell/scripts/cleanup-stale-processes.sh"
#    (crontab -l 2>/dev/null || true; echo "0 * * * * $SCRIPT_PATH") | crontab -
#
# VERIFICATION:
# - View installed crontab: crontab -l
# - Check logs: tail -f $HOME/log/delete-stale-test-processes.log
# - Manual run: $HOME/mobileshell/scripts/cleanup-stale-processes.sh

LOG_FILE="${LOG_FILE:-$HOME/log/delete-stale-test-processes.log}"

# Create log directory if it doesn't exist
mkdir -p "$(dirname "$LOG_FILE")"

echo "=== Cleanup started at $(date) ===" >> "$LOG_FILE"

# Find orphaned mobileshell nohup processes (PPID=1) running from /tmp with test-workspace
# These are test processes that should have been cleaned up
# Pattern matches: /tmp.*/mobileshell nohup --state-dir /tmp.* test-workspace-*
# This is specific enough to only match test processes, not production processes
STALE_PIDS=$(pgrep -u "$USER" -P 1 -f "/tmp.*mobileshell nohup --state-dir /tmp.*test-workspace" || true)

if [ -z "$STALE_PIDS" ]; then
    echo "No stale processes found" >> "$LOG_FILE"
else
    COUNT=$(echo "$STALE_PIDS" | wc -l)
    echo "Found $COUNT stale mobileshell nohup processes" >> "$LOG_FILE"

    for PID in $STALE_PIDS; do
        # Get process info before killing
        PROC_INFO=$(ps -p "$PID" -o pid,etime,cmd --no-headers 2>/dev/null || true)
        if [ -n "$PROC_INFO" ]; then
            echo "Killing PID $PID: $PROC_INFO" >> "$LOG_FILE"
            # Kill the process and all its children
            pkill -TERM -P "$PID" 2>/dev/null || true
            kill -TERM "$PID" 2>/dev/null || true

            # Wait briefly then force kill if still alive
            sleep 1
            if ps -p "$PID" > /dev/null 2>&1; then
                echo "  Force killing PID $PID" >> "$LOG_FILE"
                pkill -KILL -P "$PID" 2>/dev/null || true
                kill -KILL "$PID" 2>/dev/null || true
            fi
        fi
    done

    echo "Cleanup completed: $COUNT processes terminated" >> "$LOG_FILE"
fi

# Log final process count
REMAINING=$(ps -u "$USER" --no-headers | wc -l)
echo "Remaining $USER processes: $REMAINING" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"
