# Changelog

All notable changes to this project will be documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/) + [Conventional Commits](https://www.conventionalcommits.org/)

---

## [v0.1.4] — 2026-04-12

### Fixed
- `curdx-mounted` now detects the unified `askd` daemon via RPC ping instead of legacy per-provider daemons (`caskd`/`gaskd`/`oaskd`/`laskd`) via `pgrep`

## [v0.1.3] — 2026-04-12

### Changed
- README rewritten with screenshot, SVG layout diagram, and OpenCode provider docs

### Fixed
- Version injected via ldflags and binary renamed to CurdX Bridge

## [v0.1.2] — 2026-04-12

### Added
- **OpenCode provider** — full support with `oask`, `opend`, `oping` commands
- Bilingual README (English + Chinese)

### Fixed
- Detect and kill stuck/zombie curdx processes holding stale locks
- Skip inactive sessions when resolving session files

## [v0.1.1] — 2026-04-11

### Changed
- Rename module path from `anthropics` to `curdx`, complete ccb → curdx migration
- Default layout: Claude left, Codex top-right, Gemini bottom-right; focus returns to anchor pane

### Removed
- Stale compiled binaries and empty mcp directory

## [v0.1.0] — 2026-04-10

### Added
- Cross-platform one-line installers (`install.sh`, `install.ps1`)
- Skills installation and `CLAUDE.md` config injection
- Windows cross-compilation and GitHub Actions release workflow

### Changed
- Strip providers down to **codex, gemini, opencode, claude** (removed droid, copilot, codebuddy, qwen)

### Fixed
- CodexLogReader `extractCWDFromLog` buffer too small for `session_meta`
- Remove remaining `dask` daemon checks

---

# 更新日志

---

## [v0.1.4] — 2026-04-12

### 修复
- `curdx-mounted` 改用 RPC ping 检测统一 `askd` 守护进程，替代通过 `pgrep` 查找已废弃的分体守护进程

## [v0.1.3] — 2026-04-12

### 变更
- 重写 README，加入截图、SVG 布局图和 OpenCode 文档

### 修复
- 构建时通过 ldflags 注入版本号，二进制文件重命名为 CurdX Bridge

## [v0.1.2] — 2026-04-12

### 新增
- **OpenCode 支持** — 完整的 `oask`、`opend`、`oping` 命令
- 双语 README（英文 + 中文）

### 修复
- 检测并清理僵尸进程导致的过期锁
- 解析会话文件时跳过不活跃的会话

## [v0.1.1] — 2026-04-11

### 变更
- 模块路径从 `anthropics` 改为 `curdx`，完成 ccb → curdx 迁移
- 默认布局改为左 Claude、右上 Codex、右下 Gemini，启动后焦点回到主面板

### 移除
- 清理过期的编译产物和空目录

## [v0.1.0] — 2026-04-10

### 新增
- 跨平台一键安装脚本（`install.sh`、`install.ps1`）
- 技能安装与 `CLAUDE.md` 配置注入
- Windows 交叉编译与 GitHub Actions 发布流程

### 变更
- 精简为四个 Provider：codex、gemini、opencode、claude（移除 droid、copilot、codebuddy、qwen）

### 修复
- CodexLogReader 日志缓冲区过小的问题
- 清除残留的 `dask` 守护进程检查

---

[v0.1.4]: https://github.com/curdx/curdx-bridge/compare/v0.1.3...v0.1.4
[v0.1.3]: https://github.com/curdx/curdx-bridge/compare/v0.1.2...v0.1.3
[v0.1.2]: https://github.com/curdx/curdx-bridge/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/curdx/curdx-bridge/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/curdx/curdx-bridge/releases/tag/v0.1.0
