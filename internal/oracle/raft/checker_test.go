package raft

import (
	"strconv"
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

func TestCheckerComparesCommonCommittedPrefixWithUncommittedTails(t *testing.T) {
	checker := New()
	conflict := observation(
		node(1, "follower", 1, 2, 2, 2, "digest-a"),
		node(2, "follower", 1, 2, 2, 2, "digest-b"),
	)
	assertFinding(t, checker.Reset(conflict), "committed_log_conflict")

	left := node(1, "follower", 1, 3, 2, 2, "full-log-a")
	right := node(2, "follower", 1, 2, 2, 2, "full-log-b")
	left.Semantic["committed_prefix_digests"] = map[string]string{"1": "same-1", "2": "same-2"}
	right.Semantic["committed_prefix_digests"] = map[string]any{"1": "same-1", "2": "same-2"}
	withUncommittedTail := observation(left, right)
	if findings := checker.Reset(withUncommittedTail); len(findings) != 0 {
		t.Fatalf("uncommitted suffix must not be compared: %+v", findings)
	}
}

func TestCheckerUsesMinimumCommittedIndexAndIncludesCrashedNodes(t *testing.T) {
	checker := New()
	left := node(1, "leader", 2, 3, 3, 3, "left")
	right := node(2, "crashed", 2, 1, 1, 1, "right")
	right.Status = core.NodeCrashed
	left.Semantic["committed_prefix_digests"] = map[string]string{"1": "left-1", "2": "left-2", "3": "left-3"}
	right.Semantic["committed_prefix_digests"] = map[string]string{"1": "right-1"}
	assertFinding(t, checker.Reset(observation(left, right)), "committed_log_conflict")
}

func TestCheckerRejectsAdvertisedButIncompleteCommittedPrefix(t *testing.T) {
	checker := New()
	incomplete := node(1, "follower", 2, 2, 2, 2, "same")
	incomplete.Semantic["committed_prefix_digests"] = map[string]string{"1": "same"}
	assertFinding(t, checker.Reset(observation(incomplete)), "committed_prefix_incomplete")
}

func TestCheckerChecksPersistentIndexesOnCrashedNode(t *testing.T) {
	checker := New()
	crashed := node(1, "crashed", 2, 1, 3, 2, "same")
	crashed.Status = core.NodeCrashed
	findings := checker.Reset(observation(crashed))
	assertFinding(t, findings, "applied_exceeds_commit")
	assertFinding(t, findings, "commit_exceeds_log")
}

func TestCheckerChecksSnapshotAndCompactionBoundaries(t *testing.T) {
	checker := New()
	invalid := node(1, "follower", 3, 4, 4, 4, "same")
	invalid.Semantic["snapshot_index"] = uint64(5)
	invalid.Semantic["snapshot_term"] = uint64(4)
	invalid.Semantic["first_index"] = uint64(7)
	findings := checker.Reset(observation(invalid))
	for _, code := range []string{
		"snapshot_exceeds_applied", "snapshot_exceeds_commit", "snapshot_term_exceeds_term",
		"snapshot_exceeds_log", "log_window_discontinuous", "compacted_without_covering_snapshot",
	} {
		assertFinding(t, findings, code)
	}
}

func TestCheckerAcceptsRetainedEntriesAroundSnapshot(t *testing.T) {
	checker := New()
	valid := node(1, "follower", 3, 8, 8, 8, "same")
	valid.Semantic["snapshot_index"] = uint64(8)
	valid.Semantic["snapshot_term"] = uint64(3)
	valid.Semantic["first_index"] = uint64(6)
	if findings := checker.Reset(observation(valid)); len(findings) != 0 {
		t.Fatalf("valid retained snapshot window findings = %+v", findings)
	}
}

func TestCheckerDetectsPersistentStateRegressionAcrossRestart(t *testing.T) {
	checker := New()
	beforeNode := node(1, "follower", 3, 8, 8, 8, "same")
	beforeNode.Semantic["snapshot_index"] = uint64(8)
	beforeNode.Semantic["snapshot_term"] = uint64(3)
	beforeNode.Semantic["first_index"] = uint64(7)
	before := observation(beforeNode)
	if findings := checker.Reset(before); len(findings) != 0 {
		t.Fatalf("initial findings = %+v", findings)
	}

	afterNode := node(1, "follower", 2, 7, 7, 7, "same")
	afterNode.Epoch = 2
	afterNode.Semantic["snapshot_index"] = uint64(6)
	afterNode.Semantic["snapshot_term"] = uint64(2)
	afterNode.Semantic["first_index"] = uint64(5)
	findings := checker.Check(model.Transition{Before: before, After: observation(afterNode)})
	for _, code := range []string{
		"term_regressed", "commit_regressed", "applied_regressed",
		"snapshot_index_regressed", "first_index_regressed",
	} {
		assertFinding(t, findings, code)
	}
}

func observation(nodes ...core.NodeObservation) core.Observation {
	return core.Observation{Nodes: nodes}
}

func node(id core.NodeID, role string, term, lastIndex, applied, commit uint64, digest string) core.NodeObservation {
	prefixes := make(map[string]string)
	for index := uint64(1); index <= commit; index++ {
		prefixes[strconv.FormatUint(index, 10)] = digest
	}
	return core.NodeObservation{
		ID: id, Epoch: 1, Status: core.NodeRunning,
		Semantic: map[string]any{
			"role": role, "term": term, "last_index": lastIndex,
			"applied": applied, "commit": commit, "log_digest": digest,
			"committed_prefix_available": true, "committed_prefix_digests": prefixes,
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
