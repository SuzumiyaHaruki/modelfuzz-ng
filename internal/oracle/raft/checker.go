// Package raft 实现基于 Adapter Semantic Observation 的基础 Raft Oracle。
package raft

import (
	"fmt"
	"sort"
	"strconv"

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
	committedLogs := make([]committedNode, 0, len(observation.Nodes))
	for _, node := range observation.Nodes {
		commit, commitOK := unsigned(node.Semantic["commit"])
		if available, _ := node.Semantic["committed_prefix_available"].(bool); commitOK && available {
			digests := prefixDigestMap(node.Semantic["committed_prefix_digests"])
			if commit > 0 {
				if _, exists := digests[commit]; !exists {
					findings = append(findings, finding("committed_prefix_incomplete", node.ID, 0,
						fmt.Sprintf("node %s does not expose its committed prefix digest at index %d", node.ID, commit)))
				}
			}
			committedLogs = append(committedLogs, committedNode{id: node.ID, commit: commit, digests: digests})
		}
		term, termOK := unsigned(node.Semantic["term"])
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
		if node.Status != core.NodeRunning {
			continue
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
	}

	sort.Slice(committedLogs, func(i, j int) bool { return committedLogs[i].id < committedLogs[j].id })
	for left := 0; left < len(committedLogs); left++ {
		for right := left + 1; right < len(committedLogs); right++ {
			common := committedLogs[left].commit
			if committedLogs[right].commit < common {
				common = committedLogs[right].commit
			}
			if common == 0 {
				continue
			}
			leftDigest, leftOK := committedLogs[left].digests[common]
			rightDigest, rightOK := committedLogs[right].digests[common]
			if !leftOK || !rightOK {
				missing := committedLogs[left].id
				if leftOK {
					missing = committedLogs[right].id
				}
				findings = append(findings, finding("committed_prefix_incomplete", missing, 0,
					fmt.Sprintf("node %s does not expose committed prefix digest at comparison index %d", missing, common)))
				continue
			}
			if leftDigest != rightDigest {
				findings = append(findings, finding("committed_log_conflict", committedLogs[right].id, 0,
					fmt.Sprintf("nodes %s and %s have different committed log prefixes through index %d",
						committedLogs[left].id, committedLogs[right].id, common)))
			}
		}
	}
	return findings
}

type committedNode struct {
	id      core.NodeID
	commit  uint64
	digests map[uint64]string
}

func prefixDigestMap(value any) map[uint64]string {
	result := make(map[uint64]string)
	switch values := value.(type) {
	case map[string]string:
		for index, digest := range values {
			if parsed, err := strconv.ParseUint(index, 10, 64); err == nil && digest != "" {
				result[parsed] = digest
			}
		}
	case map[string]any:
		for index, rawDigest := range values {
			digest, ok := rawDigest.(string)
			if parsed, err := strconv.ParseUint(index, 10, 64); err == nil && ok && digest != "" {
				result[parsed] = digest
			}
		}
	}
	return result
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
