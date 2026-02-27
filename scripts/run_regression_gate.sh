#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RUNNER="${REGRESSION_RUNNER:-$REPO_ROOT/scripts/pg18_regression.sh}"

RUNNER_ARGS=()
if (( $# > 0 )); then
  RUNNER_ARGS=("$@")
fi

has_runner_option() {
  local option="$1"
  local token
  for token in ${RUNNER_ARGS[@]+"${RUNNER_ARGS[@]}"}; do
    case "$token" in
      "$option"|"$option="*)
        return 0
        ;;
    esac
  done
  return 1
}

append_runner_option_if_absent() {
  local option="$1"
  local value="$2"
  if [[ -n "$value" ]] && ! has_runner_option "$option"; then
    RUNNER_ARGS+=("$option" "$value")
  fi
}

append_runner_flag_if_absent() {
  local flag="$1"
  local enabled="$2"
  if has_runner_option "$flag"; then
    return 0
  fi
  case "$enabled" in
    1|true|TRUE|yes|YES|on|ON)
      RUNNER_ARGS+=("$flag")
      ;;
  esac
}

append_runner_option_if_absent "--host" "${REGRESSION_DB_HOST:-${PGHOST:-}}"
append_runner_option_if_absent "--port" "${REGRESSION_DB_PORT:-${PGPORT:-}}"
append_runner_option_if_absent "--user" "${REGRESSION_DB_USER:-${PGUSER:-}}"
append_runner_option_if_absent "--sslmode" "${REGRESSION_DB_SSLMODE:-${PGSSLMODE:-}}"
append_runner_option_if_absent "--connect-timeout" "${REGRESSION_DB_CONNECT_TIMEOUT:-${PGCONNECT_TIMEOUT:-}}"
append_runner_option_if_absent "--admin-db" "${REGRESSION_DB_ADMIN_DB:-}"
append_runner_option_if_absent "--run-db" "${REGRESSION_DB_RUN_DB:-}"
append_runner_option_if_absent "--rollback-db" "${REGRESSION_DB_ROLLBACK_DB:-}"
append_runner_option_if_absent "--case-sql" "${REGRESSION_CASE_SQL:-}"
append_runner_option_if_absent "--workdir" "${REGRESSION_WORKDIR:-}"
append_runner_flag_if_absent "--allow-non-pg18" "${REGRESSION_ALLOW_NON_PG18:-}"

if [[ ! -x "$RUNNER" ]]; then
  echo "Gate error: runner script is missing or not executable: $RUNNER" >&2
  exit 6
fi

RUNNER_LOG="$(mktemp -t pg18-gate-runner.XXXXXX)"
trap 'rm -f "$RUNNER_LOG"' EXIT

echo "==> Running PG18 regression script"
set +e
"$RUNNER" ${RUNNER_ARGS[@]+"${RUNNER_ARGS[@]}"} 2>&1 | tee "$RUNNER_LOG"
RUNNER_RC=${PIPESTATUS[0]}
set -e

if (( RUNNER_RC != 0 )); then
  echo "Gate info: regression runner failed with exit code $RUNNER_RC (propagated)." >&2
  exit "$RUNNER_RC"
fi

WORKDIR="$(awk 'NF { line=$0 } END { print line }' "$RUNNER_LOG")"
if [[ -z "$WORKDIR" || ! -d "$WORKDIR" ]]; then
  echo "Gate error: cannot resolve workdir from runner output: $WORKDIR" >&2
  exit 6
fi

SUMMARY_FILE="$WORKDIR/99_summary.md"
if [[ ! -r "$SUMMARY_FILE" ]]; then
  echo "Gate error: summary file not found or unreadable: $SUMMARY_FILE" >&2
  exit 6
fi

extract_summary_value() {
  local label="$1"
  local matched

  matched="$(grep -F -- "- $label: " "$SUMMARY_FILE" || true)"
  if [[ -z "$matched" ]]; then
    return 1
  fi
  if (( $(printf '%s\n' "$matched" | wc -l | tr -d ' ') != 1 )); then
    return 1
  fi

  printf '%s\n' "${matched#"- $label: "}"
}

parse_errors=()
gate_failures=()

required_pass_labels=(
  "PG18 Gate"
  "First migration run"
  "Second migration expected failure (42710)"
  "Rollback drill expected failure (42P01)"
  "Rollback clean check"
  "Supplemental C09 (cross transaction)"
  "Supplemental C10 (cross transaction)"
)

for label in "${required_pass_labels[@]}"; do
  if ! value="$(extract_summary_value "$label")"; then
    parse_errors+=("$label")
    continue
  fi
  if [[ "$value" != PASS* ]]; then
    gate_failures+=("$label=$value")
  fi
done

if ! non_pass_ids="$(extract_summary_value "Non-pass IDs from suite output")"; then
  parse_errors+=("Non-pass IDs from suite output")
else
  if [[ "$non_pass_ids" != "NONE" ]]; then
    gate_failures+=("Non-pass IDs from suite output=$non_pass_ids")
  fi
fi

if (( ${#parse_errors[@]} > 0 )); then
  echo "Gate error: summary parse failed for labels: ${parse_errors[*]}" >&2
  exit 6
fi

if (( ${#gate_failures[@]} > 0 )); then
  echo "Gate failed. Summary mismatches:" >&2
  for item in "${gate_failures[@]}"; do
    echo "  - $item" >&2
  done
  exit 7
fi

echo "Gate passed: summary checks are all PASS/NONE."
echo "Summary file: $SUMMARY_FILE"
