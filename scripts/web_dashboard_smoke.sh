#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:29082}"
MOBILE_TOKEN="${MOBILE_TOKEN:-}"
CLIENT_FINGERPRINT="${CLIENT_FINGERPRINT:-}"
YEAR="${YEAR:-$(date -u +%Y)}"
MONTH_START="${MONTH_START:-$(date -u +%Y-%m-01)}"
DRY_RUN="${DRY_RUN:-0}"

usage() {
  cat <<'EOF'
Web dashboard smoke (token-only month/day summaries)

Usage:
  BASE_URL=http://127.0.0.1:29082 \
  MOBILE_TOKEN=tok_xxx \
  CLIENT_FINGERPRINT=9cfce7bcd5d6dfac2697fdf1f5b9f226 \
  YEAR=2026 \
  MONTH_START=2026-02-01 \
  ./scripts/web_dashboard_smoke.sh

Notes:
  - Since 2026-03-14, Web read-only queries use Authorization: Bearer <mobile_token>.
  - /api/v1/web/read-bindings and /api/v1/web/read-bindings/auth are paused and return 410 FEATURE_PAUSED.
  - If MOBILE_TOKEN is missing, this script prints request samples and exits 0.
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
    echo "  -H 'Authorization: Bearer $MOBILE_TOKEN' \\"
    if [[ -n "$CLIENT_FINGERPRINT" ]]; then
      echo "  -H 'X-Client-Fingerprint: $CLIENT_FINGERPRINT' \\"
    fi
    echo "  -H 'X-Request-ID: $req_id' \\"
    echo "  -d '$payload'"
    return 0
  fi

  local curl_args=(
    -sS
    -X POST "$BASE_URL$path"
    -H 'Content-Type: application/json'
    -H "Authorization: Bearer $MOBILE_TOKEN"
    -H "X-Request-ID: $req_id"
    -d "$payload"
  )
  if [[ -n "$CLIENT_FINGERPRINT" ]]; then
    curl_args+=(-H "X-Client-Fingerprint: $CLIENT_FINGERPRINT")
  fi

  curl "${curl_args[@]}"
}

main() {
  require_cmd curl
  require_cmd jq

  if [[ -z "$MOBILE_TOKEN" ]]; then
    usage
    return 0
  fi

  echo "# base_url=$BASE_URL"
  echo "# token_only year=$YEAR month_start=$MONTH_START"
  echo

  echo "## 1) POST /api/v1/web/month-summaries/query"
  local month_payload
  month_payload="$(jq -cn --argjson year "$YEAR" '{year:$year}')"
  post_json "/api/v1/web/month-summaries/query" "req-web-smoke-month" "$month_payload" | jq .
  echo

  echo "## 2) POST /api/v1/web/day-summaries/query"
  local day_payload
  day_payload="$(jq -cn --arg month_start "$MONTH_START" '{month_start:$month_start}')"
  post_json "/api/v1/web/day-summaries/query" "req-web-smoke-day" "$day_payload" | jq .
}

main "$@"
