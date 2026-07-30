package goalsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
	raft "go.etcd.io/raft/v3"
)

func TestDefinitionsAreVersionedUniqueAndStable(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 2 {
		t.Fatalf("definitions=%d want 2", len(definitions))
	}
	seen := make(map[GoalID]bool)
	for _, definition := range definitions {
		if definition.SchemaVersion != SchemaVersion {
			t.Fatalf("goal %s schema=%q", definition.GoalID, definition.SchemaVersion)
		}
		if seen[definition.GoalID] {
			t.Fatalf("duplicate goal %s", definition.GoalID)
		}
		seen[definition.GoalID] = true
		first, err := SerializeDefinition(definition)
		if err != nil {
			t.Fatal(err)
		}
		second, err := SerializeDefinition(definition)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("goal %s serialization is unstable", definition.GoalID)
		}
		for index, waypoint := range definition.Waypoints {
			want := "W" + string(rune('1'+index))
			if waypoint.ID != want {
				t.Fatalf("goal %s waypoint[%d]=%s want %s", definition.GoalID, index, waypoint.ID, want)
			}
		}
	}
	if _, err := Definition("missing", 3); err == nil {
		t.Fatal("unknown goal was accepted")
	}
	if _, err := Definition(GoalSnapshotCatchUpAfterPartition, 4); err == nil {
		t.Fatal("unsupported node count was accepted")
	}
}

func TestStableFollowerBindingDoesNotDependOnNodeOrder(t *testing.T) {
	definition, err := Definition(GoalSnapshotCatchUpAfterPartition, 3)
	if err != nil {
		t.Fatal(err)
	}
	first := stableObservation([]core.NodeID{1, 2, 3})
	second := stableObservation([]core.NodeID{3, 1, 2})
	for index, observation := range []core.Observation{first, second} {
		evaluator, newErr := NewEvaluator(definition, "binding", true)
		if newErr != nil {
			t.Fatal(newErr)
		}
		if resetErr := evaluator.Reset(observation); resetErr != nil {
			t.Fatal(resetErr)
		}
		result := evaluator.Result()
		if got := result.Instance.Bindings[SymbolLeader].Node; got != 1 {
			t.Fatalf("case %d leader=%s want n1", index, got)
		}
		// Both followers have the same log position, so the declared stable
		// tie-break chooses the greatest NodeID.
		if got := result.Instance.Bindings[SymbolTargetFollower].Node; got != 3 {
			t.Fatalf("case %d target=%s want n3", index, got)
		}
		if result.Instance.Progress.CompletedWaypointCount != 1 {
			t.Fatalf("case %d completed=%d", index, result.Instance.Progress.CompletedWaypointCount)
		}
	}
}

func TestProgressOrderingIsLexicographicAndDeterministic(t *testing.T) {
	base := GoalProgress{
		CompletedWaypointCount: 2, DistanceToCurrent: 3,
		EvidenceStrength: 2, PrefixLength: 10, StableKey: "b",
	}
	cases := []GoalProgress{
		func() GoalProgress { value := base; value.CompletedWaypointCount = 3; return value }(),
		func() GoalProgress { value := base; value.DistanceToCurrent = 2; return value }(),
		func() GoalProgress { value := base; value.EvidenceStrength = 3; return value }(),
		func() GoalProgress { value := base; value.PrefixLength = 9; return value }(),
		func() GoalProgress { value := base; value.StableKey = "a"; return value }(),
	}
	for index, better := range cases {
		if !BetterProgress(better, base) {
			t.Fatalf("case %d was not better", index)
		}
		if BetterProgress(base, better) {
			t.Fatalf("case %d ordering is not antisymmetric", index)
		}
	}
}

