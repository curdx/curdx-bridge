# CURDX Bridge

**Orchestrate multiple AI coding agents in one terminal.** Claude, Codex, Gemini, and OpenCode — working together, side by side.

[中文说明](#中文说明)

---

## What is CURDX Bridge?

CURDX Bridge turns your terminal into a **multi-AI collaboration workspace**. Instead of switching between different AI tools, you get them all in split panes — each with its own session, memory, and specialty — communicating asynchronously through a unified protocol.

```
┌─────────────────────┬──────────────────────┐
│                     │       Codex          │
│      Claude         │    (Reviewer)        │
│    (Designer)       ├──────────────────────┤
│                     │       Gemini         │
│                     │   (Inspiration)      │
└─────────────────────┴──────────────────────┘
```

## Who is this for?

You're a developer who already uses AI coding assistants (Claude Code, Codex CLI, etc.) and you've noticed:

- **You keep switching tabs** between different AI tools for different tasks
- **No single AI is best at everything** — one plans well, another reviews better, another brainstorms creatively
- **You want AI agents to collaborate**, not just respond to you one at a time

If you've ever wished you could tell Claude to write the code, have Codex review it, and ask Gemini for alternative approaches — all without leaving your terminal — CURDX Bridge is for you.

## Why?

Each AI has strengths. Claude plans and implements. Codex reviews with scoring rubrics. Gemini brainstorms creative alternatives. CURDX Bridge lets you **combine them in real workflows** — not just chat with one at a time.

- **Plan** with Claude, get it **reviewed** by Codex, gather **inspiration** from Gemini
- Async communication — agents don't block each other
- Context transfer between providers when needed
- One command to launch everything

## Quick Start

### Prerequisites

You need a terminal multiplexer and at least one AI provider CLI installed:

| Component | How to get it |
|-----------|--------------|
| **tmux 3.0+** (or WezTerm) | `brew install tmux` / `apt install tmux` |
| **Claude Code** | `npm install -g @anthropic-ai/claude-code` |
| **Codex CLI** (optional) | `npm install -g @openai/codex` |
| **Gemini CLI** (optional) | `npm install -g @anthropic-ai/claude-code && pip install google-generativeai` |
| **OpenCode** (optional) | See [OpenCode docs](https://github.com/opencode-ai/opencode) |

Each provider uses its own authentication. Make sure you can run `claude`, `codex`, or `gemini` individually before using CURDX Bridge.

### Install

```bash
# macOS / Linux (one-liner)
curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.sh | bash
```

### Verify Installation

```bash
# Check version
curdx --version

# Test provider connectivity
cping    # Codex reachable?
gping    # Gemini reachable?
lping    # Claude reachable?
```

### Launch

```bash
# Start with Claude + Codex + Gemini (3-pane layout)
curdx claude codex gemini

# Start with just Claude + Codex (2-pane layout)
curdx claude codex
```

### Talk to Providers

```bash
# Synchronous (wait for reply)
cask "Review this function for bugs"     # → Codex
gask "Suggest alternative UI designs"    # → Gemini
lask "Implement the auth middleware"     # → Claude

# Asynchronous (fire and forget)
ask codex "Review the latest diff"
ask gemini "Brainstorm naming ideas"

# Check replies
cpend    # Latest Codex reply
gpend    # Latest Gemini reply
lpend    # Latest Claude reply
```

## Example: A Real Workflow

Here's a concrete multi-agent session — planning a feature, reviewing it, and implementing it:

```bash
# 1. Launch workspace
curdx claude codex gemini

# 2. Ask Claude to design an auth middleware
lask "Design a JWT auth middleware for our Express app. Consider refresh tokens."

# 3. While Claude is thinking, ask Gemini for inspiration
ask gemini "What are the best practices for JWT refresh token rotation in 2024?"

# 4. Once Claude's plan is ready, send it to Codex for review
cpend                          # Check if Gemini replied yet
ask codex "[PLAN REVIEW REQUEST]
--- PLAN START ---
$(lpend)
--- PLAN END ---
Score this plan on security, maintainability, and completeness."

# 5. Check Codex's review scores
cpend

# 6. If review passes (score >= 7), tell Claude to implement
lask "Implement the auth middleware based on the approved plan. Address the reviewer's feedback."

# 7. After implementation, send code for final review
ask codex "[CODE REVIEW REQUEST]
--- CHANGES START ---
$(git diff)
--- CHANGES END ---"

# 8. Done — multi-agent plan → review → implement → review cycle complete
```

## Features

### Multi-Provider Orchestration
- **4 providers**: Claude, Codex (OpenAI), Gemini (Google), OpenCode
- **Role-based workflow**: Designer → Inspiration → Reviewer → Executor
- **Async protocol**: Reliable request tracking with `CURDX_PROTOCOL` markers

### Terminal Integration
- **tmux** and **WezTerm** backends
- Auto-layout: 2-pane, 3-pane, or 2×2 grid
- Rich status bar with git branch, agent focus, and version info
- Pane labels so you always know who's who

### Session Management
- Persistent sessions per provider
- Context transfer between providers (`ctx-transfer`)
- Automatic cleanup of stale sessions (7-day TTL)
- Session resume on reconnect

### Review Framework
- Structured plan reviews and code reviews
- Scoring rubrics (1–10 across multiple dimensions)
- Pass/fail gates before shipping code
- Up to 3 review rounds before escalating to human

### Skill Ecosystem
12+ built-in Claude skills: `/ask`, `/pend`, `/cping`, `/review`, `/tp` (task plan), `/tr` (task run), `/all-plan`, `/continue`, and more.

## Architecture

```
┌─────────────────┐
│   curdx CLI     │  ← Main orchestrator
└────────┬────────┘
         │
         ├── askd (daemon) ── TCP JSON-RPC server
         │    ├── Claude adapter
         │    ├── Codex adapter
         │    ├── Gemini adapter
         │    └── OpenCode adapter
         │
         ├── Terminal backend (tmux / WezTerm)
         │    └── Pane registry & layout engine
         │
         └── Session & Memory layer
              ├── Per-provider session logs
              ├── Context deduplication
              └── Token estimation
```

## Configuration

Key environment variables:

| Variable | Default | When to change |
|----------|---------|----------------|
| `CURDX_RUN_DIR` | `~/.cache/curdx` | Custom daemon runtime directory |
| `CURDX_LANG` | auto-detect | Force language: `en` or `zh` |
| `CURDX_THEME` | auto | Force tmux theme: `light` or `dark` |
| `CURDX_DEBUG` | off | Set to `1` when troubleshooting |
| `CURDX_ASKD_IDLE_TIMEOUT_S` | `3600` | Increase if daemon shuts down too soon |
| `CURDX_CASKD_AUTOSTART` | `1` | Set to `0` to disable auto-starting Codex daemon |
| `CURDX_GASKD_AUTOSTART` | `1` | Set to `0` to disable auto-starting Gemini daemon |

## Requirements

- **tmux 3.0+** or **WezTerm** (terminal multiplexer)
- At least one AI provider CLI configured and authenticated
- **Go 1.22+** (only for building from source)

## Supported Platforms

| Platform | Architecture |
|----------|-------------|
| macOS | Intel (amd64), Apple Silicon (arm64) |
| Linux | x86-64, ARM64 |
| Windows | x86-64 (native + WSL) |

## Build from Source

```bash
git clone https://github.com/curdx/curdx-bridge.git
cd curdx-bridge
./scripts/build-all.sh
```

Binaries output to `dist/curdx-{os}-{arch}/`.

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `curdx: command not found` | Ensure `~/.local/bin` is in your `$PATH` |
| `no tmux session` | Install tmux: `brew install tmux` or `apt install tmux` |
| `cping` shows unreachable | Check that `codex` CLI works standalone first |
| Daemon won't start | Check logs: `cat ~/.cache/curdx/askd.log` |
| Panes not appearing | Verify tmux version: `tmux -V` (need 3.0+) |
| Provider auth failure | Run the provider CLI directly to fix auth (e.g. `claude` or `codex`) |

For debug mode, set `CURDX_DEBUG=1` before launching:

```bash
CURDX_DEBUG=1 curdx claude codex
```

## License

AGPL-3.0. See [LICENSE](LICENSE) for details.

## Acknowledgements

CURDX Bridge evolved from [**Claude Code Bridge (CCB)**](https://github.com/bfly123/claude_code_bridge) by [bfly123](https://github.com/bfly123).

The original CCB pioneered the idea of multi-AI terminal collaboration — running multiple AI coding agents side by side in tmux with async communication. CURDX Bridge is a complete rewrite in Go (CCB was Python-based), with a streamlined provider set and an enhanced daemon protocol. No code was directly inherited; the relationship is conceptual inspiration and architectural influence.

Thank you, bfly123, for open-sourcing the project that seeded this idea.

---

# 中文说明

## CURDX Bridge 是什么？

CURDX Bridge 将你的终端变成一个**多 AI 协作工作区**。Claude、Codex、Gemini、OpenCode — 在分屏面板中并排工作，通过统一协议异步通信。

```
┌─────────────────────┬──────────────────────┐
│                     │       Codex          │
│      Claude         │    (评审员)          │
│     (设计师)        ├──────────────────────┤
│                     │       Gemini         │
│                     │     (灵感源)         │
└─────────────────────┴──────────────────────┘
```

## 适合谁？

你是一个已经在用 AI 编程工具的开发者，而且你发现：

- **频繁切换**不同的 AI 工具来完成不同任务
- **没有哪个 AI 样样最强** — 有的规划好，有的 review 好，有的创意好
- **你希望 AI 之间能协作**，而不是只能一对一回答你

如果你曾想过：让 Claude 写代码、Codex 来审代码、Gemini 出创意 — 全部在终端里完成 — CURDX Bridge 就是为你做的。

## 为什么需要它？

每个 AI 都有自己的长处。Claude 擅长规划和实现，Codex 擅长评审打分，Gemini 擅长创意发散。CURDX Bridge 让你**在真实工作流中组合它们** — 而不是一次只和一个聊天。

- 用 Claude **规划**，让 Codex **评审**，从 Gemini 获取**灵感**
- 异步通信 — 各 Agent 互不阻塞
- 需要时可跨 Provider 传递上下文
- 一条命令启动一切

## 快速开始

### 前置条件

| 组件 | 安装方式 |
|------|---------|
| **tmux 3.0+**（或 WezTerm） | `brew install tmux` / `apt install tmux` |
| **Claude Code** | `npm install -g @anthropic-ai/claude-code` |
| **Codex CLI**（可选） | `npm install -g @openai/codex` |
| **Gemini CLI**（可选） | 参考 Gemini 官方文档 |

确保每个 Provider 的 CLI 能单独运行后再使用 CURDX Bridge。

### 安装

```bash
curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.sh | bash
```

### 验证安装

```bash
curdx --version    # 查看版本
cping              # 测试 Codex 连通性
gping              # 测试 Gemini 连通性
lping              # 测试 Claude 连通性
```

### 启动

```bash
# Claude + Codex + Gemini（三面板布局）
curdx claude codex gemini

# 仅 Claude + Codex（双面板布局）
curdx claude codex
```

### 与 Provider 对话

```bash
# 同步（等待回复）
cask "检查这个函数有没有 bug"     # → Codex
gask "建议一些 UI 设计方案"       # → Gemini
lask "实现认证中间件"             # → Claude

# 异步（发送后继续工作）
ask codex "Review the latest diff"
ask gemini "Brainstorm naming ideas"

# 查看回复
cpend    # Codex 最新回复
gpend    # Gemini 最新回复
lpend    # Claude 最新回复
```

## 示例：一个真实的工作流

```bash
# 1. 启动工作区
curdx claude codex gemini

# 2. 让 Claude 设计一个 JWT 认证中间件
lask "设计一个 Express 的 JWT 认证中间件，考虑 refresh token"

# 3. 同时问 Gemini 要灵感
ask gemini "2024 年 JWT refresh token 轮换的最佳实践是什么？"

# 4. Claude 的方案好了后，发给 Codex 评审
ask codex "[PLAN REVIEW REQUEST]
--- PLAN START ---
$(lpend)
--- PLAN END ---
从安全性、可维护性、完整性三个维度评分。"

# 5. 查看 Codex 的评审分数
cpend

# 6. 评审通过（>= 7分），让 Claude 实现
lask "按照已通过的方案实现认证中间件，注意评审反馈"

# 7. 实现完成后，最终代码评审
ask codex "[CODE REVIEW REQUEST]
--- CHANGES START ---
$(git diff)
--- CHANGES END ---"

# 8. 完成 — 规划 → 评审 → 实现 → 复审 的多 Agent 循环
```

## 核心特性

### 多 Provider 编排
- **4 个 Provider**：Claude、Codex（OpenAI）、Gemini（Google）、OpenCode
- **角色工作流**：设计师 → 灵感 → 评审 → 执行
- **异步协议**：通过 `CURDX_PROTOCOL` 标记可靠追踪请求

### 终端集成
- **tmux** 和 **WezTerm** 双后端
- 自动布局：双面板、三面板、2×2 网格
- 丰富的状态栏：git 分支、当前 Agent 焦点、版本号
- 面板标签，一眼看清谁是谁

### 会话管理
- 每个 Provider 独立持久会话
- 跨 Provider 上下文传递（`ctx-transfer`）
- 过期会话自动清理（7 天 TTL）
- 断线重连自动恢复会话

### 评审框架
- 结构化的方案评审和代码评审
- 多维度评分（1–10 分）
- 通过/失败门禁，代码上线前必须过审
- 最多 3 轮评审，之后升级给人类决策

### 技能生态
12+ 内置 Claude 技能：`/ask`、`/pend`、`/cping`、`/review`、`/tp`（任务规划）、`/tr`（任务执行）、`/all-plan`、`/continue` 等。

## 架构

```
┌─────────────────┐
│   curdx CLI     │  ← 主编排器
└────────┬────────┘
         │
         ├── askd（守护进程）── TCP JSON-RPC 服务
         │    ├── Claude 适配器
         │    ├── Codex 适配器
         │    ├── Gemini 适配器
         │    └── OpenCode 适配器
         │
         ├── 终端后端（tmux / WezTerm）
         │    └── 面板注册与布局引擎
         │
         └── 会话与记忆层
              ├── Provider 会话日志
              ├── 上下文去重
              └── Token 估算
```

## 配置

| 环境变量 | 默认值 | 何时修改 |
|----------|--------|----------|
| `CURDX_RUN_DIR` | `~/.cache/curdx` | 自定义守护进程运行目录 |
| `CURDX_LANG` | 自动检测 | 强制语言：`en` 或 `zh` |
| `CURDX_THEME` | auto | 强制 tmux 主题：`light` 或 `dark` |
| `CURDX_DEBUG` | off | 排查问题时设为 `1` |
| `CURDX_ASKD_IDLE_TIMEOUT_S` | `3600` | 守护进程空闲超时（秒） |

## 故障排查

| 问题 | 解决方案 |
|------|----------|
| `curdx: command not found` | 确认 `~/.local/bin` 在 `$PATH` 中 |
| 没有 tmux 会话 | 安装 tmux：`brew install tmux` 或 `apt install tmux` |
| `cping` 显示不可达 | 先确认 `codex` CLI 能单独正常运行 |
| 守护进程启动失败 | 查看日志：`cat ~/.cache/curdx/askd.log` |
| 面板没出现 | 检查 tmux 版本：`tmux -V`（需要 3.0+） |
| Provider 认证失败 | 直接运行 Provider CLI 修复认证（如 `claude` 或 `codex`） |

调试模式：

```bash
CURDX_DEBUG=1 curdx claude codex
```

## 系统要求

- **tmux 3.0+** 或 **WezTerm**
- 至少配置一个 AI Provider CLI 并完成认证
- 从源码构建需要 **Go 1.22+**

## 支持平台

| 平台 | 架构 |
|------|------|
| macOS | Intel (amd64)、Apple Silicon (arm64) |
| Linux | x86-64、ARM64 |
| Windows | x86-64（原生 + WSL） |

## 从源码构建

```bash
git clone https://github.com/curdx/curdx-bridge.git
cd curdx-bridge
./scripts/build-all.sh
```

产物输出到 `dist/curdx-{os}-{arch}/`。

## 许可证

AGPL-3.0，详见 [LICENSE](LICENSE)。

## 致谢

CURDX Bridge 由 [**Claude Code Bridge (CCB)**](https://github.com/bfly123/claude_code_bridge) 演化而来，感谢原作者 [bfly123](https://github.com/bfly123) 开创了多 AI 终端协作的理念。

CCB 首创了在 tmux 中并排运行多个 AI 编程 Agent 并异步通信的模式。CURDX Bridge 是用 Go 完全重写的版本（CCB 基于 Python），精简了 Provider 集合并增强了守护进程协议。两者之间没有直接代码继承，关系是概念启发和架构影响。

感谢 bfly123 将项目开源，播下了这颗种子。
