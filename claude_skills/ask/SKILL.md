---
name: ask
description: Async via ask, end turn immediately; use when user explicitly delegates to any AI provider (codex); NOT for questions about the providers themselves.
metadata:
  short-description: Ask AI provider asynchronously
---

# Ask AI Provider (Async)

Send the user's request to specified AI provider asynchronously.

## Usage

The first argument must be the provider name, followed by the message:
- `codex` - Send to Codex

## Execution (MANDATORY)

```
Bash(CURDX_CALLER=claude ask $PROVIDER "$MESSAGE")
```

## Rules

- Follow the **Async Guardrail** rule in CLAUDE.md (mandatory).
- Local fallback: if output contains `CURDX_ASYNC_SUBMITTED`, end your turn immediately.
- If submit fails (non-zero exit):
  - Reply with exactly one line: `[Provider] submit failed: <short error>`
  - End your turn immediately.

## Examples

- `/ask codex Refactor this code`

