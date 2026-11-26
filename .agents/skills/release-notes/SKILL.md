---
name: release-notes
description: Write the release note entries of a pull request, preview the notes of the next release, pick the next version and tag it. Use when opening or editing a pull request that has a feat, fix or breaking commit, when the release notes preview warns about a blockquote count, when asked what the next version is, when asked to release or tag main, or when a published release note is wrong.
---

# Release notes & versioning

The repository has no changelog file. When a `vX.Y.Z` tag is pushed, `tools/releaser` builds the
GitHub release notes from the commits on `main` since the previous tag, and the `Release` workflow
publishes them. `main` is always ready to tag, so every pull request carries its own entries.

## Which commits get an entry

Pull requests are rebase-merged, so each commit of a pull request lands on `main` with its own
title. A commit gets one entry when its title has:

| Title | Section of the notes |
|---|---|
| a `!` before the colon, with any type, such as `feat!:` or `build!:` | Breaking changes |
| `feat:` | Features |
| `fix:` | Fixes |

Every other commit, such as `docs:`, `test:`, `ci:`, `chore:`, `build:` or `refactor:`, gets no
entry. Choose the type by what an operator of a runner or the author of a building block definition
notices: a change that neither notices is no `feat` and no `fix`. The **`commit-messages`** skill
holds the title format.

## Write the entries in the pull request description

Start the pull request description with one blockquote per entry commit, in the order of the
commits. A blank line separates two blockquotes. The first line of normal text ends the entries.

<example>
A pull request with the commits `fix: keep the tofu cache after an aborted install`,
`test: cover the aborted install` and `feat: retry a GitLab trigger on 429` has two entry commits,
so its description starts with two blockquotes:

```markdown
> tf runs: a run whose tofu install is aborted no longer leaves a broken version in `tf.cacheDir`.
> The next run that needs this version installs it again.

> `gitlab-block-runner`: a pipeline trigger that GitLab answers with 429 is retried, so a busy
> GitLab no longer fails the run.

## Summary

The rest of the description is free text.
```
</example>

- **The count must match.** The releaser uses the blockquotes of a pull request only when their
  number equals the number of its entry commits. With any other number it uses none of them and
  prints a warning.
- **Only the leading blockquotes count.** Put every other blockquote, such as a quoted error
  message, below the first line of normal text. A blockquote above that line is taken as an entry.
- **A pull request written by an AI agent keeps `> 🤖 _Written by AI agent_` as its first line.** The
  entries follow it as the next blockquotes, separated from it by a blank line. The releaser skips
  every leading blockquote that starts with 🤖: it is no entry and does not count.
- An entry can have several lines and several paragraphs. Keep paragraphs in one blockquote with a
  `>` line between them.
- Wrap the lines of an entry freely, in the pull request description and in a commit body. The
  releaser joins the lines of a paragraph, because GitHub shows every newline of a release body as
  a line break. Code blocks and lists stay as written.

### Wording of an entry

- Name what is affected first: the runner type or image, such as `` `gitlab-block-runner`: `` or
  `tf runs:`, or the config key, such as `` `dispatcher.timeout`: ``. Write `All runners:` for a
  change to every runner.
- Describe what the operator or the building block author notices. For a fix, name the symptom that
  goes away. For a breaking change, say what the operator must change: the config key, the
  environment variable, the mounted file, the port or the image.
- Write complete sentences in present tense. Leave out the internal diff.
- The first line of the notes, `Requires meshStack X or later.`, is written by the releaser from
  `client.MinMeshStackVersion` of meshstack-cli. When one feature needs a newer meshStack than that,
  say so as plain text in its entry.

## Where the releaser takes the text from

For each entry commit the releaser takes the first text that exists:

1. The matching leading blockquote of the pull request description.
2. The leading blockquotes of the commit message body.
3. The commit title without its `type!: ` prefix.

The pull request description wins over the commit body. GitHub is read when the tag is pushed, so
the description of a merged pull request can still be edited until then. Each entry ends with its
pull request number and the GitHub handle of the commit author, such as `(#421 by @octocat)`. The
releaser takes the handle from the commit, so a commit by another person than the one who opened
the pull request names its own author. A commit that was pushed without a pull request shows its
short commit id.

An entry also names the issues that GitHub linked to its pull request as closed, such as
`(#423 by @octocat, closes #389)`. Link an issue with a closing keyword, such as `Fixes #389`. The
preferred place is the body of the commit that fixes the issue, because this ties the issue to the
entry of that commit. A keyword in the pull request description also links the issue. Such an issue
is shown when the pull request has one entry commit, or when exactly one of its entry commits has no
issue of its own.

## Preview the notes

```bash
GOWORK=off go run ./tools/releaser "$(git describe --tags --abbrev=0 HEAD^)..HEAD"
```

The notes go to stdout. The warnings and the next version go to stderr. The releaser needs a token
in `GITHUB_TOKEN` or `GH_TOKEN` to read the pull requests, the authors and the issues from GitHub;
`export GH_TOKEN=$(gh auth token)` sets one. Without a token it warns, and every entry uses the
commit text. The `Release Notes Preview` workflow runs the same command on every push to `main` and
shows the notes in its run summary.

Fix a warning about a blockquote count by editing the pull request description, then run the
preview again. The releaser reads merged pull requests only, so the commits of an open pull request
use the commit text.

## Pick the version

The runner follows semantic versioning. Since the last tag:

- a commit with `!` raises the major version (`v1.9.1` → `v2.0.0`);
- else a `feat` commit raises the minor version (`v2.0.0` → `v2.1.0`);
- else the patch version goes up (`v2.1.0` → `v2.1.1`).

The preview prints the version and its reason, such as
`Next version: v2.1.0 (feature since v2.0.0: 1a2b3c4 feat: ...)`. The Go module path ends in the
major version, so a new major version also changes the module path in `go.mod` and in every import.

## Tag and release

1. Run the preview on an up-to-date `main` and read the notes.
2. Push the tag on `main`:

   ```bash
   git push origin main:refs/tags/vX.Y.Z
   ```

The `Release` workflow writes the notes, publishes the release and pushes the images. A wrong commit
title on `main` stays as it is, because the history of `main` is not rewritten. When it put an entry
into the wrong section or left one out, correct the notes by hand on the published release page.
