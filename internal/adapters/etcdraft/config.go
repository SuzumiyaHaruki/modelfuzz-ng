package etcdraft

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
)

var (
	ErrInvalidConfig  = errors.New("invalid etcd-raft adapter config")
	ErrNotReset       = errors.New("etcd-raft adapter is not reset")
	ErrUnknownNode    = errors.New("unknown etcd-raft node")
	ErrNodeState      = errors.New("invalid etcd-raft node state")
	ErrInvalidMessage = errors.New("invalid etcd-raft message")
)

// Config 保存一个 etcd-raft 集群在多次执行间不变的配置。
// 第一版故意关闭 PreVote、CheckQuorum 和异步存储写入，以保留尽量直接的
// Raft 状态机行为。
type Config struct {
	NodeIDs       []core.NodeID
	ElectionTick  int
	HeartbeatTick int

	MaxSizePerMsg    uint64
	MaxInflightMsgs  int
	MaxInflightBytes uint64
	Snapshot         SnapshotPolicy
	Faults           FaultPolicy

	// Logger 为 nil 时使用 etcd-raft 的默认 Logger。
	Logger raft.Logger
}

// FaultPolicy 保存只用于受控缺陷复现实验的 fault injection。所有字段的默认值
// 都保持正确实现；非默认值必须由实验配置显式开启。
type FaultPolicy struct {
	VoteQuorumDivisor    int    `json:"vote_quorum_divisor"`
	SnapshotStatusMap    string `json:"snapshot_status_mapping"`
	RestartLoseHardState bool   `json:"restart_lose_hard_state"`
}

const (
	normalVoteQuorumDivisor   = 2
	weakenedVoteQuorumDivisor = 3

	SnapshotStatusMappingCorrect = "correct"
	SnapshotStatusMappingInvert  = "invert"
)

// SnapshotPolicy 模拟应用层按已应用日志数量维护 snapshot 和压缩日志。
// Threshold 为0时关闭；RetainEntries 表示 snapshot 点之前仍保留的条目数。
type SnapshotPolicy struct {
	Threshold     uint64 `json:"threshold"`
	RetainEntries uint64 `json:"retain_entries"`
}

// DefaultConfig 返回当前最小实验配置：三个 voter、10 个 tick 的选举超时
// 基数和 1 个 tick 的心跳间隔。
func DefaultConfig() Config {
	return Config{
		NodeIDs:          []core.NodeID{1, 2, 3},
		ElectionTick:     10,
		HeartbeatTick:    1,
		MaxSizePerMsg:    math.MaxUint64,
		MaxInflightMsgs:  256,
		MaxInflightBytes: math.MaxUint64,
		Faults: FaultPolicy{
			VoteQuorumDivisor: normalVoteQuorumDivisor,
			SnapshotStatusMap: SnapshotStatusMappingCorrect,
		},
	}
}

func (c Config) normalized() (Config, error) {
	defaults := DefaultConfig()
	if len(c.NodeIDs) == 0 {
		c.NodeIDs = defaults.NodeIDs
	}
	if c.ElectionTick == 0 {
		c.ElectionTick = defaults.ElectionTick
	}
	if c.HeartbeatTick == 0 {
		c.HeartbeatTick = defaults.HeartbeatTick
	}
	if c.MaxSizePerMsg == 0 {
		c.MaxSizePerMsg = defaults.MaxSizePerMsg
	}
	if c.MaxInflightMsgs == 0 {
		c.MaxInflightMsgs = defaults.MaxInflightMsgs
	}
	if c.MaxInflightBytes == 0 {
		c.MaxInflightBytes = defaults.MaxInflightBytes
	}
	if c.Faults.VoteQuorumDivisor == 0 {
		c.Faults.VoteQuorumDivisor = defaults.Faults.VoteQuorumDivisor
	}
	if c.Faults.SnapshotStatusMap == "" {
		c.Faults.SnapshotStatusMap = defaults.Faults.SnapshotStatusMap
	}

	c.NodeIDs = append([]core.NodeID(nil), c.NodeIDs...)
	sort.Slice(c.NodeIDs, func(i, j int) bool { return c.NodeIDs[i] < c.NodeIDs[j] })
	seen := make(map[core.NodeID]struct{}, len(c.NodeIDs))
	for _, id := range c.NodeIDs {
		if !id.Valid() {
			return Config{}, fmt.Errorf("%w: node ID must be non-zero", ErrInvalidConfig)
		}
		if raft.IsLocalMsgTarget(uint64(id)) {
			return Config{}, fmt.Errorf("%w: node ID %s is reserved by etcd-raft", ErrInvalidConfig, id)
		}
		if _, exists := seen[id]; exists {
			return Config{}, fmt.Errorf("%w: duplicate node ID %s", ErrInvalidConfig, id)
		}
		seen[id] = struct{}{}
	}
	if c.HeartbeatTick <= 0 {
		return Config{}, fmt.Errorf("%w: heartbeat tick must be positive", ErrInvalidConfig)
	}
	if c.ElectionTick <= c.HeartbeatTick {
		return Config{}, fmt.Errorf("%w: election tick must be greater than heartbeat tick", ErrInvalidConfig)
	}
	if c.MaxInflightMsgs <= 0 {
		return Config{}, fmt.Errorf("%w: max inflight messages must be positive", ErrInvalidConfig)
	}
	if c.MaxInflightBytes < c.MaxSizePerMsg {
		return Config{}, fmt.Errorf("%w: max inflight bytes must be at least max message size", ErrInvalidConfig)
	}
	if c.Faults.VoteQuorumDivisor != normalVoteQuorumDivisor &&
		c.Faults.VoteQuorumDivisor != weakenedVoteQuorumDivisor {
		return Config{}, fmt.Errorf("%w: vote quorum divisor must be 2 (normal) or 3 (ModelFuzz mutant)", ErrInvalidConfig)
	}
	if c.Faults.VoteQuorumDivisor == weakenedVoteQuorumDivisor && len(c.NodeIDs) < 4 {
		return Config{}, fmt.Errorf("%w: vote quorum divisor 3 needs at least four voters to differ from majority", ErrInvalidConfig)
	}
	if c.Faults.SnapshotStatusMap != SnapshotStatusMappingCorrect &&
		c.Faults.SnapshotStatusMap != SnapshotStatusMappingInvert {
		return Config{}, fmt.Errorf(
			"%w: snapshot status mapping must be %q or %q",
			ErrInvalidConfig, SnapshotStatusMappingCorrect, SnapshotStatusMappingInvert,
		)
	}
	return c, nil
}

// raftConfig 返回一个 etcd-raft Config，适用于给定节点 ID、存储和已应用索引。
func (c Config) raftConfig(id core.NodeID, storage raft.Storage, applied uint64, random raft.Rand) *raft.Config {
	return &raft.Config{
		ID:                       uint64(id),
		ElectionTick:             c.ElectionTick,
		HeartbeatTick:            c.HeartbeatTick,
		Storage:                  storage,
		Applied:                  applied,
		Rand:                     random,
		AsyncStorageWrites:       false,
		MaxSizePerMsg:            c.MaxSizePerMsg,
		MaxCommittedSizePerReady: c.MaxSizePerMsg,
		MaxInflightMsgs:          c.MaxInflightMsgs,
		MaxInflightBytes:         c.MaxInflightBytes,
		CheckQuorum:              false,
		PreVote:                  false,
		Logger:                   c.Logger,
	}
}
