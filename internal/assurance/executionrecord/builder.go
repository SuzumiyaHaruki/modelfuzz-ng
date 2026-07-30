package executionrecord

import (
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
)

func BuildV1(input BuildInput) (CompletedExecutionRecordV1, error) {
	if err := validateBuildInput(input); err != nil {
		return CompletedExecutionRecordV1{}, err
	}
	artifacts, err := normalizeArtifacts(input.Artifacts)
	if err != nil {
		return CompletedExecutionRecordV1{}, err
	}
	failure, err := buildFailureSummary(
		input.FailureSignature,
		input.Completion.Execution.Result.Status,
		artifacts,
	)
	if err != nil {
		return CompletedExecutionRecordV1{}, err
	}

	completion := input.Completion
	run := completion.Run
	result := completion.Execution.Result
	oracleCodes := normalizeOracleCodes(result.OracleFindings)
	effectCount := countEffects(result.Trace)
	configArtifact := singleArtifact(artifacts, ArtifactConfig)
	traceArtifact := singleArtifact(artifacts, ArtifactTrace)
	record := CompletedExecutionRecordV1{
		SchemaID:     SchemaIDV1,
		MajorVersion: MajorVersionV1,
		Candidate: CandidateIdentity{
			ID: completion.Candidate.ID, Kind: completion.Candidate.Kind,
			ParentID: completion.Candidate.ParentID, Source: completion.Candidate.Source,
			Depth: completion.Candidate.Depth, RunIndex: run.Index, Seed: run.Seed,
		},
		Plan: PlanSummary{
			Digest: run.PlanDigest, ActionCount: len(completion.Execution.Plan.Actions),
			Artifact: singleArtifact(artifacts, ArtifactPlan),
		},
		Engine: EngineOutcome{
			Status: result.Status, DebugError: result.Error, ModelExecuted: result.ModelExecuted,
			BudgetExhausted: result.BudgetExhausted, Termination: result.Termination,
			TerminationCode: result.TerminationCode, TerminationDetail: result.TerminationDetail,
			ResolutionCount: len(result.Resolutions), ConcreteActionCount: len(result.Actions.Actions),
			EffectCount: effectCount, TraceStepCount: len(result.Trace.Steps),
			ModelEventCount: len(result.ModelEvents), ModelStateCount: len(result.ModelStates),
			OracleFindingCount: len(result.OracleFindings),
		},
		Experiment: ExperimentOutcome{
			Completed: run.Completed, Status: run.Status, Succeeded: run.Succeeded, DebugError: run.Error,
		},
		Trace: TraceSummary{
			Digest: run.TraceDigest, StepCount: len(result.Trace.Steps), Version: result.Trace.Version,
			ExecutionID: result.Trace.ExecutionID, Seed: result.Trace.Seed,
			Artifact: traceArtifact,
		},
		Model: ModelSummary{
			Executed: result.ModelExecuted, EventCount: len(result.ModelEvents),
			StateCount: len(result.ModelStates), StatePathDigest: run.ModelStatePathDigest,
			EventsArtifact: singleArtifact(artifacts, ArtifactModelEvents),
			StatesArtifact: singleArtifact(artifacts, ArtifactModelStates),
		},
		Oracle: OracleSummary{
			FindingCount: len(result.OracleFindings), Codes: oracleCodes,
			Artifact: singleArtifact(artifacts, ArtifactOracleFindings),
		},
		Failure: failure,
		Corpus: CorpusOutcome{
			Retained: run.Retained, ID: run.CorpusID, Admission: run.CorpusAdmission,
		},
		Replay: ReplaySummary{
			TraceExecutionID: result.Trace.ExecutionID, TraceSeed: result.Trace.Seed,
			ConfigArtifact: configArtifact, TraceArtifact: traceArtifact,
		},
		ConfigurationFingerprint: input.ConfigurationFingerprint,
		Artifacts:                artifacts,
	}
	record.Replay.Replayable = replayable(record)
	if err := validateRecord(record, false); err != nil {
		return CompletedExecutionRecordV1{}, err
	}
	record.RecordDigest, err = recordDigest(record)
	if err != nil {
		return CompletedExecutionRecordV1{}, err
	}
	return cloneRecord(record), nil
}

