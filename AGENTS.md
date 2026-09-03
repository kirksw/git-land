# Agent Guidance

## Landing changes

Use `land` when work is ready to be submitted.
Do not bypass repository landing policy.

For safe inspection and automation, prefer:

```bash
land status --json
land validate --json
land submit --dry-run --json
```
