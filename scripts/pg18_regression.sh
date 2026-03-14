#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

HOST="45.207.209.114"
PORT="5432"
USER_NAME="user_2me8xA"
SSLMODE="prefer"
CONNECT_TIMEOUT="5"
ADMIN_DB="postgres"
RUN_DB="no_overtime_task04_pg18"
ROLLBACK_DB="no_overtime_task04_pg18_rb"
ALLOW_NON_PG18=0
CASE_SQL="$REPO_ROOT/db/validation/db_live_cases.sql"
MIGRATION_DIR="$REPO_ROOT/db/migrations"
BASE_MIGRATION_SQL="$REPO_ROOT/db/migrations/001_init.sql"
WORKDIR=""

usage() {
  cat <<'USAGE'
Usage:
  PGPASSWORD='***' scripts/pg18_regression.sh [options]

Options:
  --host <host>                 Default: 45.207.209.114
  --port <port>                 Default: 5432
  --user <user>                 Default: user_2me8xA
  --sslmode <mode>              Default: prefer
  --connect-timeout <seconds>   Default: 5
  --admin-db <db>               Default: postgres
  --run-db <db>                 Default: no_overtime_task04_pg18
  --rollback-db <db>            Default: no_overtime_task04_pg18_rb
  --case-sql <path>             Default: db/validation/db_live_cases.sql
  --workdir <path>              Default: artifacts/pg18-regression/<timestamp>
  --allow-non-pg18              Allow run on non-PG18 server (still marks risk)
  -h, --help                    Show this help
USAGE
}

ensure_identifier() {
  local value="$1"
  local name="$2"
  if [[ ! "$value" =~ ^[a-zA-Z_][a-zA-Z0-9_]*$ ]]; then
    echo "Invalid ${name}: ${value}. Only [a-zA-Z_][a-zA-Z0-9_]* is allowed." >&2
    exit 2
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      HOST="$2"
      shift 2
      ;;
    --port)
      PORT="$2"
      shift 2
      ;;
    --user)
      USER_NAME="$2"
      shift 2
      ;;
    --sslmode)
      SSLMODE="$2"
      shift 2
      ;;
    --connect-timeout)
      CONNECT_TIMEOUT="$2"
      shift 2
      ;;
    --admin-db)
      ADMIN_DB="$2"
      shift 2
      ;;
    --run-db)
      RUN_DB="$2"
      shift 2
      ;;
    --rollback-db)
      ROLLBACK_DB="$2"
      shift 2
      ;;
    --case-sql)
      CASE_SQL="$2"
      shift 2
      ;;
    --workdir)
      WORKDIR="$2"
      shift 2
      ;;
    --allow-non-pg18)
      ALLOW_NON_PG18=1
      shift 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "${PGPASSWORD:-}" ]]; then
  echo "PGPASSWORD is required." >&2
  exit 2
fi

ensure_identifier "$ADMIN_DB" "admin-db"
ensure_identifier "$RUN_DB" "run-db"
ensure_identifier "$ROLLBACK_DB" "rollback-db"

if [[ ! -d "$MIGRATION_DIR" ]]; then
  echo "Migration directory not found: $MIGRATION_DIR" >&2
  exit 2
fi

if [[ ! -f "$BASE_MIGRATION_SQL" ]]; then
  echo "Base migration file not found: $BASE_MIGRATION_SQL" >&2
  exit 2
fi

