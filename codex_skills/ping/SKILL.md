---
name: ping
description: Test connectivity with AI provider (codex/claude).
metadata:
  short-description: Test AI provider connectivity
---

# Ping AI Provider

Test connectivity with specified AI provider.

## Usage

The first argument must be the provider name:
- `codex` - Test Codex
- `claude` - Test Claude

## Execution (MANDATORY)

```bash
curdx-ping $ARGUMENTS
```

## Examples

- `/ping codex`
- `/ping claude`
