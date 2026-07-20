package etcdraft

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// node 将可丢失的 RawNode 与崩溃后仍保留的稳定状态分开保存。
type node struct {
	id      core.NodeID
	epoch   core.NodeEpoch
	running bool

	raw       *raft.RawNode
	storage   *raft.MemoryStorage
	applied   uint64
	confState *raftpb.ConfState
	lastIndex uint64
	lastTerm  uint64
	logDigest string
}

func newNode(config Config, id core.NodeID, confState *raftpb.ConfState, random raft.Rand) (*node, error) {
	storage := raft.NewMemoryStorage()
	// 成员集合是本次测试的初始条件，不作为配置日志进入被探索状态。
	// index=0 的 Snapshot 只为 Storage.InitialState 提供 ConfState，因而
	// 节点从 term=0、空日志启动，与轻量 TLA+ 模型的 Init 对齐。
	initialSnapshot := &raftpb.Snapshot{
		Metadata: &raftpb.SnapshotMetadata{
			ConfState: proto.Clone(confState).(*raftpb.ConfState),
		},
	}
	if err := storage.ApplySnapshot(initialSnapshot); err != nil {
		return nil, fmt.Errorf("initialize storage for node %s: %w", id, err)
	}
	raw, err := raft.NewRawNode(config.raftConfig(id, storage, 0, random))
	if err != nil {
		return nil, fmt.Errorf("create node %s: %w", id, err)
	}
	n := &node{
		id:        id,
		epoch:     1,
		running:   true,
		raw:       raw,
		storage:   storage,
		confState: proto.Clone(confState).(*raftpb.ConfState),
	}
	if err := n.refreshLogState(); err != nil {
		return nil, fmt.Errorf("observe initial log for node %s: %w", id, err)
	}
	return n, nil
}

func (n *node) restart(config Config, random raft.Rand) error {
	raw, err := raft.NewRawNode(config.raftConfig(n.id, n.storage, n.applied, random))
	if err != nil {
		return err
	}
	n.epoch++
	n.running = true
	n.raw = raw
	return n.refreshLogState()
}

func (n *node) crash() {
	n.running = false
	n.raw = nil
}

func (n *node) setConfState(state *raftpb.ConfState) {
	if state == nil {
		n.confState = &raftpb.ConfState{}
		return
	}
	n.confState = proto.Clone(state).(*raftpb.ConfState)
}

// refreshLogState 在持久化日志或 snapshot 变化时更新轻量摘要。Observe 只读取
// 缓存值，避免每个 Action 都重新扫描全部日志形成 O(n²) 开销。
func (n *node) refreshLogState() error {
	first, err := n.storage.FirstIndex()
	if err != nil {
		return err
	}
	last, err := n.storage.LastIndex()
	if err != nil {
		return err
	}
	lastTerm := uint64(0)
	if last != 0 {
		lastTerm, err = n.storage.Term(last)
		if err != nil {
			return err
		}
	}

	hash := sha256.New()
	snapshot, err := n.storage.Snapshot()
	if err != nil {
		return err
	}
	if err := writeProtoDigest(hash, snapshot); err != nil {
		return err
	}
	if first <= last {
		entries, err := n.storage.Entries(first, last+1, math.MaxUint64)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := writeProtoDigest(hash, entry); err != nil {
				return err
			}
		}
	}
	n.lastIndex = last
	n.lastTerm = lastTerm
	n.logDigest = fmt.Sprintf("%x", hash.Sum(nil))
	return nil
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeProtoDigest(writer digestWriter, message proto.Message) error {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return err
	}
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(data)))
	if _, err := writer.Write(size[:]); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}
