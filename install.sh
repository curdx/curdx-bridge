#!/usr/bin/env bash
set -euo pipefail

# Detect if running via pipe (curl | bash) vs direct execution
if [[ -n "${BASH_SOURCE[0]:-}" && "${BASH_SOURCE[0]}" != "bash" && "${BASH_SOURCE[0]}" != "/dev/stdin" ]]; then
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  # Running via curl | bash — download latest release tarball (pre-built binaries)
  _CURDX_TMPDIR="$(mktemp -d)"
  trap 'rm -rf "$_CURDX_TMPDIR"' EXIT

  _CURDX_REPO="curdx/curdx-bridge"
  # Detect OS and architecture
  _os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  _arch="$(uname -m)"
  case "$_arch" in
    x86_64)  _arch="amd64" ;;
    aarch64|arm64) _arch="arm64" ;;
    *) echo "ERROR: Unsupported architecture: $_arch" >&2; exit 1 ;;
  esac
  case "$_os" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*) _os="windows" ;;
    *) echo "ERROR: Unsupported OS: $_os" >&2; exit 1 ;;
  esac

  _asset="curdx-${_os}-${_arch}"
  if [[ "$_os" == "windows" ]]; then
    _asset_file="${_asset}.zip"
  else
    _asset_file="${_asset}.tar.gz"
  fi

  # Fetch latest release download URL
  _download_url="https://github.com/${_CURDX_REPO}/releases/latest/download/${_asset_file}"
  echo "Downloading curdx-bridge (${_os}/${_arch})..."
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$_download_url" -o "$_CURDX_TMPDIR/$_asset_file"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$_CURDX_TMPDIR/$_asset_file" "$_download_url"
  else
    echo "ERROR: curl or wget is required for remote install." >&2
    exit 1
  fi

  # Extract
  echo "Extracting..."
  if [[ "$_os" == "windows" ]]; then
    unzip -q "$_CURDX_TMPDIR/$_asset_file" -d "$_CURDX_TMPDIR"
  else
    tar xzf "$_CURDX_TMPDIR/$_asset_file" -C "$_CURDX_TMPDIR"
  fi
  REPO_ROOT="$_CURDX_TMPDIR/$_asset"
  _CURDX_PIPED=1
fi
INSTALL_PREFIX="${CODEX_INSTALL_PREFIX:-$HOME/.local/share/codex-dual}"
BIN_DIR="${CODEX_BIN_DIR:-$HOME/.local/bin}"
readonly REPO_ROOT INSTALL_PREFIX BIN_DIR
HELPER="$BIN_DIR/curdx-installer-helper"

# i18n support
detect_lang() {
  local lang="${CURDX_LANG:-auto}"
  case "$lang" in
    zh|cn|chinese) echo "zh" ;;
    en|english) echo "en" ;;
    *)
      local sys_lang="${LANG:-${LC_ALL:-${LC_MESSAGES:-}}}"
      if [[ "$sys_lang" == zh* ]] || [[ "$sys_lang" == *chinese* ]]; then
        echo "zh"
      else
        echo "en"
      fi
      ;;
  esac
}

CURDX_LANG_DETECTED="$(detect_lang)"

# Message function
msg() {
  local key="$1"
  shift
  local en_msg zh_msg
  case "$key" in
    install_complete)
      en_msg="Installation complete"
      zh_msg="安装完成" ;;
    uninstall_complete)
      en_msg="Uninstall complete"
      zh_msg="卸载完成" ;;
    python_version_old)
      en_msg="Python version too old: $1"
      zh_msg="Python 版本过旧: $1" ;;
    requires_python)
      en_msg="Requires Python 3.10+"
      zh_msg="需要 Python 3.10+" ;;
    missing_dep)
      en_msg="Missing dependency: $1"
      zh_msg="缺少依赖: $1" ;;
    detected_env)
      en_msg="Detected $1 environment"
      zh_msg="检测到 $1 环境" ;;
    confirm_wsl)
      en_msg="Confirm continue installing in WSL? (y/N)"
      zh_msg="确认继续在 WSL 中安装？(y/N)" ;;
    cancelled)
      en_msg="Installation cancelled"
      zh_msg="安装已取消" ;;
    wsl_warning)
      en_msg="Detected WSL environment"
      zh_msg="检测到 WSL 环境" ;;
    same_env_required)
      en_msg="curdx/cxb-ask/ping/cxb-pend must run in the same environment as codex/gemini."
      zh_msg="curdx/cxb-ask/ping/cxb-pend 必须与 codex/gemini 在同一环境运行。" ;;
    confirm_wsl_native)
      en_msg="Please confirm: you will install and run codex/gemini in WSL (not Windows native)."
      zh_msg="请确认：你将在 WSL 中安装并运行 codex/gemini（不是 Windows 原生）。" ;;
    wezterm_recommended)
      en_msg="Recommend installing WezTerm as terminal frontend"
      zh_msg="推荐安装 WezTerm 作为终端前端" ;;
    watchdog_installing)
      en_msg="Installing Python dependency: watchdog"
      zh_msg="正在安装 Python 依赖: watchdog" ;;
    watchdog_installed)
      en_msg="OK: watchdog installed"
      zh_msg="OK: watchdog 已安装" ;;
    watchdog_failed)
      en_msg="WARN: watchdog install failed (will fall back to polling)"
      zh_msg="警告：watchdog 安装失败（将退回轮询）" ;;
    pip_missing)
      en_msg="WARN: pip not available; please install watchdog manually"
      zh_msg="警告：未找到 pip，请手动安装 watchdog" ;;
    root_error)
      en_msg="ERROR: Do not run as root/sudo. Please run as normal user."
      zh_msg="错误：请勿以 root/sudo 身份运行。请使用普通用户执行。" ;;
    *)
      en_msg="$key"
      zh_msg="$key" ;;
  esac
  if [[ "$CURDX_LANG_DETECTED" == "zh" ]]; then
    echo "$zh_msg"
  else
    echo "$en_msg"
  fi
}

# Check for root/sudo - refuse to run as root
if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
  msg root_error >&2
  exit 1
fi

SCRIPTS_TO_LINK=(
  bin/cxb-codex-ask
  bin/cxb-codex-pend
  bin/cxb-codex-ping
  bin/cxb-gemini-ask
  bin/cxb-gemini-pend
  bin/cxb-gemini-ping
  bin/cxb-opencode-ask
  bin/cxb-opencode-pend
  bin/cxb-opencode-ping
  bin/cxb-llm-ask
  bin/cxb-llm-pend
  bin/cxb-llm-ping
  bin/cxb-ask
  bin/curdx-ping
  bin/cxb-pend
  bin/cxb-autonew
  bin/curdx-completion-hook
  bin/maild
  bin/cxb-ctx-transfer
  curdx
)

CLAUDE_MARKDOWN=(
  # Old CURDX commands removed - replaced by unified ask/ping/pend skills
)

LEGACY_SCRIPTS=(
  ping
  cast
  cast-w
  codex-ask
  codex-pending
  codex-ping
  claude-codex-dual
  claude_codex
  claude_ai
  claude_bridge
  caskd
  gaskd
  oaskd
  laskd
  cask
  gask
  lask
  oask
  cpend
  gpend
  lpend
  opend
  cping
  gping
  lping
  oping
  ask
  pend
  askd
  laskd
  autoloop
  autonew
  ctx-transfer
)