mapfile -t MIGRATION_FILES < <(find "$MIGRATION_DIR" -maxdepth 1 -type f -name '*.sql' | sort)
if (( ${#MIGRATION_FILES[@]} == 0 )); then
  echo "No migration files found under: $MIGRATION_DIR" >&2
  exit 2
fi

for cmd in psql nc pg_isready awk; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Required command not found: $cmd" >&2
    exit 2
  fi
done

if [[ -z "$WORKDIR" ]]; then
  TS="$(date +%Y%m%d-%H%M%S)"
  WORKDIR="$REPO_ROOT/artifacts/pg18-regression/$TS"
fi
mkdir -p "$WORKDIR"

log() {
  local msg="$1"
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S %z')" "$msg" | tee -a "$WORKDIR/00_runner.log"
}

dsn_for_db() {
  local db="$1"
  printf 'host=%s port=%s dbname=%s user=%s sslmode=%s connect_timeout=%s' \
    "$HOST" "$PORT" "$db" "$USER_NAME" "$SSLMODE" "$CONNECT_TIMEOUT"
}

run_all_migrations() {
  local dsn="$1"
  shift || true

  local migration
  for migration in "${MIGRATION_FILES[@]}"; do
    psql "$dsn" -X -v ON_ERROR_STOP=1 "$@" -f "$migration"
  done
}

ADMIN_DSN="$(dsn_for_db "$ADMIN_DB")"
RUN_DSN="$(dsn_for_db "$RUN_DB")"
RB_DSN="$(dsn_for_db "$ROLLBACK_DB")"

EMPTY_CHECK_SQL_FILE="$WORKDIR/empty_check.sql"
cat >"$EMPTY_CHECK_SQL_FILE" <<'SQL'
WITH ext_owned AS (
  SELECT objid
  FROM pg_depend
  WHERE deptype = 'e'
),
non_system_objects AS (
  SELECT 'table' AS obj_type, c.oid
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
    AND c.relkind IN ('r','p','v','m','S','i')
  UNION ALL
  SELECT 'type' AS obj_type, t.oid
  FROM pg_type t
  JOIN pg_namespace n ON n.oid = t.typnamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
    AND t.typtype = 'e'
  UNION ALL
  SELECT 'function' AS obj_type, p.oid
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
)
SELECT obj_type, count(*) AS cnt
FROM non_system_objects o
LEFT JOIN ext_owned eo ON eo.objid = o.oid
WHERE eo.objid IS NULL
GROUP BY obj_type
ORDER BY obj_type;
SQL

log "Step 1/8: connectivity and client probe"
{
  echo "which psql"
  which psql
  echo
  echo "psql --version"
  psql --version
  echo
  echo "nc -vz -w $CONNECT_TIMEOUT $HOST $PORT"
  nc -vz -w "$CONNECT_TIMEOUT" "$HOST" "$PORT"
  echo
  echo "pg_isready -h $HOST -p $PORT -t $CONNECT_TIMEOUT"
  pg_isready -h "$HOST" -p "$PORT" -t "$CONNECT_TIMEOUT"
} >"$WORKDIR/01_connectivity.out" 2>&1

log "Step 2/8: create fresh databases"
psql "$ADMIN_DSN" -X -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$RUN_DB','$ROLLBACK_DB') AND pid <> pg_backend_pid();" >"$WORKDIR/02_terminate_backends.out" 2>&1
psql "$ADMIN_DSN" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$RUN_DB\";" >"$WORKDIR/03_drop_run_db.out" 2>&1
psql "$ADMIN_DSN" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$RUN_DB\";" >"$WORKDIR/04_create_run_db.out" 2>&1
psql "$ADMIN_DSN" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$ROLLBACK_DB\";" >"$WORKDIR/05_drop_rb_db.out" 2>&1
psql "$ADMIN_DSN" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$ROLLBACK_DB\";" >"$WORKDIR/06_create_rb_db.out" 2>&1

log "Step 3/8: precheck and PG18 gate"
psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "SHOW server_version_num; SHOW server_version; SHOW TimeZone;" >"$WORKDIR/07_precheck_version.out" 2>&1
SERVER_VERSION_NUM="$(psql "$RUN_DSN" -X -At -v ON_ERROR_STOP=1 -c "SHOW server_version_num;" | tr -d '[:space:]')"
if [[ ! "$SERVER_VERSION_NUM" =~ ^[0-9]+$ ]]; then
  echo "Invalid server_version_num: $SERVER_VERSION_NUM" >&2
  exit 2
fi

if (( SERVER_VERSION_NUM < 180000 || SERVER_VERSION_NUM >= 190000 )); then
  if (( ALLOW_NON_PG18 == 1 )); then
    log "Warning: server_version_num=$SERVER_VERSION_NUM, outside PG18 gate, continue by --allow-non-pg18"
  else
    log "Error: server_version_num=$SERVER_VERSION_NUM, requires PG18 [180000,190000)."
    echo "Re-run with --allow-non-pg18 only for temporary PG18 evidence collection." >&2
    exit 3
  fi
fi

psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 >"$WORKDIR/08_precheck_extensions.out" 2>&1 <<'SQL'
SELECT e.extname, n.nspname AS extnamespace
FROM pg_extension e
JOIN pg_namespace n ON n.oid = e.extnamespace
WHERE e.extname IN ('plpgsql', 'pgcrypto')
ORDER BY e.extname;

BEGIN;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
ROLLBACK;

SELECT e.extname, n.nspname AS extnamespace
FROM pg_extension e
JOIN pg_namespace n ON n.oid = e.extnamespace
WHERE e.extname IN ('plpgsql', 'pgcrypto')
ORDER BY e.extname;
SQL

psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -f "$EMPTY_CHECK_SQL_FILE" >"$WORKDIR/09_precheck_empty_db.out" 2>&1

log "Step 4/8: first migration run"
run_all_migrations "$RUN_DSN" >"$WORKDIR/10_migration_first_run.out" 2>&1

log "Step 5/8: second migration run expected failure"
set +e
run_all_migrations "$RUN_DSN" --set=VERBOSITY=verbose >"$WORKDIR/11_migration_second_run.out" 2>&1
SECOND_RC=$?
set -e
if (( SECOND_RC == 0 )); then
  log "Error: second run unexpectedly succeeded"
  exit 4
fi
if ! grep -q "ERROR:  42710" "$WORKDIR/11_migration_second_run.out"; then
  log "Error: second run failed but SQLSTATE 42710 not found"
  exit 4
fi

log "Step 6/8: rollback drill expected failure + clean check"
awk '{if($0=="COMMIT;"){print "SELECT * FROM __force_failure_relation_not_exists__;"} print}' "$BASE_MIGRATION_SQL" >"$WORKDIR/001_init_fail_before_commit.sql"
set +e
psql "$RB_DSN" -X --set=VERBOSITY=verbose -v ON_ERROR_STOP=1 -f "$WORKDIR/001_init_fail_before_commit.sql" >"$WORKDIR/12_rollback_drill_run.out" 2>&1
RB_RC=$?
set -e
if (( RB_RC == 0 )); then
  log "Error: rollback drill unexpectedly succeeded"
  exit 5
fi
if ! grep -q "ERROR:  42P01" "$WORKDIR/12_rollback_drill_run.out"; then
  log "Error: rollback drill failed but SQLSTATE 42P01 not found"
  exit 5
fi

psql "$RB_DSN" -X -v ON_ERROR_STOP=1 -At -f "$EMPTY_CHECK_SQL_FILE" >"$WORKDIR/13_rollback_empty_check.out" 2>&1
if [[ -n "$(tr -d '[:space:]' <"$WORKDIR/13_rollback_empty_check.out")" ]]; then
  log "Error: rollback database is not clean"
  exit 5
fi

log "Step 7/8: object count check"
psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 >"$WORKDIR/14_object_counts.out" 2>&1 <<'SQL'
WITH core_enums AS (
  SELECT unnest(ARRAY['punch_type','punch_source','leave_type','device_status','migration_mode','migration_status','summary_status','security_scene']) AS name
), core_tables AS (
  SELECT unnest(ARRAY['users','devices','punch_records','leave_records','day_summaries','month_summaries','migration_requests','security_attempt_windows','sync_commits','mobile_tokens','web_read_bindings']) AS name
), core_indexes AS (
  SELECT unnest(ARRAY['idx_devices_user_status','uk_punch_active_unique','idx_punch_user_date','idx_punch_user_updated','uk_leave_active_unique','idx_leave_user_date','uk_day_summary_user_date','idx_day_summary_user_date','uk_month_summary_user_month','idx_month_summary_user_month','idx_migration_user_status','uk_migration_user_pending','idx_security_blocked_until','idx_sync_commits_user_created','uq_mobile_tokens_active_user','idx_web_binding_user_status']) AS name
), core_functions AS (
  SELECT unnest(ARRAY['validate_punch_pair','revoke_web_bindings_on_pairing_change','rotate_pairing_code','enforce_migration_status_transition','validate_web_binding_version','validate_sync_commit_writer','normalize_security_window_start','validate_auto_punch_not_on_full_day_leave','validate_full_day_leave_without_auto_punch','validate_record_user_id_immutable']) AS name
), core_triggers AS (
  SELECT unnest(ARRAY['trg_validate_punch_pair','trg_revoke_web_bindings_on_pairing_change','trg_enforce_migration_status_transition','trg_validate_web_binding_version','trg_validate_sync_commit_writer','trg_normalize_security_window_start','trg_validate_auto_punch_not_on_full_day_leave','trg_validate_full_day_leave_without_auto_punch','trg_punch_user_id_immutable','trg_leave_user_id_immutable']) AS name
)
SELECT 'enum' AS object_type, count(*) AS matched_count FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN core_enums c ON c.name=t.typname WHERE n.nspname='public' AND t.typtype='e'
UNION ALL
SELECT 'table', count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace JOIN core_tables c ON c.name=t.relname WHERE n.nspname='public' AND t.relkind='r'
UNION ALL
SELECT 'index', count(*) FROM pg_class i JOIN pg_namespace n ON n.oid=i.relnamespace JOIN core_indexes c ON c.name=i.relname WHERE n.nspname='public' AND i.relkind='i'
UNION ALL
SELECT 'function', count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN core_functions c ON c.name=p.proname WHERE n.nspname='public'
UNION ALL
SELECT 'trigger', count(*) FROM pg_trigger tg JOIN pg_class t ON t.oid=tg.tgrelid JOIN pg_namespace n ON n.oid=t.relnamespace JOIN core_triggers c ON c.name=tg.tgname WHERE n.nspname='public' AND NOT tg.tgisinternal
UNION ALL
SELECT 'extension', count(*) FROM pg_extension WHERE extname='pgcrypto';
SQL

CASE_FAIL_IDS="SKIPPED"
C09_STATUS="SKIPPED"
C10_STATUS="SKIPPED"

if [[ -f "$CASE_SQL" ]]; then
  log "Step 8/8: DB case suite + supplemental C09/C10"
  psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -f "$CASE_SQL" >"$WORKDIR/15_cases_suite.out" 2>&1

  CASE_FAIL_IDS="$(awk -F'|' '/^ DB-C[0-9]+/{id=$1; pass=$2; gsub(/ /,"",id); gsub(/ /,"",pass); if(pass=="t" || pass=="f"){if(pass!="t"){printf "%s ", id}}}' "$WORKDIR/15_cases_suite.out" | sed 's/[[:space:]]*$//')"
  if [[ -z "$CASE_FAIL_IDS" ]]; then
    CASE_FAIL_IDS="NONE"
  fi

  set +e
  {
    psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "SELECT _reset_base_data();"
    psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at) VALUES ('70000000-0000-0000-0000-000000009009','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '1 second');"
    sleep 2
    psql "$RUN_DSN" -X --set=VERBOSITY=verbose -v ON_ERROR_STOP=1 -c "UPDATE migration_requests SET status='CONFIRMED' WHERE id='70000000-0000-0000-0000-000000009009';"
  } >"$WORKDIR/16_case_c09_cross_tx.out" 2>&1
  C09_RC=$?
  set -e
  if (( C09_RC != 0 )) && grep -q "ERROR:  P0001" "$WORKDIR/16_case_c09_cross_tx.out" && grep -q "MIGRATION_TRANSITION_INVALID" "$WORKDIR/16_case_c09_cross_tx.out"; then
    C09_STATUS="PASS"
  else
    C09_STATUS="FAIL"
  fi

  {
    psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "SELECT _reset_base_data();"
    psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at) VALUES ('70000000-0000-0000-0000-000000009010','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '1 second');"
    sleep 2
    psql "$RUN_DSN" -X -v ON_ERROR_STOP=1 -c "INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at) VALUES ('70000000-0000-0000-0000-000000009011','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000103','NORMAL','PENDING',FALSE, now() + interval '30 minutes'); SELECT id,status FROM migration_requests WHERE id IN ('70000000-0000-0000-0000-000000009010','70000000-0000-0000-0000-000000009011') ORDER BY id;"
  } >"$WORKDIR/17_case_c10_cross_tx.out" 2>&1
  if grep -q "70000000-0000-0000-0000-000000009010 | EXPIRED" "$WORKDIR/17_case_c10_cross_tx.out" && grep -q "70000000-0000-0000-0000-000000009011 | PENDING" "$WORKDIR/17_case_c10_cross_tx.out"; then
    C10_STATUS="PASS"
  else
    C10_STATUS="FAIL"
  fi
else
  log "Warning: case SQL not found: $CASE_SQL, skip DB-C01~DB-C28 batch"
fi

SUMMARY_FILE="$WORKDIR/99_summary.md"
cat >"$SUMMARY_FILE" <<SUMMARY
# PG18 Regression Summary

- Host: $HOST:$PORT
- User: $USER_NAME
- Admin DB: $ADMIN_DB
- Run DB: $RUN_DB
- Rollback DB: $ROLLBACK_DB
- SSL Mode: $SSLMODE
- server_version_num: $SERVER_VERSION_NUM
- PG18 Gate: $([[ $SERVER_VERSION_NUM -ge 180000 && $SERVER_VERSION_NUM -lt 190000 ]] && echo PASS || echo FAIL)

## Core Checks

- First migration run: PASS (see 10_migration_first_run.out)
- Second migration expected failure (42710): PASS (see 11_migration_second_run.out)
- Rollback drill expected failure (42P01): PASS (see 12_rollback_drill_run.out)
- Rollback clean check: PASS (see 13_rollback_empty_check.out)

## DB Case Suite

- Case file: $CASE_SQL
- Non-pass IDs from suite output: $CASE_FAIL_IDS
- Supplemental C09 (cross transaction): $C09_STATUS
- Supplemental C10 (cross transaction): $C10_STATUS

## Artifacts

- Output directory: $WORKDIR
- Fillback template: $REPO_ROOT/docs/templates/PG18回归回写模板.md
SUMMARY

log "Completed. Summary: $SUMMARY_FILE"
printf '%s\n' "$WORKDIR"
