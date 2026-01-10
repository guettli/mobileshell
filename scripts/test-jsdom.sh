#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Only show verbose output if not in CI
if [[ -z "${CI:-}" ]]; then
    echo "MobileShell JSDOM Integration Test"
    echo "===================================="
fi

# Create temporary state directory
TEMP_STATE_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_STATE_DIR"' EXIT
[[ -z "${CI:-}" ]] && echo "Using temporary state directory: $TEMP_STATE_DIR"

# Generate test password (must be at least 36 characters)
PASSWORD_FILE="$TEMP_STATE_DIR/test-password.txt"
openssl rand -base64 32 | tr -d '/+=' | head -c 32 > "$PASSWORD_FILE"
PASSWORD="test-password-$(cat "$PASSWORD_FILE")"
[[ -z "${CI:-}" ]] && echo "Generated test password (length: ${#PASSWORD})"

# Add password using the CLI
[[ -z "${CI:-}" ]] && echo "Adding password via add-password command..."
STDERR_TMP=$(mktemp)
echo "$PASSWORD" | go run ./cmd/mobileshell add-password --state-dir "$TEMP_STATE_DIR" --from-stdin 2>"$STDERR_TMP"
exit_code=$?
[ -s "$STDERR_TMP" ] && grep -v "copying path" "$STDERR_TMP" >&2 || true
rm -f "$STDERR_TMP"
[ $exit_code -ne 0 ] && exit $exit_code
[[ -z "${CI:-}" ]] && echo "✓ Password added"

# Build the server
[[ -z "${CI:-}" ]] && echo "Building server..."
STDERR_TMP=$(mktemp)
go build -o "$TEMP_STATE_DIR/mobileshell" ./cmd/mobileshell 2>"$STDERR_TMP"
exit_code=$?
[ -s "$STDERR_TMP" ] && grep -v "copying path" "$STDERR_TMP" >&2 || true
rm -f "$STDERR_TMP"
[ $exit_code -ne 0 ] && exit $exit_code
[[ -z "${CI:-}" ]] && echo "✓ Server built"

# Find a free port
[[ -z "${CI:-}" ]] && echo "Finding free port..."
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
[[ -z "${CI:-}" ]] && echo "✓ Using port $PORT"

# Start server
[[ -z "${CI:-}" ]] && echo "Starting server..."
SERVER_LOG="$TEMP_STATE_DIR/server.log"
"$TEMP_STATE_DIR/mobileshell" run --state-dir "$TEMP_STATE_DIR" --port "$PORT" > "$SERVER_LOG" 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true; rm -rf "$TEMP_STATE_DIR"' EXIT

# Wait for server to start
[[ -z "${CI:-}" ]] && echo "Waiting for server to start..."
for i in {1..30}; do
  if grep -q "Starting server" "$SERVER_LOG" 2>/dev/null; then
    [[ -z "${CI:-}" ]] && echo "✓ Server started (PID: $SERVER_PID)"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "Failed to start server"
    echo "Server log:"
    cat "$SERVER_LOG"
    exit 1
  fi
  sleep 0.1
done

# Verify server is responding
for i in {1..30}; do
  if curl -s -o /dev/null "http://localhost:$PORT/login"; then
    [[ -z "${CI:-}" ]] && echo "✓ Server is responding"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "Server not responding after 3 seconds"
    exit 1
  fi
  sleep 0.1
done

# Install pnpm dependencies if needed
cd "$(dirname "$0")/.."
if [ ! -d "node_modules" ]; then
  [[ -z "${CI:-}" ]] && echo "Installing pnpm dependencies..."
  pnpm install > /dev/null 2>&1
  [[ -z "${CI:-}" ]] && echo "✓ Dependencies installed"
fi
cd "$(dirname "$0")"

# Run the JSDOM tests in parallel
[[ -z "${CI:-}" ]] && echo "Running JSDOM tests in parallel..."
[[ -z "${CI:-}" ]] && echo ""
if ! SERVER_URL="http://localhost:$PORT" PASSWORD="$PASSWORD" node jsdom-test-parallel.mjs; then
  echo ""
  echo "Test failed. Server log (last 100 lines):"
  tail -100 "$SERVER_LOG"
  exit 1
fi

# Cleanup
[[ -z "${CI:-}" ]] && echo ""
[[ -z "${CI:-}" ]] && echo "Stopping server..."
kill $SERVER_PID 2>/dev/null || true
wait $SERVER_PID 2>/dev/null || true

[[ -z "${CI:-}" ]] && echo "✓ Test completed successfully"