usage() {
  cat <<'USAGE'
Usage:
  ./install.sh install    # Install or update Codex dual-window tools
  ./install.sh uninstall  # Uninstall installed content

Optional environment variables:
  CODEX_INSTALL_PREFIX     Install directory (default: ~/.local/share/codex-dual)
  CODEX_BIN_DIR            Executable directory (default: ~/.local/bin)
  CODEX_CLAUDE_COMMAND_DIR Custom Claude commands directory (default: auto-detect)
  CURDX_CLAUDE_MD_MODE       CLAUDE.md injection mode: "inline" (default) or "route"
                           inline = full config in CLAUDE.md (~57 lines)
                           route  = minimal pointer in CLAUDE.md, full config in ~/.claude/rules/curdx-config.md
USAGE
}

detect_claude_dir() {
  if [[ -n "${CODEX_CLAUDE_COMMAND_DIR:-}" ]]; then
    echo "$CODEX_CLAUDE_COMMAND_DIR"
    return
  fi

  local candidates=(
    "$HOME/.claude/commands"
    "$HOME/.config/claude/commands"
    "$HOME/.local/share/claude/commands"
  )

  for dir in "${candidates[@]}"; do
    if [[ -d "$dir" ]]; then
      echo "$dir"
      return
    fi
  done

  local fallback="$HOME/.claude/commands"
  mkdir -p "$fallback"
  echo "$fallback"
}

require_command() {
  local cmd="$1"
  local pkg="${2:-$1}"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "ERROR: Missing dependency: $cmd"
    echo "   Please install $pkg first, then re-run install.sh"
    exit 1
  fi
}

PYTHON_BIN="${CURDX_PYTHON_BIN:-}"

_python_check_310() {
  local cmd="$1"
  command -v "$cmd" >/dev/null 2>&1 || return 1
  "$cmd" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' >/dev/null 2>&1
}

pick_python_bin() {
  if [[ -n "${PYTHON_BIN}" ]] && _python_check_310 "${PYTHON_BIN}"; then
    return 0
  fi
  for cmd in python3 python; do
    if _python_check_310 "$cmd"; then
      PYTHON_BIN="$cmd"
      return 0
    fi
  done
  return 1
}

pick_any_python_bin() {
  if [[ -n "${PYTHON_BIN}" ]] && command -v "${PYTHON_BIN}" >/dev/null 2>&1; then
    return 0
  fi
  for cmd in python3 python; do
    if command -v "$cmd" >/dev/null 2>&1; then
      PYTHON_BIN="$cmd"
      return 0
    fi
  done
  return 1
}

require_python_version() {
  # curdx requires Python 3.10+ (PEP 604 type unions: `str | None`, etc.)
  if ! pick_python_bin; then
    echo "ERROR: Missing dependency: python (3.10+ required)"
    echo "   Please install Python 3.10+ and ensure it is on PATH, then re-run install.sh"
    exit 1
  fi
  local version
  version="$("$PYTHON_BIN" -c 'import sys; print("{}.{}.{}".format(sys.version_info[0], sys.version_info[1], sys.version_info[2]))' 2>/dev/null || echo unknown)"
  if ! _python_check_310 "$PYTHON_BIN"; then
    echo "ERROR: Python version too old: $version"
    echo "   Requires Python 3.10+, please upgrade and retry"
    exit 1
  fi
  echo "OK: Python $version ($PYTHON_BIN)"
}

python_has_module() {
  local module="$1"
  if ! pick_any_python_bin; then
    return 1
  fi
  "$PYTHON_BIN" - <<PY >/dev/null 2>&1
import importlib.util
import sys
sys.exit(0 if importlib.util.find_spec("${module}") else 1)
PY
}

install_watchdog() {
  if python_has_module "watchdog"; then
    msg watchdog_installed
    return 0
  fi
  msg watchdog_installing

  # 1. Try uv (fast, no PEP 668 issues)
  if command -v uv >/dev/null 2>&1; then
    if uv pip install --system "watchdog>=2.1.0" >/dev/null 2>&1 || \
       uv pip install "watchdog>=2.1.0" >/dev/null 2>&1; then
      if python_has_module "watchdog"; then
        msg watchdog_installed
        return 0
      fi
    fi
  fi

  if ! "$PYTHON_BIN" -m pip --version >/dev/null 2>&1; then
    msg pip_missing
    return 1
  fi

  # 2. Try standard pip install --user
  if "$PYTHON_BIN" -m pip install --user "watchdog>=2.1.0" >/dev/null 2>&1; then
    if python_has_module "watchdog"; then
      msg watchdog_installed
      return 0
    fi
  fi

  # 3. PEP 668 fallback: --break-system-packages (Homebrew Python, Debian 12+, etc.)
  if "$PYTHON_BIN" -m pip install --user --break-system-packages "watchdog>=2.1.0" >/dev/null 2>&1; then
    if python_has_module "watchdog"; then
      msg watchdog_installed
      return 0
    fi
  fi

  # 4. Try pipx inject into a shared venv as last resort
  if command -v pipx >/dev/null 2>&1; then
    if pipx install watchdog >/dev/null 2>&1; then
      if python_has_module "watchdog"; then
        msg watchdog_installed
        return 0
      fi
    fi
  fi

  msg watchdog_failed
  return 1
}

# Return linux / macos / unknown based on uname
detect_platform() {
  local name
  name="$(uname -s 2>/dev/null || echo unknown)"
  case "$name" in
    Linux) echo "linux" ;;
    Darwin) echo "macos" ;;
    *) echo "unknown" ;;
  esac
}


is_wsl() {
  [[ -f /proc/version ]] && grep -qi microsoft /proc/version 2>/dev/null
}

get_wsl_version() {
  if [[ -n "${WSL_INTEROP:-}" ]]; then
    echo 2
  else
    echo 1
  fi
}

check_wsl_compatibility() {
  if is_wsl; then
    local ver
    ver="$(get_wsl_version)"
    echo "OK: Detected WSL $ver environment"
  fi
}

confirm_backend_env_wsl() {
  if ! is_wsl; then
    return
  fi

  if [[ "${CURDX_INSTALL_ASSUME_YES:-}" == "1" ]]; then
    return
  fi

  if [[ ! -t 0 ]]; then
    echo "ERROR: Installing in WSL but detected non-interactive terminal; aborted to avoid env mismatch."
    echo "   If you confirm codex/gemini will be installed and run in WSL:"
    echo "   Re-run: CURDX_INSTALL_ASSUME_YES=1 ./install.sh install"
    exit 1
  fi

  echo
  echo "================================================================"
  echo "WARN: Detected WSL environment"
  echo "================================================================"
  echo "curdx/cxb-ask/ping/cxb-pend must run in the same environment as codex/gemini."
  echo
  echo "Please confirm: you will install and run codex/gemini in WSL (not Windows native)."
  echo "If you plan to run codex/gemini in Windows native, exit and run on Windows side:"
  echo "   powershell -ExecutionPolicy Bypass -File .\\install.ps1 install"
  echo "================================================================"
  echo
  read -r -p "Confirm continue installing in WSL? (y/N): " reply
  case "$reply" in
    y|Y|yes|YES) ;;
    *) echo "Installation cancelled"; exit 1 ;;
  esac
}