func TestHintStrengthModesAreDeterministicAndDoNotLeakGoalIntoNone(t *testing.T) {
	definition, err := Definition(GoalSnapshotCatchUpAfterPartition, 3)
	if err != nil {
		t.Fatal(err)
	}
	observation := stableObservation([]core.NodeID{1, 2, 3})
	observation.Messages = []core.MessageObservation{{
		ID: 7, From: 1, To: 3, SenderEpoch: 1, LinkSequence: 1,
		Position: 0, TypeHint: "MsgSnap",
	}}
	evaluation := EvaluationResult{
		FinalObservation: observation,
		Instance: GoalInstance{
			Progress: GoalProgress{CurrentWaypointIndex: 5},
			Bindings: map[Symbol]Binding{
				SymbolLeader:         {Symbol: SymbolLeader, Node: 1},
				SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
			},
			WaypointResults: make([]WaypointResult, 7),
		},
	}
	parent := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
	}}
	first, firstStats, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 91, 10,
		MutationOptions{HintStrength: HintNone},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 91, 10,
		MutationOptions{HintStrength: HintNone},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed produced different none mutations\n%+v\n%+v", first, second)
	}
	if first.Plan.Metadata["goal_id"] != "" || first.Plan.Metadata["waypoint"] != "" ||
		firstStats.ExactMessageUses != 0 {
		t.Fatalf("none leaked goal information: plan=%+v stats=%+v", first.Plan, firstStats)
	}
	weak, weakStats, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 91, 10,
		MutationOptions{HintStrength: HintWeak},
	)
	if err != nil {
		t.Fatal(err)
	}
	if weakStats.ExactMessageUses != 0 ||
		weak.Plan.Metadata["hint_strength"] != string(HintWeak) {
		t.Fatalf("weak mutation=%+v stats=%+v", weak, weakStats)
	}
	rebound := evaluation
	rebound.Instance.Bindings = map[Symbol]Binding{
		SymbolLeader:         {Symbol: SymbolLeader, Node: 2},
		SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 1},
	}
	weakRebound, _, err := MutateTowardWaypointWithOptions(
		definition, parent, rebound, 91, 10,
		MutationOptions{HintStrength: HintWeak},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(weak.Plan, weakRebound.Plan) {
		t.Fatalf("weak hint used symbolic node binding\noriginal=%+v\nrebound=%+v",
			weak.Plan, weakRebound.Plan)
	}
}

func TestStrongHintUsesBoundSnapshotMessageAndNoPrefixCanEditParent(t *testing.T) {
	definition, err := Definition(GoalSnapshotCatchUpAfterPartition, 3)
	if err != nil {
		t.Fatal(err)
	}
	observation := stableObservation([]core.NodeID{1, 2, 3})
	observation.Messages = []core.MessageObservation{
		{ID: 8, From: 1, To: 3, SenderEpoch: 1, LinkSequence: 1, Position: 0, TypeHint: "MsgSnap"},
		{ID: 9, From: 1, To: 3, SenderEpoch: 1, LinkSequence: 2, Position: 1, TypeHint: "MsgSnap"},
	}
	results := make([]WaypointResult, 7)
	results[5].RelatedMessageIDs = []core.MessageID{9}
	evaluation := EvaluationResult{
		FinalObservation: observation,
		Instance: GoalInstance{
			Progress: GoalProgress{CurrentWaypointIndex: 5},
			Bindings: map[Symbol]Binding{
				SymbolLeader:         {Symbol: SymbolLeader, Node: 1},
				SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
			},
			WaypointResults: results,
		},
	}
	parent := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		{Kind: plan.ActionTimeout, Node: 1},
		{Kind: plan.ActionRequest, Node: 1, Request: "1"},
	}}
	strong, stats, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 2, 10,
		MutationOptions{
			HintStrength: HintStrong, AllowWholePlanMutation: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ExactMessageUses != 1 || stats.WholePlanEdits != 1 ||
		strong.PreservedPrefixLength != 0 ||
		strong.Plan.Actions[len(strong.Plan.Actions)-1].Messages.Start != 1 {
		t.Fatalf("strong no-prefix mutation=%+v stats=%+v", strong, stats)
	}
}

