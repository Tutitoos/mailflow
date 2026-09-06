---
name: mailflow-create-branch
description: "Create a safe Mailflow work branch. Use when starting repository work, switching from main for a change, or when the user asks to create a branch."
---

# Create a Mailflow branch

Read `../../../docs/git-workflow.md` before acting.

1. Inspect `git status --short --branch`, remotes, and existing branches. Stop if switching would overwrite or strand user changes.
2. Fetch `origin` without modifying the worktree.
3. Verify `origin/main` exists. Start from it only when the current worktree is clean and doing so preserves all user work.
4. Choose one allowed category and a concise kebab-case slug. The exact form is `<category>/<slug>`.
   - `feature/` for new behavior.
   - `bugfix/` for a normal defect.
   - `hotfix/` for an urgent production correction.
   - `release/` for release preparation.
   - `docs/` for documentation-only work.
   This project rule overrides the client's global `codex/` prefix.
5. Check that the name does not already exist locally or remotely, then create it.
6. Report the base SHA and new branch. Do not edit, commit, push, or open a PR unless the task also authorizes those actions.

Never delete, reset, stash, rebase, or force-update a branch just to make creation succeed. Ask for direction when existing work makes the safe base ambiguous.
