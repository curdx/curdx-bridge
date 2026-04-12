<div align="center">

# CURDX Bridge

**多 AI 分屏终端 — Claude · Codex · Gemini · OpenCode**

一个终端，四个 AI，真正的协作。

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8.svg)](https://golang.org/)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey.svg)]()

[English](README.md) | **中文**

</div>

---

<div align="center">
<img src="docs/screenshot.png" alt="CURDX Bridge — 四个 AI 在分屏面板中协作" width="800" />
<br/>
<em>Claude、Codex、Gemini、OpenCode 在同一终端中并肩工作</em>
</div>

---

## 这是什么？

CURDX Bridge 把多个 AI 编程助手放进终端分屏。你像平时一样和 Claude 聊天 — 需要第二意见时，说一句"让 Codex 审查下代码"或"问问 Gemini 有什么想法"，Claude 自动协调。

<div align="center">
<img src="docs/layout.svg" alt="CURDX Bridge 布局 — 左 Claude，右 Codex/Gemini/OpenCode" width="680" />
</div>

不用切标签页。不用复制粘贴上下文。直接说。

## 快速开始

### 1. 安装

```bash
curl -fsSL https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.sh | bash
```

### 2. 启动

```bash
curdx                                  # 默认：Claude + Codex + Gemini
curdx claude codex gemini opencode     # 全部四个 Provider
curdx claude codex                     # 只启动两个
curdx -r                               # 恢复上次会话
curdx -r claude codex gemini           # 恢复指定 Provider 的会话
```

面板出现，Provider 启动，开始聊天。

### 参数

| 参数 | 作用 |
|------|------|
| `-r` | 恢复上次会话（保持上下文） |
| `--no-auto` | 关闭自动审批模式 |

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

**就是这样。** Claude 是你的主界面，Codex、Gemini 和 OpenCode 是它的协作者。

### 背后的原理

当你说"让 Codex 审查下"，Claude 用内置技能（`/ask`、`/pend`）来：
1. 通过异步协议把请求发到 Codex 面板
2. 等 Codex 完成（你能看到它在自己的面板里工作）
3. 把结果带回你的对话

每个 Provider 在独立面板运行 — 你可以实时看到它们在干什么。

## 角色分工

| 角色 | Provider | 职责 |
|------|----------|------|
| **设计师** | Claude | 规划、实现、协调 |
| **评审员** | Codex | 代码/方案评分（1-10 多维度） |
| **灵感源** | Gemini | 发散思路（仅参考） |
| **协作者** | OpenCode | 额外的 AI 视角 |

评审框架有通过/失败门禁 — 评分 ≥ 7 才能通过，最多 3 轮评审，之后交给你决定。

## 命令行（进阶用户）

一般不需要手动用这些 — Claude 会帮你处理 — 但它们存在：

```bash
# 直接通信
cask "消息"    # 发给 Codex
gask "消息"    # 发给 Gemini
oask "消息"    # 发给 OpenCode
lask "消息"    # 发给 Claude

# 查看最新回复
cpend / gpend / opend / lpend

# 测试连通性
cping / gping / oping / lping

# 会话管理
curdx kill              # 终止所有会话
curdx kill codex -f     # 强制终止指定 Provider
```

## 前置条件

| 需要 | 安装方式 |
|------|---------|
| **tmux**（或 WezTerm） | `brew install tmux` / `apt install tmux` |
| **Claude Code** | `npm install -g @anthropic-ai/claude-code` |
| **Codex CLI**（可选） | `npm install -g @openai/codex` |
| **Gemini CLI**（可选） | 参考官方文档 |
| **OpenCode CLI**（可选） | 参考官方文档 |

确保每个 Provider CLI 能单独运行。

## 平台支持

macOS（Intel / Apple Silicon）· Linux（x86-64 / ARM64）· Windows（x86-64 + WSL）

## 配置

### curdx.config

放在 `.curdx/curdx.config`（项目级）或 `~/.curdx/curdx.config`（全局）：

```
claude codex gemini opencode
```

JSON 高级配置：

```json
{
  "providers": ["claude", "codex", "gemini", "opencode"],
  "flags": { "resume": true, "auto": true }
}
```

### 环境变量

| 变量 | 作用 |
|------|------|
| `CURDX_DEBUG=1` | 调试日志 |
| `CURDX_LANG=zh` | 强制中文 |
| `CURDX_THEME=dark` | 强制暗色主题 |

## 从源码构建

```bash
git clone https://github.com/curdx/curdx-bridge.git
cd curdx-bridge
./scripts/build-all.sh
```

## 故障排查

| 问题 | 解决 |
|------|------|
| `curdx: command not found` | 把 `~/.local/bin` 加到 `$PATH` |
| 面板没出现 | 安装 tmux：`brew install tmux` |
| Provider 连不上 | 先单独运行它（如 `codex`） |
| 提示已有实例运行 | `curdx kill` 后重试 |

调试模式：`CURDX_DEBUG=1 curdx`

## 更新日志

详见 [CHANGELOG.md](CHANGELOG.md)。

## 许可证

AGPL-3.0，详见 [LICENSE](LICENSE)。

## 致谢

CURDX Bridge 是受 [**Claude Code Bridge**](https://github.com/bfly123/claude_code_bridge)（作者 [bfly123](https://github.com/bfly123)）启发的 Go 重写版。原版 CCB 用 Python 首创了多 AI 分屏终端协作。CURDX Bridge 继承了核心理念 — 多个 AI 在同一工作区可见可控 — 用 Go 重新实现并精简了通信协议。

感谢 bfly123 开源了这个开创性的项目。
