package breadthdepth

import (
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// RelativeSemanticTraceKey removes execution identity, node identity,
// MessageID, and absolute protocol counters. It is suitable for measuring
// whether successful schedules differ semantically rather than nominally.
func RelativeSemanticTraceKey(trace core.Trace) string {
	type stepShape struct {
		Action  core.ActionKind `json:"action"`
		Nodes   []string        `json:"nodes"`
		Effects []string        `json:"effects"`
	}
	steps := make([]stepShape, 0, len(trace.Steps))
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
		sort.Strings(effects)
		steps = append(steps, stepShape{
			Action: step.Action.Kind, Nodes: relativeNodes(step.NodesAfter), Effects: effects,
		})
	}
	return stableKey(steps)
}

// RelativeQueueShapeKey intentionally omits link endpoints and MessageID.
// Binding roles are represented separately by GoalProgress.BindingRoles.
func RelativeQueueShapeKey(observation core.Observation) string {
	counts := make(map[string]int)
	for _, message := range observation.Messages {
		key := message.TypeHint + "|" + strconv.FormatBool(message.Blocked)
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type queueShape struct {
		Message string `json:"message"`
		Count   int    `json:"count"`
	}
	messages := make([]queueShape, 0, len(keys))
	for _, key := range keys {
		messages = append(messages, queueShape{Message: key, Count: counts[key]})
	}
	return stableKey(struct {
		Nodes    []string     `json:"nodes"`
		Messages []queueShape `json:"messages"`
	}{relativeNodes(observation.Nodes), messages})
}

func BindingRoleKey(observation core.Observation, nodeID core.NodeID) string {
	var maximumTerm, maximumLast, maximumCommit uint64
	for _, node := range observation.Nodes {
		maximumTerm = max(maximumTerm, semanticNumber(node.Semantic["term"]))
		maximumLast = max(maximumLast, semanticNumber(node.Semantic["last_index"]))
		maximumCommit = max(maximumCommit, semanticNumber(node.Semantic["commit"]))
	}
	for _, node := range observation.Nodes {
		if node.ID != nodeID {
			continue
		}
		return string(node.Status) + "|" + semanticText(node.Semantic["role"]) + "|" +
			relativeValue(maximumTerm, semanticNumber(node.Semantic["term"])) + "|" +
			relativeValue(maximumLast, semanticNumber(node.Semantic["last_index"])) + "|" +
			relativeValue(maximumCommit, semanticNumber(node.Semantic["commit"]))
	}
	return "unbound"
}

func relativeNodes(nodes []core.NodeObservation) []string {
	var maximumTerm, maximumLast, maximumCommit, maximumApplied uint64
	for _, node := range nodes {
		maximumTerm = max(maximumTerm, semanticNumber(node.Semantic["term"]))
		maximumLast = max(maximumLast, semanticNumber(node.Semantic["last_index"]))
		maximumCommit = max(maximumCommit, semanticNumber(node.Semantic["commit"]))
		maximumApplied = max(maximumApplied, semanticNumber(node.Semantic["applied"]))
	}
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		snapshot := semanticNumber(node.Semantic["snapshot_index"]) > 0
		result = append(result,
			string(node.Status)+"|"+semanticText(node.Semantic["role"])+"|"+
				relativeValue(maximumTerm, semanticNumber(node.Semantic["term"]))+"|"+
				relativeValue(maximumLast, semanticNumber(node.Semantic["last_index"]))+"|"+
				relativeValue(maximumCommit, semanticNumber(node.Semantic["commit"]))+"|"+
				relativeValue(maximumApplied, semanticNumber(node.Semantic["applied"]))+"|"+
				strconv.FormatBool(snapshot),
		)
	}
	sort.Strings(result)
	return result
}

func relativeValue(maximum, value uint64) string {
	if value >= maximum {
		return "max"
	}
	switch lag := maximum - value; {
	case lag == 1:
		return "behind-one"
	case lag <= 3:
		return "behind-small"
	default:
		return "behind-large"
	}
}

func semanticNumber(value any) uint64 {
	switch typed := value.(type) {
	case uint64:
		return typed
	case uint32:
		return uint64(typed)
	case int:
		if typed >= 0 {
			return uint64(typed)
		}
	case int64:
		if typed >= 0 {
			return uint64(typed)
		}
	case float64:
		if typed >= 0 {
			return uint64(typed)
		}
	}
	return 0
}

func semanticText(value any) string {
	text, _ := value.(string)
	return text
}
