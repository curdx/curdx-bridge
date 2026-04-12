# AutoFlow File-Op

Plan mode is optional. This command delegates **all repo file I/O** to Codex using the `FileOpsREQ` / `FileOpsRES` JSON protocol.

**Protocol**: See `~/.claude/skills/docs/protocol.md`

---

## Input

From `$ARGUMENTS`:
- A single `FileOpsREQ` JSON object (must include `proto: "autoflow.fileops.v1"`).

---

## Execution

1. Validate `$ARGUMENTS` is a single JSON object (no prose).
2. Send to Codex:

```
Bash(CURDX_CALLER=claude ask codex "Execute this FileOpsREQ JSON exactly and return FileOpsRES JSON only.\n\n## CRITICAL: Roles Self-Resolution (Hard Constraint)\nYou MUST read roles config yourself to determine executor. Do NOT rely on Claude passing constraints.executor.\n\nRoles priority (first valid wins):\n1. .autoflow/roles.json\n2. Default: executor=codex\n\nValidation: schemaVersion=1, enabled=true; otherwise skip to default.\n\n## Executor Routing\n- executor=codex (or missing): execute ops directly.\n\n$ARGUMENTS", run_in_background=true)
TaskOutput(task_id=<task_id>, block=true)
```

3. Validate the response is JSON only and matches `proto`/`id`.
4. Dispatch by `status`:
   - `ok`: return the JSON to the caller
   - `ask`: surface `ask.questions`
   - `split`: surface `split.substeps`
   - `fail`: surface `fail.reason` and stop

---

## Principles

1. **Claude never edits files**: all writes/patches happen in Codex
2. **JSON-only boundary**: request/response must be machine-parsable
3. **Prefer domain ops**: use `autoflow_*` ops for state/todo/log updates
