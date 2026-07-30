package goalsearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestBranchEvidenceCatalogIsVersionedUniqueStableAndIdentityFree(t *testing.T) {
	catalog := BranchEvidenceCatalog()
	if len(catalog) == 0 {
		t.Fatal("empty Branch evidence catalog")
	}
	seen := make(map[string]struct{}, len(catalog))
	for _, definition := range catalog {
		if definition.SchemaVersion != BranchEvidenceSchemaVersion {
			t.Fatalf("evidence %s schema=%q", definition.EvidenceID, definition.SchemaVersion)
		}
		if _, duplicate := seen[definition.EvidenceID]; duplicate {
			t.Fatalf("duplicate evidence ID %q", definition.EvidenceID)
		}
		seen[definition.EvidenceID] = struct{}{}
		if definition.StableKey == "" {
			t.Fatalf("evidence %s has no stable key", definition.EvidenceID)
		}
		encoded, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"message_id", "node_id", "absolute_term", "absolute_index"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("evidence definition contains %q: %s", forbidden, encoded)
			}
		}
	}
	first, _ := json.Marshal(catalog)
	second, _ := json.Marshal(BranchEvidenceCatalog())
	if string(first) != string(second) {
		t.Fatal("Branch evidence catalog serialization is unstable")
	}
}

