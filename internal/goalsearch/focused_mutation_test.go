package goalsearch

import (
	"math/rand"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
)

type fixedAdvisor struct {
	action plan.PlanAction
}

func (fixedAdvisor) ID() string { return "fixed-test" }
func (advisor fixedAdvisor) Advise(request protocolmutation.Request) (protocolmutation.Decision, error) {
	decision := protocolmutation.NewDecision(advisor.ID(), "test-stage", request)
	decision.Candidates = []protocolmutation.Candidate{{
		Class: "fixed", Action: advisor.action, ReasonCode: "test",
		Reason: "test advisor", ExpectedEffect: "test",
	}}
	return decision, protocolmutation.FinishDecision(&decision)
}

func focusedMutationFixture(t *testing.T) (BehaviorGoalDefinition, plan.PlanSequence, EvaluationResult) {
	t.Helper()
	definition, err := Definition(GoalSnapshotCatchUpAfterPartition, 3)
	if err != nil {
		t.Fatal(err)
	}
	parent := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader", "term": uint64(1)}},
			{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower", "term": uint64(1)}},
			{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower", "term": uint64(1)}},
		},
	}
	evaluation := EvaluationResult{
		FinalObservation: observation,
		Instance: GoalInstance{
			Bindings: map[Symbol]Binding{
				SymbolLeader:         {Symbol: SymbolLeader, Node: 1},
				SymbolTargetFollower: {Symbol: SymbolTargetFollower, Node: 3},
			},
			Progress: GoalProgress{CurrentWaypointIndex: 1, CurrentWaypointID: "W2"},
		},
	}
	return definition, parent, evaluation
}

func TestAdvisorRecordOnlyDoesNotChangeWeakCandidate(t *testing.T) {
	definition, parent, evaluation := focusedMutationFixture(t)
	seed := int64(7719)
	legacy, _, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, seed, 20,
		MutationOptions{HintStrength: HintWeak},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorded, _, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, seed, 20,
		MutationOptions{
			HintStrength: HintWeak, AdvisorRecordOnly: true,
			Advisor: fixedAdvisor{action: plan.PlanAction{Kind: plan.ActionCrash, Node: 2}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	delete(recorded.Plan.Metadata, "mutation_advisor")
	delete(recorded.Plan.Metadata, "mutation_advisor_stage")
	delete(recorded.Plan.Metadata, "mutation_advisor_key")
	if PlanKey(legacy.Plan) != PlanKey(recorded.Plan) || legacy.Operator != recorded.Operator {
		t.Fatalf("record-only changed candidate:\nlegacy=%+v\nrecorded=%+v", legacy, recorded)
	}
	if recorded.AdvisorDecision == nil {
		t.Fatal("record-only omitted advisor decision")
	}
}

func TestInvalidAdvisorActionIsRejectedByMutation(t *testing.T) {
	definition, parent, evaluation := focusedMutationFixture(t)
	_, _, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 1, 20,
		MutationOptions{
			HintStrength: HintWeak,
			Advisor:      fixedAdvisor{action: plan.PlanAction{Kind: plan.ActionRequest, Node: 1}},
		},
	)
	if err == nil {
		t.Fatal("advisor action without request value was accepted")
	}
}

func TestNoAdvisorRetainsLegacyWeakRNGChoice(t *testing.T) {
	definition, _, evaluation := focusedMutationFixture(t)
	random := rand.New(rand.NewSource(99))
	expected, expectedOperator := weightedCategoryAction(
		definition, 1, evaluation, evaluation.FinalObservation, random, true, nil, 1,
	)
	parent := plan.PlanSequence{}
	mutated, _, err := MutateTowardWaypointWithOptions(
		definition, parent, evaluation, 99, 20,
		MutationOptions{HintStrength: HintWeak},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) != 1 || len(mutated.Plan.Actions) != 1 ||
		expectedOperator != mutated.Operator ||
		expected[0].Kind != mutated.Plan.Actions[0].Kind {
		t.Fatalf("legacy weak selection changed: expected %s/%+v got %s/%+v",
			expectedOperator, expected, mutated.Operator, mutated.Plan.Actions)
	}
}

func TestAdvisorIsReplaceableThroughProtocolNeutralInterface(t *testing.T) {
	var advisor protocolmutation.Advisor = fixedAdvisor{
		action: plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1},
	}
	if advisor.ID() != "fixed-test" {
		t.Fatalf("replaceable advisor ID=%q", advisor.ID())
	}
}
