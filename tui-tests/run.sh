#!/usr/bin/env bash
#
# TUI E2E test runner for temporal-agent-harness.
#
# Builds tcx + worker, starts Temporal dev server and worker,
# runs tui-test, then tears everything down.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPORAL_PORT=18233
TEMPORAL_UI_PORT=18234

# PIDs to clean up
TEMPORAL_PID=""
WORKER_PID=""

cleanup() {
  echo ""
  echo "==> Cleaning up..."
  if [ -n "$WORKER_PID" ] && kill -0 "$WORKER_PID" 2>/dev/null; then
    echo "    Stopping worker (PID $WORKER_PID)"
    kill "$WORKER_PID" 2>/dev/null || true
    wait "$WORKER_PID" 2>/dev/null || true
  fi
  if [ -n "$TEMPORAL_PID" ] && kill -0 "$TEMPORAL_PID" 2>/dev/null; then
    echo "    Stopping Temporal dev server (PID $TEMPORAL_PID)"
    kill "$TEMPORAL_PID" 2>/dev/null || true
    wait "$TEMPORAL_PID" 2>/dev/null || true
  fi
  echo "==> Cleanup complete"
}
trap cleanup EXIT

# --- 1. Check prerequisites ---

echo "==> Checking prerequisites..."

if [ -z "${OPENAI_API_KEY:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "ERROR: At least one LLM API key required: OPENAI_API_KEY or ANTHROPIC_API_KEY"
  exit 1
fi

# Find temporal CLI
TEMPORAL_BIN=""
if command -v temporal &>/dev/null; then
  TEMPORAL_BIN="temporal"
elif [ -x "$HOME/.temporalio/bin/temporal" ]; then
  TEMPORAL_BIN="$HOME/.temporalio/bin/temporal"
else
  echo "ERROR: temporal CLI not found. Install from https://docs.temporal.io/cli"
  exit 1
fi
echo "    temporal CLI: $TEMPORAL_BIN"

if ! command -v go &>/dev/null; then
  echo "ERROR: go not found in PATH"
  exit 1
fi

if ! command -v node &>/dev/null; then
  echo "ERROR: node not found in PATH"
  exit 1
fi

# --- 2. Build binaries ---

echo "==> Building tcx binary..."
(cd "$PROJECT_ROOT" && go build -o "$PROJECT_ROOT/tcx" ./cmd/tcx)
echo "    Built: $PROJECT_ROOT/tcx"

echo "==> Building worker binary..."
(cd "$PROJECT_ROOT" && go build -o "$PROJECT_ROOT/worker" ./cmd/worker)
echo "    Built: $PROJECT_ROOT/worker"

# --- 3. Start Temporal dev server ---

echo "==> Starting Temporal dev server on port $TEMPORAL_PORT..."
$TEMPORAL_BIN server start-dev \
  --port "$TEMPORAL_PORT" \
  --ui-port "$TEMPORAL_UI_PORT" \
  --headless \
  --log-format json \
  --log-level error \
  &>/dev/null &
TEMPORAL_PID=$!
echo "    Temporal PID: $TEMPORAL_PID"

# Wait for Temporal to be ready (TCP probe)
echo "==> Waiting for Temporal to be ready..."
for i in $(seq 1 30); do
  if bash -c "echo >/dev/tcp/localhost/$TEMPORAL_PORT" 2>/dev/null; then
    echo "    Temporal ready after ${i}s"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "ERROR: Temporal did not start within 30s"
    exit 1
  fi
  sleep 1
done

# --- 4. Start worker ---

echo "==> Starting worker..."
TEMPORAL_ADDRESS="localhost:$TEMPORAL_PORT" \
  "$PROJECT_ROOT/worker" &>/dev/null &
WORKER_PID=$!
echo "    Worker PID: $WORKER_PID"

# Give worker a moment to register with Temporal
sleep 2

# Verify worker is still running
if ! kill -0 "$WORKER_PID" 2>/dev/null; then
  echo "ERROR: Worker exited prematurely"
  exit 1
fi

# --- 5. Install npm dependencies (if needed) ---

if [ ! -d "$SCRIPT_DIR/node_modules" ]; then
  echo "==> Installing npm dependencies..."
  (cd "$SCRIPT_DIR" && npm install)
fi

# --- 6. Create seed session for resume testing ---

echo "==> Creating seed session for resume testing..."
SEED_TYPESCRIPT=$(mktemp)
# Run tcx under `script` to provide a PTY (bubbletea requires one).
# tcx doesn't auto-exit after a turn, so we background it, wait for
# the LLM response, then send SIGINT for a clean exit.
script -qf -c "$PROJECT_ROOT/tcx --temporal-host localhost:$TEMPORAL_PORT --full-auto --model gpt-4o-mini --no-color --inline -m 'Say exactly the word: persimmon'" "$SEED_TYPESCRIPT" &>/dev/null &
SEED_PID=$!

# Wait up to 45s for the LLM response (check for "ready" in status bar = turn complete)
for i in $(seq 1 45); do
  if sed 's/\x1b\[[0-9;]*[a-zA-Z]//g' "$SEED_TYPESCRIPT" 2>/dev/null | grep -q 'ready'; then
    echo "    Seed session turn complete after ${i}s"
    break
  fi
  if [ "$i" -eq 45 ]; then
    echo "    WARNING: Seed session timed out (45s)"
  fi
  sleep 1
done

# Send SIGINT for a clean exit (in StateInput, tcx handles Ctrl+C gracefully)
kill -INT "$SEED_PID" 2>/dev/null || true
sleep 1
kill "$SEED_PID" 2>/dev/null || true
wait "$SEED_PID" 2>/dev/null || true
rm -f "$SEED_TYPESCRIPT"

# Use Temporal CLI to find the most recent workflow ID (more reliable than
# parsing ANSI-laden script output).
RESUME_SESSION_ID=$($TEMPORAL_BIN workflow list --address "localhost:$TEMPORAL_PORT" --limit 1 2>/dev/null | grep -oP 'codex-[a-f0-9]+' | head -1 || true)
if [ -n "$RESUME_SESSION_ID" ]; then
  echo "    Seed session: $RESUME_SESSION_ID"
  export RESUME_SESSION_ID
else
  echo "    WARNING: Could not create seed session (resume tests will skip)"
fi

# --- 7. Run tui-test ---

echo "==> Running TUI tests..."
echo ""

export TCX_BINARY="$PROJECT_ROOT/tcx"
export TEMPORAL_HOST="localhost:$TEMPORAL_PORT"

cd "$SCRIPT_DIR"
set +e
npx @microsoft/tui-test
TEST_EXIT=$?
set -e

echo ""
if [ $TEST_EXIT -eq 0 ]; then
  echo "==> All TUI tests passed!"
else
  echo "==> TUI tests failed (exit code: $TEST_EXIT)"
fi

exit $TEST_EXIT
