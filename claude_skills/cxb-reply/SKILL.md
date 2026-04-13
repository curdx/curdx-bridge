---
name: cxb-reply
description: View latest reply from AI provider (gemini/codex/opencode/claude).
metadata:
  short-description: View latest AI provider reply
---

# Pend - View Latest Reply

View the latest reply from specified AI provider.

## Usage

The first argument must be the provider name:
- `gemini` - View Gemini reply
- `codex` - View Codex reply
- `opencode` - View OpenCode reply
- `claude` - View Claude reply

Optional: Add a number N to show the latest N conversations.

## Execution (MANDATORY)

```bash
cxb-pend $ARGUMENTS
```

## Examples

- `/cxb-reply gemini`
- `/cxb-reply codex 3`
- `/cxb-reply claude`
