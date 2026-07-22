package raft_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strconv"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raft "go.etcd.io/raft/v3"
)

func TestMapperMapsElectionPath(t *testing.T) {
	config := etcdraft.DefaultConfig()
	config.NodeIDs = []core.NodeID{1, 2, 3}
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "model-election", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapper := raftmodel.NewMapper()

	timeout := execute(t, runtime, core.Action{Kind: core.ActionTimeout, Node: 1})
	timeoutEvents := mapResult(t, mapper, timeout)
	assertEventNames(t, timeoutEvents, "Timeout")

	vote := findMessage(t, timeout.Observation, "MsgVote", 1, 2)
	voteResult := execute(t, runtime, deliverAction(vote))
	voteEvents := mapResult(t, mapper, voteResult)
	assertEventNames(t, voteEvents, "DeliverMessage")
	if voteEvents[0].Params["type"] != "MsgVote" || voteEvents[0].Params["term"] != uint64(1) {
		t.Fatalf("vote event params = %+v", voteEvents[0].Params)
	}

	response := findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1)
	leaderResult := execute(t, runtime, deliverAction(response))
	leaderEvents := mapResult(t, mapper, leaderResult)
	assertEventNames(t, leaderEvents, "DeliverMessage", "BecomeLeader", "ClientRequest")
	if leaderEvents[2].Params["request"] != 0 || leaderEvents[2].Params["leader"] != uint64(1) {
		t.Fatalf("leader no-op params = %+v", leaderEvents[2].Params)
	}

	// 持久化 Trace 经过 JSON 往返后，仍能只依赖 StepRecord 离线重建映射输入。
	encoded, err := json.Marshal(leaderResult.Record)
	if err != nil {
		t.Fatal(err)
	}
	var persisted core.StepRecord
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	offline, err := model.TransitionFromRecord(persisted)
	if err != nil {
		t.Fatal(err)
	}
	offlineEvents, err := mapper.Map(offline)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, offlineEvents, "DeliverMessage", "BecomeLeader", "ClientRequest")

	appendMessage := findMessage(t, leaderResult.Observation, "MsgApp", 1, 2)
	appendResult := execute(t, runtime, deliverAction(appendMessage))
	appendEvents := mapResult(t, mapper, appendResult)
	assertEventNames(t, appendEvents, "DeliverMessage")
	entries := appendEvents[0].Params["entries"].([]map[string]any)
	if len(entries) != 1 {
		t.Fatalf("no-op append entries = %+v", entries)
	}
	if _, containsData := entries[0]["Data"]; containsData {
		t.Fatalf("no-op entry must omit Data for the TLC mapper: %+v", entries[0])
	}

	appendResponse := findMessage(t, appendResult.Observation, "MsgAppResp", 2, 1)
	commitResult := execute(t, runtime, deliverAction(appendResponse))
	commitEvents := mapResult(t, mapper, commitResult)
	assertEventNames(t, commitEvents, "DeliverMessage", "AdvanceCommitIndex")

	// 心跳在轻量模型中是无 entry 的 AppendEntries，不能被静默忽略。
	heartbeatStep := execute(t, runtime, core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
	heartbeat := findMessage(t, heartbeatStep.Observation, "MsgHeartbeat", 1, 2)
	heartbeatResult := execute(t, runtime, deliverAction(heartbeat))
	heartbeatEvents := mapResult(t, mapper, heartbeatResult)
	assertEventNames(t, heartbeatEvents, "DeliverMessage")
	if heartbeatEvents[0].Params["type"] != "MsgApp" {
		t.Fatalf("heartbeat model type = %v, want MsgApp", heartbeatEvents[0].Params["type"])
	}
}

func TestMapperExposesPrematureMutantLeadershipToCorrectQuorumModel(t *testing.T) {
	config := etcdraft.DefaultConfig()
	config.NodeIDs = []core.NodeID{1, 2, 3, 4, 5}
	config.ElectionTick = 100
	config.Faults.VoteQuorumDivisor = 3
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "model-quorum-mutant", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	modelConfig := raftmodel.DefaultConfig()
	modelConfig.NodeIDs = append([]core.NodeID(nil), config.NodeIDs...)
	mapper, err := raftmodel.NewMapperWithConfig(modelConfig)
	if err != nil {
		t.Fatal(err)
	}

	timeout := execute(t, runtime, core.Action{Kind: core.ActionTimeout, Node: 1})
	vote := execute(t, runtime, deliverAction(findMessage(t, timeout.Observation, "MsgVote", 1, 2)))
	premature := execute(t, runtime, deliverAction(findMessage(t, vote.Observation, "MsgVoteResp", 2, 1)))
	events := mapResult(t, mapper, premature)
	// The activation marker itself stutters. The correct model sees the one
	// real vote response followed immediately by the invalid leadership.
	assertEventNames(t, events, "DeliverMessage", "BecomeLeader", "ClientRequest")
}

