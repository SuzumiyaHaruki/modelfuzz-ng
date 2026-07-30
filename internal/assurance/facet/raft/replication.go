package raft

import (
	"reflect"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

var replicationClassIDs = []string{
	"log_aligned_commit_aligned_applied_aligned",
	"log_aligned_commit_aligned_applied_diverged",
	"log_aligned_commit_diverged_applied_aligned",
	"log_aligned_commit_diverged_applied_diverged",
	"log_diverged_commit_aligned_applied_aligned",
	"log_diverged_commit_aligned_applied_diverged",
	"log_diverged_commit_diverged_applied_aligned",
	"log_diverged_commit_diverged_applied_diverged",
}

type replicationEvaluator struct {
	definition facet.DefinitionV1
}

type replicationNode struct {
	ID      core.NodeID
	Epoch   core.NodeEpoch
	Status  core.NodeStatus
	Last    uint64
	Commit  uint64
	Applied uint64
}

func NewReplicationAlignmentShapeV1() facet.Evaluator {
	definition := facet.DefinitionV1{
		ID: "raft.replication_alignment_shape", Version: 1,
		Name: "Replication commit/applied/log alignment shape", Protocol: "raft",
		Grounding: facet.GroundingImplementation, Scope: facet.ScopeState,
		Rationale:               "Classify cross-node log, commit, and applied alignment without absolute indices.",
		RelatedPropertyFamilies: []string{"Commit/Apply Progress", "Log Matching", "State Machine Safety"},
		RequiredEvidence: []facet.EvidenceRequirement{
			facet.EvidenceCompletedRecord, facet.EvidenceRaftReplication, facet.EvidenceStateNodeSnapshots,
		},
		OptionalEvidence: []facet.EvidenceRequirement{
			facet.EvidenceInitialObservation, facet.EvidenceModelEventsOptional,
			facet.EvidenceModelStatesOptional, facet.EvidenceTraceV1,
		},
		Classes: classDefinitions(replicationClassIDs), CardinalityBound: len(replicationClassIDs),
		Invariances: allInvariances(), ValidationStatus: "stage2_preregistered",
	}
	if err := definition.Validate(); err != nil {
		panic(err)
	}
	return &replicationEvaluator{definition: definition}
}

func (e *replicationEvaluator) Definition() facet.DefinitionV1 {
	return e.definition.Copy()
}

func (e *replicationEvaluator) Evaluate(input facet.EvaluationInputV1) (facet.EvaluationV1, error) {
	prepared, status, detail := facet.PrepareInputV1(input)
	if status != facet.StatusEvaluated {
		return terminal(e.definition, status, detail)
	}
	if prepared.Trace == nil {
		if prepared.Record.Trace.StepCount > 0 || prepared.InitialObservation == nil {
			return terminal(e.definition, facet.StatusInsufficientEvidence, "complete state evidence is unavailable")
		}
		return e.evaluateStates([]replicationStateInput{{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		}})
	}
	trace := prepared.Trace
	if len(trace.Steps) == 0 {
		if prepared.InitialObservation == nil {
			return terminal(e.definition, facet.StatusInsufficientEvidence, "empty trace has no initial state")
		}
		return e.evaluateStates([]replicationStateInput{{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		}})
	}
	states := make([]replicationStateInput, 0, len(trace.Steps)+1)
	firstBefore, status, detail := projectReplication(trace.Steps[0].NodesBefore)
	if status != facet.StatusEvaluated {
		return terminal(e.definition, status, detail)
	}
	if prepared.InitialObservation != nil {
		explicit, initialStatus, initialDetail := projectReplication(prepared.InitialObservation.Nodes)
		if initialStatus != facet.StatusEvaluated {
			return terminal(e.definition, initialStatus, initialDetail)
		}
		if !reflect.DeepEqual(explicit, firstBefore) {
			return terminal(e.definition, facet.StatusInvalidEvidence, "initial replication projection differs from trace")
		}
		states = append(states, replicationStateInput{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		})
	} else {
		states = append(states, replicationStateInput{
			Nodes: trace.Steps[0].NodesBefore, Occurrence: facet.TraceInitialOccurrence(0),
		})
	}
	for index, step := range trace.Steps {
		if index > 0 {
			previous, previousStatus, previousDetail := projectReplication(trace.Steps[index-1].NodesAfter)
			current, currentStatus, currentDetail := projectReplication(step.NodesBefore)
			if previousStatus != facet.StatusEvaluated {
				return terminal(e.definition, previousStatus, previousDetail)
			}
			if currentStatus != facet.StatusEvaluated {
				return terminal(e.definition, currentStatus, currentDetail)
			}
			if !reflect.DeepEqual(previous, current) {
				return terminal(e.definition, facet.StatusInvalidEvidence, "adjacent replication projections are discontinuous")
			}
		}
		states = append(states, replicationStateInput{
			Nodes: step.NodesAfter, Occurrence: facet.TraceStepAfterOccurrence(index),
		})
	}
	return e.evaluateStates(states)
}

type replicationStateInput struct {
	Nodes      []core.NodeObservation
	Occurrence facet.Occurrence
}

func (e *replicationEvaluator) evaluateStates(states []replicationStateInput) (facet.EvaluationV1, error) {
	observations := make([]facet.ObservationV1, 0, len(states))
	for _, state := range states {
		projected, status, detail := projectReplication(state.Nodes)
		if status != facet.StatusEvaluated {
			return terminal(e.definition, status, detail)
		}
		classID := replicationClass(projected)
		observation, err := facet.NewObservation(e.definition, classID, state.Occurrence, classID)
		if err != nil {
			return facet.EvaluationV1{}, err
		}
		observations = append(observations, observation)
	}
	return facet.NewEvaluation(e.definition, facet.StatusEvaluated, observations, "")
}

func projectReplication(nodes []core.NodeObservation) ([]replicationNode, facet.EvaluationStatus, string) {
	if len(nodes) == 0 {
		return nil, facet.StatusInvalidEvidence, "replication state has no nodes"
	}
	result := make([]replicationNode, len(nodes))
	seen := make(map[core.NodeID]struct{}, len(nodes))
	for index, node := range nodes {
		if _, duplicate := seen[node.ID]; duplicate {
			return nil, facet.StatusInvalidEvidence, "replication state contains duplicate node ID"
		}
		seen[node.ID] = struct{}{}
		last, lastState := uintField(node.Semantic, "last_index")
		if lastState != fieldPresent {
			status, detail := issueStatus(lastState, "last_index")
			return nil, status, detail
		}
		commit, commitState := uintField(node.Semantic, "commit")
		if commitState != fieldPresent {
			status, detail := issueStatus(commitState, "commit")
			return nil, status, detail
		}
		applied, appliedState := uintField(node.Semantic, "applied")
		if appliedState != fieldPresent {
			status, detail := issueStatus(appliedState, "applied")
			return nil, status, detail
		}
		if applied > commit || commit > last {
			return nil, facet.StatusInvalidEvidence, "replication boundary violates applied <= commit <= last_index"
		}
		result[index] = replicationNode{
			ID: node.ID, Epoch: node.Epoch, Status: node.Status,
			Last: last, Commit: commit, Applied: applied,
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, facet.StatusEvaluated, ""
}

func replicationClass(nodes []replicationNode) string {
	logAligned, commitAligned, appliedAligned := true, true, true
	for index := 1; index < len(nodes); index++ {
		logAligned = logAligned && nodes[index].Last == nodes[0].Last
		commitAligned = commitAligned && nodes[index].Commit == nodes[0].Commit
		appliedAligned = appliedAligned && nodes[index].Applied == nodes[0].Applied
	}
	logPart, commitPart, appliedPart := "log_diverged", "commit_diverged", "applied_diverged"
	if logAligned {
		logPart = "log_aligned"
	}
	if commitAligned {
		commitPart = "commit_aligned"
	}
	if appliedAligned {
		appliedPart = "applied_aligned"
	}
	return logPart + "_" + commitPart + "_" + appliedPart
}
