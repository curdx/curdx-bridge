#!/usr/bin/env bash
# Cross-compile all CURDX binaries for multiple platforms.
# Usage: ./scripts/build-all.sh [output_dir]
#
# Environment:
#   CURDX_VERSION  — version string (default: `git describe` or "dev")
#   CURDX_JOBS     — max parallel platforms (default: all 5)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${1:-$REPO_ROOT/dist}"

# Platforms: os/arch
PLATFORMS=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  windows/amd64
)

# All cmd/ binaries
CMDS=($(ls "$REPO_ROOT/cmd/"))

# Derive version metadata. git-describe covers tagged releases;
# falls back to "dev" for dirty trees.
if [[ -z "${CURDX_VERSION:-}" ]]; then
  if CURDX_VERSION=$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null); then
    :
  else
    CURDX_VERSION="dev"
  fi
fi
GIT_COMMIT=$(cd "$REPO_ROOT" && git rev-parse HEAD 2>/dev/null || echo "unknown")
GIT_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS="-s -w \
  -X main.Version=${CURDX_VERSION} \
  -X main.GitCommit=${GIT_COMMIT} \
  -X main.GitDate=${GIT_DATE}"

# Note: `-X main.VAR=VAL` is the correct form for main-package vars; a full
# import path won't match because the linker treats `main` as a special name.
# Binaries that don't declare Version/GitCommit/GitDate simply ignore the
# flags (the linker silently skips unknown targets) but still benefit from
# `-s -w` stripping.

echo "Building ${#CMDS[@]} binaries × ${#PLATFORMS[@]} platforms"
echo "  version: ${CURDX_VERSION}"
echo "  commit:  ${GIT_COMMIT:0:12}"
echo "  date:    ${GIT_DATE}"
echo

build_platform() {
  local platform="$1"
  local GOOS="${platform%/*}"
  local GOARCH="${platform#*/}"
  local EXT=""
  if [[ "$GOOS" == "windows" ]]; then
    EXT=".exe"
  fi

  local PLATFORM_DIR="$OUT_DIR/curdx-${GOOS}-${GOARCH}"
  mkdir -p "$PLATFORM_DIR"

  local log="$OUT_DIR/.build-${GOOS}-${GOARCH}.log"
  : > "$log"

  for cmd in "${CMDS[@]}"; do
    if ! CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -ldflags="$LDFLAGS" \
      -o "$PLATFORM_DIR/${cmd}${EXT}" \
      "$REPO_ROOT/cmd/${cmd}" >>"$log" 2>&1; then
      echo "  [${GOOS}/${GOARCH}] FAIL: $cmd — see $log" >&2
      return 1
    fi
  done

  # Copy non-binary files needed for installation
  cp -r "$REPO_ROOT/claude_skills" "$PLATFORM_DIR/"
  cp -r "$REPO_ROOT/codex_skills" "$PLATFORM_DIR/"
  cp -r "$REPO_ROOT/config" "$PLATFORM_DIR/"
  cp "$REPO_ROOT/install.sh" "$PLATFORM_DIR/"

  # Package
  local ARCHIVE_NAME="curdx-${GOOS}-${GOARCH}"
  (
    cd "$OUT_DIR"
    if [[ "$GOOS" == "windows" ]]; then
      zip -qr "${ARCHIVE_NAME}.zip" "$(basename "$PLATFORM_DIR")"
    else
      tar czf "${ARCHIVE_NAME}.tar.gz" "$(basename "$PLATFORM_DIR")"
    fi
  )

  local size
  if [[ "$GOOS" == "windows" ]]; then
    size=$(du -h "$OUT_DIR/${ARCHIVE_NAME}.zip" | cut -f1)
    echo "  [${GOOS}/${GOARCH}] OK — ${ARCHIVE_NAME}.zip (${size})"
  else
    size=$(du -h "$OUT_DIR/${ARCHIVE_NAME}.tar.gz" | cut -f1)
    echo "  [${GOOS}/${GOARCH}] OK — ${ARCHIVE_NAME}.tar.gz (${size})"
  fi
  rm -f "$log"
}

mkdir -p "$OUT_DIR"

# Fan out per-platform builds in parallel; wait for all.
JOBS="${CURDX_JOBS:-${#PLATFORMS[@]}}"
pids=()
for platform in "${PLATFORMS[@]}"; do
  build_platform "$platform" &
  pids+=($!)

  # Throttle when hitting the job limit
  while (( $(jobs -rp | wc -l) >= JOBS )); do
    wait -n
  done
done

fail=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then
    fail=1
  fi
done

if (( fail )); then
  echo "Build FAILED — see per-platform logs in $OUT_DIR/.build-*.log" >&2
  exit 1
fi

echo
echo "Done. Archives in $OUT_DIR"
