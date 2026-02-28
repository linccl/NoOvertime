# PG18 Regression Summary

- Host: 45.207.209.114:5432
- User: user_2me8xA
- Admin DB: postgres
- Run DB: no_overtime_task04_pg18
- Rollback DB: no_overtime_task04_pg18_rb
- SSL Mode: prefer
- server_version_num: 180001
- PG18 Gate: PASS

## Core Checks

- First migration run: PASS (see 10_migration_first_run.out)
- Second migration expected failure (42710): PASS (see 11_migration_second_run.out)
- Rollback drill expected failure (42P01): PASS (see 12_rollback_drill_run.out)
- Rollback clean check: PASS (see 13_rollback_empty_check.out)

## DB Case Suite

- Case file: /Users/linshiyu/lincc/lincc-project/NoOvertime/.swarm-worktrees/backend/db/validation/db_live_cases.sql
- Non-pass IDs from suite output: NONE
- Supplemental C09 (cross transaction): PASS
- Supplemental C10 (cross transaction): PASS

## Artifacts

- Output directory: /Users/linshiyu/lincc/lincc-project/NoOvertime/.swarm-worktrees/backend/artifacts/pg18-regression/20260301-014329
- Fillback template: /Users/linshiyu/lincc/lincc-project/NoOvertime/.swarm-worktrees/backend/docs/templates/PG18回归回写模板.md
