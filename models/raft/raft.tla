-------------------------------- MODULE raft --------------------------------
\* 面向 ModelFuzz-NG controlled execution 的 Raft 状态机。
\* 网络队列由 Runtime 管理，因此模型不包含 messages bag；DeliverMessage 会由
\* RaftActionMapper 直接映射为下面四类消息处理动作。

EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS Server, MaxValue, Follower, Candidate, Leader, Nil,
          MaxLogIndex, LargestTerm

VARIABLES currentTerm, state, votedFor,
          log, commitIndex,
          votesResponded, votesGranted,
          nextIndex, matchIndex

serverVars    == <<currentTerm, state, votedFor>>
logVars       == <<log, commitIndex>>
candidateVars == <<votesResponded, votesGranted>>
leaderVars    == <<nextIndex, matchIndex>>
vars          == <<serverVars, candidateVars, leaderVars, logVars>>

Quorum == {q \in SUBSET Server : Cardinality(q) * 2 > Cardinality(Server)}
Terms == 0..LargestTerm
LogIndices == 0..MaxLogIndex
AllValues == 1..MaxValue \cup {Nil}
LastTerm(l) == IF Len(l) = 0 THEN 0 ELSE l[Len(l)].term
Min(s) == CHOOSE x \in s : \A y \in s : x <= y
Max(s) == CHOOSE x \in s : \A y \in s : x >= y

Init ==
    /\ currentTerm = [i \in Server |-> 0]
    /\ state = [i \in Server |-> Follower]
    /\ votedFor = [i \in Server |-> Nil]
    /\ log = [i \in Server |-> << >>]
    /\ commitIndex = [i \in Server |-> 0]
    /\ votesResponded = [i \in Server |-> {}]
    /\ votesGranted = [i \in Server |-> {}]
    /\ nextIndex = [i \in Server |-> [j \in Server |-> 1]]
    /\ matchIndex = [i \in Server |-> [j \in Server |-> 0]]

\* 节点 i 发生选举超时。自然超时和强制超时在该抽象层语义相同。
Timeout(i) ==
    /\ state[i] \in {Follower, Candidate}
    /\ currentTerm[i] < LargestTerm
    /\ state' = [state EXCEPT ![i] = Candidate]
    /\ currentTerm' = [currentTerm EXCEPT ![i] = @ + 1]
    /\ votedFor' = [votedFor EXCEPT ![i] = i]
    /\ votesResponded' = [votesResponded EXCEPT ![i] = {i}]
    /\ votesGranted' = [votesGranted EXCEPT ![i] = {i}]
    /\ UNCHANGED <<leaderVars, logVars>>

BecomeLeader(i) ==
    /\ state[i] = Candidate
    /\ votesGranted[i] \in Quorum
    /\ state' = [state EXCEPT ![i] = Leader]
    /\ nextIndex' = [nextIndex EXCEPT
                         ![i] = [j \in Server |-> Len(log[i]) + 1]]
    /\ matchIndex' = [matchIndex EXCEPT
                          ![i] = [j \in Server |-> 0]]
    /\ UNCHANGED <<currentTerm, votedFor, candidateVars, logVars>>

\* v=Nil(配置中为 0) 表示 etcd-raft 成为 leader 时自动追加的 no-op。
ClientRequest(i, v) ==
    /\ state[i] = Leader
    /\ Len(log[i]) < MaxLogIndex
    /\ log' = [log EXCEPT
                   ![i] = Append(@, [term |-> currentTerm[i], value |-> v])]
    /\ UNCHANGED <<serverVars, candidateVars, leaderVars, commitIndex>>

AdvanceCommitIndex(i) ==
    /\ state[i] = Leader
    /\ LET Agree(index) == {i} \cup {j \in Server : matchIndex[i][j] >= index}
           agreed == {index \in 1..Len(log[i]) : Agree(index) \in Quorum}
           newCommit == IF /\ agreed /= {}
                           /\ log[i][Max(agreed)].term = currentTerm[i]
                        THEN Max(agreed)
                        ELSE commitIndex[i]
       IN commitIndex' = [commitIndex EXCEPT ![i] = newCommit]
    /\ UNCHANGED <<serverVars, candidateVars, leaderVars, log>>

