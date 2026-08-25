------------------------------ MODULE TPCL ------------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS RM, N, V


VARIABLES 
    rmState,
    rmVars,
    tmState,
    tmPrepared,
    msgs

Messages == [type : {"Prepare", "Commit", "Abort"}, req : (1..N)] \cup [type : {"RMPrepared", "RMAborted"}, rm : RM, req : (1..N)]

TPTypeOK ==
    /\ rmState \in [RM -> [(1..N) -> {"init", "working", "prepared", "committed", "aborted"}]]
    /\ rmVars \in [RM -> SUBSET (1..V)]
    /\ tmState \in [(1..N) -> {"init", "working", "waiting", "committed", "aborted"}]
    \* /\ tmPrepared \in [(1..N) -> RM]
    /\ \A i \in (1..N):
        tmPrepared[i] \subseteq RM
    /\ msgs \subseteq  Messages
    
TPInit ==
    /\ rmState = [r \in RM |-> [i \in (1..N) |-> "init"]]
    /\ rmVars = [r \in RM |-> {}]
    /\ tmState = [i \in (1..N) |-> "init"]
    /\ tmPrepared = [i \in (1..N) |-> {}]
    /\ msgs = {}

NextRequest(i) == 
    /\ (i <= N /\ i > 0)
    /\ tmState' = [tmState EXCEPT ![i] = "working"]
    /\ UNCHANGED <<rmState, rmVars, tmPrepared, msgs>>

TMSendPrepareReq(i) ==
    /\ tmState[i] = "working"
    /\ msgs' = msgs \cup {[type |-> "Prepare", req |-> i]}
    /\ tmState' = [tmState EXCEPT ![i] = "waiting"]
    /\ UNCHANGED <<rmState, rmVars, tmPrepared>>

RMRcvPrepareReq(r, i) == 
    /\ rmState[r][i] = "init"
    /\ [type |-> "Prepare", req |-> i] \in msgs
    /\ rmState' = [rmState EXCEPT ![r][i] = "working"]
    /\ UNCHANGED <<rmVars, tmState, tmPrepared, msgs>>

RMSendPrepared(r, i, v) == 
    /\ rmState[r][i] = "working"
    /\ (rmVars[r] \cap v) = {}
    /\ rmState' = [rmState EXCEPT ![r][i] = "prepared"]
    /\ rmVars' = [rmVars EXCEPT ![r] = rmVars[r] \cup v]
    /\ msgs' = msgs \cup {[type |-> "RMPrepared", rm |-> r, req |-> i]}
    /\ UNCHANGED <<tmState, tmPrepared>>

RMSendAborted(r, i, v) == 
    /\ rmState[r][i] = "working"
    /\ (rmVars[r] \cap v) /= {}
    /\ rmState' = [rmState EXCEPT ![r][i] = "aborted"]
    /\ msgs' = msgs \cup {[type |-> "RMAborted", rm |-> r, req |-> i]}
    /\ UNCHANGED <<rmVars, tmState, tmPrepared>>

TMRcvPrepared(r, i) ==
    /\ tmState[i] = "waiting"
    /\ [type |-> "RMPrepared", rm |-> r, req |-> i] \in msgs
    /\ r \notin tmPrepared[i]
    /\ tmPrepared' = [tmPrepared EXCEPT ![i] = tmPrepared[i] \cup {r}]
    /\ UNCHANGED <<rmState, rmVars, tmState, msgs>>

TMSendGlobalCommit(i) == 
    /\ tmState[i] = "waiting"
    /\ tmPrepared[i] = RM
    /\ tmState' = [tmState EXCEPT ![i] = "committed"]
    /\ msgs' = msgs \cup {[type |-> "Commit", req |-> i]}
    /\ UNCHANGED <<rmState, rmVars, tmPrepared>>

TMRcvAborted(r, i) ==
    /\ tmState[i] = "waiting"
    /\ [type |-> "RMAborted", rm |-> r, req |-> i] \in msgs
    /\ tmState' = [tmState EXCEPT ![i] = "aborted"]
    /\ msgs' = msgs \cup {[type |-> "Abort", req |-> i]}
    /\ UNCHANGED <<rmState, rmVars, tmPrepared>>

RMRcvGlobalCommit(r, i, v) == 
    /\ rmState[r][i] = "prepared"
    /\ [type |-> "Commit", req |-> i] \in msgs
    /\ rmState' = [rmState EXCEPT ![r][i] = "committed"]
    /\ rmVars' = [rmVars EXCEPT ![r] = rmVars[r] \ v]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

RMRcvGlobalAbort(r, i, v) == 
    /\ (rmState[r][i] = "working"  \/ rmState[r][i] = "init" \/ rmState[r][i] = "prepared") \* 
    /\ [type |-> "Abort", req |-> i] \in msgs
    /\ rmState' = [rmState EXCEPT ![r][i] = "aborted"]
    /\ rmVars' = [rmVars EXCEPT ![r] = rmVars[r] \ v]
    /\ UNCHANGED <<tmState, tmPrepared, msgs>>

TPNext ==
    \/ \E r \in RM, i \in (1..N), v \in SUBSET (1..V):
        NextRequest(i) \/ TMSendPrepareReq(i) \/ RMRcvPrepareReq(r, i) \/ RMSendPrepared(r, i, v) \/ RMSendAborted(r, i, v)
        \/ TMRcvPrepared(r, i) \/ TMSendGlobalCommit(i) \/ TMRcvAborted(r, i) \/ RMRcvGlobalCommit(r, i, v) \/ RMRcvGlobalAbort(r, i, v)

TPSpec == TPInit /\ [][TPNext]_<<rmState, rmVars, tmState, tmPrepared, msgs>>

TCConsistent ==
    \A r1, r2 \in RM: \A i \in (1..N): ~ /\ rmState[r1][i] = "aborted"
                                            /\ rmState[r2][i] = "committed"

THEOREM TPSpec => [](TPTypeOK /\ TCConsistent)

=====