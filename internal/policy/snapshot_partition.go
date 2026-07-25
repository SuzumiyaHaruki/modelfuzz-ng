package policy

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// SnapshotPartitionConfig controls the directed partition/compaction/snapshot
// scenario. The policy remains observation-driven: it never records or predicts
// a concrete MessageID before that message appears in the runtime queue.
type SnapshotPartitionConfig struct {
	NodeIDs           []core.NodeID `json:"node_ids"`
	MaxValue          int           `json:"max_value"`
	MaxLogIndex       uint64        `json:"max_log_index"`
	SnapshotThreshold uint64        `json:"snapshot_threshold"`
	RetainEntries     uint64        `json:"retain_entries"`
	DuplicateSnapshot bool          `json:"duplicate_snapshot"`
	FailFirstSnapshot bool          `json:"fail_first_snapshot"`
}

// SnapshotPartition is a deterministic online scenario generator. It elects a
// leader, isolates one follower, drives the connected majority past a snapshot
// boundary, heals the partition, and makes the lagging follower install (and,
// optionally, reject a duplicate of) the resulting snapshot.
type SnapshotPartition struct {
	config             SnapshotPartitionConfig
	seed               int64
	preferredLeader    core.NodeID
	lagger             core.NodeID
	leader             core.NodeID
	partitionStarted   bool
	healIssued         bool
	snapshotDuplicated bool
	snapshotFailed     bool
	targetSnapshot     uint64
	laggerBaseline     uint64
	recoveryTicks      int
	generated          []plan.PlanAction
}

func NewSnapshotPartition(seed int64, config SnapshotPartitionConfig) (*SnapshotPartition, error) {
	if len(config.NodeIDs) < 3 {
		return nil, fmt.Errorf("snapshot partition policy needs at least three nodes")
	}
	if config.MaxValue <= 0 || config.MaxLogIndex == 0 || config.SnapshotThreshold == 0 {
		return nil, fmt.Errorf("snapshot partition policy bounds and snapshot threshold must be positive")
	}
	if config.SnapshotThreshold > config.MaxLogIndex {
		return nil, fmt.Errorf("snapshot threshold %d exceeds max log index %d", config.SnapshotThreshold, config.MaxLogIndex)
	}
	lastPossibleSnapshot := config.MaxLogIndex - config.MaxLogIndex%config.SnapshotThreshold
	if lastPossibleSnapshot <= config.RetainEntries {
		return nil, fmt.Errorf("snapshot retain entries %d cannot compact a lagging follower before max log index %d",
			config.RetainEntries, config.MaxLogIndex)
	}
	seen := make(map[core.NodeID]struct{}, len(config.NodeIDs))
	for _, node := range config.NodeIDs {
		if !node.Valid() {
			return nil, fmt.Errorf("snapshot partition policy contains an invalid node")
		}
		if _, duplicate := seen[node]; duplicate {
			return nil, fmt.Errorf("snapshot partition policy contains duplicate node %s", node)
		}
		seen[node] = struct{}{}
	}
	config.NodeIDs = append([]core.NodeID(nil), config.NodeIDs...)
	sort.Slice(config.NodeIDs, func(i, j int) bool { return config.NodeIDs[i] < config.NodeIDs[j] })
	return &SnapshotPartition{config: config, seed: seed}, nil
}

func (p *SnapshotPartition) Reset(initial core.Observation) error {
	if p == nil {
		return fmt.Errorf("snapshot partition policy is nil")
	}
	if err := initial.Validate(); err != nil {
		return fmt.Errorf("invalid initial observation: %w", err)
	}
	if !observationCoversNodes(initial, p.config.NodeIDs) {
		return fmt.Errorf("initial observation does not match snapshot partition nodes")
	}
	offset := int(p.seed % int64(len(p.config.NodeIDs)))
	if offset < 0 {
		offset += len(p.config.NodeIDs)
	}
	p.preferredLeader = p.config.NodeIDs[offset]
	p.lagger = 0
	p.leader = 0
	p.partitionStarted = false
	p.healIssued = false
	p.snapshotDuplicated = false
	p.snapshotFailed = false
	p.targetSnapshot = 0
	p.laggerBaseline = 0
	p.recoveryTicks = 0
	p.generated = p.generated[:0]
	return nil
}

func (p *SnapshotPartition) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	if p == nil || !p.preferredLeader.Valid() {
		return plan.PlanAction{}, false, fmt.Errorf("snapshot partition policy is not initialized")
	}
	if err := observation.Validate(); err != nil {
		return plan.PlanAction{}, false, fmt.Errorf("invalid observation: %w", err)
	}
	if leader, ok := observedLeader(observation); ok {
		p.leader = leader.ID
	}
	var action plan.PlanAction
	var more bool
	var err error
	switch {
	case !p.partitionStarted:
		action, more, err = p.beforePartition(observation)
	case observation.NetworkPartition != nil:
		action, more, err = p.duringPartition(observation)
	default:
		action, more, err = p.afterHeal(observation)
	}
	if err != nil || !more {
		return action, more, err
	}
	p.generated = append(p.generated, action.Copy())
	return action, true, nil
}

