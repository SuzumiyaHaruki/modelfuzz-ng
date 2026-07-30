// Package executionrecord builds a compact, read-only index over an already
// completed candidate execution. It does not execute candidates or persist
// artifacts.
package executionrecord

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
)

const (
	SchemaIDV1            = "modelfuzz-ng-completed-execution-v1"
	MajorVersionV1 uint32 = 1
)

var ErrInvalidRecord = errors.New("invalid completed execution record")

type ArtifactKind string

const (
	ArtifactConfig         ArtifactKind = "config"
	ArtifactPlan           ArtifactKind = "plan"
	ArtifactTrace          ArtifactKind = "trace"
	ArtifactModelEvents    ArtifactKind = "model_events"
	ArtifactModelStates    ArtifactKind = "model_states"
	ArtifactOracleFindings ArtifactKind = "oracle_findings"
	ArtifactFailure        ArtifactKind = "failure"
	ArtifactResult         ArtifactKind = "result"
	ArtifactCandidate      ArtifactKind = "candidate"
	ArtifactRunSummary     ArtifactKind = "run_summary"
	ArtifactMinimizeReport ArtifactKind = "minimize_report"
)

func (kind ArtifactKind) valid() bool {
	switch kind {
	case ArtifactConfig, ArtifactPlan, ArtifactTrace, ArtifactModelEvents,
		ArtifactModelStates, ArtifactOracleFindings, ArtifactFailure,
		ArtifactResult, ArtifactCandidate, ArtifactRunSummary, ArtifactMinimizeReport:
		return true
	default:
		return false
	}
}

type ArtifactReference struct {
	Kind   ArtifactKind `json:"kind"`
	Path   string       `json:"path"`
	SHA256 string       `json:"sha256"`
}

