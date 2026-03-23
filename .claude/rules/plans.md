# Plan File Convention

Plan files live in `docs/plans/`. They are never deleted — they serve as historical records.

## Filename Format

Concise kebab-case. No dates, no issue IDs, no prefixes.
- Good: `provider-fallback-router.md`, `headless-mode.md`
- Bad: `2026-03-22-feature-router.md`

## Required Metadata Block

```markdown
# Descriptive Plan Title

## Date: YYYY-MM-DD
## Status: <status>
## GitHub Issue: #<number>

---
```

## Statuses

| Status | Meaning |
|--------|---------|
| Draft | Actively being designed |
| Open | Design complete, ready for implementation |
| In Progress | Currently being implemented |
| Pending Verification | Code complete, awaiting verification |
| Complete | Verified working |
| Deferred | Postponed |

## Required Sections (in order)

1. Problem Statement
2. Proposed Solution
3. Data Model Changes
4. Specific Scenarios to Cover
5. Implementation Plan
6. Phases & Dependency Graph
7. Risks and Mitigations
8. Scope Boundaries
9. Implementation Checklist

## Workflow

1. Write the plan
2. Create a GitHub issue referencing the plan
3. Get review (code review agent + Copilot PR review)
4. Implement against the plan
5. Update checklist as work progresses
6. Mark plan status as Complete when done
