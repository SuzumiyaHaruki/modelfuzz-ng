// Frozen diagnostic command for the accepted round-six C=2 differential
// analysis. It is not part of the default fuzz or mainline goal-search path.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const c2DifferentialSchema = "raft-c2-differential-v1-prototype"

type c2StepComparison struct {
	SchemaVersion       string                                   `json:"schema_version"`
	Campaign            string                                   `json:"campaign"`
	CandidateIndex      int                                      `json:"candidate_index"`
	PlanActionIndex     int                                      `json:"plan_action_index"`
	TraceStep           int                                      `json:"trace_step"`
	PlanAction          plan.PlanAction                          `json:"plan_action"`
	MessageSelector     *plan.MessageRangeSelector               `json:"message_selector,omitempty"`
	ResolvedAction      core.Action                              `json:"resolved_action"`
	ResolvedMessageID   core.MessageID                           `json:"resolved_message_id,omitempty"`
	Effects             []core.Effect                            `json:"effects"`
	ModelEvents         []string                                 `json:"model_events"`
	ObservationDigest   string                                   `json:"observation_digest"`
	GoalBindings        map[goalsearch.Symbol]goalsearch.Binding `json:"goal_bindings"`
	GoalWaypoint        string                                   `json:"goal_waypoint"`
	GoalDistance        int                                      `json:"goal_distance"`
	BranchTemplate      goalsearch.BranchTemplateID              `json:"branch_template"`
	PlannedSignature    string                                   `json:"planned_signature"`
	PartialEvidence     []string                                 `json:"partial_evidence"`
	CommitmentReached   bool                                     `json:"commitment_reached"`
	FinalRealizedStatus string                                   `json:"final_realized_status"`
	FrontierDecision    string                                   `json:"frontier_decision"`
	EvictedBranches     []goalsearch.BranchTemplateID            `json:"evicted_branches,omitempty"`
	PrefixEndAction     int                                      `json:"prefix_end_action"`
	QueueShape          string                                   `json:"queue_shape"`
	FacetKeys           []int64                                  `json:"facet_keys,omitempty"`
	InteractionKeys     []int64                                  `json:"interaction_keys,omitempty"`
	MutationOperator    string                                   `json:"mutation_operator"`
	BranchBudget        string                                   `json:"branch_budget"`
	SemanticStepKey     string                                   `json:"semantic_step_key"`
}

type c2Divergence struct {
	ComparedCampaign string          `json:"compared_campaign"`
	CandidateIndex   int             `json:"candidate_index"`
	PlanActionIndex  int             `json:"plan_action_index"`
	TraceStep        int             `json:"trace_step"`
	C2Action         core.Action     `json:"c2_action"`
	OtherAction      core.Action     `json:"other_action"`
	C2PlanAction     plan.PlanAction `json:"c2_plan_action"`
	OtherPlanAction  plan.PlanAction `json:"other_plan_action"`
	Reason           string          `json:"reason"`
}

type c2MinimalSkeleton struct {
	SourceCampaign       string                      `json:"source_campaign"`
	Seed                 int64                       `json:"seed"`
	TargetCandidate      int                         `json:"target_candidate"`
	ContributingBranch   goalsearch.BranchTemplateID `json:"contributing_branch"`
	NecessaryWaypoints   []string                    `json:"necessary_waypoints"`
	NecessaryEvidence    []string                    `json:"necessary_evidence"`
	NecessaryMessageKind string                      `json:"necessary_message_class"`
	Commitment           goalsearch.BranchCommitment `json:"commitment"`
	IncidentalActions    []core.ActionKind           `json:"incidental_action_kinds"`
	NotAMutationScript   bool                        `json:"not_a_mutation_script"`
}

type c2CampaignSummary struct {
	Campaign              string                        `json:"campaign"`
	Seed                  int64                         `json:"seed"`
	TargetReached         bool                          `json:"target_reached"`
	FirstTargetCandidate  int                           `json:"first_target_candidate"`
	Candidates            int                           `json:"candidates"`
	Actions               int                           `json:"actions"`
	FrontierCapacity      int                           `json:"frontier_capacity"`
	FullRealizedBranches  []goalsearch.BranchTemplateID `json:"full_realized_branches"`
	FullRealizedInstances []c2RealizedInstance          `json:"full_realized_instances"`
	RealizedDistribution  map[string]int                `json:"realized_distribution"`
	FinalFrontierSeeds    []goalsearch.FrontierSeed     `json:"final_frontier_seeds"`
}

