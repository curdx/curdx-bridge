#!/usr/bin/env bash
set -euo pipefail

# AutoFlow autoloop runner.
# Assumption: run from project root (WORKDIR = pwd).

WORKDIR="$(pwd)"

PIDFILE="$WORKDIR/.curdx/cxb-autoloop.pid"
LOGFILE="$WORKDIR/.curdx/cxb-autoloop.log"

# Resolve autoloop binary: prefer Go binary, fallback to Python script.
autoloop_bin() {
  # Check for Go binary in common locations
  for candidate in \
    "$HOME/.local/bin/cxb-autoloop" \
    "$HOME/.local/share/codex-dual/bin/cxb-autoloop" \
    "$(dirname "${BASH_SOURCE[0]}")/cxb-autoloop"; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return
    fi
  done
  # Fallback: not found
  echo ""
}

is_running() {
  if [[ -f "$PIDFILE" ]]; then
    local pid
    pid="$(cat "$PIDFILE" 2>/dev/null || true)"
    [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
  else
    return 1
  fi
}

start() {
  local bin
  bin="$(autoloop_bin)"
  if [[ -z "$bin" ]]; then
    echo "cxb-autoloop binary not found" >&2
    exit 1
  fi

  mkdir -p "$WORKDIR/.curdx"

  if is_running; then
    echo "cxb-autoloop already running (pid $(cat "$PIDFILE"))"
    exit 0
  fi

  : >"$LOGFILE"
  nohup "$bin" --repo-root "$WORKDIR" >>"$LOGFILE" 2>&1 &
  local pid=$!
  echo "$pid" >"$PIDFILE"
  echo "cxb-autoloop started (pid $pid)"
  echo "log: $LOGFILE"
}

stop() {
  if ! [[ -f "$PIDFILE" ]]; then
    echo "cxb-autoloop not running"
    exit 0
  fi
  local pid
  pid="$(cat "$PIDFILE" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    rm -f "$PIDFILE"
    echo "cxb-autoloop not running"
    exit 0
  fi

  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    sleep 0.2
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$PIDFILE"
  echo "cxb-autoloop stopped"
}

status() {
  if is_running; then
    echo "cxb-autoloop running (pid $(cat "$PIDFILE"))"
    exit 0
  fi
  echo "cxb-autoloop not running"
}

once() {
  local bin
  bin="$(autoloop_bin)"
  if [[ -z "$bin" ]]; then
    echo "cxb-autoloop binary not found" >&2
    exit 1
  fi
  "$bin" --repo-root "$WORKDIR" --once
}

cmd="${1:-start}"
case "$cmd" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  once) once ;;
  *)
    echo "Usage: $0 {start|stop|status|once}" >&2
    exit 2;;
esac
