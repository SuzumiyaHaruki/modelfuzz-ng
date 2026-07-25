---------------------- MODULE raft_storage_snapshot ----------------------
\* Storage/Snapshot 边界与 Leader snapshot progress 模型。
\*
\* 基础 Raft 协议状态由 raft.tla 提供。本模块保留完整逻辑日志作为 ghost
\* history，不物理删除 log 前缀；firstIndex 只表示底层 Storage 当前仍可读取
\* 的第一个日志索引。这样压缩不会破坏基础模型的日志匹配与提交前缀不变量。

EXTENDS raft

VARIABLES appliedIndex, snapshotIndex, snapshotTerm, firstIndex,
          pendingSnapshot

storageVars == <<appliedIndex, snapshotIndex, snapshotTerm, firstIndex,
                 pendingSnapshot>>
allVars     == <<vars, storageVars>>

StorageInit ==
    /\ Init
    /\ appliedIndex = [i \in Server |-> 0]
    /\ snapshotIndex = [i \in Server |-> 0]
    /\ snapshotTerm = [i \in Server |-> 0]
    /\ firstIndex = [i \in Server |-> 1]
    /\ pendingSnapshot = [i \in Server |-> [j \in Server |-> 0]]

\* 基础动作必须显式保持 storageVars，确保 controlled TLC 的后继状态完整且唯一。
StorageRemoveFromActive(i) ==
    /\ RemoveFromActive(i)
    /\ UNCHANGED storageVars

StorageAddToActive(i) ==
    /\ AddToActive(i)
    /\ pendingSnapshot' = [pendingSnapshot EXCEPT
                               ![i] = [j \in Server |-> 0]]
    /\ UNCHANGED <<appliedIndex, snapshotIndex, snapshotTerm, firstIndex>>

StorageTimeout(i) ==
    /\ Timeout(i)
    /\ UNCHANGED storageVars

StorageBecomeLeader(i) ==
    /\ BecomeLeader(i)
    /\ pendingSnapshot' = [pendingSnapshot EXCEPT
                               ![i] = [j \in Server |-> 0]]
    /\ UNCHANGED <<appliedIndex, snapshotIndex, snapshotTerm, firstIndex>>

StorageClientRequest(i, v) ==
    /\ ClientRequest(i, v)
    /\ UNCHANGED storageVars

StorageAdvanceCommitIndex(i) ==
    /\ AdvanceCommitIndex(i)
    /\ UNCHANGED storageVars

StorageHandleRequestVoteRequest(i, j, lTerm, lIndex, term) ==
    /\ HandleRequestVoteRequest(i, j, lTerm, lIndex, term)
    /\ UNCHANGED storageVars

StorageHandleRequestVoteResponse(i, j, term, grant) ==
    /\ HandleRequestVoteResponse(i, j, term, grant)
    /\ UNCHANGED storageVars

StorageHandleNilAppendEntriesRequest(i, j, pLogIndex, pLogTerm, term, cIndex) ==
    /\ HandleNilAppendEntriesRequest(i, j, pLogIndex, pLogTerm, term, cIndex)
    /\ UNCHANGED storageVars

StorageHandleAppendEntriesRequest(i, j, pLogIndex, pLogTerm,
                                  term, entryTerm, entryValue, cIndex) ==
    /\ HandleAppendEntriesRequest(i, j, pLogIndex, pLogTerm,
                                  term, entryTerm, entryValue, cIndex)
    /\ UNCHANGED storageVars

StorageHandleAppendEntriesResponse(i, j, term, success, mIndex) ==
    LET oldPending == pendingSnapshot[i][j]
        staleSnapshotReject ==
            /\ ~success
            /\ term = currentTerm[i]
            /\ state[i] = Leader
            /\ oldPending > 0
            /\ mIndex # nextIndex[i][j] - 1
        resumesReplication ==
            /\ success
            /\ term = currentTerm[i]
            /\ state[i] = Leader
            /\ oldPending > 0
            \* etcd-raft only leaves StateSnapshot when this response advances
            \* Match far enough that the leader's current Storage can resume.
            /\ mIndex > matchIndex[i][j]
            /\ mIndex + 1 >= firstIndex[i]
    IN
    \* In StateSnapshot, etcd-raft ignores a rejection unless it refers to
    \* exactly Next-1. The base model has no progress-state variable, so keep
    \* this snapshot-specific stale-response rule in the storage wrapper.
    /\ IF staleSnapshotReject
       THEN UNCHANGED vars
       ELSE HandleAppendEntriesResponse(i, j, term, success, mIndex)
    /\ pendingSnapshot' = [pendingSnapshot EXCEPT
          ![i][j] = IF resumesReplication THEN 0 ELSE oldPending]
    /\ UNCHANGED <<appliedIndex, snapshotIndex, snapshotTerm, firstIndex>>

