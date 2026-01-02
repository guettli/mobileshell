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

    # Run the test script
    if "$script" > "$output_file" 2>&1; then
        echo "0" > "$exit_code_file"
    else
        echo "$?" > "$exit_code_file"
    fi
}

# Start all test scripts in parallel
for script in $TEST_SCRIPTS; do
    name=$(basename "$script" .sh)
    run_test "$script" &
    pids[$!]=$name
done

# Wait for all background jobs and collect results
failed_tests=()
for pid in "${!pids[@]}"; do
    name="${pids[$pid]}"
    wait "$pid" || true  # Don't exit on failure, collect all results

    exit_code_file="$TEMP_DIR/$name.exit"
    output_file="$TEMP_DIR/$name.out"

    if [[ -f "$exit_code_file" ]]; then
        exit_code=$(cat "$exit_code_file")
        if [[ "$exit_code" == "0" ]]; then
            echo "✓ $name"
        else
            echo "✗ $name (exit code: $exit_code)"
            failed_tests+=("$name")
        fi
    fi
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