type c2RealizedInstance struct {
	CandidateIndex  int                         `json:"candidate_index"`
	PlannedBranch   goalsearch.BranchTemplateID `json:"planned_branch"`
	RealizedBranch  goalsearch.BranchTemplateID `json:"realized_branch"`
	RealizedKey     string                      `json:"realized_key"`
	Deviation       goalsearch.BranchDeviation  `json:"deviation"`
	ContributedGoal bool                        `json:"contributed_goal"`
}

type c2DifferentialSummary struct {
	SchemaVersion       string              `json:"schema_version"`
	InputRoot           string              `json:"input_root"`
	ReferenceCampaign   string              `json:"reference_campaign"`
	Seed                int64               `json:"seed"`
	Campaigns           []c2CampaignSummary `json:"campaigns"`
	FirstDivergences    []c2Divergence      `json:"first_divergences"`
	MinimalSkeleton     c2MinimalSkeleton   `json:"minimal_success_skeleton"`
	StepFrameCount      int                 `json:"step_frame_count"`
	AnalysisLimitations []string            `json:"analysis_limitations"`
}

func c2DifferentialAnalysisCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng c2-differential-analysis", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "completed C2 differential benchmark root")
	output := flags.String("output", "", "new analysis output directory")
	reference := flags.String("reference", "c2-diff-realized-cap2", "successful reference campaign")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *output == "" {
		flags.Usage()
		return fmt.Errorf("-input and -output are required")
	}
	if err := createOutputDirectory(*output); err != nil {
		return err
	}
	campaignDirectories, err := c2CampaignDirectories(*input)
	if err != nil {
		return err
	}
	framesByCampaign := make(map[string][]c2StepComparison, len(campaignDirectories))
	var summary c2DifferentialSummary
	summary.SchemaVersion = c2DifferentialSchema
	summary.InputRoot = *input
	summary.ReferenceCampaign = *reference
	summary.AnalysisLimitations = []string{
		"Facet/Interaction 只在模型状态可与具体 step 一一对齐时写入；当前旧 artifact 保存的是候选级模型状态序列。",
		"Frontier insert 与 replace 在第五轮原始 JSONL 中共用 frontier_changed；evict 使用原始 Branch Progress 记录。",
		"Minimal Skeleton 是离线因果摘要，不是可直接执行或硬编码到 mutation 的成功 Plan。",
	}
	for _, directory := range campaignDirectories {
		campaign := filepath.Base(filepath.Dir(directory))
		frames, campaignSummary, loadErr := loadC2Campaign(campaign, directory)
		if loadErr != nil {
			return loadErr
		}
		framesByCampaign[campaign] = frames
		summary.Campaigns = append(summary.Campaigns, campaignSummary)
		summary.StepFrameCount += len(frames)
		if campaign == *reference {
			summary.Seed = campaignSummary.Seed
		}
	}
	referenceFrames, ok := framesByCampaign[*reference]
	if !ok {
		return fmt.Errorf("reference campaign %q was not found", *reference)
	}
	for campaign, frames := range framesByCampaign {
		if campaign == *reference {
			continue
		}
		summary.FirstDivergences = append(
			summary.FirstDivergences,
			firstC2Divergence(campaign, referenceFrames, frames),
		)
	}
	sort.Slice(summary.FirstDivergences, func(i, j int) bool {
		return summary.FirstDivergences[i].ComparedCampaign <
			summary.FirstDivergences[j].ComparedCampaign
	})
	skeleton, err := c2Skeleton(
		filepath.Join(*input, *reference, fmt.Sprintf("seed-%d", summary.Seed)),
		*reference, summary.Seed,
	)
	if err != nil {
		return err
	}
	summary.MinimalSkeleton = skeleton
	journal, err := persistence.OpenJournal(filepath.Join(*output, "c2-step-comparison.jsonl"))
	if err != nil {
		return err
	}
	campaignNames := make([]string, 0, len(framesByCampaign))
	for campaign := range framesByCampaign {
		campaignNames = append(campaignNames, campaign)
	}
	sort.Strings(campaignNames)
	for _, campaign := range campaignNames {
		for _, frame := range framesByCampaign[campaign] {
			if err := journal.Append(frame); err != nil {
				_ = journal.Close()
				return err
			}
		}
	}
	if err := journal.Close(); err != nil {
		return err
	}
	if err := writeJSONFile(
		filepath.Join(*output, "c2-differential-summary.json"), summary,
	); err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		stdout, "C2 differential analysis 结束: campaigns=%d frames=%d output=%s\n",
		len(summary.Campaigns), summary.StepFrameCount, *output,
	)
	return err
}

