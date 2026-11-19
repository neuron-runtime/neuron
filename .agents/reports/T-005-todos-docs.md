# Documentation — T-005-todos

status: complete

## Updated
- `todos/TODO.md` — Checked exactly the requested verified Phase 5 items (R5-1, R5-2, R5-3, R5-16, R5-18, R5-19, R5-20 and F5-1, F5-4, F5-7, F5-8), appending closing commit(s) and the `scripts/script.sh` result at `cf3e6a6` to each. Appended F5-9 and F5-10 as unchecked findings. No other file or phase touched.

## Verified against
- `git log --oneline -30` — confirms commits `2b9dcaa`, `7e7e633`, `059cae7`, `ffa4d57`, `e7ab02e`, `157ac66`, `18dc6a6`, `100d9dd`, `0a4b42d`, `3d2e6d5`, `23aca93`, `f82ffa6`, `a78a366` on this branch.
- `.agents/ledger.yaml` T-020/T-021/T-022/T-023/T-024/T-034/T-035/T-040 — status `done` with the commit mapping used for evidence annotations; validation results corroborate the brief's claim that `scripts/script.sh` passed in full at `cf3e6a6`.
- `.agents/reports/T-024.md` — `2b9dcaa` adds the typed wait model, `StatusWaiting`, in-flight exclusion and snapshot v3; `7e7e633` adds atomic `SaveWaitTransition` committing wait effects in one transaction.
- `.agents/reports/T-024c1b1.md` — `ffa4d57` makes cancellation terminal, retains wait metadata, discards join buffers; confirms `scripts/script.sh` was not run by that slice (full-run result taken from the brief/ledger).
- `.agents/reports/phase5-implementation-plan.md:139-168` — closure map corroborates the task-to-item ownership used for annotations.
- `git status --short` — only `todos/TODO.md` modified in the codebase.
- `git diff --check` — clean.
- `git diff --numstat -- todos/TODO.md` — 27 additions, 11 deletions, all within Phase 5 lines.

## Not updated
- Source code, tests, config, manifests — outside lease and role boundary.
- R5-4..R5-15, R5-17, R5-21..R5-28, F5-2, F5-3, F5-5, F5-6 — explicitly out of scope; their closing tasks (T-025/T-027/T-028/T-029/T-030/T-031/T-032) are not complete.
- `R5-11` — already checked by T-039/T-022; left untouched.
- Phases 1–4, 6, 7 — out of scope.

## Unverified or ambiguous
- F5-9/F5-10 defect details are transcribed from the brief's `reviewer T-024c1b1-review` evidence; the standalone review report file was not present in `.agents/reports/`, so the reviewer claims were not independently re-derived from code. They remain unchecked, as instructed.
- `scripts/script.sh` was not re-run by this task; its pass result is taken from the verified brief/ledger context at HEAD `cf3e6a6`.
- No PostgreSQL behavior is claimed.

## Links and examples checked
- No links or code examples added; evidence references are commit hashes and the same `scripts/script.sh` path already used in the file.
- `grep -n 'R5-\|F5-' todos/TODO.md` — verified final checkbox states: exactly the 11 requested existing items are `[x]`, all other requested-unchecked items and F5-9/F5-10 are `[ ]`.

## Next safe action
- Coordinator to reconcile this checkbox state with T-031 final documentation synchronization and keep F5-9/F5-10 open until T-041/T-042 land. No commit made.
