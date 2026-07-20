// Package raft 实现基于 Adapter Semantic Observation 的基础 Raft Oracle。
package raft

import (
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
)

const oracleName = "raft.basic"

// Checker 检查不依赖 TLA+ 后端即可判定的基础 Raft 安全条件。
type Checker struct {
	leadersByTerm map[uint64]core.NodeID
}

func New() *Checker {
	return &Checker{}
}

func (c *Checker) Reset(initial core.Observation) []oracle.Finding {
	c.leadersByTerm = make(map[uint64]core.NodeID)
	return c.checkObservation(initial)
}

func (c *Checker) Check(transition model.Transition) []oracle.Finding {
	if c.leadersByTerm == nil {
		c.leadersByTerm = make(map[uint64]core.NodeID)
	}
	findings := make([]oracle.Finding, 0)
	before := nodesByID(transition.Before)
	for _, after := range transition.After.Nodes {
		previous, exists := before[after.ID]
		if !exists || previous.Epoch != after.Epoch {
			continue
		}
		findings = append(findings, monotonicFindings(previous, after)...)
	}
	findings = append(findings, c.checkObservation(transition.After)...)
	return findings
}

func (c *Checker) checkObservation(observation core.Observation) []oracle.Finding {
	findings := make([]oracle.Finding, 0)
	committedLogs := make(map[uint64]map[string]core.NodeID)
	for _, node := range observation.Nodes {
		if node.Status != core.NodeRunning {
			continue
		}
		term, termOK := unsigned(node.Semantic["term"])
		commit, commitOK := unsigned(node.Semantic["commit"])
		applied, appliedOK := unsigned(node.Semantic["applied"])
		lastIndex, indexOK := unsigned(node.Semantic["last_index"])
		if commitOK && appliedOK && applied > commit {
			findings = append(findings, finding("applied_exceeds_commit", node.ID, term,
				fmt.Sprintf("node %s applied=%d exceeds commit=%d", node.ID, applied, commit)))
		}
		if commitOK && indexOK && commit > lastIndex {
			findings = append(findings, finding("commit_exceeds_log", node.ID, term,
				fmt.Sprintf("node %s commit=%d exceeds last_index=%d", node.ID, commit, lastIndex)))
		}

		role, _ := node.Semantic["role"].(string)
		if role == "leader" && termOK {
			if leader, exists := c.leadersByTerm[term]; exists && leader != node.ID {
				findings = append(findings, finding("multiple_leaders_same_term", node.ID, term,
					fmt.Sprintf("term %d has leaders %s and %s", term, leader, node.ID)))
			} else {
				c.leadersByTerm[term] = node.ID
			}
		}

		// 当前 Observation 只有整段日志摘要，无法直接比较任意 committed prefix。
		// 仅当 applied=commit=last_index 时，整个摘要才严格代表已提交日志。
		if appliedOK && commitOK && indexOK && applied > 0 && applied == commit && commit == lastIndex {
			digest, _ := node.Semantic["log_digest"].(string)
			if digest != "" {
				if committedLogs[applied] == nil {
					committedLogs[applied] = make(map[string]core.NodeID)
				}
				committedLogs[applied][digest] = node.ID
			}
		}
	}

	indexes := make([]uint64, 0, len(committedLogs))
	for index := range committedLogs {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	for _, index := range indexes {
		digests := committedLogs[index]
		if len(digests) <= 1 {
			continue
		}
		nodes := make([]core.NodeID, 0, len(digests))
		for _, node := range digests {
			nodes = append(nodes, node)
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
		findings = append(findings, finding("committed_log_conflict", nodes[0], 0,
			fmt.Sprintf("nodes with applied index %d have different fully committed log digests", index)))
	}
	return findings
}

func monotonicFindings(before, after core.NodeObservation) []oracle.Finding {
	findings := make([]oracle.Finding, 0)
	beforeTerm, beforeTermOK := unsigned(before.Semantic["term"])
	afterTerm, afterTermOK := unsigned(after.Semantic["term"])
	if beforeTermOK && afterTermOK && afterTerm < beforeTerm {
		findings = append(findings, finding("term_regressed", after.ID, afterTerm,
			fmt.Sprintf("node %s term regressed from %d to %d", after.ID, beforeTerm, afterTerm)))
	}
	for _, field := range []struct {
		name string
		code string
	}{
		{name: "commit", code: "commit_regressed"},
		{name: "applied", code: "applied_regressed"},
	} {
		previous, previousOK := unsigned(before.Semantic[field.name])
		current, currentOK := unsigned(after.Semantic[field.name])
		if previousOK && currentOK && current < previous {
			findings = append(findings, finding(field.code, after.ID, afterTerm,
				fmt.Sprintf("node %s %s regressed from %d to %d", after.ID, field.name, previous, current)))
		}
	}
	return findings
}

func nodesByID(observation core.Observation) map[core.NodeID]core.NodeObservation {
	result := make(map[core.NodeID]core.NodeObservation, len(observation.Nodes))
	for _, node := range observation.Nodes {
		result[node.ID] = node
	}
	return result
}

func unsigned(value any) (uint64, bool) {
	switch number := value.(type) {
	case uint64:
		return number, true
	case uint32:
		return uint64(number), true
	case int:
		if number >= 0 {
			return uint64(number), true
		}
	case int64:
		if number >= 0 {
			return uint64(number), true
		}
	case float64:
		if number >= 0 && number == float64(uint64(number)) {
			return uint64(number), true
		}
	}
	return 0, false
}

func finding(code string, node core.NodeID, term uint64, message string) oracle.Finding {
	return oracle.Finding{Oracle: oracleName, Code: code, Message: message, Node: node, Term: term}
}
