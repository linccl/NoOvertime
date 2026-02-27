# PG16 Regression Summary

- Host: 45.207.209.114:5432
- User: user_2me8xA
- Admin DB: postgres
- Run DB: no_overtime_task04_pg16
- Rollback DB: no_overtime_task04_pg16_rb
- SSL Mode: prefer
- server_version_num: 180001
- PG16 Gate: FAIL

## Core Checks

- First migration run: PASS (see 10_migration_first_run.out)
- Second migration expected failure (42710): PASS (see 11_migration_second_run.out)
- Rollback drill expected failure (42P01): PASS (see 12_rollback_drill_run.out)
- Rollback clean check: PASS (see 13_rollback_empty_check.out)

## DB Case Suite

- Case file: /Users/linshiyu/lincc/lincc-project/NoOvertime/db/validation/db_live_cases.sql
- Non-pass IDs from suite output: DB-C09 DB-C10 DB-C19 DB-C20 DB-C13 DB-C13 DB-C13 DB-C13 DB-C13 DB-C14 DB-C14 DB-C14 DB-C14 DB-C14 DB-C15 DB-C15 DB-C15 DB-C15 DB-C15
- Supplemental C09 (cross transaction): PASS
- Supplemental C10 (cross transaction): PASS

## Artifacts

- Output directory: /Users/linshiyu/lincc/lincc-project/NoOvertime/artifacts/pg16-regression/20260212-123353
- Fillback template: /Users/linshiyu/lincc/lincc-project/NoOvertime/docs/templates/PG16回归回写模板.md