func TestDistanceModesKeepTargetPredicateButRemovePartialOrdering(t *testing.T) {
	definition, err := Definition(GoalSnapshotCatchUpAfterPartition, 3)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := NewEvaluatorWithDistance(definition, "staged", true, DistanceStaged)
	if err != nil {
		t.Fatal(err)
	}
	boolean, err := NewEvaluatorWithDistance(definition, "boolean", true, DistanceBooleanOnly)
	if err != nil {
		t.Fatal(err)
	}
	partial := evalDistance{value: 4, explanation: "partial", decidable: true}
	if got := staged.effectiveDistance(false, partial); got.value != 4 {
		t.Fatalf("staged distance=%+v", got)
	}
	if got := boolean.effectiveDistance(false, partial); got.value != 1 {
		t.Fatalf("boolean distance=%+v", got)
	}
	if got := boolean.effectiveDistance(true, evalDistance{value: 0, decidable: true}); got.value != 0 {
		t.Fatalf("boolean changed successful predicate=%+v", got)
	}
	if staged.definition.TargetPredicate != boolean.definition.TargetPredicate {
		t.Fatal("distance mode changed target predicate")
	}
}

func TestSupportedFrontierTopKValuesAndInvalidZero(t *testing.T) {
	for _, topK := range []int{1, 2, 4, 8} {
		frontier, err := NewFrontier(topK)
		if err != nil {
			t.Fatalf("K=%d: %v", topK, err)
		}
		if got := frontier.Snapshot().TopK; got != topK {
			t.Fatalf("K=%d snapshot=%d", topK, got)
		}
	}
	if _, err := NewFrontier(0); err == nil {
		t.Fatal("K=0 was accepted")
	}
}

func TestSemanticTraceDiversityGroupsAbsoluteTermAndIndexShifts(t *testing.T) {
	makeTrace := func(termOffset, indexOffset uint64) core.Trace {
		nodes := []core.NodeObservation{
			observedNode(1, "leader", 2+termOffset, 4+indexOffset, 3+indexOffset),
			observedNode(2, "follower", 2+termOffset, 3+indexOffset, 3+indexOffset),
			observedNode(3, "follower", 1+termOffset, 2+indexOffset, 2+indexOffset),
		}
		for index := range nodes {
			nodes[index].Semantic["applied"] = nodes[index].Semantic["commit"]
			nodes[index].Semantic["snapshot_index"] = uint64(1) + indexOffset
		}
		return core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "diversity", Seed: 1,
			Steps: []core.StepRecord{{
				Index: 0, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
				NodesAfter: nodes,
			}},
		}
	}
	first := makeTrace(0, 0)
	shifted := makeTrace(10, 20)
	if TraceKey(first) == TraceKey(shifted) {
		t.Fatal("exact trace key ignored absolute values")
	}
	if SemanticTraceKey(first) != SemanticTraceKey(shifted) {
		t.Fatal("semantic trace key did not group equivalent relative schedule")
	}
	if ProgressPathKey([]ProgressPoint{{1, 3}, {2, 1}}) ==
		ProgressPathKey([]ProgressPoint{{1, 3}, {2, 2}}) {
		t.Fatal("different progress paths were grouped")
	}
}

