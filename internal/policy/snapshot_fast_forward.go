package policy

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// SnapshotFastForwardConfig controls the directed out-of-order append scenario
// that makes etcd-raft naturally take its snapshot matchTerm fast-forward path.
type SnapshotFastForwardConfig struct {
	NodeIDs           []core.NodeID `json:"node_ids"`
	MaxValue          int           `json:"max_value"`
	MaxLogIndex       uint64        `json:"max_log_index"`
	SnapshotThreshold uint64        `json:"snapshot_threshold"`
	RetainEntries     uint64        `json:"retain_entries"`
}

type snapshotFastForwardPhase uint8

const (
	fastForwardElect snapshotFastForwardPhase = iota
	fastForwardSetupPeers
	fastForwardCleanTarget
	fastForwardQueueRequests
	fastForwardDuplicateMiddle
	fastForwardRejectMiddle
	fastForwardFillTarget
	fastForwardCommitQuorum
	fastForwardCleanTargetResponses
	fastForwardDeliverStaleReject
	fastForwardDeliverSnapshot
	fastForwardDrainResponses
)

// SnapshotFastForward first gives the target an acknowledged leader no-op, then
// pipelines entries and delivers one append out of order. Its stale rejection is
// held until another quorum commits and compacts the same entries. Delivering the
// rejection then makes the leader send a snapshot to a follower that already has
// the matching index/term but has not committed it.
type SnapshotFastForward struct {
	config          SnapshotFastForwardConfig
	seed            int64
	preferredLeader core.NodeID
	leader          core.NodeID
	target          core.NodeID
	peers           []core.NodeID
	setupIndex      int
	awaitingPeer    core.NodeID
	phase           snapshotFastForwardPhase
	requestsQueued  uint64
	fillPrevious    uint64
	targetSnapshot  uint64
	snapshotSeen    bool
	recoveryTicks   int
	generated       []plan.PlanAction
}

func NewSnapshotFastForward(seed int64, config SnapshotFastForwardConfig) (*SnapshotFastForward, error) {
	if len(config.NodeIDs) < 3 {
		return nil, fmt.Errorf("snapshot fast-forward policy needs at least three nodes")
	}
	if config.MaxValue <= 0 || config.MaxLogIndex == 0 || config.SnapshotThreshold < 3 {
		return nil, fmt.Errorf("snapshot fast-forward policy needs positive bounds and threshold at least 3")
	}
	if config.SnapshotThreshold > config.MaxLogIndex {
		return nil, fmt.Errorf("snapshot threshold %d exceeds max log index %d", config.SnapshotThreshold, config.MaxLogIndex)
	}
	if config.RetainEntries >= config.SnapshotThreshold-1 {
		return nil, fmt.Errorf("retain entries %d leaves the stale next index inside storage", config.RetainEntries)
	}
	seen := make(map[core.NodeID]struct{}, len(config.NodeIDs))
	for _, node := range config.NodeIDs {
		if !node.Valid() {
			return nil, fmt.Errorf("snapshot fast-forward policy contains an invalid node")
		}
		if _, duplicate := seen[node]; duplicate {
			return nil, fmt.Errorf("snapshot fast-forward policy contains duplicate node %s", node)
		}
		seen[node] = struct{}{}
	}
	config.NodeIDs = append([]core.NodeID(nil), config.NodeIDs...)
	sort.Slice(config.NodeIDs, func(i, j int) bool { return config.NodeIDs[i] < config.NodeIDs[j] })
	return &SnapshotFastForward{config: config, seed: seed}, nil
}

func (p *SnapshotFastForward) Reset(initial core.Observation) error {
	if p == nil {
		return fmt.Errorf("snapshot fast-forward policy is nil")
	}
	if err := initial.Validate(); err != nil {
		return fmt.Errorf("invalid initial observation: %w", err)
	}
	if !observationCoversNodes(initial, p.config.NodeIDs) {
		return fmt.Errorf("initial observation does not match snapshot fast-forward nodes")
	}
	offset := int(p.seed % int64(len(p.config.NodeIDs)))
	if offset < 0 {
		offset += len(p.config.NodeIDs)
	}
	p.preferredLeader = p.config.NodeIDs[offset]
	p.leader, p.target = 0, 0
	p.peers = nil
	p.setupIndex = 0
	p.awaitingPeer = 0
	p.phase = fastForwardElect
	p.requestsQueued = 0
	p.fillPrevious = 1
	p.targetSnapshot = p.config.SnapshotThreshold
	p.snapshotSeen = false
	p.recoveryTicks = 0
	p.generated = p.generated[:0]
	return nil
}

