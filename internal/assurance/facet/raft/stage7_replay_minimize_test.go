package raft_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

type stage7ReplayItem struct {
	Mode            activeMode `json:"mode"`
	CanonicalKey    string     `json:"canonical_key"`
	RecordDigest    string     `json:"record_digest"`
	PlanDigest      string     `json:"plan_digest"`
	TraceDigest     string     `json:"trace_digest"`
	ConcreteStatus  string     `json:"concrete_replay_status"`
	MatchedSteps    uint64     `json:"matched_steps"`
	ReexecuteStatus string     `json:"reexecute_status"`
	KeyVerified     bool       `json:"key_verified"`
}

type stage7ReplaySummary struct {
	Schema             string             `json:"schema"`
	PreregistrationSHA string             `json:"preregistration_sha256"`
	SelectedSlots      int                `json:"selected_key_slots"`
	DistinctRecords    int                `json:"distinct_record_digests"`
	Mismatches         int                `json:"mismatches"`
	Items              []stage7ReplayItem `json:"items"`
}

type stage7SelectedRepresentative struct {
	Mode      activeMode
	Canonical string
	Reference facetbreadth.RepresentativeRefV1
	Execution activeExecution
	Config    stage7ExecutionConfig
}

func stage7ReplayRepresentatives(
	t *testing.T,
	executor model.Executor,
	campaigns []stage7CampaignResult,
	resultsDir string,
) stage7ReplaySummary {
	t.Helper()
	selected := stage7SelectGlobalRepresentatives(t, campaigns)
	distinct := make(map[string]struct{}, len(selected))
	summary := stage7ReplaySummary{
		Schema:             "modelfuzz-ng-stage7-representative-replay-v1",
		PreregistrationSHA: stage7PreregistrationSHA,
		SelectedSlots:      len(selected), Items: make([]stage7ReplayItem, 0, len(selected)),
	}
	for _, representative := range selected {
		execution := representative.Execution
		distinct[execution.Record.RecordDigest] = struct{}{}
		runtime, err := stage7NewRuntime(
			representative.Config, execution.Candidate, execution.Seed,
		)
		if err != nil {
			t.Fatal(err)
		}
		replayer, err := tracepkg.NewReplayer(runtime)
		if err != nil {
			t.Fatal(err)
		}
		replayed, replayErr := replayer.Replay(
			context.Background(), execution.Completion.Execution.Result.Trace.Copy(),
		)
		item := stage7ReplayItem{
			Mode: representative.Mode, CanonicalKey: representative.Canonical,
			RecordDigest:   execution.Record.RecordDigest,
			PlanDigest:     execution.Completion.Run.PlanDigest,
			TraceDigest:    execution.Completion.Run.TraceDigest,
			ConcreteStatus: string(replayed.Status), MatchedSteps: replayed.MatchedSteps,
		}
		if replayErr != nil || replayed.Status != tracepkg.StatusCompleted {
			summary.Mismatches++
			t.Fatalf("representative %s concrete replay: %v status=%s",
				representative.Canonical, replayErr, replayed.Status)
		}
		reexecuted := runStage7Candidate(
			t, representative.Config, execution.Candidate, executor,
		)
		item.ReexecuteStatus = string(reexecuted.Completion.Execution.Result.Status)
		item.KeyVerified = stage7EvaluationHasKey(reexecuted.Evaluations, representative.Canonical)
		if !stage7SemanticExecutionEqual(execution, reexecuted) || !item.KeyVerified {
			summary.Mismatches++
			t.Fatalf("representative %s strict reexecution mismatch", representative.Canonical)
		}
		summary.Items = append(summary.Items, item)
	}
	summary.DistinctRecords = len(distinct)
	if summary.SelectedSlots > facetbreadth.MaxRepresentativeSlotsV1 ||
		summary.DistinctRecords > facetbreadth.MaxRepresentativeSlotsV1 {
		t.Fatalf("representative bound exceeded: %+v", summary)
	}
	sort.Slice(summary.Items, func(i, j int) bool {
		if summary.Items[i].Mode != summary.Items[j].Mode {
			return summary.Items[i].Mode < summary.Items[j].Mode
		}
		return summary.Items[i].CanonicalKey < summary.Items[j].CanonicalKey
	})
	if resultsDir != "" {
		writeStage7JSONAtomic(t, filepath.Join(resultsDir, "representative-replay.json"), summary)
	}
	return summary
}