func (p *SnapshotPartition) Sequence() plan.PlanSequence {
	source := "snapshot_partition"
	if p.config.FailFirstSnapshot {
		source = "snapshot_failure"
	}
	return plan.PlanSequence{Actions: copyActions(p.generated), Metadata: map[string]string{
		"source": source, "seed": strconv.FormatInt(p.seed, 10),
		"leader": p.leader.String(), "lagger": p.lagger.String(),
		"target_snapshot": strconv.FormatUint(p.targetSnapshot, 10),
	}}
}

func (p *SnapshotPartition) beforePartition(observation core.Observation) (plan.PlanAction, bool, error) {
	if p.leader.Valid() {
		p.lagger = p.chooseLagger()
		lagger, _ := findNode(observation, p.lagger)
		p.laggerBaseline, _ = semanticUint(lagger.Semantic["last_index"])
		groups := make([][]core.NodeID, 0, 2)
		majority := make([]core.NodeID, 0, len(p.config.NodeIDs)-1)
		for _, node := range p.config.NodeIDs {
			if node != p.lagger {
				majority = append(majority, node)
			}
		}
		groups = append(groups, majority, []core.NodeID{p.lagger})
		partition := core.NetworkPartition{Groups: groups}.Normalized()
		p.partitionStarted = true
		return plan.PlanAction{Kind: plan.ActionPartition, Partition: &partition}, true, nil
	}
	if len(p.generated) == 0 {
		return plan.PlanAction{Kind: plan.ActionTimeout, Node: p.preferredLeader}, true, nil
	}
	if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
		return !message.Blocked && (message.From == p.preferredLeader || message.To == p.preferredLeader)
	}); ok {
		return observedMessageAction(plan.ActionDeliver, message), true, nil
	}
	if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool { return !message.Blocked }); ok {
		return observedMessageAction(plan.ActionDeliver, message), true, nil
	}
	return plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1}, true, nil
}

func (p *SnapshotPartition) duringPartition(observation core.Observation) (plan.PlanAction, bool, error) {
	leader, ok := findNode(observation, p.leader)
	if !ok || leader.Status != core.NodeRunning || leader.Semantic["role"] != "leader" {
		return plan.PlanAction{}, false, fmt.Errorf("directed snapshot scenario lost leader %s during partition", p.leader)
	}
	lagger, ok := findNode(observation, p.lagger)
	if !ok {
		return plan.PlanAction{}, false, fmt.Errorf("directed snapshot scenario lost lagging node %s", p.lagger)
	}
	snapshotIndex, _ := semanticUint(leader.Semantic["snapshot_index"])
	firstIndex, firstIndexOK := semanticUint(leader.Semantic["first_index"])
	laggerLast, _ := semanticUint(lagger.Semantic["last_index"])
	laggerNext := laggerLast + 1
	laggerNextValid := laggerLast != ^uint64(0)
	if snapshotIndex > p.laggerBaseline && firstIndexOK && laggerNextValid && firstIndex > laggerNext {
		p.targetSnapshot = snapshotIndex
		p.healIssued = true
		return plan.PlanAction{Kind: plan.ActionHeal}, true, nil
	}
	if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
		return !message.Blocked && (message.From == p.leader || message.To == p.leader)
	}); ok {
		return observedMessageAction(plan.ActionDeliver, message), true, nil
	}
	if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool { return !message.Blocked }); ok {
		return observedMessageAction(plan.ActionDeliver, message), true, nil
	}
	lastIndex, _ := semanticUint(leader.Semantic["last_index"])
	if lastIndex < p.config.MaxLogIndex {
		value := 1 + len(p.generated)%p.config.MaxValue
		return plan.PlanAction{Kind: plan.ActionRequest, Node: p.leader, Request: strconv.Itoa(value)}, true, nil
	}
	return plan.PlanAction{}, false, fmt.Errorf(
		"directed snapshot scenario reached max log index %d before leader first index passed lagger next index %d (retain_entries=%d)",
		p.config.MaxLogIndex, laggerNext, p.config.RetainEntries,
	)
}

