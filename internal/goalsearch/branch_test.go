package goalsearch

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestBranchCatalogIsVersionedUniqueStableAndGoalScoped(t *testing.T) {
	catalog := BranchCatalog()
	if len(catalog) != 8 {
		t.Fatalf("catalog size=%d want 8", len(catalog))
	}
	seen := make(map[BranchTemplateID]struct{})
	counts := make(map[GoalID]int)
	for _, template := range catalog {
		if template.SchemaVersion != BranchSchemaVersion {
			t.Fatalf("branch %s schema=%q", template.BranchTemplateID, template.SchemaVersion)
		}
		if _, duplicate := seen[template.BranchTemplateID]; duplicate {
			t.Fatalf("duplicate branch %s", template.BranchTemplateID)
		}
		seen[template.BranchTemplateID] = struct{}{}
		counts[template.GoalID]++
		if err := template.Validate(); err != nil {
			t.Fatal(err)
		}
		first, _ := json.Marshal(template)
		second, _ := json.Marshal(BranchCatalog())
		var decoded []BehaviorBranchTemplate
		if err := json.Unmarshal(second, &decoded); err != nil {
			t.Fatal(err)
		}
		var found BehaviorBranchTemplate
		for _, candidate := range decoded {
			if candidate.BranchTemplateID == template.BranchTemplateID {
				found = candidate
			}
		}
		roundTrip, _ := json.Marshal(found)
		if string(first) != string(roundTrip) {
			t.Fatalf("branch %s serialization is unstable", template.BranchTemplateID)
		}
	}
	if counts[GoalSnapshotCatchUpAfterPartition] < 3 ||
		counts[GoalRestartHigherTermMessage] < 3 {
		t.Fatalf("branch counts=%v", counts)
	}
	if _, err := BranchTemplate("missing"); err == nil {
		t.Fatal("unknown Branch ID was accepted")
	}
}

func TestPlannedKeyContainsNoConcreteIdentityAndSupportsDimensionAblation(t *testing.T) {
	template, err := BranchTemplate(BranchBHigherHeartbeat)
	if err != nil {
		t.Fatal(err)
	}
	first := PlannedSignature(template, BranchAblationNone)
	second := PlannedSignature(template, BranchAblationNone)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("planned signature is unstable\n%+v\n%+v", first, second)
	}
	encoded, _ := json.Marshal(first)
	for _, forbidden := range []string{"message_id", "node_id", "seed", "timestamp", "plan_hash"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("planned signature contains forbidden identity %q: %s", forbidden, encoded)
		}
	}
	ablated := PlannedSignature(template, BranchAblationKeyMessage)
	if ablated.Dimensions.KeyMessageClass != "" || ablated.StableKey == first.StableKey {
		t.Fatalf("key-message ablation did not change the signature: %+v", ablated)
	}
}

func TestBranchFeasibilityIsDeterministicAndPermanentFailureCanBeSkipped(t *testing.T) {
	template, _ := BranchTemplate(BranchASnapshotAfterHeal)
	disabled := EvaluateBranchFeasibility(template, BranchEnvironment{
		NodeCount: 3, ModelProfile: "storage-snapshot",
		SnapshotThreshold: 0, PartitionEnabled: true,
	})
	if disabled.Status != BranchPermanentlyInfeasible {
		t.Fatalf("disabled snapshot feasibility=%+v", disabled)
	}
	enabled := EvaluateBranchFeasibility(template, BranchEnvironment{
		NodeCount: 3, ModelProfile: "storage-snapshot",
		SnapshotThreshold: 3, PartitionEnabled: true,
	})
	if enabled.Status != BranchFeasible {
		t.Fatalf("enabled snapshot feasibility=%+v", enabled)
	}
	if again := EvaluateBranchFeasibility(template, BranchEnvironment{
		NodeCount: 3, ModelProfile: "storage-snapshot",
		SnapshotThreshold: 0, PartitionEnabled: true,
	}); !reflect.DeepEqual(disabled, again) {
		t.Fatalf("feasibility changed\n%+v\n%+v", disabled, again)
	}
}

