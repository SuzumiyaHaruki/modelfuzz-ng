package raft

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

type fieldState uint8

const (
	fieldPresent fieldState = iota
	fieldMissing
	fieldInvalid
)

func uintField(values map[string]any, name string) (uint64, fieldState) {
	value, exists := values[name]
	if !exists {
		return 0, fieldMissing
	}
	result, ok := nonNegativeInteger(value)
	if !ok {
		return 0, fieldInvalid
	}
	return result, fieldPresent
}

func stringField(values map[string]any, name string) (string, fieldState) {
	value, exists := values[name]
	if !exists {
		return "", fieldMissing
	}
	result, ok := value.(string)
	if !ok {
		return "", fieldInvalid
	}
	return result, fieldPresent
}

func boolField(values map[string]any, name string) (bool, fieldState) {
	value, exists := values[name]
	if !exists {
		return false, fieldMissing
	}
	result, ok := value.(bool)
	if !ok {
		return false, fieldInvalid
	}
	return result, fieldPresent
}

func nonNegativeInteger(value any) (uint64, bool) {
	switch value := value.(type) {
	case uint:
		return uint64(value), true
	case uint8:
		return uint64(value), true
	case uint16:
		return uint64(value), true
	case uint32:
		return uint64(value), true
	case uint64:
		return value, true
	case int:
		if value >= 0 {
			return uint64(value), true
		}
	case int8:
		if value >= 0 {
			return uint64(value), true
		}
	case int16:
		if value >= 0 {
			return uint64(value), true
		}
	case int32:
		if value >= 0 {
			return uint64(value), true
		}
	case int64:
		if value >= 0 {
			return uint64(value), true
		}
	case float32:
		return exactFloat64(float64(value))
	case float64:
		return exactFloat64(value)
	case json.Number:
		result, err := strconv.ParseUint(value.String(), 10, 64)
		return result, err == nil
	}
	return 0, false
}

func exactFloat64(value float64) (uint64, bool) {
	const exclusiveUint64Limit = 18446744073709551616.0
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 ||
		value >= exclusiveUint64Limit || math.Trunc(value) != value {
		return 0, false
	}
	return uint64(value), true
}

func terminal(
	definition facet.DefinitionV1,
	status facet.EvaluationStatus,
	detail string,
) (facet.EvaluationV1, error) {
	return facet.NewEvaluation(definition, status, nil, detail)
}

func issueStatus(state fieldState, field string) (facet.EvaluationStatus, string) {
	switch state {
	case fieldMissing:
		return facet.StatusInsufficientEvidence, "required field " + field + " is missing"
	case fieldInvalid:
		return facet.StatusInvalidEvidence, "field " + field + " has an invalid type or value"
	default:
		return facet.StatusEvaluated, ""
	}
}

func validNodeID(value uint64) bool {
	return core.NodeID(value).Valid()
}

func classDefinitions(ids []string) []facet.ClassDefinition {
	result := make([]facet.ClassDefinition, len(ids))
	for index, id := range ids {
		result[index] = facet.ClassDefinition{
			ID: id, Name: id, Description: fmt.Sprintf("Frozen Raft Facet v1 class %s.", id),
		}
	}
	return result
}

func allInvariances() facet.InvarianceSet {
	return facet.InvarianceSet{
		NodeRenaming: true, MessageIDRenaming: true, UniformTermShift: true,
		UniformLogIndexShift: true, ArtifactLayout: true, ExecutionIDSeed: true,
		MapIteration: true, UnrelatedDebugText: true,
	}
}
