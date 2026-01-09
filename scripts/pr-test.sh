#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# PR Test Script - Run tests, push, and check CI
# This script automates the common workflow before creating a PR

echo "=== Running test suite ==="
./scripts/test.sh

echo ""
echo "=== Pushing to remote ==="
git push

echo ""
echo "=== Checking CI status ==="
# Wait a moment for CI to start
sleep 5

# Get current branch name
BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Check if gh CLI is available
if command -v gh &> /dev/null; then
    echo "Waiting for CI checks to complete..."
    gh pr checks || echo "No PR found for current branch, or checks not yet started"
else
    echo "gh CLI not installed. Please check CI status manually at:"
    REPO_URL=$(git config --get remote.origin.url | sed 's/\.git$//')
    echo "$REPO_URL/actions"
fi

echo ""
echo "✓ PR test workflow complete!"
