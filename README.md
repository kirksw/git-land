# land

`land` is a repository-aware Go CLI for moving completed local changes into the shared repository according to a committed `land.yaml` policy.

It provides the same deterministic landing workflow for people and coding agents:

```text
synchronize -> validate -> publish -> verify
```

## End-to-end landing

The default `land` command is a resumable pipeline: each run advances the current branch toward landed as far as policy allows, then reports state.

When continuous integration is pending, `land` polls until checks settle (streaming one observation per poll), then applies the merge policy in the same run; `--no-wait` reports the first observation instead, and `--wait-timeout` overrides the default 30-minute budget.
A timeout is a coherent report — `phase: ci_pending` with a hint to rerun — not an error.

- `land` fetches, validates, gates, pushes the branch, creates or updates the pull request, waits on CI, and applies the merge policy.
- `land status` inspects the branch, integration base, divergence, and working tree.
- `land validate` executes validation commands declared in `land.yaml`.
- `land submit --dry-run` validates and confirms publish eligibility without mutations.
- `land submit` validates, pushes the branch, and creates a GitHub pull request when configured.
- `land verify` reports pull-request and CI status without mutating anything.

Every command accepts `--json` and emits a report whose `phase`, `blockedOn`, and `hint` fields form a machine-readable contract for agent-driven loops; in JSON mode waiting progress streams to stderr so stdout stays a single report document.

Phases progress through `blocked`, `validated`, `published`, `ci_pending`, `ci_failed`, `ready_for_merge`, and `merged` (or `landed` for direct pushes).

## Install and run

With Nix (source-built from this flake, dependencies vendored — no module proxy needed):

```bash
nix run github:kirksw/git-land#land            # latest main
nix profile install github:kirksw/git-land/v0.1#land   # pinned to a tag
```

The flake builds for `x86_64-linux`, `aarch64-linux`, and `aarch64-darwin` (`x86_64-darwin` was dropped by nixpkgs; Intel Mac users can use the release tarballs below).

From a GitHub release (prebuilt tarballs for macOS and Linux, amd64 and arm64):

```bash
curl -fsSLO https://github.com/kirksw/git-land/releases/download/v0.1/land_0.1_linux_amd64.tar.gz
tar -xzf land_0.1_linux_amd64.tar.gz
./land --version   # land version 0.1
```

Or install from source with Go:

```bash
go install github.com/kirksw/git-land/cmd/land@v0.1   # or @latest
```

Then run from a repository root:

```bash
cp land.yaml.example land.yaml
land --json
```

`land --version` always identifies the source: `0.1` for tagged releases, `dev-<sha>` for flake builds from untagged checkouts.

## Releases

Releases are automated with GoReleaser:

- Tagging `v*` (for example `git tag v0.1 && git push origin v0.1`) publishes a GitHub release with `land_<version>_<os>_<arch>.tar.gz` archives and a `SHA256SUMS` file; CI vets and tests before packaging.
- For unreleased changes on main, build directly from source: `nix run github:kirksw/git-land#land` or `go install github.com/kirksw/git-land/cmd/land@main`.
- Every pull request cross-compiles all four targets so architecture breakage fails early.
- Windows is not packaged yet; adding `windows/amd64` to the target lists is a one-line change once its behavior is tested.

## Policy

Bootstrap a policy with `land init`: it detects the integration base and, for Go modules, records `go vet ./...` and `go test ./...`, then writes a commented `land.yaml` to review and commit.
Flags (`--base`, `--lint`, `--test`, `--merge`, `--force`) override the detection; without a policy every command fails with a pointer to `land init`.
Alternatively, copy and adapt `land.yaml.example`.

```yaml
version: 1
base: main

validation:
  lint: go vet ./...
  test: go test ./...

publish:
  strategy: pull_request

merge:
  mode: human
  method: squash
  delete_branch: true
```

Supported publication strategies are `direct_push` and `pull_request`.
A `pull_request` strategy uses `gh pr create --fill`; `gh` must be installed and authenticated whenever land creates, inspects, or merges a pull request.

Merge policy:

- `mode: human` (default) — land stops at `ready_for_merge`; a human merges.
- `mode: auto` — land merges once every check passes (a pull request with no checks counts as passed).
- `method` selects `squash` (default), `merge`, or `rebase` for auto-merges.
- `delete_branch` removes the branch locally and remotely after an auto-merge.

## Agent skill

`.agents/skills/land/SKILL.md` teaches any Agent Skills-compatible harness (pi, Claude Code, Codex) the landing loop: run `land --json`, treat each `blockedOn` as the next authoring instruction, rerun until landed, and stop for human decisions.

## Safety behavior

`land` never stages, commits, rebases, merges locally, resets, or discards work.
Before publishing it requires a clean working tree, a non-base branch, at least one branch commit, and no missing commits from `origin/<base>`.
It always fetches `origin` before acting.
The only mutations it performs are branch pushes, pull-request creation and inspection, and — exclusively under `merge.mode: auto` with all checks passing — the remote merge.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/land
```

## License

Apache-2.0 — see [LICENSE](LICENSE).

## Roadmap

This increment leaves stack modeling, configurable rebase policy, commit-policy enforcement, and non-GitHub forge adapters for later.