func TestFrontierTopKSemanticDedupAndCorpusIsolation(t *testing.T) {
	defaultCorpus := corpus.New()
	before := defaultCorpus.Snapshot()
	frontier, err := NewFrontier(2)
	if err != nil {
		t.Fatal(err)
	}
	makeSeed := func(id, semantic string, distance int) FrontierSeed {
		return FrontierSeed{
			ID: id, GoalID: GoalSnapshotCatchUpAfterPartition,
			WaypointIndex: 2, WaypointID: "W3", PrefixPlanEnd: 0, PrefixTraceEnd: 0,
			SemanticKey: semantic,
			Progress: GoalProgress{
				CompletedWaypointCount: 2, CurrentWaypointIndex: 2,
				DistanceToCurrent: distance, EvidenceStrength: 1,
				PrefixLength: 1, StableKey: id,
			},
		}
	}
	for _, seed := range []FrontierSeed{
		makeSeed("a", "shape-a", 3),
		makeSeed("b", "shape-b", 2),
		makeSeed("c", "shape-c", 1),
	} {
		if _, err := frontier.Consider(seed); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := frontier.Snapshot()
	if len(snapshot.Seeds) != 2 || snapshot.Stats.Evicted != 1 {
		t.Fatalf("frontier=%+v", snapshot)
	}
	duplicate := makeSeed("d", "shape-c", 4)
	if retained, err := frontier.Consider(duplicate); err != nil || retained {
		t.Fatalf("worse semantic duplicate retained=%v err=%v", retained, err)
	}
	if frontier.Snapshot().Stats.Deduplicated != 1 {
		t.Fatalf("dedup stats=%+v", frontier.Snapshot().Stats)
	}
	after := defaultCorpus.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("goal frontier modified default Corpus\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestSeedFromResultTrimsBoundaryMessageBatch(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionDeliver,
		Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 1, To: 2}, Start: 0, Count: 2,
		},
	}}}
	run := engine.Result{
		Trace: core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "prefix", Seed: 1,
			Steps: []core.StepRecord{{Index: 0}, {Index: 1}},
		},
		Resolutions: []plan.Resolution{{
			Actions: []core.Action{{Kind: core.ActionDeliver}, {Kind: core.ActionDeliver}},
		}},
	}
	evaluation := EvaluationResult{
		PrefixEndActionIndex: 0, PrefixEndTraceStep: 0,
		Instance: GoalInstance{
			GoalID: GoalSnapshotCatchUpAfterPartition,
			Progress: GoalProgress{
				CompletedWaypointCount: 1, CurrentWaypointIndex: 1,
				CurrentWaypointID: "W2", DistanceToCurrent: 1,
			},
		},
	}
	seed, err := SeedFromResult(
		"seed", "", 0, 1, "prefix", sequence, run, evaluation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := seed.PrefixPlan.Actions[0].Messages.Count; got != 1 {
		t.Fatalf("trimmed selector count=%d want 1", got)
	}
	if len(seed.PrefixTrace.Steps) != 1 {
		t.Fatalf("trace prefix steps=%d", len(seed.PrefixTrace.Steps))
	}
}

func TestSnapshotRequiredNeedsStorageBoundaryNotOnlyLargeLag(t *testing.T) {
	leader := observedNode(1, "leader", 1, 10, 10)
	target := observedNode(3, "follower", 1, 0, 0)
	leader.Semantic["first_index"] = uint64(5)
	leader.Semantic["leader_progress"] = map[string]any{
		"3": map[string]any{
			"next": uint64(5), "pending_snapshot": uint64(0), "state": "StateProbe",
		},
	}
	if required, _, decidable := snapshotCatchUpRequired(leader, target, nil); required || !decidable {
		t.Fatalf("large lag within retained log required=%v decidable=%v", required, decidable)
	}
	leader.Semantic["leader_progress"].(map[string]any)["3"].(map[string]any)["next"] = uint64(4)
	if required, _, decidable := snapshotCatchUpRequired(leader, target, nil); !required || !decidable {
		t.Fatalf("next behind first required=%v decidable=%v", required, decidable)
	}
}

func TestProtocolTermMessagesExcludeClientRequests(t *testing.T) {
	for _, messageType := range []string{"MsgApp", "MsgHeartbeat", "MsgVote", "MsgVoteResp", "MsgSnap"} {
		if !protocolTermMessage(messageType) {
			t.Fatalf("%s should carry protocol term evidence", messageType)
		}
	}
	for _, messageType := range []string{"", "client-request", "MsgProp"} {
		if protocolTermMessage(messageType) {
			t.Fatalf("%s must not carry protocol term evidence", messageType)
		}
	}
}

