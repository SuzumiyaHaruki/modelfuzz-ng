package raft_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
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

func TestMapperRejectsSemanticsOutsideLightweightModel(t *testing.T) {
	mapper := raftmodel.NewMapper()
	record := deliveredRecord("MsgSnap", nil)
	transition, err := model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mapper.Map(transition); !errors.Is(err, raftmodel.ErrUnsupportedSemantics) {
		t.Fatalf("snapshot mapping error = %v, want ErrUnsupportedSemantics", err)
	}

	record = deliveredRecord("MsgApp", []map[string]any{
		{"Term": uint64(1), "Data": "1"},
		{"Term": uint64(1), "Data": "2"},
	})
	transition, err = model.TransitionFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mapper.Map(transition); !errors.Is(err, raftmodel.ErrUnsupportedSemantics) {
		t.Fatalf("multi-entry mapping error = %v, want ErrUnsupportedSemantics", err)
	}
}

func deliveredRecord(messageType string, entries []map[string]any) core.StepRecord {
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
	message := core.Message{ID: 1, From: 1, To: 2, SenderEpoch: 1, Sequence: 1}
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