\* i 是接收者，j 是发送者。参数名与原 RaftActionMapper 保持一致。
HandleRequestVoteRequest(i, j, lTerm, lIndex, term) ==
    LET cTerm == IF term > currentTerm[i] THEN term ELSE currentTerm[i]
        cState == IF term > currentTerm[i] THEN Follower ELSE state[i]
        oldVote == IF term > currentTerm[i] THEN Nil ELSE votedFor[i]
        logOK == \/ lTerm > LastTerm(log[i])
                 \/ /\ lTerm = LastTerm(log[i])
                    /\ lIndex >= Len(log[i])
        grant == /\ term = cTerm
                 /\ logOK
                 /\ oldVote \in {Nil, j}
    IN
    /\ i /= j
    /\ currentTerm' = [currentTerm EXCEPT ![i] = cTerm]
    /\ state' = [state EXCEPT ![i] = cState]
    /\ votedFor' = [votedFor EXCEPT ![i] = IF grant THEN j ELSE oldVote]
    /\ UNCHANGED <<candidateVars, leaderVars, logVars>>

HandleRequestVoteResponse(i, j, term, grant) ==
    LET newer == term > currentTerm[i]
        current == term = currentTerm[i]
        canCount == /\ current
                    /\ state[i] = Candidate
    IN
    /\ i /= j
    /\ currentTerm' = [currentTerm EXCEPT ![i] = IF newer THEN term ELSE @]
    /\ state' = [state EXCEPT ![i] = IF newer THEN Follower ELSE @]
    /\ votedFor' = [votedFor EXCEPT ![i] = IF newer THEN Nil ELSE @]
    /\ votesResponded' = [votesResponded EXCEPT
                              ![i] = IF canCount THEN @ \cup {j} ELSE @]
    /\ votesGranted' = [votesGranted EXCEPT
                            ![i] = IF canCount /\ grant THEN @ \cup {j} ELSE @]
    /\ UNCHANGED <<leaderVars, logVars>>

LogMatches(i, pLogIndex, pLogTerm) ==
    \/ pLogIndex = 0
    \/ /\ pLogIndex <= Len(log[i])
       /\ log[i][pLogIndex].term = pLogTerm

\* MsgApp 不携带 entry 时只确认前缀并传播 commit index。
HandleNilAppendEntriesRequest(i, j, pLogIndex, pLogTerm, term, cIndex) ==
    LET acceptable == /\ term >= currentTerm[i]
                       /\ LogMatches(i, pLogIndex, pLogTerm)
        newTerm == IF term > currentTerm[i] THEN term ELSE currentTerm[i]
        newCommit == IF acceptable
                     THEN Min({cIndex, Len(log[i])})
                     ELSE commitIndex[i]
    IN
    /\ i /= j
    /\ currentTerm' = [currentTerm EXCEPT ![i] = newTerm]
    /\ state' = [state EXCEPT
                     ![i] = IF term >= currentTerm[i] THEN Follower ELSE @]
    /\ votedFor' = [votedFor EXCEPT
                        ![i] = IF term > currentTerm[i] THEN Nil ELSE @]
    /\ commitIndex' = [commitIndex EXCEPT ![i] = newCommit]
    /\ UNCHANGED <<candidateVars, leaderVars, log>>

\* 与原 ModelFuzz 一样，单次只抽象 MsgApp 中的第一条 entry。
HandleAppendEntriesRequest(i, j, pLogIndex, pLogTerm,
                           term, entryTerm, entryValue, cIndex) ==
    LET acceptable == /\ term >= currentTerm[i]
                       /\ LogMatches(i, pLogIndex, pLogTerm)
        entry == [term |-> entryTerm, value |-> entryValue]
        prefix == SubSeq(log[i], 1, pLogIndex)
        same == /\ Len(log[i]) > pLogIndex
                /\ log[i][pLogIndex + 1] = entry
        newLog == IF ~acceptable
                  THEN log[i]
                  ELSE IF same THEN log[i] ELSE Append(prefix, entry)
        newCommit == IF acceptable
                     THEN Min({cIndex, Len(newLog)})
                     ELSE commitIndex[i]
    IN
    /\ i /= j
    /\ pLogIndex < MaxLogIndex
    /\ currentTerm' = [currentTerm EXCEPT
                           ![i] = IF term > currentTerm[i] THEN term ELSE @]
    /\ state' = [state EXCEPT
                     ![i] = IF term >= currentTerm[i] THEN Follower ELSE @]
    /\ votedFor' = [votedFor EXCEPT
                        ![i] = IF term > currentTerm[i] THEN Nil ELSE @]
    /\ log' = [log EXCEPT ![i] = newLog]
    /\ commitIndex' = [commitIndex EXCEPT ![i] = newCommit]
    /\ UNCHANGED <<candidateVars, leaderVars>>

