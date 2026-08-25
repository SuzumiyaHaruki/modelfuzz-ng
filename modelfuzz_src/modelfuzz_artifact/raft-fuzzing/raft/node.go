// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package raft

import (
	"context"
	"errors"

	pb "github.com/zeu5/raft-fuzzing/raft/raftpb"
)

type SnapshotStatus int

const (
	// SnapshotFinish 表示传输层认为快照发送流程已经成功完成。
	SnapshotFinish SnapshotStatus = 1
	// SnapshotFailure 表示快照发送或应用失败，leader 需要恢复对该 follower 的日志探测。
	SnapshotFailure SnapshotStatus = 2
)

var (
	// emptyState 用作 HardState 的零值哨兵，表示当前 Ready 中没有需要持久化的 HardState 更新。
	emptyState = pb.HardState{}

	// ErrStopped 会由已经停止的 Node 上的方法返回。
	ErrStopped = errors.New("raft: stopped")
)

// SoftState 提供对日志记录和调试有用的状态。
// 它只描述当前内存中的 leader 和角色，不属于 Raft 安全性所依赖的持久状态，
// 因此不需要写入 WAL。
type SoftState struct {
	Lead      uint64 // 当前 leader 的节点 ID；访问时必须使用原子操作，并保持 64 位对齐。
	RaftState StateType
}

func (a *SoftState) equal(b *SoftState) bool {
	return a.Lead == b.Lead && a.RaftState == b.RaftState
}

// Ready 是 raft 核心交给应用层处理的一批“待完成工作”。
// 它把同一时刻产生的持久化任务、状态机 apply 任务、对外发送的消息以及只读请求结果
// 聚合在一起。应用层必须遵守 Ready 的处理契约，尤其要保证需要持久化的状态
// 在依赖这些状态的响应消息发送之前落盘。
//
// Ready 中的所有字段都是只读的；应用层消费完这一批工作后，通常需要调用 Advance，
// 让 Node 继续释放下一批 Ready。
type Ready struct {
	// Node 当前的易失状态。
	// 如果没有更新，SoftState 将为 nil。
	// 应用层可以把它用于日志、指标或调试，不需要保存到持久化存储。
	*SoftState

	// Node 当前需要在发送 Messages 之前保存到稳定存储的状态。
	// HardState 包含 currentTerm、votedFor 和 commit index，
	// 这些字段决定节点重启后的安全行为。
	//
	// 如果没有更新，HardState 将等于空状态。
	//
	// 如果启用了异步存储写入，则不需要立即处理该字段。
	// 它会体现在 Messages 切片中的 MsgStorageAppend 消息里。
	pb.HardState

	// ReadStates 是 ReadIndex 请求的结果。
	// 当应用层已经 apply 到大于等于 ReadState.Index 的位置时，
	// 与该 ReadState.RequestCtx 对应的线性一致读请求就可以安全地在本地返回。
	// 注意，ReadState 只对发起该 ReadIndex 的那次请求有效。
	ReadStates []ReadState

	// Entries 指定需要在发送 Messages 之前保存到稳定存储的 entries。
	// 它们是 raft 生成但尚未稳定落盘的日志条目，通常需要追加到 WAL。
	//
	// 如果启用了异步存储写入，则不需要立即处理该字段。
	// 它会体现在 Messages 切片中的 MsgStorageAppend 消息里。
	Entries []pb.Entry

	// Snapshot 指定需要保存到稳定存储的快照。
	// 如果该字段非空，应用层还需要让自己的状态机安装该快照。
	//
	// 如果启用了异步存储写入，则不需要立即处理该字段。
	// 它会体现在 Messages 切片中的 MsgStorageAppend 消息里。
	Snapshot pb.Snapshot

	// CommittedEntries 指定已经提交、需要应用到应用状态机的 entries。
	// 这些 entries 此前已经被追加到稳定存储。
	// 应用层按顺序 apply 它们；如果遇到配置变更 entry，还必须调用 ApplyConfChange。
	//
	// 如果启用了异步存储写入，则不需要立即处理该字段。
	// 它会体现在 Messages 切片中的 MsgStorageApply 消息里。
	CommittedEntries []pb.Entry

	// Messages 指定待发出的消息。
	// 它们可能是发送给其他 raft 节点的网络消息，也可能是在 AsyncStorageWrites 模式下
	// 发送给本地存储线程的 MsgStorageAppend/MsgStorageApply。
	//
	// 如果未启用异步存储写入，这些消息必须在 Entries 被追加到稳定存储之后发送。
	//
	// 如果启用了异步存储写入，这些消息可以立即发送；那些以异步写入完成为前提的
	// 消息会改为附加在各自的 MsgStorage{Append,Apply} 消息上。
	//
	// 如果其中包含 MsgSnap 消息，应用程序在快照接收完成或失败时必须调用
	// ReportSnapshot 向 raft 回报。
	Messages []pb.Message

	// MustSync 表示 HardState 和 Entries 是否必须持久写入磁盘，
	// 或者是否允许先进行非同步写入。term/vote 变化或存在新日志条目时通常必须同步落盘。
	MustSync bool
}