func (p *SnapshotFastForward) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	if p == nil || !p.preferredLeader.Valid() {
		return plan.PlanAction{}, false, fmt.Errorf("snapshot fast-forward policy is not initialized")
	}
	if err := observation.Validate(); err != nil {
		return plan.PlanAction{}, false, fmt.Errorf("invalid observation: %w", err)
	}
	for {
		action, more, advance, err := p.next(observation)
		if err != nil || !more {
			return action, more, err
		}
		if advance {
			continue
		}
		p.generated = append(p.generated, action.Copy())
		return action, true, nil
	}
}

func (p *SnapshotFastForward) next(observation core.Observation) (plan.PlanAction, bool, bool, error) {
	switch p.phase {
	case fastForwardElect:
		if leader, ok := observedLeader(observation); ok {
			p.leader = leader.ID
			for index := len(p.config.NodeIDs) - 1; index >= 0; index-- {
				if p.config.NodeIDs[index] != p.leader {
					p.target = p.config.NodeIDs[index]
					break
				}
			}
			p.peers = append(p.peers, p.target)
			for _, node := range p.config.NodeIDs {
				if node != p.leader && node != p.target {
					p.peers = append(p.peers, node)
				}
			}
			p.phase = fastForwardSetupPeers
			return plan.PlanAction{}, true, true, nil
		}
		if len(p.generated) == 0 {
			return plan.PlanAction{Kind: plan.ActionTimeout, Node: p.preferredLeader}, true, false, nil
		}
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return !message.Blocked && (message.From == p.preferredLeader || message.To == p.preferredLeader)
		}); ok {
			return observedMessageAction(plan.ActionDeliver, message), true, false, nil
		}
		return plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1}, true, false, nil

	case fastForwardSetupPeers:
		if p.setupIndex >= len(p.peers) {
			p.phase = fastForwardCleanTarget
			return plan.PlanAction{}, true, true, nil
		}
		peer := p.peers[p.setupIndex]
		if p.awaitingPeer.Valid() {
			if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
				return message.From == peer && message.To == p.leader && message.TypeHint == "MsgAppResp"
			}); ok {
				p.awaitingPeer = 0
				p.setupIndex++
				return observedMessageAction(plan.ActionDeliver, response), true, false, nil
			}
			return plan.PlanAction{}, false, false, fmt.Errorf("setup append to %s produced no response", peer)
		}
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == peer && message.TypeHint == "MsgApp" &&
				message.Metadata["entry_count"] != "0"
		}); ok {
			p.awaitingPeer = peer
			return observedMessageAction(plan.ActionDeliver, message), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("leader has no initial append for setup peer %s", peer)

	case fastForwardCleanTarget:
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.target
		}); ok {
			return observedMessageAction(plan.ActionDrop, message), true, false, nil
		}
		p.phase = fastForwardQueueRequests
		return plan.PlanAction{}, true, true, nil

	case fastForwardQueueRequests:
		if p.requestsQueued >= p.targetSnapshot-1 {
			p.phase = fastForwardDuplicateMiddle
			return plan.PlanAction{}, true, true, nil
		}
		p.requestsQueued++
		value := 1 + int(p.requestsQueued-1)%p.config.MaxValue
		return plan.PlanAction{Kind: plan.ActionRequest, Node: p.leader, Request: strconv.Itoa(value)}, true, false, nil

	case fastForwardDuplicateMiddle:
		if middle, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.target && message.TypeHint == "MsgApp" &&
				message.Metadata["index"] == "2"
		}); ok {
			p.phase = fastForwardRejectMiddle
			return observedMessageAction(plan.ActionDuplicate, middle), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("optimistic append with previous index 2 was not queued")

	case fastForwardRejectMiddle:
		if middle, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.target && message.TypeHint == "MsgApp" &&
				message.Metadata["index"] == "2"
		}); ok {
			p.phase = fastForwardFillTarget
			return observedMessageAction(plan.ActionDeliver, middle), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("duplicated out-of-order append disappeared")

	case fastForwardFillTarget:
		if p.fillPrevious >= p.targetSnapshot {
			p.phase = fastForwardCommitQuorum
			return plan.PlanAction{}, true, true, nil
		}
		want := strconv.FormatUint(p.fillPrevious, 10)
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.target && message.TypeHint == "MsgApp" &&
				message.Metadata["index"] == want
		}); ok {
			p.fillPrevious++
			return observedMessageAction(plan.ActionDeliver, message), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("target append with previous index %s disappeared", want)

	case fastForwardCommitQuorum:
		leader, _ := findNode(observation, p.leader)
		snapshotIndex, _ := semanticUint(leader.Semantic["snapshot_index"])
		firstIndex, _ := semanticUint(leader.Semantic["first_index"])
		if snapshotIndex >= p.targetSnapshot && firstIndex > 2 {
			p.phase = fastForwardCleanTargetResponses
			return plan.PlanAction{}, true, true, nil
		}
		if p.awaitingPeer.Valid() {
			peer := p.awaitingPeer
			if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
				return message.From == peer && message.To == p.leader && message.TypeHint == "MsgAppResp"
			}); ok {
				p.awaitingPeer = 0
				return observedMessageAction(plan.ActionDeliver, response), true, false, nil
			}
			return plan.PlanAction{}, false, false, fmt.Errorf("quorum append to %s produced no response", peer)
		}
		if message, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			if message.From != p.leader || message.To == p.target || message.TypeHint != "MsgApp" {
				return false
			}
			for _, peer := range p.peers[1:] {
				if message.To == peer {
					return true
				}
			}
			return false
		}); ok {
			p.awaitingPeer = message.To
			return observedMessageAction(plan.ActionDeliver, message), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("quorum did not commit snapshot boundary %d", p.targetSnapshot)

	case fastForwardCleanTargetResponses:
		if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.target && message.To == p.leader && message.TypeHint == "MsgAppResp" &&
				message.Metadata["reject"] != "true"
		}); ok {
			return observedMessageAction(plan.ActionDrop, response), true, false, nil
		}
		p.phase = fastForwardDeliverStaleReject
		return plan.PlanAction{}, true, true, nil

	case fastForwardDeliverStaleReject:
		if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.target && message.To == p.leader && message.TypeHint == "MsgAppResp" &&
				message.Metadata["reject"] == "true"
		}); ok {
			p.phase = fastForwardDeliverSnapshot
			return observedMessageAction(plan.ActionDeliver, response), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("stale target rejection was not preserved")

	case fastForwardDeliverSnapshot:
		if snapshot, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.leader && message.To == p.target && message.TypeHint == "MsgSnap" &&
				message.Metadata["snapshot_index"] == strconv.FormatUint(p.targetSnapshot, 10)
		}); ok {
			p.snapshotSeen = true
			p.phase = fastForwardDrainResponses
			return observedMessageAction(plan.ActionDeliver, snapshot), true, false, nil
		}
		return plan.PlanAction{}, false, false, fmt.Errorf("stale rejection did not trigger snapshot")

	case fastForwardDrainResponses:
		if response, ok := chooseMessage(observation, func(message core.MessageObservation) bool {
			return message.From == p.target && message.To == p.leader && message.TypeHint == "MsgAppResp"
		}); ok {
			return observedMessageAction(plan.ActionDeliver, response), true, false, nil
		}
		target, _ := findNode(observation, p.target)
		commit, commitOK := semanticUint(target.Semantic["commit"])
		applied, appliedOK := semanticUint(target.Semantic["applied"])
		if p.snapshotSeen && commitOK && appliedOK && commit >= p.targetSnapshot && applied >= p.targetSnapshot {
			return plan.PlanAction{}, false, false, nil
		}
		if p.recoveryTicks >= 8 {
			return plan.PlanAction{}, false, false, fmt.Errorf("fast-forward target did not reach snapshot boundary")
		}
		p.recoveryTicks++
		return plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1}, true, false, nil
	}
	return plan.PlanAction{}, false, false, fmt.Errorf("unknown snapshot fast-forward phase %d", p.phase)
}

func (p *SnapshotFastForward) Sequence() plan.PlanSequence {
	return plan.PlanSequence{Actions: copyActions(p.generated), Metadata: map[string]string{
		"source": "snapshot_fast_forward", "seed": strconv.FormatInt(p.seed, 10),
		"leader": p.leader.String(), "target": p.target.String(),
		"target_snapshot": strconv.FormatUint(p.targetSnapshot, 10),
	}}
}
