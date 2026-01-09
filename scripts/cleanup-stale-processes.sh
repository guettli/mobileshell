#!/bin/bash
# Cleanup script for stale mobileshell nohup test processes
#
# This script removes orphaned test processes that were not properly cleaned up
# after test runs. These processes accumulate over time and consume resources.
#
# INSTALLATION:
# 1. Ensure the script is executable (already done in git):
#    chmod +x scripts/cleanup-stale-processes.sh
#
# 2. Create the log directory:
#    mkdir -p $HOME/log
#
# 3. Add to crontab to run every hour:
#    crontab -e
#    Then add this line (adjust path as needed):
#    0 * * * * $HOME/mobileshell/scripts/cleanup-stale-processes.sh
#
# 4. Or install via command:
#    SCRIPT_PATH="$HOME/mobileshell/scripts/cleanup-stale-processes.sh"
#    (crontab -l 2>/dev/null || true; echo "0 * * * * $SCRIPT_PATH") | crontab -
#
# VERIFICATION:
# - View installed crontab: crontab -l
# - Check logs: tail -f $HOME/log/delete-stale-test-processes.log
# - Manual run: $HOME/mobileshell/scripts/cleanup-stale-processes.sh

LOG_FILE="${LOG_FILE:-$HOME/log/delete-stale-test-processes.log}"

echo "=== Cleanup started at $(date) ===" >> "$LOG_FILE"

# Find orphaned mobileshell nohup processes (PPID=1) running from /tmp
# These are test processes that should have been cleaned up
# shellcheck disable=SC2009  # Need ps output format for PPID+cmd pattern matching
STALE_PIDS=$(ps -u mobileshell -o ppid,pid,etime,cmd --no-headers | \
    grep "^ *1 " | grep "/tmp.*mobileshell nohup" | awk '{print $2}')

if [ -z "$STALE_PIDS" ]; then
    echo "No stale processes found" >> "$LOG_FILE"
else
    COUNT=$(echo "$STALE_PIDS" | wc -l)
    echo "Found $COUNT stale mobileshell nohup processes" >> "$LOG_FILE"

    for PID in $STALE_PIDS; do
        # Get process info before killing
        PROC_INFO=$(ps -p "$PID" -o pid,etime,cmd --no-headers 2>/dev/null)
        if [ -n "$PROC_INFO" ]; then
            echo "Killing PID $PID: $PROC_INFO" >> "$LOG_FILE"
            # Kill the process and all its children
            pkill -TERM -P "$PID" 2>/dev/null
            kill -TERM "$PID" 2>/dev/null

            # Wait briefly then force kill if still alive
            sleep 1
            if ps -p "$PID" > /dev/null 2>&1; then
                echo "  Force killing PID $PID" >> "$LOG_FILE"
                pkill -KILL -P "$PID" 2>/dev/null
                kill -KILL "$PID" 2>/dev/null
            fi
        fi
    done

    echo "Cleanup completed: $COUNT processes terminated" >> "$LOG_FILE"
fi

# Log final process count
REMAINING=$(ps -u mobileshell --no-headers | wc -l)
echo "Remaining mobileshell processes: $REMAINING" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"