func validateBuildInput(input BuildInput) error {
	completion := input.Completion
	run := completion.Run
	result := completion.Execution.Result
	candidate := completion.Candidate
	if !run.Completed {
		return fmt.Errorf("%w: experiment run is not completed", ErrInvalidRecord)
	}
	if candidate.ID == "" {
		return fmt.Errorf("%w: candidate ID is empty", ErrInvalidRecord)
	}
	if !validCandidateKind(candidate.Kind) {
		return fmt.Errorf("%w: unknown candidate kind %q", ErrInvalidRecord, candidate.Kind)
	}
	if run.CandidateID != candidate.ID || run.CandidateKind != candidate.Kind ||
		run.ParentID != candidate.ParentID || run.Source != candidate.Source || run.Depth != candidate.Depth {
		return fmt.Errorf("%w: candidate and run identity differ", ErrInvalidRecord)
	}
	if candidate.Depth < 0 || run.Index < 0 {
		return fmt.Errorf("%w: candidate depth and run index must be non-negative", ErrInvalidRecord)
	}
	if !validSHA256(input.ConfigurationFingerprint) {
		return fmt.Errorf("%w: invalid configuration fingerprint", ErrInvalidRecord)
	}
	if len(completion.Execution.Plan.Actions) == 0 {
		return fmt.Errorf("%w: completed plan has no actions", ErrInvalidRecord)
	}
	if err := completion.Execution.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: completed plan: %v", ErrInvalidRecord, err)
	}
	if !validSHA256(run.PlanDigest) {
		return fmt.Errorf("%w: invalid plan digest", ErrInvalidRecord)
	}
	if !validEngineStatus(result.Status) || !validEngineStatus(run.Status) {
		return fmt.Errorf("%w: unknown engine or experiment status", ErrInvalidRecord)
	}
	if !validTermination(result.Termination) || !validTermination(run.Termination) {
		return fmt.Errorf("%w: unknown termination reason", ErrInvalidRecord)
	}
	if run.Actions != len(result.Actions.Actions) ||
		run.Effects != countEffects(result.Trace) ||
		run.ModelEvents != len(result.ModelEvents) ||
		run.ModelStates != len(result.ModelStates) ||
		run.OracleFindings != len(result.OracleFindings) ||
		run.BudgetExhausted != result.BudgetExhausted ||
		run.Termination != result.Termination ||
		run.TerminationCode != result.TerminationCode {
		return fmt.Errorf("%w: run summary and engine result counts or termination differ", ErrInvalidRecord)
	}
	if result.Trace.Version == 0 {
		if run.TraceDigest != "" || len(result.Trace.Steps) != 0 {
			return fmt.Errorf("%w: absent trace has steps or a digest", ErrInvalidRecord)
		}
	} else {
		if result.Trace.Version != core.CurrentTraceVersion {
			return fmt.Errorf("%w: unsupported trace version %d", ErrInvalidRecord, result.Trace.Version)
		}
		if !validSHA256(run.TraceDigest) {
			return fmt.Errorf("%w: valid trace requires a trace digest", ErrInvalidRecord)
		}
	}
	if len(result.ModelStates) == 0 {
		if run.ModelStatePathDigest != "" {
			return fmt.Errorf("%w: empty model state path has a digest", ErrInvalidRecord)
		}
	} else if !validSHA256(run.ModelStatePathDigest) {
		return fmt.Errorf("%w: model states require a state path digest", ErrInvalidRecord)
	}
	if run.Retained && run.CorpusID == "" {
		return fmt.Errorf("%w: retained run has no corpus ID", ErrInvalidRecord)
	}
	if !run.Retained && run.CorpusID != "" {
		return fmt.Errorf("%w: unretained run has a corpus ID", ErrInvalidRecord)
	}
	return nil
}