func isHardStateEqual(a, b pb.HardState) bool {
	return a.Term == b.Term && a.Vote == b.Vote && a.Commit == b.Commit
}

// IsEmptyHardState 在给定 HardState 为空时返回 true。
func IsEmptyHardState(st pb.HardState) bool {
	return isHardStateEqual(st, emptyState)
}

// IsEmptySnap 在给定 Snapshot 为空时返回 true。
func IsEmptySnap(sp pb.Snapshot) bool {
	return sp.Metadata.Index == 0
}

// Node 表示 raft 集群中的一个节点，是应用层使用 raft 库的主要接口。
//
// 与 RawNode 不同，Node 内部通过一个 goroutine 和多个 channel 串行化所有输入：
// 本地 proposal、网络消息、tick、配置变更和 Ready/Advance 确认最终都会进入
// node.run 的事件循环，再调用底层 RawNode/raft 状态机。
//
// 应用层通常需要围绕 Node 实现三件事：
//  1. 周期性调用 Tick。
//  2. 将收到的 raft 网络消息传给 Step。
//  3. 持续读取 Ready，完成持久化、发送消息和状态机 apply 后调用 Advance。
type Node interface {
	// Tick 将 Node 的内部逻辑时钟推进一个 tick。
	// raft 不直接依赖真实时间，选举超时和心跳超时都由调用者按固定间隔调用 Tick 来驱动。
	Tick()
	// Campaign 主动触发一次竞选。
	// 它会向本地 raft 状态机注入 MsgHup，使节点进入 candidate 或 pre-candidate 流程。
	Campaign(ctx context.Context) error
	// Propose 提议将 data 作为一条普通日志追加到 raft 日志中。
	// 如果当前节点不是 leader，proposal 可能被转发给 leader；如果禁用了转发或没有可用 leader，
	// proposal 可能失败或被丢弃。
	//
	// 注意：proposal 成功返回只表示 raft 状态机接收了这次提议，不表示该数据已经 committed。
	// 应用层必须通过 Ready.CommittedEntries 观察提交结果，并自行处理超时重试。
	Propose(ctx context.Context, data []byte) error
	// ProposeConfChange 提议一次配置变更，例如添加 voter、添加 learner 或移除节点。
	// 配置变更本质上也是一条 raft 日志，因此和普通 proposal 一样，可能失败、被丢弃，
	// 或者成功接收但最终没有提交。
	//
	// 特别地，为了避免同时存在多个未应用的成员变更，leader 只有在确认日志中没有更早的、
	// 尚未应用的配置变更时，才会接受新的配置变更 proposal。
	//
	// 该方法接受 pb.ConfChange（已废弃）或 pb.ConfChangeV2 消息。
	// ConfChangeV2 允许通过 joint consensus 进行更复杂的配置变更，尤其包括替换 voter。
	// 只有当参与集群的所有 Node 都运行支持 V2 API 的本库版本时，
	// 才允许传入 ConfChangeV2 消息。用法细节和语义见 pb.ConfChangeV2。
	ProposeConfChange(ctx context.Context, cc pb.ConfChangeI) error

	// Step 将一条外部消息交给 raft 状态机处理。
	// 典型用法是在网络层收到其他节点发来的 raftpb.Message 后调用 Step。
	// 如果等待投递消息期间 ctx 被取消，将返回 ctx.Err()。
	Step(ctx context.Context, msg pb.Message) error

	// Ready 返回一个 channel，应用层从中接收 raft 核心产生的待处理工作批次。
	// 每个 Ready 都是 Node 和外部系统之间的同步点：应用层需要根据其中的字段写 WAL、
	// 发送网络消息、安装快照、apply 已提交日志和处理 ReadState。
	//
	// Node 的使用者在取出 Ready 返回的状态后必须调用 Advance
	// （除非启用了异步存储写入；在这种情况下绝不应调用 Advance）。
	//
	// 注意：在上一个 Ready 中的所有 CommittedEntries 和 Snapshot 处理完成之前，
	// 不得应用下一个 Ready 中的任何 CommittedEntries。
	Ready() <-chan Ready

	// Advance 通知 Node：应用程序已经保存了截至上一个 Ready 的进度。
	// 对非异步存储模式而言，它还会触发 RawNode.Advance，让 raft 知道哪些 unstable entries
	// 已经稳定、哪些 committed entries 已经交给应用层处理。
	//
	// 应用程序通常应在持久化 Entries/HardState/Snapshot、发送 Messages、
	// 并应用完上一个 Ready 中的 CommittedEntries 后调用 Advance。
	//
	// 不过作为一种优化，应用程序也可以在应用命令的过程中调用 Advance。
	// 例如，当上一个 Ready 包含快照时，应用程序应用快照数据可能耗时很久。
	// 为了在不阻塞 raft 进展的情况下继续接收 Ready，可以在完成应用上一个 Ready 之前调用 Advance。
	//
	// 注意：使用 AsyncStorageWrites 时不得调用 Advance。
	// 此时由本地 append 和 apply 线程返回的响应消息替代它的作用。
	Advance()
	// ApplyConfChange 将配置变更（此前传给 ProposeConfChange）应用到节点。
	// 每当在 Ready.CommittedEntries 中应用到配置变更 entry 时都必须调用它，
	// 除非应用程序决定拒绝该配置变更（即把它当作 noop 处理），这种情况下不得调用。
	//
	// 返回的 ConfState 描述应用后的集群成员配置，必须记录到后续快照中，
	// 这样节点重启或安装快照时才能恢复正确的 voter/learner 集合。
	ApplyConfChange(cc pb.ConfChangeI) *pb.ConfState

	// TransferLeadership 请求当前 leader 将领导权转移给 transferee。
	// lead 是调用者认为的当前 leader；transferee 是希望接任 leader 的节点 ID。
	TransferLeadership(ctx context.Context, lead, transferee uint64)

	// ReadIndex 请求一个读状态。该读状态会被设置到 Ready 中。
	// 读状态包含一个 read index。一旦应用程序 apply 到大于等于该 read index 的位置，
	// 在该读请求之前发出的任何线性一致读请求都可以被安全处理。
	// Ready.ReadStates 中返回的 ReadState 会携带相同的 rctx，应用层可用它关联原始读请求。
	//
	// 注意：ReadIndex 请求也可能在没有通知的情况下丢失，因此应用层需要自行处理超时重试。
	ReadIndex(ctx context.Context, rctx []byte) error

	// Status 返回 raft 状态机的当前状态。
	Status() Status
	// ReportUnreachable 报告给定节点在上一次发送时不可达。
	// leader 会用该信息调整对应 follower 的 Progress，重新进入探测流程。
	ReportUnreachable(id uint64)
	// ReportSnapshot 报告已发送快照的状态。id 是预期接收该快照的 follower 的 raft ID，
	// status 为 SnapshotFinish 或 SnapshotFailure。
	//
	// 使用 SnapshotFinish 调用 ReportSnapshot 不会改变 raft 状态。
	// 但是，任何发送或应用快照的失败（例如从 leader 流式传输到 follower 时失败）
	// 都应使用 SnapshotFailure 报告给 leader。leader 发送快照后会暂停该 follower 的日志探测；
	// 如果失败没有被报告，leader 可能一直等待，导致该 follower 长时间收不到新的日志更新。
	ReportSnapshot(id uint64, status SnapshotStatus)
	// Stop 执行 Node 必要的终止流程。
	Stop()
}