print_tmux_install_hint() {
  local platform
  platform="$(detect_platform)"
  case "$platform" in
    macos)
      if command -v brew >/dev/null 2>&1; then
        echo "   macOS: Run 'brew install tmux'"
      else
        echo "   macOS: Homebrew not detected, install from https://brew.sh then run 'brew install tmux'"
      fi
      ;;
    linux)
      if command -v apt-get >/dev/null 2>&1; then
        echo "   Debian/Ubuntu: sudo apt-get update && sudo apt-get install -y tmux"
      elif command -v dnf >/dev/null 2>&1; then
        echo "   Fedora/CentOS/RHEL: sudo dnf install -y tmux"
      elif command -v yum >/dev/null 2>&1; then
        echo "   CentOS/RHEL: sudo yum install -y tmux"
      elif command -v pacman >/dev/null 2>&1; then
        echo "   Arch/Manjaro: sudo pacman -S tmux"
      elif command -v apk >/dev/null 2>&1; then
        echo "   Alpine: sudo apk add tmux"
      elif command -v zypper >/dev/null 2>&1; then
        echo "   openSUSE: sudo zypper install -y tmux"
      else
        echo "   Linux: Please use your distro's package manager to install tmux"
      fi
      ;;
    *)
      echo "   See https://github.com/tmux/tmux/wiki/Installing for tmux installation"
      ;;
  esac
}

require_terminal_backend() {
  local wezterm_override="${CODEX_WEZTERM_BIN:-${WEZTERM_BIN:-}}"

  # ============================================
  # Prioritize detecting current environment
  # ============================================

  # 1. If running in WezTerm environment
  if [[ -n "${WEZTERM_PANE:-}" ]]; then
    if [[ -n "${wezterm_override}" ]] && { command -v "${wezterm_override}" >/dev/null 2>&1 || [[ -f "${wezterm_override}" ]]; }; then
      echo "OK: Detected WezTerm environment (${wezterm_override})"
      return
    fi
    if command -v wezterm >/dev/null 2>&1 || command -v wezterm.exe >/dev/null 2>&1; then
      echo "OK: Detected WezTerm environment"
      return
    fi
  fi

  # 2. If running in tmux environment
  if [[ -n "${TMUX:-}" ]]; then
    echo "OK: Detected tmux environment"
    return
  fi

  # ============================================
  # Not in specific environment, detect by availability
  # ============================================

  # 3. Check WezTerm environment variable override
  if [[ -n "${wezterm_override}" ]]; then
    if command -v "${wezterm_override}" >/dev/null 2>&1 || [[ -f "${wezterm_override}" ]]; then
      echo "OK: Detected WezTerm (${wezterm_override})"
      return
    fi
  fi

  # 4. Check WezTerm command
  if command -v wezterm >/dev/null 2>&1 || command -v wezterm.exe >/dev/null 2>&1; then
    echo "OK: Detected WezTerm"
    return
  fi

  # WSL: Windows PATH may not be injected, try common install paths
  if [[ -f "/proc/version" ]] && grep -qi microsoft /proc/version 2>/dev/null; then
    if [[ -x "/mnt/c/Program Files/WezTerm/wezterm.exe" ]] || [[ -f "/mnt/c/Program Files/WezTerm/wezterm.exe" ]]; then
      echo "OK: Detected WezTerm (/mnt/c/Program Files/WezTerm/wezterm.exe)"
      return
    fi
    if [[ -x "/mnt/c/Program Files (x86)/WezTerm/wezterm.exe" ]] || [[ -f "/mnt/c/Program Files (x86)/WezTerm/wezterm.exe" ]]; then
      echo "OK: Detected WezTerm (/mnt/c/Program Files (x86)/WezTerm/wezterm.exe)"
      return
    fi
  fi

  # 5. Check tmux
  if command -v tmux >/dev/null 2>&1; then
    echo "OK: Detected tmux (recommend also installing WezTerm for better experience)"
    return
  fi

  # 6. No terminal multiplexer found
  echo "ERROR: Missing dependency: WezTerm or tmux (at least one required)"
  echo "   WezTerm website: https://wezfurlong.org/wezterm/"

  if [[ "$(uname)" == "Darwin" ]]; then
    echo
    echo "NOTE: macOS user recommended options:"
    echo "   - Install tmux: brew install tmux"
  fi

  print_tmux_install_hint
  exit 1
}

has_wezterm() {
  local wezterm_override="${CODEX_WEZTERM_BIN:-${WEZTERM_BIN:-}}"
  if [[ -n "${wezterm_override}" ]]; then
    command -v "${wezterm_override}" >/dev/null 2>&1 || [[ -f "${wezterm_override}" ]] && return 0
  fi
  command -v wezterm >/dev/null 2>&1 && return 0
  command -v wezterm.exe >/dev/null 2>&1 && return 0
  if [[ -f "/proc/version" ]] && grep -qi microsoft /proc/version 2>/dev/null; then
    [[ -f "/mnt/c/Program Files/WezTerm/wezterm.exe" ]] && return 0
    [[ -f "/mnt/c/Program Files (x86)/WezTerm/wezterm.exe" ]] && return 0
  fi
  return 1
}

detect_wezterm_path() {
  local wezterm_override="${CODEX_WEZTERM_BIN:-${WEZTERM_BIN:-}}"
  if [[ -n "${wezterm_override}" ]] && [[ -f "${wezterm_override}" ]]; then
    echo "${wezterm_override}"
    return
  fi
  local found
  found="$(command -v wezterm 2>/dev/null)" && [[ -n "$found" ]] && echo "$found" && return
  found="$(command -v wezterm.exe 2>/dev/null)" && [[ -n "$found" ]] && echo "$found" && return
  if is_wsl; then
    for drive in c d e f; do
      for path in "/mnt/${drive}/Program Files/WezTerm/wezterm.exe" \
                  "/mnt/${drive}/Program Files (x86)/WezTerm/wezterm.exe"; do
        if [[ -f "$path" ]]; then
          echo "$path"
          return
        fi
      done
    done
  fi
}

save_wezterm_config() {
  local wezterm_path
  wezterm_path="$(detect_wezterm_path)"
  if [[ -n "$wezterm_path" ]]; then
    local cfg_root="${XDG_CONFIG_HOME:-$HOME/.config}"
    mkdir -p "$cfg_root/curdx"
    echo "CODEX_WEZTERM_BIN=${wezterm_path}" > "$cfg_root/curdx/env"
    echo "OK: WezTerm path cached: $wezterm_path"
  fi
}

