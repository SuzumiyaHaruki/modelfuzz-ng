package core

import (
	"fmt"
	"sort"
)

// NetworkPartition 把节点划分为两个或更多互不连通的组。组内链路保持可用，
// 跨组消息继续进入 Runtime 队列，但在分区合并前不能投递。
type NetworkPartition struct {
	Groups [][]NodeID `json:"groups"`
}

func (p NetworkPartition) Validate() error {
	if len(p.Groups) < 2 {
		return invalidValue("network_partition", "groups", "must contain at least two groups")
	}
	seen := make(map[NodeID]struct{})
	for groupIndex, group := range p.Groups {
		if len(group) == 0 {
			return invalidValue("network_partition", "groups", fmt.Sprintf("group %d must not be empty", groupIndex))
		}
		for _, node := range group {
			if !node.Valid() {
				return invalidValue("network_partition", "groups", "node IDs must be non-zero")
			}
			if _, duplicate := seen[node]; duplicate {
				return invalidValue("network_partition", "groups", "node "+node.String()+" appears more than once")
			}
			seen[node] = struct{}{}
		}
	}
	return nil
}

func (p NetworkPartition) Copy() NetworkPartition {
	copy := NetworkPartition{Groups: make([][]NodeID, len(p.Groups))}
	for index, group := range p.Groups {
		copy.Groups[index] = append([]NodeID(nil), group...)
	}
	return copy
}

func (p NetworkPartition) Normalized() NetworkPartition {
	copy := p.Copy()
	for _, group := range copy.Groups {
		sort.Slice(group, func(i, j int) bool { return group[i] < group[j] })
	}
	sort.Slice(copy.Groups, func(i, j int) bool {
		left, right := copy.Groups[i], copy.Groups[j]
		for index := 0; index < len(left) && index < len(right); index++ {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return len(left) < len(right)
	})
	return copy
}

// Covers 报告 partition 是否恰好包含给定节点集合一次。
func (p NetworkPartition) Covers(nodes []NodeID) bool {
	if p.Validate() != nil {
		return false
	}
	want := make(map[NodeID]struct{}, len(nodes))
	for _, node := range nodes {
		if !node.Valid() {
			return false
		}
		if _, duplicate := want[node]; duplicate {
			return false
		}
		want[node] = struct{}{}
	}
	seen := 0
	for _, group := range p.Groups {
		for _, node := range group {
			if _, exists := want[node]; !exists {
				return false
			}
			seen++
		}
	}
	return seen == len(want)
}

func (p NetworkPartition) Blocks(link LinkID) bool {
	groups := make(map[NodeID]int)
	for groupIndex, group := range p.Groups {
		for _, node := range group {
			groups[node] = groupIndex
		}
	}
	from, fromExists := groups[link.From]
	to, toExists := groups[link.To]
	return fromExists && toExists && from != to
}
