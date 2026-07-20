package raft

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
)

func TestCheckerAcceptsValidLeaderAndReplicationProgress(t *testing.T) {
	checker := New()
	before := observation(
		node(1, "candidate", 1, 0, 0, 0, ""),
		node(2, "follower", 1, 0, 0, 0, ""),
	)
	if findings := checker.Reset(before); len(findings) != 0 {
		t.Fatalf("initial findings = %+v", findings)
	}
	after := observation(
		node(1, "leader", 1, 1, 1, 1, "same"),
		node(2, "follower", 1, 1, 1, 1, "same"),
	)
	if findings := checker.Check(model.Transition{Before: before, After: after}); len(findings) != 0 {
		t.Fatalf("valid transition findings = %+v", findings)
	}
}

func TestCheckerDetectsLeaderReappearingOnAnotherNodeInSameTerm(t *testing.T) {
	checker := New()
	before := observation(
		node(1, "leader", 2, 1, 1, 1, "same"),
		node(2, "follower", 2, 1, 1, 1, "same"),
	)
	if findings := checker.Reset(before); len(findings) != 0 {
		t.Fatalf("initial findings = %+v", findings)
	}
	after := observation(
		node(1, "follower", 2, 1, 1, 1, "same"),
		node(2, "leader", 2, 1, 1, 1, "same"),
	)
	assertFinding(t, checker.Check(model.Transition{Before: before, After: after}), "multiple_leaders_same_term")
}

func TestCheckerDetectsRegressionsAndInvalidIndexes(t *testing.T) {
	checker := New()
	before := observation(node(1, "follower", 3, 3, 2, 3, "old"))
	checker.Reset(before)
	after := observation(node(1, "follower", 2, 2, 3, 2, "new"))
	findings := checker.Check(model.Transition{Before: before, After: after})
	for _, code := range []string{"term_regressed", "commit_regressed", "applied_exceeds_commit"} {
		assertFinding(t, findings, code)
	}
}

func TestCheckerComparesOnlyFullyCommittedLogDigests(t *testing.T) {
	checker := New()
	conflict := observation(
		node(1, "follower", 1, 2, 2, 2, "digest-a"),
		node(2, "follower", 1, 2, 2, 2, "digest-b"),
	)
	assertFinding(t, checker.Reset(conflict), "committed_log_conflict")

	withUncommittedTail := observation(
		node(1, "follower", 1, 3, 2, 2, "digest-a"),
		node(2, "follower", 1, 2, 2, 2, "digest-b"),
	)
	if findings := checker.Reset(withUncommittedTail); len(findings) != 0 {
		t.Fatalf("uncommitted suffix must not be compared: %+v", findings)
	}
}

func observation(nodes ...core.NodeObservation) core.Observation {
	return core.Observation{Nodes: nodes}
}

func node(id core.NodeID, role string, term, lastIndex, applied, commit uint64, digest string) core.NodeObservation {
	return core.NodeObservation{
		ID: id, Epoch: 1, Status: core.NodeRunning,
		Semantic: map[string]any{
			"role": role, "term": term, "last_index": lastIndex,
			"applied": applied, "commit": commit, "log_digest": digest,
		},
	}
}

func assertFinding(t *testing.T, findings []oracle.Finding, code string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("finding %q absent from %+v", code, findings)
}