func (reference ArtifactReference) Validate() error {
	if !reference.Kind.valid() {
		return fmt.Errorf("%w: unknown artifact kind %q", ErrInvalidRecord, reference.Kind)
	}
	if reference.Path == "" {
		return fmt.Errorf("%w: artifact path is empty", ErrInvalidRecord)
	}
	if strings.Contains(reference.Path, `\`) {
		return fmt.Errorf("%w: artifact path %q contains a backslash", ErrInvalidRecord, reference.Path)
	}
	if path.IsAbs(reference.Path) || strings.HasPrefix(reference.Path, "//") ||
		isWindowsDrivePath(reference.Path) {
		return fmt.Errorf("%w: artifact path %q is absolute", ErrInvalidRecord, reference.Path)
	}
	for _, component := range strings.Split(reference.Path, "/") {
		if component == ".." {
			return fmt.Errorf("%w: artifact path %q contains parent traversal", ErrInvalidRecord, reference.Path)
		}
	}
	if cleaned := path.Clean(reference.Path); cleaned == "." || cleaned != reference.Path {
		return fmt.Errorf("%w: artifact path %q is not canonical", ErrInvalidRecord, reference.Path)
	}
	if !validSHA256(reference.SHA256) {
		return fmt.Errorf("%w: artifact %s has invalid SHA-256", ErrInvalidRecord, reference.Path)
	}
	return nil
}

type FailureSignatureAvailability string

const (
	FailureSignatureNotApplicable FailureSignatureAvailability = "not_applicable"
	FailureSignatureUnavailable   FailureSignatureAvailability = "unavailable"
	FailureSignatureAvailable     FailureSignatureAvailability = "available"
)

func (availability FailureSignatureAvailability) valid() bool {
	switch availability {
	case FailureSignatureNotApplicable, FailureSignatureUnavailable, FailureSignatureAvailable:
		return true
	default:
		return false
	}
}

type FailureSignatureInput struct {
	Availability FailureSignatureAvailability
	Signature    *minimize.Signature
}

type BuildInput struct {
	Completion               experiment.Completion
	ConfigurationFingerprint string
	Artifacts                []ArtifactReference
	FailureSignature         FailureSignatureInput
}

type CompletedExecutionRecordV1 struct {
	SchemaID                 string              `json:"schema_id"`
	MajorVersion             uint32              `json:"major_version"`
	RecordDigest             string              `json:"record_digest"`
	Candidate                CandidateIdentity   `json:"candidate"`
	Plan                     PlanSummary         `json:"plan"`
	Engine                   EngineOutcome       `json:"engine_outcome"`
	Experiment               ExperimentOutcome   `json:"experiment_outcome"`
	Trace                    TraceSummary        `json:"trace"`
	Model                    ModelSummary        `json:"model"`
	Oracle                   OracleSummary       `json:"oracle"`
	Failure                  FailureSummary      `json:"failure"`
	Corpus                   CorpusOutcome       `json:"corpus"`
	Replay                   ReplaySummary       `json:"replay"`
	ConfigurationFingerprint string              `json:"configuration_fingerprint"`
	Artifacts                []ArtifactReference `json:"artifacts"`
}

type CandidateIdentity struct {
	ID       string                   `json:"id"`
	Kind     experiment.CandidateKind `json:"kind"`
	ParentID string                   `json:"parent_id,omitempty"`
	Source   string                   `json:"source"`
	Depth    int                      `json:"depth"`
	RunIndex int                      `json:"run_index"`
	Seed     int64                    `json:"seed"`
}

type PlanSummary struct {
	Digest      string             `json:"digest"`
	ActionCount int                `json:"action_count"`
	Artifact    *ArtifactReference `json:"artifact,omitempty"`
}

type EngineOutcome struct {
	Status              engine.Status            `json:"status"`
	DebugError          string                   `json:"debug_error,omitempty"`
	ModelExecuted       bool                     `json:"model_executed"`
	BudgetExhausted     bool                     `json:"budget_exhausted"`
	Termination         engine.TerminationReason `json:"termination,omitempty"`
	TerminationCode     string                   `json:"termination_code,omitempty"`
	TerminationDetail   string                   `json:"termination_detail,omitempty"`
	ResolutionCount     int                      `json:"resolution_count"`
	ConcreteActionCount int                      `json:"concrete_action_count"`
	EffectCount         int                      `json:"effect_count"`
	TraceStepCount      int                      `json:"trace_step_count"`
	ModelEventCount     int                      `json:"model_event_count"`
	ModelStateCount     int                      `json:"model_state_count"`
	OracleFindingCount  int                      `json:"oracle_finding_count"`
}

type ExperimentOutcome struct {
	Completed  bool          `json:"completed"`
	Status     engine.Status `json:"status"`
	Succeeded  bool          `json:"succeeded"`
	DebugError string        `json:"debug_error,omitempty"`
}

type TraceSummary struct {
	Digest      string             `json:"digest,omitempty"`
	StepCount   int                `json:"step_count"`
	Version     uint32             `json:"version"`
	ExecutionID core.ExecutionID   `json:"execution_id,omitempty"`
	Seed        int64              `json:"seed"`
	Artifact    *ArtifactReference `json:"artifact,omitempty"`
}

type ModelSummary struct {
	Executed        bool               `json:"executed"`
	EventCount      int                `json:"event_count"`
	StateCount      int                `json:"state_count"`
	StatePathDigest string             `json:"state_path_digest,omitempty"`
	EventsArtifact  *ArtifactReference `json:"events_artifact,omitempty"`
	StatesArtifact  *ArtifactReference `json:"states_artifact,omitempty"`
}

type OracleSummary struct {
	FindingCount int                `json:"finding_count"`
	Codes        []string           `json:"codes"`
	Artifact     *ArtifactReference `json:"artifact,omitempty"`
}

type FailureSummary struct {
	Availability           FailureSignatureAvailability `json:"availability"`
	Signature              *minimize.Signature          `json:"signature,omitempty"`
	SignatureDigest        string                       `json:"signature_digest,omitempty"`
	Artifact               *ArtifactReference           `json:"artifact,omitempty"`
	MinimizeReportArtifact *ArtifactReference           `json:"minimize_report_artifact,omitempty"`
}

type CorpusOutcome struct {
	Retained  bool   `json:"retained"`
	ID        string `json:"id,omitempty"`
	Admission string `json:"admission,omitempty"`
}

type ReplaySummary struct {
	Replayable       bool               `json:"replayable"`
	TraceExecutionID core.ExecutionID   `json:"trace_execution_id,omitempty"`
	TraceSeed        int64              `json:"trace_seed"`
	ConfigArtifact   *ArtifactReference `json:"config_artifact,omitempty"`
	TraceArtifact    *ArtifactReference `json:"trace_artifact,omitempty"`
}

func normalizeArtifacts(references []ArtifactReference) ([]ArtifactReference, error) {
	result := append([]ArtifactReference(nil), references...)
	for _, reference := range result {
		if err := reference.Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Path < result[j].Path
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].Kind == result[index].Kind && result[index-1].Path == result[index].Path {
			return nil, fmt.Errorf("%w: duplicate artifact %s %q", ErrInvalidRecord, result[index].Kind, result[index].Path)
		}
	}
	return result, nil
}

func singleArtifact(references []ArtifactReference, kind ArtifactKind) *ArtifactReference {
	var found *ArtifactReference
	for index := range references {
		if references[index].Kind != kind {
			continue
		}
		if found != nil {
			return nil
		}
		copy := references[index]
		found = &copy
	}
	return found
}

func isWindowsDrivePath(value string) bool {
	return len(value) >= 2 &&
		((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':'
}
