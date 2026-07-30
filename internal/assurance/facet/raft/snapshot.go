package raft

import (
	"fmt"
	"math"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

var snapshotClassIDs = []string{
	"log_compacted",
	"snapshot_applied",
	"snapshot_created",
	"snapshot_delivered",
	"snapshot_fast_forwarded",
	"snapshot_rejected_or_stale",
	"snapshot_sent",
	"snapshot_status_failed",
	"snapshot_status_ignored",
	"snapshot_status_succeeded",
}

type snapshotEvaluator struct {
	definition facet.DefinitionV1
}

func NewSnapshotLifecycleEventV1() facet.Evaluator {
	definition := facet.DefinitionV1{
		ID: "raft.snapshot_lifecycle_event", Version: 1,
		Name: "Snapshot/storage lifecycle event", Protocol: "raft",
		Grounding: facet.GroundingCrossLayer, Scope: facet.ScopeTransition,
		Rationale: "Classify persisted snapshot and storage lifecycle markers without multi-step history.",
		RelatedPropertyFamilies: []string{
			"Log Compaction", "Recovery", "Snapshot Installation", "Snapshot Transport Status",
		},
		RequiredEvidence: []facet.EvidenceRequirement{
			facet.EvidenceCompletedRecord, facet.EvidenceRaftSnapshotMarkers, facet.EvidenceTraceV1,
		},
		OptionalEvidence: []facet.EvidenceRequirement{
			facet.EvidenceModelEventsOptional, facet.EvidenceModelStatesOptional,
		},
		Classes: classDefinitions(snapshotClassIDs), CardinalityBound: len(snapshotClassIDs),
		Invariances: allInvariances(), ValidationStatus: "stage2_preregistered",
	}
	if err := definition.Validate(); err != nil {
		panic(err)
	}
	return &snapshotEvaluator{definition: definition}
}

func (e *snapshotEvaluator) Definition() facet.DefinitionV1 {
	return e.definition.Copy()
}

func (e *snapshotEvaluator) Evaluate(input facet.EvaluationInputV1) (facet.EvaluationV1, error) {
	prepared, status, detail := facet.PrepareInputV1(input)
	if status != facet.StatusEvaluated {
		return terminal(e.definition, status, detail)
	}
	if prepared.Trace == nil {
		return terminal(e.definition, facet.StatusInsufficientEvidence, "transition trace was not provided")
	}
	if len(prepared.Trace.Steps) == 0 {
		return terminal(e.definition, facet.StatusNotApplicable, "trace contains no transitions")
	}
	observations := make([]facet.ObservationV1, 0)
	for stepIndex, step := range prepared.Trace.Steps {
		for effectIndex, effect := range step.Effects {
			if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
				continue
			}
			classID, explanation, recognized, valid, markerDetail := classifySnapshotMarker(*effect.ModelEvent)
			if !recognized {
				continue
			}
			if !valid {
				return terminal(e.definition, facet.StatusInvalidEvidence, markerDetail)
			}
			observation, err := facet.NewObservation(
				e.definition, classID,
				facet.TransitionEffectOccurrence(stepIndex, effectIndex), explanation,
			)
			if err != nil {
				return facet.EvaluationV1{}, err
			}
			observations = append(observations, observation)
		}
	}
	if len(observations) == 0 {
		return terminal(e.definition, facet.StatusNotApplicable, "no recognized snapshot marker")
	}
	return facet.NewEvaluation(e.definition, facet.StatusEvaluated, observations, "")
}