\* Adapter 已经应用一条或一批 committed entries。模型只跟踪最终应用边界，
\* 允许一次动作跨过多个索引，但绝不允许越过 commit 或向后移动。
ApplyCommitted(i, index) ==
    /\ i \in currentActive
    /\ appliedIndex[i] < index
    /\ index <= commitIndex[i]
    /\ index <= Len(log[i])
    /\ appliedIndex' = [appliedIndex EXCEPT ![i] = index]
    /\ UNCHANGED <<vars, snapshotIndex, snapshotTerm, firstIndex,
                    pendingSnapshot>>

\* 本地 CreateSnapshot 只能覆盖已经应用的逻辑日志，并记录该边界处的 term。
CreateSnapshot(i, index, term) ==
    /\ i \in currentActive
    /\ snapshotIndex[i] < index
    /\ index <= appliedIndex[i]
    /\ index <= Len(log[i])
    /\ term = log[i][index].term
    /\ snapshotIndex' = [snapshotIndex EXCEPT ![i] = index]
    /\ snapshotTerm' = [snapshotTerm EXCEPT ![i] = term]
    /\ UNCHANGED <<vars, appliedIndex, firstIndex, pendingSnapshot>>

\* MemoryStorage.Compact(index) 使新的 firstIndex 等于 index + 1。
\* 压缩边界必须前进，且被当前 snapshot 完整覆盖。
CompactLog(i, index) ==
    /\ i \in currentActive
    /\ firstIndex[i] <= index
    /\ index <= snapshotIndex[i]
    /\ firstIndex' = [firstIndex EXCEPT ![i] = index + 1]
    /\ UNCHANGED <<vars, appliedIndex, snapshotIndex, snapshotTerm,
                    pendingSnapshot>>

\* Leader 在 peer 的 nextIndex 仍落在可读 Storage 窗口内时可以继续增量复制。
EntryAvailable(i, j) ==
    /\ nextIndex[i][j] >= firstIndex[i]
    /\ nextIndex[i][j] <= Len(log[i]) + 1

NeedSnapshot(i, j) ==
    /\ i /= j
    /\ i \in currentActive
    /\ state[i] = Leader
    /\ pendingSnapshot[i][j] = 0
    /\ nextIndex[i][j] < firstIndex[i]

SnapshotAvailable(i) ==
    IF /\ snapshotIndex[i] > 0
       /\ snapshotIndex[i] <= Len(log[i])
    THEN /\ snapshotIndex[i] <= appliedIndex[i]
         /\ firstIndex[i] - 1 <= snapshotIndex[i]
         /\ snapshotTerm[i] = log[i][snapshotIndex[i]].term
    ELSE FALSE

SnapshotPayloadValid(i, index, sTerm) ==
    /\ index > 0
    /\ index <= snapshotIndex[i]
    /\ index <= appliedIndex[i]
    /\ index <= commitIndex[i]
    /\ (IF index <= Len(log[i])
        THEN sTerm = log[i][index].term
        ELSE FALSE)

\* Leader 已判定增量窗口消失并成功取得非空 snapshot，随后进入 pending
\* snapshot progress；Follower outcome 由 Install/FastForward/Reject 表达。
SendSnapshot(i, j, index, term, match, next, pending) ==
    /\ NeedSnapshot(i, j)
    /\ SnapshotAvailable(i)
    /\ index = snapshotIndex[i]
    /\ term = snapshotTerm[i]
    /\ match = matchIndex[i][j]
    /\ pending = index
    /\ next = pending + 1
    /\ next <= Len(log[i]) + 1
    /\ nextIndex' = [nextIndex EXCEPT ![i][j] = next]
    /\ pendingSnapshot' = [pendingSnapshot EXCEPT ![i][j] = pending]
    /\ UNCHANGED <<serverVars, candidateVars, logVars, currentActive,
                    matchIndex, appliedIndex, snapshotIndex, snapshotTerm,
                    firstIndex>>