func c2CampaignDirectories(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var directories []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		seeds, globErr := filepath.Glob(filepath.Join(root, entry.Name(), "seed-*"))
		if globErr != nil {
			return nil, globErr
		}
		for _, seed := range seeds {
			if _, statErr := os.Stat(filepath.Join(seed, "final-report.json")); statErr == nil {
				directories = append(directories, seed)
			}
		}
	}
	sort.Strings(directories)
	return directories, nil
}

func loadC2Campaign(
	campaign string,
	directory string,
) ([]c2StepComparison, c2CampaignSummary, error) {
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(directory, "final-report.json"), &report); err != nil {
		return nil, c2CampaignSummary{}, err
	}
	progress, err := readGoalProgressJSONL(filepath.Join(directory, "goal-progress.jsonl"))
	if err != nil {
		return nil, c2CampaignSummary{}, err
	}
	branchProgress, err := readBranchProgressJSONL(filepath.Join(directory, "branch-progress.jsonl"))
	if err != nil {
		return nil, c2CampaignSummary{}, err
	}
	branchByCandidate := make(map[int]branchProgressRecord, len(branchProgress))
	for _, record := range branchProgress {
		branchByCandidate[record.CandidateIndex] = record
	}
	var frames []c2StepComparison
	for _, record := range progress {
		runDirectory := filepath.Join(
			directory, "runs", fmt.Sprintf("candidate-%06d", record.CandidateIndex),
		)
		var result engine.Result
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &result); err != nil {
			return nil, c2CampaignSummary{}, err
		}
		var sequence plan.PlanSequence
		if err := persistence.ReadJSON(filepath.Join(runDirectory, "plan.json"), &sequence); err != nil {
			return nil, c2CampaignSummary{}, err
		}
		var evaluation goalsearch.EvaluationResult
		if err := persistence.ReadJSON(
			filepath.Join(runDirectory, "goal-progress-online.json"), &evaluation,
		); err != nil {
			return nil, c2CampaignSummary{}, err
		}
		vector, vectorErr := c2EvidenceVector(record, result, evaluation)
		if vectorErr != nil {
			return nil, c2CampaignSummary{}, vectorErr
		}
		mapper, mapperErr := raftmodel.NewMapperWithConfig(report.Settings.Config.Model)
		if mapperErr != nil {
			return nil, c2CampaignSummary{}, mapperErr
		}
		planIndices := concretePlanIndices(result.Resolutions)
		facetByStep, interactionByStep, facetErr := c2FacetKeys(
			record.RunID, report.Settings.Config.Model, result,
		)
		if facetErr != nil {
			return nil, c2CampaignSummary{}, facetErr
		}
		branchRecord := branchByCandidate[record.CandidateIndex]
		for stepIndex, step := range result.Trace.Steps {
			planIndex := -1
			var planAction plan.PlanAction
			if stepIndex < len(planIndices) {
				planIndex = planIndices[stepIndex]
			}
			if planIndex >= 0 && planIndex < len(sequence.Actions) {
				planAction = sequence.Actions[planIndex]
			}
			transition, transitionErr := model.TransitionFromRecord(step)
			if transitionErr != nil {
				return nil, c2CampaignSummary{}, transitionErr
			}
			events, mapErr := mapper.Map(transition)
			eventNames := make([]string, 0, len(events))
			for _, event := range events {
				eventNames = append(eventNames, event.Name)
			}
			if mapErr != nil {
				eventNames = append(eventNames, "mapping-error:"+mapErr.Error())
			}
			evidence := evidenceAtStep(vector, stepIndex)
			frontierDecision := "none"
			if record.FrontierChanged {
				frontierDecision = "insert-or-replace"
			}
			realized := "undecidable"
			if branchRecord.RealizedDecidable {
				realized = string(branchRecord.RealizedTemplateID)
				if realized == "" {
					realized = "decidable-unmatched"
				}
			}
			frame := c2StepComparison{
				SchemaVersion: c2DifferentialSchema, Campaign: campaign,
				CandidateIndex: record.CandidateIndex, PlanActionIndex: planIndex,
				TraceStep: stepIndex, PlanAction: planAction,
				MessageSelector: planAction.Messages, ResolvedAction: step.Action,
				ResolvedMessageID: step.Action.Message,
				Effects:           append([]core.Effect(nil), step.Effects...),
				ModelEvents:       eventNames, ObservationDigest: step.ObservationDigest,
				GoalBindings: record.Bindings, GoalWaypoint: record.CurrentWaypoint,
				GoalDistance: record.Distance, PartialEvidence: evidence,
				CommitmentReached: vector.Commitment.Reached &&
					vector.Commitment.FirstStep <= stepIndex,
				FinalRealizedStatus: realized, FrontierDecision: frontierDecision,
				EvictedBranches:  branchRecord.EvictedBranches,
				PrefixEndAction:  evaluation.PrefixEndActionIndex,
				QueueShape:       goalsearch.MessageQueueShapeKey(evaluation.FinalObservation),
				FacetKeys:        facetByStep[stepIndex],
				InteractionKeys:  interactionByStep[stepIndex],
				MutationOperator: record.MutationOperator,
				BranchBudget:     "fixed-round-robin",
			}
			if record.Branch != nil {
				frame.BranchTemplate = record.Branch.BranchTemplateID
				frame.PlannedSignature = record.Branch.PlannedBranchSignature.StableKey
			}
			frame.SemanticStepKey = c2StepKey(frame)
			frames = append(frames, frame)
		}
	}
	realized := make([]goalsearch.BranchTemplateID, 0)
	var realizedInstances []c2RealizedInstance
	seen := make(map[goalsearch.BranchTemplateID]struct{})
	for _, record := range branchProgress {
		if !record.RealizedDecidable || record.RealizedTemplateID == "" {
			continue
		}
		if _, exists := seen[record.RealizedTemplateID]; !exists {
			seen[record.RealizedTemplateID] = struct{}{}
			realized = append(realized, record.RealizedTemplateID)
		}
		realizedInstances = append(realizedInstances, c2RealizedInstance{
			CandidateIndex:  record.CandidateIndex,
			PlannedBranch:   record.PlannedTemplateID,
			RealizedBranch:  record.RealizedTemplateID,
			RealizedKey:     record.RealizedKey,
			Deviation:       record.Deviation,
			ContributedGoal: record.GoalReached,
		})
	}
	sort.Slice(realized, func(i, j int) bool { return realized[i] < realized[j] })
	return frames, c2CampaignSummary{
		Campaign: campaign, Seed: report.Settings.Seed,
		TargetReached:        report.TargetReached,
		FirstTargetCandidate: report.FirstTargetCandidate,
		Candidates:           report.Candidates, Actions: report.Actions,
		FrontierCapacity:      report.Settings.TotalFrontierCapacity,
		FullRealizedBranches:  realized,
		FullRealizedInstances: realizedInstances,
		RealizedDistribution:  report.Branch.RealizedDistribution,
		FinalFrontierSeeds:    report.Frontier.Seeds,
	}, nil
}

