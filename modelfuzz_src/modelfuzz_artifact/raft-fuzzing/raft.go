package main

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"strconv"

	"github.com/zeu5/raft-fuzzing/raft"
	pb "github.com/zeu5/raft-fuzzing/raft/raftpb"
)

// 下面的 RaftRand 原本用于把 raft 内部随机数也接入 FuzzContext，
// 这样随机选举超时可以被记录成 RandomIntegerChoice 并重放。
// 当前实现中这段代码被注释掉，NewRawNode 仍使用 raft 默认随机源。
// type RaftRand struct {
// 	rand *rand.Rand
// 	ctx  *FuzzContext
// 	lock *sync.Mutex
// }

// var _ raft.Rand = &RaftRand{}

// func NewRaftRand() *RaftRand {
// 	return &RaftRand{
// 		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
// 		ctx:  nil,
// 		lock: new(sync.Mutex),
// 	}
// }

// func (r *RaftRand) Intn(max int) int {
// 	r.lock.Lock()
// 	defer r.lock.Unlock()
// 	if r.ctx == nil {
// 		return r.rand.Intn(max)
// 	}
// 	return r.ctx.RandomIntegerChoice(max)
// }

// func (r *RaftRand) UpdateCtx(ctx *FuzzContext) {
// 	r.lock.Lock()
// 	defer r.lock.Unlock()
// 	r.ctx = ctx
// }

type RaftEnvironmentConfig struct {
	// Replicas 是真实 raft 节点数，节点 ID 使用 1..Replicas。
	Replicas int
	// ElectionTick/HeartbeatTick 直接传给 etcd-raft Config，决定逻辑时间下的选举和心跳节奏。
	ElectionTick  int
	HeartbeatTick int
	// TicksPerStep 表示每个 fuzz step 中对每个 RawNode 调用 Tick 的次数。
	TicksPerStep int
	// RandomSeed 固定每个 RawNode 的选举随机源；由 Fuzzer 注入实际运行 seed。
	RandomSeed int64
}

// RaftEnvironment 是 etcd-raft 版对通用 modelfuzz Cluster 的内联实现。
//
// 通用版需要 Cluster.Reset/Stop/Start/ReceiveMessage/ClientRequest/Tick；
// 这里因为被测对象是 Go 库而不是真实进程，所以直接在内存里持有多个 RawNode：
//   - Step 相当于 ReceiveMessage + ClientRequest；
//   - Tick 推进所有 RawNode，处理 Ready，并返回待调度的 raftpb.Message；
//   - Stop/Start 通过删除/重建 RawNode 模拟节点宕机恢复。
//
// 这个设计比 RedisRaft 版轻很多：没有真实网络、没有 redis-server 进程、没有 HTTP 拦截层。
// 好处是执行快、状态可直接观察；代价是测试的是 raft 库本身，而不是完整系统集成行为。
type RaftEnvironment struct {
	config RaftEnvironmentConfig
	// nodes 只保存当前存活的 RawNode；被 Stop 的节点会从这里删除。
	nodes map[uint64]*raft.RawNode
	// storages 模拟每个节点的稳定存储，Start 时会复用，从而保留持久状态。
	storages map[uint64]*raft.MemoryStorage
	// curStates 用于发现状态变化，并转换成模型可见事件，如 Timeout/BecomeLeader。
	curStates map[uint64]raft.Status
	// restartCounts 让同一节点每次 restart 使用不同但可重现的随机序列。
	restartCounts map[uint64]int64
}

func NewRaftEnvironment(config RaftEnvironmentConfig) *RaftEnvironment {
	r := &RaftEnvironment{
		config:        config,
		nodes:         make(map[uint64]*raft.RawNode),
		storages:      make(map[uint64]*raft.MemoryStorage),
		curStates:     make(map[uint64]raft.Status),
		restartCounts: make(map[uint64]int64),
	}
	r.makeNodes()
	return r
}

