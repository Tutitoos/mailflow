---
name: mailflow-create-pr
description: "Publish and shepherd a Mailflow pull request through GitHub checks and reviewer feedback. Use when the user asks to push a branch, open a PR, or publish completed repository work for review."
---

# Create a Mailflow pull request

Read `../../../docs/git-workflow.md` before acting.

1. Verify authentication, the `origin` repository, branch name, clean task scope, commits ahead of `origin/main`, and that no PR already exists for the branch. Extract the primary issue number from `<category>/<issue-number>-<slug>`, then verify that issue is open and owns this scope.
2. Fetch and compare against the latest `origin/main`. Do not rewrite shared history or silently incorporate unrelated changes.
3. Run the relevant validation again if the branch changed since its last verified run. Every PR that changes `deploy/repos.lock` must include a successful `scripts/repos-lock.sh validate` result and explain the external repository, full SHA, and reason for adding or updating it.
4. Push the current branch normally and set upstream. Never force-push unless the user explicitly authorizes the exact rewrite after seeing its impact.
5. Build the title from the complete branch diff using Conventional Commits.
6. Fill every applicable section of `.github/pull_request_template.md` with concrete evidence. Include exactly one primary `Closes #<issue-number>` matching the branch, address its acceptance criteria, and mark unrun validation honestly.
7. Open a draft if work or required validation remains; otherwise open a ready PR targeting `main`.
8. Re-read the created PR to verify base, head, title, body, state, URL, closing issue link, and branch/issue consistency.
9. Continue with the post-publication review loop in `../mailflow-review-merge-pr/SKILL.md`, even when merge was not requested:
   - Apply one 20-minute maximum observation window per head SHA. If an expected GitHub Actions workflow does not appear or a job remains unavailable when it expires, stop and report the PR as blocked with the exact pending state.
   - Wait for every GitHub Actions job reported for the exact current head SHA to finish.
   - Inspect check failures, reviews, issue comments, inline comments, and unresolved threads rather than relying on the PR summary.
   - Inspect automated reviewer feedback when present, but do not block on an absent, unavailable, or rate-limited optional bot and do not request bot review unless the project explicitly enables it again.
   - Verify every finding against the code. Fix valid in-scope findings, validate, commit with `mailflow-create-commit`, push normally, and restart the loop for the new head.
   - Reply with evidence and resolve a bot thread only after its finding is fixed or demonstrably inapplicable. Never blindly apply bot suggestions.
   - Stop and report when a finding requires a material scope expansion, unavailable credentials, a repository setting, or user authority not already granted.
10. Report the final reviewed head SHA, checks, reviewer feedback, unresolved findings, and merge readiness. Distinguish an open reviewed PR from a merged PR.

Creating a PR does not authorize approving, enabling auto-merge, or merging it.

Dependabot PRs are exempt from creating a synthetic issue. For a private security advisory, preserve confidentiality and use GitHub's advisory linkage instead of a public `Closes` reference.