func c2FacetKeys(
	runID string,
	config raftmodel.Config,
	result engine.Result,
) (map[int][]int64, map[int][]int64, error) {
	frames, err := coverageanalysis.BuildCoverageFrames(coverageanalysis.RunArtifact{
		Name: runID, Source: "c2-differential", ModelConfig: config,
		Initial: result.Initial, Trace: result.Trace,
		ModelEvents: result.ModelEvents, ModelStates: result.ModelStates,
	})
	if err != nil {
		return nil, nil, err
	}
	facets := make(map[int][]int64)
	interactions := make(map[int][]int64)
	for _, frame := range frames {
		if frame.StepIndex < 0 {
			continue
		}
		projection, err := raftmodel.ProjectCoverageFacets(frame.ModelState, frame.Context)
		if err != nil {
			return nil, nil, err
		}
		facets[frame.StepIndex] = append(facets[frame.StepIndex],
			projection.ElectionKey, projection.ReplicationKey,
			projection.SnapshotKey, projection.RecoveryKey, projection.NetworkKey,
		)
		for _, interaction := range projection.Interactions {
			interactions[frame.StepIndex] = append(
				interactions[frame.StepIndex], interaction.Key,
			)
		}
	}
	for step := range facets {
		facets[step] = uniqueInt64(facets[step])
	}
	for step := range interactions {
		interactions[step] = uniqueInt64(interactions[step])
	}
	return facets, interactions, nil
}

func uniqueInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func c2EvidenceVector(
	record goalProgressRecord,
	result engine.Result,
	evaluation goalsearch.EvaluationResult,
) (goalsearch.BranchEvidenceVector, error) {
	if record.Branch == nil {
		return goalsearch.BranchEvidenceVector{
			SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
			HighestLevel:  goalsearch.EvidenceLevelPlanned,
			Commitment:    goalsearch.BranchCommitment{FirstStep: -1},
		}, nil
	}
	template, err := goalsearch.BranchTemplate(record.Branch.BranchTemplateID)
	if err != nil {
		return goalsearch.BranchEvidenceVector{}, err
	}
	return goalsearch.AnalyzeBranchEvidence(template, *record.Branch, evaluation, result.Trace)
}

func concretePlanIndices(resolutions []plan.Resolution) []int {
	var result []int
	for index, resolution := range resolutions {
		for range resolution.Actions {
			result = append(result, index)
		}
	}
	return result
}

func evidenceAtStep(vector goalsearch.BranchEvidenceVector, step int) []string {
	var ids []string
	for _, dimension := range vector.Dimensions {
		if dimension.FirstObservedStep >= 0 && dimension.FirstObservedStep <= step &&
			(dimension.Status == goalsearch.EvidenceSupported ||
				dimension.Status == goalsearch.EvidenceCommitted) {
			ids = append(ids, dimension.EvidenceID)
		}
	}
	sort.Strings(ids)
	return ids
}

