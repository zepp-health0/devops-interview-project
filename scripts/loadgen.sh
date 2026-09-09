#!/usr/bin/env bash
# Deterministic business traffic for validating the dashboard.
#
# The operation mix is fixed rather than random so that every panel has a value that can be
# predicted before the run and compared afterwards. Assumes a freshly started app (empty store).
#
#   usage: scripts/loadgen.sh [BASE_URL] [PACE_SECONDS]

set -euo pipefail

BASE="${1:-http://localhost:8080}"
PACE="${2:-0.15}"

CREATES=40
UPDATES=20   # ids 0..19 marked done
DELETES=10   # ids 30..39, all still pending
MISSING=15   # ids 100..114 -> 404
INVALID=10   # empty title  -> 400
LISTS=20

req() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

echo "target: $BASE"
echo

echo "--- ${CREATES} creates (expect 201) ---"
for i in $(seq 0 $((CREATES - 1))); do
  req -X POST "$BASE/tasks" -H 'Content-Type: application/json' -d "{\"title\":\"task-$i\"}" >/dev/null
  sleep "$PACE"
done

echo "--- ${CREATES} reads by id (expect 200) ---"
for i in $(seq 0 $((CREATES - 1))); do
  req "$BASE/tasks/$i" >/dev/null
  sleep "$PACE"
done

echo "--- ${UPDATES} updates to done (expect 200) ---"
for i in $(seq 0 $((UPDATES - 1))); do
  req -X PUT "$BASE/tasks/$i" -H 'Content-Type: application/json' -d '{"done":true}' >/dev/null
  sleep "$PACE"
done

echo "--- ${DELETES} deletes of pending tasks (expect 204) ---"
for i in $(seq 30 $((30 + DELETES - 1))); do
  req -X DELETE "$BASE/tasks/$i" >/dev/null
  sleep "$PACE"
done

echo "--- ${MISSING} reads of missing ids (expect 404) ---"
for i in $(seq 100 $((100 + MISSING - 1))); do
  req "$BASE/tasks/$i" >/dev/null
  sleep "$PACE"
done

echo "--- ${INVALID} invalid creates (expect 400) ---"
for _ in $(seq 1 $INVALID); do
  req -X POST "$BASE/tasks" -H 'Content-Type: application/json' -d '{"title":""}' >/dev/null
  sleep "$PACE"
done

echo "--- ${LISTS} list calls (expect 200) ---"
for _ in $(seq 1 $LISTS); do
  req "$BASE/tasks" >/dev/null
  sleep "$PACE"
done

cat <<EOF

done. expected end state:
  task_api_tasks_total        30    (${CREATES} created - ${DELETES} deleted)
  task_api_tasks_done         20    (ids 0..19)
  tasks_by_state{pending}     10    (30 remaining - 20 done)

  http_requests_total by code, business routes only:
    201  ${CREATES}
    200  $((CREATES + UPDATES + LISTS))
    204  ${DELETES}
    404  ${MISSING}
    400  ${INVALID}
EOF
