---
name: mailflow-create-issue
description: "Create a scoped Mailflow GitHub issue from roadmap or operational work before implementation begins. Use when planning the next repository change or when the user asks to create an issue or start roadmap work."
---

# Create a Mailflow issue

Read `../../../docs/roadmap.md` and `../../../docs/git-workflow.md` before acting. Creating an issue changes external state and requires user authorization for that action or an explicitly authorized end-to-end workflow.

1. Inspect open and closed issues, milestones, labels, and current `origin/main`. Verify the requested work is not already implemented, owned by another active issue or PR, or represented by a Dependabot PR.
2. Identify the exact roadmap phase and item. For defects or operational work, describe why it sits outside a roadmap deliverable. Select the matching milestone when one exists.
3. Shape one reviewable unit that can normally be implemented, validated, accepted, and rolled back in one PR. Split independent deliverables or unresolved dependencies into separate issues before implementation. Do not create the complete future backlog; keep a small ready window for the active milestone.
4. Use the matching issue form and complete objective, scope, task checklist, measurable acceptance criteria, validation, dependencies, risks, and out-of-scope work. Write a Conventional Commit-style title describing the outcome.
5. Apply one `type:*`, one priority, and every relevant `area:*` label. Use `status:ready` only when dependencies are actionable; use `status:blocked` instead when they are not.
6. Create the issue, then re-read it to verify title, body, state, labels, milestone, and URL. Report its number and the recommended `<category>/<issue-number>-<slug>` branch. Do not create the branch unless the current task also authorizes it.

Milestones are planning containers, not implementation issues, and do not get branches. Dependabot PRs do not need synthetic issues. Never expose a sensitive vulnerability in a public issue; use a private GitHub Security Advisory.