func TestPlannedAndRealizedCanDeviateOnlyAfterCausalEvidence(t *testing.T) {
	delayed, _ := BranchTemplate(BranchADelayedDelivery)
	drop, _ := BranchTemplate(BranchADropAppend)
	initial := branchObservation(1, 3, 10, 20)
	evaluation := completedBranchEvaluation(GoalSnapshotCatchUpAfterPartition, 1, 3, 7)
	evaluation.Instance.WaypointResults[2].FirstReachedStep = 0
	evaluation.Instance.WaypointResults[3].FirstReachedStep = 0
	evaluation.Instance.WaypointResults[6].FirstReachedStep = 2
	evaluation.TargetReachedStep = 2
	partition := core.NetworkPartition{Groups: [][]core.NodeID{{3}, {1, 2}}}
	appendMessage := branchMessage(41, 1, 3, "MsgApp", "10")
	snapshotMessage := branchMessage(99, 1, 3, "MsgSnap", "10")
	higherHeartbeat := branchMessage(100, 1, 3, "MsgHeartbeat", "11")
	traceBeforeEvidence := core.Trace{Steps: []core.StepRecord{{
		Index: 0, Action: core.Action{Kind: core.ActionPartition, Partition: &partition},
		Effects:     []core.Effect{{Kind: core.EffectSendMessage, Message: &appendMessage}},
		NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
	}}}
	partialEvaluation := evaluation
	partialEvaluation.TargetReached = false
	partialEvaluation.Instance.Progress.CompletedWaypointCount = 1
	for index := 1; index < len(partialEvaluation.Instance.WaypointResults); index++ {
		partialEvaluation.Instance.WaypointResults[index].Reached = false
	}
	partial, err := AnalyzeBranch(drop, partialEvaluation, initial, traceBeforeEvidence, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Deviation.Occurred {
		t.Fatalf("deviation appeared before lag evidence: %+v", partial.Deviation)
	}
	trace := traceBeforeEvidence.Copy()
	trace.Steps = append(trace.Steps,
		core.StepRecord{
			Index: 1, Action: core.Action{Kind: core.ActionHeal},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		core.StepRecord{
			Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		core.StepRecord{
			Index: 3, Action: core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1},
			Effects: []core.Effect{
				{Kind: core.EffectSendMessage, Message: &snapshotMessage},
				{Kind: core.EffectSendMessage, Message: &higherHeartbeat},
			},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
	)
	realizedDelayed, err := AnalyzeBranch(delayed, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if got := realizedDelayed.RealizedBranchSignature.Dimensions.LagConstructionClass; got != "delayed-delivery" {
		t.Fatalf("lag construction=%q", got)
	}
	if got := realizedDelayed.RealizedBranchSignature.Dimensions.HealTimingClass; got != "snapshot-after-heal" {
		t.Fatalf("heal timing=%q", got)
	}
	if dimensions := realizedDelayed.RealizedBranchSignature.Dimensions; dimensions.KeyMessageClass != "" || dimensions.TermAdvanceClass != "" {
		t.Fatalf("Goal A leaked Goal B dimensions: %+v", dimensions)
	}
	deviated, err := AnalyzeBranch(drop, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if !deviated.Deviation.Occurred || deviated.Deviation.StepIndex < 1 {
		t.Fatalf("causal deviation=%+v", deviated.Deviation)
	}
	if deviated.RealizedBranchSignature.StableKey !=
		realizedDelayed.RealizedBranchSignature.StableKey {
		t.Fatal("realized signature incorrectly depends on planned label")
	}
}

func TestRealizedBranchIgnoresMessageIDAbsoluteOffsetsAndNodePermutation(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherHeartbeat)
	makeRun := func(leader, target core.NodeID, messageID core.MessageID, termOffset, indexOffset uint64) (
		core.Observation, EvaluationResult, core.Trace,
	) {
		initial := branchObservation(leader, target, 1+termOffset, 2+indexOffset)
		evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, leader, target, 6)
		message := branchMessage(messageID, leader, target, "MsgHeartbeat",
			stringUint(2+termOffset))
		after := initial.Copy()
		trace := core.Trace{Steps: []core.StepRecord{
			{
				Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: target},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
			},
			{
				Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: leader},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
			},
			{
				Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: target},
				Effects:     []core.Effect{{Kind: core.EffectSendMessage, Message: &message}},
				NodesBefore: initial.Nodes, NodesAfter: after.Nodes,
			},
		}}
		return initial, evaluation, trace
	}
	firstInitial, firstEval, firstTrace := makeRun(1, 3, 7, 0, 0)
	secondInitial, secondEval, secondTrace := makeRun(8, 4, 700, 20, 50)
	first, err := AnalyzeBranch(template, firstEval, firstInitial, firstTrace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AnalyzeBranch(template, secondEval, secondInitial, secondTrace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if first.RealizedBranchSignature.StableKey != second.RealizedBranchSignature.StableKey {
		t.Fatalf("semantic key changed under identity/offset shift\n%+v\n%+v",
			first.RealizedBranchSignature, second.RealizedBranchSignature)
	}
}

func TestHigherTermMessageClassesDoNotCollapse(t *testing.T) {
	heartbeat, _ := BranchTemplate(BranchBHigherHeartbeat)
	app, _ := BranchTemplate(BranchBHigherApp)
	initial := branchObservation(1, 3, 1, 2)
	evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 6)
	message := branchMessage(12, 1, 3, "MsgHeartbeat", "2")
	trace := core.Trace{Steps: []core.StepRecord{
		{Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		{Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 1},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		{Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		{Index: 3, Action: core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1},
			Effects:     []core.Effect{{Kind: core.EffectSendMessage, Message: &message}},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
	}}
	heartbeatRun, err := AnalyzeBranch(heartbeat, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeatRun.Deviation.Occurred {
		t.Fatalf("heartbeat path deviated: %+v", heartbeatRun.Deviation)
	}
	appRun, err := AnalyzeBranch(app, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if !appRun.Deviation.Occurred ||
		appRun.RealizedBranchSignature.Dimensions.KeyMessageClass != "MsgHeartbeat" {
		t.Fatalf("MsgHeartbeat was classified as MsgApp: %+v", appRun)
	}
}

func TestSameTermMessageIsNotClassifiedAsHigherTermBranch(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherApp)
	initial := branchObservation(1, 3, 2, 4)
	evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 5)
	evaluation.TargetReached = false
	message := branchMessage(17, 1, 3, "MsgApp", "2")
	initial.Messages = []core.MessageObservation{{
		ID: message.ID, From: message.From, To: message.To,
		SenderEpoch: message.SenderEpoch, LinkSequence: message.Sequence,
		Position: 0, TypeHint: message.TypeHint,
		PayloadDigest: message.PayloadDigest, Metadata: message.Metadata,
	}}
	trace := core.Trace{Steps: []core.StepRecord{
		{
			Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 1, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 2,
			Action: core.Action{
				Kind: core.ActionDeliver, Message: message.ID,
				Selector: &core.MessageSelector{Link: message.Link(), Position: 0},
			},
			Effects: []core.Effect{{
				Kind: core.EffectSendMessage, Message: &message,
			}},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
	}}
	analyzed, err := AnalyzeBranch(template, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	if analyzed.RealizedBranchSignature.Dimensions.KeyMessageClass != "" {
		t.Fatalf("same-term MsgApp became higher-term evidence: %+v",
			analyzed.RealizedBranchSignature)
	}
	if dimensions := analyzed.RealizedBranchSignature.Dimensions; dimensions.LagConstructionClass != "" ||
		dimensions.FaultDurationClass != "" ||
		dimensions.HealTimingClass != "" ||
		dimensions.SnapshotRouteClass != "" {
		t.Fatalf("Goal B leaked Goal A dimensions: %+v", dimensions)
	}
}

func TestDiversityFrontierHasFixedCapacityAndPreservesBranches(t *testing.T) {
	frontier, err := NewDiversityFrontier(2, 1, BranchRealizedAware)
	if err != nil {
		t.Fatal(err)
	}
	makeSeed := func(id, branch, semantic string, completed, distance int) FrontierSeed {
		return FrontierSeed{
			ID: id, PlannedBranchKey: "planned-" + branch,
			RealizedBranchKey: branch, RealizedBranchDecidable: true,
			BranchSemanticKey: semantic,
			PrefixPlanEnd:     0, PrefixTraceEnd: 0,
			Progress: GoalProgress{
				CompletedWaypointCount: completed, DistanceToCurrent: distance,
				PrefixLength: 1, StableKey: id,
			},
		}
	}
	for _, seed := range []FrontierSeed{
		makeSeed("a1", "a", "a-one", 3, 1),
		makeSeed("a2", "a", "a-two", 3, 2),
		makeSeed("b1", "b", "b-one", 3, 2),
	} {
		if _, err := frontier.Consider(seed); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := frontier.Snapshot()
	if len(snapshot.Seeds) != 2 || snapshot.SizesByBranch["a"] != 1 ||
		snapshot.SizesByBranch["b"] != 1 {
		t.Fatalf("fixed-capacity diversity snapshot=%+v", snapshot)
	}
	if _, err := frontier.Consider(makeSeed("c1", "c", "c-one", 0, 9)); err != nil {
		t.Fatal(err)
	}
	snapshot = frontier.Snapshot()
	if snapshot.Stats.Evicted != 2 || snapshot.Stats.EvictedByBranch["c"] != 1 {
		t.Fatalf("immediately discarded insertion was not counted: %+v", snapshot.Stats)
	}
	plannedOnly, _ := NewDiversityFrontier(2, 1, BranchPlannedOnly)
	seed := makeSeed("x", "realized-a", "one", 2, 1)
	seed.PlannedBranchKey = "same"
	_, _ = plannedOnly.Consider(seed)
	seed = makeSeed("y", "realized-b", "two", 2, 1)
	seed.PlannedBranchKey = "same"
	_, _ = plannedOnly.Consider(seed)
	if len(plannedOnly.Snapshot().SizesByBranch) != 1 {
		t.Fatal("planned-only Frontier read realized Branch")
	}
}

func TestFixedCapacityControlDoesNotChangeLegacyPerWaypointFrontier(t *testing.T) {
	makeSeed := func(id, semantic string, waypoint int) FrontierSeed {
		return FrontierSeed{
			ID: id, WaypointIndex: waypoint, SemanticKey: semantic,
			PrefixPlanEnd: 0, PrefixTraceEnd: 0,
			Progress: GoalProgress{
				CompletedWaypointCount: waypoint,
				DistanceToCurrent:      1,
				PrefixLength:           1,
				StableKey:              id,
			},
		}
	}
	control, err := NewCapacityFrontier(2)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := NewFrontier(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []FrontierSeed{
		makeSeed("w1-a", "w1-a", 1),
		makeSeed("w1-b", "w1-b", 1),
		makeSeed("w2-a", "w2-a", 2),
		makeSeed("w2-b", "w2-b", 2),
	} {
		if _, err := control.Consider(seed); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Consider(seed); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(control.Snapshot().Seeds); got != 2 {
		t.Fatalf("fixed-total control retained %d seeds, want 2", got)
	}
	legacySnapshot := legacy.Snapshot()
	if got := len(legacySnapshot.Seeds); got != 4 {
		t.Fatalf("legacy per-waypoint Frontier retained %d seeds, want 4", got)
	}
	if legacySnapshot.Sizes["1"] != 2 || legacySnapshot.Sizes["2"] != 2 {
		t.Fatalf("legacy Frontier buckets changed: %+v", legacySnapshot.Sizes)
	}
}

func containsBytes(value, wanted []byte) bool {
	for index := 0; index+len(wanted) <= len(value); index++ {
		if string(value[index:index+len(wanted)]) == string(wanted) {
			return true
		}
	}
	return false
}

func completedBranchEvaluation(
	goal GoalID, leader, target core.NodeID, completed int,
) EvaluationResult {
	results := make([]WaypointResult, completed)
	for index := range results {
		results[index] = WaypointResult{
			WaypointID: "W" + string(rune('1'+index)), Reached: true,
			FirstReachedStep: index,
		}
	}
	return EvaluationResult{
		TargetReached: true,
		Instance: GoalInstance{
			GoalID: goal, InstanceID: "branch-test",
			Bindings: map[Symbol]Binding{
				SymbolLeader:         {Symbol: SymbolLeader, Node: leader},
				SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: target},
			},
			WaypointResults: results,
			Progress:        GoalProgress{CompletedWaypointCount: completed},
		},
	}
}

func branchObservation(leader, target core.NodeID, term, index uint64) core.Observation {
	other := core.NodeID(2)
	if other == leader || other == target {
		other = 9
	}
	return core.Observation{Nodes: []core.NodeObservation{
		observedNode(leader, "leader", term, index, index),
		observedNode(other, "follower", term, index, index),
		observedNode(target, "follower", term, index-1, index-1),
	}}
}

func branchMessage(
	id core.MessageID, from, to core.NodeID, messageType, term string,
) core.Message {
	return core.Message{
		ID: id, From: from, To: to, SenderEpoch: 1, Sequence: uint64(id),
		TypeHint: messageType, PayloadDigest: "test",
		Metadata: map[string]string{"term": term},
	}
}

func stringUint(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var output [20]byte
	index := len(output)
	for value > 0 {
		index--
		output[index] = digits[value%10]
		value /= 10
	}
	return string(output[index:])
}
