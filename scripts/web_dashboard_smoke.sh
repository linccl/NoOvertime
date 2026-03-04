#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:29082}"
PAIRING_CODE="${PAIRING_CODE:-}"
CLIENT_FINGERPRINT="${CLIENT_FINGERPRINT:-}"
WEB_DEVICE_NAME="${WEB_DEVICE_NAME:-Chrome@Local}"
YEAR="${YEAR:-$(date -u +%Y)}"
MONTH_START="${MONTH_START:-$(date -u +%Y-%m-01)}"
DRY_RUN="${DRY_RUN:-0}"

usage() {
  cat <<'EOF'
Web dashboard smoke (bind -> auth -> month/day summaries)

Usage:
  BASE_URL=http://127.0.0.1:29082 \
  PAIRING_CODE=12345678 \
  CLIENT_FINGERPRINT=9cfce7bcd5d6dfac2697fdf1f5b9f226 \
  YEAR=2026 \
  MONTH_START=2026-02-01 \
  ./scripts/web_dashboard_smoke.sh

Notes:
  - Auth uses request body fields: binding_token + client_fingerprint (NOT Authorization header).
  - If PAIRING_CODE / CLIENT_FINGERPRINT is missing, this script prints request samples and exits 0.

Minimal seed (empty local DB after running db/migrations/001_init.sql):
  psql "$DATABASE_DSN" -v ON_ERROR_STOP=1 <<'SQL'
  INSERT INTO users(user_id, pairing_code, recovery_code_hash)
  VALUES ('00000000-0000-0000-0000-000000000001', '12345678', 'dev_hash');
  INSERT INTO month_summaries(id, user_id, month_start, work_minutes_total, adjust_minutes_balance, version)
  VALUES ('40000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-01', 540, 0, 1);
  INSERT INTO day_summaries(
    id, user_id, local_date, start_at_utc, end_at_utc,
    is_leave_day, leave_type, is_late, work_minutes, adjust_minutes, status, version
  ) VALUES (
    '30000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-12',
    '2026-02-12 01:10:00+00', '2026-02-12 10:10:00+00',
    FALSE, NULL, FALSE, 540, 0, 'COMPUTED', 1
  );
  SQL
EOF
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing command: $1" >&2
    exit 1
  }
}

post_json() {
  local path="$1"
  local req_id="$2"
  local payload="$3"

  if [[ "$DRY_RUN" == "1" ]]; then
    echo "curl -sS -X POST '$BASE_URL$path' \\"
    echo "  -H 'Content-Type: application/json' \\"
    echo "  -H 'X-Request-ID: $req_id' \\"
    echo "  -d '$payload'"
    return 0
  fi

  curl -sS -X POST "$BASE_URL$path" \
    -H 'Content-Type: application/json' \
    -H "X-Request-ID: $req_id" \
    -d "$payload"
}

main() {
  require_cmd curl
  require_cmd jq

  if [[ -z "$PAIRING_CODE" || -z "$CLIENT_FINGERPRINT" ]]; then
    usage
    return 0
  fi

  echo "# base_url=$BASE_URL"
  echo "# pairing_code=$PAIRING_CODE year=$YEAR month_start=$MONTH_START"
  echo

  echo "## 1) POST /api/v1/web/read-bindings"
  local bind_payload
  bind_payload="$(jq -cn \
    --arg pairing_code "$PAIRING_CODE" \
    --arg client_fingerprint "$CLIENT_FINGERPRINT" \
    --arg web_device_name "$WEB_DEVICE_NAME" \
    '{pairing_code:$pairing_code, client_fingerprint:$client_fingerprint, web_device_name:$web_device_name}')"
  local bind_resp
  bind_resp="$(post_json "/api/v1/web/read-bindings" "req-web-smoke-bind" "$bind_payload")"
  echo "$bind_resp" | jq .
  local binding_token
  binding_token="$(echo "$bind_resp" | jq -r '.binding_token // empty')"
  if [[ -z "$binding_token" ]]; then
    echo "missing binding_token in response" >&2
    return 1
  fi
  echo

  echo "## 2) POST /api/v1/web/read-bindings/auth"
  local auth_payload
  auth_payload="$(jq -cn \
    --arg binding_token "$binding_token" \
    --arg client_fingerprint "$CLIENT_FINGERPRINT" \
    '{binding_token:$binding_token, client_fingerprint:$client_fingerprint}')"
  post_json "/api/v1/web/read-bindings/auth" "req-web-smoke-auth" "$auth_payload" | jq .
  echo

  echo "## 3) POST /api/v1/web/month-summaries/query"
  local month_payload
  month_payload="$(jq -cn \
    --arg binding_token "$binding_token" \
    --arg client_fingerprint "$CLIENT_FINGERPRINT" \
    --argjson year "$YEAR" \
    '{binding_token:$binding_token, client_fingerprint:$client_fingerprint, year:$year}')"
  post_json "/api/v1/web/month-summaries/query" "req-web-smoke-month" "$month_payload" | jq .
  echo

  echo "## 4) POST /api/v1/web/day-summaries/query"
  local day_payload
  day_payload="$(jq -cn \
    --arg binding_token "$binding_token" \
    --arg client_fingerprint "$CLIENT_FINGERPRINT" \
    --arg month_start "$MONTH_START" \
    '{binding_token:$binding_token, client_fingerprint:$client_fingerprint, month_start:$month_start}')"
  post_json "/api/v1/web/day-summaries/query" "req-web-smoke-day" "$day_payload" | jq .
}

main "$@"
