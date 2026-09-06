---
name: mailflow-create-pr
description: "Publish a Mailflow pull request. Use when the user asks to push a branch, open a PR, or publish completed repository work for review."
---

# Create a Mailflow pull request

Read `../../../docs/git-workflow.md` before acting.

1. Verify authentication, the `origin` repository, branch name, clean task scope, commits ahead of `origin/main`, and that no PR already exists for the branch.
2. Fetch and compare against the latest `origin/main`. Do not rewrite shared history or silently incorporate unrelated changes.
3. Run the relevant validation again if the branch changed since its last verified run. Every PR that changes `deploy/repos.lock` must include a successful `scripts/repos-lock.sh validate` result and explain the external repository, full SHA, and reason for adding or updating it.
4. Push the current branch normally and set upstream. Never force-push unless the user explicitly authorizes the exact rewrite after seeing its impact.
5. Build the title from the complete branch diff using Conventional Commits.
6. Fill every applicable section of `.github/pull_request_template.md` with concrete evidence. Mark unrun validation honestly.
7. Open a draft if work or required validation remains; otherwise open a ready PR targeting `main`.
8. Re-read the created PR to verify base, head, title, body, state, and URL. Report those values plus the pushed commit SHA.

Creating a PR does not authorize approving, enabling auto-merge, or merging it.
