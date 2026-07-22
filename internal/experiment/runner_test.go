package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/metrics"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestAggregateGroupsStableDecisionCodesBySource(t *testing.T) {
	report := aggregate(Report{Runs: []Run{
		{Completed: true, Source: "random_init", Succeeded: true, Status: engine.StatusCompleted,
			Metrics: &metrics.RunMetrics{DecisionCounts: map[string]int{"message_term_bound": 2}}},
		{Completed: true, Source: "mutation", Succeeded: true, Status: engine.StatusCompleted,
			Metrics: &metrics.RunMetrics{DecisionCounts: map[string]int{"message_term_bound": 1, "message_missing": 3}}},
	}})
	if report.DecisionCounts["message_term_bound"] != 3 || report.DecisionCounts["message_missing"] != 3 ||
		report.DecisionCountsBySource["random_init"]["message_term_bound"] != 2 ||
		report.DecisionCountsBySource["mutation"]["message_missing"] != 3 {
		t.Fatalf("decision statistics = %+v / %+v", report.DecisionCounts, report.DecisionCountsBySource)
	}
	statistics := report.Statistics()
	statistics.DecisionCountsBySource["mutation"]["message_missing"] = 99
	if report.DecisionCountsBySource["mutation"]["message_missing"] != 3 {
		t.Fatal("Statistics did not deep-copy decision counts by source")
	}
}

