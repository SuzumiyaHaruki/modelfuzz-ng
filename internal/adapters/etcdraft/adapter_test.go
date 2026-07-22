package etcdraft

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"strconv"
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

func dropObserved(t *testing.T, r *runtimepkg.Runtime, message core.MessageObservation) runtimepkg.StepResult {
	t.Helper()
	result, err := r.Execute(context.Background(), core.Action{
		Kind: core.ActionDrop, Message: message.ID,
		Selector: &core.MessageSelector{Link: core.LinkID{From: message.From, To: message.To}, Position: message.Position},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func findMessageOK(observation core.Observation, typeHint string, from, to core.NodeID) (core.MessageObservation, bool) {
	for _, message := range observation.Messages {
		if message.TypeHint == typeHint && message.From == from && message.To == to {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}

func findSnapshotMessage(t *testing.T, observation core.Observation, from, to core.NodeID, index uint64) core.MessageObservation {
	t.Helper()
	if message, found := findSnapshotMessageOK(observation, from, to, index); found {
		return message
	}
	t.Fatalf("snapshot %s->%s at index %d not found in %+v", from, to, index, observation.Messages)
	return core.MessageObservation{}
}

func findSnapshotMessageOK(observation core.Observation, from, to core.NodeID, index uint64) (core.MessageObservation, bool) {
	want := strconv.FormatUint(index, 10)
	for _, message := range observation.Messages {
		if message.TypeHint == "MsgSnap" && message.From == from && message.To == to &&
			message.Metadata["snapshot_index"] == want {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}

func currentObservation(t *testing.T, r *runtimepkg.Runtime) core.Observation {
	t.Helper()
	observation, err := r.CurrentObservation()
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func dropMessagesTo(t *testing.T, r *runtimepkg.Runtime, to core.NodeID) {
	t.Helper()
	for {
		observation := currentObservation(t, r)
		var selected core.MessageObservation
		found := false
		for _, message := range observation.Messages {
			if message.To == to {
				selected, found = message, true
				break
			}
		}
		if !found {
			return
		}
		dropObserved(t, r, selected)
	}
}

func dropMessagesToExcept(t *testing.T, r *runtimepkg.Runtime, to core.NodeID, keep core.MessageID) {
	t.Helper()
	for {
		observation := currentObservation(t, r)
		var selected core.MessageObservation
		found := false
		for _, message := range observation.Messages {
			if message.To == to && message.ID != keep {
				selected, found = message, true
				break
			}
		}
		if !found {
			return
		}
		dropObserved(t, r, selected)
	}
}

func replicateLeaderToFollower(t *testing.T, r *runtimepkg.Runtime, leader, follower core.NodeID) {
	t.Helper()
	for attempts := 0; attempts < 100; attempts++ {
		observation := currentObservation(t, r)
		leaderState := observation.Nodes[int(leader)-1].Semantic
		if leaderState["commit"] == leaderState["last_index"] {
			return
		}
		message, found := findMessageOK(observation, "MsgApp", leader, follower)
		if !found {
			message, found = findMessageOK(observation, "MsgHeartbeat", leader, follower)
		}
		if !found {
			t.Fatalf("no replication message %s->%s in %+v", leader, follower, observation.Messages)
		}
		result := deliverObserved(t, r, message)
		response, found := findMessageOK(result.Observation, "MsgAppResp", follower, leader)
		if !found {
			response, found = findMessageOK(result.Observation, "MsgHeartbeatResp", follower, leader)
		}
		if !found {
			t.Fatalf("no replication response %s->%s in %+v", follower, leader, result.Observation.Messages)
		}
		deliverObserved(t, r, response)
	}
	t.Fatal("replication did not converge")
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

func TestAdapterReforwardsQueuedProposalAfterOriginalSenderCrashes(t *testing.T) {
	r := newTestRuntime(t, testConfig(1, 2, 3))
	ctx := context.Background()
	timeout, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	voteResult := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
	leaderResult := deliverObserved(t, r, findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1))
	deliverObserved(t, r, findMessage(t, leaderResult.Observation, "MsgApp", 1, 2))
	deliverObserved(t, r, findMessage(t, currentObservation(t, r), "MsgApp", 1, 3))

	requested, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 3, Request: []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	proposal := findMessage(t, requested.Observation, "MsgProp", 3, 1)

	secondElection, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 2})
	if err != nil {
		t.Fatal(err)
	}
	secondVote := deliverObserved(t, r, findMessage(t, secondElection.Observation, "MsgVote", 2, 1))
	secondLeader := deliverObserved(t, r, findMessage(t, secondVote.Observation, "MsgVoteResp", 1, 2))
	deliverObserved(t, r, findMessage(t, secondLeader.Observation, "MsgApp", 2, 1))

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 3}); err != nil {
		t.Fatal(err)
	}
	forwarded := deliverObserved(t, r, findMessage(t, currentObservation(t, r), "MsgProp", proposal.From, proposal.To))
	reforwarded := findMessage(t, forwarded.Observation, "MsgProp", 3, 2)
	if reforwarded.SenderEpoch != proposal.SenderEpoch {
		t.Fatalf("reforwarded proposal sender epoch = %d, want original epoch %d", reforwarded.SenderEpoch, proposal.SenderEpoch)
	}
}

func TestAdapterRecordsForwardedProposalDropWithoutFailingDelivery(t *testing.T) {
	r := newTestRuntime(t, testConfig(1, 2, 3))
	ctx := context.Background()
	timeout, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	voteResult := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
	leaderResult := deliverObserved(t, r, findMessage(t, voteResult.Observation, "MsgVoteResp", 2, 1))
	deliverObserved(t, r, findMessage(t, leaderResult.Observation, "MsgApp", 1, 2))

	forwarded, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	proposal := findMessage(t, forwarded.Observation, "MsgProp", 2, 1)

	// 让 node 2 进入更高任期，并把它的投票请求交给旧 leader。node 1
	// 随后已不再是 leader，先前排队的 MsgProp 到达时会被正常丢弃。
	newElection, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 2})
	if err != nil {
		t.Fatal(err)
	}
	steppedDown := deliverObserved(t, r, findMessage(t, newElection.Observation, "MsgVote", 2, 1))
	dropped := deliverObserved(t, r, findMessage(t, steppedDown.Observation, "MsgProp", proposal.From, proposal.To))

	foundDelivered, foundDrop := false, false
	for _, effect := range dropped.Record.Effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
			continue
		}
		switch effect.ModelEvent.Name {
		case deliveredMessageEvent:
			foundDelivered = true
		case proposalDroppedEvent:
			foundDrop = effect.ModelEvent.Params["source"] == "forwarded"
		}
	}
	if !foundDelivered || !foundDrop {
		t.Fatalf("forwarded proposal drop effects = %+v", dropped.Record.Effects)
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

func TestSnapshotPolicyDefaultsDisabled(t *testing.T) {
	config := DefaultConfig()
	if config.Snapshot.Threshold != 0 || config.Snapshot.RetainEntries != 0 {
		t.Fatalf("default snapshot policy = %+v", config.Snapshot)
	}
}

func TestAdapterCreatesSnapshotCompactsAndPreservesPrefixAcrossRestart(t *testing.T) {
	config := testConfig(1)
	config.Snapshot = SnapshotPolicy{Threshold: 2, RetainEntries: 1}
	r := newTestRuntime(t, config)
	ctx := context.Background()
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	created, compacted := false, false
	for _, effect := range result.Record.Effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
			continue
		}
		switch effect.ModelEvent.Name {
		case "raft.snapshot_created":
			created = true
		case "raft.log_compacted":
			compacted = effect.ModelEvent.Params["compacted_entries"] == uint64(1)
		}
	}
	semantic := result.Observation.Nodes[0].Semantic
	if !created || !compacted || semantic["snapshot_index"] != uint64(2) || semantic["first_index"] != uint64(2) {
		t.Fatalf("snapshot effects/state = %+v / %+v", result.Record.Effects, semantic)
	}
	if semantic["committed_prefix_available"] != true {
		t.Fatalf("committed prefix unavailable after compact: %+v", semantic)
	}
	prefixes := semantic["committed_prefix_digests"]
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 1}); err != nil {
		t.Fatal(err)
	}
	restarted, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	after := restarted.Observation.Nodes[0].Semantic
	if after["snapshot_index"] != uint64(2) || after["first_index"] != uint64(2) ||
		!reflect.DeepEqual(after["committed_prefix_digests"], prefixes) {
		t.Fatalf("snapshot state changed across restart: before=%+v after=%+v", semantic, after)
	}
}

