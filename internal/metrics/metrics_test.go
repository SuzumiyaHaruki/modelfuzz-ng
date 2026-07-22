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

func TestCollectCountsAdapterModelEventsThatMapToStutter(t *testing.T) {
	effect := core.Effect{
		Kind: core.EffectModelEvent,
		ModelEvent: &core.ModelEvent{Name: "raft.proposal_dropped", Params: map[string]any{
			"source": "forwarded",
		}},
	}
	result := engine.Result{Trace: core.Trace{Steps: []core.StepRecord{{Effects: []core.Effect{effect}}}}}
	got := Collect(result)
	if got.ModelEventCounts["raft.proposal_dropped"] != 1 || got.FailureCounts["runtime_error"] != 0 {
		t.Fatalf("dropped proposal metrics = %+v", got)
	}
}

func TestCollectSnapshotLifecycleMetrics(t *testing.T) {
	names := []string{"raft.snapshot_created", "raft.snapshot_sent", "raft.snapshot_delivered",
		"raft.snapshot_applied", "raft.snapshot_rejected_or_stale", "raft.log_compacted"}
	effects := make([]core.Effect, 0, len(names))
	for _, name := range names {
		effects = append(effects, core.Effect{Kind: core.EffectModelEvent, ModelEvent: &core.ModelEvent{
			Name: name, Params: map[string]any{"snapshot_bytes": 123, "compacted_entries": uint64(4)},
		}})
	}
	got := Collect(engine.Result{Trace: core.Trace{Steps: []core.StepRecord{{Effects: effects}}}})
	if got.SnapshotsCreated != 1 || got.SnapshotsSent != 1 || got.SnapshotsDelivered != 1 ||
		got.SnapshotsApplied != 1 || got.SnapshotsRejectedOrStale != 1 || got.LogsCompacted != 1 ||
		got.CompactedEntries != 4 || got.SnapshotBytes != 123 {
		t.Fatalf("snapshot metrics = %+v", got)
	}
}

func TestDurations(t *testing.T) {
	got := Durations([]int64{10, 30, 20, 40})
	if got.MinMicros != 10 || got.MaxMicros != 40 || got.P50Micros != 20 || got.P95Micros != 40 || got.MeanMicros != 25 {
		t.Fatalf("duration summary = %+v", got)
	}
}