func c2StepKey(frame c2StepComparison) string {
	encoded, _ := json.Marshal(struct {
		Candidate int
		Plan      int
		Action    core.Action
		Events    []string
		Digest    string
	}{
		frame.CandidateIndex, frame.PlanActionIndex, frame.ResolvedAction,
		frame.ModelEvents, frame.ObservationDigest,
	})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func firstC2Divergence(
	campaign string,
	reference []c2StepComparison,
	other []c2StepComparison,
) c2Divergence {
	limit := min(len(reference), len(other))
	for index := 0; index < limit; index++ {
		if reference[index].SemanticStepKey == other[index].SemanticStepKey {
			continue
		}
		return c2Divergence{
			ComparedCampaign: campaign,
			CandidateIndex:   min(reference[index].CandidateIndex, other[index].CandidateIndex),
			PlanActionIndex:  min(reference[index].PlanActionIndex, other[index].PlanActionIndex),
			TraceStep:        min(reference[index].TraceStep, other[index].TraceStep),
			C2Action:         reference[index].ResolvedAction,
			OtherAction:      other[index].ResolvedAction,
			C2PlanAction:     reference[index].PlanAction,
			OtherPlanAction:  other[index].PlanAction,
			Reason:           "first aligned semantic step key differs",
		}
	}
	return c2Divergence{
		ComparedCampaign: campaign, CandidateIndex: -1, PlanActionIndex: -1,
		TraceStep: -1, Reason: fmt.Sprintf("common prefix identical; frame lengths c2=%d other=%d",
			len(reference), len(other)),
	}
}

func c2Skeleton(
	directory string,
	campaign string,
	seed int64,
) (c2MinimalSkeleton, error) {
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(directory, "final-report.json"), &report); err != nil {
		return c2MinimalSkeleton{}, err
	}
	if !report.TargetReached || report.FirstTargetCandidate <= 0 {
		return c2MinimalSkeleton{}, fmt.Errorf("reference campaign did not reach its Goal")
	}
	candidate := report.FirstTargetCandidate - 1
	runDirectory := filepath.Join(directory, "runs", fmt.Sprintf("candidate-%06d", candidate))
	var evaluation goalsearch.EvaluationResult
	if err := persistence.ReadJSON(
		filepath.Join(runDirectory, "goal-progress-online.json"), &evaluation,
	); err != nil {
		return c2MinimalSkeleton{}, err
	}
	var result engine.Result
	if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &result); err != nil {
		return c2MinimalSkeleton{}, err
	}
	progress, err := readGoalProgressJSONL(filepath.Join(directory, "goal-progress.jsonl"))
	if err != nil {
		return c2MinimalSkeleton{}, err
	}
	if candidate >= len(progress) || progress[candidate].Branch == nil {
		return c2MinimalSkeleton{}, fmt.Errorf("target candidate has no Branch record")
	}
	instance := *progress[candidate].Branch
	contributing := instance.RealizedBranchSignature.MatchedTemplateID
	if contributing == "" {
		contributing = instance.BranchTemplateID
	}
	template, err := goalsearch.BranchTemplate(contributing)
	if err != nil {
		return c2MinimalSkeleton{}, err
	}
	actual, err := goalsearch.AnalyzeBranch(
		template, evaluation, result.Initial, result.Trace, goalsearch.BranchAblationNone,
	)
	if err != nil {
		return c2MinimalSkeleton{}, err
	}
	vector, err := goalsearch.AnalyzeBranchEvidence(template, actual, evaluation, result.Trace)
	if err != nil {
		return c2MinimalSkeleton{}, err
	}
	var evidence []string
	for _, dimension := range vector.Dimensions {
		if dimension.RequiredForCommitment &&
			(dimension.Status == goalsearch.EvidenceSupported ||
				dimension.Status == goalsearch.EvidenceCommitted) {
			evidence = append(evidence, dimension.EvidenceID)
		}
	}
	var waypoints []string
	for _, waypoint := range evaluation.Instance.WaypointResults {
		if waypoint.Reached {
			waypoints = append(waypoints, waypoint.WaypointID)
		}
	}
	incidentalSet := make(map[core.ActionKind]struct{})
	for _, step := range result.Trace.Steps {
		if step.Action.Kind == core.ActionAdvanceTime ||
			step.Action.Kind == core.ActionRequest {
			incidentalSet[step.Action.Kind] = struct{}{}
		}
	}
	var incidental []core.ActionKind
	for kind := range incidentalSet {
		incidental = append(incidental, kind)
	}
	sort.Slice(incidental, func(i, j int) bool { return incidental[i] < incidental[j] })
	return c2MinimalSkeleton{
		SourceCampaign: campaign, Seed: seed, TargetCandidate: candidate,
		ContributingBranch: contributing, NecessaryWaypoints: waypoints,
		NecessaryEvidence:    evidence,
		NecessaryMessageKind: template.PlannedDimensions.KeyMessageClass,
		Commitment:           vector.Commitment, IncidentalActions: incidental,
		NotAMutationScript: true,
	}, nil
}

func readGoalProgressJSONL(path string) ([]goalProgressRecord, error) {
	var result []goalProgressRecord
	err := readJSONL(path, func(line []byte) error {
		var record goalProgressRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		result = append(result, record)
		return nil
	})
	return result, err
}

func readBranchProgressJSONL(path string) ([]branchProgressRecord, error) {
	var result []branchProgressRecord
	err := readJSONL(path, func(line []byte) error {
		var record branchProgressRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		result = append(result, record)
		return nil
	})
	return result, err
}

func readJSONL(path string, consume func([]byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		if err := consume(scanner.Bytes()); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return scanner.Err()
}
