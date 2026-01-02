#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Create temp directory for results
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

# Find all test-*.sh scripts (excluding test.sh itself and test-for-ever.sh)
TEST_SCRIPTS=$(find scripts -name 'test-*.sh' -not -name 'test.sh' -not -name 'test-for-ever.sh' | sort)

# Arrays to track jobs
declare -A pids

# Function to run a test and capture its output
run_test() {
    local script=$1
    local name
    name=$(basename "$script" .sh)
    local output_file="$TEMP_DIR/$name.out"
    local exit_code_file="$TEMP_DIR/$name.exit"
    local time_file="$TEMP_DIR/$name.time"

    # Run the test script with timing
    local start_time
    start_time=$(date +%s)
    if "$script" > "$output_file" 2>&1; then
        echo "0" > "$exit_code_file"
    else
        echo "$?" > "$exit_code_file"
    fi
    local end_time
    end_time=$(date +%s)
    local duration=$((end_time - start_time))
    echo "$duration" > "$time_file"
}

# Start all test scripts in parallel
for script in $TEST_SCRIPTS; do
    name=$(basename "$script" .sh)
    run_test "$script" &
    pids[$!]=$name
done

# Wait for all background jobs and collect results as they finish
failed_tests=()
remaining_pids=("${!pids[@]}")

while [[ ${#remaining_pids[@]} -gt 0 ]]; do
    for i in "${!remaining_pids[@]}"; do
        pid="${remaining_pids[$i]}"

        # Check if process has finished (non-blocking wait)
        if ! kill -0 "$pid" 2>/dev/null; then
            # Process finished, get its results
            wait "$pid" || true
            name="${pids[$pid]}"

            exit_code_file="$TEMP_DIR/$name.exit"
            output_file="$TEMP_DIR/$name.out"
            time_file="$TEMP_DIR/$name.time"

            if [[ -f "$exit_code_file" ]]; then
                exit_code=$(cat "$exit_code_file")
                duration=$(cat "$time_file" 2>/dev/null || echo "?")
                if [[ "$exit_code" == "0" ]]; then
                    echo "✓ $name (${duration}s)"
                else
                    echo "✗ $name (exit code: $exit_code, ${duration}s)"
                    failed_tests+=("$name")
                fi
            fi

            # Remove this PID from remaining list
            unset 'remaining_pids[$i]'
            remaining_pids=("${remaining_pids[@]}")  # Re-index array
            break  # Restart the loop to re-index properly
        fi
    done

    # Small sleep to avoid busy-waiting
    sleep 0.1
done

# If any tests failed, show their output and exit with error
if [[ ${#failed_tests[@]} -gt 0 ]]; then
    echo ""
    echo "Failed tests output:"
    echo "==================="
    for name in "${failed_tests[@]}"; do
        echo ""
        echo "--- $name ---"
        cat "$TEMP_DIR/$name.out"
    done
    exit 1
fi

echo ""
echo "All tests passed!"
