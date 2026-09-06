# Git workflow

This document is the canonical Git and GitHub standard for Mailflow.

## Branches

`main` is protected by GitHub branch rules. All changes use a short-lived branch created from the current `origin/main`:

```text
<category>/<kebab-case-description>
```

The allowed categories are:

- `feature/` for new user-visible behavior or capability.
- `bugfix/` for normal defects that can follow the regular release flow.
- `hotfix/` for urgent production corrections created from the production baseline.
- `release/` for release preparation, versioning, and release-only fixes.
- `docs/` for documentation-only changes.

Examples: `docs/git-workflow` and `feature/google-oauth`. This repository-specific rule overrides any global branch-name prefix configured in the client.

Never reuse a merged branch, mix unrelated work, or rewrite a shared branch. Delete the remote branch after merge.

## Commits

Use Conventional Commits:

```text
<type>(optional-scope): <imperative summary>
```

- Use Conventional Commit types such as `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, `chore`, `perf`, and `revert`. Branch categories and commit types are intentionally different vocabularies.
- Keep the subject lower-case, imperative, specific, and at most 72 characters.
- One commit should represent one reviewable idea.
- Add a body when the reason or trade-off is not obvious. Use `BREAKING CHANGE:` only for a real incompatible change.
- Do not amend an existing commit, add attribution trailers, or sign on the user's behalf unless requested.
- Inspect the complete staged diff and run proportionate validation before committing.

## Pull requests

- The title follows Conventional Commits and describes the whole PR.
- The body uses `.github/pull_request_template.md` and records actual validation; never claim checks that were not run.
- Open a draft while required work is incomplete. Mark it ready only when the branch is reviewable.
- Keep the PR scoped. Move unrelated findings to an issue or a later branch.
- Prefer squash merge so `main` receives one coherent commit.

## Review and merge gates

A PR can be approved or merged only after verifying the exact current head SHA and all of these conditions:

1. The base branch is `main`, the PR is open, and it is not a draft.
2. The diff matches the stated scope and contains no secrets, personal data, generated noise, or unexpected files.
3. The relevant validation has passed for that exact head revision.
4. Every required GitHub check is successful; pending, skipped-required, cancelled, stale, or failing checks block the merge.
5. GitHub reports the PR as mergeable with no conflicts.
6. There are no unresolved review threads or active `CHANGES_REQUESTED` reviews.
7. No security or dependency finding requires action before merge.

Approval and merge are different actions. Approve another contributor's PR only after an independent review. Do not approve your own PR or use a self-review as a substitute for required review.

When the user explicitly asks to finish or auto-merge a PR, use GitHub native auto-merge with squash and branch deletion if checks are still running. Merge immediately with squash only when every gate is already satisfied. Never use `--admin`, dismiss a review, weaken a ruleset, force-push, or merge a different head revision from the one reviewed.

## GitHub repository settings

The public repository is configured so that:

- `main` requires a pull request, including for administrators.
- Squash is the only merge strategy and the PR title becomes the commit title.
- Auto-merge is available and merged branches are deleted automatically.
- Required review conversations must be resolved.
- Linear history is required; force-push and branch deletion are disabled on `main`.
- The minimum approval count is zero because this is currently a personal project. Approval is still performed for another contributor when the user requests it and the independent review passes.

No status check is required yet because the documentation-only repository has no CI workflow. Add the real check as required protection as soon as CI exists; never configure a placeholder check that cannot run.

## Evidence in the handoff

Report the branch, commit SHA, PR URL and number, checks reviewed, merge method and resulting merge SHA when each exists. Distinguish clearly between local, pushed, PR-open, auto-merge-enabled, and merged states.
