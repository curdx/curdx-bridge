## Execution (MANDATORY)

```bash
CCB_CALLER=droid ask $PROVIDER <<'EOF'
$MESSAGE
EOF
```

## Rules

- After running the command, say "[Provider] processing..." and immediately end your turn.
- Do not wait for results or check status in the same turn.
- The task ID and log file path will be displayed for tracking.

## Examples

- `/ask gemini What is 12+12?` (send via heredoc)
- `CCB_CALLER=droid ask gemini <<'EOF'`
  `What is 12+12?`
  `EOF`

## Notes

- If it fails, check backend health with `ccb-ping <provider>` (e.g., `ccb-ping gemini`).
