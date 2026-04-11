#!/usr/bin/env bash
# CURDX Installer — auto-detects pre-built binaries vs source compilation.
#
# One-line install (macOS/Linux):
#   curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install-quick.sh | bash
#
# Options:
#   CURDX_VERSION=v5.3.0    Pin a specific version (default: latest)
#   CURDX_INSTALL_DIR=~/.local/bin  Change binary directory
#   CURDX_FROM_SOURCE=1     Force source compilation even if pre-built available
set -euo pipefail

REPO="curdx/curdx-bridge"
INSTALL_DIR="${CURDX_INSTALL_DIR:-$HOME/.local/bin}"
SHARE_DIR="${CURDX_SHARE_DIR:-$HOME/.local/share/curdx}"

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

  local archive="curdx-${os}-${arch}.${ext}"
  local url="https://github.com/${REPO}/releases/download/${version}/${archive}"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  info "Downloading CURDX ${version} for ${os}/${arch}..."
  download "$url" "${tmpdir}/${archive}" || return 1

  info "Extracting..."
  cd "$tmpdir"
  if [[ "$ext" == "zip" ]]; then
    unzip -q "$archive" || return 1
  else
    tar xzf "$archive" || return 1
  fi

  local srcdir="curdx-${os}-${arch}"
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

# ── Install Claude skills to ~/.claude/skills/ ──
install_claude_skills() {
  local src="$SHARE_DIR/claude_skills"
  local dst="$HOME/.claude/skills"

  [[ -d "$src" ]] || return 0

  mkdir -p "$dst"

  # Clean obsolete skills
  for obs in cask gask oask lask cpend gpend opend lpend cping gping oping lping ping auto; do
    [[ -d "$dst/$obs" ]] && rm -rf "$dst/$obs"
  done

  local count=0
  for skill_dir in "$src"/*/; do
    [[ -d "$skill_dir" ]] || continue
    local name
    name="$(basename "$skill_dir")"
    [[ "$name" == "docs" ]] && continue

    local skill_md=""
    if [[ -f "$skill_dir/SKILL.md.bash" ]]; then
      skill_md="$skill_dir/SKILL.md.bash"
    elif [[ -f "$skill_dir/SKILL.md" ]]; then
      skill_md="$skill_dir/SKILL.md"
    else
      continue
    fi

    mkdir -p "$dst/$name"
    cp -f "$skill_md" "$dst/$name/SKILL.md"

    # Copy subdirectories (references/ etc.)
    for subdir in "$skill_dir"*/; do
      [[ -d "$subdir" ]] && cp -rf "$subdir" "$dst/$name/"
    done
    count=$((count + 1))
  done

  # Copy shared docs
  [[ -d "$src/docs" ]] && { rm -rf "$dst/docs"; cp -r "$src/docs" "$dst/docs"; }

  ok "Installed $count Claude skills to $dst"
}

# ── Install Codex skills to ~/.codex/skills/ ──
install_codex_skills() {
  local src="$SHARE_DIR/codex_skills"
  local dst="${CODEX_HOME:-$HOME/.codex}/skills"

  [[ -d "$src" ]] || return 0

  mkdir -p "$dst"

  # Clean obsolete skills
  for obs in cask gask oask lask cpend gpend opend lpend cping gping oping lping; do
    [[ -d "$dst/$obs" ]] && rm -rf "$dst/$obs"
  done

  local count=0
  for skill_dir in "$src"/*/; do
    [[ -d "$skill_dir" ]] || continue
    local name
    name="$(basename "$skill_dir")"

    local skill_md=""
    if [[ -f "$skill_dir/SKILL.md.bash" ]]; then
      skill_md="$skill_dir/SKILL.md.bash"
    elif [[ -f "$skill_dir/SKILL.md" ]]; then
      skill_md="$skill_dir/SKILL.md"
    else
      continue
    fi

    mkdir -p "$dst/$name"
    cp -f "$skill_md" "$dst/$name/SKILL.md"

    for subdir in "$skill_dir"*/; do
      [[ -d "$subdir" ]] && cp -rf "$subdir" "$dst/$name/"
    done
    count=$((count + 1))
  done

  ok "Installed $count Codex skills to $dst"
}

# ── Inject CURDX config into ~/.claude/CLAUDE.md ──
install_claude_md() {
  local claude_md="$HOME/.claude/CLAUDE.md"
  local template="$SHARE_DIR/config/claude-md-curdx.md"
  local start_marker="<!-- CURDX_CONFIG_START -->"
  local end_marker="<!-- CURDX_CONFIG_END -->"

  [[ -f "$template" ]] || return 0

  mkdir -p "$HOME/.claude"

  if [[ -f "$claude_md" ]]; then
    if grep -q "$start_marker" "$claude_md" 2>/dev/null; then
      # Replace existing block using helper if available, otherwise sed
      if [[ -x "$INSTALL_DIR/curdx-installer-helper" ]]; then
        "$INSTALL_DIR/curdx-installer-helper" replace-block "$claude_md" "$template" "$start_marker" "$end_marker"
      else
        # Manual replacement: remove old block and append new
        local tmpfile
        tmpfile="$(mktemp)"
        awk -v start="$start_marker" -v end="$end_marker" '
          $0 == start { skip=1; next }
          $0 == end   { skip=0; next }
          !skip { print }
        ' "$claude_md" > "$tmpfile"
        cat "$template" >> "$tmpfile"
        mv "$tmpfile" "$claude_md"
      fi
      ok "Updated CURDX config in CLAUDE.md"
    else
      echo "" >> "$claude_md"
      cat "$template" >> "$claude_md"
      ok "Added CURDX config to CLAUDE.md"
    fi
  else
    cp "$template" "$claude_md"
    ok "Created CLAUDE.md with CURDX config"
  fi
}

# ── Post-install setup ──
post_install() {
  echo ""

  # Install skills
  install_claude_skills
  install_codex_skills

  # Inject CLAUDE.md config
  install_claude_md

  # PATH check
  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    warn "$INSTALL_DIR is not in your PATH"
    echo "  Add to your shell config (~/.bashrc, ~/.zshrc, etc.):"
    printf "  ${BOLD}export PATH=\"%s:\$PATH\"${NC}\n" "$INSTALL_DIR"
  fi

  # Verify
  echo ""
  if command -v curdx >/dev/null 2>&1; then
    ok "Installation complete! $(curdx --print-version 2>/dev/null || echo '')"
  else
    ok "Installation complete!"
  fi
  echo ""
  echo "  Get started:"
  echo "    curdx --help          # Show help"
  echo "    curdx codex claude    # Start with Codex + Claude"
  echo ""
}

# ── Main ──
main() {
  printf "${BOLD}CURDX Installer${NC}\n"
  echo ""

  local platform version
  platform="$(detect_platform)"

  # Try pre-built first (unless forced source)
  if [[ "${CURDX_FROM_SOURCE:-}" == "1" ]]; then
    install_from_source
    post_install
    return
  fi

  version="${CURDX_VERSION:-$(get_latest_version)}"

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
