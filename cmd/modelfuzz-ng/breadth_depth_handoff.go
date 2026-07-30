package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

func buildHandoffCandidates(
	ctx context.Context,
	globalDirectory string,
	config cliConfig,
	goalID goalsearch.GoalID,
	verifyReplay bool,
	stderr io.Writer,
) (materializedHandoff, error) {
	result := materializedHandoff{
		Candidates: make([]breadthdepth.HandoffSeed, 0),
		Frontier:   make(map[string]goalsearch.FrontierSeed),
		Replays:    make([]handoffReplayRecord, 0),
	}
	report := readExperimentReport(globalDirectory)
	entries, err := persistence.ReadJSONLines[corpus.Entry](
		filepath.Join(globalDirectory, "corpus.jsonl"), report.CorpusEntries)
	if err != nil {
		return result, err
	}
	observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
		filepath.Join(globalDirectory, "coverage-observations.jsonl"), report.CompletedRuns)
	if err != nil {
		return result, err
	}
	decisions, err := persistence.ReadJSONLines[coverageguidance.Decision](
		filepath.Join(globalDirectory, "corpus-decisions.jsonl"), report.CompletedRuns)
	if err != nil {
		return result, err
	}
	runs, err := persistence.ReadJSONLines[experiment.Run](
		filepath.Join(globalDirectory, "runs.jsonl"), report.CompletedRuns)
	if err != nil {
		return result, err
	}
	observationByRun := make(map[int]coverageguidance.CoverageObservation, len(runs))
	decisionByRun := make(map[int]coverageguidance.Decision, len(runs))
	runByIndex := make(map[int]experiment.Run, len(runs))
	for index, run := range runs {
		if index >= len(observations) || index >= len(decisions) {
			return result, fmt.Errorf("coverage journals are shorter than run journal")
		}
		if observations[index].CandidateID != run.CandidateID ||
			decisions[index].CandidateID != run.CandidateID {
			return result, fmt.Errorf(
				"run %d candidate %s is misaligned with coverage journals", run.Index, run.CandidateID)
		}
		observationByRun[run.Index] = observations[index]
		decisionByRun[run.Index] = decisions[index]
		runByIndex[run.Index] = run
	}
	definition, err := goalsearch.Definition(goalID, len(config.Raft.NodeIDs))
	if err != nil {
		return result, err
	}
	globalEntries := make([]breadthdepth.GlobalEntry, 0, len(entries))
	for admissionRank, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		run, found := runByIndex[entry.RunIndex]
		if !found {
			return result, fmt.Errorf("corpus %s references missing run %d", entry.ID, entry.RunIndex)
		}
		runDirectory := filepath.Join(
			globalDirectory, fmt.Sprintf("run-%04d-seed-%d", run.Index, run.Seed))
		var execution engine.Result
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &execution); err != nil {
			return result, fmt.Errorf("read %s result: %w", entry.ID, err)
		}
		coverage := observationByRun[entry.RunIndex]
		decision := decisionByRun[entry.RunIndex]
		globalEntry := breadthdepth.GlobalEntry{
			SchemaVersion: breadthdepth.SchemaVersion, CorpusID: entry.ID,
			RunIndex: entry.RunIndex, RuntimeSeed: entry.Seed,
			ExecutionID: execution.Trace.ExecutionID, Plan: entry.Plan.Copy(),
			Trace: execution.Trace.Copy(), Observation: execution.Final.Copy(),
			Coverage: coverage, Admission: decision, AdmissionRank: admissionRank + 1,
			ReplayStatus: "not_evaluated",
		}
		globalEntry.StableKey = breadthDepthStableKey(struct {
			CorpusID string
			RunIndex int
			Coverage string
			Decision string
		}{entry.ID, entry.RunIndex, coverage.StableKey, decision.StableDecisionKey})
		globalEntries = append(globalEntries, globalEntry)

		evaluation, evalErr := goalsearch.Recompute(goalsearch.ArtifactInput{
			Definition: definition, InstanceID: "handoff-" + entry.ID,
			ModelConfig: config.Model, Initial: execution.Initial,
			Trace: execution.Trace, Resolutions: execution.Resolutions,
			ModelEvents: execution.ModelEvents,
		})
		candidate := breadthdepth.HandoffSeed{
			SchemaVersion: breadthdepth.SchemaVersion, GlobalCorpusID: entry.ID,
			GlobalAdmissionRank: admissionRank + 1, Plan: entry.Plan.Copy(),
			Trace: execution.Trace.Copy(), Observation: execution.Final.Copy(),
			PlanPrefixLength:    len(entry.Plan.Actions),
			SemanticTraceDigest: breadthdepth.RelativeSemanticTraceKey(execution.Trace),
			FacetCombinationKey: facetCombinationKey(coverage),
			NewFacet:            hasNewFacet(decision),
			FacetNoveltyCount:   facetNoveltyCount(decision),
			QueueShapeKey:       breadthdepth.RelativeQueueShapeKey(execution.Final),
			ReplayStatus:        "not_attempted",
		}
		if evalErr != nil {
			candidate.Progress = breadthdepth.GoalProgress{
				Distance: 99, ProgressStableKey: "evaluation-error:" + evalErr.Error(),
			}
			candidate.StableKey = breadthDepthStableKey(candidate)
			result.Candidates = append(result.Candidates, candidate)
			result.Replays = append(result.Replays, handoffReplayRecord{
				SchemaVersion: breadthdepth.SchemaVersion, GlobalCorpusID: entry.ID,
				Error: evalErr.Error(),
			})
			continue
		}
		candidate.Progress = handoffGoalProgress(evaluation)
		frontierSeed, seedErr := goalsearch.SeedFromResult(
			"handoff-"+entry.ID, "", entry.RunIndex, entry.Seed,
			execution.Trace.ExecutionID, entry.Plan, execution, evaluation,
		)
		if seedErr != nil {
			candidate.StableKey = breadthDepthStableKey(candidate)
			result.Candidates = append(result.Candidates, candidate)
			result.Replays = append(result.Replays, handoffReplayRecord{
				SchemaVersion: breadthdepth.SchemaVersion, GlobalCorpusID: entry.ID,
				Error: seedErr.Error(),
			})
			continue
		}
		candidate.Plan = frontierSeed.PrefixPlan.Copy()
		candidate.Trace = frontierSeed.PrefixTrace.Copy()
		candidate.Observation = frontierSeed.PrefixObservation.Copy()
		candidate.PlanPrefixLength = len(frontierSeed.PrefixPlan.Actions)
		candidate.SemanticTraceDigest =
			breadthdepth.RelativeSemanticTraceKey(frontierSeed.PrefixTrace)
		candidate.QueueShapeKey =
			breadthdepth.RelativeQueueShapeKey(frontierSeed.PrefixObservation)
		candidate.Progress.BindingRoles = bindingRoles(
			frontierSeed.PrefixObservation, evaluation.Instance.Bindings)

		replay := handoffReplayRecord{
			SchemaVersion: breadthdepth.SchemaVersion, GlobalCorpusID: entry.ID,
		}
		var replayEvaluation goalsearch.EvaluationResult
		if verifyReplay {
			replay, candidate, err = verifyHandoffSeed(
				ctx, config, definition, execution,
				evaluation, frontierSeed, candidate, &replayEvaluation, stderr,
			)
			if err != nil {
				// A divergent entry is recorded and excluded, not fatal to the
				// campaign; the selector can try the next deterministic seed.
				replay.Error = err.Error()
			}
		} else {
			candidate.Replayable = true
			candidate.ReplayStatus = "verification_disabled"
			frontierSeed.ReplayVerified = false
			frontierSeed.ReplayStatus = "verification_disabled"
		}
		candidate.StableKey = breadthDepthStableKey(candidate)
		if candidate.Replayable {
			if verifyReplay {
				frontierSeed.Instance = replayEvaluation.Instance
				frontierSeed.Progress = replayEvaluation.Instance.Progress
				frontierSeed.Bindings = replayEvaluation.Instance.Bindings
				frontierSeed.PrefixObservation = replayEvaluation.FinalObservation
				frontierSeed.SourceResultKey = replayEvaluation.StableKey
			}
			frontierSeed.ReplayVerified = true
			frontierSeed.ReplayStatus = candidate.ReplayStatus
			frontierSeed.ReplayMatchedSteps = uint64(len(frontierSeed.PrefixTrace.Steps))
			if err := goalsearch.RefreshFrontierSeed(&frontierSeed); err != nil {
				return result, err
			}
			result.Frontier[entry.ID] = frontierSeed
		}
		result.Candidates = append(result.Candidates, candidate)
		result.Replays = append(result.Replays, replay)
	}
	sort.Slice(result.Candidates, func(i, j int) bool {
		return result.Candidates[i].GlobalAdmissionRank <
			result.Candidates[j].GlobalAdmissionRank
	})
	globalEntriesPath := filepath.Join(globalDirectory, "global-corpus-entries.jsonl")
	var existing []breadthdepth.GlobalEntry
	if readErr := readJSONLines(globalEntriesPath, &existing); readErr == nil {
		if !equalJSON(existing, globalEntries) {
			return result, fmt.Errorf(
				"existing global corpus entries differ from deterministic recomputation")
		}
	} else if err := writeJSONLines(globalEntriesPath, globalEntries); err != nil {
		return result, err
	}
	return result, nil
}