func TestLaggingFollowerReceivesSnapshotThroughRuntimeAndRejectsDuplicate(t *testing.T) {
	config := testConfig(1, 2, 3)
	config.Snapshot = SnapshotPolicy{Threshold: 2}
	r := newTestRuntime(t, config)
	ctx := context.Background()
	timeout, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	vote := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
	leader := deliverObserved(t, r, findMessage(t, vote.Observation, "MsgVoteResp", 2, 1))
	if leader.Observation.Nodes[0].Semantic["role"] != "leader" {
		t.Fatalf("node 1 did not become leader: %+v", leader.Observation.Nodes[0])
	}
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 3}); err != nil {
		t.Fatal(err)
	}
	dropMessagesTo(t, r, 3)
	replicateLeaderToFollower(t, r, 1, 2)

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	dropMessagesTo(t, r, 3)
	replicateLeaderToFollower(t, r, 1, 2)
	dropMessagesTo(t, r, 3)
	observation := currentObservation(t, r)
	if observation.Nodes[0].Semantic["first_index"] != uint64(3) {
		t.Fatalf("leader did not compact through snapshot: %+v", observation.Nodes[0].Semantic)
	}
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 3}); err != nil {
		t.Fatal(err)
	}
	observation = currentObservation(t, r)

	ticked, err := r.Execute(ctx, core.Action{Kind: core.ActionAdvanceTime, TargetTime: observation.Time + 1})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := findMessage(t, ticked.Observation, "MsgHeartbeat", 1, 3)
	heartbeatResult := deliverObserved(t, r, heartbeat)
	heartbeatResponse := findMessage(t, heartbeatResult.Observation, "MsgHeartbeatResp", 3, 1)
	snapshotResult := deliverObserved(t, r, heartbeatResponse)
	snapshotMessage := findMessage(t, snapshotResult.Observation, "MsgSnap", 1, 3)

	duplicated, err := r.Execute(ctx, core.Action{
		Kind: core.ActionDuplicate, Message: snapshotMessage.ID,
		Selector: &core.MessageSelector{Link: core.LinkID{From: 1, To: 3}, Position: snapshotMessage.Position},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCopy := findMessage(t, duplicated.Observation, "MsgSnap", 1, 3)
	applied := deliverObserved(t, r, firstCopy)
	if applied.Observation.Nodes[2].Semantic["snapshot_index"] != uint64(2) ||
		applied.Observation.Nodes[2].Semantic["applied"] != uint64(2) {
		t.Fatalf("follower did not apply snapshot: %+v", applied.Observation.Nodes[2].Semantic)
	}
	remaining := findMessage(t, applied.Observation, "MsgSnap", 1, 3)
	stale := deliverObserved(t, r, remaining)
	foundStale := false
	for _, effect := range stale.Record.Effects {
		if effect.Kind == core.EffectModelEvent && effect.ModelEvent.Name == "raft.snapshot_rejected_or_stale" {
			foundStale = true
		}
	}
	if !foundStale || stale.Observation.Nodes[2].Semantic["applied"] != uint64(2) {
		t.Fatalf("duplicate snapshot was not a stable stale delivery: effects=%+v node=%+v",
			stale.Record.Effects, stale.Observation.Nodes[2].Semantic)
	}
	if response, found := findMessageOK(stale.Observation, "MsgAppResp", 3, 1); found {
		deliverObserved(t, r, response)
	}
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("2")}); err != nil {
		t.Fatal(err)
	}
	replicateLeaderToFollower(t, r, 1, 2)
	for attempts := 0; attempts < 20; attempts++ {
		observation := currentObservation(t, r)
		leaderCommit := observation.Nodes[0].Semantic["commit"].(uint64)
		if observation.Nodes[2].Semantic["applied"].(uint64) >= leaderCommit {
			break
		}
		message, found := findMessageOK(observation, "MsgApp", 1, 3)
		if !found {
			message, found = findMessageOK(observation, "MsgHeartbeat", 1, 3)
		}
		if !found {
			t.Fatalf("no post-snapshot replication message: %+v", observation.Messages)
		}
		result := deliverObserved(t, r, message)
		if response, found := findMessageOK(result.Observation, "MsgAppResp", 3, 1); found {
			deliverObserved(t, r, response)
		} else if response, found := findMessageOK(result.Observation, "MsgHeartbeatResp", 3, 1); found {
			deliverObserved(t, r, response)
		}
	}
	final := currentObservation(t, r)
	if final.Nodes[2].Semantic["applied"].(uint64) < final.Nodes[0].Semantic["commit"].(uint64) ||
		final.Nodes[2].Semantic["committed_prefix_available"] != true {
		t.Fatalf("follower did not catch up after snapshot: leader=%+v follower=%+v",
			final.Nodes[0].Semantic, final.Nodes[2].Semantic)
	}
}

