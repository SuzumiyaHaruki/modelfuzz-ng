package executionrecord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func DecodeV1(reader io.Reader) (CompletedExecutionRecordV1, error) {
	if reader == nil {
		return CompletedExecutionRecordV1{}, fmt.Errorf("%w: reader is nil", ErrInvalidRecord)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return CompletedExecutionRecordV1{}, fmt.Errorf("%w: read JSON: %v", ErrInvalidRecord, err)
	}
	if !utf8.Valid(data) {
		return CompletedExecutionRecordV1{}, fmt.Errorf("%w: JSON is not valid UTF-8", ErrInvalidRecord)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record CompletedExecutionRecordV1
	if err := decoder.Decode(&record); err != nil {
		return CompletedExecutionRecordV1{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidRecord, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return CompletedExecutionRecordV1{}, fmt.Errorf("%w: trailing JSON value", ErrInvalidRecord)
		}
		return CompletedExecutionRecordV1{}, fmt.Errorf("%w: trailing JSON: %v", ErrInvalidRecord, err)
	}
	if err := validateRecord(record, true); err != nil {
		return CompletedExecutionRecordV1{}, err
	}
	return cloneRecord(record), nil
}

func validateRecord(record CompletedExecutionRecordV1, checkDigest bool) error {
	if record.SchemaID != SchemaIDV1 {
		return fmt.Errorf("%w: unsupported schema %q", ErrInvalidRecord, record.SchemaID)
	}
	if record.MajorVersion != MajorVersionV1 {
		return fmt.Errorf("%w: unsupported major version %d", ErrInvalidRecord, record.MajorVersion)
	}
	if record.Candidate.ID == "" || !validCandidateKind(record.Candidate.Kind) ||
		record.Candidate.Depth < 0 || record.Candidate.RunIndex < 0 {
		return fmt.Errorf("%w: invalid candidate identity", ErrInvalidRecord)
	}
	if record.Plan.ActionCount <= 0 || !validSHA256(record.Plan.Digest) {
		return fmt.Errorf("%w: invalid plan summary", ErrInvalidRecord)
	}
	if !validEngineStatus(record.Engine.Status) || !validEngineStatus(record.Experiment.Status) {
		return fmt.Errorf("%w: invalid outcome status", ErrInvalidRecord)
	}
	if !record.Experiment.Completed {
		return fmt.Errorf("%w: experiment outcome is not completed", ErrInvalidRecord)
	}
	if !validTermination(record.Engine.Termination) ||
		hasNegativeCount(
			record.Engine.ResolutionCount, record.Engine.ConcreteActionCount, record.Engine.EffectCount,
			record.Engine.TraceStepCount, record.Engine.ModelEventCount, record.Engine.ModelStateCount,
			record.Engine.OracleFindingCount,
		) {
		return fmt.Errorf("%w: invalid engine outcome", ErrInvalidRecord)
	}
	if record.Trace.StepCount != record.Engine.TraceStepCount ||
		record.Model.Executed != record.Engine.ModelExecuted ||
		record.Model.EventCount != record.Engine.ModelEventCount ||
		record.Model.StateCount != record.Engine.ModelStateCount ||
		record.Oracle.FindingCount != record.Engine.OracleFindingCount {
		return fmt.Errorf("%w: summary counts differ from engine outcome", ErrInvalidRecord)
	}
	if record.Trace.Version == 0 {
		if record.Trace.Digest != "" || record.Trace.StepCount != 0 {
			return fmt.Errorf("%w: absent trace has steps or a digest", ErrInvalidRecord)
		}
	} else if record.Trace.Version != core.CurrentTraceVersion || !validSHA256(record.Trace.Digest) {
		return fmt.Errorf("%w: invalid trace summary", ErrInvalidRecord)
	}
	if record.Model.StateCount == 0 {
		if record.Model.StatePathDigest != "" {
			return fmt.Errorf("%w: empty model state path has a digest", ErrInvalidRecord)
		}
	} else if !validSHA256(record.Model.StatePathDigest) {
		return fmt.Errorf("%w: invalid model state path digest", ErrInvalidRecord)
	}
	if !canonicalUniqueStrings(record.Oracle.Codes) {
		return fmt.Errorf("%w: oracle codes are not canonical", ErrInvalidRecord)
	}
	for _, code := range record.Oracle.Codes {
		parts := strings.SplitN(code, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("%w: invalid oracle code %q", ErrInvalidRecord, code)
		}
	}
	if !validSHA256(record.ConfigurationFingerprint) {
		return fmt.Errorf("%w: invalid configuration fingerprint", ErrInvalidRecord)
	}
	if record.Corpus.Retained && record.Corpus.ID == "" ||
		!record.Corpus.Retained && record.Corpus.ID != "" {
		return fmt.Errorf("%w: invalid corpus outcome", ErrInvalidRecord)
	}
	if err := validateFailureSummary(record.Failure); err != nil {
		return err
	}
	if err := validateCanonicalArtifacts(record.Artifacts); err != nil {
		return err
	}
	if !sameArtifact(record.Plan.Artifact, singleArtifact(record.Artifacts, ArtifactPlan)) ||
		!sameArtifact(record.Trace.Artifact, singleArtifact(record.Artifacts, ArtifactTrace)) ||
		!sameArtifact(record.Model.EventsArtifact, singleArtifact(record.Artifacts, ArtifactModelEvents)) ||
		!sameArtifact(record.Model.StatesArtifact, singleArtifact(record.Artifacts, ArtifactModelStates)) ||
		!sameArtifact(record.Oracle.Artifact, singleArtifact(record.Artifacts, ArtifactOracleFindings)) ||
		!sameArtifact(record.Failure.Artifact, singleArtifact(record.Artifacts, ArtifactFailure)) ||
		!sameArtifact(record.Failure.MinimizeReportArtifact, singleArtifact(record.Artifacts, ArtifactMinimizeReport)) {
		return fmt.Errorf("%w: summary artifact reference differs from canonical artifacts", ErrInvalidRecord)
	}
	wantReplay := ReplaySummary{
		TraceExecutionID: record.Trace.ExecutionID,
		TraceSeed:        record.Trace.Seed,
		ConfigArtifact:   singleArtifact(record.Artifacts, ArtifactConfig),
		TraceArtifact:    singleArtifact(record.Artifacts, ArtifactTrace),
	}
	copy := record
	copy.Replay = wantReplay
	wantReplay.Replayable = replayable(copy)
	if !reflect.DeepEqual(record.Replay, wantReplay) {
		return fmt.Errorf("%w: replay summary is not derived from record evidence", ErrInvalidRecord)
	}
	if checkDigest {
		if !validSHA256(record.RecordDigest) {
			return fmt.Errorf("%w: invalid record digest", ErrInvalidRecord)
		}
		expected, err := recordDigest(record)
		if err != nil {
			return err
		}
		if record.RecordDigest != expected {
			return fmt.Errorf("%w: record digest mismatch", ErrInvalidRecord)
		}
	}
	return nil
}

func validateFailureSummary(summary FailureSummary) error {
	if !summary.Availability.valid() {
		return fmt.Errorf("%w: invalid failure signature availability", ErrInvalidRecord)
	}
	if summary.Availability != FailureSignatureAvailable {
		if summary.Signature != nil || summary.SignatureDigest != "" {
			return fmt.Errorf("%w: unavailable failure signature has data", ErrInvalidRecord)
		}
		return nil
	}
	if summary.Signature == nil || !validSHA256(summary.SignatureDigest) ||
		!validEngineStatus(summary.Signature.Status) ||
		!canonicalUniqueStrings(summary.Signature.OracleCodes) {
		return fmt.Errorf("%w: invalid available failure signature", ErrInvalidRecord)
	}
	expected, err := signatureDigest(*summary.Signature)
	if err != nil {
		return err
	}
	if expected != summary.SignatureDigest {
		return fmt.Errorf("%w: failure signature digest mismatch", ErrInvalidRecord)
	}
	return nil
}

func validateCanonicalArtifacts(references []ArtifactReference) error {
	for index, reference := range references {
		if err := reference.Validate(); err != nil {
			return err
		}
		if index == 0 {
			continue
		}
		previous := references[index-1]
		if previous.Kind > reference.Kind ||
			previous.Kind == reference.Kind && previous.Path >= reference.Path {
			return fmt.Errorf("%w: artifacts are not canonical", ErrInvalidRecord)
		}
	}
	return nil
}

func canonicalUniqueStrings(values []string) bool {
	return sort.StringsAreSorted(values) && len(normalizeStrings(values)) == len(values)
}

func sameArtifact(left, right *ArtifactReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func hasNegativeCount(values ...int) bool {
	for _, value := range values {
		if value < 0 {
			return true
		}
	}
	return false
}

func cloneRecord(record CompletedExecutionRecordV1) CompletedExecutionRecordV1 {
	record.Artifacts = append([]ArtifactReference(nil), record.Artifacts...)
	record.Oracle.Codes = append([]string(nil), record.Oracle.Codes...)
	record.Plan.Artifact = cloneArtifact(record.Plan.Artifact)
	record.Trace.Artifact = cloneArtifact(record.Trace.Artifact)
	record.Model.EventsArtifact = cloneArtifact(record.Model.EventsArtifact)
	record.Model.StatesArtifact = cloneArtifact(record.Model.StatesArtifact)
	record.Oracle.Artifact = cloneArtifact(record.Oracle.Artifact)
	record.Failure.Artifact = cloneArtifact(record.Failure.Artifact)
	record.Failure.MinimizeReportArtifact = cloneArtifact(record.Failure.MinimizeReportArtifact)
	record.Replay.ConfigArtifact = cloneArtifact(record.Replay.ConfigArtifact)
	record.Replay.TraceArtifact = cloneArtifact(record.Replay.TraceArtifact)
	if record.Failure.Signature != nil {
		signature := copySignature(*record.Failure.Signature)
		record.Failure.Signature = &signature
	}
	return record
}

func cloneArtifact(reference *ArtifactReference) *ArtifactReference {
	if reference == nil {
		return nil
	}
	copy := *reference
	return &copy
}