build_go_binaries() {
  mkdir -p "$BIN_DIR"

  # If pre-built binaries exist in REPO_ROOT (release tarball), copy them directly
  if [[ -x "$REPO_ROOT/curdx" ]]; then
    echo "Installing pre-built binaries..."
    local installed=0
    for bin in "$REPO_ROOT"/curdx "$REPO_ROOT"/curdx-* "$REPO_ROOT"/cxb-*; do
      [[ -f "$bin" && -x "$bin" ]] || continue
      local name
      name="$(basename "$bin")"
      cp -f "$bin" "$BIN_DIR/$name"
      chmod +x "$BIN_DIR/$name"
      installed=$((installed + 1))
    done
    echo "  Installed $installed pre-built binaries"
    return
  fi

  # Otherwise, build from source
  if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: Go compiler required. Install from https://go.dev/dl/"
    exit 1
  fi

  echo "Building Go binaries..."
  mkdir -p "$BIN_DIR"

  # Resolve version info for ldflags injection
  local go_version go_commit go_date go_ldflags
  go_version=""
  go_commit=""
  go_date=""
  if [[ -d "$REPO_ROOT/.git" ]] && command -v git >/dev/null 2>&1; then
    go_commit="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || true)"
    go_date="$(git -C "$REPO_ROOT" log -1 --format=%ci 2>/dev/null | cut -d' ' -f1 || true)"
    # Derive version from git tag
    go_version="$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || true)"
  fi

  go_ldflags=""
  local pkg="github.com/curdx/curdx-bridge/cmd/curdx"
  if [[ -n "$go_version" ]]; then
    go_ldflags="$go_ldflags -X main.Version=$go_version"
  fi
  if [[ -n "$go_commit" ]]; then
    go_ldflags="$go_ldflags -X main.GitCommit=$go_commit"
  fi
  if [[ -n "$go_date" ]]; then
    go_ldflags="$go_ldflags -X main.GitDate=$go_date"
  fi

  local built=0
  for target in "$REPO_ROOT"/cmd/*/; do
    local name
    name="$(basename "$target")"
    local build_ok=false
    # Only inject ldflags for the curdx binary (others don't have these vars)
    if [[ "$name" == "curdx" && -n "$go_ldflags" ]]; then
      if (cd "$REPO_ROOT" && go build -ldflags "$go_ldflags" -o "$BIN_DIR/$name" "./cmd/$name") 2>/dev/null; then
        build_ok=true
      fi
    else
      if (cd "$REPO_ROOT" && go build -o "$BIN_DIR/$name" "./cmd/$name") 2>/dev/null; then
        build_ok=true
      fi
    fi
    if $build_ok; then
      built=$((built + 1))
    else
      echo "WARN: Failed to build $name"
    fi
  done
  echo "  Built $built Go binaries"
}

copy_project() {
  local staging
  staging="$(mktemp -d)"
  trap 'rm -rf "$staging"' EXIT

  if command -v rsync >/dev/null 2>&1; then
    rsync -a \
      --exclude '.git/' \
      --exclude '__pycache__/' \
      --exclude '.pytest_cache/' \
      --exclude '.mypy_cache/' \
      --exclude '.venv/' \
      --exclude 'lib/web/' \
      --exclude 'bin/curdx-web' \
      "$REPO_ROOT"/ "$staging"/
  else
    tar -C "$REPO_ROOT" \
      --exclude '.git' \
      --exclude '__pycache__' \
      --exclude '.pytest_cache' \
      --exclude '.mypy_cache' \
      --exclude '.venv' \
      --exclude 'lib/web' \
      --exclude 'bin/curdx-web' \
      -cf - . | tar -C "$staging" -xf -
  fi

  rm -rf "$INSTALL_PREFIX"
  mkdir -p "$(dirname "$INSTALL_PREFIX")"
  mv "$staging" "$INSTALL_PREFIX"
  trap - EXIT

  # Update GIT_COMMIT and GIT_DATE in curdx file
  local git_commit="" git_date=""

  # Method 1: From git repo
  if command -v git >/dev/null 2>&1 && [[ -d "$REPO_ROOT/.git" ]]; then
    git_commit=$(git -C "$REPO_ROOT" log -1 --format='%h' 2>/dev/null || echo "")
    git_date=$(git -C "$REPO_ROOT" log -1 --format='%cs' 2>/dev/null || echo "")
  fi

  # Method 2: From environment variables (set by curdx update)
  if [[ -z "$git_commit" && -n "${CURDX_GIT_COMMIT:-}" ]]; then
    git_commit="$CURDX_GIT_COMMIT"
    git_date="${CURDX_GIT_DATE:-}"
  fi

  # Method 3: From GitHub API (fallback)
  if [[ -z "$git_commit" ]] && command -v curl >/dev/null 2>&1; then
    local api_response
    api_response=$(curl -fsSL "https://api.github.com/repos/bfly123/claude_code_bridge/commits/main" 2>/dev/null || echo "")
    if [[ -n "$api_response" ]]; then
      git_commit=$(echo "$api_response" | grep -o '"sha": "[^"]*"' | head -1 | cut -d'"' -f4 | cut -c1-7)
      git_date=$(echo "$api_response" | grep -o '"date": "[^"]*"' | head -1 | cut -d'"' -f4 | cut -c1-10)
    fi
  fi

  if [[ -n "$git_commit" && -f "$INSTALL_PREFIX/curdx" ]]; then
    # Validate git_commit and git_date to prevent sed injection
    if [[ ! "$git_commit" =~ ^[0-9a-fA-F]+$ ]]; then
      echo "WARN: invalid git_commit format, skipping version stamp"
      return
    fi
    if [[ -n "$git_date" && ! "$git_date" =~ ^[0-9T:Z.+-]+$ ]]; then
      echo "WARN: invalid git_date format, skipping version stamp"
      return
    fi
    sed -i.bak "s/^GIT_COMMIT = .*/GIT_COMMIT = \"$git_commit\"/" "$INSTALL_PREFIX/curdx"
    sed -i.bak "s/^GIT_DATE = .*/GIT_DATE = \"$git_date\"/" "$INSTALL_PREFIX/curdx"
    rm -f "$INSTALL_PREFIX/curdx.bak"
  fi
}

install_bin_links() {
  mkdir -p "$BIN_DIR"

  for path in "${SCRIPTS_TO_LINK[@]}"; do
    local name
    name="$(basename "$path")"
    if [[ ! -f "$INSTALL_PREFIX/$path" ]]; then
      echo "WARN: Script not found $INSTALL_PREFIX/$path, skipping link creation"
      continue
    fi
    chmod +x "$INSTALL_PREFIX/$path"
    if ln -sf "$INSTALL_PREFIX/$path" "$BIN_DIR/$name" 2>/dev/null; then
      :
    else
      # Windows (Git Bash) / restricted environments may not allow symlinks. Fall back to copying.
      cp -f "$INSTALL_PREFIX/$path" "$BIN_DIR/$name"
      chmod +x "$BIN_DIR/$name" 2>/dev/null || true
    fi
  done

  for legacy in "${LEGACY_SCRIPTS[@]}"; do
    rm -f "$BIN_DIR/$legacy"
  done

  echo "Created executable links in $BIN_DIR"
}

ensure_path_configured() {
  # Check if BIN_DIR is already in PATH
  if [[ ":$PATH:" == *":$BIN_DIR:"* ]]; then
    return
  fi

  local shell_rc=""
  local current_shell
  current_shell="$(basename "${SHELL:-/bin/bash}")"

  case "$current_shell" in
    zsh)  shell_rc="$HOME/.zshrc" ;;
    bash)
      if [[ -f "$HOME/.bash_profile" ]]; then
        shell_rc="$HOME/.bash_profile"
      else
        shell_rc="$HOME/.bashrc"
      fi
      ;;
    *)    shell_rc="$HOME/.profile" ;;
  esac

  local path_line="export PATH=\"${BIN_DIR}:\$PATH\""

  # Check if already configured in shell rc
  if [[ -f "$shell_rc" ]] && grep -qF "$BIN_DIR" "$shell_rc" 2>/dev/null; then
    echo "PATH already configured in $shell_rc (restart terminal to apply)"
    return
  fi

  # Add to shell rc
  echo "" >> "$shell_rc"
  echo "# Added by curdx installer" >> "$shell_rc"
  echo "$path_line" >> "$shell_rc"
  echo "OK: Added $BIN_DIR to PATH in $shell_rc"
  echo "   Run: source $shell_rc  (or restart terminal)"
}