func TestSupportedEvidenceDoesNotBecomeCommitmentOrFullRealized(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherVote)
	initial := branchObservation(1, 3, 1, 2)
	evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 4)
	evaluation.TargetReached = false
	trace := core.Trace{Steps: []core.StepRecord{
		{
			Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
	}}
	instance, err := AnalyzeBranch(template, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := AnalyzeBranchEvidence(template, instance, evaluation, trace)
	if err != nil {
		t.Fatal(err)
	}
	if vector.SupportedCount < 3 || vector.Commitment.Reached ||
		vector.RealizedDecidable || vector.FullRealized {
		t.Fatalf("partial evidence was promoted: %+v", vector)
	}
	if vector.HighestLevel != EvidenceLevelSupported {
		t.Fatalf("highest level=%q want supported", vector.HighestLevel)
	}
	concreteSupport := false
	for _, dimension := range vector.Dimensions {
		if dimension.Status != EvidenceSupported &&
			dimension.Status != EvidenceCommitted {
			continue
		}
		if dimension.FirstObservedStep >= 0 && dimension.LastObservedStep >= 0 {
			concreteSupport = true
			break
		}
	}
	if !concreteSupport || vector.PrefixProtectionStep < 0 {
		t.Fatalf("stable-key normalization erased concrete evidence support: %+v", vector)
	}
}

func TestEvidenceIsPrefixCausalAndMessageIdentityDoesNotChangeKey(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherVote)
	makeRun := func(messageID core.MessageID) (
		core.Observation, EvaluationResult, core.Trace, BehaviorBranchInstance, BranchEvidenceVector,
	) {
		initial := branchObservation(1, 3, 1, 2)
		evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 5)
		evaluation.TargetReached = false
		message := branchMessage(messageID, 2, 3, "MsgVote", "2")
		trace := core.Trace{Steps: []core.StepRecord{
			{
				Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
			},
			{
				Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
			},
			{
				Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
				Effects:     []core.Effect{{Kind: core.EffectSendMessage, Message: &message}},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
			},
		}}
		instance, err := AnalyzeBranch(template, evaluation, initial, trace, BranchAblationNone)
		if err != nil {
			t.Fatal(err)
		}
		vector, err := AnalyzeBranchEvidence(template, instance, evaluation, trace)
		if err != nil {
			t.Fatal(err)
		}
		return initial, evaluation, trace, instance, vector
	}
	_, _, _, _, first := makeRun(11)
	_, _, _, _, second := makeRun(999)
	if first.StableKey != second.StableKey {
		t.Fatalf("MessageID changed semantic evidence key: %s != %s",
			first.StableKey, second.StableKey)
	}
	if !first.Commitment.Reached {
		t.Fatalf("higher-term Vote did not establish commitment: %+v", first)
	}

	initial := branchObservation(1, 3, 1, 2)
	prefixEvaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 4)
	prefixEvaluation.TargetReached = false
	prefixTrace := core.Trace{Steps: []core.StepRecord{
		{
			Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
		{
			Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes,
		},
	}}
	prefixInstance, err := AnalyzeBranch(
		template, prefixEvaluation, initial, prefixTrace, BranchAblationNone,
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := AnalyzeBranchEvidence(
		template, prefixInstance, prefixEvaluation, prefixTrace,
	)
	if err != nil {
		t.Fatal(err)
	}
	if before.Commitment.Reached {
		t.Fatal("future higher-term message retroactively committed an earlier prefix")
	}
}

func TestContradictedEvidenceIsNotInvalidatedOrCommitted(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherApp)
	initial := branchObservation(1, 3, 1, 2)
	evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 5)
	evaluation.TargetReached = false
	message := branchMessage(17, 2, 3, "MsgVote", "2")
	trace := core.Trace{Steps: []core.StepRecord{
		{Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		{Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		{Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
			Effects:     []core.Effect{{Kind: core.EffectSendMessage, Message: &message}},
			NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
	}}
	instance, err := AnalyzeBranch(template, evaluation, initial, trace, BranchAblationNone)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := AnalyzeBranchEvidence(template, instance, evaluation, trace)
	if err != nil {
		t.Fatal(err)
	}
	if !vector.Contradicted || vector.Commitment.Reached ||
		vector.HighestLevel != EvidenceLevelContradicted {
		t.Fatalf("contradicted planned MsgApp was promoted: %+v", vector)
	}
	found := false
	for _, dimension := range vector.Dimensions {
		if strings.HasSuffix(dimension.EvidenceID, "higher-term-message-pending") {
			found = dimension.Status == EvidenceContradicted
			if dimension.Status == EvidenceInvalidated {
				t.Fatal("contradicted and invalidated were collapsed")
			}
		}
	}
	if !found {
		t.Fatal("missing contradicted key-message evidence")
	}
}

func TestEvidenceFrontierKeepsCommittedDiversityWithProgressGuard(t *testing.T) {
	frontier, err := NewEvidenceFrontier(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	makeSeed := func(id, commitment string, completed, distance int, level EvidenceLevel) FrontierSeed {
		return FrontierSeed{
			ID: id, EvidenceKey: id, EvidenceLevel: level,
			CommitmentKey: commitment, NecessaryEvidenceCount: EvidenceLevelRank(level),
			Progress: GoalProgress{
				CompletedWaypointCount: completed,
				DistanceToCurrent:      distance,
				PrefixLength:           4,
				StableKey:              id,
			},
		}
	}
	for _, seed := range []FrontierSeed{
		makeSeed("a", "commit-a", 3, 1, EvidenceLevelCommitted),
		makeSeed("b", "commit-b", 3, 1, EvidenceLevelCommitted),
		makeSeed("bad", "commit-bad", 0, 0, EvidenceLevelFullRealized),
	} {
		if _, err := frontier.Consider(seed); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := frontier.Snapshot()
	if len(snapshot.Seeds) != 2 ||
		snapshot.Stats.RetainedByCommitment["commit-a"] != 1 ||
		snapshot.Stats.RetainedByCommitment["commit-b"] != 1 {
		t.Fatalf("committed diversity was not retained: %+v", snapshot)
	}
	for _, seed := range snapshot.Seeds {
		if seed.ID == "bad" {
			t.Fatal("clearly worse Goal progress monopolized Frontier through evidence")
		}
	}

	supported, err := NewEvidenceFrontier(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = supported.Consider(makeSeed("s1", "", 1, 1, EvidenceLevelSupported))
	_, _ = supported.Consider(makeSeed("s2", "", 1, 1, EvidenceLevelSupported))
	if got := len(supported.Snapshot().Seeds); got != 1 {
		t.Fatalf("supported-only seeds retained %d slots, want 1", got)
	}
	_, _ = supported.Consider(makeSeed("planned", "", 1, 1, EvidenceLevelPlanned))
	if got := len(supported.Snapshot().Seeds); got != 2 {
		t.Fatalf("planned exploration fallback was incorrectly slot-limited: %d", got)
	}
}

func TestStageBudgetAllocationIsDeterministicAndStopsContradictedBranch(t *testing.T) {
	config := StageBudgetConfig{
		InitialQuota: 1, SupportedQuota: 2, CommitmentQuota: 3,
		NextWaypointQuota: 1, PerBranchTotalCap: 8,
	}
	first, err := NewStageBudgetAllocator(
		[]BranchTemplateID{BranchBHigherVote, BranchBHigherApp}, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := NewStageBudgetAllocator(
		[]BranchTemplateID{BranchBHigherApp, BranchBHigherVote}, config,
	)
	for candidate := 0; candidate < 2; candidate++ {
		left, leftOK := first.Next(candidate)
		right, rightOK := second.Next(candidate)
		if left != right || leftOK != rightOK {
			t.Fatalf("allocation order is unstable: %q/%v != %q/%v",
				left, leftOK, right, rightOK)
		}
	}
	vector := BranchEvidenceVector{
		HighestLevel:   EvidenceLevelCommitted,
		SupportedCount: 4,
		Commitment:     BranchCommitment{Reached: true},
	}
	first.Observe(2, BranchBHigherApp, vector, 3, 17)
	state := first.Summary().States[BranchBHigherApp]
	if !state.SupportedGranted || !state.CommitmentGranted || state.Granted != 6 ||
		state.ActionUsed != 17 {
		t.Fatalf("stage quota not granted exactly once: %+v", state)
	}
	vector.Contradicted = true
	vector.HighestLevel = EvidenceLevelContradicted
	first.Observe(3, BranchBHigherApp, vector, 3, 5)
	if !first.Summary().States[BranchBHigherApp].Stopped {
		t.Fatal("contradicted branch still receives budget")
	}
	if summary := first.Summary(); summary.TotalGranted > 16 ||
		summary.TotalActions != 22 {
		t.Fatalf("stage candidate/action accounting escaped fixed bounds: %+v", summary)
	}
	if !reflect.DeepEqual(first.Ledger(), first.Ledger()) {
		t.Fatal("budget ledger is not stable")
	}
}

func TestMicroProgressRegistrySeparatesNecessaryIncidentalAndNoisy(t *testing.T) {
	registry := MicroProgressRegistry()
	if len(registry) == 0 {
		t.Fatal("empty micro-progress registry")
	}
	classes := make(map[MicroProgressClass]int)
	for _, definition := range registry {
		classes[definition.Class]++
		switch definition.Class {
		case MicroNecessary:
			if !definition.Necessary || !definition.MayExtendPrefix {
				t.Fatalf("necessary evidence cannot protect prefix: %+v", definition)
			}
		case MicroIncidental, MicroNoisy:
			if definition.Necessary || definition.MayExtendPrefix {
				t.Fatalf("incidental/noisy evidence protects prefix: %+v", definition)
			}
		}
	}
	for _, class := range []MicroProgressClass{
		MicroNecessary, MicroUseful, MicroIncidental, MicroNoisy,
	} {
		if classes[class] == 0 {
			t.Fatalf("micro-progress class %q is not represented", class)
		}
	}
}

func TestEvidenceKeyIgnoresAbsoluteTermAndIndexTranslation(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherVote)
	makeVector := func(term, index uint64) BranchEvidenceVector {
		initial := branchObservation(1, 3, term, index)
		evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 4)
		trace := core.Trace{Steps: []core.StepRecord{
			{Index: 0, Action: core.Action{Kind: core.ActionCrash, Node: 3},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
			{Index: 1, Action: core.Action{Kind: core.ActionTimeout, Node: 2},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
			{Index: 2, Action: core.Action{Kind: core.ActionRestart, Node: 3},
				NodesBefore: initial.Nodes, NodesAfter: initial.Nodes},
		}}
		instance, err := AnalyzeBranch(template, evaluation, initial, trace, BranchAblationNone)
		if err != nil {
			t.Fatal(err)
		}
		vector, err := AnalyzeBranchEvidence(template, instance, evaluation, trace)
		if err != nil {
			t.Fatal(err)
		}
		return vector
	}
	first := makeVector(2, 3)
	translated := makeVector(20, 30)
	if first.StableKey != translated.StableKey {
		t.Fatalf("absolute term/index changed evidence key: %s != %s",
			first.StableKey, translated.StableKey)
	}
}

func TestFormationFailureUsesSpecificMissingMessageCause(t *testing.T) {
	template, _ := BranchTemplate(BranchBHigherVote)
	evaluation := completedBranchEvaluation(GoalRestartHigherTermMessage, 1, 3, 4)
	instance := BehaviorBranchInstance{
		BranchTemplateID: template.BranchTemplateID,
		Feasibility:      BranchFeasible,
		Progress:         evaluation.Instance.Progress,
	}
	vector := BranchEvidenceVector{
		SchemaVersion: BranchEvidenceSchemaVersion,
		HighestLevel:  EvidenceLevelSupported,
		Commitment:    BranchCommitment{FirstStep: -1},
	}
	failure := ClassifyFormationFailure(
		"run", 3, template, instance, vector, evaluation, false, false,
	)
	if failure.SchemaVersion != FormationFailureSchemaVersion ||
		failure.PrimaryCause != FormationRequiredMessageAbsent ||
		!failure.RequiredMessageAbsent {
		t.Fatalf("failure-to-form classification=%+v", failure)
	}
	if failure.StableKey == "" {
		t.Fatal("failure-to-form has no deterministic key")
	}
}

func TestEvidencePriorityMultiplierFavorsButDoesNotScriptNextCategory(t *testing.T) {
	definition, err := Definition(GoalRestartHigherTermMessage, 3)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := InitialPlan([]core.NodeID{1, 2, 3}, 50)
	if err != nil {
		t.Fatal(err)
	}
	observation := branchObservation(1, 3, 2, 2)
	for index := range observation.Nodes {
		if observation.Nodes[index].ID == 3 {
			observation.Nodes[index].Status = core.NodeCrashed
		}
	}
	evaluation := EvaluationResult{
		Instance: GoalInstance{
			Bindings: map[Symbol]Binding{
				SymbolLeader:         {Symbol: SymbolLeader, Node: 1},
				SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
			},
			Progress: GoalProgress{
				CompletedWaypointCount: 3,
				CurrentWaypointIndex:   3,
				CurrentWaypointID:      "W4",
				DistanceToCurrent:      1,
			},
		},
		FinalObservation: observation,
	}
	template, _ := BranchTemplate(BranchBHigherVote)
	countRestarts := func(multiplier int) int {
		restarts := 0
		for seed := int64(1); seed <= 100; seed++ {
			mutation, _, err := MutateTowardWaypointWithOptions(
				definition, parent, evaluation, seed, 60,
				MutationOptions{
					HintStrength: HintWeak, PlannedBranch: &template,
					EvidencePriorityMultiplier: multiplier,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if mutation.Plan.Actions[len(mutation.Plan.Actions)-1].Kind == "restart" {
				restarts++
			}
		}
		return restarts
	}
	baseline := countRestarts(1)
	evidence := countRestarts(16)
	if evidence <= baseline || evidence == 100 {
		t.Fatalf("priority multiplier baseline=%d evidence=%d", baseline, evidence)
	}
}