func hasNewFacet(decision coverageguidance.Decision) bool {
	return facetNoveltyCount(decision) > 0
}

func facetNoveltyCount(decision coverageguidance.Decision) int {
	count := 0
	for _, values := range decision.NewCoverageUnits.Facets {
		count += len(values)
	}
	return count
}

func verifyHandoffSeed(
	ctx context.Context,
	config cliConfig,
	definition goalsearch.BehaviorGoalDefinition,
	original engine.Result,
	originalEvaluation goalsearch.EvaluationResult,
	frontierSeed goalsearch.FrontierSeed,
	candidate breadthdepth.HandoffSeed,
	verifiedEvaluation *goalsearch.EvaluationResult,
	stderr io.Writer,
) (handoffReplayRecord, breadthdepth.HandoffSeed, error) {
	record := handoffReplayRecord{
		SchemaVersion:  breadthdepth.SchemaVersion,
		GlobalCorpusID: candidate.GlobalCorpusID, Attempted: true,
	}
	replayConfig := config
	replayConfig.Seed = frontierSeed.RuntimeSeed
	replayConfig.ExecutionID = frontierSeed.ExecutionID
	runner, err := buildEngine(replayConfig, stderr)
	if err != nil {
		return record, candidate, err
	}
	replayed, runErr := runner.Run(ctx, frontierSeed.PrefixPlan)
	record.Actions = len(replayed.Trace.Steps)
	record.TraceEqual = equalJSON(frontierSeed.PrefixTrace, replayed.Trace)
	record.MessageIdentityEqual = record.TraceEqual
	expectedEvents := prefixModelEvents(original.ModelEvents, len(replayed.ModelEvents))
	record.ModelEventsEqual = len(expectedEvents) == len(replayed.ModelEvents) &&
		equalJSON(expectedEvents, replayed.ModelEvents)
	record.ObservationEqual = equalJSON(frontierSeed.PrefixObservation, replayed.Final)
	record.ObservationDigestEqual = replayObservationDigestEqual(
		frontierSeed.PrefixTrace, replayed.Trace)
	replayEvaluation, evalErr := goalsearch.Recompute(goalsearch.ArtifactInput{
		Definition: definition, InstanceID: originalEvaluation.Instance.InstanceID,
		ModelConfig: config.Model, Initial: replayed.Initial, Trace: replayed.Trace,
		Resolutions: replayed.Resolutions, ModelEvents: replayed.ModelEvents,
	})
	expectedEvaluation, expectedEvalErr := goalsearch.Recompute(goalsearch.ArtifactInput{
		Definition: definition, InstanceID: originalEvaluation.Instance.InstanceID,
		ModelConfig: config.Model, Initial: original.Initial,
		Trace:       frontierSeed.PrefixTrace,
		Resolutions: replayed.Resolutions,
		ModelEvents: expectedEvents,
	})
	if evalErr == nil && expectedEvalErr == nil {
		record.GoalProgressEqual = equalJSON(
			expectedEvaluation.Instance.Progress, replayEvaluation.Instance.Progress) &&
			equalJSON(expectedEvaluation.Instance.Bindings, replayEvaluation.Instance.Bindings)
		record.StableKeyEqual = expectedEvaluation.StableKey == replayEvaluation.StableKey
		candidate.Progress = handoffGoalProgress(expectedEvaluation)
		if verifiedEvaluation != nil {
			*verifiedEvaluation = replayEvaluation
		}
	}
	replayCoverage, coverageErr := coverageanalysis.BuildCoverageObservation(
		coverageanalysis.ObservationInput{
			RunID:       "handoff-replay-" + candidate.GlobalCorpusID,
			CandidateID: "handoff-replay-" + candidate.GlobalCorpusID,
			Source:      "handoff-replay", Plan: frontierSeed.PrefixPlan,
			Result: replayed, ModelConfig: config.Model,
		},
	)
	expectedCoverage, expectedCoverageErr := expectedPrefixCoverage(
		candidate.GlobalCorpusID, config, original, frontierSeed, replayed,
	)
	if coverageErr == nil && expectedCoverageErr == nil {
		record.FacetEqual = equalJSON(
			expectedCoverage.FacetKeys, replayCoverage.FacetKeys) &&
			equalJSON(expectedCoverage.InteractionKeys, replayCoverage.InteractionKeys)
		candidate.FacetCombinationKey = facetCombinationKey(replayCoverage)
	}
	record.Succeeded = runErr == nil && evalErr == nil && coverageErr == nil &&
		expectedCoverageErr == nil && record.TraceEqual && record.ModelEventsEqual &&
		record.ObservationDigestEqual && record.GoalProgressEqual && record.FacetEqual &&
		record.StableKeyEqual && record.MessageIdentityEqual
	candidate.Replayable = record.Succeeded
	if record.Succeeded {
		candidate.ReplayStatus = string(tracepkg.StatusCompleted)
		frontierSeed.ReplayVerified = true
		frontierSeed.ReplayStatus = string(tracepkg.StatusCompleted)
		frontierSeed.ReplayMatchedSteps = uint64(len(replayed.Trace.Steps))
		return record, candidate, nil
	}
	candidate.ReplayStatus = string(tracepkg.StatusDiverged)
	if runErr != nil {
		return record, candidate, runErr
	}
	if evalErr != nil {
		return record, candidate, evalErr
	}
	if expectedEvalErr != nil {
		return record, candidate, expectedEvalErr
	}
	if coverageErr != nil {
		return record, candidate, coverageErr
	}
	if expectedCoverageErr != nil {
		return record, candidate, expectedCoverageErr
	}
	return record, candidate, fmt.Errorf(
		"handoff replay mismatch trace=%v events=%v observation_digest=%v goal=%v facet=%v stable=%v message=%v",
		record.TraceEqual, record.ModelEventsEqual, record.ObservationDigestEqual,
		record.GoalProgressEqual, record.FacetEqual, record.StableKeyEqual,
		record.MessageIdentityEqual,
	)
}