func (r *RaftEnvironment) makeNodes() {
	// 直接给每个 RawNode 应用同一组 ConfChange，建立初始 1..Replicas 的 voter 集合。
	// 这里没有通过日志提交 membership change，而是为了快速构造一个已配置好的测试集群。
	confChanges := make([]pb.ConfChangeV2, r.config.Replicas)
	for i := 0; i < r.config.Replicas; i++ {
		confChanges[i] = pb.ConfChange{NodeID: uint64(i + 1), Type: pb.ConfChangeAddNode}.AsV2()
	}
	for i := 0; i < r.config.Replicas; i++ {
		storage := raft.NewMemoryStorage()
		nodeID := uint64(i + 1)
		r.storages[nodeID] = storage
		r.restartCounts[nodeID] = 0
		node, _ := raft.NewRawNode(&raft.Config{
			ID:                        nodeID,
			ElectionTick:              r.config.ElectionTick,
			HeartbeatTick:             r.config.HeartbeatTick,
			Storage:                   storage,
			MaxSizePerMsg:             1024 * 1024,
			MaxInflightMsgs:           256,
			Rand:                      r.nodeRand(nodeID, 0),
			MaxUncommittedEntriesSize: 1 << 30,
			Logger:                    &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)},
			CheckQuorum:               true,
		})
		for _, c := range confChanges {
			node.ApplyConfChange(c)
		}
		r.curStates[nodeID] = node.Status()
		r.nodes[nodeID] = node
	}
}

func (r *RaftEnvironment) nodeRand(nodeID uint64, generation int64) *rand.Rand {
	seed := r.config.RandomSeed + int64(nodeID)*1_000_003 + generation*10_000_019
	return rand.New(rand.NewSource(seed))
}

func (r *RaftEnvironment) Reset(ctx *FuzzContext) {
	// 重建所有 RawNode 和 MemoryStorage，清空上一轮 iteration 的内存状态。
	// 注意：这不同于 Start，Reset 是新 iteration 的全量重置，不保留任何节点持久状态。
	r.makeNodes()
}

func (r *RaftEnvironment) Step(ctx *FuzzContext, m pb.Message) {
	// 将 panic 转成当前 iteration 的执行错误，避免单条异常 trace 直接杀掉整个 fuzz 进程。
	defer func(c *FuzzContext) {
		if r := recover(); r != nil {
			c.traceCtx.SetError(fmt.Errorf("panic in Step: %v", r))
		}
	}(ctx)
	if m.Type == pb.MsgProp {
		// 客户端 proposal 没有固定目标节点。这里查找当前 leader，并把请求直接交给 leader。
		// 这和真实客户端重试/重定向逻辑不同，是 etcd-raft 库级 fuzz 的一个简化。
		//
		// 如果当前没有 leader，请求会被静默忽略，也不会记录 ClientRequest 事件。
		// 因而模型看到的客户端请求只包括实际被 leader 接收的请求。
		haveLeader := false
		leader := uint64(0)
		for id := uint64(1); id <= uint64(r.config.Replicas); id++ {
			node, ok := r.nodes[id]
			if !ok {
				continue
			}
			if node.Status().RaftState == raft.StateLeader {
				haveLeader = true
				leader = id
				break
			}
		}
		if haveLeader {
			m.To = leader
			request, _ := strconv.Atoi(string(m.Entries[0].Data))
			// ClientRequest 是模型侧事件，用来把应用请求和后续日志复制关联起来。
			ctx.AddEvent(&Event{
				Name: "ClientRequest",
				Node: leader,
				Params: map[string]interface{}{
					"request": request,
					"leader":  leader,
				},
			})
			r.nodes[leader].Step(m)
		}
	} else {
		// 普通 raft 网络消息按 To 字段投递；如果目标节点已宕机，则 nodes 中没有该项。
		// 这里不记录 DeliverMessage，调用方 Fuzzer.RunIteration 会在投递前统一记录。
		node, ok := r.nodes[m.To]
		if ok {
			node.Step(m)
		}
	}
}

func (r *RaftEnvironment) Tick(ctx *FuzzContext) []pb.Message {
	return r.TickN(ctx, r.config.TicksPerStep)
}

