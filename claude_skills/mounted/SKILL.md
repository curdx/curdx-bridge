---
name: mounted
description: Report which CURDX providers are mounted (session exists AND daemon is online). Outputs JSON.
metadata:
  short-description: Show mounted CURDX providers as JSON
---

# Mounted Providers

Reports which CURDX providers are considered "mounted" for the current project.

## Definition

`mounted = has_session && daemon_on`

## Execution

```bash
curdx-mounted
```
