package etcdraft

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raft "go.etcd.io/raft/v3"
)

func testConfig(ids ...core.NodeID) Config {
	config := DefaultConfig()
	config.NodeIDs = ids
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	return config
}

func newTestRuntime(t *testing.T, config Config) *runtimepkg.Runtime {
	t.Helper()
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "etcdraft-test", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func findMessage(t *testing.T, observation core.Observation, typeHint string, from, to core.NodeID) core.MessageObservation {
	t.Helper()
	for _, message := range observation.Messages {
		if message.TypeHint == typeHint && message.From == from && message.To == to {
			return message
		}
	}
	t.Fatalf("message %s %s->%s not found in %+v", typeHint, from, to, observation.Messages)
	return core.MessageObservation{}
}

func deliverObserved(t *testing.T, r *runtimepkg.Runtime, message core.MessageObservation) runtimepkg.StepResult {
	t.Helper()
	result, err := r.Execute(context.Background(), core.Action{
		Kind:    core.ActionDeliver,
		Message: message.ID,
		Selector: &core.MessageSelector{
			Link: core.LinkID{From: message.From, To: message.To}, Position: message.Position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAdapterForceElectionAndDeliverVotesThroughRuntime(t *testing.T) {
	r := newTestRuntime(t, testConfig(1, 2, 3))
	initial, err := r.CurrentObservation()
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range initial.Nodes {
		if node.Semantic["term"] != uint64(0) || node.Semantic["applied"] != uint64(0) {
			t.Fatalf("initial node is not aligned with empty-log model: %+v", node)
		}
		if node.Semantic["last_index"] != uint64(0) || node.Semantic["last_term"] != uint64(0) ||
			node.Semantic["log_digest"] == "" {
			t.Fatalf("initial log summary is incomplete: %+v", node.Semantic)
		}
	}

	timeout, err := r.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeout.Record.Effects) < 3 || timeout.Record.Effects[0].Kind != core.EffectTimerFired ||
		timeout.Record.Effects[0].TimerFired.Source != core.TimerFireForced {
		t.Fatalf("timeout effects = %+v", timeout.Record.Effects)
	}
	if role := timeout.Observation.Nodes[0].Semantic["role"]; role != "candidate" {
		t.Fatalf("node 1 role after campaign = %v, want candidate", role)
	}

	vote := findMessage(t, timeout.Observation, "MsgVote", 1, 2)
	voteResult := deliverObserved(t, r, vote)
	response := findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1)
	leaderResult := deliverObserved(t, r, response)
	if role := leaderResult.Observation.Nodes[0].Semantic["role"]; role != "leader" {
		t.Fatalf("node 1 role after vote response = %v, want leader", role)
	}
	findMessage(t, leaderResult.Observation, "MsgApp", 1, 2)
}

func TestAdapterForwardsFollowerRequestToKnownLeader(t *testing.T) {
	r := newTestRuntime(t, testConfig(1, 2, 3))
	ctx := context.Background()
	timeout, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	voteResult := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
	leaderResult := deliverObserved(t, r, findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1))
	appendResult := deliverObserved(t, r, findMessage(t, leaderResult.Observation, "MsgApp", 1, 2))
	if appendResult.Observation.Nodes[1].Semantic["leader"] != uint64(1) {
		t.Fatalf("follower does not know leader: %+v", appendResult.Observation.Nodes[1].Semantic)
	}

	forwarded, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, effect := range forwarded.Record.Effects {
		if effect.Kind == core.EffectModelEvent && effect.ModelEvent.Name == proposalDroppedEvent {
			t.Fatalf("request with known leader was dropped: %+v", forwarded.Record.Effects)
		}
	}
	proposal := findMessage(t, forwarded.Observation, "MsgProp", 2, 1)
	beforeIndex := forwarded.Observation.Nodes[0].Semantic["last_index"].(uint64)
	accepted := deliverObserved(t, r, proposal)
	afterIndex := accepted.Observation.Nodes[0].Semantic["last_index"].(uint64)
	if afterIndex != beforeIndex+1 {
		t.Fatalf("leader last index = %d, want %d after forwarded proposal", afterIndex, beforeIndex+1)
	}
}

func TestAdapterExposesAndAdvancesRaftTimerState(t *testing.T) {
	r := newTestRuntime(t, testConfig(1))
	initial, err := r.CurrentObservation()
	if err != nil {
		t.Fatal(err)
	}
	semantic := initial.Nodes[0].Semantic
	remaining, ok := semantic["election_ticks_remaining"].(int)
	if !ok || remaining <= 0 || semantic["election_elapsed"] != 0 || semantic["election_timeout"] != 100 {
		t.Fatalf("initial timer state = %+v", semantic)
	}

	oneTick, err := r.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
	if err != nil {
		t.Fatal(err)
	}
	afterOne := oneTick.Observation.Nodes[0].Semantic
	if afterOne["election_elapsed"] != 1 || afterOne["election_ticks_remaining"] != remaining-1 {
		t.Fatalf("timer after one tick = %+v, initial remaining=%d", afterOne, remaining)
	}

	timeout, err := r.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: core.LogicalTime(remaining)})
	if err != nil {
		t.Fatal(err)
	}
	foundNatural := false
	for _, effect := range timeout.Record.Effects {
		if effect.Kind == core.EffectTimerFired && effect.TimerFired.Node == 1 && effect.TimerFired.Source == core.TimerFireNatural {
			foundNatural = true
		}
	}
	if !foundNatural || timeout.Observation.Nodes[0].Semantic["term"] != uint64(1) {
		t.Fatalf("natural timeout effects/observation = %+v / %+v", timeout.Record.Effects, timeout.Observation.Nodes[0])
	}
}