install_claude_commands() {
  local claude_dir
  claude_dir="$(detect_claude_dir)"
  mkdir -p "$claude_dir"

  # Clean up obsolete CURDX commands (replaced by unified ask/ping/pend)
  local obsolete_cmds="cask.md gask.md oask.md lask.md cpend.md gpend.md opend.md lpend.md cping.md gping.md oping.md lping.md"
  for obs_cmd in $obsolete_cmds; do
    if [[ -f "$claude_dir/$obs_cmd" ]]; then
      rm -f "$claude_dir/$obs_cmd"
      echo "  Removed obsolete command: $obs_cmd"
    fi
  done

  for doc in "${CLAUDE_MARKDOWN[@]+"${CLAUDE_MARKDOWN[@]}"}"; do
    cp -f "$REPO_ROOT/commands/$doc" "$claude_dir/$doc"
    chmod 0644 "$claude_dir/$doc" 2>/dev/null || true
  done

  echo "Updated Claude commands directory: $claude_dir"
}

install_claude_skills() {
  local skills_src="$REPO_ROOT/claude_skills"
  local skills_dst="$HOME/.claude/skills"

  if [[ ! -d "$skills_src" ]]; then
    return
  fi

  mkdir -p "$skills_dst"

  # Clean up obsolete CURDX skills (replaced by unified ask/cping/pend)
  local obsolete_skills="cask gask oask lask cpend gpend opend lpend cping gping oping lping ping auto tp tr all-plan ask pend autonew file-op mounted continue review"
  for obs_skill in $obsolete_skills; do
    if [[ -d "$skills_dst/$obs_skill" ]]; then
      rm -rf "$skills_dst/$obs_skill"
      echo "  Removed obsolete skill: $obs_skill"
    fi
  done

  echo "Installing Claude skills (bash SKILL.md templates)..."
  for skill_dir in "$skills_src"/*/; do
    [[ -d "$skill_dir" ]] || continue
    local skill_name
    skill_name=$(basename "$skill_dir")
    [[ "$skill_name" == "docs" ]] && continue

    local src_skill_md=""
    if [[ -f "$skill_dir/SKILL.md.bash" ]]; then
      src_skill_md="$skill_dir/SKILL.md.bash"
    elif [[ -f "$skill_dir/SKILL.md" ]]; then
      src_skill_md="$skill_dir/SKILL.md"
    else
      continue
    fi

    local dst_dir="$skills_dst/$skill_name"
    local dst_skill_md="$dst_dir/SKILL.md"
    mkdir -p "$dst_dir"
    cp -f "$src_skill_md" "$dst_skill_md"

    # Copy additional subdirectories (e.g., references/) if they exist
    for subdir in "$skill_dir"*/; do
      if [[ -d "$subdir" ]]; then
        local subdir_name
        subdir_name=$(basename "$subdir")
        cp -rf "$subdir" "$dst_dir/$subdir_name"
      fi
    done

    echo "  Updated skill: $skill_name"
  done

  # Shared docs live at skills/docs but are not a "skill directory". Install them as well.
  if [[ -d "$skills_src/docs" ]]; then
    rm -rf "$skills_dst/docs"
    cp -r "$skills_src/docs" "$skills_dst/docs"
    echo "  Installed skills docs: docs/"
  fi

  # Make autoloop scripts executable
  local autoloop_sh="$skills_dst/cxb-task-run/scripts/autoloop.sh"
  [[ -f "$autoloop_sh" ]] && chmod +x "$autoloop_sh"

  echo "Updated Claude skills directory: $skills_dst"
}

install_codex_skills() {
  local skills_src="$REPO_ROOT/codex_skills"
  local skills_dst="${CODEX_HOME:-$HOME/.codex}/skills"

  if [[ ! -d "$skills_src" ]]; then
    return
  fi

  mkdir -p "$skills_dst"

  # Clean up obsolete CURDX skills (replaced by unified ask/ping/pend)
  local obsolete_skills="cask gask oask lask cpend gpend opend lpend cping gping oping lping all-plan ask pend ping file-op mounted"
  for obs_skill in $obsolete_skills; do
    if [[ -d "$skills_dst/$obs_skill" ]]; then
      rm -rf "$skills_dst/$obs_skill"
      echo "  Removed obsolete skill: $obs_skill"
    fi
  done

  echo "Installing Codex skills (bash SKILL.md templates)..."
  for skill_dir in "$skills_src"/*/; do
    [[ -d "$skill_dir" ]] || continue
    local skill_name
    skill_name=$(basename "$skill_dir")

    local src_skill_md=""
    if [[ -f "$skill_dir/SKILL.md.bash" ]]; then
      src_skill_md="$skill_dir/SKILL.md.bash"
    elif [[ -f "$skill_dir/SKILL.md" ]]; then
      src_skill_md="$skill_dir/SKILL.md"
    else
      continue
    fi

    local dst_dir="$skills_dst/$skill_name"
    local dst_skill_md="$dst_dir/SKILL.md"
    mkdir -p "$dst_dir"
    cp -f "$src_skill_md" "$dst_skill_md"

    # Copy additional subdirectories (e.g., references/) if they exist
    for subdir in "$skill_dir"*/; do
      if [[ -d "$subdir" ]]; then
        local subdir_name
        subdir_name=$(basename "$subdir")
        cp -rf "$subdir" "$dst_dir/$subdir_name"
      fi
    done

    echo "  Updated Codex skill: $skill_name"
  done
  echo "Updated Codex skills directory: $skills_dst"
}

CURDX_START_MARKER="<!-- CURDX_CONFIG_START -->"
CURDX_END_MARKER="<!-- CURDX_CONFIG_END -->"
LEGACY_RULE_MARKER="## Codex 协作规则"

remove_codex_mcp() {
  local claude_config="$HOME/.claude.json"

  if [[ ! -f "$claude_config" ]]; then
    return
  fi

  local has_codex_mcp
  has_codex_mcp=$("$HELPER" check-mcp-has-codex "$claude_config" 2>/dev/null)

  if [[ "$has_codex_mcp" == "yes" ]]; then
    echo "WARN: Detected codex-related MCP configuration, removing to avoid conflicts..."
    "$HELPER" remove-codex-mcp "$claude_config"
    echo "OK: Codex MCP configuration cleaned"
  fi
}

install_claude_md_config() {
  local claude_md="$HOME/.claude/CLAUDE.md"
  local md_mode="${CURDX_CLAUDE_MD_MODE:-inline}"
  local full_template="$INSTALL_PREFIX/config/claude-md-curdx.md"
  local route_template="$INSTALL_PREFIX/config/claude-md-curdx-route.md"
  local external_config="$HOME/.claude/rules/curdx-config.md"

  # Select template based on mode
  local template
  if [[ "$md_mode" == "route" ]]; then
    template="$route_template"
  else
    template="$full_template"
  fi

  mkdir -p "$HOME/.claude"

  if [[ ! -f "$template" ]]; then
    echo "WARN: Template not found: $template; skipping CLAUDE.md injection"
    return 1
  fi

  # In route mode, write full config to external file
  if [[ "$md_mode" == "route" ]]; then
    mkdir -p "$HOME/.claude/rules"
    cp "$full_template" "$external_config"
    echo "Wrote full CURDX config to $external_config"
  fi

  if [[ -f "$claude_md" ]]; then
    if grep -q "$CURDX_START_MARKER" "$claude_md" 2>/dev/null; then
      echo "Updating existing CURDX config block (mode: $md_mode)..."
      "$HELPER" replace-block "$claude_md" "$template" "<!-- CURDX_CONFIG_START -->" "<!-- CURDX_CONFIG_END -->"
    elif grep -qE "$LEGACY_RULE_MARKER|## Codex Collaboration Rules|## Gemini|## OpenCode" "$claude_md" 2>/dev/null; then
      echo "Removing legacy rules and adding new CURDX config block..."
      "$HELPER" remove-legacy-md-rules "$claude_md"
      cat "$template" >> "$claude_md"
    else
      echo "" >> "$claude_md"
      cat "$template" >> "$claude_md"
    fi
  else
    cat "$template" > "$claude_md"
  fi

  echo "Updated AI collaboration rules in $claude_md (mode: $md_mode)"
}