func TestMapperMapsCrashAndRestartToControlledTLCEvents(t *testing.T) {
	config := etcdraft.DefaultConfig()
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "model-crash-restart", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapper := raftmodel.NewMapper()

	crash := execute(t, runtime, core.Action{Kind: core.ActionCrash, Node: 2})
	crashEvents := mapResult(t, mapper, crash)
	assertEventNames(t, crashEvents, "Remove")
	if crashEvents[0].Params["i"] != uint64(2) {
		t.Fatalf("crash params = %+v", crashEvents[0].Params)
	}

	restart := execute(t, runtime, core.Action{Kind: core.ActionRestart, Node: 2})
	restartEvents := mapResult(t, mapper, restart)
	assertEventNames(t, restartEvents, "Add")
	if restartEvents[0].Params["i"] != uint64(2) {
		t.Fatalf("restart params = %+v", restartEvents[0].Params)
	}
	if restart.BeforeObservation.Nodes[1].Epoch+1 != restart.Observation.Nodes[1].Epoch {
		t.Fatalf("node epoch did not advance: before=%+v after=%+v",
			restart.BeforeObservation.Nodes[1], restart.Observation.Nodes[1])
	}

	encoded, err := json.Marshal(restart.Record)
	if err != nil {
		t.Fatal(err)
	}
	var persisted core.StepRecord
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	offline, err := model.TransitionFromRecord(persisted)
	if err != nil {
		t.Fatal(err)
	}
	offlineEvents, err := mapper.Map(offline)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, offlineEvents, "Add")
}

func TestMapperMapsFollowerProposalOnlyWhenLeaderReceivesIt(t *testing.T) {
	config := etcdraft.DefaultConfig()
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "model-forwarded-proposal", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapper := raftmodel.NewMapper()

	// 没有已知 Leader 的 follower 会由 etcd-raft 明确丢弃 proposal，模型 stutter。
	dropped := execute(t, runtime, core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("1")})
	assertEventNames(t, mapResult(t, mapper, dropped))

	timeout := execute(t, runtime, core.Action{Kind: core.ActionTimeout, Node: 1})
	vote := findMessage(t, timeout.Observation, "MsgVote", 1, 2)
	voteResult := execute(t, runtime, deliverAction(vote))
	response := findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1)
	leaderResult := execute(t, runtime, deliverAction(response))
	appendMessage := findMessage(t, leaderResult.Observation, "MsgApp", 1, 2)
	followerReady := execute(t, runtime, deliverAction(appendMessage))

	forwarded := execute(t, runtime, core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("1")})
	assertEventNames(t, mapResult(t, mapper, forwarded))
	proposal := findMessage(t, forwarded.Observation, "MsgProp", 2, 1)
	accepted := execute(t, runtime, deliverAction(proposal))
	events := mapResult(t, mapper, accepted)
	assertEventNames(t, events, "ClientRequest")
	if events[0].Params["request"] != 1 || events[0].Params["leader"] != uint64(1) {
		t.Fatalf("forwarded proposal event = %+v", events[0])
	}
	_ = followerReady
}

