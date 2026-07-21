----------------------- MODULE InvariantViolation -----------------------
EXTENDS Naturals

CONSTANT Server
VARIABLE x

Init == x = 0

Timeout(i) ==
    /\ i \in Server
    /\ x' = x + 1

Next == \E i \in Server : Timeout(i)
Spec == Init /\ [][Next]_<<x>>
Safe == x = 0

=============================================================================