CURDX_ROLES_START_MARKER="<!-- CURDX_ROLES_START -->"
CURDX_ROLES_END_MARKER="<!-- CURDX_ROLES_END -->"
CURDX_RUBRICS_START_MARKER="<!-- REVIEW_RUBRICS_START -->"
CURDX_RUBRICS_END_MARKER="<!-- REVIEW_RUBRICS_END -->"

install_agents_md_config() {
  local agents_md="$INSTALL_PREFIX/AGENTS.md"
  local template="$INSTALL_PREFIX/config/agents-md-curdx.md"

  if [[ ! -f "$template" ]]; then
    echo "WARN: Template not found: $template; skipping AGENTS.md injection"
    return 1
  fi

  if [[ -f "$agents_md" ]]; then
    # Replace existing CURDX blocks if present
    local updated=false
    if grep -q "$CURDX_ROLES_START_MARKER" "$agents_md" 2>/dev/null || \
       grep -q "$CURDX_RUBRICS_START_MARKER" "$agents_md" 2>/dev/null; then
      echo "Updating existing CURDX blocks in AGENTS.md..."
      "$HELPER" replace-block "$agents_md" "$template" "<!-- CURDX_ROLES_START -->" "<!-- CURDX_ROLES_END -->"
      "$HELPER" replace-block "$agents_md" "$template" "<!-- REVIEW_RUBRICS_START -->" "<!-- REVIEW_RUBRICS_END -->"
      updated=true
    fi
    if ! $updated; then
      echo "" >> "$agents_md"
      cat "$template" >> "$agents_md"
    fi
  else
    cat "$template" > "$agents_md"
  fi

  echo "Updated AGENTS.md: $agents_md"
}

install_clinerules_config() {
  local clinerules="$INSTALL_PREFIX/.clinerules"
  local template="$INSTALL_PREFIX/config/clinerules-curdx.md"

  if [[ ! -f "$template" ]]; then
    echo "WARN: Template not found: $template; skipping .clinerules injection"
    return 1
  fi

  if [[ -f "$clinerules" ]]; then
    if grep -q "$CURDX_ROLES_START_MARKER" "$clinerules" 2>/dev/null; then
      echo "Updating existing CURDX roles block in .clinerules..."
      "$HELPER" replace-block "$clinerules" "$template" "<!-- CURDX_ROLES_START -->" "<!-- CURDX_ROLES_END -->"
    else
      echo "" >> "$clinerules"
      cat "$template" >> "$clinerules"
    fi
  else
    cat "$template" > "$clinerules"
  fi

  echo "Updated .clinerules: $clinerules"
}

install_settings_permissions() {
  local settings_file="$HOME/.claude/settings.json"
  mkdir -p "$HOME/.claude"

  local perms_to_add=(
    'Bash(cxb-ask *)'
    'Bash(curdx-ping *)'
    'Bash(cxb-pend *)'
  )

  if [[ ! -f "$settings_file" ]]; then
    cat > "$settings_file" << 'SETTINGS'
{
	  "permissions": {
	    "allow": [
	      "Bash(cxb-ask *)",
	      "Bash(curdx-ping *)",
	      "Bash(cxb-pend *)"
	    ],
    "deny": []
  }
}
SETTINGS
    echo "Created $settings_file with permissions"
    return
  fi

  # Remove legacy permissions from previous versions
  local perms_to_remove=(
    'Bash(ping *)'
    'Bash(ask *)'
    'Bash(pend *)'
  )
  for old_perm in "${perms_to_remove[@]}"; do
    if grep -q "$old_perm" "$settings_file" 2>/dev/null; then
      "$HELPER" settings-remove-permission "$settings_file" "$old_perm"
      echo "  Removed legacy permission: $old_perm"
    fi
  done

  local added=0
  for perm in "${perms_to_add[@]}"; do
    if ! grep -q "$perm" "$settings_file" 2>/dev/null; then
      "$HELPER" settings-add-permission "$settings_file" "$perm"
      added=1
    fi
  done

  if [[ $added -eq 1 ]]; then
    echo "Updated $settings_file permissions"
  else
    echo "Permissions already exist in $settings_file"
  fi
}

CURDX_TMUX_MARKER="# CURDX (CURDX Bridge) tmux configuration"
CURDX_TMUX_MARKER_LEGACY="# CURDX tmux configuration"

remove_curdx_tmux_block_from_file() {
  local target_conf="$1"

  if [[ ! -f "$target_conf" ]]; then
    return 0
  fi

  if ! grep -q "$CURDX_TMUX_MARKER" "$target_conf" 2>/dev/null && \
     ! grep -q "$CURDX_TMUX_MARKER_LEGACY" "$target_conf" 2>/dev/null; then
    return 0
  fi

  "$HELPER" remove-tmux-block "$target_conf"
}