func TestLaggingFollowerRejectsOlderSnapshotAfterNewerSnapshotAndRestarts(t *testing.T) {
	config := testConfig(1, 2, 3)
	config.Snapshot = SnapshotPolicy{Threshold: 2}
	r := newTestRuntime(t, config)
	ctx := context.Background()

	timeout, err := r.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 1})
	if err != nil {
		t.Fatal(err)
	}
	vote := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
	leader := deliverObserved(t, r, findMessage(t, vote.Observation, "MsgVoteResp", 2, 1))
	if leader.Observation.Nodes[0].Semantic["role"] != "leader" {
		t.Fatalf("node 1 did not become leader: %+v", leader.Observation.Nodes[0])
	}

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 3}); err != nil {
		t.Fatal(err)
	}
	dropMessagesTo(t, r, 3)
	replicateLeaderToFollower(t, r, 1, 2)
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	dropMessagesTo(t, r, 3)
	replicateLeaderToFollower(t, r, 1, 2)
	dropMessagesTo(t, r, 3)

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 3}); err != nil {
		t.Fatal(err)
	}
	observation := currentObservation(t, r)
	ticked, err := r.Execute(ctx, core.Action{Kind: core.ActionAdvanceTime, TargetTime: observation.Time + 1})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := findMessage(t, ticked.Observation, "MsgHeartbeat", 1, 3)
	heartbeatResult := deliverObserved(t, r, heartbeat)
	heartbeatResponse := findMessage(t, heartbeatResult.Observation, "MsgHeartbeatResp", 3, 1)
	oldSnapshotResult := deliverObserved(t, r, heartbeatResponse)
	oldSnapshot := findSnapshotMessage(t, oldSnapshotResult.Observation, 1, 3, 2)
	duplicated, err := r.Execute(ctx, core.Action{
		Kind: core.ActionDuplicate, Message: oldSnapshot.ID,
		Selector: &core.MessageSelector{
			Link: core.LinkID{From: oldSnapshot.From, To: oldSnapshot.To}, Position: oldSnapshot.Position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot = findSnapshotMessage(t, duplicated.Observation, 1, 3, 2)
	appliedOld := deliverObserved(t, r, oldSnapshot)
	if appliedOld.Observation.Nodes[2].Semantic["snapshot_index"] != uint64(2) {
		t.Fatalf("follower did not apply old snapshot: %+v", appliedOld.Observation.Nodes[2].Semantic)
	}
	remainingOld := findSnapshotMessage(t, appliedOld.Observation, 1, 3, 2)
	oldSnapshotID := remainingOld.ID
	if response, found := findMessageOK(appliedOld.Observation, "MsgAppResp", 3, 1); found {
		deliverObserved(t, r, response)
	} else {
		t.Fatalf("snapshot application produced no response: %+v", appliedOld.Observation.Messages)
	}

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 3}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"2", "3"} {
		if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte(request)}); err != nil {
			t.Fatal(err)
		}
		dropMessagesToExcept(t, r, 3, oldSnapshotID)
		replicateLeaderToFollower(t, r, 1, 2)
		dropMessagesToExcept(t, r, 3, oldSnapshotID)
	}
	observation = currentObservation(t, r)
	if observation.Nodes[0].Semantic["snapshot_index"].(uint64) < 4 {
		t.Fatalf("leader did not create a newer snapshot: %+v", observation.Nodes[0].Semantic)
	}

	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 3}); err != nil {
		t.Fatal(err)
	}
	observation = currentObservation(t, r)
	ticked, err = r.Execute(ctx, core.Action{Kind: core.ActionAdvanceTime, TargetTime: observation.Time + 1})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat = findMessage(t, ticked.Observation, "MsgHeartbeat", 1, 3)
	heartbeatResult = deliverObserved(t, r, heartbeat)
	heartbeatResponse = findMessage(t, heartbeatResult.Observation, "MsgHeartbeatResp", 3, 1)
	newSnapshotResult := deliverObserved(t, r, heartbeatResponse)
	newSnapshot, found := findSnapshotMessageOK(newSnapshotResult.Observation, 1, 3, 4)
	for attempts := 0; !found && attempts < 10; attempts++ {
		appendMessage := findMessage(t, newSnapshotResult.Observation, "MsgApp", 1, 3)
		appendResult := deliverObserved(t, r, appendMessage)
		appendResponse := findMessage(t, appendResult.Observation, "MsgAppResp", 3, 1)
		newSnapshotResult = deliverObserved(t, r, appendResponse)
		newSnapshot, found = findSnapshotMessageOK(newSnapshotResult.Observation, 1, 3, 4)
	}
	if !found {
		t.Fatalf("leader did not send newer snapshot: %+v", newSnapshotResult.Observation.Messages)
	}
	appliedNew := deliverObserved(t, r, newSnapshot)
	if appliedNew.Observation.Nodes[2].Semantic["snapshot_index"] != uint64(4) ||
		appliedNew.Observation.Nodes[2].Semantic["applied"] != uint64(4) {
		t.Fatalf("follower did not apply newer snapshot: %+v", appliedNew.Observation.Nodes[2].Semantic)
	}

	remainingOld = findSnapshotMessage(t, appliedNew.Observation, 1, 3, 2)
	stale := deliverObserved(t, r, remainingOld)
	foundStale := false
	for _, effect := range stale.Record.Effects {
		if effect.Kind == core.EffectModelEvent && effect.ModelEvent.Name == "raft.snapshot_rejected_or_stale" {
			foundStale = true
		}
	}
	if !foundStale || stale.Observation.Nodes[2].Semantic["snapshot_index"] != uint64(4) {
		t.Fatalf("older snapshot changed follower state: effects=%+v node=%+v",
			stale.Record.Effects, stale.Observation.Nodes[2].Semantic)
	}

	beforeRestart := stale.Observation.Nodes[2].Semantic
	if _, err := r.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 3}); err != nil {
		t.Fatal(err)
	}
	restarted, err := r.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 3})
	if err != nil {
		t.Fatal(err)
	}
	afterRestart := restarted.Observation.Nodes[2].Semantic
	for _, field := range []string{"snapshot_index", "snapshot_term", "first_index", "commit", "applied"} {
		if afterRestart[field] != beforeRestart[field] {
			t.Fatalf("%s changed across post-snapshot restart: before=%v after=%v", field, beforeRestart[field], afterRestart[field])
		}
	}
	if !reflect.DeepEqual(afterRestart["committed_prefix_digests"], beforeRestart["committed_prefix_digests"]) {
		t.Fatalf("committed prefix changed across post-snapshot restart: before=%v after=%v",
			beforeRestart["committed_prefix_digests"], afterRestart["committed_prefix_digests"])
	}
}