\* Follower 没有 snapshot 边界处的同 term entry 时，etcd-raft Restore 会用
\* snapshot 重建日志基线并原子推进 commit/applied/Storage 边界。log 仍保存为
\* ghost prefix，以便继续检查 LogMatching 与 CommittedPrefixAgreement。
InstallSnapshot(i, j, index, sTerm, term) ==
    /\ i /= j
    /\ j \in currentActive
    /\ SnapshotPayloadValid(i, index, sTerm)
    /\ term >= currentTerm[j]
    /\ \/ term > currentTerm[j]
       \/ state[j] /= Leader
    /\ index > commitIndex[j]
    /\ (IF index > Len(log[j])
        THEN TRUE
        ELSE log[j][index].term /= sTerm)
    /\ currentTerm' = [currentTerm EXCEPT ![j] = term]
    /\ state' = [state EXCEPT ![j] = Follower]
    /\ votedFor' = [votedFor EXCEPT
                         ![j] = IF term > currentTerm[j] THEN Nil ELSE @]
    /\ log' = [log EXCEPT ![j] = SubSeq(log[i], 1, index)]
    /\ commitIndex' = [commitIndex EXCEPT ![j] = index]
    /\ appliedIndex' = [appliedIndex EXCEPT ![j] = index]
    /\ snapshotIndex' = [snapshotIndex EXCEPT ![j] = index]
    /\ snapshotTerm' = [snapshotTerm EXCEPT ![j] = sTerm]
    /\ firstIndex' = [firstIndex EXCEPT ![j] = index + 1]
    /\ UNCHANGED <<candidateVars, leaderVars, currentActive, pendingSnapshot>>

\* 若 Follower 已有 snapshot index/term 对应的 entry，etcd-raft 不安装
\* snapshot，而是 fast-forward commit；随后 Ready 中的 committed entries 仍由
\* ApplyCommitted 表示。
FastForwardSnapshot(i, j, index, sTerm, term) ==
    /\ i /= j
    /\ j \in currentActive
    /\ SnapshotPayloadValid(i, index, sTerm)
    /\ term >= currentTerm[j]
    /\ \/ term > currentTerm[j]
       \/ state[j] /= Leader
    /\ index > commitIndex[j]
    /\ (IF index <= Len(log[j])
        THEN log[j][index].term = sTerm
        ELSE FALSE)
    /\ currentTerm' = [currentTerm EXCEPT ![j] = term]
    /\ state' = [state EXCEPT ![j] = Follower]
    /\ votedFor' = [votedFor EXCEPT
                         ![j] = IF term > currentTerm[j] THEN Nil ELSE @]
    /\ commitIndex' = [commitIndex EXCEPT ![j] = index]
    /\ UNCHANGED <<candidateVars, leaderVars, log, currentActive,
                    appliedIndex, snapshotIndex, snapshotTerm, firstIndex,
                    pendingSnapshot>>

\* 旧 term、已不领先于 commit 的 snapshot，或同 term 发给当前 Leader 的
\* snapshot 都被拒绝/忽略。Candidate 收到同 term MsgSnap 时仍会降级。
RejectSnapshot(i, j, index, sTerm, term) ==
    /\ i /= j
    /\ j \in currentActive
    /\ SnapshotPayloadValid(i, index, sTerm)
    /\ \/ term < currentTerm[j]
       \/ index <= commitIndex[j]
       \/ /\ term = currentTerm[j]
             /\ state[j] = Leader
    /\ currentTerm' = [currentTerm EXCEPT
                            ![j] = IF term > currentTerm[j] THEN term ELSE @]
    /\ state' = [state EXCEPT
                      ![j] = IF \/ term > currentTerm[j]
                                  \/ /\ term = currentTerm[j]
                                        /\ state[j] = Candidate
                              THEN Follower
                              ELSE @]
    /\ votedFor' = [votedFor EXCEPT
                         ![j] = IF term > currentTerm[j] THEN Nil ELSE @]
    /\ UNCHANGED <<candidateVars, leaderVars, logVars, currentActive,
                    appliedIndex, snapshotIndex, snapshotTerm, firstIndex,
                    pendingSnapshot>>

