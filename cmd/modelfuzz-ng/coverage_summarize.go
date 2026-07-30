package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type coverageConsistencyReport struct {
	Schema                    string `json:"schema"`
	Candidates                int    `json:"candidates"`
	Compared                  int    `json:"compared"`
	Mismatches                int    `json:"mismatches"`
	RawMismatch               int    `json:"raw_mismatch"`
	V2Mismatch                int    `json:"v2_mismatch"`
	FacetMismatch             int    `json:"facet_mismatch"`
	InteractionMismatch       int    `json:"interaction_mismatch"`
	StableKeyMismatch         int    `json:"stable_key_mismatch"`
	DecisionRecomputeMismatch int    `json:"decision_recompute_mismatch"`
	UnavailableArtifacts      int    `json:"unavailable_artifacts"`
}

func coverageSummarizeCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng coverage-summarize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "包含 coverage observations 和完整 run artifacts 的 campaign")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" {
		return fmt.Errorf("coverage-summarize requires -input")
	}
	directory, err := filepath.Abs(*input)
	if err != nil {
		return err
	}
	var settings experimentSettings
	if err := persistence.ReadJSON(filepath.Join(directory, "experiment-settings.json"), &settings); err != nil {
		return err
	}
	mode := settings.CoverageGuidanceMode
	if mode == "" {
		mode = coverageguidance.ModeLegacyRaw
	}
	var report experiment.Report
	if err := persistence.ReadJSON(filepath.Join(directory, "experiment-report.json"), &report); err != nil {
		return err
	}
	var config cliConfig
	if err := persistence.ReadJSON(filepath.Join(directory, "config.json"), &config); err != nil {
		return err
	}
	runs, err := persistence.ReadJSONLines[experiment.Run](
		filepath.Join(directory, "runs.jsonl"), report.CompletedRuns)
	if err != nil {
		return err
	}
	var observations []coverageguidance.CoverageObservation
	if mode == coverageguidance.ModeLegacyRaw {
		observations, err = buildLegacyCoverageRecords(directory, config, runs)
	} else {
		observations, err = persistence.ReadJSONLines[coverageguidance.CoverageObservation](
			filepath.Join(directory, "coverage-observations.jsonl"), report.CompletedRuns)
	}
	if err != nil {
		return err
	}
	if mode != coverageguidance.ModeLegacyRaw {
		changed, backfillErr := backfillObservationElapsed(directory, observations)
		if backfillErr != nil {
			return backfillErr
		}
		if changed {
			if err := rewriteCoverageObservations(
				filepath.Join(directory, "coverage-observations.jsonl"), observations); err != nil {
				return err
			}
		}
	}
	consistency := coverageConsistencyReport{
		Schema: "online-offline-coverage-consistency-v1", Candidates: len(observations),
	}
	for index, run := range runs {
		runDirectory := filepath.Join(directory, fmt.Sprintf("run-%04d-seed-%d", run.Index, run.Seed))
		resultPath := filepath.Join(runDirectory, "result.json")
		if _, statErr := os.Stat(resultPath); statErr != nil {
			if os.IsNotExist(statErr) && mode != coverageguidance.ModeLegacyRaw {
				consistency.UnavailableArtifacts++
				continue
			}
			return fmt.Errorf("run %d result artifact: %w", run.Index, statErr)
		}
		var result engine.Result
		var sequence plan.PlanSequence
		var candidate experiment.Candidate
		if err := persistence.ReadJSON(resultPath, &result); err != nil {
			return fmt.Errorf("run %d result: %w", run.Index, err)
		}
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "plan.json"), &sequence); err != nil {
			return fmt.Errorf("run %d plan: %w", run.Index, err)
		}
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "candidate.json"), &candidate); err != nil {
			return fmt.Errorf("run %d candidate: %w", run.Index, err)
		}
		offline, err := coverageanalysis.BuildCoverageObservation(coverageanalysis.ObservationInput{
			RunID: observations[index].RunID, CandidateID: candidate.ID,
			ParentPlanKey: candidate.ParentPlanKey, Source: candidate.Source,
			Plan: sequence, Result: result, ModelConfig: config.Model,
		})
		if err != nil {
			return fmt.Errorf("run %d offline observation: %w", run.Index, err)
		}
		online := observations[index]
		consistency.Compared++
		mismatch := false
		if !reflect.DeepEqual(online.RawTLCFingerprints, offline.RawTLCFingerprints) {
			consistency.RawMismatch++
			mismatch = true
		}
		if !reflect.DeepEqual(online.V2StateKeys, offline.V2StateKeys) {
			consistency.V2Mismatch++
			mismatch = true
		}
		if !reflect.DeepEqual(online.FacetKeys, offline.FacetKeys) {
			consistency.FacetMismatch++
			mismatch = true
		}
		if !reflect.DeepEqual(online.InteractionKeys, offline.InteractionKeys) {
			consistency.InteractionMismatch++
			mismatch = true
		}
		if online.StableKey != offline.StableKey {
			consistency.StableKeyMismatch++
			mismatch = true
		}
		if mismatch {
			consistency.Mismatches++
		}
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(directory, "online-offline-consistency.json"), consistency); err != nil {
		return err
	}
	var reusableGoals *offlineGoalEvaluationArtifact
	if settings.OfflineGoalEvaluation {
		var savedGoals offlineGoalEvaluationArtifact
		if readErr := persistence.ReadJSON(
			filepath.Join(directory, "offline-goal-evaluation.json"), &savedGoals,
		); readErr == nil {
			reusableGoals = &savedGoals
		}
	}
	first, firstCross, err := writeCoverageGuidanceArtifacts(
		directory, mode, report.CompletedRuns, report.ElapsedMillis,
		settings.OfflineGoalEvaluation, reusableGoals)
	if err != nil {
		return err
	}
	decisions, err := persistence.ReadJSONLines[coverageguidance.Decision](
		filepath.Join(directory, "corpus-decisions.jsonl"), report.CompletedRuns)
	if err != nil {
		return err
	}
	if mode != coverageguidance.ModeLegacyRaw {
		recomputedDecisions, _, recomputeErr := coverageguidance.Recompute(
			coverageguidance.Config{
				Mode: mode, FixedEnergy: settings.FixedEnergy,
				CorpusLimit: settings.CoverageCorpusLimit,
			}, observations)
		if recomputeErr != nil {
			return recomputeErr
		}
		for index := range decisions {
			if !reflect.DeepEqual(decisions[index], recomputedDecisions[index]) {
				consistency.DecisionRecomputeMismatch++
			}
		}
		if consistency.DecisionRecomputeMismatch > 0 {
			_ = persistence.WriteJSONAtomic(
				filepath.Join(directory, "online-offline-consistency.json"), consistency)
			return fmt.Errorf(
				"offline guidance decision mismatch=%d", consistency.DecisionRecomputeMismatch)
		}
	}
	recomputed, recomputedCross, err := coverageguidance.Summarize(
		mode, observations, decisions, report.ElapsedMillis)
	if err != nil {
		return err
	}
	repeated, repeatedCross, err := coverageguidance.Summarize(
		mode, observations, decisions, report.ElapsedMillis)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(first, recomputed) || !reflect.DeepEqual(recomputed, repeated) ||
		!reflect.DeepEqual(recomputedCross, repeatedCross) {
		return fmt.Errorf("coverage summary is not deterministic across repeated recomputation")
	}
	if consistency.Mismatches != 0 {
		return fmt.Errorf("online/offline coverage mismatch=%d", consistency.Mismatches)
	}
	_, err = fmt.Fprintf(stdout,
		"coverage summary recomputed: candidates=%d mismatch=0 raw=%d v2=%d corpus=%d input=%s\n",
		report.CompletedRuns, firstCross.RawDistinct, firstCross.V2Distinct, firstCross.CorpusSize, directory)
	return err
}

