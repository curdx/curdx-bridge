#!/usr/bin/env bash
# Cross-compile all CCB binaries for multiple platforms.
# Usage: ./scripts/build-all.sh [output_dir]
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

echo "Building ${#CMDS[@]} binaries for ${#PLATFORMS[@]} platforms..."

for platform in "${PLATFORMS[@]}"; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  EXT=""
  if [[ "$GOOS" == "windows" ]]; then
    EXT=".exe"
  fi

  PLATFORM_DIR="$OUT_DIR/ccb-${GOOS}-${GOARCH}"
  mkdir -p "$PLATFORM_DIR"

  echo "  ${GOOS}/${GOARCH}:"
  for cmd in "${CMDS[@]}"; do
    echo -n "    $cmd"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -ldflags="-s -w" \
      -o "$PLATFORM_DIR/${cmd}${EXT}" \
      "$REPO_ROOT/cmd/${cmd}" 2>&1
    echo " OK"
  done

  # Copy non-binary files needed for installation
  cp -r "$REPO_ROOT/claude_skills" "$PLATFORM_DIR/"
  cp -r "$REPO_ROOT/codex_skills" "$PLATFORM_DIR/"
  cp -r "$REPO_ROOT/config" "$PLATFORM_DIR/"
  cp "$REPO_ROOT/install.sh" "$PLATFORM_DIR/"

  # Package
  ARCHIVE_NAME="ccb-${GOOS}-${GOARCH}"
  cd "$OUT_DIR"
  if [[ "$GOOS" == "windows" ]]; then
    zip -qr "${ARCHIVE_NAME}.zip" "$(basename "$PLATFORM_DIR")"
    echo "  -> ${ARCHIVE_NAME}.zip"
  else
    tar czf "${ARCHIVE_NAME}.tar.gz" "$(basename "$PLATFORM_DIR")"
    echo "  -> ${ARCHIVE_NAME}.tar.gz"
  fi
  cd - >/dev/null
done

echo "Done. Archives in $OUT_DIR"