type Peer struct {
	// ID 是 raft 集群内唯一的节点 ID，必须非零且不能复用。
	ID uint64
	// Context 会随初始 ConfChangeAddNode entry 一起写入日志，供上层携带自定义元数据。
	Context []byte
}

// setupNode 创建 RawNode、写入初始集群成员配置，并把 RawNode 包装成带 goroutine 的 Node。
// StartNode 使用该函数启动一个全新的集群或新 group；RestartNode 不走这里，
// 因为重启时成员配置应从 Storage.InitialState 中恢复。
func setupNode(c *Config, peers []Peer) *node {
	if len(peers) == 0 {
		panic("no peers given; use RestartNode instead")
	}
	rn, err := NewRawNode(c)
	if err != nil {
		panic(err)
	}
	err = rn.Bootstrap(peers)
	if err != nil {
		c.Logger.Warningf("error occurred during starting a new node: %v", err)
	}

	n := newNode(rn)
	return &n
}

// StartNode 根据给定配置和 raft peers 列表返回一个新的 Node。
// 它会为每个给定 peer 向初始日志追加一条 ConfChangeAddNode entry。
//
// Peers 长度不得为零；这种情况下请调用 RestartNode。
func StartNode(c *Config, peers []Peer) Node {
	n := setupNode(c, peers)
	go n.run()
	return n
}