\* MsgSnapStatus 是应用层对 snapshot 传输结果的本地反馈。成功时从 snapshot
\* 边界之后继续 probe；失败时回退到已确认 match 的下一项。两种结果都会退出
\* pending snapshot。具体的 heartbeat pacing 不属于这个安全性抽象。
HandleSnapshotStatus(i, j, success, next) ==
    LET oldPending == pendingSnapshot[i][j]
        expectedNext == IF success
                        THEN Max({matchIndex[i][j] + 1, oldPending + 1})
                        ELSE matchIndex[i][j] + 1
    IN
    /\ i /= j
    /\ i \in currentActive
    /\ state[i] = Leader
    /\ oldPending > 0
    /\ next = expectedNext
    /\ next \in 1..(MaxLogIndex + 1)
    /\ nextIndex' = [nextIndex EXCEPT ![i][j] = next]
    /\ pendingSnapshot' = [pendingSnapshot EXCEPT ![i][j] = 0]
    /\ UNCHANGED <<serverVars, candidateVars, logVars, currentActive,
                    matchIndex, appliedIndex, snapshotIndex, snapshotTerm,
                    firstIndex>>

StorageNext ==
    \/ \E i \in Server : StorageRemoveFromActive(i)
    \/ \E i \in Server : StorageAddToActive(i)
    \/ \E i \in Server : StorageTimeout(i)
    \/ \E i \in Server : StorageBecomeLeader(i)
    \/ \E i \in Server, v \in AllValues : StorageClientRequest(i, v)
    \/ \E i, j \in Server, term, lTerm \in Terms, lIndex \in LogIndices :
           StorageHandleRequestVoteRequest(i, j, lTerm, lIndex, term)
    \/ \E i, j \in Server, term \in Terms, grant \in BOOLEAN :
           StorageHandleRequestVoteResponse(i, j, term, grant)
    \/ \E i, j \in Server, term, pLogTerm \in Terms,
          pLogIndex, cIndex \in LogIndices :
           StorageHandleNilAppendEntriesRequest(i, j, pLogIndex, pLogTerm, term, cIndex)
    \/ \E i, j \in Server, term, pLogTerm, entryTerm \in Terms,
          pLogIndex, cIndex \in LogIndices, entryValue \in AllValues :
           StorageHandleAppendEntriesRequest(i, j, pLogIndex, pLogTerm,
                                             term, entryTerm, entryValue, cIndex)
    \/ \E i, j \in Server, term \in Terms, success \in BOOLEAN,
          mIndex \in LogIndices :
           StorageHandleAppendEntriesResponse(i, j, term, success, mIndex)
    \/ \E i \in Server : StorageAdvanceCommitIndex(i)
    \/ \E i \in Server, index \in LogIndices : ApplyCommitted(i, index)
    \/ \E i \in Server, index \in LogIndices, term \in Terms :
           CreateSnapshot(i, index, term)
    \/ \E i \in Server, index \in LogIndices : CompactLog(i, index)
    \/ \E i, j \in Server, index, match, pending \in LogIndices,
          term \in Terms, next \in 1..(MaxLogIndex + 1) :
           SendSnapshot(i, j, index, term, match, next, pending)
    \/ \E i, j \in Server, index \in LogIndices, sTerm, term \in Terms :
           InstallSnapshot(i, j, index, sTerm, term)
    \/ \E i, j \in Server, index \in LogIndices, sTerm, term \in Terms :
           FastForwardSnapshot(i, j, index, sTerm, term)
    \/ \E i, j \in Server, index \in LogIndices, sTerm, term \in Terms :
           RejectSnapshot(i, j, index, sTerm, term)
    \/ \E i, j \in Server, success \in BOOLEAN,
          next \in 1..(MaxLogIndex + 1) :
           HandleSnapshotStatus(i, j, success, next)

StorageSpec == StorageInit /\ [][StorageNext]_allVars

\* 严格 HTTP 服务按外部事件绑定 Storage* 操作符，不枚举 StorageNext。
StorageControlledNext == UNCHANGED allVars

StorageTypeOK ==
    /\ TypeOK
    /\ appliedIndex \in [Server -> LogIndices]
    /\ snapshotIndex \in [Server -> LogIndices]
    /\ snapshotTerm \in [Server -> Terms]
    /\ firstIndex \in [Server -> 1..(MaxLogIndex + 1)]
    /\ pendingSnapshot \in [Server -> [Server -> LogIndices]]

\* 验证本地应用、快照创建和日志压缩之间的边界关系。
SnapshotStorageBoundary ==
    /\ \A i \in Server : snapshotIndex[i] <= appliedIndex[i]
    /\ \A i \in Server : appliedIndex[i] <= commitIndex[i]
    /\ \A i \in Server : firstIndex[i] - 1 <= snapshotIndex[i]
    /\ \A i \in Server :
           IF snapshotIndex[i] = 0
           THEN snapshotTerm[i] = 0
           ELSE snapshotTerm[i] = log[i][snapshotIndex[i]].term

