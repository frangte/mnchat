#!/usr/bin/env bash
# make chaos-search: stop search, then kill the MySQL replica, under chat load, and fail if
# chat returns a single error. Search may fail during the outage. Chat must not.
#
# Timeline, in seconds from the start of the k6 run (each one can be set from the environment):
#   0   k6 starts: 5 chat users, plus a search scenario at 2 requests a second
#   15  docker-compose stop search
#   30  docker kill the replica, post a marker message, and check search fails fast
#   45  start the replica and search again
#   60  k6 ends, and its chat thresholds give the verdict on chat errors and latency
# Then the marker posted during the outage must become searchable within 60 s.
set -euo pipefail

cd "$(dirname "$0")/.."

API=${API:-http://localhost:8080/api/v1}
WORKSPACE=1
CHANNEL=load-test
SEARCH_DOWN_AT=${SEARCH_DOWN_AT:-15}
REPLICA_DOWN_AT=${REPLICA_DOWN_AT:-30}
RESTORE_AT=${RESTORE_AT:-45}
LOAD_SECONDS=${LOAD_SECONDS:-60}
RUN_ID=$(date +%s)
# One FULLTEXT token each, only letters and digits, so search matches them exactly.
MARKER_BEFORE="loadtestbefore${RUN_ID}"
MARKER_DURING="loadtestduring${RUN_ID}"
K6_LOG=$(mktemp -t mnchat-k6.XXXXXX)
K6_PID=
# Named, because killing `docker-compose run` leaves its container running.
K6_CONTAINER="mnchat-chaos-k6-${RUN_ID}"
FAILURES=0

dc() { docker-compose "$@"; }
log() { printf '%s  %s\n' "$(date +%H:%M:%S)" "$*"; }
pass() { log "ok    $1"; }
fail() { FAILURES=$((FAILURES + 1)); log "FAIL  $1"; }
die() { log "ERROR $1"; exit 1; }

# Whatever happens, leave search and the replica running and k6 stopped.
restore() {
  if [ -n "$K6_PID" ]; then
    kill "$K6_PID" 2>/dev/null || true
    docker rm -f "$K6_CONTAINER" >/dev/null 2>&1 || true
  fi
  dc start mysql-replica search >/dev/null 2>&1 || true
}
trap restore EXIT
# set -e exits silently, so say where.
trap 'log "ERROR the command on line $LINENO failed, stopping"' ERR

api_get() { curl -sS -H "Authorization: Bearer $TOKEN" "$@"; }

post_message() { # body -> HTTP status
  curl -sS -o /dev/null -w '%{http_code}' -X POST "$API/workspaces/$WORKSPACE/channels/$CHANNEL_ID/messages" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "$(jq -cn --arg body "$1" '{body: $body}')"
}

searchable() { # marker -> success when text search returns a message containing it
  api_get "$API/workspaces/$WORKSPACE/search?q=$1" 2>/dev/null |
    jq -e --arg m "$1" '(.items // []) | any(.message.body | contains($m))' >/dev/null 2>&1
}

wait_searchable() { # marker, seconds -> prints how long it took, fails on timeout
  local start=$SECONDS
  while ((SECONDS - start < $2)); do
    if searchable "$1"; then
      echo $((SECONDS - start))
      return 0
    fi
    sleep 1
  done
  return 1
}

at() { # sleep until this many seconds after k6 started
  local wait=$(($1 - (SECONDS - T0)))
  if ((wait > 0)); then sleep "$wait"; fi
}

# 1. The stack must be healthy before anything is broken.
command -v jq >/dev/null || die "jq is required: brew install jq"
curl -fsS -o /dev/null "$API/readyz" || die "api isn't ready at $API. Run make up first."
for svc in search mysql-replica; do
  [ -n "$(dc ps -q --status running "$svc")" ] || die "$svc isn't running. Run make up first."
done
TOKEN=$(curl -fsS -X POST "$API/auth/demo-login" -H 'Content-Type: application/json' -d '{"user_id":1}' | jq -r .token)
CHANNEL_ID=$(api_get "$API/workspaces/$WORKSPACE/channels" | jq -r --arg n "$CHANNEL" '.items[] | select(.name == $n) | .id')
if [ -z "$CHANNEL_ID" ]; then
  CHANNEL_ID=$(curl -fsS -X POST "$API/workspaces/$WORKSPACE/channels" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d "{\"name\":\"$CHANNEL\"}" | jq -r .id)
fi

# 2. A marker sent while everything is up must become searchable, or the stack isn't healthy.
[ "$(post_message "load-test chaos marker $MARKER_BEFORE")" = 201 ] || die "couldn't post the first marker"
took=$(wait_searchable "$MARKER_BEFORE" 30) || die "search didn't find the first marker within 30 s, so the stack isn't healthy"
pass "search finds a new message before the outage (after ${took} s)"

# 3. Chat load in the background. k6 runs in Compose, so it reaches api by service name.
log "k6: ${LOAD_SECONDS} s of chat load, plus a search scenario"
dc --profile load run --rm -T --name "$K6_CONTAINER" -e DURATION="${LOAD_SECONDS}s" k6 run /scripts/chat.js >"$K6_LOG" 2>&1 &
K6_PID=$!
T0=$SECONDS

# 4. and 5. Take search down, then the replica too.
at "$SEARCH_DOWN_AT"
log "stopping search"
dc stop search >/dev/null 2>&1
at "$REPLICA_DOWN_AT"
log "killing the replica"
docker kill "$(dc ps -q mysql-replica)" >/dev/null

# 6. Chat must still take writes.
status=$(post_message "load-test chaos marker $MARKER_DURING")
if [ "$status" = 201 ]; then
  pass "chat accepts a message with search and the replica both down"
else
  fail "posting during the outage returned $status, want 201"
fi

# 7. Search must fail fast: its breaker has been open since search stopped.
read -r code secs < <(api_get -o /dev/null -w '%{http_code} %{time_total}\n' "$API/workspaces/$WORKSPACE/search?q=$MARKER_DURING")
if [ "$code" = 503 ] && awk -v s="$secs" 'BEGIN { exit !(s < 1) }'; then
  pass "search returns 503 in ${secs} s during the outage"
else
  fail "search returned $code in ${secs} s, want 503 in under 1 s"
fi

# 8. Bring both back.
at "$RESTORE_AT"
log "starting the replica and search"
dc start mysql-replica search >/dev/null 2>&1

# 9. k6's exit code is the verdict on chat: its thresholds allow zero chat errors.
k6_exit=0
wait "$K6_PID" || k6_exit=$?
K6_PID=
if [ "$k6_exit" = 0 ]; then
  pass "k6: zero failed chat requests and chat p95 under 300 ms"
else
  fail "k6 chat thresholds failed (exit $k6_exit)"
fi

# 10. The message sent during the outage must reach search once it recovers.
if took=$(wait_searchable "$MARKER_DURING" 60); then
  pass "the marker sent during the outage is searchable ${took} s after k6 ended"
else
  fail "the marker sent during the outage isn't searchable 60 s after k6 ended"
fi

# 11. Summary.
echo
sed -n '/THRESHOLDS/,/TOTAL RESULTS/p' "$K6_LOG" | sed '$d'
echo
if ((FAILURES > 0)); then
  log "CHAOS TEST FAILED: $FAILURES check(s) failed. Full k6 output: $K6_LOG"
  exit 1
fi
rm -f "$K6_LOG"
log "CHAOS TEST PASSED: chat had zero errors while search and the replica were down"
