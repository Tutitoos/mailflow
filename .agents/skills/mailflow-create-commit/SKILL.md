---
name: mailflow-create-commit
description: "Create a verified Mailflow commit. Use when the user asks to commit changes or a requested workflow explicitly includes committing."
---

# Create a Mailflow commit

Read `../../../docs/git-workflow.md` before acting.

1. Confirm the branch is not `main`, matches `<category>/<issue-number>-<slug>`, and points to an open primary issue. Inspect the full repository status, including untracked files.
2. Compare the changes with the primary issue. Separate task changes from pre-existing user changes and from other issues. Stage explicit paths; never use broad staging when unrelated files exist.
3. Review `git diff --cached --stat` and the complete staged diff. Scan filenames and content for secrets, credentials, personal data, temporary files, and the excluded Gmail screenshot.
4. Run validation proportionate to the staged change. Documentation-only changes require at least structural checks and the skill validator when skills changed. If `deploy/repos.lock` or its tooling changed, run `scripts/repos-lock.sh validate`; run `verify` only when all locked external checkouts are expected to exist locally.
5. Write one Conventional Commit subject of at most 72 characters. Add a body only when it contributes useful reasoning.
6. Commit without amending, bypassing hooks, or adding attribution unless explicitly requested.
7. Verify the new commit and remaining worktree. Report the issue, SHA, subject, validation, and any changes deliberately left uncommitted.

Do not create an empty commit. A request to commit does not authorize a push or PR.

Dependabot and private security-advisory branches follow their dedicated linkage rules instead of the public issue-number requirement.
