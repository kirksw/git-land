---
name: land
description: Land completed branch changes through the land CLI. Use when work is ready to submit, when a pull request's CI fails, or when asked to land, submit, publish, or verify changes. Drives synchronize -> validate -> publish -> verify by rerunning `land --json` and following its blockedOn hints.
---

# Landing changes with land

`land` is a deterministic gate: it synchronizes, validates, publishes, and verifies, but it never commits, rebases, or discards work.
Those authoring steps are yours.
Each `land --json` run reports a `phase`, a `blockedOn` reason, and a `hint`; your job is to resolve the block and rerun until the branch is landed.

## The loop

Run from the repository root:

```bash
land --json
```

The command polls continuous integration until checks settle (default timeout 30 minutes), so one run usually returns a terminal verdict; in `--json` mode progress streams to stderr while stdout stays a single report document — parse stdout only.

Act on the report:

- `landed: true` — done.
Report the `phase` (`merged` for pull requests, `landed` for direct pushes) and stop.
- `blockedOn: dirty_tree` — commit the work with a descriptive message, then rerun.
- `blockedOn: on_base` — create a feature branch from the current integration base first, then rerun.
- `blockedOn: no_commits` — there is nothing to land; stop and tell the user.
- `blockedOn: behind_base`, `merge_conflicts`, or `branch_behind_remote` — integrate `origin/<base>` into the branch (rebase when it applies cleanly, merge otherwise), resolve conflicts, commit, then rerun.
- `blockedOn: validation` — the `validation` array in the report names the failing command and captures its output; fix the code, commit, then rerun.
- `blockedOn: ci_failed` — the `checks` array names the failing checks.
Inspect their logs with `gh run view` or `gh pr view --json`, fix the code, commit, then rerun.
- `blockedOn: ci_pending` after a full wait — CI is slower than the timeout; rerun `land` (or raise `--wait-timeout`), and mention it to the user if it keeps timing out.
- `blockedOn: human_merge` (phase `ready_for_merge`) — policy requires a human to merge; report the pull-request URL and stop.
- `blockedOn: pull_request_closed` — a human closed the pull request; stop and ask how to proceed.

## Rules

- `land` is the only mutation path for publishing: never `git push`, `gh pr create`, or `gh pr merge` by hand, and never force-push.
- Never commit secrets, generated files, or unrelated changes just to clear `dirty_tree`; ask when in doubt.
- Use `land status --json`, `land validate --json`, `land verify --json`, or `land --no-wait --json` for inspection without waiting or side effects.
- `land submit` publishes without CI follow-up; the bare `land` command is the full wait-and-land loop.
- Merge policy comes from `land.yaml`: `merge.mode: human` (default) stops at `ready_for_merge`; `merge.mode: auto` lets land merge once every check passes.