func TestHigherTermPendingRejectsStaleSameTermAndClientMessages(t *testing.T) {
	definition, err := Definition(GoalRestartHigherTermMessage, 3)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(definition, "higher-term", true)
	if err != nil {
		t.Fatal(err)
	}
	evaluator.instance.Bindings = map[Symbol]Binding{
		SymbolLeader:         {Symbol: SymbolLeader, Node: 2},
		SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
	}
	leader := observedNode(2, "leader", 2, 2, 1)
	target := observedNode(3, "follower", 1, 0, 0)
	base := core.Observation{Nodes: []core.NodeObservation{leader, target, observedNode(1, "follower", 2, 1, 1)}}
	message := func(id core.MessageID, kind, term string) core.MessageObservation {
		return core.MessageObservation{
			ID: id, From: 2, To: 3, SenderEpoch: 1, LinkSequence: uint64(id),
			TypeHint: kind, Metadata: map[string]string{"term": term},
		}
	}
	for _, candidate := range []core.MessageObservation{
		message(1, "MsgHeartbeat", "0"),
		message(2, "MsgHeartbeat", "1"),
		message(3, "client-request", "2"),
		message(4, "MsgProp", "2"),
	} {
		observation := base.Copy()
		observation.Messages = []core.MessageObservation{candidate}
		result := evaluator.higherTermPending(evalFrame{after: observation})
		if result.satisfied {
			t.Fatalf("message %+v was incorrectly accepted", candidate)
		}
	}
	observation := base.Copy()
	observation.Messages = []core.MessageObservation{message(5, "MsgHeartbeat", "2")}
	result := evaluator.higherTermPending(evalFrame{after: observation})
	if !result.satisfied || evaluator.higherMessage != 5 ||
		len(result.messageIDs) != 1 || result.messageIDs[0] != 5 {
		t.Fatalf("higher-term result=%+v bound=%s", result, evaluator.higherMessage)
	}
}

func TestHigherTermDeliveryRequiresExactMessageAndIncompleteRecovery(t *testing.T) {
	definition, err := Definition(GoalRestartHigherTermMessage, 3)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(definition, "delivery", true)
	if err != nil {
		t.Fatal(err)
	}
	evaluator.instance.Bindings = map[Symbol]Binding{
		SymbolLeader:         {Symbol: SymbolLeader, Node: 2},
		SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
	}
	evaluator.higherMessage = 9
	leader := observedNode(2, "leader", 2, 2, 1)
	beforeTarget := observedNode(3, "follower", 1, 0, 0)
	afterTarget := observedNode(3, "follower", 2, 0, 0)
	delivered := &core.Message{
		ID: 9, From: 2, To: 3, SenderEpoch: 1, Sequence: 1,
		TypeHint: "MsgHeartbeat", Metadata: map[string]string{"term": "2"},
	}
	frame := evalFrame{
		action:    core.Action{Kind: core.ActionDeliver, Message: 9},
		before:    core.Observation{Nodes: []core.NodeObservation{leader, beforeTarget}},
		after:     core.Observation{Nodes: []core.NodeObservation{leader, afterTarget}},
		delivered: delivered,
	}
	if result := evaluator.higherTermDelivered(frame); !result.satisfied {
		t.Fatalf("valid delivery was rejected: %+v", result)
	}
	wrong := delivered.Copy()
	wrong.ID = 10
	frame.delivered = &wrong
	if result := evaluator.higherTermDelivered(frame); result.satisfied {
		t.Fatal("different MessageID was accepted")
	}
	frame.delivered = delivered
	caughtUp := observedNode(3, "follower", 2, 2, 1)
	frame.before.Nodes[1] = caughtUp
	if result := evaluator.higherTermDelivered(frame); result.satisfied || result.invalid == "" {
		t.Fatalf("post-recovery delivery result=%+v", result)
	}
}