func TestMapperTreatsLeaderForcedTimeoutAttemptAsStutter(t *testing.T) {
	mapper := raftmodel.NewMapper()
	nodes := []core.NodeObservation{
		{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("leader")},
		{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
		{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
	}
	record := core.StepRecord{
		Action: core.Action{Kind: core.ActionTimeout, Node: 1},
		Effects: []core.Effect{{Kind: core.EffectTimerFired, TimerFired: &core.TimerFired{
			Node: 1, Epoch: 1, Source: core.TimerFireForced, TypeHint: "election", RoleHint: "leader",
		}}},
		NodesBefore: nodes,
		NodesAfter:  nodes,
	}
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events)
}

func TestMapperUsesPerEffectTermsForMultipleNaturalTimeouts(t *testing.T) {
	mapper := raftmodel.NewMapper()
	beforeNodes := []core.NodeObservation{
		{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("candidate")},
		{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
		{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
	}
	afterNodes := make([]core.NodeObservation, len(beforeNodes))
	copy(afterNodes, beforeNodes)
	beforeNodes[0].Semantic["term"] = uint64(0)
	afterNodes[0].Semantic = modelNode("candidate")
	afterNodes[0].Semantic["term"] = uint64(2)
	record := core.StepRecord{
		TimeBefore: 0,
		TimeAfter:  20,
		Action:     core.Action{Kind: core.ActionAdvanceTime, TargetTime: 20},
		Effects: []core.Effect{
			{At: 10, Kind: core.EffectTimerFired, TimerFired: &core.TimerFired{
				Node: 1, Epoch: 1, Source: core.TimerFireNatural, TypeHint: "election", RoleHint: "candidate",
				Metadata: map[string]string{"term_before": "0", "term_after": "1"},
			}},
			{At: 20, Kind: core.EffectTimerFired, TimerFired: &core.TimerFired{
				Node: 1, Epoch: 1, Source: core.TimerFireNatural, TypeHint: "election", RoleHint: "candidate",
				Metadata: map[string]string{"term_before": "1", "term_after": "2"},
			}},
		},
		NodesBefore: beforeNodes,
		NodesAfter:  afterNodes,
	}
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events, "Timeout", "Timeout")
}

func TestMapperRejectsSemanticsOutsideLightweightModel(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgSnap", nil, false)
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if events, err := mapper.Map(transition); err != nil || len(events) != 0 {
		t.Fatalf("snapshot mapping = %+v, %v; want stable stutter", events, err)
	}
}

func TestMapperTreatsSnapshotLifecycleEffectsAsStutter(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgHeartbeatResp", nil, false)
	for _, name := range []string{"raft.snapshot_created", "raft.snapshot_sent", "raft.snapshot_delivered",
		"raft.snapshot_applied", "raft.snapshot_rejected_or_stale", "raft.log_compacted"} {
		record.Effects = append(record.Effects, core.Effect{Kind: core.EffectModelEvent,
			ModelEvent: &core.ModelEvent{Name: name, Node: 1, Params: map[string]any{"index": uint64(2)}}})
	}
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil || len(events) != 0 {
		t.Fatalf("snapshot lifecycle mapping = %+v, %v; want stutter", events, err)
	}
}

func TestMapperExpandsAcceptedMultiEntryAppendInLogOrder(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgApp", []map[string]any{
		{"Term": uint64(1), "Data": "1"},
		{"Term": uint64(2), "Data": "2"},
	}, false)
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events, "DeliverMessage", "DeliverMessage")
	for i, event := range events {
		if event.Params["index"] != uint64(i) {
			t.Fatalf("event %d index = %v, want %d", i, event.Params["index"], i)
		}
		entries := event.Params["entries"].([]map[string]any)
		if len(entries) != 1 || entries[0]["Data"] != string(rune('1'+i)) {
			t.Fatalf("event %d entries = %+v", i, entries)
		}
	}
	if events[0].Params["log_term"] != uint64(0) || events[1].Params["log_term"] != uint64(1) {
		t.Fatalf("expanded log terms = %v, %v", events[0].Params["log_term"], events[1].Params["log_term"])
	}
}

func TestMapperDoesNotPartiallyAcceptRejectedMultiEntryAppend(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgApp", []map[string]any{
		{"Term": uint64(1), "Data": "1"},
		{"Term": uint64(1), "Data": "2"},
	}, true)
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events, "DeliverMessage")
}

func TestMapperTreatsIgnoredMultiEntryAppendAsSingleStutter(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgApp", []map[string]any{
		{"Term": uint64(1), "Data": "1"},
		{"Term": uint64(1), "Data": "2"},
	}, false)
	// 没有 MsgAppResp 表示目标节点直接忽略了旧任期消息。
	record.Effects = record.Effects[:1]
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events, "DeliverMessage")
	entries := events[0].Params["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["Data"] != "1" {
		t.Fatalf("ignored entries = %+v", entries)
	}
}