func TestAdapterRecordsProposalDropWithoutFailingStep(t *testing.T) {
	r := newTestRuntime(t, testConfig(1, 2, 3))
	result, err := r.Execute(context.Background(), core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Record.Effects) != 1 || result.Record.Effects[0].Kind != core.EffectModelEvent ||
		result.Record.Effects[0].ModelEvent.Name != proposalDroppedEvent {
		t.Fatalf("proposal drop effects = %+v", result.Record.Effects)
	}
}

func TestAdapterSingleNodeCommitCrashAndRestart(t *testing.T) {
	r := newTestRuntime(t, testConfig(1))
	ctx := context.Background()
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1}); err != nil {
		t.Fatal(err)
	}

	request, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("set x=1")})
	if err != nil {
		t.Fatal(err)
	}
	foundCommit := false
	for _, effect := range request.Record.Effects {
		if effect.Kind == core.EffectModelEvent && effect.ModelEvent.Name == "raft.entry_committed" {
			foundCommit = true
		}
	}
	if !foundCommit {
		t.Fatalf("request effects do not contain committed entry: %+v", request.Record.Effects)
	}
	applied := request.Observation.Nodes[0].Semantic["applied"]
	lastIndex := request.Observation.Nodes[0].Semantic["last_index"]
	logDigest := request.Observation.Nodes[0].Semantic["log_digest"]
	prefixes, ok := request.Observation.Nodes[0].Semantic["committed_prefix_digests"].(map[string]string)
	if !ok || request.Observation.Nodes[0].Semantic["committed_prefix_available"] != true {
		t.Fatalf("committed prefix is unavailable: %+v", request.Observation.Nodes[0].Semantic)
	}
	commit := request.Observation.Nodes[0].Semantic["commit"].(uint64)
	if commit == 0 || prefixes[fmt.Sprint(commit)] == "" {
		t.Fatalf("committed prefix does not cover commit=%d: %+v", commit, prefixes)
	}
	if lastIndex == uint64(0) || logDigest == "" {
		t.Fatalf("request did not update log summary: %+v", request.Observation.Nodes[0].Semantic)
	}

	crashed, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	if crashed.Observation.Nodes[0].Status != core.NodeCrashed {
		t.Fatalf("node after crash = %+v", crashed.Observation.Nodes[0])
	}

	restarted, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	node := restarted.Observation.Nodes[0]
	if node.Status != core.NodeRunning || node.Epoch != 2 {
		t.Fatalf("node after restart = %+v", node)
	}
	if node.Semantic["applied"] != applied {
		t.Fatalf("applied after restart = %v, want %v", node.Semantic["applied"], applied)
	}
	if node.Semantic["last_index"] != lastIndex || node.Semantic["log_digest"] != logDigest {
		t.Fatalf("log summary changed across restart: got %+v", node.Semantic)
	}
	if got := node.Semantic["committed_prefix_digests"]; !reflect.DeepEqual(got, prefixes) {
		t.Fatalf("committed prefix changed across restart: got %v, want %v", got, prefixes)
	}
}

func TestAdapterNaturalElectionTimeoutIsObserved(t *testing.T) {
	config := testConfig(1, 2, 3)
	config.ElectionTick = 2
	r := newTestRuntime(t, config)

	result, err := r.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 3})
	if err != nil {
		t.Fatal(err)
	}
	foundNatural := false
	for _, effect := range result.Record.Effects {
		if effect.Kind == core.EffectTimerFired && effect.TimerFired.Source == core.TimerFireNatural &&
			effect.TimerFired.TypeHint == "election" {
			foundNatural = true
			break
		}
	}
	if !foundNatural {
		t.Fatalf("advance effects do not contain natural election timeout: %+v", result.Record.Effects)
	}
}

func TestAdapterResetReplaysNaturalTimeoutsWithSameSeed(t *testing.T) {
	config := testConfig(1, 2, 3)
	config.ElectionTick = 4
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: "deterministic-timeouts",
		Seed:        20260720,
	})
	if err != nil {
		t.Fatal(err)
	}

	run := func() []byte {
		t.Helper()
		if _, err := r.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Execute(context.Background(), core.Action{
			Kind: core.ActionAdvanceTime, TargetTime: 20,
		}); err != nil {
			t.Fatal(err)
		}
		trace, err := r.Trace()
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(trace)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	first := run()
	second := run()
	if string(first) != string(second) {
		t.Fatalf("same seed produced different traces:\nfirst:  %s\nsecond: %s", first, second)
	}
}