func TestOnlineOfflineProgressAndPrefixReplayAgree(t *testing.T) {
	definition, err := Definition(GoalRestartHigherTermMessage, 3)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig := raftmodel.DefaultConfig()
	modelConfig.Profile = raftmodel.ProfileStorageSnapshot
	sequence, err := InitialPlan(modelConfig.NodeIDs, 50)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := testEngine(modelConfig, "goal-equality", 37)
	if err != nil {
		t.Fatal(err)
	}
	onlineEvaluator, err := NewEvaluator(definition, "same-trace", true)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := runner.RunObserved(context.Background(), sequence, onlineEvaluator)
	if runErr != nil {
		t.Fatal(runErr)
	}
	online := onlineEvaluator.Result()
	offline, err := Recompute(ArtifactInput{
		Definition: definition, InstanceID: "same-trace", Initial: result.Initial,
		Trace: result.Trace, Resolutions: result.Resolutions,
		ModelEvents: result.ModelEvents, ModelConfig: modelConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sameEvaluationForTest(online, offline) {
		t.Fatalf("online/offline differ\nonline=%+v\noffline=%+v", online.Instance.Progress, offline.Instance.Progress)
	}
	if online.PrefixEndTraceStep < 0 {
		t.Fatal("initial plan did not produce a replayable progress prefix")
	}
	prefix := result.Trace.Copy()
	prefix.Steps = prefix.Steps[:online.PrefixEndTraceStep+1]
	replayRuntime, err := testRuntime("goal-equality", 37)
	if err != nil {
		t.Fatal(err)
	}
	replayer, err := tracepkg.NewReplayer(replayRuntime)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := replayer.Replay(context.Background(), prefix)
	if err != nil || replayed.Status != tracepkg.StatusCompleted {
		t.Fatalf("prefix replay status=%s err=%v divergence=%+v", replayed.Status, err, replayed.Divergence)
	}
}

func stableObservation(order []core.NodeID) core.Observation {
	nodes := make([]core.NodeObservation, 0, len(order))
	for _, id := range order {
		role := "follower"
		last, commit := uint64(0), uint64(0)
		if id == 1 {
			role, last, commit = "leader", 1, 0
		}
		nodes = append(nodes, observedNode(id, role, 1, last, commit))
	}
	return core.Observation{Nodes: nodes}
}

func observedNode(id core.NodeID, role string, term, last, commit uint64) core.NodeObservation {
	return core.NodeObservation{
		ID: id, Epoch: 1, Status: core.NodeRunning,
		Semantic: map[string]any{
			"role": role, "term": term, "last_index": last, "commit": commit,
			"applied": commit, "first_index": uint64(1), "snapshot_index": uint64(0),
			"committed_prefix_available": true,
			"committed_prefix_digests":   map[string]string{},
		},
	}
}

func testEngine(config raftmodel.Config, id core.ExecutionID, seed int64) (*engine.Engine, error) {
	runtime, err := testRuntime(id, seed)
	if err != nil {
		return nil, err
	}
	resolver, err := plan.NewResolver(plan.DefaultResolverConfig())
	if err != nil {
		return nil, err
	}
	mapper, err := raftmodel.NewMapperWithConfig(config)
	if err != nil {
		return nil, err
	}
	return engine.New(runtime, resolver, mapper, nil, engine.Config{
		MaxPlanActions: 100, MaxConsecutiveNoops: 32,
	})
}

func testRuntime(id core.ExecutionID, seed int64) (*runtimepkg.Runtime, error) {
	config := etcdraft.DefaultConfig()
	config.Snapshot = etcdraft.SnapshotPolicy{Threshold: 3, RetainEntries: 1}
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		return nil, err
	}
	return runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: id, Seed: seed,
		Limits: runtimepkg.Limits{
			MaxActions: 1000, MaxTicks: 1000, MaxEffects: 10000, MaxQueuedMessages: 10000,
		},
	})
}

func sameEvaluationForTest(left, right EvaluationResult) bool {
	left.Online, right.Online = false, false
	left.StableKey, right.StableKey = "", ""
	left.Instance.StableKey, right.Instance.StableKey = "", ""
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
