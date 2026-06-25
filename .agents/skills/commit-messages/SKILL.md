---
name: commit-messages
description: Commit message conventions for this repo — load before writing any git commit or PR title. Commits drive the generated release notes.
---
## Format

Use [Conventional Commits](https://www.conventionalcommits.org/), plain and minimal:

```
<type>: <short description>
```

- **No scope.** Write `feat: ...`, not `feat(ci): ...`. We do not use the optional `(scope)` — it is stripped from the release notes anyway and only adds noise.
- Description in the imperative mood, lower-case start, no trailing period: `fix: correct ldflags package path`.
- Keep the subject line concise; add a body (after a blank line) only when the *why* isn't obvious.

## Types and their effect on release notes

Release notes are generated from commit messages by git-cliff (see [cliff.toml](../../../cliff.toml)). **Only two types appear in the notes:**

| Type     | Shows in notes under | Use for |
|----------|----------------------|---------|
| `feat`   | **Features**         | a new user-facing capability |
| `fix`    | **Bug Fixes**        | a user-facing bug fix |

All other types are valid and encouraged for clean history, but are **hidden** from release notes: `refactor`, `perf`, `docs`, `style`, `test`, `build`, `ci`, `chore`. Pick the most accurate type — don't inflate something to `feat`/`fix` just to make it appear in the notes.

## Breaking changes

Mark a breaking change with `!` after the type (e.g. `feat!: drop legacy config format`) or a `BREAKING CHANGE:` footer. This also drives the automatic major version bump in the release workflow.

## Examples

```
feat: drain run backlog back-to-back with a job capacity guard
fix: prefer API key auth when basic auth is also configured
refactor: extract image build into a reusable workflow   # hidden from notes
ci: pin release tooling to a verified checksum            # hidden from notes
feat!: require run-controller API key from environment    # major bump
```