func classifySnapshotMarker(event core.ModelEvent) (classID, explanation string, recognized, valid bool, detail string) {
	switch event.Name {
	case "raft.snapshot_created", "raft.log_compacted", "raft.snapshot_sent",
		"raft.snapshot_delivered", "raft.snapshot_applied", "raft.snapshot_fast_forwarded",
		"raft.snapshot_rejected_or_stale":
		recognized = true
	case "raft.snapshot_status_reported":
		return classifySnapshotStatus(event)
	default:
		return "", "", false, true, ""
	}
	if !event.Node.Valid() {
		return "", "", true, false, "snapshot marker has invalid node"
	}
	index, indexState := uintField(event.Params, "index")
	term, termState := uintField(event.Params, "term")
	_, bytesState := uintField(event.Params, "snapshot_bytes")
	if indexState != fieldPresent || termState != fieldPresent || bytesState != fieldPresent || index == 0 {
		return "", "", true, false, "snapshot marker has invalid boundary"
	}
	_ = term
	switch event.Name {
	case "raft.snapshot_created":
		return "snapshot_created", event.Name, true, true, ""
	case "raft.log_compacted":
		compactIndex, compactState := uintField(event.Params, "compact_index")
		compacted, compactedState := uintField(event.Params, "compacted_entries")
		if compactState != fieldPresent || compactedState != fieldPresent ||
			compactIndex == 0 || compacted == 0 {
			return "", "", true, false, "log compacted marker has invalid bounds"
		}
		return "log_compacted", event.Name, true, true, ""
	case "raft.snapshot_sent":
		to, toState := uintField(event.Params, "to")
		match, matchState := uintField(event.Params, "match_index")
		next, nextState := uintField(event.Params, "next_index")
		pending, pendingState := uintField(event.Params, "pending_snapshot")
		progress, progressState := stringField(event.Params, "progress_state")
		if toState != fieldPresent || matchState != fieldPresent || nextState != fieldPresent ||
			pendingState != fieldPresent || progressState != fieldPresent ||
			!validNodeID(to) || core.NodeID(to) == event.Node ||
			index == math.MaxUint64 || pending != index || next != index+1 ||
			progress != "StateSnapshot" {
			return "", "", true, false, "snapshot sent marker has invalid progress"
		}
		_ = match
		return "snapshot_sent", event.Name, true, true, ""
	case "raft.snapshot_delivered":
		return "snapshot_delivered", event.Name, true, true, ""
	case "raft.snapshot_applied":
		return "snapshot_applied", event.Name, true, true, ""
	case "raft.snapshot_fast_forwarded":
		before, beforeState := uintField(event.Params, "commit_before")
		after, afterState := uintField(event.Params, "commit_after")
		if beforeState == fieldMissing && afterState == fieldMissing {
			return "snapshot_fast_forwarded", event.Name, true, true, ""
		}
		if beforeState != fieldPresent || afterState != fieldPresent || after < before {
			return "", "", true, false, "snapshot fast-forward marker has invalid commit boundary"
		}
		return "snapshot_fast_forwarded", event.Name, true, true, ""
	case "raft.snapshot_rejected_or_stale":
		explanation = event.Name
		if reason, exists := event.Params["reason"]; exists {
			text, ok := reason.(string)
			if !ok {
				return "", "", true, false, "snapshot rejection reason is not a string"
			}
			explanation += ": " + text
		}
		return "snapshot_rejected_or_stale", explanation, true, true, ""
	default:
		panic(fmt.Sprintf("unreachable snapshot marker %q", event.Name))
	}
}

func classifySnapshotStatus(event core.ModelEvent) (classID, explanation string, recognized, valid bool, detail string) {
	if !event.Node.Valid() {
		return "", "", true, false, "snapshot status marker has invalid reporter"
	}
	from, fromState := uintField(event.Params, "from")
	to, toState := uintField(event.Params, "to")
	handled, handledState := boolField(event.Params, "handled")
	reject, rejectState := boolField(event.Params, "reject")
	if fromState != fieldPresent || toState != fieldPresent ||
		handledState != fieldPresent || rejectState != fieldPresent ||
		!validNodeID(from) || !validNodeID(to) || from == to || core.NodeID(to) != event.Node {
		return "", "", true, false, "snapshot status marker has invalid endpoint or outcome"
	}
	switch {
	case !handled:
		return "snapshot_status_ignored", event.Name, true, true, ""
	case reject:
		return "snapshot_status_failed", event.Name, true, true, ""
	default:
		return "snapshot_status_succeeded", event.Name, true, true, ""
	}
}
