package raft

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

var electionClassIDs = []string{
	"leaders_multiple_candidates_none_terms_split",
	"leaders_multiple_candidates_none_terms_uniform",
	"leaders_multiple_candidates_some_terms_split",
	"leaders_multiple_candidates_some_terms_uniform",
	"leaders_none_candidates_none_terms_split",
	"leaders_none_candidates_none_terms_uniform",
	"leaders_none_candidates_some_terms_split",
	"leaders_none_candidates_some_terms_uniform",
	"leaders_one_candidates_none_terms_split",
	"leaders_one_candidates_none_terms_uniform",
	"leaders_one_candidates_some_terms_split",
	"leaders_one_candidates_some_terms_uniform",
	"no_running_nodes",
}

type electionEvaluator struct {
	definition facet.DefinitionV1
}

type electionNode struct {
	ID     core.NodeID
	Epoch  core.NodeEpoch
	Status core.NodeStatus
	Role   string
	Term   uint64
}

func NewElectionRoleTermShapeV1() facet.Evaluator {
	definition := facet.DefinitionV1{
		ID: "raft.election_role_term_shape", Version: 1,
		Name: "Election role/term population shape", Protocol: "raft",
		Grounding: facet.GroundingImplementation, Scope: facet.ScopeState,
		Rationale:               "Classify running role population and term agreement without node or absolute term identity.",
		RelatedPropertyFamilies: []string{"Election Safety", "Leader Election", "Term monotonicity"},
		RequiredEvidence: []facet.EvidenceRequirement{
			facet.EvidenceCompletedRecord, facet.EvidenceRaftRoleTerm, facet.EvidenceStateNodeSnapshots,
		},
		OptionalEvidence: []facet.EvidenceRequirement{
			facet.EvidenceInitialObservation, facet.EvidenceModelEventsOptional,
			facet.EvidenceModelStatesOptional, facet.EvidenceTraceV1,
		},
		Classes: classDefinitions(electionClassIDs), CardinalityBound: len(electionClassIDs),
		Invariances: allInvariances(), ValidationStatus: "stage2_preregistered",
	}
	if err := definition.Validate(); err != nil {
		panic(err)
	}
	return &electionEvaluator{definition: definition}
}

func (e *electionEvaluator) Definition() facet.DefinitionV1 {
	return e.definition.Copy()
}

func (e *electionEvaluator) Evaluate(input facet.EvaluationInputV1) (facet.EvaluationV1, error) {
	prepared, status, detail := facet.PrepareInputV1(input)
	if status != facet.StatusEvaluated {
		return terminal(e.definition, status, detail)
	}
	if prepared.Trace == nil {
		if prepared.Record.Trace.StepCount > 0 || prepared.InitialObservation == nil {
			return terminal(e.definition, facet.StatusInsufficientEvidence, "complete state evidence is unavailable")
		}
		return e.evaluateStates([]electionStateInput{{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		}})
	}
	trace := prepared.Trace
	if len(trace.Steps) == 0 {
		if prepared.InitialObservation == nil {
			return terminal(e.definition, facet.StatusInsufficientEvidence, "empty trace has no initial state")
		}
		return e.evaluateStates([]electionStateInput{{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		}})
	}
	states := make([]electionStateInput, 0, len(trace.Steps)+1)
	firstBefore, status, detail := projectElection(trace.Steps[0].NodesBefore)
	if status != facet.StatusEvaluated {
		return terminal(e.definition, status, detail)
	}
	if prepared.InitialObservation != nil {
		explicit, initialStatus, initialDetail := projectElection(prepared.InitialObservation.Nodes)
		if initialStatus != facet.StatusEvaluated {
			return terminal(e.definition, initialStatus, initialDetail)
		}
		if !reflect.DeepEqual(explicit, firstBefore) {
			return terminal(e.definition, facet.StatusInvalidEvidence, "initial election projection differs from trace")
		}
		states = append(states, electionStateInput{
			Nodes: prepared.InitialObservation.Nodes, Occurrence: facet.ExplicitInitialOccurrence(),
		})
	} else {
		states = append(states, electionStateInput{
			Nodes: trace.Steps[0].NodesBefore, Occurrence: facet.TraceInitialOccurrence(0),
		})
	}
	for index, step := range trace.Steps {
		if index > 0 {
			previous, previousStatus, previousDetail := projectElection(trace.Steps[index-1].NodesAfter)
			current, currentStatus, currentDetail := projectElection(step.NodesBefore)
			if previousStatus != facet.StatusEvaluated {
				return terminal(e.definition, previousStatus, previousDetail)
			}
			if currentStatus != facet.StatusEvaluated {
				return terminal(e.definition, currentStatus, currentDetail)
			}
			if !reflect.DeepEqual(previous, current) {
				return terminal(e.definition, facet.StatusInvalidEvidence, "adjacent election projections are discontinuous")
			}
		}
		states = append(states, electionStateInput{
			Nodes: step.NodesAfter, Occurrence: facet.TraceStepAfterOccurrence(index),
		})
	}
	return e.evaluateStates(states)
}

