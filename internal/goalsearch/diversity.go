package goalsearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type ProgressPoint struct {
	Completed int `json:"completed"`
	Distance  int `json:"distance"`
}

func PlanKey(sequence plan.PlanSequence) string {
	return diversityDigest(sequence.Actions)
}

func TraceKey(trace core.Trace) string {
	return diversityDigest(struct {
		Version uint32            `json:"version"`
		Steps   []core.StepRecord `json:"steps"`
	}{trace.Version, trace.Steps})
}

func ProgressPathKey(points []ProgressPoint) string {
	return diversityDigest(points)
}

func StringSequenceKey(values []string) string {
	return diversityDigest(values)
}

func SemanticTraceKey(trace core.Trace) string {
	type stepShape struct {
		Action  string         `json:"action"`
		Nodes   []semanticNode `json:"nodes"`
		Effects []string       `json:"effects"`
	}
	shapes := make([]stepShape, 0, len(trace.Steps))
	for _, step := range trace.Steps {
		effects := make([]string, 0, len(step.Effects))
		for _, effect := range step.Effects {
			value := string(effect.Kind)
			switch {
			case effect.ModelEvent != nil:
				value += ":" + effect.ModelEvent.Name
			case effect.Message != nil:
				value += ":" + effect.Message.TypeHint
			case effect.TimerFired != nil:
				value += ":" + effect.TimerFired.TypeHint
			}
			effects = append(effects, value)
		}
		shapes = append(shapes, stepShape{
			Action: string(step.Action.Kind), Nodes: semanticNodes(step.NodesAfter),
			Effects: effects,
		})
	}
	return diversityDigest(shapes)
}

func MessageQueueShapeKey(observation core.Observation) string {
	type messageShape struct {
		Shape string `json:"shape"`
		Count int    `json:"count"`
	}
	counts := make(map[string]int)
	for _, message := range observation.Messages {
		key := strconv.FormatUint(uint64(message.From), 10) + ">" +
			strconv.FormatUint(uint64(message.To), 10) + "|" + message.TypeHint + "|" +
			strconv.FormatBool(message.Blocked)
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	messages := make([]messageShape, 0, len(keys))
	for _, key := range keys {
		messages = append(messages, messageShape{Shape: key, Count: counts[key]})
	}
	return diversityDigest(struct {
		Nodes    []semanticNode `json:"nodes"`
		Messages []messageShape `json:"messages"`
	}{semanticNodes(observation.Nodes), messages})
}

type semanticNode struct {
	ID            core.NodeID     `json:"id"`
	Status        core.NodeStatus `json:"status"`
	Role          string          `json:"role"`
	TermBehind    string          `json:"term_behind"`
	LastBehind    string          `json:"last_behind"`
	CommitBehind  string          `json:"commit_behind"`
	AppliedBehind string          `json:"applied_behind"`
	Snapshot      bool            `json:"has_snapshot"`
}

func semanticNodes(nodes []core.NodeObservation) []semanticNode {
	var maxTerm, maxLast, maxCommit, maxApplied uint64
	for _, node := range nodes {
		term, _ := semanticUint(node.Semantic["term"])
		last, _ := semanticUint(node.Semantic["last_index"])
		commit, _ := semanticUint(node.Semantic["commit"])
		applied, _ := semanticUint(node.Semantic["applied"])
		maxTerm = max(maxTerm, term)
		maxLast = max(maxLast, last)
		maxCommit = max(maxCommit, commit)
		maxApplied = max(maxApplied, applied)
	}
	result := make([]semanticNode, 0, len(nodes))
	for _, node := range nodes {
		term, _ := semanticUint(node.Semantic["term"])
		last, _ := semanticUint(node.Semantic["last_index"])
		commit, _ := semanticUint(node.Semantic["commit"])
		applied, _ := semanticUint(node.Semantic["applied"])
		snapshot, _ := semanticUint(node.Semantic["snapshot_index"])
		result = append(result, semanticNode{
			ID: node.ID, Status: node.Status, Role: semanticString(node.Semantic["role"]),
			TermBehind: relativeBucket(maxTerm, term), LastBehind: relativeBucket(maxLast, last),
			CommitBehind:  relativeBucket(maxCommit, commit),
			AppliedBehind: relativeBucket(maxApplied, applied), Snapshot: snapshot > 0,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func relativeBucket(maximum, value uint64) string {
	if value >= maximum {
		return "max"
	}
	_, ordinal := lagClass(maximum - value)
	switch ordinal {
	case 1:
		return "behind-one"
	case 2:
		return "behind-small"
	default:
		return "behind-large"
	}
}

func diversityDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