// RestartNode 类似于 StartNode，但不接收 peers 列表。
// 集群当前成员关系会从 Storage 中恢复。
// 如果调用者已有状态机，应通过 Config.Applied 设置该状态机已经应用到的最后一个日志 index；
// 否则保持零值。
func RestartNode(c *Config) Node {
	rn, err := NewRawNode(c)
	if err != nil {
		panic(err)
	}
	n := newNode(rn)
	go n.run()
	return &n
}

type msgWithResult struct {
	m pb.Message
	// result 只用于需要同步等待处理结果的 proposal。
	// 例如 Propose 会等待 raft.Step 返回，以便把 ErrProposalDropped 等错误传回调用者。
	result chan error
}

// node 是 Node interface 的标准实现。
//
// 它的主要职责不是实现 Raft 算法本身，而是在并发安全的公开 API 和非线程安全的 RawNode
// 之间建立一个串行化边界。所有外部输入都会通过下面这些 channel 送入 run 循环，
// run 循环再按顺序调用 RawNode/raft。
type node struct {
	// propc 承载本地 proposal，包括普通日志 proposal 和配置变更 proposal。
	// 只有在当前节点知道 leader 存在时，run 循环才会启用该 channel。
	propc chan msgWithResult
	// recvc 承载来自网络或本地控制路径的非 proposal 消息。
	recvc chan pb.Message
	// confc/confstatec 用于 ApplyConfChange 的同步请求/响应。
	confc      chan pb.ConfChangeV2
	confstatec chan pb.ConfState
	// readyc 将 RawNode 产生的 Ready 暴露给应用层。
	readyc chan Ready
	// advancec 接收应用层对上一批 Ready 的完成确认。
	advancec chan struct{}
	// tickc 接收逻辑时钟 tick，驱动选举和心跳。
	tickc chan struct{}
	// done 在 run 循环退出时关闭，用于通知所有阻塞中的调用者。
	done chan struct{}
	// stop 请求 run 循环退出。
	stop chan struct{}
	// status 用于同步获取当前 raft 状态。
	status chan chan Status

	// rn 是真正持有 raft 状态机的 RawNode。RawNode 非线程安全，只能在 run 中访问。
	rn *RawNode
}