func TestRunnerDerivesSeedsPreservesOrderAndAggregatesCoverage(t *testing.T) {
	runner, err := New(Config{Runs: 4, BaseSeed: 40, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	seen := make(map[int64]int)
	report, err := runner.Run(context.Background(), func(_ context.Context, index int, seed int64) (engine.Result, error) {
		mutex.Lock()
		seen[seed]++
		mutex.Unlock()
		return engine.Result{
			Status:      engine.StatusCompleted,
			Actions:     core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}},
			ModelEvents: []model.Event{{Name: "event"}},
			ModelStates: []model.State{{Key: int64(index % 2)}},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 4 || report.Failed != 0 || report.TotalActions != 4 || report.UniqueModelStates != 2 {
		t.Fatalf("report = %+v", report)
	}
	for index, run := range report.Runs {
		if run.Index != index || run.Seed != int64(40+index) || seen[run.Seed] != 1 {
			t.Fatalf("run %d = %+v, seen=%v", index, run, seen)
		}
	}
}

func TestFeedbackReportDoesNotAllocateRunSlots(t *testing.T) {
	report := newFeedbackReport(Config{Runs: 20_000})
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 0 || len(data) > 10_000 {
		t.Fatalf("empty 20k-run feedback report contains run history: runs=%d bytes=%d", len(report.Runs), len(data))
	}
}

type feedbackMutator struct{}

func (feedbackMutator) Name() string { return "test_mutation" }

func (feedbackMutator) Mutate(_ context.Context, request mutation.Request) ([]plan.PlanSequence, error) {
	result := make([]plan.PlanSequence, request.Count)
	for index := range result {
		result[index] = request.Entry.Plan.Copy()
		result[index].Actions = append(result[index].Actions,
			plan.PlanAction{Kind: plan.ActionTimeout, Node: core.NodeID(2 + index%2)})
	}
	return result, nil
}

func TestFeedbackRunnerRetainsCoverageAndExecutesMutations(t *testing.T) {
	runner, err := New(Config{
		Runs: 5, BaseSeed: 10, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 2, MaxMutationsPerCorpus: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := make([]Run, 0, 5)
	report, snapshot, err := runner.RunFeedback(context.Background(), FeedbackOptions{
		Mutator: feedbackMutator{}, Hooks: Hooks{OnRunComplete: func(completion Completion) error {
			runs = append(runs, completion.Run)
			return nil
		}},
	},
		func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
			sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
			if candidate.Plan != nil {
				sequence = candidate.Plan.Copy()
			}
			key := int64(1)
			if candidate.ParentID != "" && index == 1 {
				key = 2
			}
			return FeedbackExecution{Plan: sequence, Result: engine.Result{
				Status: engine.StatusCompleted, ModelExecuted: true,
				ModelStates: []model.State{{Key: 1}, {Key: key}},
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("feedback-%d", index)), Steps: []core.StepRecord{}},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Feedback || report.CorpusEntries != 2 || report.UniqueModelStates != 2 ||
		report.ExecutedMutations == 0 || report.GeneratedMutations == 0 {
		t.Fatalf("feedback report = %+v", report)
	}
	if snapshot.EntryCount != 2 || !runs[0].Retained || runs[1].ParentID == "" || len(report.Runs) != 0 {
		t.Fatalf("snapshot/runs = %+v/%+v", snapshot, runs)
	}
}

func TestFeedbackRunnerReportsCorpusAdmissionAndNoveltyDensity(t *testing.T) {
	runner, err := New(Config{
		Runs: 3, BaseSeed: 15, Parallelism: 1, InitialPopulation: 3,
		MinNewModelStates: 2, SemanticCoverage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := make([]Run, 0, 3)
	project := func(states []model.State, _ []model.Event) (corpus.Projection, error) {
		switch states[0].Key {
		case 1, 2:
			return corpus.Projection{StateKeys: []int64{11}}, nil
		default:
			return corpus.Projection{StateKeys: []int64{12}, TransitionKeys: []int64{21}}, nil
		}
	}
	report, _, err := runner.RunFeedback(context.Background(), FeedbackOptions{
		Mutator: failingMutator{}, CoverageProjector: project,
		Hooks: Hooks{OnRunComplete: func(completion Completion) error {
			runs = append(runs, completion.Run)
			return nil
		}},
	}, func(_ context.Context, index int, _ int64, _ Candidate) (FeedbackExecution, error) {
		keys := [][]int64{{1}, {2, 3}, {4, 5}}[index]
		states := make([]model.State, len(keys))
		for stateIndex, key := range keys {
			states[stateIndex] = model.State{Key: key}
		}
		return FeedbackExecution{Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}, Result: engine.Result{
			Status:      engine.StatusCompleted,
			Actions:     core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout}, {Kind: core.ActionAdvanceTime}}},
			ModelStates: states,
			Trace:       core.Trace{Version: core.CurrentTraceVersion, ExecutionID: core.ExecutionID(fmt.Sprintf("admission-%d", index))},
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || runs[0].CorpusAdmission != string(corpus.AdmissionRejectedRawThreshold) ||
		runs[1].CorpusAdmission != string(corpus.AdmissionRejectedNoSemanticNovelty) ||
		runs[2].CorpusAdmission != string(corpus.AdmissionRetainedSemanticStateAndTransition) {
		t.Fatalf("run admissions = %+v", runs)
	}
	if runs[0].NewRawStates != 1 || runs[2].NewSemanticStates != 1 || runs[2].NewSemanticTransitions != 1 ||
		runs[2].SemanticNoveltyPer100Actions != 100 {
		t.Fatalf("per-run novelty = %+v", runs)
	}
	if report.RejectedRawThreshold != 1 || report.RejectedNoSemanticNovelty != 1 ||
		report.RetainedBySemanticState != 1 || report.RetainedBySemanticTransition != 1 || report.CorpusEntries != 1 {
		t.Fatalf("corpus admission report = %+v", report)
	}
	if report.UniqueSemanticStates != 2 || report.UniqueSemanticTransitions != 1 || report.SemanticNoveltyPer100Actions != 50 {
		t.Fatalf("semantic novelty report = %+v", report)
	}
	statistics := report.Statistics()
	if statistics.CorpusAdmissionCounts[string(corpus.AdmissionRejectedRawThreshold)] != 1 ||
		statistics.SemanticNoveltyPer100Actions != 50 {
		t.Fatalf("statistics = %+v", statistics)
	}
}

func TestFeedbackRunnerReportsSemanticProjectionFailure(t *testing.T) {
	runner, err := New(Config{Runs: 1, BaseSeed: 18, Parallelism: 1, InitialPopulation: 1})
	if err != nil {
		t.Fatal(err)
	}
	var completed Run
	report, snapshot, runErr := runner.RunFeedback(context.Background(), FeedbackOptions{
		Mutator: failingMutator{},
		CoverageProjector: func([]model.State, []model.Event) (corpus.Projection, error) {
			return corpus.Projection{}, errors.New("malformed controlled state")
		},
		Hooks: Hooks{OnRunComplete: func(completion Completion) error {
			completed = completion.Run
			return nil
		}},
	}, func(_ context.Context, _ int, _ int64, _ Candidate) (FeedbackExecution, error) {
		return FeedbackExecution{Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}, Result: engine.Result{
			Status: engine.StatusCompleted, ModelStates: []model.State{{Key: 1, Text: "malformed"}},
			Trace: core.Trace{Version: core.CurrentTraceVersion, ExecutionID: "projection-failure"},
		}}, nil
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if report.Succeeded != 0 || report.Failed != 1 || snapshot.EntryCount != 0 || completed.Succeeded ||
		completed.Status != engine.StatusMappingFailed || !strings.Contains(completed.Error, "semantic coverage projection") {
		t.Fatalf("report=%+v snapshot=%+v run=%+v", report, snapshot, completed)
	}
}

func TestFeedbackRunnerCountsUniquePlansTracesAndModelStatePaths(t *testing.T) {
	runner, err := New(Config{
		Runs: 4, BaseSeed: 20, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := make([]Run, 0, 4)
	report, _, err := runner.RunFeedback(context.Background(), FeedbackOptions{
		Mutator: failingMutator{}, Hooks: Hooks{OnRunComplete: func(completion Completion) error {
			runs = append(runs, completion.Run)
			return nil
		}},
	},
		func(_ context.Context, index int, _ int64, _ Candidate) (FeedbackExecution, error) {
			node := core.NodeID(1)
			states := []model.State{{Key: 1}, {Key: 2}}
			if index >= 2 {
				node = 2
				states = []model.State{{Key: 2}, {Key: 1}}
			}
			return FeedbackExecution{
				Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: node}}},
				Result: engine.Result{
					Status: engine.StatusCompleted, ModelStates: states,
					Trace: core.Trace{Version: core.CurrentTraceVersion,
						ExecutionID: core.ExecutionID(fmt.Sprintf("unique-%d", index)), Steps: []core.StepRecord{}},
				},
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if report.PlansObserved != 4 || report.UniquePlans != 2 || report.DuplicatePlanRatio != 0.5 {
		t.Fatalf("plan statistics = observed %d unique %d duplicate %.2f", report.PlansObserved, report.UniquePlans, report.DuplicatePlanRatio)
	}
	if report.TracesObserved != 4 || report.UniqueTraces != 1 || report.DuplicateTraceRatio != 0.75 {
		t.Fatalf("trace statistics = observed %d unique %d duplicate %.2f", report.TracesObserved, report.UniqueTraces, report.DuplicateTraceRatio)
	}
	if report.ModelStatePathsObserved != 4 || report.UniqueModelStatePaths != 2 || report.DuplicateModelStatePathRatio != 0.5 {
		t.Fatalf("path statistics = observed %d unique %d duplicate %.2f", report.ModelStatePathsObserved, report.UniqueModelStatePaths, report.DuplicateModelStatePathRatio)
	}
	newPlans, newTraces, newPaths := 0, 0, 0
	for _, run := range runs {
		if run.NewPlan {
			newPlans++
		}
		if run.NewTrace {
			newTraces++
		}
		if run.NewModelStatePath {
			newPaths++
		}
	}
	if newPlans != 2 || newTraces != 1 || newPaths != 2 {
		t.Fatalf("new flags = plans %d traces %d paths %d", newPlans, newTraces, newPaths)
	}
	source := report.NoveltyBySource["random_init"]
	if source.Executions != 4 || source.UniquePlansDiscovered != 2 ||
		source.UniqueTracesDiscovered != 1 || source.UniqueStatePathsDiscovered != 2 {
		t.Fatalf("source novelty = %+v", source)
	}
	statistics := report.Statistics()
	if statistics.UniquePlans != 2 || statistics.UniqueTraces != 1 || statistics.UniqueModelStatePaths != 2 {
		t.Fatalf("persisted statistics = %+v", statistics)
	}
	lastPoint := report.CoverageTimeline[len(report.CoverageTimeline)-1]
	if lastPoint.UniquePlans != 2 || lastPoint.UniqueTraces != 1 || lastPoint.UniqueModelStatePaths != 2 {
		t.Fatalf("novelty timeline = %+v", report.CoverageTimeline)
	}
}

func TestPeriodicRandomSeedsDoNotClearMutationQueue(t *testing.T) {
	runner, err := New(Config{
		Runs: 5, BaseSeed: 100, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 2, MaxMutationsPerCorpus: 2,
		RandomSeedInterval: 2, RandomSeedsPerInterval: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]CandidateKind, 0, 5)
	report, _, err := runner.RunFeedback(context.Background(), FeedbackOptions{Mutator: feedbackMutator{}},
		func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
			kinds = append(kinds, candidate.Kind)
			sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
			if candidate.Plan != nil {
				sequence = candidate.Plan.Copy()
			}
			return FeedbackExecution{Plan: sequence, Result: engine.Result{
				Status: engine.StatusCompleted, ModelStates: []model.State{{Key: 1}},
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("periodic-%d", index)), Steps: []core.StepRecord{}},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	want := []CandidateKind{
		CandidateInitial, CandidateMutation, CandidatePeriodicRandom,
		CandidateMutation, CandidatePeriodicRandom,
	}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("candidate order = %v, want %v", kinds, want)
	}
	if report.InitialExecutions != 1 || report.ExecutedMutations != 2 || report.PeriodicSeedExecutions != 2 {
		t.Fatalf("execution sources = initial %d mutations %d periodic %d",
			report.InitialExecutions, report.ExecutedMutations, report.PeriodicSeedExecutions)
	}
	if report.NoveltyBySource[string(CandidatePeriodicRandom)].Executions != 2 {
		t.Fatalf("periodic source statistics = %+v", report.NoveltyBySource)
	}
}

func TestPeriodicRandomSeedScheduleSurvivesCheckpointResume(t *testing.T) {
	config := Config{
		Runs: 4, BaseSeed: 300, Parallelism: 1, InitialPopulation: 1,
		RandomSeedInterval: 2, RandomSeedsPerInterval: 1,
	}
	runner, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	stopErr := fmt.Errorf("stop after first completion")
	var checkpoint Checkpoint
	options := FeedbackOptions{
		Mutator: failingMutator{},
		Hooks: Hooks{
			OnRunComplete: func(Completion) error { return stopErr },
			OnCheckpoint:  func(value Checkpoint) error { checkpoint = value; return nil },
		},
	}
	execute := func(_ context.Context, index int, _ int64, _ Candidate) (FeedbackExecution, error) {
		return FeedbackExecution{
			Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
			Result: engine.Result{Status: engine.StatusCompleted,
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("periodic-resume-%d", index)), Steps: []core.StepRecord{}}},
		}, nil
	}
	partial, _, err := runner.RunFeedback(context.Background(), options, execute)
	if err == nil || partial.CompletedRuns != 1 {
		t.Fatalf("partial report = %+v, err = %v", partial, err)
	}
	if checkpoint.Completed != 1 || checkpoint.NextRandomSeedAt != 2 || checkpoint.RandomSeedsDue != 0 {
		t.Fatalf("checkpoint periodic state = %+v", checkpoint)
	}

	resumedKinds := make([]CandidateKind, 0, 3)
	options.Resume = &checkpoint
	options.Hooks.OnRunComplete = nil
	options.Hooks.OnCheckpoint = func(value Checkpoint) error { checkpoint = value; return nil }
	final, _, err := runner.RunFeedback(context.Background(), options,
		func(ctx context.Context, index int, seed int64, candidate Candidate) (FeedbackExecution, error) {
			resumedKinds = append(resumedKinds, candidate.Kind)
			return execute(ctx, index, seed, candidate)
		})
	if err != nil {
		t.Fatal(err)
	}
	want := []CandidateKind{CandidateInitial, CandidatePeriodicRandom, CandidateInitial}
	if fmt.Sprint(resumedKinds) != fmt.Sprint(want) {
		t.Fatalf("resumed candidate order = %v, want %v", resumedKinds, want)
	}
	if final.InitialExecutions != 3 || final.PeriodicSeedExecutions != 1 ||
		checkpoint.Completed != 4 || checkpoint.NextRandomSeedAt != 6 || checkpoint.RandomSeedsDue != 0 {
		t.Fatalf("final/checkpoint = %+v / %+v", final, checkpoint)
	}
	if final.UniquePlans != 1 || final.UniqueTraces != 1 {
		t.Fatalf("resume lost novelty history: %+v", final)
	}
}

type failingMutator struct{}

func (failingMutator) Name() string { return "failing_mutation" }
func (failingMutator) Mutate(context.Context, mutation.Request) ([]plan.PlanSequence, error) {
	return nil, fmt.Errorf("temporary failure")
}

type cancelBlockingMutator struct{}

func (cancelBlockingMutator) Name() string { return "cancel_blocking" }
func (cancelBlockingMutator) Mutate(ctx context.Context, _ mutation.Request) ([]plan.PlanSequence, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestFeedbackRunnerResubmitsPendingMutationAfterResume(t *testing.T) {
	config := Config{Runs: 3, BaseSeed: 500, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1}
	runner, _ := New(config)
	ctx, cancel := context.WithCancel(context.Background())
	var checkpoint Checkpoint
	var corpusEntries []corpus.Entry
	options := FeedbackOptions{
		Mutator: cancelBlockingMutator{},
		Hooks: Hooks{
			OnRunComplete: func(Completion) error { cancel(); return nil },
			OnCheckpoint:  func(value Checkpoint) error { checkpoint = value; return nil },
			OnCorpusEntry: func(entry corpus.Entry) error {
				corpusEntries = append(corpusEntries, entry)
				return nil
			},
		},
	}
	execute := func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
		sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
		if candidate.Plan != nil {
			sequence = candidate.Plan.Copy()
		}
		return FeedbackExecution{Plan: sequence, Result: engine.Result{
			Status: engine.StatusCompleted, ModelStates: []model.State{{Key: 1}},
			Trace: core.Trace{Version: core.CurrentTraceVersion,
				ExecutionID: core.ExecutionID(fmt.Sprintf("pending-%d", index)), Steps: []core.StepRecord{}},
		}}, nil
	}
	partial, _, err := runner.RunFeedback(ctx, options, execute)
	if err == nil {
		t.Fatal("interrupted run unexpectedly succeeded")
	}
	if partial.CorpusEntries != 1 || partial.RetainedRuns != 1 || partial.UniqueModelStates != 1 {
		t.Fatalf("interrupted report lost final statistics: %+v", partial)
	}
	if checkpoint.Completed != 1 || len(checkpoint.PendingMutations) != 1 {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}

	options.Resume = &checkpoint
	options.ResumeCorpusEntries = corpusEntries
	options.Mutator = feedbackMutator{}
	options.Hooks.OnRunComplete = nil
	report, _, err := runner.RunFeedback(context.Background(), options, execute)
	if err != nil {
		t.Fatal(err)
	}
	if report.CompletedRuns != 3 || report.ExecutedMutations == 0 || report.GeneratedMutations == 0 {
		t.Fatalf("resumed report = %+v", report)
	}
}

func TestFeedbackRunnerDoesNotCheckpointEveryMutationCompletion(t *testing.T) {
	runner, err := New(Config{
		Runs: 2, BaseSeed: 700, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := 0
	mutationEvents := 0
	options := FeedbackOptions{
		Mutator: feedbackMutator{}, CheckpointEvery: 100,
		Hooks: Hooks{
			OnCheckpoint: func(Checkpoint) error { checkpoints++; return nil },
			OnEvent: func(event Event) error {
				if event.Kind == EventMutationCompleted {
					mutationEvents++
				}
				return nil
			},
		},
	}
	_, _, err = runner.RunFeedback(context.Background(), options,
		func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
			sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
			if candidate.Plan != nil {
				sequence = candidate.Plan.Copy()
			}
			return FeedbackExecution{Plan: sequence, Result: engine.Result{
				Status: engine.StatusCompleted, ModelStates: []model.State{{Key: 1}},
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("checkpoint-mutation-%d", index)), Steps: []core.StepRecord{}},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if mutationEvents != 1 || checkpoints != 2 {
		t.Fatalf("mutation events/checkpoints = %d/%d, want 1/2", mutationEvents, checkpoints)
	}
}

func TestFeedbackRunnerBoundsReadyQueueAndPrefersNewCandidates(t *testing.T) {
	runner, err := New(Config{
		Runs: 12, BaseSeed: 800, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 5, MaxMutationsPerCorpus: 5, MaxReadyCandidates: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	maxCheckpointReady := 0
	report, _, err := runner.RunFeedback(context.Background(), FeedbackOptions{
		Mutator: feedbackMutator{},
		Hooks: Hooks{OnCheckpoint: func(checkpoint Checkpoint) error {
			if len(checkpoint.Ready) > maxCheckpointReady {
				maxCheckpointReady = len(checkpoint.Ready)
			}
			if len(checkpoint.Ready) > 2 {
				t.Fatalf("checkpoint ready queue exceeded bound: %d", len(checkpoint.Ready))
			}
			return nil
		}},
	}, func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
		sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
		if candidate.Plan != nil {
			sequence = candidate.Plan.Copy()
		}
		return FeedbackExecution{Plan: sequence, Result: engine.Result{
			Status: engine.StatusCompleted, ModelStates: []model.State{{Key: int64(index + 1)}},
			Trace: core.Trace{Version: core.CurrentTraceVersion,
				ExecutionID: core.ExecutionID(fmt.Sprintf("bounded-ready-%d", index)), Steps: []core.StepRecord{}},
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.PeakReadyCandidates != 2 || maxCheckpointReady > 2 || report.DiscardedMutations == 0 ||
		report.AdmittedMutations == 0 || report.GeneratedMutations < report.AdmittedMutations {
		t.Fatalf("bounded queue report = %+v, checkpoint peak = %d", report, maxCheckpointReady)
	}
	statistics := report.Statistics()
	if statistics.PeakReadyCandidates != 2 || statistics.DiscardedMutations != report.DiscardedMutations {
		t.Fatalf("bounded queue statistics = %+v", statistics)
	}
}

func TestFeedbackRunnerRefillsEveryAvailableExecutionSlot(t *testing.T) {
	runner, err := New(Config{
		Runs: 8, BaseSeed: 900, Parallelism: 4, InitialPopulation: 8,
		MaxReadyCandidates: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	var initialStarted sync.WaitGroup
	initialStarted.Add(4)
	releaseInitialTail := make(chan struct{})
	postBoundaryStarted := make(chan struct{}, 1)
	probeResult := make(chan bool, 1)
	go func() {
		select {
		case <-postBoundaryStarted:
			probeResult <- true
		case <-time.After(time.Second):
			probeResult <- false
		}
		close(releaseInitialTail)
	}()

	report, _, runErr := runner.RunFeedback(context.Background(), FeedbackOptions{Mutator: failingMutator{}},
		func(_ context.Context, index int, _ int64, _ Candidate) (FeedbackExecution, error) {
			if index < 4 {
				initialStarted.Done()
				initialStarted.Wait()
				if index != 0 {
					<-releaseInitialTail
				}
			} else {
				select {
				case postBoundaryStarted <- struct{}{}:
				default:
				}
			}
			return FeedbackExecution{Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}, Result: engine.Result{
				Status: engine.StatusCompleted,
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("refill-slots-%d", index))},
			}}, nil
		})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !<-probeResult {
		t.Fatal("runner waited for all active executions before refilling an available slot")
	}
	if report.Succeeded != 8 || report.InitialExecutions != 8 {
		t.Fatalf("report = %+v", report)
	}
}

func TestFeedbackRunnerContinuesWithNewSeedAfterMutationFailure(t *testing.T) {
	runner, _ := New(Config{Runs: 3, BaseSeed: 1, Parallelism: 1, InitialPopulation: 1})
	report, _, err := runner.RunFeedback(context.Background(), FeedbackOptions{Mutator: failingMutator{}},
		func(_ context.Context, index int, _ int64, _ Candidate) (FeedbackExecution, error) {
			sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
			return FeedbackExecution{Plan: sequence, Result: engine.Result{
				Status: engine.StatusCompleted, ModelStates: []model.State{{Key: int64(index)}},
				Trace: core.Trace{Version: core.CurrentTraceVersion,
					ExecutionID: core.ExecutionID(fmt.Sprintf("failure-%d", index)), Steps: []core.StepRecord{}},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.MutationErrors) != 2 || report.InitialExecutions != 3 || report.CorpusEntries != 3 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunnerContinuesAfterIndividualFailure(t *testing.T) {
	runner, _ := New(Config{Runs: 3, BaseSeed: 1, Parallelism: 1})
	report, err := runner.Run(context.Background(), func(_ context.Context, index int, _ int64) (engine.Result, error) {
		if index == 1 {
			return engine.Result{Status: engine.StatusMappingFailed}, context.DeadlineExceeded
		}
		return engine.Result{Status: engine.StatusCompleted}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 2 || report.Failed != 1 || report.StatusCounts[string(engine.StatusMappingFailed)] != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestFeedbackRunnerCheckpointAndResume(t *testing.T) {
	config := Config{Runs: 4, BaseSeed: 90, Parallelism: 1, InitialPopulation: 1}
	runner, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	firstContext, cancel := context.WithCancel(context.Background())
	var checkpoint Checkpoint
	completedFirst := 0
	options := FeedbackOptions{
		Mutator: feedbackMutator{}, CheckpointEvery: 1,
		Hooks: Hooks{
			OnRunComplete: func(Completion) error {
				completedFirst++
				if completedFirst == 2 {
					cancel()
				}
				return nil
			},
			OnCheckpoint: func(value Checkpoint) error { checkpoint = value; return nil },
		},
	}
	execute := func(_ context.Context, index int, _ int64, candidate Candidate) (FeedbackExecution, error) {
		sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
		if candidate.Plan != nil {
			sequence = candidate.Plan.Copy()
		}
		return FeedbackExecution{Plan: sequence, Result: engine.Result{
			Status: engine.StatusCompleted,
			Trace: core.Trace{Version: core.CurrentTraceVersion,
				ExecutionID: core.ExecutionID(fmt.Sprintf("resume-%d", index)), Steps: []core.StepRecord{}},
		}}, nil
	}
	partial, _, err := runner.RunFeedback(firstContext, options, execute)
	if err == nil || partial.CompletedRuns != 2 || checkpoint.Completed != 2 {
		t.Fatalf("partial=%+v checkpoint=%+v err=%v", partial, checkpoint, err)
	}

	resumedIndices := make([]int, 0)
	options.Resume = &checkpoint
	options.Hooks.OnRunComplete = func(completion Completion) error {
		resumedIndices = append(resumedIndices, completion.Run.Index)
		return nil
	}
	final, _, err := runner.RunFeedback(context.Background(), options, func(ctx context.Context, index int, seed int64, candidate Candidate) (FeedbackExecution, error) {
		resumedIndices = append(resumedIndices, -100-index) // 区分回调和实际执行记录。
		return execute(ctx, index, seed, candidate)
	})
	if err != nil {
		t.Fatal(err)
	}
	if final.CompletedRuns != 4 || final.Succeeded != 4 || len(final.CoverageTimeline) != 4 {
		t.Fatalf("final = %+v", final)
	}
	seenExecution := map[int]bool{}
	for _, value := range resumedIndices {
		if value <= -100 {
			seenExecution[-100-value] = true
		}
	}
	if !seenExecution[2] || !seenExecution[3] || seenExecution[0] || seenExecution[1] {
		t.Fatalf("resumed executions = %v", resumedIndices)
	}
}