func TestMapperTreatsAppendBehindCommitAsNilAppend(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgApp", []map[string]any{
		{"Term": uint64(1), "Data": "1"},
		{"Term": uint64(1), "Data": "2"},
		{"Term": uint64(1), "Data": "3"},
	}, false)
	record.Effects[0].ModelEvent.Params["index"] = uint64(1)
	record.Effects[0].ModelEvent.Params["log_term"] = uint64(1)
	for index := range record.NodesBefore {
		if record.NodesBefore[index].ID != 2 {
			continue
		}
		record.NodesBefore[index].Semantic["last_index"] = uint64(3)
		record.NodesBefore[index].Semantic["last_term"] = uint64(1)
		record.NodesBefore[index].Semantic["commit"] = uint64(2)
		record.NodesAfter[index].Semantic["last_index"] = uint64(3)
		record.NodesAfter[index].Semantic["last_term"] = uint64(1)
		record.NodesAfter[index].Semantic["commit"] = uint64(2)
	}
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	assertEventNames(t, events, "DeliverMessage")
	if entries := events[0].Params["entries"].([]map[string]any); len(entries) != 0 {
		t.Fatalf("entries = %+v, want nil append", entries)
	}
	if events[0].Params["index"] != uint64(0) || events[0].Params["commit"] != uint64(2) {
		t.Fatalf("nil append params = %+v", events[0].Params)
	}
}

func deliveredRecord(messageType string, entries []map[string]any, rejected bool) core.StepRecord {
	params := map[string]any{
		"type": messageType, "from": uint64(1), "to": uint64(2),
		"term": uint64(1), "commit": uint64(0), "log_term": uint64(0),
		"index": uint64(0), "reject": false,
	}
	if entries != nil {
		params["entries"] = entries
	}
	nodes := []core.NodeObservation{
		{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("leader")},
		{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
		{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: modelNode("follower")},
	}
	message := core.Message{
		ID: 2, From: 2, To: 1, SenderEpoch: 1, Sequence: 1, TypeHint: "MsgAppResp",
		Metadata: map[string]string{"reject": strconv.FormatBool(rejected)},
	}
	return core.StepRecord{
		Action: core.Action{Kind: core.ActionDeliver, Message: 1, Selector: &core.MessageSelector{
			Link: core.LinkID{From: 1, To: 2}, Position: 0,
		}},
		Effects: []core.Effect{
			{Kind: core.EffectModelEvent, ModelEvent: &core.ModelEvent{
				Name: "raft.message_delivered", Node: 2, Params: params,
			}},
			{Kind: core.EffectSendMessage, Message: &message},
		},
		NodesBefore: nodes,
		NodesAfter:  nodes,
	}
}

func modelNode(role string) map[string]any {
	return map[string]any{
		"role": role, "term": uint64(1), "last_index": uint64(0),
		"last_term": uint64(0), "commit": uint64(0),
	}
}

func execute(t *testing.T, runtime *runtimepkg.Runtime, action core.Action) runtimepkg.StepResult {
	t.Helper()
	result, err := runtime.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mapResult(t *testing.T, mapper *raftmodel.Mapper, result runtimepkg.StepResult) []model.Event {
	t.Helper()
	events, err := mapper.Map(model.Transition{
		Before: result.BeforeObservation,
		Record: result.Record,
		After:  result.Observation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func findMessage(t *testing.T, observation core.Observation, kind string, from, to core.NodeID) core.MessageObservation {
	t.Helper()
	for _, message := range observation.Messages {
		if message.TypeHint == kind && message.From == from && message.To == to {
			return message
		}
	}
	t.Fatalf("message %s %s->%s not found", kind, from, to)
	return core.MessageObservation{}
}

func deliverAction(message core.MessageObservation) core.Action {
	return core.Action{
		Kind:    core.ActionDeliver,
		Message: message.ID,
		Selector: &core.MessageSelector{
			Link: core.LinkID{From: message.From, To: message.To}, Position: message.Position,
		},
	}
}

func assertEventNames(t *testing.T, events []model.Event, expected ...string) {
	t.Helper()
	if len(events) != len(expected) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(expected), events)
	}
	for i, name := range expected {
		if events[i].Name != name {
			t.Fatalf("event %d = %q, want %q: %+v", i, events[i].Name, name, events)
		}
	}
}
