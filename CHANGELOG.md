# Changelog / 更新日志

All notable changes to this project will be documented in this file.
本文件记录项目的所有重要变更。

Format: [Keep a Changelog](https://keepachangelog.com/) + [Conventional Commits](https://www.conventionalcommits.org/)

---

## [Unreleased] / 未发布

### Added / 新增
- Screenshot in README / README 中加入截图

### Changed / 变更
- README rewritten with OpenCode provider and screenshot / 重写 README，新增 OpenCode 和截图

---

## [v0.1.2] — 2026-04-12

### Added / 新增
- **OpenCode provider** — full support with `oask`, `opend`, `oping` commands / **OpenCode 支持** — 完整的 `oask`、`opend`、`oping` 命令
- Bilingual README (English + Chinese) / 双语 README（英文 + 中文）

### Fixed / 修复
- Detect and kill stuck/zombie curdx processes holding stale locks / 检测并清理僵尸进程导致的过期锁
- Skip inactive sessions when resolving session files / 解析会话文件时跳过不活跃的会话
- Version injected via ldflags at build time / 构建时通过 ldflags 注入版本号

---

## [v0.1.1] — 2026-04-11

### Changed / 变更
- Rename module path from `anthropics` to `curdx`, complete ccb → curdx migration / 模块路径从 `anthropics` 改为 `curdx`，完成 ccb → curdx 迁移
- Default layout: Claude left, Codex top-right, Gemini bottom-right; focus returns to anchor pane / 默认布局改为左 Claude 右上 Codex 右下 Gemini，启动后焦点回到主面板

### Removed / 移除
- Stale compiled binaries and empty mcp directory / 清理过期的编译产物和空目录

---

## [v0.1.0] — 2026-04-10

### Added / 新增
- Cross-platform one-line installers (`install.sh`, `install.ps1`) / 跨平台一键安装脚本
- Skills installation and `CLAUDE.md` config injection / 技能安装与 `CLAUDE.md` 配置注入
- Windows cross-compilation and GitHub Actions release workflow / Windows 交叉编译与 GitHub Actions 发布流程

### Changed / 变更
- Strip providers down to **codex, gemini, opencode, claude** (removed droid, copilot, codebuddy, qwen) / 精简 Provider 为四个（移除 droid、copilot、codebuddy、qwen）

### Fixed / 修复
- CodexLogReader `extractCWDFromLog` buffer too small for `session_meta` / CodexLogReader 日志缓冲区过小的问题
- Remove remaining `dask` daemon checks / 清除残留的 `dask` 守护进程检查

---

[Unreleased]: https://github.com/curdx/curdx-bridge/compare/v0.1.2...HEAD
[v0.1.2]: https://github.com/curdx/curdx-bridge/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/curdx/curdx-bridge/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/curdx/curdx-bridge/releases/tag/v0.1.0