// TickN advances logical time by count ticks for every live node. Ready is
// still consumed once even when count is zero, preserving the existing
// end-of-step message and storage processing frequency.
func (r *RaftEnvironment) TickN(ctx *FuzzContext, count int) []pb.Message {
	result := make([]pb.Message, 0)
	// 推进所有存活节点的逻辑时间。原始模式传入固定 TicksPerStep；显式 Tick
	// 模式可以传入重新分配后的 count，但所有节点仍使用同一个值。
	for id := uint64(1); id <= uint64(r.config.Replicas); id++ {
		node, ok := r.nodes[id]
		if !ok {
			continue
		}
		for i := 0; i < count; i++ {
			node.Tick()
		}
	}
	r.updateStates(ctx)
	for id := uint64(1); id <= uint64(r.config.Replicas); id++ {
		node, ok := r.nodes[id]
		if !ok {
			continue
		}
		if node.HasReady() {
			// RawNode 不提供 Node 的 channel 封装，所以环境需要手动执行 Ready/Advance 契约。
			// 这里实现了最小化的 Ready 消费：应用快照、追加 unstable entries、收集 outbound messages、
			// 记录 commit 推进，然后调用 Advance 告知 raft 这批 Ready 已处理。
			ready := node.Ready()
			if !raft.IsEmptySnap(ready.Snapshot) {
				r.storages[id].ApplySnapshot(ready.Snapshot)
			}
			r.storages[id].Append(ready.Entries)
			result = append(result, ready.Messages...)
			if len(ready.CommittedEntries) > 0 {
				// 模型不关心具体 apply 逻辑，只需要知道 commit index 有推进。
				// 因为本 harness 没有真实应用状态机，所以不会逐条解释 EntryNormal 的业务语义。
				ctx.AddEvent(&Event{
					Name: "AdvanceCommitIndex",
					Node: id,
					Params: map[string]interface{}{
						"i": int(id),
					},
				})
			}
			node.Advance(ready)
		}
	}
	return result
}

func (r *RaftEnvironment) updateStates(ctx *FuzzContext) {
	for id := uint64(1); id <= uint64(r.config.Replicas); id++ {
		node, ok := r.nodes[id]
		if !ok {
			continue
		}
		newStatus := node.Status()
		// 对比上一次状态，把 raft 内部状态变化映射成 TLA+ 模型动作。
		// 这些事件不是网络消息，而是从实现状态中“观察”出来的抽象动作。
		old := r.curStates[id].RaftState
		new := newStatus.RaftState
		oldTerm := r.curStates[id].Term
		newTerm := newStatus.Term
		if old != new && new == raft.StateLeader {
			// BecomeLeader 之后额外记录 request=0，是为了和模型里 leader 当前 term 的 no-op/初始请求对齐。
			// etcd-raft 成为 leader 时会追加一个空 entry；模型侧常把它抽象成特殊请求。
			ctx.AddEvent(&Event{
				Name: "BecomeLeader",
				Node: id,
				Params: map[string]interface{}{
					"node": id,
				},
			})
			ctx.AddEvent(&Event{
				Name: "ClientRequest",
				Node: id,
				Params: map[string]interface{}{
					"request": 0,
					"leader":  id,
				},
			})
		} else if (old != new && new == raft.StateCandidate) || (oldTerm < newTerm && old == new && new == raft.StateCandidate) {
			// 进入 candidate 或 candidate 任期增加，都被抽象成 Timeout。
			ctx.AddEvent(&Event{
				Name: "Timeout",
				Node: id,
				Params: map[string]interface{}{
					"node": id,
				},
			})
		}
		r.curStates[id] = newStatus
	}
}

func (r *RaftEnvironment) Stop(ctx *FuzzContext, node uint64) {
	// 删除 RawNode 模拟宕机；MemoryStorage 保留，用于之后 Start 恢复持久状态。
	// 已经进入 Fuzzer.messageQueues 的旧消息不会被删除，之后如果目标节点恢复，仍可能被调度投递。
	delete(r.nodes, node)
}

func (r *RaftEnvironment) Start(ctx *FuzzContext, nodeID uint64) {
	if storage, ok := r.storages[nodeID]; ok {
		// 用原来的 MemoryStorage 重建 RawNode，模拟进程重启但磁盘未丢失。
		// 这会丢失易失状态，例如当前角色、计时器和内存中的未持久化状态。
		r.restartCounts[nodeID]++
		node, err := raft.NewRawNode(&raft.Config{
			ID:                        nodeID,
			ElectionTick:              r.config.ElectionTick,
			HeartbeatTick:             r.config.HeartbeatTick,
			Storage:                   storage,
			MaxSizePerMsg:             1024 * 1024,
			MaxInflightMsgs:           256,
			Rand:                      r.nodeRand(nodeID, r.restartCounts[nodeID]),
			MaxUncommittedEntriesSize: 1 << 30,
			Logger:                    &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)},
			CheckQuorum:               true,
		})
		r.nodes[nodeID] = node
		if err != nil {
			ctx.traceCtx.SetError(fmt.Errorf("error starting node: %v", err))
		}
	}
}
