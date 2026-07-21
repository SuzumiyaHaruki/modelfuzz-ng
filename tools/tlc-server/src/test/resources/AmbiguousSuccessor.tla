----------------------- MODULE AmbiguousSuccessor -----------------------
EXTENDS Naturals

CONSTANT Server
VARIABLE x

Init == x = 0

Timeout(i) ==
    /\ i \in Server
    /\ x' \in {1, 2}

Next == \E i \in Server : Timeout(i)
Spec == Init /\ [][Next]_<<x>>

=============================================================================
