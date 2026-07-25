package core

import (
	"encoding/json"
	"sort"
)

type NodeStatus string

const (
	NodeRunning NodeStatus = "running"
	NodeCrashed NodeStatus = "crashed"
)

func (s NodeStatus) Valid() bool {
	switch s {
	case NodeRunning, NodeCrashed:
		return true
	default:
		return false
	}
}

type NodeObservation struct {
	ID       NodeID         `json:"id"`
	Epoch    NodeEpoch      `json:"epoch"`
	Status   NodeStatus     `json:"status"`
	Digest   string         `json:"digest,omitempty"`
	Semantic map[string]any `json:"semantic,omitempty"`
}

func (n NodeObservation) Validate() error {
	if !n.ID.Valid() {
		return invalidValue("node_observation", "id", "must be non-zero")
	}
	if !n.Epoch.Valid() {
		return invalidValue("node_observation", "epoch", "must be non-zero")
	}
	if !n.Status.Valid() {
		return invalidValue("node_observation", "status", "is unknown or invalid")
	}
	if _, err := json.Marshal(n.Semantic); err != nil {
		return invalidValue("node_observation", "semantic", "must be JSON serializable: "+err.Error())
	}
	return nil
}

func (n NodeObservation) Copy() NodeObservation {
	copy := n
	copy.Semantic = cloneAnyMap(n.Semantic)
	return copy
}

type MessageObservation struct {
	ID            MessageID         `json:"id"`
	From          NodeID            `json:"from"`
	To            NodeID            `json:"to"`
	SenderEpoch   NodeEpoch         `json:"sender_epoch"`
	LinkSequence  uint64            `json:"link_sequence"`
	ParentID      MessageID         `json:"parent_id,omitempty"`
	Position      int               `json:"position"`
	EnqueuedAt    LogicalTime       `json:"enqueued_at"`
	TypeHint      string            `json:"type_hint,omitempty"`
	PayloadDigest string            `json:"payload_digest,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Blocked       bool              `json:"blocked,omitempty"`
}

func (m MessageObservation) Validate() error {
	message := Message{
		ID:            m.ID,
		From:          m.From,
		To:            m.To,
		SenderEpoch:   m.SenderEpoch,
		Sequence:      m.LinkSequence,
		ParentID:      m.ParentID,
		PayloadDigest: m.PayloadDigest,
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if m.Position < 0 {
		return invalidValue("message_observation", "position", "must be non-negative")
	}
	return nil
}

// Observation 是提供给 Plan/Policy 的协议无关当前视图。Trace 可以
// 记录它的稳定摘要以检查重放分歧。Semantic 由 Adapter 可选提供，
// core 不解释其协议语义。
type Observation struct {
	Time             LogicalTime          `json:"time"`
	Nodes            []NodeObservation    `json:"nodes,omitempty"`
	Messages         []MessageObservation `json:"messages,omitempty"`
	LastAction       *Action              `json:"last_action,omitempty"`
	Semantic         map[string]any       `json:"semantic,omitempty"`
	NetworkPartition *NetworkPartition    `json:"network_partition,omitempty"`
}

func (o Observation) Validate() error {
	if _, err := json.Marshal(o.Semantic); err != nil {
		return invalidValue("observation", "semantic", "must be JSON serializable: "+err.Error())
	}
	seenNodes := make(map[NodeID]struct{}, len(o.Nodes))
	for _, node := range o.Nodes {
		if err := node.Validate(); err != nil {
			return err
		}
		if _, exists := seenNodes[node.ID]; exists {
			return invalidValue("observation", "nodes", "contains duplicate node "+node.ID.String())
		}
		seenNodes[node.ID] = struct{}{}
	}

	seenMessages := make(map[MessageID]struct{}, len(o.Messages))
	for _, message := range o.Messages {
		if err := message.Validate(); err != nil {
			return err
		}
		if _, exists := seenMessages[message.ID]; exists {
			return invalidValue("observation", "messages", "contains duplicate message "+message.ID.String())
		}
		seenMessages[message.ID] = struct{}{}
	}
	if o.NetworkPartition != nil {
		if err := o.NetworkPartition.Validate(); err != nil {
			return err
		}
		nodes := make([]NodeID, len(o.Nodes))
		for index, node := range o.Nodes {
			nodes[index] = node.ID
		}
		if !o.NetworkPartition.Covers(nodes) {
			return invalidValue("observation", "network_partition", "must cover every observed node exactly once")
		}
		for _, message := range o.Messages {
			if message.Blocked != o.NetworkPartition.Blocks(LinkID{From: message.From, To: message.To}) {
				return invalidValue("observation", "messages", "blocked flag does not match network partition")
			}
		}
	} else {
		for _, message := range o.Messages {
			if message.Blocked {
				return invalidValue("observation", "messages", "message is blocked without an active partition")
			}
		}
	}

	if o.LastAction != nil {
		if err := o.LastAction.Validate(); err != nil {
			return invalidValue("observation", "last_action", err.Error())
		}
	}
	return nil
}

func (o Observation) Copy() Observation {
	copy := o
	copy.Nodes = make([]NodeObservation, len(o.Nodes))
	for i, node := range o.Nodes {
		copy.Nodes[i] = node.Copy()
	}
	copy.Messages = make([]MessageObservation, len(o.Messages))
	for i, message := range o.Messages {
		copy.Messages[i] = message
		copy.Messages[i].Metadata = cloneStringMap(message.Metadata)
	}
	if o.LastAction != nil {
		action := o.LastAction.Copy()
		copy.LastAction = &action
	}
	copy.Semantic = cloneAnyMap(o.Semantic)
	if o.NetworkPartition != nil {
		partition := o.NetworkPartition.Copy()
		copy.NetworkPartition = &partition
	}
	return copy
}

// Normalized 返回经过稳定排序的副本，用于 hash 和比较。
func (o Observation) Normalized() Observation {
	copy := o.Copy()
	if copy.NetworkPartition != nil {
		partition := copy.NetworkPartition.Normalized()
		copy.NetworkPartition = &partition
	}
	sort.Slice(copy.Nodes, func(i, j int) bool { return copy.Nodes[i].ID < copy.Nodes[j].ID })
	sort.Slice(copy.Messages, func(i, j int) bool { return copy.Messages[i].ID < copy.Messages[j].ID })
	return copy
}
