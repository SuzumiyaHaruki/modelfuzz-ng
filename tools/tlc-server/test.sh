#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd "$script_dir/../.." && pwd)
port=${TLC_SERVER_TEST_PORT:-22024}
log_file="$script_dir/build/test-server.log"

"$script_dir/run.sh" \
  --model "$repo_dir/models/raft/raft.tla" \
  --config "$repo_dir/models/raft/raft.cfg" \
  --port "$port" >"$log_file" 2>&1 &
server_pid=$!
violation_pid=""
ambiguous_pid=""
trap 'kill "$server_pid" "$violation_pid" "$ambiguous_pid" 2>/dev/null || true; wait "$server_pid" "$violation_pid" "$ambiguous_pid" 2>/dev/null || true' EXIT

for _ in $(seq 1 100); do
  if curl --fail --silent "http://127.0.0.1:$port/health" >/dev/null; then
    break
  fi
  sleep 0.1
done

curl --fail --silent "http://127.0.0.1:$port/health" |
  jq -e '.status == "ok" and .strict == true' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$port/execute" |
  jq -e '(.States | length) == 2 and (.Keys | length) == 2' >/dev/null

status=$(curl --silent --output "$script_dir/build/disabled.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"BecomeLeader","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "disabled_action" and .error.event_index == 0' \
  "$script_dir/build/disabled.json" >/dev/null

status=$(curl --silent --output "$script_dir/build/unmapped.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":99}},{"reset":true}]' \
  "http://127.0.0.1:$port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "unmapped_action" and .error.event_index == 0' \
  "$script_dir/build/unmapped.json" >/dev/null

violation_port=$((port + 1))
"$script_dir/run.sh" \
  --model "$script_dir/src/test/resources/InvariantViolation.tla" \
  --config "$script_dir/src/test/resources/InvariantViolation.cfg" \
  --port "$violation_port" >"$script_dir/build/invariant-server.log" 2>&1 &
violation_pid=$!
for _ in $(seq 1 100); do
  if curl --fail --silent "http://127.0.0.1:$violation_port/health" >/dev/null; then
    break
  fi
  sleep 0.1
done
status=$(curl --silent --output "$script_dir/build/invariant.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$violation_port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "invariant_violation" and (.error.message | contains("Safe"))' \
  "$script_dir/build/invariant.json" >/dev/null

ambiguous_port=$((port + 2))
"$script_dir/run.sh" \
  --model "$script_dir/src/test/resources/AmbiguousSuccessor.tla" \
  --config "$script_dir/src/test/resources/AmbiguousSuccessor.cfg" \
  --port "$ambiguous_port" >"$script_dir/build/ambiguous-server.log" 2>&1 &
ambiguous_pid=$!
for _ in $(seq 1 100); do
  if curl --fail --silent "http://127.0.0.1:$ambiguous_port/health" >/dev/null; then
    break
  fi
  sleep 0.1
done
status=$(curl --silent --output "$script_dir/build/ambiguous.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$ambiguous_port/execute")
[[ "$status" == "409" ]]
jq -e '.error.code == "ambiguous_successor"' "$script_dir/build/ambiguous.json" >/dev/null

echo "strict TLC server integration tests passed"
