---- MODULE RAFT_3_5 ----
EXTENDS raft_enhanced, TLC

\* CONSTANT definitions @modelParameterConstants:0Follower
const_168354219750410000 == 
"follower"
----

\* CONSTANT definitions @modelParameterConstants:1Leader
const_168354219750711000 == 
"leader"
----

\* CONSTANT definitions @modelParameterConstants:2LargestTerm
const_168354219750712000 == 
10
----

\* CONSTANT definitions @modelParameterConstants:3Nil
const_168354219750713000 == 
0
----

\* CONSTANT definitions @modelParameterConstants:4MaxLogIndex
const_168354219750714000 == 
10
----

\* CONSTANT definitions @modelParameterConstants:5Candidate
const_168354219750715000 == 
"candidate"
----

\* CONSTANT definitions @modelParameterConstants:6MaxValue
const_168354219750716000 == 
3
----

\* CONSTANT definitions @modelParameterConstants:7Server
const_168354219750717000 == 
{1,2,3,4,5}
----

=============================================================================
\* Modification History
\* Created Mon May 08 12:36:37 CEST 2023 by srinidhin