func newNode(rn *RawNode) node {
	return node{
		propc:      make(chan msgWithResult),
		recvc:      make(chan pb.Message),
		confc:      make(chan pb.ConfChangeV2),
		confstatec: make(chan pb.ConfState),
		readyc:     make(chan Ready),
		advancec:   make(chan struct{}),
		// 将 tickc 设为带缓冲的 chan，这样 raft node 在忙于处理 raft 消息时
		// 可以缓冲一些 ticks。raft node 空闲后会继续处理缓冲的 ticks。
		tickc:  make(chan struct{}, 128),
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
		status: make(chan chan Status),
		rn:     rn,
	}
}

func (n *node) Stop() {
	select {
	case n.stop <- struct{}{}:
		// 尚未停止，因此触发停止流程。
	case <-n.done:
		// Node 已经停止，无需执行任何操作。
		return
	}
	// 阻塞直到 run() 确认停止。
	<-n.done
}

// run 是 Node 的事件循环。
//
// 它把并发到来的 API 调用串行化，然后驱动底层 RawNode：
// proposal 和网络消息会调用 raft.Step，tick 会调用 RawNode.Tick，
// 有待处理工作时会向 readyc 发送 Ready，应用层调用 Advance 后再释放下一批 Ready。
//
// 这个循环还通过将某些 channel 置为 nil 来控制 select 的可选分支：
// 没有 leader 时禁用 propc，防止本地 proposal 堆积；
// 已经发出 Ready 但尚未 Advance 时禁用 readyc，避免同时有两批未确认的 Ready。
func (n *node) run() {
	var propc chan msgWithResult
	var readyc chan Ready
	var advancec chan struct{}
	var rd Ready

	r := n.rn.raft

	lead := None

	for {
		if advancec == nil && n.rn.HasReady() {
			// 先构造一个候选 Ready。注意，这个 Ready 不保证一定会被实际发送给应用层。
			// 下面只是启用 readyc 这个 select 分支；如果本轮 select 选择了别的分支，
			// 下一轮可能会重新构造 Ready。
			// 也可以选择强制先处理前一个 Ready，但通常发出更大的 Ready 更好，
			// 同时也能简化测试（因为发出频率更低、更可预测）。
			rd = n.rn.readyWithoutAccept()
			readyc = n.readyc
		}

		if lead != r.lead {
			// proposal 只有在已知 leader 存在时才开放。
			// 如果当前节点是 follower，raft.Step 会负责把 proposal 转发给 leader；
			// 如果没有 leader，让调用方等待或超时，比让 proposal 在内部无限堆积更清晰。
			if r.hasLeader() {
				if lead == None {
					r.logger.Infof("raft.node: %x elected leader %x at term %d", r.id, r.lead, r.Term)
				} else {
					r.logger.Infof("raft.node: %x changed leader from %x to %x at term %d", r.id, lead, r.lead, r.Term)
				}
				propc = n.propc
			} else {
				r.logger.Infof("raft.node: %x lost leader %x at term %d", r.id, lead, r.Term)
				propc = nil
			}
			lead = r.lead
		}

		select {
		// TODO: 如果存在 config proposal，也许应该按照 raft dissertation 描述的方式缓冲它。
		// 当前在某些场景下它会在 Step 中被静默丢弃。
		case pm := <-propc:
			// 本地 proposal 从当前节点发起，因此在进入 raft.Step 前补上 From。
			// 如果调用方需要等待结果，stepWithWaitOption 会在 result 上等待这里返回的错误。
			m := pm.m
			m.From = r.id
			err := r.Step(m)
			if pm.result != nil {
				pm.result <- err
				close(pm.result)
			}
		case m := <-n.recvc:
			// 网络消息和本地控制消息走 recvc。
			// 对未知 peer 的响应消息没有可更新的 Progress，直接过滤即可。
			if IsResponseMsg(m.Type) && !IsLocalMsgTarget(m.From) && r.prs.Progress[m.From] == nil {
				// 过滤掉来自未知 From 的响应消息。
				break
			}
			r.Step(m)
		case cc := <-n.confc:
			// ApplyConfChange 是同步 API：run 应用配置变更后，通过 confstatec 返回新的 ConfState。
			_, okBefore := r.prs.Progress[r.id]
			cs := r.applyConfChange(cc)
			// 如果该节点被移除，则阻塞传入的 proposals。注意，只有当该节点之前就在配置中时
			// 才这样做。节点可能在自己尚不知情的情况下成为 group 成员
			// （当它们正在追赶日志且尚未拥有最新配置时），这种情况下我们不想阻塞 proposal channel。
			//
			// 注意：propc 会在 leader 变化时被重置；如果我们得知 leader 发生变化，
			// 某种程度上可能意味着我们被重新加入了？这个逻辑并不十分可靠，可能存在 bug。
			if _, okAfter := r.prs.Progress[r.id]; okBefore && !okAfter {
				var found bool
				for _, sl := range [][]uint64{cs.Voters, cs.VotersOutgoing} {
					for _, id := range sl {
						if id == r.id {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found {
					propc = nil
				}
			}
			select {
			case n.confstatec <- cs:
			case <-n.done:
			}
		case <-n.tickc:
			// Tick 只推进逻辑时间；是否触发选举或心跳由 raft 当前角色决定。
			n.rn.Tick()
		case readyc <- rd:
			// 应用层已经接收这批 Ready。acceptReady 会记录 Soft/HardState 进度、
			// 清空已交出的消息，并把 Advance 阶段需要回灌给 raft 的本地响应预先保存起来。
			n.rn.acceptReady(rd)
			if !n.rn.asyncStorageWrites {
				// 非异步存储模式下，必须等应用层调用 Advance，才能继续发出下一批 Ready。
				advancec = n.advancec
			} else {
				// 异步存储模式下没有 Advance；本地存储线程完成工作后会用响应消息推进 raft。
				rd = Ready{}
			}
			readyc = nil
		case <-advancec:
			// 应用层确认上一批 Ready 已处理到可以让 raft 前进的程度。
			// RawNode.Advance 会处理 acceptReady 预先保存的本地响应消息。
			n.rn.Advance(rd)
			rd = Ready{}
			advancec = nil
		case c := <-n.status:
			// Status 需要在 run 中读取，避免并发访问 RawNode/raft。
			c <- getStatus(r)
		case <-n.stop:
			// 关闭 done 会唤醒所有正在等待该 Node 的 API 调用。
			close(n.done)
			return
		}
	}
}

// Tick 将此 Node 的内部逻辑时钟推进一个 tick。
// 选举超时和心跳超时都以 tick 为单位。
func (n *node) Tick() {
	select {
	case n.tickc <- struct{}{}:
	case <-n.done:
	default:
		// tickc 满说明 run 循环暂时处理不过来。丢弃一个 tick 比阻塞调用者更好，
		// 但这通常意味着应用层处理 Ready 或消息太慢。
		n.rn.raft.logger.Warningf("%x A tick missed to fire. Node blocks too long!", n.rn.raft.id)
	}
}

func (n *node) Campaign(ctx context.Context) error { return n.step(ctx, pb.Message{Type: pb.MsgHup}) }

func (n *node) Propose(ctx context.Context, data []byte) error {
	return n.stepWait(ctx, pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Data: data}}})
}

