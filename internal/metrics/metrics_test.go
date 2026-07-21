package metrics

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestCollectCountsExecutionAndQueue(t *testing.T) {
	result := engine.Result{
		Status: engine.StatusCompleted, Termination: engine.TerminationPlanComplete,
		Initial: core.Observation{Messages: []core.MessageObservation{{EnqueuedAt: 1}}},
		Final:   core.Observation{Time: 7, Messages: []core.MessageObservation{{EnqueuedAt: 2}, {EnqueuedAt: 5}}},
		Actions: core.ActionSequence{Actions: []core.Action{{Kind: core.ActionDeliver}, {Kind: core.ActionDuplicate}}},
		Resolutions: []plan.Resolution{{Status: plan.ResolutionResolved}, {
			Status: plan.ResolutionInapplicable, ReasonCode: plan.ReasonMessageNotAvailable,
		}},
		ModelEvents: []model.Event{{Name: "AppendEntries"}, model.ResetEvent()},
		Trace: core.Trace{Steps: []core.StepRecord{
			{Action: core.Action{Kind: core.ActionDeliver}, Effects: []core.Effect{{Kind: core.EffectSendMessage,
				Message: &core.Message{TypeHint: "MsgHeartbeat"}}}},
			{Action: core.Action{Kind: core.ActionDuplicate}},
		}},
	}
	got := Collect(result)
	if got.ActionCounts["deliver"] != 1 || got.EffectCounts["send_message"] != 1 ||
		got.ModelEventCounts["reset"] != 1 || got.MessageTypeCounts["MsgHeartbeat"] != 1 ||
		got.EstimatedPeakQueuedMessages != 2 || got.MaxFinalMessageAge != 5 ||
		got.DecisionCounts[string(plan.ReasonMessageNotAvailable)] != 1 {
		t.Fatalf("metrics = %+v", got)
	}
}

func TestDurations(t *testing.T) {
	got := Durations([]int64{10, 30, 20, 40})
	if got.MinMicros != 10 || got.MaxMicros != 40 || got.P50Micros != 20 || got.P95Micros != 40 || got.MeanMicros != 25 {
		t.Fatalf("duration summary = %+v", got)
	}
}
