#!/usr/bin/env bash
# Generates a documented mix of task-api traffic to validate the Task 3
# Grafana dashboard: successful creates/reads/updates/deletes plus the
# validation failures (400) and not-founds (404) needed to see the
# error/failure panels move.
#
# Deliberately avoids shell arrays: an earlier version used
# ${ids[0]}, which is silently empty under zsh (1-indexed arrays) and produced
# misleading "everything 404s" results — see deploy/NOTES.md section 3.
# A plain file used as a FIFO queue sidesteps that whole bug class.
#
# Usage: ./scripts/generate-traffic.sh [base_url] [iterations]
set -euo pipefail

BASE="${1:-http://localhost:8080}"
N="${2:-45}"
IDS_FILE=$(mktemp)
trap 'rm -f "$IDS_FILE"' EXIT

for i in $(seq 1 "$N"); do
  id=$(curl -s -X POST "$BASE/tasks" -H 'Content-Type: application/json' -d "{\"title\":\"task $i\"}" \
    | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
  [ -n "$id" ] && echo "$id" >> "$IDS_FILE"

  # every 5th create: missing title -> 400
  if [ $((i % 5)) -eq 0 ]; then
    curl -s -o /dev/null -X POST "$BASE/tasks" -H 'Content-Type: application/json' -d '{"title":""}'
  fi

  oldest=$(head -n1 "$IDS_FILE" 2>/dev/null || true)
  [ -n "$oldest" ] && curl -s -o /dev/null "$BASE/tasks/$oldest"

  # every 7th: nonexistent id -> 404
  if [ $((i % 7)) -eq 0 ]; then
    curl -s -o /dev/null "$BASE/tasks/999999"
  fi

  # every 4th: mark the oldest task done -> 200
  if [ $((i % 4)) -eq 0 ] && [ -n "$oldest" ]; then
    curl -s -o /dev/null -X PUT "$BASE/tasks/$oldest" -H 'Content-Type: application/json' -d '{"done":true}'
  fi

  # every 10th: delete the oldest task -> 204
  if [ $((i % 10)) -eq 0 ] && [ -n "$oldest" ]; then
    curl -s -o /dev/null -X DELETE "$BASE/tasks/$oldest"
    tail -n +2 "$IDS_FILE" > "$IDS_FILE.tmp" && mv "$IDS_FILE.tmp" "$IDS_FILE"
  fi

  sleep 1
done

echo "done: $N create attempts against $BASE"
