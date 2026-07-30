package executionrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
)

type recordDigestPayload struct {
	SchemaID                 string                  `json:"schema_id"`
	MajorVersion             uint32                  `json:"major_version"`
	Candidate                CandidateIdentity       `json:"candidate"`
	Plan                     planDigestPayload       `json:"plan"`
	Engine                   engineDigestPayload     `json:"engine_outcome"`
	Experiment               experimentDigestPayload `json:"experiment_outcome"`
	Trace                    traceDigestPayload      `json:"trace"`
	Model                    modelDigestPayload      `json:"model"`
	OracleCodes              []string                `json:"oracle_codes"`
	Failure                  failureDigestPayload    `json:"failure"`
	ConfigurationFingerprint string                  `json:"configuration_fingerprint"`
	Corpus                   CorpusOutcome           `json:"corpus"`
}

type planDigestPayload struct {
	Digest      string `json:"digest"`
	ActionCount int    `json:"action_count"`
}

type engineDigestPayload struct {
	Status              string `json:"status"`
	ModelExecuted       bool   `json:"model_executed"`
	BudgetExhausted     bool   `json:"budget_exhausted"`
	Termination         string `json:"termination"`
	TerminationCode     string `json:"termination_code"`
	ResolutionCount     int    `json:"resolution_count"`
	ConcreteActionCount int    `json:"concrete_action_count"`
	EffectCount         int    `json:"effect_count"`
	TraceStepCount      int    `json:"trace_step_count"`
	ModelEventCount     int    `json:"model_event_count"`
	ModelStateCount     int    `json:"model_state_count"`
	OracleFindingCount  int    `json:"oracle_finding_count"`
}

type experimentDigestPayload struct {
	Completed bool   `json:"completed"`
	Status    string `json:"status"`
	Succeeded bool   `json:"succeeded"`
}

type traceDigestPayload struct {
	Digest    string `json:"digest"`
	Version   uint32 `json:"version"`
	StepCount int    `json:"step_count"`
}

type modelDigestPayload struct {
	Executed        bool   `json:"executed"`
	EventCount      int    `json:"event_count"`
	StateCount      int    `json:"state_count"`
	StatePathDigest string `json:"state_path_digest"`
}

type failureDigestPayload struct {
	Availability    FailureSignatureAvailability `json:"availability"`
	SignatureDigest string                       `json:"signature_digest"`
}

func recordDigest(record CompletedExecutionRecordV1) (string, error) {
	payload := recordDigestPayload{
		SchemaID: record.SchemaID, MajorVersion: record.MajorVersion,
		Candidate: record.Candidate,
		Plan:      planDigestPayload{Digest: record.Plan.Digest, ActionCount: record.Plan.ActionCount},
		Engine: engineDigestPayload{
			Status: string(record.Engine.Status), ModelExecuted: record.Engine.ModelExecuted,
			BudgetExhausted: record.Engine.BudgetExhausted, Termination: string(record.Engine.Termination),
			TerminationCode: record.Engine.TerminationCode, ResolutionCount: record.Engine.ResolutionCount,
			ConcreteActionCount: record.Engine.ConcreteActionCount, EffectCount: record.Engine.EffectCount,
			TraceStepCount: record.Engine.TraceStepCount, ModelEventCount: record.Engine.ModelEventCount,
			ModelStateCount: record.Engine.ModelStateCount, OracleFindingCount: record.Engine.OracleFindingCount,
		},
		Experiment: experimentDigestPayload{
			Completed: record.Experiment.Completed, Status: string(record.Experiment.Status),
			Succeeded: record.Experiment.Succeeded,
		},
		Trace: traceDigestPayload{
			Digest: record.Trace.Digest, Version: record.Trace.Version, StepCount: record.Trace.StepCount,
		},
		Model: modelDigestPayload{
			Executed: record.Model.Executed, EventCount: record.Model.EventCount,
			StateCount: record.Model.StateCount, StatePathDigest: record.Model.StatePathDigest,
		},
		OracleCodes: append([]string(nil), record.Oracle.Codes...),
		Failure: failureDigestPayload{
			Availability: record.Failure.Availability, SignatureDigest: record.Failure.SignatureDigest,
		},
		ConfigurationFingerprint: record.ConfigurationFingerprint,
		Corpus:                   record.Corpus,
	}
	return digestJSON(payload)
}

func signatureDigest(signature minimize.Signature) (string, error) {
	return digestJSON(signature)
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: encode digest payload: %v", ErrInvalidRecord, err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
