---
name: commit-messages
description: Commit message conventions for this repo — load before writing any git commit or PR title. Commits drive the generated release notes.
---
## Format

Use [Conventional Commits](https://www.conventionalcommits.org/), plain and minimal:

```
<type>: <short description>
```

- **No scope.** Write `feat: ...`, not `feat(ci): ...`. We do not use the optional `(scope)` — the release notes ignore it and it only adds noise.
- Description in the imperative mood, lower-case start, no trailing period: `fix: correct ldflags package path`.
- Keep the subject line concise; add a body (after a blank line) only when the *why* isn't obvious.

## Types and their effect on release notes

A ruleset on `main` accepts only the types below and rejects a title with any other type, such as `style:`.
`tools/releaser` builds the release notes from the commit titles (see the **`release-notes`** skill). **Only two types
appear in the notes:**

| Type     | Shows in notes under | Use for |
|----------|----------------------|---------|
| `feat`   | **Features**         | a new capability that an operator or a building block author notices |
| `fix`    | **Fixes**            | a bug fix that an operator or a building block author notices |

All other types are valid and encouraged for clean history, but are **hidden** from release notes: `refactor`, `perf`,
`docs`, `test`, `build`, `ci`, `chore`, `revert`. Pick the most accurate type — don't inflate something to `feat`/`fix`
just to make it appear in the notes. Each `feat` commit, `fix` commit and commit with `!` needs a leading blockquote in the pull request
description, which the **`release-notes`** skill describes.

## Breaking changes

Mark a breaking change with `!` after the type (e.g. `feat!: drop legacy config format`). The commit gets an entry
under **Breaking changes**, whatever its type, and the next version raises the major version. Explain what an operator
must change in a `BREAKING CHANGE:` footer; the footer alone does not mark the commit as breaking.

## Examples

```
feat: drain run backlog back-to-back with a job capacity guard
fix: prefer API key auth when basic auth is also configured
refactor: extract image build into a reusable workflow   # hidden from notes
ci: pin release tooling to a verified checksum            # hidden from notes
feat!: require run-controller API key from environment    # breaking, major bump
```