func (p *SnapshotPartition) afterHeal(observation core.Observation) (plan.PlanAction, bool, error) {
	if !p.healIssued {
		return plan.PlanAction{}, false, fmt.Errorf("directed snapshot scenario partition disappeared before heal")
	}
	leader, leaderOK := findNode(observation, p.leader)
	lagger, laggerOK := findNode(observation, p.lagger)
	if !leaderOK || !laggerOK {
		return plan.PlanAction{}, false, fmt.Errorf("directed snapshot scenario nodes disappeared after heal")
	}
	laggerSnapshot, _ := semanticUint(lagger.Semantic["snapshot_index"])
	snapshotApplied := p.targetSnapshot > 0 && laggerSnapshot >= p.targetSnapshot

	if snapshot, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
		return message.From == p.leader && message.To == p.lagger && message.TypeHint == "MsgSnap"
	}); ok {
		if p.config.FailFirstSnapshot && !p.snapshotFailed {
			p.snapshotFailed = true
			return observedMessageAction(plan.ActionDrop, snapshot), true, nil
		}
		if p.config.DuplicateSnapshot && !p.snapshotDuplicated {
			p.snapshotDuplicated = true
			return observedMessageAction(plan.ActionDuplicate, snapshot), true, nil
		}
		return observedMessageAction(plan.ActionDeliver, snapshot), true, nil
	}
	// Snapshot Restore 会产生 MsgAppResp。先让 Leader 消费响应并退出 pending
	// snapshot progress，再把场景判定为完成。
	if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
		return message.From == p.lagger && message.To == p.leader
	}); ok {
		return observedMessageAction(plan.ActionDeliver, response), true, nil
	}
	if snapshotApplied && snapshotConverged(leader, lagger, p.targetSnapshot) {
		return plan.PlanAction{}, false, nil
	}
	if !snapshotApplied {
		if stale, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.lagger &&
				message.TypeHint != "MsgHeartbeat" && message.TypeHint != "MsgSnap"
		}); ok {
			return observedMessageAction(plan.ActionDrop, stale), true, nil
		}
		if heartbeat, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.lagger && message.TypeHint == "MsgHeartbeat"
		}); ok {
			return observedMessageAction(plan.ActionDeliver, heartbeat), true, nil
		}
	}
	if snapshotApplied {
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return (message.From == p.leader && message.To == p.lagger) ||
				(message.From == p.lagger && message.To == p.leader)
		}); ok {
			return observedMessageAction(plan.ActionDeliver, message), true, nil
		}
	}
	if p.recoveryTicks >= 16 {
		return plan.PlanAction{}, false, fmt.Errorf("directed snapshot scenario did not produce a snapshot after heal")
	}
	p.recoveryTicks++
	return plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1}, true, nil
}

func (p *SnapshotPartition) chooseLagger() core.NodeID {
	for index := len(p.config.NodeIDs) - 1; index >= 0; index-- {
		if p.config.NodeIDs[index] != p.leader {
			return p.config.NodeIDs[index]
		}
	}
	return 0
}

func observedMessageAction(kind plan.ActionKind, message core.MessageObservation) plan.PlanAction {
	return plan.PlanAction{Kind: kind, Messages: &plan.MessageRangeSelector{
		Link: core.LinkID{From: message.From, To: message.To}, Start: message.Position, Count: 1,
	}}
}

func chooseMessage(observation core.Observation, predicate func(core.MessageObservation) bool) (core.MessageObservation, bool) {
	messages := append([]core.MessageObservation(nil), observation.Messages...)
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	for _, message := range messages {
		if predicate(message) {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}

func observedLeader(observation core.Observation) (core.NodeObservation, bool) {
	for _, node := range observation.Nodes {
		if node.Status == core.NodeRunning && node.Semantic["role"] == "leader" {
			return node, true
		}
	}
	return core.NodeObservation{}, false
}

func findNode(observation core.Observation, id core.NodeID) (core.NodeObservation, bool) {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return core.NodeObservation{}, false
}

func snapshotConverged(leader, lagger core.NodeObservation, target uint64) bool {
	laggerSnapshot, _ := semanticUint(lagger.Semantic["snapshot_index"])
	leaderCommit, leaderCommitOK := semanticUint(leader.Semantic["commit"])
	laggerCommit, laggerCommitOK := semanticUint(lagger.Semantic["commit"])
	laggerApplied, laggerAppliedOK := semanticUint(lagger.Semantic["applied"])
	return laggerSnapshot >= target && leaderCommitOK && laggerCommitOK && laggerAppliedOK &&
		laggerCommit >= leaderCommit && laggerApplied >= leaderCommit
}

func observationCoversNodes(observation core.Observation, nodes []core.NodeID) bool {
	if len(observation.Nodes) != len(nodes) {
		return false
	}
	seen := make(map[core.NodeID]struct{}, len(observation.Nodes))
	for _, node := range observation.Nodes {
		seen[node.ID] = struct{}{}
	}
	for _, node := range nodes {
		if _, ok := seen[node]; !ok {
			return false
		}
	}
	return true
}
