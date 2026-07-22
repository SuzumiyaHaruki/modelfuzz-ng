#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd "$script_dir/../.." && pwd)
port=${TLC_SERVER_TEST_PORT:-22024}
log_file="$script_dir/build/test-server.log"
tlc_jar="$script_dir/.cache/tla2tools-1.8.0.jar"
server_jar=$($script_dir/build.sh)
test_classes="$script_dir/build/test-classes"

rm -rf "$test_classes"
mkdir -p "$test_classes"
find "$script_dir/src/test/java" -name '*.java' -print0 |
  xargs -0 javac --release 17 -cp "$server_jar:$tlc_jar" -d "$test_classes"
java -cp "$test_classes:$server_jar:$tlc_jar" org.modelfuzzng.tlc.LazyActionEquivalence \
  "$repo_dir/models/raft/raft.tla" \
  "$script_dir/src/test/resources/raft-eager.cfg" \
  "$script_dir/src/test/resources/raft-lazy.cfg"

"$script_dir/run.sh" \
  --model "$repo_dir/models/raft/raft.tla" \
  --config "$repo_dir/models/raft/raft-5.cfg" \
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
  jq -e '.status == "ok" and .strict == true and .action_mode == "lazy" and
    .action_definitions > 0 and .cached_actions == 0 and .action_cache_limit == 16384 and
    .largest_term == 5 and .max_log_index == 5 and .server_ids == [1,2,3] and
    .max_value == 5 and .nil_value == 0' >/dev/null

curl --fail --silent "http://127.0.0.1:$port/metrics" |
  jq -e '.requests == 0 and .model_events == 0 and .timing.successor_nanos == 0' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$port/execute" |
  jq -e '(.States | length) == 2 and (.Keys | length) == 2' >/dev/null

curl --fail --silent "http://127.0.0.1:$port/metrics" |
  jq -e '.requests == 1 and .succeeded == 1 and .model_events == 1 and
    .action_lookups == 1 and .actions_created == 1 and .action_cache_misses == 1 and
    .cached_actions == 1 and .timing.successor_nanos > 0' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[{"name":"Timeout","params":{"node":1}},{"reset":true}]' \
  "http://127.0.0.1:$port/execute" >/dev/null
curl --fail --silent "http://127.0.0.1:$port/metrics" |
  jq -e '.action_lookups == 2 and .actions_created == 1 and
    .action_cache_hits == 1 and .action_cache_misses == 1' >/dev/null

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