func stage7SelectGlobalRepresentatives(
	t *testing.T,
	campaigns []stage7CampaignResult,
) []stage7SelectedRepresentative {
	t.Helper()
	selected := make(map[string]stage7SelectedRepresentative)
	for _, campaign := range campaigns {
		for _, covered := range campaign.facetSnapshot.Covered {
			execution, exists := campaign.executions[covered.Shortest.RecordDigest]
			if !exists {
				t.Fatalf("%d/%s missing shortest record %s",
					campaign.Seed, campaign.Mode, covered.Shortest.RecordDigest)
			}
			slot := string(campaign.Mode) + "\x00" + covered.CanonicalString
			candidate := stage7SelectedRepresentative{
				Mode: campaign.Mode, Canonical: covered.CanonicalString,
				Reference: covered.Shortest, Execution: execution, Config: campaign.executionConfig,
			}
			current, exists := selected[slot]
			if !exists || stage7RepresentativeLess(candidate.Reference, current.Reference) {
				selected[slot] = candidate
			}
		}
	}
	result := make([]stage7SelectedRepresentative, 0, len(selected))
	for _, representative := range selected {
		result = append(result, representative)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Mode != result[j].Mode {
			return result[i].Mode < result[j].Mode
		}
		return result[i].Canonical < result[j].Canonical
	})
	return result
}

func stage7RepresentativeLess(
	left, right facetbreadth.RepresentativeRefV1,
) bool {
	switch {
	case left.PlanActionCount != right.PlanActionCount:
		return left.PlanActionCount < right.PlanActionCount
	case left.TraceStepCount != right.TraceStepCount:
		return left.TraceStepCount < right.TraceStepCount
	case left.PlanDigest != right.PlanDigest:
		return left.PlanDigest < right.PlanDigest
	case left.TraceDigest != right.TraceDigest:
		return left.TraceDigest < right.TraceDigest
	default:
		return left.RecordDigest < right.RecordDigest
	}
}

func stage7EvaluationHasKey(evaluations []facet.EvaluationV1, canonical string) bool {
	for _, evaluation := range evaluations {
		for _, observation := range evaluation.Observations {
			got, err := observation.Key.CanonicalString()
			if err == nil && got == canonical {
				return true
			}
		}
	}
	return false
}

type stage7MinimizeItem struct {
	Mode                 activeMode         `json:"mode"`
	Seed                 int64              `json:"seed"`
	Lineage              string             `json:"lineage"`
	OriginalPlanDigest   string             `json:"original_plan_digest"`
	OriginalActions      int                `json:"original_actions"`
	MinimizedActions     int                `json:"minimized_actions"`
	Signature            minimize.Signature `json:"signature"`
	Attempts             int                `json:"attempts"`
	StableReproductions  int                `json:"stable_reproductions"`
	CacheHits            int                `json:"cache_hits"`
	OneMinimal           bool               `json:"one_minimal"`
	ConcreteReplayStatus string             `json:"concrete_replay_status"`
	MatchedSteps         uint64             `json:"matched_steps"`
	FinalSignatureMatch  bool               `json:"final_signature_match"`
}

type stage7MinimizeSummary struct {
	Schema             string               `json:"schema"`
	PreregistrationSHA string               `json:"preregistration_sha256"`
	Items              []stage7MinimizeItem `json:"items"`
}

