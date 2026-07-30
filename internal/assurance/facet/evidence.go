package facet

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

type OccurrenceKind string

const (
	OccurrenceExplicitInitial    OccurrenceKind = "explicit_initial_state"
	OccurrenceTraceInitialBefore OccurrenceKind = "trace_initial_before"
	OccurrenceTraceStepAfter     OccurrenceKind = "trace_step_after"
	OccurrenceTransitionEffect   OccurrenceKind = "transition_effect"
)

func (kind OccurrenceKind) Valid() bool {
	switch kind {
	case OccurrenceExplicitInitial, OccurrenceTraceInitialBefore,
		OccurrenceTraceStepAfter, OccurrenceTransitionEffect:
		return true
	default:
		return false
	}
}

type Occurrence struct {
	Kind        OccurrenceKind `json:"kind"`
	StepIndex   *int           `json:"step_index,omitempty"`
	EffectIndex *int           `json:"effect_index,omitempty"`
}

func (occurrence Occurrence) Validate() error {
	if !occurrence.Kind.Valid() {
		return fmt.Errorf("invalid occurrence kind %q", occurrence.Kind)
	}
	if occurrence.StepIndex != nil && *occurrence.StepIndex < 0 ||
		occurrence.EffectIndex != nil && *occurrence.EffectIndex < 0 {
		return fmt.Errorf("occurrence index must not be negative")
	}
	switch occurrence.Kind {
	case OccurrenceExplicitInitial:
		if occurrence.StepIndex != nil || occurrence.EffectIndex != nil {
			return fmt.Errorf("explicit initial occurrence must not have indices")
		}
	case OccurrenceTraceInitialBefore, OccurrenceTraceStepAfter:
		if occurrence.StepIndex == nil || occurrence.EffectIndex != nil {
			return fmt.Errorf("state trace occurrence requires only a step index")
		}
	case OccurrenceTransitionEffect:
		if occurrence.StepIndex == nil || occurrence.EffectIndex == nil {
			return fmt.Errorf("transition effect occurrence requires both indices")
		}
	}
	return nil
}

func (occurrence Occurrence) Copy() Occurrence {
	copy := occurrence
	if occurrence.StepIndex != nil {
		value := *occurrence.StepIndex
		copy.StepIndex = &value
	}
	if occurrence.EffectIndex != nil {
		value := *occurrence.EffectIndex
		copy.EffectIndex = &value
	}
	return copy
}

func ExplicitInitialOccurrence() Occurrence {
	return Occurrence{Kind: OccurrenceExplicitInitial}
}

func TraceInitialOccurrence(step int) Occurrence {
	return Occurrence{Kind: OccurrenceTraceInitialBefore, StepIndex: intPointer(step)}
}

func TraceStepAfterOccurrence(step int) Occurrence {
	return Occurrence{Kind: OccurrenceTraceStepAfter, StepIndex: intPointer(step)}
}

func TransitionEffectOccurrence(step, effect int) Occurrence {
	return Occurrence{
		Kind: OccurrenceTransitionEffect, StepIndex: intPointer(step), EffectIndex: intPointer(effect),
	}
}

func intPointer(value int) *int { return &value }

type ObservationV1 struct {
	Key         KeyV1      `json:"key"`
	KeyDigest   string     `json:"key_digest"`
	Occurrence  Occurrence `json:"occurrence"`
	Explanation string     `json:"explanation,omitempty"`
}

func (observation ObservationV1) Copy() ObservationV1 {
	copy := observation
	copy.Occurrence = observation.Occurrence.Copy()
	return copy
}

type EvaluationV1 struct {
	FacetID      string           `json:"facet_id"`
	FacetVersion uint32           `json:"facet_version"`
	Status       EvaluationStatus `json:"status"`
	Observations []ObservationV1  `json:"observations"`
	Detail       string           `json:"detail,omitempty"`
}

func (evaluation EvaluationV1) Copy() EvaluationV1 {
	copy := evaluation
	copy.Observations = make([]ObservationV1, len(evaluation.Observations))
	for index, observation := range evaluation.Observations {
		copy.Observations[index] = observation.Copy()
	}
	return copy
}

type EvaluationInputV1 struct {
	Record             executionrecord.CompletedExecutionRecordV1
	InitialObservation *core.Observation
	Trace              *core.Trace
	ModelEvents        []model.Event
	ModelStates        []model.State
}

type PreparedInputV1 struct {
	Record             executionrecord.CompletedExecutionRecordV1
	InitialObservation *core.Observation
	Trace              *core.Trace
}

func PrepareInputV1(input EvaluationInputV1) (PreparedInputV1, EvaluationStatus, string) {
	record := input.Record
	if record.SchemaID != executionrecord.SchemaIDV1 ||
		record.MajorVersion != executionrecord.MajorVersionV1 {
		return PreparedInputV1{}, StatusInvalidEvidence, "completed execution record schema is unsupported"
	}
	if !validDigest(record.RecordDigest) {
		return PreparedInputV1{}, StatusInvalidEvidence, "completed execution record digest is malformed"
	}
	if record.Trace.StepCount != record.Engine.TraceStepCount ||
		record.Model.EventCount != record.Engine.ModelEventCount ||
		record.Model.StateCount != record.Engine.ModelStateCount {
		return PreparedInputV1{}, StatusInvalidEvidence, "record summary counts are inconsistent"
	}
	prepared := PreparedInputV1{Record: record}
	if input.InitialObservation != nil {
		initial := input.InitialObservation.Copy()
		if err := initial.Validate(); err != nil {
			return PreparedInputV1{}, StatusInvalidEvidence, "initial observation is invalid: " + err.Error()
		}
		prepared.InitialObservation = &initial
	}
	if input.Trace != nil {
		trace := input.Trace.Copy()
		if err := trace.Validate(); err != nil {
			return PreparedInputV1{}, StatusInvalidEvidence, "trace is invalid: " + err.Error()
		}
		if trace.Version != record.Trace.Version ||
			trace.ExecutionID != record.Trace.ExecutionID ||
			trace.Seed != record.Trace.Seed ||
			len(trace.Steps) != record.Trace.StepCount ||
			countTraceEffects(trace) != record.Engine.EffectCount {
			return PreparedInputV1{}, StatusInvalidEvidence, "trace identity or counts differ from record"
		}
		prepared.Trace = &trace
	}
	if input.ModelEvents != nil {
		if len(input.ModelEvents) != record.Model.EventCount {
			return PreparedInputV1{}, StatusInvalidEvidence, "model event count differs from record"
		}
		for _, event := range input.ModelEvents {
			if err := event.Validate(); err != nil {
				return PreparedInputV1{}, StatusInvalidEvidence, "model event is invalid: " + err.Error()
			}
		}
	}
	if input.ModelStates != nil && len(input.ModelStates) != record.Model.StateCount {
		return PreparedInputV1{}, StatusInvalidEvidence, "model state count differs from record"
	}
	return prepared, StatusEvaluated, ""
}

func countTraceEffects(trace core.Trace) int {
	total := 0
	for _, step := range trace.Steps {
		total += len(step.Effects)
	}
	return total
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