func backfillObservationElapsed(
	directory string, observations []coverageguidance.CoverageObservation,
) (bool, error) {
	needsBackfill := false
	for _, observation := range observations {
		if observation.ElapsedMillis <= 0 {
			needsBackfill = true
			break
		}
	}
	if !needsBackfill {
		return false, nil
	}
	file, err := os.Open(filepath.Join(directory, "progress.jsonl"))
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	var startedAt int64
	elapsedByCandidate := make(map[string]int64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event experiment.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return false, err
		}
		switch event.Kind {
		case experiment.EventExperimentStarted:
			startedAt = event.At.UnixMilli()
		case experiment.EventRunCompleted:
			if startedAt > 0 && event.CandidateID != "" {
				elapsedByCandidate[event.CandidateID] = event.At.UnixMilli() - startedAt
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	for index := range observations {
		if observations[index].ElapsedMillis > 0 {
			continue
		}
		elapsed, found := elapsedByCandidate[observations[index].CandidateID]
		if !found {
			return false, fmt.Errorf(
				"missing completion time for candidate %s", observations[index].CandidateID)
		}
		observations[index].ElapsedMillis = elapsed
		if err := coverageguidance.NormalizeObservation(&observations[index]); err != nil {
			return false, err
		}
	}
	return true, nil
}

func rewriteCoverageObservations(
	path string, observations []coverageguidance.CoverageObservation,
) error {
	if err := persistence.KeepJSONLines(path, 0); err != nil {
		return err
	}
	journal, err := persistence.OpenJournal(path)
	if err != nil {
		return err
	}
	for _, observation := range observations {
		if err := journal.Append(observation); err != nil {
			_ = journal.Close()
			return err
		}
	}
	return journal.Close()
}

func buildLegacyCoverageRecords(
	directory string, config cliConfig, runs []experiment.Run,
) ([]coverageguidance.CoverageObservation, error) {
	controller, err := coverageguidance.New(coverageguidance.Config{
		Mode: coverageguidance.ModeRandom, FixedEnergy: 1, CorpusLimit: max(1, len(runs)+1),
	})
	if err != nil {
		return nil, err
	}
	observationPath := filepath.Join(directory, "coverage-observations.jsonl")
	decisionPath := filepath.Join(directory, "corpus-decisions.jsonl")
	parentPath := filepath.Join(directory, "parent-selection.jsonl")
	for _, path := range []string{observationPath, decisionPath, parentPath} {
		if err := persistence.KeepJSONLines(path, 0); err != nil {
			return nil, err
		}
	}
	observationJournal, err := persistence.OpenJournal(observationPath)
	if err != nil {
		return nil, err
	}
	decisionJournal, err := persistence.OpenJournal(decisionPath)
	if err != nil {
		_ = observationJournal.Close()
		return nil, err
	}
	parentJournal, err := persistence.OpenJournal(parentPath)
	if err != nil {
		_ = observationJournal.Close()
		_ = decisionJournal.Close()
		return nil, err
	}
	defer func() {
		_ = observationJournal.Close()
		_ = decisionJournal.Close()
		_ = parentJournal.Close()
	}()
	observations := make([]coverageguidance.CoverageObservation, 0, len(runs))
	corpusSize := 0
	selectedParents := make(map[string]coverageguidance.ParentSelection)
	for index, run := range runs {
		runDirectory := filepath.Join(directory, fmt.Sprintf("run-%04d-seed-%d", run.Index, run.Seed))
		var result engine.Result
		var sequence plan.PlanSequence
		var candidate experiment.Candidate
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &result); err != nil {
			return nil, fmt.Errorf("legacy run %d result: %w", run.Index, err)
		}
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "plan.json"), &sequence); err != nil {
			return nil, fmt.Errorf("legacy run %d plan: %w", run.Index, err)
		}
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "candidate.json"), &candidate); err != nil {
			return nil, fmt.Errorf("legacy run %d candidate: %w", run.Index, err)
		}
		observation, err := coverageanalysis.BuildCoverageObservation(coverageanalysis.ObservationInput{
			RunID:       fmt.Sprintf("%s-feedback-%04d", config.ExecutionID, run.Index),
			CandidateID: candidate.ID, ParentPlanKey: candidate.ParentPlanKey,
			Source: candidate.Source, Plan: sequence, Result: result, ModelConfig: config.Model,
		})
		if err != nil {
			return nil, err
		}
		recordOnly, err := controller.Observe(observation)
		if err != nil {
			return nil, err
		}
		before := corpusSize
		if run.Retained {
			corpusSize++
		}
		recordOnly.GuidanceMode = coverageguidance.ModeLegacyRaw
		recordOnly.WasAdmitted = run.Retained
		recordOnly.AdmissionReason = run.CorpusAdmission
		recordOnly.CorpusSizeBefore = before
		recordOnly.CorpusSizeAfter = corpusSize
		recordOnly.FixedEnergy = 0
		recordOnly.StableDecisionKey = ""
		recordOnly.StableDecisionKey, err = configurationFingerprint(recordOnly)
		if err != nil {
			return nil, err
		}
		if err := observationJournal.Append(observation); err != nil {
			return nil, err
		}
		if err := decisionJournal.Append(recordOnly); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
		if candidate.ParentID != "" {
			selection := selectedParents[candidate.ParentID]
			selection.Schema = coverageguidance.SchemaVersion
			selection.CorpusID = candidate.ParentID
			selection.ParentPlanKey = candidate.ParentPlanKey
			selection.Policy = "legacy-ready-queue"
			selection.FixedEnergy++
			if selection.Sequence == 0 {
				selection.Sequence = index + 1
			}
			selectedParents[candidate.ParentID] = selection
		}
	}
	parentIDs := make([]string, 0, len(selectedParents))
	for parentID := range selectedParents {
		parentIDs = append(parentIDs, parentID)
	}
	sort.Strings(parentIDs)
	for _, parentID := range parentIDs {
		if err := parentJournal.Append(selectedParents[parentID]); err != nil {
			return nil, err
		}
	}
	return observations, nil
}
