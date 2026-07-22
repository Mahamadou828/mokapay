---
name: review-code
description: Review a diff, branch, or PR for correctness bugs, security issues, concurrency problems, resource leaks, and violations of the workspace coding rules. Use when asked to review code, review changes, review a PR, or check pending work before commit.
---

# Code review

Review the requested changes against the checklists below and the workspace
rules. Findings must be **concrete**: file, line, what breaks, and how.

## 1. Determine the diff scope

- No argument → review the working tree: `git diff` (unstaged) + `git diff --cached` (staged); if both are empty, review the current branch against the default branch: `git diff <default-branch>...HEAD`.
- A branch or PR number was given → diff that instead (`gh pr diff <n>` for PRs).
- Read every changed file in full, not just the hunks — bugs live in the
  interaction between the change and its surroundings.

## 2. Load the review criteria

Read, in this order:

1. [references/universal-checklist.md](references/universal-checklist.md) — always.
2. [references/go-checklist.md](references/go-checklist.md) — if the diff touches `.go` files.
3. The workspace rules that apply to the touched files:
   - Go files → `${CLAUDE_SKILL_DIR}/../../rules/go-coding-style.md`
   - SQL files (`db/` migrations, views, procedures, queries) → `${CLAUDE_SKILL_DIR}/../../rules/sql.md`
   - Frontend files (`.tsx`, `.ts` under a React app) → `${CLAUDE_SKILL_DIR}/../../rules/frontend-structure.md`

   If the consuming project has its own `.claude/rules/` versions of these
   files, prefer those — project rules override workspace rules.

## 3. Review

Work file by file. For each finding, verify it is real before reporting:
trace the actual code path, don't pattern-match. A finding that depends on
"if X happens elsewhere" requires checking whether X actually happens.

Check in this order of importance:

1. **Correctness** — does the change do what it claims? Broken edge cases,
   inverted conditions, off-by-one, wrong variable, unhandled nil/zero values.
2. **Security & data safety** — from the universal checklist.
3. **Concurrency & resources** — from the language checklist.
4. **Rule violations** — anything that contradicts the loaded rules files.
   Cite the rule.
5. **Simplification** — only flag when the simpler form is clearly better;
   no style nitpicks already covered by `gofmt`/linters.

## 4. Report

Order findings most-severe first. For each:

```
<severity: blocker | major | minor> — <file>:<line>
<one-sentence defect statement>
<concrete failure scenario: inputs/state → wrong outcome>
<suggested fix, one line or a short snippet>
```

- If a finding violates a rules file, name the rule section.
- If nothing survives verification, say so plainly — do not pad the report
  with speculative or cosmetic findings.
- Do not apply fixes unless the user asked for that.
