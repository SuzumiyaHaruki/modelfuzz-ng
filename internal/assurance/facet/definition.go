// Package facet defines the read-only, offline Facet v1 evaluation boundary.
package facet

import (
	"fmt"
	"sort"
	"strings"
)

const KeySchemaIDV1 = "modelfuzz-ng-facet-key-v1"

type Scope string

const (
	ScopeState      Scope = "state"
	ScopeTransition Scope = "transition"
)

func (scope Scope) Valid() bool {
	return scope == ScopeState || scope == ScopeTransition
}

type Grounding string

const (
	GroundingModel          Grounding = "model_grounded"
	GroundingImplementation Grounding = "implementation_grounded"
	GroundingCrossLayer     Grounding = "cross_layer"
)

func (grounding Grounding) Valid() bool {
	switch grounding {
	case GroundingModel, GroundingImplementation, GroundingCrossLayer:
		return true
	default:
		return false
	}
}

type EvaluationStatus string

const (
	StatusEvaluated            EvaluationStatus = "evaluated"
	StatusNotApplicable        EvaluationStatus = "not_applicable"
	StatusInsufficientEvidence EvaluationStatus = "insufficient_evidence"
	StatusInvalidEvidence      EvaluationStatus = "invalid_evidence"
)

func (status EvaluationStatus) Valid() bool {
	switch status {
	case StatusEvaluated, StatusNotApplicable, StatusInsufficientEvidence, StatusInvalidEvidence:
		return true
	default:
		return false
	}
}

type EvidenceRequirement string

const (
	EvidenceCompletedRecord     EvidenceRequirement = "completed_execution_record_v1"
	EvidenceRaftReplication     EvidenceRequirement = "raft_replication_boundaries"
	EvidenceRaftRoleTerm        EvidenceRequirement = "raft_role_term"
	EvidenceRaftSnapshotMarkers EvidenceRequirement = "raft_snapshot_markers"
	EvidenceStateNodeSnapshots  EvidenceRequirement = "state_node_snapshots"
	EvidenceTraceV1             EvidenceRequirement = "trace_v1"
	EvidenceInitialObservation  EvidenceRequirement = "initial_observation"
	EvidenceModelEventsOptional EvidenceRequirement = "model_events"
	EvidenceModelStatesOptional EvidenceRequirement = "model_states"
)

func (requirement EvidenceRequirement) Valid() bool {
	switch requirement {
	case EvidenceCompletedRecord, EvidenceRaftReplication, EvidenceRaftRoleTerm,
		EvidenceRaftSnapshotMarkers, EvidenceStateNodeSnapshots, EvidenceTraceV1,
		EvidenceInitialObservation, EvidenceModelEventsOptional, EvidenceModelStatesOptional:
		return true
	default:
		return false
	}
}

type ClassDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type InvarianceSet struct {
	NodeRenaming         bool `json:"node_renaming"`
	MessageIDRenaming    bool `json:"message_id_renaming"`
	UniformTermShift     bool `json:"uniform_term_shift"`
	UniformLogIndexShift bool `json:"uniform_log_index_shift"`
	ArtifactLayout       bool `json:"artifact_layout"`
	ExecutionIDSeed      bool `json:"execution_id_seed"`
	MapIteration         bool `json:"map_iteration"`
	UnrelatedDebugText   bool `json:"unrelated_debug_text"`
}

type DefinitionV1 struct {
	ID                      string                `json:"id"`
	Version                 uint32                `json:"version"`
	Name                    string                `json:"name"`
	Protocol                string                `json:"protocol"`
	Grounding               Grounding             `json:"grounding"`
	Scope                   Scope                 `json:"scope"`
	Rationale               string                `json:"rationale"`
	RelatedPropertyFamilies []string              `json:"related_property_families"`
	RequiredEvidence        []EvidenceRequirement `json:"required_evidence"`
	OptionalEvidence        []EvidenceRequirement `json:"optional_evidence"`
	Classes                 []ClassDefinition     `json:"classes"`
	CardinalityBound        int                   `json:"cardinality_bound"`
	Invariances             InvarianceSet         `json:"invariances"`
	ValidationStatus        string                `json:"validation_status"`
}

func (definition DefinitionV1) Validate() error {
	if !safeIdentifier(definition.ID, true) {
		return fmt.Errorf("facet definition has invalid namespaced ID %q", definition.ID)
	}
	if definition.Version == 0 || strings.TrimSpace(definition.Name) == "" ||
		strings.TrimSpace(definition.Protocol) == "" || strings.TrimSpace(definition.Rationale) == "" {
		return fmt.Errorf("facet definition %q has an empty required field", definition.ID)
	}
	if !definition.Grounding.Valid() || !definition.Scope.Valid() {
		return fmt.Errorf("facet definition %q has invalid grounding or scope", definition.ID)
	}
	if definition.ValidationStatus == "" {
		return fmt.Errorf("facet definition %q has empty validation status", definition.ID)
	}
	if len(definition.Classes) == 0 || definition.CardinalityBound < len(definition.Classes) {
		return fmt.Errorf("facet definition %q has invalid cardinality", definition.ID)
	}
	classIDs := make([]string, len(definition.Classes))
	for index, class := range definition.Classes {
		if !safeIdentifier(class.ID, false) || class.Name == "" || class.Description == "" {
			return fmt.Errorf("facet definition %q has invalid class at index %d", definition.ID, index)
		}
		classIDs[index] = class.ID
	}
	if !strictlySortedUnique(classIDs) {
		return fmt.Errorf("facet definition %q classes are not canonical", definition.ID)
	}
	if !canonicalRequirements(definition.RequiredEvidence) ||
		!canonicalRequirements(definition.OptionalEvidence) {
		return fmt.Errorf("facet definition %q evidence requirements are not canonical", definition.ID)
	}
	if !strictlySortedUnique(definition.RelatedPropertyFamilies) {
		return fmt.Errorf("facet definition %q property families are not canonical", definition.ID)
	}
	return nil
}

func (definition DefinitionV1) Copy() DefinitionV1 {
	copy := definition
	copy.RelatedPropertyFamilies = append([]string(nil), definition.RelatedPropertyFamilies...)
	copy.RequiredEvidence = append([]EvidenceRequirement(nil), definition.RequiredEvidence...)
	copy.OptionalEvidence = append([]EvidenceRequirement(nil), definition.OptionalEvidence...)
	copy.Classes = append([]ClassDefinition(nil), definition.Classes...)
	return copy
}

func canonicalRequirements(values []EvidenceRequirement) bool {
	text := make([]string, len(values))
	for index, value := range values {
		if !value.Valid() {
			return false
		}
		text[index] = string(value)
	}
	return strictlySortedUnique(text)
}

func strictlySortedUnique(values []string) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return sort.StringsAreSorted(values)
}

func safeIdentifier(value string, namespaced bool) bool {
	if value == "" || strings.ContainsAny(value, "/\\ \t\r\n") {
		return false
	}
	if namespaced && (!strings.Contains(value, ".") ||
		strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".")) {
		return false
	}
	for index, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' ||
			namespaced && character == '.'
		if !valid || index == 0 && character >= '0' && character <= '9' {
			return false
		}
	}
	return !strings.Contains(value, "..")
}