install_tmux_config() {
  local tmux_conf_main="$HOME/.tmux.conf"
  local tmux_conf_local="$HOME/.tmux.conf.local"
  local tmux_conf="$tmux_conf_main"
  local reload_conf="$tmux_conf_main"
  local curdx_tmux_conf="$REPO_ROOT/config/tmux-curdx.conf"
  local curdx_status_script="$REPO_ROOT/config/curdx-status.sh"
  local status_install_path="$BIN_DIR/curdx-status.sh"

  if [[ ! -f "$curdx_tmux_conf" ]]; then
    return
  fi

  mkdir -p "$BIN_DIR"

  # Install curdx-status.sh script
  if [[ -f "$curdx_status_script" ]]; then
    cp "$curdx_status_script" "$status_install_path"
    chmod +x "$status_install_path"
    echo "Installed: $status_install_path"
  fi

  # Install curdx-border.sh script (dynamic pane border colors)
  local curdx_border_script="$REPO_ROOT/config/curdx-border.sh"
  local border_install_path="$BIN_DIR/curdx-border.sh"
  if [[ -f "$curdx_border_script" ]]; then
    cp "$curdx_border_script" "$border_install_path"
    chmod +x "$border_install_path"
    echo "Installed: $border_install_path"
  fi

  # Install curdx-git.sh script (cached git status for tmux status line)
  local curdx_git_script="$REPO_ROOT/config/curdx-git.sh"
  local git_install_path="$BIN_DIR/curdx-git.sh"
  if [[ -f "$curdx_git_script" ]]; then
    cp "$curdx_git_script" "$git_install_path"
    chmod +x "$git_install_path"
    echo "Installed: $git_install_path"
  fi

  # Install tmux UI toggle scripts (enable/disable CURDX theming per-session)
  local curdx_tmux_on_script="$REPO_ROOT/config/curdx-tmux-on.sh"
  local curdx_tmux_off_script="$REPO_ROOT/config/curdx-tmux-off.sh"
  if [[ -f "$curdx_tmux_on_script" ]]; then
    cp "$curdx_tmux_on_script" "$BIN_DIR/curdx-tmux-on.sh"
    chmod +x "$BIN_DIR/curdx-tmux-on.sh"
    echo "Installed: $BIN_DIR/curdx-tmux-on.sh"
  fi
  if [[ -f "$curdx_tmux_off_script" ]]; then
    cp "$curdx_tmux_off_script" "$BIN_DIR/curdx-tmux-off.sh"
    chmod +x "$BIN_DIR/curdx-tmux-off.sh"
    echo "Installed: $BIN_DIR/curdx-tmux-off.sh"
  fi

  # Oh-My-Tmux keeps user customizations in ~/.tmux.conf.local.
  # Appending to ~/.tmux.conf can break its internal _apply_configuration script.
  if [[ -f "$tmux_conf_main" ]] && grep -q 'TMUX_CONF_LOCAL' "$tmux_conf_main" 2>/dev/null; then
    tmux_conf="$tmux_conf_local"
    reload_conf="$tmux_conf_main"
    if [[ ! -f "$tmux_conf_local" ]]; then
      touch "$tmux_conf_local"
    fi
  else
    reload_conf="$tmux_conf"
  fi

  # Check if already configured (new or legacy marker) in either main/local config.
  local already_configured=false
  for conf in "$tmux_conf_main" "$tmux_conf_local"; do
    if [[ -f "$conf" ]] && \
      (grep -q "$CURDX_TMUX_MARKER" "$conf" 2>/dev/null || \
       grep -q "$CURDX_TMUX_MARKER_LEGACY" "$conf" 2>/dev/null); then
      already_configured=true
      break
    fi
  done

  if $already_configured; then
    # Update existing config: remove old CURDX block(s) and re-add at target location.
    echo "Updating CURDX tmux configuration..."
    remove_curdx_tmux_block_from_file "$tmux_conf_main" || true
    remove_curdx_tmux_block_from_file "$tmux_conf_local" || true
  else
    # Backup existing config if present
    if [[ -f "$tmux_conf" ]]; then
      cp "$tmux_conf" "$tmux_conf.bak.$(date +%Y%m%d%H%M%S)"
    fi
  fi

  # Append CURDX tmux config (fill in BIN_DIR placeholders)
  {
    echo ""
    "$HELPER" file-replace "$curdx_tmux_conf" "@CURDX_BIN_DIR@" "$BIN_DIR" 2>/dev/null || cat "$curdx_tmux_conf"
  } >> "$tmux_conf"

  echo "Updated tmux configuration: $tmux_conf"
  echo "   - CURDX tmux integration (copy mode, mouse, pane management)"
  echo "   - CURDX theme is enabled only while CURDX is running (auto restore on exit)"
  echo "   - Vi-style pane management with h/j/k/l"
  echo "   - Mouse support and better copy mode"
  echo "   - Run 'tmux source $reload_conf' to apply (or restart tmux)"

  # Best-effort: if a tmux server is already running, reload config automatically.
  # (Avoid spawning a new server when tmux isn't running.)
  if command -v tmux >/dev/null 2>&1; then
    if tmux list-sessions >/dev/null 2>&1; then
      if tmux source-file "$reload_conf" >/dev/null 2>&1; then
        echo "Reloaded tmux configuration in running server."
      else
        echo "WARN: Failed to reload tmux configuration automatically; run: tmux source $reload_conf"
      fi
    fi
  fi
}

uninstall_tmux_config() {
  local tmux_conf_main="$HOME/.tmux.conf"
  local tmux_conf_local="$HOME/.tmux.conf.local"
  local status_script="$BIN_DIR/curdx-status.sh"
  local border_script="$BIN_DIR/curdx-border.sh"
  local tmux_on_script="$BIN_DIR/curdx-tmux-on.sh"
  local tmux_off_script="$BIN_DIR/curdx-tmux-off.sh"

  # Remove curdx-status.sh script
  if [[ -f "$status_script" ]]; then
    rm -f "$status_script"
    echo "Removed: $status_script"
  fi

  # Remove curdx-border.sh script
  if [[ -f "$border_script" ]]; then
    rm -f "$border_script"
    echo "Removed: $border_script"
  fi

  # Remove tmux UI toggle scripts
  if [[ -f "$tmux_on_script" ]]; then
    rm -f "$tmux_on_script"
    echo "Removed: $tmux_on_script"
  fi
  if [[ -f "$tmux_off_script" ]]; then
    rm -f "$tmux_off_script"
    echo "Removed: $tmux_off_script"
  fi

  local removed_any=false
  for conf in "$tmux_conf_main" "$tmux_conf_local"; do
    if [[ -f "$conf" ]] && \
      (grep -q "$CURDX_TMUX_MARKER" "$conf" 2>/dev/null || \
       grep -q "$CURDX_TMUX_MARKER_LEGACY" "$conf" 2>/dev/null); then
      echo "Removing CURDX tmux configuration from $conf..."
      if remove_curdx_tmux_block_from_file "$conf"; then
        echo "Removed CURDX tmux configuration from $conf"
        removed_any=true
      fi
    fi
  done

  if ! $removed_any; then
    return
  fi
}

install_requirements() {
  check_wsl_compatibility
  confirm_backend_env_wsl
  require_terminal_backend
  if ! has_wezterm; then
    echo
    echo "================================================================"
    echo "NOTE: Recommend installing WezTerm as terminal frontend (better experience, recommended for WSL2/Windows)"
    echo "   - Website: https://wezfurlong.org/wezterm/"
    echo "   - Benefits: Smoother split/scroll/font rendering, more stable bridging in WezTerm mode"
    echo "================================================================"
    echo
  fi
}

# Clean up legacy daemon files (replaced by unified askd)
cleanup_legacy_files() {
  echo "Cleaning up legacy files..."
  local cleaned=0

  # Legacy daemon scripts in bin/
  local legacy_daemons="caskd gaskd oaskd laskd askd laskd"
  for daemon in $legacy_daemons; do
    if [[ -f "$BIN_DIR/$daemon" ]]; then
      rm -f "$BIN_DIR/$daemon"
      echo "  Removed legacy daemon script: $BIN_DIR/$daemon"
      cleaned=$((cleaned + 1))
    fi
    # Also check install prefix bin
    if [[ -f "$INSTALL_PREFIX/bin/$daemon" ]]; then
      rm -f "$INSTALL_PREFIX/bin/$daemon"
      echo "  Removed legacy daemon script: $INSTALL_PREFIX/bin/$daemon"
      cleaned=$((cleaned + 1))
    fi
  done

  # Legacy daemon state files in ~/.cache/curdx/
  local cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/curdx"
  local legacy_states="caskd.json gaskd.json oaskd.json laskd.json"
  for state in $legacy_states; do
    if [[ -f "$cache_dir/$state" ]]; then
      rm -f "$cache_dir/$state"
      echo "  Removed legacy state file: $cache_dir/$state"
      cleaned=$((cleaned + 1))
    fi
  done

  # Legacy daemon module files in lib/
  local legacy_modules="caskd_daemon.py gaskd_daemon.py oaskd_daemon.py laskd_daemon.py"
  for module in $legacy_modules; do
    if [[ -f "$INSTALL_PREFIX/lib/$module" ]]; then
      rm -f "$INSTALL_PREFIX/lib/$module"
      echo "  Removed legacy module: $INSTALL_PREFIX/lib/$module"
      cleaned=$((cleaned + 1))
    fi
  done

  if [[ $cleaned -eq 0 ]]; then
    echo "  No legacy files found"
  else
    echo "  Cleaned up $cleaned legacy file(s)"
  fi
}