\* Progress 只在 Leader 行上有语义；其他行是节点上次任 Leader 时留下的易失状态。
LeaderProgressSafe ==
    /\ \A i, j \in Server :
           state[i] = Leader
           => /\ matchIndex[i][j] < nextIndex[i][j]
              /\ matchIndex[i][j] <= Len(log[i])
              /\ nextIndex[i][j] <= Len(log[i]) + 1
              /\ \/ pendingSnapshot[i][j] = 0
                 \/ pendingSnapshot[i][j] <= snapshotIndex[i]
    /\ \A i, j \in Server : NeedSnapshot(i, j) => SnapshotAvailable(i)

\* StorageNext 的任意消息参数并不代表一个受网络协议约束的 Raft execution。
\* 这个两节点小模型从一个合法 Leader progress 边界出发，穷举
\* Create/Compact/Send、Follower outcome 和 MsgSnapStatus；真实协议组合继续由
\* strict controlled trace 检查。
ProgressCheckLeader == CHOOSE i \in Server : TRUE
ProgressCheckPeer == CHOOSE j \in Server : j /= ProgressCheckLeader

ProgressCheckInit ==
    /\ Cardinality(Server) = 2
    /\ currentTerm = [i \in Server |-> 1]
    /\ state = [i \in Server |->
                    IF i = ProgressCheckLeader THEN Leader ELSE Follower]
    /\ votedFor = [i \in Server |->
                       IF i = ProgressCheckLeader THEN i ELSE Nil]
    /\ log = [i \in Server |->
                  IF i = ProgressCheckLeader
                  THEN <<[term |-> 1, value |-> Nil]>>
                  ELSE << >>]
    /\ commitIndex = [i \in Server |->
                          IF i = ProgressCheckLeader THEN 1 ELSE 0]
    /\ votesResponded = [i \in Server |-> {}]
    /\ votesGranted = [i \in Server |-> {}]
    /\ nextIndex = [i \in Server |-> [j \in Server |->
                         IF /\ i = ProgressCheckLeader
                            /\ j = ProgressCheckLeader
                         THEN 2
                         ELSE 1]]
    /\ matchIndex = [i \in Server |-> [j \in Server |-> 0]]
    /\ currentActive = Server
    /\ appliedIndex = [i \in Server |->
                           IF i = ProgressCheckLeader THEN 1 ELSE 0]
    /\ snapshotIndex = [i \in Server |-> 0]
    /\ snapshotTerm = [i \in Server |-> 0]
    /\ firstIndex = [i \in Server |-> 1]
    /\ pendingSnapshot = [i \in Server |-> [j \in Server |-> 0]]

ProgressCheckNext ==
    \/ \E index \in LogIndices, term \in Terms :
           CreateSnapshot(ProgressCheckLeader, index, term)
    \/ \E index \in LogIndices : CompactLog(ProgressCheckLeader, index)
    \/ \E index, match, pending \in LogIndices,
          term \in Terms, next \in 1..(MaxLogIndex + 1) :
           SendSnapshot(ProgressCheckLeader, ProgressCheckPeer,
                        index, term, match, next, pending)
    \/ \E index \in LogIndices, sTerm, term \in Terms :
           /\ pendingSnapshot[ProgressCheckLeader][ProgressCheckPeer] = index
           /\ InstallSnapshot(ProgressCheckLeader, ProgressCheckPeer,
                              index, sTerm, term)
    \/ /\ snapshotIndex[ProgressCheckPeer] = 1
       /\ StorageHandleAppendEntriesResponse(ProgressCheckLeader,
                                             ProgressCheckPeer, 1, TRUE, 1)
    \/ \E index \in LogIndices, sTerm, term \in Terms :
           RejectSnapshot(ProgressCheckLeader, ProgressCheckPeer,
                          index, sTerm, term)
    \/ \E success \in BOOLEAN, next \in 1..(MaxLogIndex + 1) :
           HandleSnapshotStatus(ProgressCheckLeader, ProgressCheckPeer,
                                success, next)

ProgressCheckSpec == ProgressCheckInit /\ [][ProgressCheckNext]_allVars

=============================================================================