func buildFailureSummary(input FailureSignatureInput, status engine.Status, artifacts []ArtifactReference) (FailureSummary, error) {
	if !input.Availability.valid() {
		return FailureSummary{}, fmt.Errorf("%w: unknown failure signature availability %q", ErrInvalidRecord, input.Availability)
	}
	if input.Availability == FailureSignatureAvailable && input.Signature == nil {
		return FailureSummary{}, fmt.Errorf("%w: available failure signature is nil", ErrInvalidRecord)
	}
	if input.Availability != FailureSignatureAvailable && input.Signature != nil {
		return FailureSummary{}, fmt.Errorf("%w: non-available failure signature is non-nil", ErrInvalidRecord)
	}
	if input.Availability != FailureSignatureNotApplicable &&
		(status == engine.StatusCompleted || status == engine.StatusCanceled) {
		return FailureSummary{}, fmt.Errorf("%w: completed or canceled execution cannot have a failure signature", ErrInvalidRecord)
	}
	summary := FailureSummary{
		Availability:           input.Availability,
		Artifact:               singleArtifact(artifacts, ArtifactFailure),
		MinimizeReportArtifact: singleArtifact(artifacts, ArtifactMinimizeReport),
	}
	if input.Signature == nil {
		return summary, nil
	}
	signature := copySignature(*input.Signature)
	signature.OracleCodes = normalizeStrings(signature.OracleCodes)
	if !validEngineStatus(signature.Status) {
		return FailureSummary{}, fmt.Errorf("%w: failure signature has unknown status %q", ErrInvalidRecord, signature.Status)
	}
	digest, err := signatureDigest(signature)
	if err != nil {
		return FailureSummary{}, err
	}
	summary.Signature = &signature
	summary.SignatureDigest = digest
	return summary, nil
}

func normalizeOracleCodes(findings []oracle.Finding) []string {
	codes := make([]string, 0, len(findings))
	for _, finding := range findings {
		codes = append(codes, finding.Oracle+":"+finding.Code)
	}
	return normalizeStrings(codes)
}

func countEffects(trace core.Trace) int {
	total := 0
	for _, step := range trace.Steps {
		total += len(step.Effects)
	}
	return total
}

func copySignature(signature minimize.Signature) minimize.Signature {
	signature.OracleCodes = append([]string(nil), signature.OracleCodes...)
	return signature
}

func normalizeStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func replayable(record CompletedExecutionRecordV1) bool {
	return record.Replay.ConfigArtifact != nil &&
		record.Replay.TraceArtifact != nil &&
		record.Trace.Version == core.CurrentTraceVersion &&
		record.Trace.ExecutionID.Valid() &&
		record.Trace.Seed == record.Candidate.Seed &&
		validSHA256(record.Trace.Digest)
}

func validCandidateKind(kind experiment.CandidateKind) bool {
	switch kind {
	case experiment.CandidateInitial, experiment.CandidateMutation, experiment.CandidatePeriodicRandom:
		return true
	default:
		return false
	}
}

func validEngineStatus(status engine.Status) bool {
	switch status {
	case engine.StatusCompleted, engine.StatusCanceled, engine.StatusInvalidPlan,
		engine.StatusResolutionFailed, engine.StatusRuntimeFailed, engine.StatusMappingFailed,
		engine.StatusUnsupported, engine.StatusOracleFailed, engine.StatusPolicyFailed,
		engine.StatusModelFailed:
		return true
	default:
		return false
	}
}

func validTermination(reason engine.TerminationReason) bool {
	switch reason {
	case "", engine.TerminationPlanComplete, engine.TerminationPolicyComplete,
		engine.TerminationPlanActionBudget, engine.TerminationRuntimeBudget,
		engine.TerminationConsecutiveNoops, engine.TerminationModelBound:
		return true
	default:
		return false
	}
}
