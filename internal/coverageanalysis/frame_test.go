package coverageanalysis

import (
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
)

func TestBuildCoverageFramesKeepsZeroEventNetworkSteps(t *testing.T) {
	nodes := frameNodes()
	partition := core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2, 3}}}
	trace := core.Trace{
		Version: core.CurrentTraceVersion, ExecutionID: "frame-network", Seed: 1,
		Steps: []core.StepRecord{
			{
				Index: 0, Action: core.Action{Kind: core.ActionPartition, Partition: &partition},
				NodesBefore: nodes, NodesAfter: nodes, ObservationDigest: "partition",
			},
			{
				Index: 1, Action: core.Action{Kind: core.ActionHeal},
				NodesBefore: nodes, NodesAfter: nodes, ObservationDigest: "heal",
			},
		},
	}
	run := RunArtifact{
		Name: "network", ModelConfig: raftmodel.DefaultConfig(),
		Initial: core.Observation{Nodes: nodes}, Trace: trace,
		ModelStates: []model.State{{Text: comparisonState(2)}},
	}
	frames, err := BuildCoverageFrames(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("frames=%d want initial+2 stutter frames", len(frames))
	}
	if frames[1].ModelEventIndex != -1 || frames[1].ModelStateIndex != 0 ||
		frames[1].Context.NetworkPartition == nil {
		t.Fatalf("partition frame=%+v", frames[1])
	}
	if frames[2].ModelEventIndex != -1 || frames[2].ModelStateIndex != 0 ||
		frames[2].Context.NetworkPartition != nil || !frames[2].Context.JustHealed {
		t.Fatalf("heal frame=%+v", frames[2])
	}
}

func TestBuildCoverageFramesAdvancesOncePerMappedEvent(t *testing.T) {
	before := frameNodes()
	before[0].Semantic["term"] = uint64(0)
	before[0].Semantic["role"] = "candidate"
	after := copyFrameNodes(before)
	after[0].Semantic["term"] = uint64(2)
	step := core.StepRecord{
		Index: 0, TimeBefore: 0, TimeAfter: 20,
		Action: core.Action{Kind: core.ActionAdvanceTime, TargetTime: 20},
		Effects: []core.Effect{
			{At: 10, Kind: core.EffectTimerFired, TimerFired: &core.TimerFired{
				Node: 1, Epoch: 1, Source: core.TimerFireNatural, TypeHint: "election",
				RoleHint: "candidate", Metadata: map[string]string{"term_before": "0", "term_after": "1"},
			}},
			{At: 20, Kind: core.EffectTimerFired, TimerFired: &core.TimerFired{
				Node: 1, Epoch: 1, Source: core.TimerFireNatural, TypeHint: "election",
				RoleHint: "candidate", Metadata: map[string]string{"term_before": "1", "term_after": "2"},
			}},
		},
		NodesBefore: before, NodesAfter: after, ObservationDigest: "two-timeouts",
	}
	transition, err := model.TransitionFromRecord(step)
	if err != nil {
		t.Fatal(err)
	}
	mapper := raftmodel.NewMapper()
	events, err := mapper.Map(transition)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("fixture emitted %d events", len(events))
	}
	states := []model.State{
		{Text: comparisonState(2)},
		{Text: strings.Replace(comparisonState(2), "currentTerm = <<2, 2, 2>>", "currentTerm = <<3, 2, 2>>", 1)},
		{Text: strings.Replace(comparisonState(2), "currentTerm = <<2, 2, 2>>", "currentTerm = <<4, 2, 2>>", 1)},
	}
	frames, err := BuildCoverageFrames(RunArtifact{
		Name: "multi", ModelConfig: raftmodel.DefaultConfig(),
		Initial: core.Observation{Nodes: before},
		Trace: core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "multi", Seed: 1,
			Steps: []core.StepRecord{step},
		},
		ModelEvents: events, ModelStates: states,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[1].ModelStateIndex != 1 || frames[2].ModelStateIndex != 2 ||
		frames[1].ModelEventIndex != 0 || frames[2].ModelEventIndex != 1 {
		t.Fatalf("multi-event alignment=%+v", frames)
	}
}

func TestBuildCoverageFramesRejectsMisalignment(t *testing.T) {
	run := RunArtifact{
		Name: "mismatch", ModelConfig: raftmodel.DefaultConfig(),
		Initial: core.Observation{Nodes: frameNodes()},
		Trace: core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "mismatch", Seed: 1,
		},
		ModelEvents: []model.Event{model.NewEvent("Timeout", map[string]any{"i": uint64(1)})},
		ModelStates: []model.State{{Text: comparisonState(2)}},
	}
	if _, err := BuildCoverageFrames(run); err == nil {
		t.Fatal("accepted states/events cardinality mismatch")
	}
}

func frameNodes() []core.NodeObservation {
	result := make([]core.NodeObservation, 3)
	for index := range result {
		role := "follower"
		if index == 1 {
			role = "leader"
		}
		result[index] = core.NodeObservation{
			ID: core.NodeID(index + 1), Epoch: 1, Status: core.NodeRunning,
			Semantic: map[string]any{
				"role": role, "term": uint64(1), "last_index": uint64(0),
				"last_term": uint64(0), "commit": uint64(0),
			},
		}
	}
	return result
}

func copyFrameNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for index, node := range nodes {
		result[index] = node.Copy()
	}
	return result
}
