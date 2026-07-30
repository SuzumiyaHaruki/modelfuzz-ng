package coverageanalysis

import (
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestCoverageObservationReusesOfflineFrames(t *testing.T) {
	nodes := frameNodes()
	trace := core.Trace{Version: 1, ExecutionID: "online", Seed: 1, Steps: []core.StepRecord{
		{
			Index: 0,
			Action: core.Action{Kind: core.ActionPartition, Partition: &core.NetworkPartition{
				Groups: [][]core.NodeID{{1}, {2, 3}},
			}},
			NodesBefore: nodes, NodesAfter: nodes, ObservationDigest: "partition",
		},
		{
			Index:       1,
			Action:      core.Action{Kind: core.ActionHeal},
			NodesBefore: nodes, NodesAfter: nodes, ObservationDigest: "heal",
		},
	}}
	state := model.State{Key: 11, Text: comparisonState(2)}
	result := engine.Result{
		Status: engine.StatusCompleted, ModelExecuted: true, Trace: trace,
		ModelStates: []model.State{state}, ModelEvents: []model.Event{},
		Initial: core.Observation{Nodes: nodes},
	}
	input := ObservationInput{
		RunID: "run-1", CandidateID: "candidate-1", Source: "test",
		Plan: plan.PlanSequence{Actions: []plan.PlanAction{
			{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		}},
		Result: result, ModelConfig: raftmodel.DefaultConfig(),
	}
	online, err := BuildCoverageObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := BuildCoverageObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	online.Computation = coverageguidance.ComputationTiming{}
	repeated.Computation = coverageguidance.ComputationTiming{}
	if !reflect.DeepEqual(online, repeated) {
		t.Fatal("online semantic observation is not deterministic")
	}
	frames, err := BuildCoverageFrames(RunArtifact{
		Name: input.RunID, Source: input.Source, ModelConfig: input.ModelConfig,
		Initial: result.Initial, Trace: result.Trace, ModelStates: result.ModelStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want initial plus partition/heal stutters", len(frames))
	}
	if len(online.FacetKeys["network"]) == 0 || len(online.V2StateKeys) != 1 ||
		len(online.RawTLCFingerprints) != 1 {
		t.Fatalf("incomplete observation: %+v", online)
	}
}