HandleAppendEntriesResponse(i, j, term, success, mIndex) ==
    LET current == term = currentTerm[i]
        newer == term > currentTerm[i]
        canUse == /\ current
                  /\ state[i] = Leader
        updatedMatch == IF canUse /\ success
                        THEN [matchIndex EXCEPT ![i][j] = mIndex]
                        ELSE matchIndex
        updatedNext == IF ~canUse
                       THEN nextIndex
                       ELSE IF success
                            THEN [nextIndex EXCEPT ![i][j] = mIndex + 1]
                            ELSE [nextIndex EXCEPT
                                      ![i][j] = Max({1, nextIndex[i][j] - 1})]
        Agree(index) == {i} \cup {k \in Server : updatedMatch[i][k] >= index}
        agreed == {index \in 1..Len(log[i]) : Agree(index) \in Quorum}
        newCommit == IF /\ canUse
                         /\ agreed /= {}
                         /\ log[i][Max(agreed)].term = currentTerm[i]
                     THEN Max(agreed)
                     ELSE commitIndex[i]
    IN
    /\ i /= j
    /\ currentTerm' = [currentTerm EXCEPT ![i] = IF newer THEN term ELSE @]
    /\ state' = [state EXCEPT ![i] = IF newer THEN Follower ELSE @]
    /\ votedFor' = [votedFor EXCEPT ![i] = IF newer THEN Nil ELSE @]
    /\ nextIndex' = updatedNext
    /\ matchIndex' = updatedMatch
    /\ commitIndex' = [commitIndex EXCEPT ![i] = newCommit]
    /\ UNCHANGED <<candidateVars, log>>

Next ==
    \/ \E i \in Server : Timeout(i)
    \/ \E i \in Server : BecomeLeader(i)
    \/ \E i \in Server, v \in AllValues : ClientRequest(i, v)
    \/ \E i, j \in Server, term, lTerm \in Terms, lIndex \in LogIndices :
           HandleRequestVoteRequest(i, j, lTerm, lIndex, term)
    \/ \E i, j \in Server, term \in Terms, grant \in BOOLEAN :
           HandleRequestVoteResponse(i, j, term, grant)
    \/ \E i, j \in Server, term, pLogTerm \in Terms,
          pLogIndex, cIndex \in LogIndices :
           HandleNilAppendEntriesRequest(i, j, pLogIndex, pLogTerm, term, cIndex)
    \/ \E i, j \in Server, term, pLogTerm, entryTerm \in Terms,
          pLogIndex, cIndex \in LogIndices, entryValue \in AllValues :
           HandleAppendEntriesRequest(i, j, pLogIndex, pLogTerm,
                                      term, entryTerm, entryValue, cIndex)
    \/ \E i, j \in Server, term \in Terms, success \in BOOLEAN,
          mIndex \in LogIndices :
           HandleAppendEntriesResponse(i, j, term, success, mIndex)
    \/ \E i \in Server : AdvanceCommitIndex(i)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ currentTerm \in [Server -> Nat]
    /\ state \in [Server -> {Follower, Candidate, Leader}]
    /\ votedFor \in [Server -> Server \cup {Nil}]
    /\ commitIndex \in [Server -> Nat]

OnlyOneLeader ==
    \A i, j \in Server :
        (i /= j /\ currentTerm[i] = currentTerm[j] /\ state[i] = Leader)
        => state[j] /= Leader

=============================================================================