func replayObservationDigestEqual(expected, actual core.Trace) bool {
	if len(expected.Steps) == 0 || len(actual.Steps) == 0 {
		return len(expected.Steps) == len(actual.Steps)
	}
	left := expected.Steps[len(expected.Steps)-1]
	right := actual.Steps[len(actual.Steps)-1]
	return left.ObservationDigest != "" &&
		left.ObservationDigest == right.ObservationDigest
}

func expectedPrefixCoverage(
	corpusID string,
	config cliConfig,
	original engine.Result,
	frontierSeed goalsearch.FrontierSeed,
	replayed engine.Result,
) (coverageguidance.CoverageObservation, error) {
	if len(replayed.ModelEvents) > len(original.ModelEvents) ||
		len(replayed.ModelStates) > len(original.ModelStates) {
		return coverageguidance.CoverageObservation{}, fmt.Errorf(
			"replay model prefix exceeds original artifacts")
	}
	expected := engine.Result{
		Status: engine.StatusCompleted, ModelExecuted: true,
		Initial: original.Initial.Copy(), Final: frontierSeed.PrefixObservation.Copy(),
		Trace:       frontierSeed.PrefixTrace.Copy(),
		ModelEvents: append([]model.Event(nil), original.ModelEvents[:len(replayed.ModelEvents)]...),
		ModelStates: append([]model.State(nil), original.ModelStates[:len(replayed.ModelStates)]...),
	}
	return coverageanalysis.BuildCoverageObservation(coverageanalysis.ObservationInput{
		RunID:       "handoff-expected-" + corpusID,
		CandidateID: "handoff-expected-" + corpusID, Source: "handoff-expected",
		Plan: frontierSeed.PrefixPlan, Result: expected, ModelConfig: config.Model,
	})
}

func handoffGoalProgress(evaluation goalsearch.EvaluationResult) breadthdepth.GoalProgress {
	progress := evaluation.Instance.Progress
	return breadthdepth.GoalProgress{
		EntryCondition:    progress.CompletedWaypointCount > 0,
		Completed:         progress.CompletedWaypointCount,
		CurrentWaypoint:   progress.CurrentWaypointID,
		Distance:          progress.DistanceToCurrent,
		TargetReached:     progress.TargetReached,
		ProgressStableKey: progress.StableKey,
		BindingRoles:      bindingRoles(evaluation.PrefixObservation, evaluation.Instance.Bindings),
	}
}

func bindingRoles(
	observation core.Observation,
	bindings map[goalsearch.Symbol]goalsearch.Binding,
) map[string]string {
	result := make(map[string]string, len(bindings))
	for symbol, binding := range bindings {
		result[string(symbol)] = breadthdepth.BindingRoleKey(observation, binding.Node)
	}
	return result
}

func prefixModelEvents(events []model.Event, count int) []model.Event {
	if count < 0 || count > len(events) {
		return nil
	}
	result := make([]model.Event, count)
	for index := range result {
		result[index] = events[index].Copy()
	}
	return result
}