func (n *node) Step(ctx context.Context, m pb.Message) error {
	// 本地消息（例如 MsgHup、MsgBeat、MsgUnreachable）只能由本节点内部生成，
	// 不应从网络层传入。Node 为兼容旧行为选择静默忽略；RawNode 会返回错误。
	if IsLocalMsg(m.Type) && !IsLocalMsgTarget(m.From) {
		// TODO: 是否应该返回错误？
		return nil
	}
	return n.step(ctx, m)
}

// confChangeToMsg 将上层传入的 ConfChange/ConfChangeV2 编码成一条 proposal 消息。
// 真正的成员变更必须先作为日志 entry 达成共识，之后应用层在 CommittedEntries 中看到它，
// 再调用 ApplyConfChange 让本地 raft 配置生效。
func confChangeToMsg(c pb.ConfChangeI) (pb.Message, error) {
	typ, data, err := pb.MarshalConfChange(c)
	if err != nil {
		return pb.Message{}, err
	}
	return pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Type: typ, Data: data}}}, nil
}

func (n *node) ProposeConfChange(ctx context.Context, cc pb.ConfChangeI) error {
	msg, err := confChangeToMsg(cc)
	if err != nil {
		return err
	}
	return n.Step(ctx, msg)
}

func (n *node) step(ctx context.Context, m pb.Message) error {
	return n.stepWithWaitOption(ctx, m, false)
}

