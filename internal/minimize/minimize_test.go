package minimize

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestReduceProducesOneMinimalPlanWithSamePanicSignature(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		{Kind: plan.ActionRequest, Node: 1, Request: "keep"},
		{Kind: plan.ActionAdvanceTicks, Ticks: 2},
		{Kind: plan.ActionRequest, Node: 2, Request: "noise"},
	}}
	execute := func(_ context.Context, candidate plan.PlanSequence) (engine.Result, error) {
		for _, action := range candidate.Actions {
			if action.Kind == plan.ActionRequest && action.Request == "keep" {
				result := engine.Result{Status: engine.StatusRuntimeFailed, Failure: &core.FailureRecord{
					Kind: core.FailureSUTPanic, Operation: "deliver", PanicValue: "need non-empty snapshot",
				}}
				return result, errors.New("expected panic")
			}
		}
		return engine.Result{Status: engine.StatusCompleted}, nil
	}
	config := DefaultConfig()
	result, err := Reduce(context.Background(), sequence, config, execute)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Actions) != 1 || result.Plan.Actions[0].Request != "keep" {
		t.Fatalf("minimized plan = %+v", result.Plan)
	}
	if !result.Report.OneMinimal || result.Report.OriginalActions != 4 || result.Report.MinimizedActions != 1 {
		t.Fatalf("report = %+v", result.Report)
	}
	if result.Report.Signature.PanicValue != "need non-empty snapshot" {
		t.Fatalf("signature = %+v", result.Report.Signature)
	}
	if result.Plan.Metadata != nil {
		t.Fatalf("minimizer changed plan metadata = %+v", result.Plan.Metadata)
	}
}

func TestReduceRejectsCompletedAndUnstableExecutions(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}}
	_, err := Reduce(context.Background(), sequence, DefaultConfig(), func(context.Context, plan.PlanSequence) (engine.Result, error) {
		return engine.Result{Status: engine.StatusCompleted}, nil
	})
	if !errors.Is(err, ErrNotFailure) {
		t.Fatalf("completed error = %v", err)
	}
	calls := 0
	_, err = Reduce(context.Background(), sequence, DefaultConfig(), func(context.Context, plan.PlanSequence) (engine.Result, error) {
		calls++
		panicValue := "first"
		if calls > 1 {
			panicValue = "second"
		}
		return engine.Result{Status: engine.StatusRuntimeFailed, Failure: &core.FailureRecord{
			Kind: core.FailureSUTPanic, Operation: "deliver", PanicValue: panicValue,
		}}, errors.New("panic")
	})
	if !errors.Is(err, ErrUnstableFailure) {
		t.Fatalf("unstable error = %v", err)
	}
}

func TestReduceAttemptLimitNeverClaimsOneMinimal(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		{Kind: plan.ActionAdvanceTicks, Ticks: 2},
	}}
	result, err := Reduce(context.Background(), sequence, Config{MaxAttempts: 2, VerifyRuns: 1},
		func(context.Context, plan.PlanSequence) (engine.Result, error) {
			return panicResult("stable"), errors.New("panic")
		})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Attempts != 2 || !result.Report.AttemptLimitReached || result.Report.OneMinimal {
		t.Fatalf("attempt-limited report = %+v", result.Report)
	}
}

func TestReduceStopsImmediatelyOnContextCancellation(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := Reduce(ctx, sequence, DefaultConfig(), func(context.Context, plan.PlanSequence) (engine.Result, error) {
		calls++
		cancel()
		return panicResult("stable"), context.Canceled
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation error/calls = %v/%d", err, calls)
	}
}

func TestReduceTreatsCallbackWithoutResultAsReducerError(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}}
	want := errors.New("executor did not produce an engine result")
	_, err := Reduce(context.Background(), sequence, DefaultConfig(), func(context.Context, plan.PlanSequence) (engine.Result, error) {
		return engine.Result{}, want
	})
	if !errors.Is(err, want) || errors.Is(err, ErrNotFailure) {
		t.Fatalf("callback error = %v", err)
	}
}

func TestSignatureUsesOracleCodeSetAndExactPanicValue(t *testing.T) {
	first := panicResult("need non-empty snapshot")
	first.OracleFindings = []oracle.Finding{
		{Oracle: "raft", Code: "committed_conflict"},
		{Oracle: "raft", Code: "same_term_leader"},
		{Oracle: "raft", Code: "committed_conflict"},
	}
	second := panicResult("need non-empty snapshot")
	second.OracleFindings = []oracle.Finding{
		{Oracle: "raft", Code: "same_term_leader"},
		{Oracle: "raft", Code: "committed_conflict"},
	}
	firstSignature, ok := SignatureOf(first)
	if !ok {
		t.Fatal("first result was not recognized as a failure")
	}
	secondSignature, ok := SignatureOf(second)
	if !ok || !firstSignature.Equal(secondSignature) ||
		!reflect.DeepEqual(firstSignature.OracleCodes, []string{"raft:committed_conflict", "raft:same_term_leader"}) {
		t.Fatalf("oracle signatures = %+v / %+v", firstSignature, secondSignature)
	}
	second.Failure.PanicValue = "different panic"
	secondSignature, _ = SignatureOf(second)
	if firstSignature.Equal(secondSignature) {
		t.Fatal("different panic values produced equal signatures")
	}
}

func TestSignatureRejectsCanceledExecution(t *testing.T) {
	if _, ok := SignatureOf(engine.Result{Status: engine.StatusCanceled}); ok {
		t.Fatal("canceled execution was treated as reducible")
	}
}