func stage7MinimizeMutants(
	t *testing.T,
	executor model.Executor,
	campaigns []stage7CampaignResult,
	resultsDir string,
) stage7MinimizeSummary {
	t.Helper()
	selected := make(map[activeMode]struct {
		campaign  stage7CampaignResult
		execution activeExecution
		ordinal   int
	})
	for _, campaign := range campaigns {
		if !campaign.Failure.Detected {
			continue
		}
		execution, ok := campaign.executions[campaign.Failure.RecordDigest]
		if !ok {
			t.Fatalf("%d/%s missing failure execution", campaign.Seed, campaign.Mode)
		}
		current, exists := selected[campaign.Mode]
		if !exists || campaign.Failure.CandidateOrdinal < current.ordinal ||
			campaign.Failure.CandidateOrdinal == current.ordinal && campaign.Seed < current.campaign.Seed {
			selected[campaign.Mode] = struct {
				campaign  stage7CampaignResult
				execution activeExecution
				ordinal   int
			}{campaign: campaign, execution: execution, ordinal: campaign.Failure.CandidateOrdinal}
		}
	}
	summary := stage7MinimizeSummary{
		Schema:             "modelfuzz-ng-stage7-mutant-minimize-v1",
		PreregistrationSHA: stage7PreregistrationSHA,
		Items:              []stage7MinimizeItem{},
	}
	for _, mode := range []activeMode{activeCurrentBaseline, activeFacetOnly} {
		value, ok := selected[mode]
		if !ok {
			t.Fatalf("mutant mode %s produced no failure to minimize", mode)
		}
		execution := value.execution
		expected, failed := minimize.SignatureOf(execution.Completion.Execution.Result)
		if !failed {
			t.Fatalf("%s selected execution is not failure", mode)
		}
		for repeat := 0; repeat < 2; repeat++ {
			repeated, _ := stage7ExecutePlan(
				context.Background(), value.campaign.executionConfig,
				execution.Candidate, execution.Seed,
				execution.Completion.Execution.Plan, executor,
			)
			signature, ok := minimize.SignatureOf(repeated)
			if !ok || !expected.Equal(signature) {
				t.Fatalf("%s failure signature is unstable", mode)
			}
		}
		reduced, err := minimize.Reduce(
			context.Background(),
			execution.Completion.Execution.Plan.Copy(),
			minimize.Config{
				MaxAttempts: 1000, VerifyRuns: 2, FinalVerifyRuns: 3,
				InputPlanSHA256: execution.Completion.Run.PlanDigest,
				ConfigSHA256:    stage7ConfigurationFingerprint(value.campaign.executionConfig),
			},
			func(ctx context.Context, sequence plan.PlanSequence) (engine.Result, error) {
				return stage7ExecutePlan(
					ctx, value.campaign.executionConfig,
					execution.Candidate, execution.Seed, sequence, executor,
				)
			},
		)
		if err != nil {
			t.Fatalf("%s minimize: %v", mode, err)
		}
		finalSignature, ok := minimize.SignatureOf(reduced.MinimizedExecution)
		if !ok || !expected.Equal(finalSignature) {
			t.Fatalf("%s minimized signature mismatch", mode)
		}
		runtime, err := stage7NewRuntime(
			value.campaign.executionConfig, execution.Candidate, execution.Seed,
		)
		if err != nil {
			t.Fatal(err)
		}
		replayer, err := tracepkg.NewReplayer(runtime)
		if err != nil {
			t.Fatal(err)
		}
		replayed, replayErr := replayer.Replay(
			context.Background(), reduced.MinimizedExecution.Trace.Copy(),
		)
		if replayErr != nil || replayed.Status != tracepkg.StatusCompleted {
			t.Fatalf("%s minimized concrete replay: %v status=%s", mode, replayErr, replayed.Status)
		}
		item := stage7MinimizeItem{
			Mode: mode, Seed: value.campaign.Seed, Lineage: execution.Candidate.Lineage,
			OriginalPlanDigest: execution.Completion.Run.PlanDigest,
			OriginalActions:    reduced.Report.OriginalActions,
			MinimizedActions:   reduced.Report.MinimizedActions,
			Signature:          reduced.Report.Signature, Attempts: reduced.Report.Attempts,
			StableReproductions: reduced.Report.StableReproductions,
			CacheHits:           reduced.Report.CacheHits, OneMinimal: reduced.Report.OneMinimal,
			ConcreteReplayStatus: string(replayed.Status), MatchedSteps: replayed.MatchedSteps,
			FinalSignatureMatch: true,
		}
		summary.Items = append(summary.Items, item)
		if resultsDir != "" {
			writeStage7JSONAtomic(
				t,
				filepath.Join(resultsDir, "minimized", fmt.Sprintf("%s-plan.json", mode)),
				reduced.Plan,
			)
		}
	}
	if resultsDir != "" {
		writeStage7JSONAtomic(t, filepath.Join(resultsDir, "mutant-minimize.json"), summary)
	}
	return summary
}

func stage7ExecutePlan(
	ctx context.Context,
	config stage7ExecutionConfig,
	candidate activeCandidate,
	seed int64,
	sequence plan.PlanSequence,
	executor model.Executor,
) (engine.Result, error) {
	source := &staticPlanSource{sequence: sequence.Copy()}
	return stage7ExecuteRealEtcdRaft(ctx, config, candidate, seed, source, executor)
}
