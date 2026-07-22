# Design docs & project documentation

All project documentation lives in `/docs` (git-ignored — local working docs,
not committed).

```
docs/
  <YYYYMMDD_HHMM>_<name>/   # one folder per design doc / plan (active work)
    plan.md                 # the plan itself (+ diagrams, images, …)
  archive/                  # implemented plans move here, folder and all
  bugs/                     # docs for bug investigations / fixes
  infra/                    # docs about the project infrastructure
```

## Rules

- Create a new design-doc folder with `make docs n=<name>` — it stamps the
  `YYYYMMDD_HHMM_<name>` prefix. Keep that naming; the prefix is the history.
- **Every `plan.md` starts with a status line** (before the title):
  `status: draft | approved | done`.
  - `draft` — being written / under discussion; do not implement from it.
  - `approved` — the user validated it; implementation can proceed.
  - `done` — fully implemented.
- **Finishing a plan means archiving it**: when every part of a plan is
  implemented, set `status: done` and move the whole folder to `docs/archive/`.
  This is part of completing the work, not a separate cleanup task.
- Long-running plans (multiple slices) keep a **Progress** section at the
  bottom of `plan.md`, updated as each slice lands; the folder stays in
  `docs/` until the last slice is done.
- Bug write-ups go to `docs/bugs/` as single files; infrastructure notes and
  diagrams to `docs/infra/`.
