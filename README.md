<div align="center">

# CURDX Bridge v5.2.9

**Multi-AI Split-Pane Terminal — Claude · Codex · Gemini**

One terminal, multiple AI agents, real collaboration.

[![Version](https://img.shields.io/badge/version-5.2.9-orange.svg)]()
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8.svg)](https://golang.org/)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey.svg)]()

**English** | [中文](#中文)

</div>

---

## What is this?

CURDX Bridge puts multiple AI coding agents into split terminal panes. You talk to Claude as usual — when you need a second opinion, just say "let Codex review this" or "ask Gemini for ideas". Claude handles the coordination automatically.

```
┌─────────────────────┬──────────────────────┐
│                     │       Codex          │
│      Claude         │    (Reviewer)        │
│   You talk here     ├──────────────────────┤
│                     │       Gemini         │
│                     │   (Brainstormer)     │
└─────────────────────┴──────────────────────┘
```

No switching tabs. No copy-pasting context. Just talk.

## Quick Start

### 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.sh | bash
```

### 2. Run

```bash
curdx                          # Default: Claude + Codex + Gemini
curdx claude codex             # Just two providers
curdx -r                       # Resume previous session
curdx -r claude codex gemini   # Resume with specific providers
```

That's it. Panes appear, providers boot up, you start talking to Claude.

### Flags

| Flag | What it does |
|------|-------------|
| `-r` | Resume last session (keeps context) |
| `--no-auto` | Disable auto-approve mode |

## How It Actually Works

You don't need to learn new commands. Just talk to Claude naturally:

```
You:    Help me refactor this auth module.
Claude: [writes the refactored code]

You:    Let Codex review this.
Claude: [sends diff to Codex, waits for scores]
        Codex scored it 8.5/10. Suggestions: ...

You:    Ask Gemini for alternative naming ideas.
Claude: [asks Gemini asynchronously]
        Gemini suggests: ...

You:    Looks good. Apply Codex's suggestions and commit.
Claude: [makes changes, commits]
```

**That's the whole workflow.** Claude is your main interface. Codex and Gemini are collaborators it can call on.

### Behind the scenes

When you say "let Codex review this", Claude uses built-in skills (`/ask`, `/pend`) to:
1. Send your request to the Codex pane via async protocol
2. Wait for Codex to finish (you can see it working in its pane)
3. Bring the result back into your conversation

Each provider runs in its own pane — you can watch them think in real time.

## Roles

| Role | Provider | What it does |
|------|----------|-------------|
| **Designer** | Claude | Plans, implements, orchestrates |
| **Reviewer** | Codex | Scores code/plans (1-10 rubrics) |
| **Inspiration** | Gemini | Brainstorms alternatives (reference only) |

The review framework has pass/fail gates — code must score ≥ 7 before shipping. Up to 3 review rounds, then escalates to you.

## Commands (for power users)

You rarely need these — Claude handles them — but they exist:

```bash
# Direct communication
cask "message"    # Send to Codex
gask "message"    # Send to Gemini
lask "message"    # Send to Claude

# Check latest replies
cpend / gpend / lpend

# Test connectivity
cping / gping / lping

# Session management
curdx kill              # Kill all sessions
curdx kill codex -f     # Force kill specific provider
```

## Prerequisites

| You need | Install with |
|----------|-------------|
| **tmux** (or WezTerm) | `brew install tmux` / `apt install tmux` |
| **Claude Code** | `npm install -g @anthropic-ai/claude-code` |
| **Codex CLI** (optional) | `npm install -g @openai/codex` |
| **Gemini CLI** (optional) | See provider docs |

Make sure each provider CLI works standalone first.

## Platforms

macOS (Intel/Apple Silicon) · Linux (x86-64/ARM64) · Windows (x86-64 + WSL)

## Configuration

### curdx.config

Place in `.curdx/curdx.config` (project-level) or `~/.curdx/curdx.config` (global):

```
claude codex gemini
```

Or JSON for advanced options:

```json
{
  "providers": ["claude", "codex", "gemini"],
  "flags": { "resume": true, "auto": true }
}
```

### Environment Variables

| Variable | What it does |
|----------|-------------|
| `CURDX_DEBUG=1` | Debug logging |
| `CURDX_LANG=zh` | Force Chinese |
| `CURDX_THEME=dark` | Force dark theme |

## Build from Source

```bash
git clone https://github.com/curdx/curdx-bridge.git
cd curdx-bridge
./scripts/build-all.sh
```

## Troubleshooting

| Problem | Fix |
|---------|-----|
| `curdx: command not found` | Add `~/.local/bin` to `$PATH` |
| No panes appear | Install tmux: `brew install tmux` |
| Provider unreachable | Run it standalone first (e.g. `codex`) |
| `Another instance running` | `curdx kill` then retry |

Debug mode: `CURDX_DEBUG=1 curdx`

## License

AGPL-3.0. See [LICENSE](LICENSE).

## Acknowledgements

CURDX Bridge is a Go rewrite inspired by [**Claude Code Bridge**](https://github.com/bfly123/claude_code_bridge) by [bfly123](https://github.com/bfly123). The original CCB pioneered multi-AI split-pane terminal collaboration in Python. CURDX Bridge inherits the core idea — multiple AI agents visible and controllable in one workspace — while reimplementing everything in Go with a streamlined protocol.

Thanks to bfly123 for open-sourcing the project that started it all.

---

<div align="center">

# 中文

</div>

## 这是什么？

CURDX Bridge 把多个 AI 编程助手放进终端分屏。你像平时一样和 Claude 聊天 — 需要第二意见时，说一句"让 Codex 审查下代码"或"问问 Gemini 有什么想法"，Claude 自动协调。

```
┌─────────────────────┬──────────────────────┐
│                     │       Codex          │
│      Claude         │     (评审员)         │
│    你在这里聊天      ├──────────────────────┤
│                     │       Gemini         │
│                     │     (创意源)         │
└─────────────────────┴──────────────────────┘
```

不用切标签页。不用复制粘贴上下文。直接说。

## 快速开始

```bash
# 安装
curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.sh | bash

# 启动
curdx                          # 默认：Claude + Codex + Gemini
curdx claude codex             # 只启动两个
curdx -r                       # 恢复上次会话
curdx -r claude codex gemini   # 恢复指定 Provider 的会话
```

面板出现，Provider 启动，开始聊天。

## 实际使用方式

不需要学新命令，和 Claude 正常对话就行：

```
你:     帮我重构这个认证模块。
Claude: [写出重构后的代码]

你:     让 Codex 审查下。
Claude: [把 diff 发给 Codex，等评分]
        Codex 评分 8.5/10，建议：...

你:     问问 Gemini 有没有更好的命名方案。
Claude: [异步询问 Gemini]
        Gemini 建议：...

你:     不错，采纳 Codex 的建议然后提交。
Claude: [修改代码，提交]
```

**就是这样。** Claude 是你的主界面，Codex 和 Gemini 是它的协作者。

每个 Provider 在独立面板运行 — 你可以实时看到它们在干什么。

## 角色分工

| 角色 | Provider | 职责 |
|------|----------|------|
| **设计师** | Claude | 规划、实现、协调 |
| **评审员** | Codex | 代码/方案评分（1-10 多维度） |
| **灵感源** | Gemini | 发散思路（仅参考） |

评审框架有通过/失败门禁 — 评分 ≥ 7 才能通过，最多 3 轮评审。

## 前置条件

| 需要 | 安装方式 |
|------|---------|
| **tmux**（或 WezTerm） | `brew install tmux` / `apt install tmux` |
| **Claude Code** | `npm install -g @anthropic-ai/claude-code` |
| **Codex CLI**（可选） | `npm install -g @openai/codex` |
| **Gemini CLI**（可选） | 参考官方文档 |

确保每个 Provider CLI 能单独运行。

## 平台支持

macOS（Intel / Apple Silicon）· Linux（x86-64 / ARM64）· Windows（x86-64 + WSL）

## 故障排查

| 问题 | 解决 |
|------|------|
| `curdx: command not found` | 把 `~/.local/bin` 加到 `$PATH` |
| 面板没出现 | 安装 tmux：`brew install tmux` |
| Provider 连不上 | 先单独运行它（如 `codex`） |
| 提示已有实例运行 | `curdx kill` 后重试 |

调试模式：`CURDX_DEBUG=1 curdx`

## 许可证

AGPL-3.0，详见 [LICENSE](LICENSE)。

## 致谢

CURDX Bridge 是受 [**Claude Code Bridge**](https://github.com/bfly123/claude_code_bridge)（作者 [bfly123](https://github.com/bfly123)）启发的 Go 重写版。原版 CCB 用 Python 首创了多 AI 分屏终端协作。CURDX Bridge 继承了核心理念 — 多个 AI 在同一工作区可见可控 — 用 Go 重新实现并精简了通信协议。

感谢 bfly123 开源了这个开创性的项目。
