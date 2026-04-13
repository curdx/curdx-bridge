---
name: cxb-ping
description: Test connectivity with AI provider (gemini/codex/opencode/claude).
metadata:
  short-description: Test AI provider connectivity
---

# Ping AI Provider

Test connectivity with specified AI provider.

## Usage

The first argument must be the provider name:
- `gemini` - Test Gemini
- `codex` - Test Codex
- `opencode` - Test OpenCode
- `claude` - Test Claude

## Execution (MANDATORY)

Use `curdx-ping` wrapper to avoid conflict with system `ping`:
```
Bash(curdx-ping $PROVIDER)
```

## Examples

- `/cxb-ping gemini`
- `/cxb-ping codex`
- `/cxb-ping claude`
