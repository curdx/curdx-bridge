#!/usr/bin/env bash
# CCB Quick Installer - downloads pre-built binaries from GitHub Release.
# Usage: curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/scripts/install-prebuilt.sh | bash
set -euo pipefail

REPO="curdx/curdx-bridge"
INSTALL_DIR="${CCB_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS and arch
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    darwin) os="darwin" ;;
    linux)  os="linux" ;;
    mingw*|msys*|cygwin*) os="windows" ;;
    *)
      echo "Unsupported OS: $os" >&2
      exit 1
      ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      echo "Unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac

  echo "${os}/${arch}"
}

# Get latest release tag
get_latest_version() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
}

main() {
  local platform version os arch ext archive_name url

  platform="$(detect_platform)"
  os="${platform%/*}"
  arch="${platform#*/}"

  version="${CCB_VERSION:-$(get_latest_version)}"
  if [[ -z "$version" ]]; then
    echo "ERROR: Could not determine latest version." >&2
    echo "Set CCB_VERSION=v1.0.0 to specify manually." >&2
    exit 1
  fi

  echo "Installing CCB ${version} for ${os}/${arch}..."

  ext="tar.gz"
  if [[ "$os" == "windows" ]]; then
    ext="zip"
  fi

  archive_name="ccb-${os}-${arch}.${ext}"
  url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  echo "Downloading ${url}..."
  curl -fsSL -o "${tmpdir}/${archive_name}" "$url"

  echo "Extracting..."
  cd "$tmpdir"
  if [[ "$ext" == "zip" ]]; then
    unzip -q "$archive_name"
  else
    tar xzf "$archive_name"
  fi

  local srcdir="ccb-${os}-${arch}"
  if [[ ! -d "$srcdir" ]]; then
    echo "ERROR: Expected directory $srcdir not found in archive." >&2
    exit 1
  fi

  # Install binaries
  mkdir -p "$INSTALL_DIR"
  local count=0
  for bin in "$srcdir"/*; do
    [[ -f "$bin" ]] || continue
    [[ -x "$bin" ]] || continue
    local name
    name="$(basename "$bin")"
    # Skip non-binary files
    case "$name" in
      *.sh|*.md) continue ;;
    esac
    cp "$bin" "$INSTALL_DIR/$name"
    chmod +x "$INSTALL_DIR/$name"
    count=$((count + 1))
  done

  # Install skills and config
  local share_dir="${CCB_SHARE_DIR:-$HOME/.local/share/ccb}"
  mkdir -p "$share_dir"
  for dir in claude_skills codex_skills config; do
    if [[ -d "$srcdir/$dir" ]]; then
      cp -r "$srcdir/$dir" "$share_dir/"
    fi
  done

  # Copy install.sh for ccb update/reinstall
  if [[ -f "$srcdir/install.sh" ]]; then
    cp "$srcdir/install.sh" "$share_dir/"
  fi

  echo ""
  echo "Installed $count binaries to $INSTALL_DIR"
  echo "Skills and config in $share_dir"

  # Check PATH
  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo "WARNING: $INSTALL_DIR is not in your PATH."
    echo "Add this to your shell config:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
  fi

  echo ""
  echo "Done! Run 'ccb --help' to get started."
}

main "$@"