install_all() {
  build_go_binaries
  install_requirements
  remove_codex_mcp
  cleanup_legacy_files
  save_wezterm_config
  copy_project
  install_bin_links
  ensure_path_configured
  install_claude_commands
  install_claude_skills
  install_codex_skills
  install_claude_md_config
  install_agents_md_config
  install_clinerules_config
  install_settings_permissions
  install_tmux_config
  echo "OK: Installation complete"
  echo "   Project dir    : $INSTALL_PREFIX"
  echo "   Executable dir : $BIN_DIR"
  echo "   Claude commands updated"
  local md_mode="${CURDX_CLAUDE_MD_MODE:-inline}"
  if [[ "$md_mode" == "route" ]]; then
    echo "   Global CLAUDE.md configured with CURDX route pointer (full config in ~/.claude/rules/curdx-config.md)"
  else
    echo "   Global CLAUDE.md configured with CURDX collaboration rules (inline)"
  fi
  echo "   AGENTS.md configured with review rubrics"
  echo "   .clinerules configured with role assignments"
  echo "   Global settings.json permissions added"
}

uninstall_claude_md_config() {
  local claude_md="$HOME/.claude/CLAUDE.md"

  if [[ ! -f "$claude_md" ]]; then
    return
  fi

  if grep -q "$CURDX_START_MARKER" "$claude_md" 2>/dev/null; then
    echo "Removing CURDX config block from CLAUDE.md..."
    "$HELPER" replace-block "$claude_md" "/dev/null" "<!-- CURDX_CONFIG_START -->" "<!-- CURDX_CONFIG_END -->"
    echo "Removed CURDX config from CLAUDE.md"
  elif grep -qE "$LEGACY_RULE_MARKER|## Codex Collaboration Rules|## Gemini|## OpenCode" "$claude_md" 2>/dev/null; then
    echo "Removing legacy collaboration rules from CLAUDE.md..."
    "$HELPER" remove-legacy-md-rules "$claude_md"
    echo "Removed collaboration rules from CLAUDE.md"
  fi

  # Clean up external config file if it exists (route mode)
  local external_config="$HOME/.claude/rules/curdx-config.md"
  if [[ -f "$external_config" ]]; then
    rm -f "$external_config"
    echo "Removed external CURDX config: $external_config"
  fi
}

uninstall_settings_permissions() {
  local settings_file="$HOME/.claude/settings.json"

  if [[ ! -f "$settings_file" ]]; then
    return
  fi

  local perms_to_remove=(
    'Bash(ask *)'
    'Bash(ping *)'
    'Bash(curdx-ping *)'
    'Bash(pend *)'
    'Bash(cxb-ask *)'
    'Bash(cxb-pend *)'
    'Bash(cask:*)'
    'Bash(cpend)'
    'Bash(cping)'
    'Bash(gask:*)'
    'Bash(gpend)'
    'Bash(gping)'
    'Bash(oask:*)'
    'Bash(opend)'
    'Bash(oping)'
    'Bash(cxb-codex-ask:*)'
    'Bash(cxb-codex-pend)'
    'Bash(cxb-codex-ping)'
    'Bash(cxb-gemini-ask:*)'
    'Bash(cxb-gemini-pend)'
    'Bash(cxb-gemini-ping)'
    'Bash(cxb-opencode-ask:*)'
    'Bash(cxb-opencode-pend)'
    'Bash(cxb-opencode-ping)'
  )

  local has_perms=0
  for perm in "${perms_to_remove[@]}"; do
    if grep -q "$perm" "$settings_file" 2>/dev/null; then
      has_perms=1
      break
    fi
  done

  if [[ $has_perms -eq 1 ]]; then
    echo "Removing permission configuration from settings.json..."
    for perm in "${perms_to_remove[@]}"; do
      "$HELPER" settings-remove-permission "$settings_file" "$perm" 2>/dev/null
    done
    echo "Removed permission configuration from settings.json"
  fi
}

uninstall_claude_skills() {
  local skills_dst="$HOME/.claude/skills"
  local curdx_skills="ask cping ping pend autonew mounted all-plan docs tp tr file-op review cxb-ask cxb-reply cxb-fresh cxb-mounted cxb-plan cxb-task-plan cxb-task-run cxb-file-op cxb-review continue cxb-continue"

  if [[ ! -d "$skills_dst" ]]; then
    return
  fi

  echo "Removing CURDX Claude skills..."
  for skill in $curdx_skills; do
    if [[ -d "$skills_dst/$skill" ]]; then
      rm -rf "$skills_dst/$skill"
      echo "  Removed skill: $skill"
    fi
  done
}

uninstall_codex_skills() {
  local skills_dst="${CODEX_HOME:-$HOME/.codex}/skills"
  local curdx_skills="ask ping pend autonew mounted all-plan file-op cxb-ask cxb-reply cxb-fresh cxb-mounted cxb-plan cxb-file-op"

  if [[ ! -d "$skills_dst" ]]; then
    return
  fi

  echo "Removing CURDX Codex skills..."
  for skill in $curdx_skills; do
    if [[ -d "$skills_dst/$skill" ]]; then
      rm -rf "$skills_dst/$skill"
      echo "  Removed skill: $skill"
    fi
  done
}

uninstall_all() {
  echo "INFO: Starting curdx uninstall..."

  # 1. Remove project directory
  if [[ -d "$INSTALL_PREFIX" ]]; then
    rm -rf "$INSTALL_PREFIX"
    echo "Removed project directory: $INSTALL_PREFIX"
  fi

  # 2. Remove bin links
  for path in "${SCRIPTS_TO_LINK[@]}"; do
    local name
    name="$(basename "$path")"
    if [[ -L "$BIN_DIR/$name" || -f "$BIN_DIR/$name" ]]; then
      rm -f "$BIN_DIR/$name"
    fi
  done
  for legacy in "${LEGACY_SCRIPTS[@]}"; do
    rm -f "$BIN_DIR/$legacy"
  done
  echo "Removed bin links: $BIN_DIR"

  # 3. Remove Claude command files (clean all possible locations)
  local cmd_dirs=(
    "$HOME/.claude/commands"
    "$HOME/.config/claude/commands"
    "$HOME/.local/share/claude/commands"
  )
  for dir in "${cmd_dirs[@]}"; do
    if [[ -d "$dir" ]]; then
      for doc in "${CLAUDE_MARKDOWN[@]+"${CLAUDE_MARKDOWN[@]}"}"; do
        rm -f "$dir/$doc"
      done
      echo "Cleaned commands directory: $dir"
    fi
  done

  # 4. Remove collaboration rules from CLAUDE.md
  uninstall_claude_md_config

  # 5. Remove permission configuration from settings.json
  uninstall_settings_permissions

  # 6. Remove tmux configuration
  uninstall_tmux_config

  # 7. Remove Claude skills
  uninstall_claude_skills

  # 8. Remove Codex skills
  uninstall_codex_skills

  echo "OK: Uninstall complete"
  echo "   NOTE: Dependencies (go, tmux, wezterm) were not removed"
}

main() {
  # When piped (curl | bash), default to "install"
  local action="${1:-}"
  if [[ -z "$action" ]]; then
    if [[ "${_CURDX_PIPED:-0}" == "1" ]]; then
      action="install"
    else
      usage
      exit 1
    fi
  fi

  case "$action" in
    install)
      install_all
      ;;
    uninstall)
      uninstall_all
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
