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

modelcheck_dir="$script_dir/build/storage-snapshot-modelcheck"
rm -rf "$modelcheck_dir"
java -cp "$tlc_jar" tlc2.TLC -nowarning -deadlock -workers 1 \
  -metadir "$modelcheck_dir" \
  -config "$repo_dir/models/raft/raft-storage-snapshot.cfg" \
  "$repo_dir/models/raft/raft_storage_snapshot.tla"

progress_modelcheck_dir="$script_dir/build/storage-snapshot-progress-modelcheck"
rm -rf "$progress_modelcheck_dir"
java -cp "$tlc_jar" tlc2.TLC -nowarning -deadlock -workers 1 \
  -metadir "$progress_modelcheck_dir" \
  -config "$repo_dir/models/raft/raft-storage-snapshot-progress.cfg" \
  "$repo_dir/models/raft/raft_storage_snapshot.tla"

"$script_dir/run.sh" \
  --model "$repo_dir/models/raft/raft.tla" \
  --config "$repo_dir/models/raft/raft-5.cfg" \
  --port "$port" >"$log_file" 2>&1 &
server_pid=$!
violation_pid=""
ambiguous_pid=""
storage_pid=""
trap 'kill "$server_pid" "$violation_pid" "$ambiguous_pid" "$storage_pid" 2>/dev/null || true; wait "$server_pid" "$violation_pid" "$ambiguous_pid" "$storage_pid" 2>/dev/null || true' EXIT

for _ in $(seq 1 100); do
  if curl --fail --silent "http://127.0.0.1:$port/health" >/dev/null; then
    break
  fi
  sleep 0.1
done

curl --fail --silent "http://127.0.0.1:$port/health" |
  jq -e '.status == "ok" and .strict == true and .action_mode == "lazy" and
    .model_profile == "basic" and
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

storage_port=$((port + 3))
"$script_dir/run.sh" \
  --model "$repo_dir/models/raft/raft_storage_snapshot.tla" \
  --config "$repo_dir/models/raft/raft-storage-snapshot-5.cfg" \
  --port "$storage_port" >"$script_dir/build/storage-snapshot-server.log" 2>&1 &