func (n *node) stepWait(ctx context.Context, m pb.Message) error {
	return n.stepWithWaitOption(ctx, m, true)
}

// stepWithWaitOption 将消息投递给 run 循环。
//
// 非 proposal 消息走 recvc，通常不需要等待 raft.Step 的返回值；
// proposal 消息走 propc，因为 run 会根据 leader 状态启用或禁用该 channel。
// wait 为 true 时，调用方会等待 run 调用 raft.Step 后返回的错误。
// 如果投递或等待期间 ctx 结束，则返回 ctx.Err()。
func (n *node) stepWithWaitOption(ctx context.Context, m pb.Message, wait bool) error {
	if m.Type != pb.MsgProp {
		select {
		case n.recvc <- m:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-n.done:
			return ErrStopped
		}
	}
	ch := n.propc
	pm := msgWithResult{m: m}
	if wait {
		pm.result = make(chan error, 1)
	}
	select {
	case ch <- pm:
		if !wait {
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-n.done:
		return ErrStopped
	}
	select {
	case err := <-pm.result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-n.done:
		return ErrStopped
	}
	return nil
}

// Ready 暴露 run 循环发出的 Ready channel。
// 调用者不应直接向该 channel 写入数据。
func (n *node) Ready() <-chan Ready { return n.readyc }

// Advance 告诉 run 循环上一批 Ready 已经处理到可以继续推进 raft 的程度。
// 如果 Node 已停止，该调用直接返回。
func (n *node) Advance() {
	select {
	case n.advancec <- struct{}{}:
	case <-n.done:
	}
}

// ApplyConfChange 通过 run 循环串行地应用配置变更。
// 这样可以避免配置状态与正在处理的 raft 消息并发修改。
func (n *node) ApplyConfChange(cc pb.ConfChangeI) *pb.ConfState {
	var cs pb.ConfState
	select {
	case n.confc <- cc.AsV2():
	case <-n.done:
	}
	select {
	case cs = <-n.confstatec:
	case <-n.done:
	}
	return &cs
}

// Status 在 run 循环中读取 raft 状态，避免并发访问底层 RawNode。
func (n *node) Status() Status {
	c := make(chan Status)
	select {
	case n.status <- c:
		return <-c
	case <-n.done:
		return Status{}
	}
}

// ReportUnreachable 把传输层的发送失败转换成 raft 内部的 MsgUnreachable。
func (n *node) ReportUnreachable(id uint64) {
	select {
	case n.recvc <- pb.Message{Type: pb.MsgUnreachable, From: id}:
	case <-n.done:
	}
}

// ReportSnapshot 把传输层/状态机层的快照结果转换成 raft 内部的 MsgSnapStatus。
func (n *node) ReportSnapshot(id uint64, status SnapshotStatus) {
	rej := status == SnapshotFailure

	select {
	case n.recvc <- pb.Message{Type: pb.MsgSnapStatus, From: id, Reject: rej}:
	case <-n.done:
	}
}

func (n *node) TransferLeadership(ctx context.Context, lead, transferee uint64) {
	select {
	// 手动设置 From 和 To：To 指向当前 leader，From 指向希望接任的 transferee。
	// leader 收到后会尝试把自己的领导权转给 transferee。
	case n.recvc <- pb.Message{Type: pb.MsgTransferLeader, From: transferee, To: lead}:
	case <-n.done:
	case <-ctx.Done():
	}
}

// ReadIndex 将线性一致读请求包装成 MsgReadIndex。
// 请求上下文 rctx 会在 Ready.ReadStates 中原样返回，方便应用层匹配请求。
func (n *node) ReadIndex(ctx context.Context, rctx []byte) error {
	return n.step(ctx, pb.Message{Type: pb.MsgReadIndex, Entries: []pb.Entry{{Data: rctx}}})
}
