# Mailflow agent rules

These instructions apply to the entire repository.

## Git workflow

- Read `docs/git-workflow.md` before changing branches, committing, opening a pull request, reviewing, or merging.
- Use the matching repository skill in `.agents/skills/` for each Git operation.
- Create or select a scoped GitHub issue before starting implementable work, and use `mailflow-create-issue` when creating it.
- Never commit directly to `main`. Start work from an up-to-date `origin/main` on a `feature/`, `bugfix/`, `hotfix/`, `release/`, or `docs/` branch named `<category>/<issue-number>-<slug>`.
- Keep one primary issue per branch and pull request. Every non-bot PR must include `Closes #<issue-number>` for that issue.
- Keep user changes intact. Do not discard, rewrite, amend, force-push, or rebase work you do not own.
- Do not commit secrets, credentials, personal data, generated artifacts, or the Gmail reference screenshot.
- Keep Mailflow first-party code in the monorepo. Use `deploy/repos.lock` only for unavoidable external source repositories and run `scripts/repos-lock.sh validate` whenever it changes.
- Never edit a lock SHA without verifying the exact remote commit. Do not add submodules, gitlinks, branches, tags, or abbreviated SHAs to `repos.lock`.
- A request to implement a change authorizes local edits, not publishing or merging. Push, open a PR, approve, enable auto-merge, or merge only when the user requests that action in the current task.
- Never bypass protections with admin privileges. A PR may merge only when its exact head revision has passed the gates in `docs/git-workflow.md`.
- Dependabot PRs do not require a separate issue. Sensitive vulnerabilities use a private security advisory instead of a public issue.

## Project status

Mailflow is currently documentation-first. Do not introduce an application scaffold until the user explicitly starts an implementation phase from `docs/roadmap.md`.
