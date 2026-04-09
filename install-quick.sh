#!/usr/bin/env bash
# CCB Installer — auto-detects pre-built binaries vs source compilation.
#
# One-line install (macOS/Linux):
#   curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install-quick.sh | bash
#
# Options:
#   CCB_VERSION=v5.3.0    Pin a specific version (default: latest)
#   CCB_INSTALL_DIR=~/.local/bin  Change binary directory
#   CCB_FROM_SOURCE=1     Force source compilation even if pre-built available
set -euo pipefail

REPO="curdx/curdx-bridge"
INSTALL_DIR="${CCB_INSTALL_DIR:-$HOME/.local/bin}"
SHARE_DIR="${CCB_SHARE_DIR:-$HOME/.local/share/ccb}"

# ── Colors ──
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}▸${NC} %s\n" "$*"; }
ok()    { printf "${GREEN}✔${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}⚠${NC} %s\n" "$*" >&2; }
fail()  { printf "${RED}✖${NC} %s\n" "$*" >&2; exit 1; }

# ── Platform detection ──
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    darwin)             os="darwin" ;;
    linux)              os="linux" ;;
    mingw*|msys*|cygwin*) os="windows" ;;
    *) fail "Unsupported OS: $os" ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) fail "Unsupported architecture: $arch" ;;
  esac

  echo "${os}/${arch}"
}

# ── Version detection ──
get_latest_version() {
  local url="https://api.github.com/repos/${REPO}/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
  fi
}

# ── Download helper ──
download() {
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$dest" "$url"
  else
    fail "Need curl or wget to download"
  fi
}

# ── Install from pre-built release ──
install_prebuilt() {
  local platform="$1" version="$2"
  local os="${platform%/*}" arch="${platform#*/}"

  local ext="tar.gz"
  [[ "$os" == "windows" ]] && ext="zip"

  local archive="ccb-${os}-${arch}.${ext}"
  local url="https://github.com/${REPO}/releases/download/${version}/${archive}"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  info "Downloading CCB ${version} for ${os}/${arch}..."
  download "$url" "${tmpdir}/${archive}" || return 1

  info "Extracting..."
  cd "$tmpdir"
  if [[ "$ext" == "zip" ]]; then
    unzip -q "$archive" || return 1
  else
    tar xzf "$archive" || return 1
  fi

  local srcdir="ccb-${os}-${arch}"
  [[ -d "$srcdir" ]] || return 1

  # Install binaries
  mkdir -p "$INSTALL_DIR"
  local count=0
  for bin in "$srcdir"/*; do
    [[ -f "$bin" ]] || continue
    local name
    name="$(basename "$bin")"
    case "$name" in
      *.sh|*.md|*.json) continue ;;  # skip non-binaries
    esac
    # On Unix, check if it's executable or an .exe
    if [[ -x "$bin" ]] || [[ "$name" == *.exe ]]; then
      cp "$bin" "$INSTALL_DIR/$name"
      chmod +x "$INSTALL_DIR/$name" 2>/dev/null || true
      count=$((count + 1))
    fi
  done

  # Install skills and config
  mkdir -p "$SHARE_DIR"
  for dir in claude_skills codex_skills config; do
    if [[ -d "$srcdir/$dir" ]]; then
      rm -rf "$SHARE_DIR/$dir"
      cp -r "$srcdir/$dir" "$SHARE_DIR/"
    fi
  done

  ok "Installed $count binaries to $INSTALL_DIR"
  return 0
}

# ── Install from source ──
install_from_source() {
  if ! command -v go >/dev/null 2>&1; then
    fail "Go is required for source install. Install Go from https://go.dev/dl/"
  fi

  local go_version
  go_version="$(go version | grep -oE '[0-9]+\.[0-9]+' | head -1)"
  info "Building from source with Go ${go_version}..."

  local repo_dir
  if [[ -f "go.mod" ]] && grep -q "curdx-bridge" go.mod 2>/dev/null; then
    repo_dir="."
  else
    local tmpdir
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT
    info "Cloning repository..."
    git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir/curdx-bridge"
    repo_dir="$tmpdir/curdx-bridge"
  fi

  mkdir -p "$INSTALL_DIR"
  local count=0
  for cmd_dir in "$repo_dir"/cmd/*/; do
    local name
    name="$(basename "$cmd_dir")"
    info "  Building $name..."
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
      -o "$INSTALL_DIR/$name" "$cmd_dir" 2>&1
    count=$((count + 1))
  done

  # Install skills and config
  mkdir -p "$SHARE_DIR"
  for dir in claude_skills codex_skills config; do
    if [[ -d "$repo_dir/$dir" ]]; then
      rm -rf "$SHARE_DIR/$dir"
      cp -r "$repo_dir/$dir" "$SHARE_DIR/"
    fi
  done

  ok "Built and installed $count binaries to $INSTALL_DIR"
}

# ── Post-install setup ──
post_install() {
  # Run Claude skills installation if install.sh exists in share
  if [[ -f "$SHARE_DIR/config/ccb-status.sh" ]]; then
    ok "Config files installed to $SHARE_DIR"
  fi

  # PATH check
  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    warn "$INSTALL_DIR is not in your PATH"
    echo "  Add to your shell config (~/.bashrc, ~/.zshrc, etc.):"
    printf "  ${BOLD}export PATH=\"%s:\$PATH\"${NC}\n" "$INSTALL_DIR"
  fi

  # Verify
  echo ""
  if command -v ccb >/dev/null 2>&1; then
    ok "Installation complete! $(ccb --print-version 2>/dev/null || echo '')"
  else
    ok "Installation complete!"
  fi
  echo ""
  echo "  Get started:"
  echo "    ccb --help          # Show help"
  echo "    ccb codex claude    # Start with Codex + Claude"
  echo ""
}

# ── Main ──
main() {
  printf "${BOLD}CCB Installer${NC}\n"
  echo ""

  local platform version
  platform="$(detect_platform)"

  # Try pre-built first (unless forced source)
  if [[ "${CCB_FROM_SOURCE:-}" == "1" ]]; then
    install_from_source
    post_install
    return
  fi

  version="${CCB_VERSION:-$(get_latest_version)}"

  if [[ -n "$version" ]]; then
    if install_prebuilt "$platform" "$version" 2>/dev/null; then
      post_install
      return
    fi
    warn "Pre-built download failed, falling back to source compilation..."
  else
    warn "No release found, building from source..."
  fi

  install_from_source
  post_install
}

main "$@"