type electionStateInput struct {
	Nodes      []core.NodeObservation
	Occurrence facet.Occurrence
}

func (e *electionEvaluator) evaluateStates(states []electionStateInput) (facet.EvaluationV1, error) {
	observations := make([]facet.ObservationV1, 0, len(states))
	for _, state := range states {
		projected, status, detail := projectElection(state.Nodes)
		if status != facet.StatusEvaluated {
			return terminal(e.definition, status, detail)
		}
		classID := electionClass(projected)
		observation, err := facet.NewObservation(e.definition, classID, state.Occurrence, classID)
		if err != nil {
			return facet.EvaluationV1{}, err
		}
		observations = append(observations, observation)
	}
	return facet.NewEvaluation(e.definition, facet.StatusEvaluated, observations, "")
}

func projectElection(nodes []core.NodeObservation) ([]electionNode, facet.EvaluationStatus, string) {
	if len(nodes) == 0 {
		return nil, facet.StatusInvalidEvidence, "election state has no nodes"
	}
	result := make([]electionNode, len(nodes))
	seen := make(map[core.NodeID]struct{}, len(nodes))
	for index, node := range nodes {
		if _, duplicate := seen[node.ID]; duplicate {
			return nil, facet.StatusInvalidEvidence, "election state contains duplicate node ID"
		}
		seen[node.ID] = struct{}{}
		role, roleState := stringField(node.Semantic, "role")
		if roleState != fieldPresent {
			status, detail := issueStatus(roleState, "role")
			return nil, status, detail
		}
		term, termState := uintField(node.Semantic, "term")
		switch node.Status {
		case core.NodeRunning:
			if termState != fieldPresent {
				status, detail := issueStatus(termState, "term")
				return nil, status, detail
			}
			if role != "follower" && role != "candidate" && role != "leader" {
				return nil, facet.StatusInvalidEvidence, fmt.Sprintf("running node %s has invalid role %q", node.ID, role)
			}
		case core.NodeCrashed:
			if role != "crashed" {
				return nil, facet.StatusInvalidEvidence, fmt.Sprintf("crashed node %s has role %q", node.ID, role)
			}
			if termState == fieldInvalid {
				return nil, facet.StatusInvalidEvidence, "crashed node term is invalid"
			}
		default:
			return nil, facet.StatusInvalidEvidence, "node status is invalid"
		}
		result[index] = electionNode{
			ID: node.ID, Epoch: node.Epoch, Status: node.Status, Role: role, Term: term,
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, facet.StatusEvaluated, ""
}

func electionClass(nodes []electionNode) string {
	leaders, candidates := 0, 0
	terms := make(map[uint64]struct{})
	for _, node := range nodes {
		if node.Status != core.NodeRunning {
			continue
		}
		terms[node.Term] = struct{}{}
		switch node.Role {
		case "leader":
			leaders++
		case "candidate":
			candidates++
		}
	}
	if len(terms) == 0 {
		return "no_running_nodes"
	}
	leaderPart := "leaders_none"
	if leaders == 1 {
		leaderPart = "leaders_one"
	} else if leaders > 1 {
		leaderPart = "leaders_multiple"
	}
	candidatePart := "candidates_none"
	if candidates > 0 {
		candidatePart = "candidates_some"
	}
	termPart := "terms_uniform"
	if len(terms) > 1 {
		termPart = "terms_split"
	}
	return leaderPart + "_" + candidatePart + "_" + termPart
}