storage_pid=$!
for _ in $(seq 1 100); do
  if curl --fail --silent "http://127.0.0.1:$storage_port/health" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl --fail --silent "http://127.0.0.1:$storage_port/health" |
  jq -e '.status == "ok" and .model_profile == "storage-snapshot" and
    .largest_term == 5 and .max_log_index == 5 and .server_ids == [1,2,3]' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":1}},
    {"name":"ApplyCommitted","params":{"i":1,"index":1}},
    {"name":"CreateSnapshot","params":{"i":1,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":1}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"name":"InstallSnapshot","params":{"i":1,"j":3,"index":1,"snapshot_term":1,"term":1}},
    {"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":true,"next":2}},
    {"name":"RejectSnapshot","params":{"i":1,"j":3,"index":1,"snapshot_term":1,"term":1}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":3,"to":1,"term":1,"reject":false,"index":1}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 15 and (.Keys | length) == 15' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgApp","from":1,"to":3,"term":1,"commit":0,"log_term":0,"index":0,"entries":[{"Term":1,"Index":1,"Type":"EntryNormal"}]}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":1}},
    {"name":"ApplyCommitted","params":{"i":1,"index":1}},
    {"name":"CreateSnapshot","params":{"i":1,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":1}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"name":"FastForwardSnapshot","params":{"i":1,"j":3,"index":1,"snapshot_term":1,"term":1}},
    {"name":"ApplyCommitted","params":{"i":3,"index":1}},
    {"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":true,"next":2}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":3,"to":1,"term":1,"reject":false,"index":1}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 16 and (.Keys | length) == 16' >/dev/null

curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":1}},
    {"name":"ApplyCommitted","params":{"i":1,"index":1}},
    {"name":"CreateSnapshot","params":{"i":1,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":1}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":false,"next":1}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"name":"InstallSnapshot","params":{"i":1,"j":3,"index":1,"snapshot_term":1,"term":1}},
    {"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":true,"next":2}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":3,"to":1,"term":1,"reject":false,"index":1}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 16 and (.Keys | length) == 16' >/dev/null

# A queued snapshot can become older than the leader's current compaction
# boundary. A delayed successful MsgAppResp may then advance Match while the
# peer remains in StateSnapshot; SnapshotFinish resumes from max(Match+1,
# PendingSnapshot+1), not unconditionally from PendingSnapshot+1.
curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":1}},
    {"name":"ApplyCommitted","params":{"i":1,"index":1}},
    {"name":"CreateSnapshot","params":{"i":1,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":1}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":3,"to":1,"term":1,"reject":true,"index":3}},
    {"name":"ClientRequest","params":{"leader":1,"request":1}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":2}},
    {"name":"ApplyCommitted","params":{"i":1,"index":2}},
    {"name":"CreateSnapshot","params":{"i":1,"index":2,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":2}},
    {"name":"ClientRequest","params":{"leader":1,"request":2}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":3}},
    {"name":"ApplyCommitted","params":{"i":1,"index":3}},
    {"name":"CreateSnapshot","params":{"i":1,"index":3,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":3}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":3,"to":1,"term":1,"reject":false,"index":2}},
    {"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":true,"next":3}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 24 and (.Keys | length) == 24' >/dev/null

# An old leaderCommit carried by a delayed AppendEntries must not move a
# follower's commit/applied/snapshot boundary backwards.
curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgApp","from":1,"to":3,"term":1,"commit":0,"log_term":0,"index":0,"entries":[{"Term":1,"Index":1,"Type":"EntryNormal"}]}},
    {"name":"DeliverMessage","params":{"type":"MsgApp","from":1,"to":3,"term":1,"commit":1,"log_term":1,"index":1,"entries":[]}},
    {"name":"ApplyCommitted","params":{"i":3,"index":1}},
    {"name":"CreateSnapshot","params":{"i":3,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":3,"index":1}},
    {"name":"DeliverMessage","params":{"type":"MsgApp","from":1,"to":3,"term":1,"commit":0,"log_term":1,"index":1,"entries":[]}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 12 and (.Keys | length) == 12' >/dev/null

# A crashed target is a transport/runtime condition unknown to the Raft
# leader. MsgSnap may be generated and queued while the target is inactive;
# only delivery and installation require the target to be active.
curl --fail --silent -H 'Content-Type: application/json' \
  --data '[
    {"name":"Timeout","params":{"node":1}},
    {"name":"DeliverMessage","params":{"type":"MsgVote","from":1,"to":2,"term":1,"log_term":0,"index":0}},
    {"name":"DeliverMessage","params":{"type":"MsgVoteResp","from":2,"to":1,"term":1,"reject":false}},
    {"name":"BecomeLeader","params":{"node":1}},
    {"name":"ClientRequest","params":{"leader":1,"request":0}},
    {"name":"DeliverMessage","params":{"type":"MsgAppResp","from":2,"to":1,"term":1,"reject":false,"index":1}},
    {"name":"ApplyCommitted","params":{"i":1,"index":1}},
    {"name":"CreateSnapshot","params":{"i":1,"index":1,"term":1}},
    {"name":"CompactLog","params":{"i":1,"index":1}},
    {"name":"Remove","params":{"i":3}},
    {"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},
    {"reset":true}
  ]' \
  "http://127.0.0.1:$storage_port/execute" |
  jq -e '(.States | length) == 12 and (.Keys | length) == 12' >/dev/null

status=$(curl --silent --output "$script_dir/build/storage-boundary-disabled.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"CompactLog","params":{"i":1,"index":1}},{"reset":true}]' \
  "http://127.0.0.1:$storage_port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "disabled_action" and .error.event_name == "CompactLog"' \
  "$script_dir/build/storage-boundary-disabled.json" >/dev/null

status=$(curl --silent --output "$script_dir/build/snapshot-send-disabled.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"SendSnapshot","params":{"i":1,"j":3,"index":1,"term":1,"match":0,"next":2,"pending":1}},{"reset":true}]' \
  "http://127.0.0.1:$storage_port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "disabled_action" and .error.event_name == "SendSnapshot"' \
  "$script_dir/build/snapshot-send-disabled.json" >/dev/null

status=$(curl --silent --output "$script_dir/build/snapshot-install-disabled.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"InstallSnapshot","params":{"i":1,"j":3,"index":1,"snapshot_term":1,"term":1}},{"reset":true}]' \
  "http://127.0.0.1:$storage_port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "disabled_action" and .error.event_name == "InstallSnapshot"' \
  "$script_dir/build/snapshot-install-disabled.json" >/dev/null

status=$(curl --silent --output "$script_dir/build/snapshot-status-disabled.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '[{"name":"HandleSnapshotStatus","params":{"i":1,"j":3,"success":false,"next":1}},{"reset":true}]' \
  "http://127.0.0.1:$storage_port/execute")
[[ "$status" == "422" ]]
jq -e '.error.code == "disabled_action" and .error.event_name == "HandleSnapshotStatus"' \
  "$script_dir/build/snapshot-status-disabled.json" >/dev/null

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