func TestSignatureDistinguishesTLCCodeAndActionButIgnoresEventIndex(t *testing.T) {
	result := engine.Result{Status: engine.StatusModelFailed,
		Error: "model execution failed: TLC disabled_action at event 3 (BecomeLeader): action is disabled"}
	first, ok := SignatureOf(result)
	if !ok || first.ModelErrorCode != "disabled_action" || first.ModelAction != "BecomeLeader" {
		t.Fatalf("first TLC signature = %+v", first)
	}
	result.Error = "model execution failed: TLC disabled_action at event 91 (BecomeLeader): action is disabled"
	same, _ := SignatureOf(result)
	if !first.Equal(same) {
		t.Fatalf("event index changed signature: %+v / %+v", first, same)
	}
	result.Error = "model execution failed: TLC invariant_violation at event 91 (BecomeLeader): invariant failed"
	differentCode, _ := SignatureOf(result)
	if first.Equal(differentCode) {
		t.Fatal("different TLC codes produced equal signatures")
	}
	result.Error = "model execution failed: TLC disabled_action at event 91 (ClientRequest): action is disabled"
	differentAction, _ := SignatureOf(result)
	if first.Equal(differentAction) {
		t.Fatal("different TLC action names produced equal signatures")
	}
}

func TestSignatureDistinguishesRuntimeRootCauseWithSameOperation(t *testing.T) {
	first := engine.Result{Status: engine.StatusRuntimeFailed, Failure: &core.FailureRecord{
		Kind: core.FailureRuntimeError, Operation: "deliver", Error: "adapter contract violation: message 17 has invalid metadata",
	}}
	second := engine.Result{Status: engine.StatusRuntimeFailed, Failure: &core.FailureRecord{
		Kind: core.FailureRuntimeError, Operation: "deliver", Error: "message is unavailable: message 99 is stale",
	}}
	firstSignature, _ := SignatureOf(first)
	secondSignature, _ := SignatureOf(second)
	if firstSignature.Equal(secondSignature) {
		t.Fatalf("different runtime failures produced equal signatures: %+v / %+v", firstSignature, secondSignature)
	}
	first.Failure.Error = "adapter contract violation: message 314 has invalid metadata"
	sameClass, _ := SignatureOf(first)
	if !firstSignature.Equal(sameClass) {
		t.Fatalf("dynamic message ID changed runtime signature: %+v / %+v", firstSignature, sameClass)
	}
}

func TestMinimizedExecutionMatchesReturnedPlanActionsAndMetadata(t *testing.T) {
	sequence := plan.PlanSequence{
		Actions: []plan.PlanAction{
			{Kind: plan.ActionRequest, Node: 1, Request: "keep"},
			{Kind: plan.ActionRequest, Node: 2, Request: "noise"},
		},
		Metadata: map[string]string{"source": "regression"},
	}
	result, err := Reduce(context.Background(), sequence, DefaultConfig(),
		func(_ context.Context, candidate plan.PlanSequence) (engine.Result, error) {
			for _, action := range candidate.Actions {
				if action.Request == "keep" {
					return engine.Result{
						Status:      engine.StatusRuntimeFailed,
						Failure:     &core.FailureRecord{Kind: core.FailureSUTPanic, Operation: "deliver", PanicValue: "stable"},
						Resolutions: []plan.Resolution{{Plan: action}},
					}, errors.New("panic")
				}
			}
			return engine.Result{Status: engine.StatusCompleted}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Actions) != 1 || len(result.MinimizedExecution.Resolutions) != 1 ||
		!reflect.DeepEqual(result.MinimizedExecution.Resolutions[0].Plan, result.Plan.Actions[0]) ||
		!reflect.DeepEqual(result.Plan.Metadata, sequence.Metadata) {
		t.Fatalf("plan/execution mismatch = %+v / %+v", result.Plan, result.MinimizedExecution.Resolutions)
	}
}

func TestReduceResumesFromCheckpointWithCandidateCache(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		{Kind: plan.ActionRequest, Node: 1, Request: "keep"},
		{Kind: plan.ActionAdvanceTicks, Ticks: 2},
		{Kind: plan.ActionRequest, Node: 2, Request: "noise"},
	}}
	execute := func(_ context.Context, candidate plan.PlanSequence) (engine.Result, error) {
		for _, action := range candidate.Actions {
			if action.Request == "keep" {
				return panicResult("resume"), errors.New("panic")
			}
		}
		return engine.Result{Status: engine.StatusCompleted}, nil
	}
	stop := errors.New("simulated interruption")
	var checkpoint Checkpoint
	_, err := Reduce(context.Background(), sequence, Config{
		MaxAttempts: 20, VerifyRuns: 1, FinalVerifyRuns: 1,
		InputPlanSHA256: "plan", ConfigSHA256: "config",
		OnCheckpoint: func(current Checkpoint) error {
			checkpoint = current
			if current.Attempts >= 2 {
				return stop
			}
			return nil
		},
	}, execute)
	if !errors.Is(err, stop) || checkpoint.Attempts != 2 || len(checkpoint.Cache) == 0 {
		t.Fatalf("interrupted checkpoint = %+v, err=%v", checkpoint, err)
	}
	resumed, err := Reduce(context.Background(), sequence, Config{
		MaxAttempts: 30, VerifyRuns: 1, FinalVerifyRuns: 1,
		InputPlanSHA256: "plan", ConfigSHA256: "config", Resume: &checkpoint,
	}, execute)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Plan.Actions) != 1 || resumed.Plan.Actions[0].Request != "keep" ||
		resumed.Report.Attempts < checkpoint.Attempts || resumed.Report.CacheHits == 0 {
		t.Fatalf("resumed result = %+v", resumed)
	}
}

func panicResult(value string) engine.Result {
	return engine.Result{Status: engine.StatusRuntimeFailed, Failure: &core.FailureRecord{
		Kind: core.FailureSUTPanic, Operation: "deliver", PanicValue: value,
	}}
}
