package raft

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestProjectCoverageNormalizesAbsoluteTerms(t *testing.T) {
	statesA := []model.State{
		{Text: baseCoverageState(`<<0, 2, 2>>`, `<<<<>>, <<[term |-> 2, value |-> 1]>>, <<>>>>`)},
		{Text: baseCoverageState(`<<2, 2, 2>>`, `<<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1]>>, <<>>>>`)},
	}
	statesB := []model.State{
		{Text: baseCoverageState(`<<0, 7, 7>>`, `<<<<>>, <<[term |-> 7, value |-> 1]>>, <<>>>>`)},
		{Text: baseCoverageState(`<<7, 7, 7>>`, `<<<<[term |-> 7, value |-> 1]>>, <<[term |-> 7, value |-> 1]>>, <<>>>>`)},
	}
	events := []model.Event{model.NewEvent("HandleAppendEntriesRequest", nil)}
	left, leftErr := ProjectCoverage(statesA, events)
	right, rightErr := ProjectCoverage(statesB, events)
	if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
		t.Fatalf("absolute terms changed semantic coverage:\n%+v\n%+v", left, right)
	}
}

func TestProjectCoverageUsesProtocolRelationshipsNotNextIndexBookkeeping(t *testing.T) {
	base := `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<1, 0, 0>>, <<0, 0, 0>>>>
/\ log = <<<<[term |-> 4, value |-> 0]>>, <<[term |-> 4, value |-> 0]>>, <<>>>>
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<0, 1, 0>>
/\ currentTerm = <<4, 4, 0>>
/\ votesResponded = <<{}, {1, 2}, {}>>
/\ nextIndex = <<<<1, 1, 1>>, <<2, 1, 1>>, <<1, 1, 1>>>>
/\ votesGranted = <<{}, {1, 2}, {}>>
/\ votedFor = <<2, 2, 0>>`
	bookkeepingOnly := strings.Replace(base, `nextIndex = <<<<1, 1, 1>>, <<2, 1, 1>>, <<1, 1, 1>>>>`, `nextIndex = <<<<2, 2, 2>>, <<1, 2, 2>>, <<2, 2, 2>>>>`, 1)
	left, leftErr := ProjectCoverage([]model.State{{Text: base}}, nil)
	right, rightErr := ProjectCoverage([]model.State{{Text: bookkeepingOnly}}, nil)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("projection errors = %v / %v", leftErr, rightErr)
	}
	if !reflect.DeepEqual(left.StateKeys, right.StateKeys) {
		t.Fatalf("nextIndex-only change affected semantic state: %v != %v", left.StateKeys, right.StateKeys)
	}
}

func TestProjectCoverageDistinguishesActionClass(t *testing.T) {
	state := model.State{Text: baseCoverageState(`<<0, 0, 0>>`, `<<<<>>, <<>>, <<>>>>`)}
	states := []model.State{state, state}
	left, leftErr := ProjectCoverage(states, []model.Event{model.NewEvent("Remove", nil)})
	right, rightErr := ProjectCoverage(states, []model.Event{model.NewEvent("Add", nil)})
	if leftErr != nil || rightErr != nil {
		t.Fatalf("projection errors = %v / %v", leftErr, rightErr)
	}
	if len(left.TransitionKeys) != 1 || reflect.DeepEqual(left.TransitionKeys, right.TransitionKeys) {
		t.Fatalf("transition keys left=%v right=%v", left.TransitionKeys, right.TransitionKeys)
	}
}

func TestProjectCoverageDistinguishesStorageBoundariesAndProgress(t *testing.T) {
	base := storageCoverageState()
	compacted := strings.Replace(base, `/\ firstIndex = <<1, 1, 1>>`, `/\ firstIndex = <<1, 2, 1>>`, 1)
	pending := strings.Replace(base,
		`/\ pendingSnapshot = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`,
		`/\ pendingSnapshot = <<<<0, 0, 0>>, <<1, 0, 1>>, <<0, 0, 0>>>>`, 1)
	belowFirst := strings.Replace(compacted,
		`/\ nextIndex = <<<<2, 2, 2>>, <<2, 2, 2>>, <<2, 2, 2>>>>`,
		`/\ nextIndex = <<<<2, 2, 2>>, <<1, 2, 1>>, <<2, 2, 2>>>>`, 1)

	projections := make([]CoverageProjection, 0, 4)
	for _, text := range []string{base, compacted, pending, belowFirst} {
		projection, err := ProjectCoverage([]model.State{{Text: text}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		projections = append(projections, projection)
	}
	for left := range projections {
		for right := left + 1; right < len(projections); right++ {
			if reflect.DeepEqual(projections[left].StateKeys, projections[right].StateKeys) {
				t.Fatalf("storage states %d and %d were merged", left, right)
			}
		}
	}
}

func TestProjectCoverageTracksEveryActiveLeaderProgress(t *testing.T) {
	base := strings.Replace(storageCoverageState(),
		`/\ state = <<"follower", "leader", "follower">>`,
		`/\ state = <<"leader", "leader", "follower">>`, 1)
	firstLeaderChanged := strings.Replace(base,
		`/\ nextIndex = <<<<2, 2, 2>>, <<2, 2, 2>>, <<2, 2, 2>>>>`,
		`/\ nextIndex = <<<<2, 2, 1>>, <<2, 2, 2>>, <<2, 2, 2>>>>`, 1)
	project := func(text string) CoverageProjection {
		projection, err := ProjectCoverage([]model.State{{Text: text}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	if reflect.DeepEqual(project(base).StateKeys, project(firstLeaderChanged).StateKeys) {
		t.Fatal("progress of an active old-term Leader was merged")
	}

	inactive := strings.Replace(base, `/\ currentActive = {1, 2, 3}`, `/\ currentActive = {2, 3}`, 1)
	inactiveChanged := strings.Replace(firstLeaderChanged, `/\ currentActive = {1, 2, 3}`, `/\ currentActive = {2, 3}`, 1)
	if !reflect.DeepEqual(project(inactive).StateKeys, project(inactiveChanged).StateKeys) {
		t.Fatal("progress of an inactive Leader changed semantic state")
	}
}

func TestProjectCoverageCanonicalizesClientValuesButPreservesEquality(t *testing.T) {
	logsA := `<<<<[term |-> 1, value |-> 4], [term |-> 1, value |-> 2], [term |-> 1, value |-> 4]>>, <<[term |-> 1, value |-> 4]>>, <<>>>>`
	logsB := `<<<<[term |-> 1, value |-> 9], [term |-> 1, value |-> 7], [term |-> 1, value |-> 9]>>, <<[term |-> 1, value |-> 9]>>, <<>>>>`
	logsDifferent := `<<<<[term |-> 1, value |-> 9], [term |-> 1, value |-> 7], [term |-> 1, value |-> 8]>>, <<[term |-> 1, value |-> 9]>>, <<>>>>`
	project := func(logs string) CoverageProjection {
		projection, err := ProjectCoverage([]model.State{{Text: baseCoverageState(`<<1, 1, 1>>`, logs)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	left, renamed, different := project(logsA), project(logsB), project(logsDifferent)
	if !reflect.DeepEqual(left.StateKeys, renamed.StateKeys) {
		t.Fatalf("renamed client values changed semantic state: %v != %v", left.StateKeys, renamed.StateKeys)
	}
	if reflect.DeepEqual(left.StateKeys, different.StateKeys) {
		t.Fatal("different cross-entry value equality was merged")
	}
}

func TestProjectCoverageFoldsNonLeaderReplicationRows(t *testing.T) {
	base := strings.Replace(
		baseCoverageState(`<<1, 1, 1>>`, `<<<<[term |-> 1, value |-> 0]>>, <<[term |-> 1, value |-> 0]>>, <<[term |-> 1, value |-> 0]>>>>`),
		`/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`,
		`/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`, 1)
	nonLeaderChanged := strings.Replace(base,
		`/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`,
		`/\ matchIndex = <<<<1, 1, 1>>, <<0, 0, 0>>, <<1, 1, 1>>>>`, 1)
	leaderChanged := strings.Replace(base,
		`/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`,
		`/\ matchIndex = <<<<0, 0, 0>>, <<1, 0, 1>>, <<0, 0, 0>>>>`, 1)
	project := func(text string) CoverageProjection {
		projection, err := ProjectCoverage([]model.State{{Text: text}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	baseProjection, staleProjection, leaderProjection := project(base), project(nonLeaderChanged), project(leaderChanged)
	if !reflect.DeepEqual(baseProjection.StateKeys, staleProjection.StateKeys) {
		t.Fatal("non-Leader matchIndex bookkeeping changed semantic state")
	}
	if reflect.DeepEqual(baseProjection.StateKeys, leaderProjection.StateKeys) {
		t.Fatal("active Leader matchIndex progress was merged")
	}
}

func TestProjectCoverageClassifiesOnlyActiveCandidateVotes(t *testing.T) {
	base := strings.Replace(baseCoverageState(`<<1, 1, 1>>`, `<<<<>>, <<>>, <<>>>>`),
		`/\ state = <<"follower", "leader", "follower">>`,
		`/\ state = <<"follower", "candidate", "follower">>`, 1)
	base = strings.Replace(base,
		`/\ votesGranted = <<{}, {}, {}>>`, `/\ votesGranted = <<{}, {2}, {}>>`, 1)
	quorum := strings.Replace(base, `/\ votesGranted = <<{}, {2}, {}>>`, `/\ votesGranted = <<{}, {1, 2}, {}>>`, 1)
	nonCandidate := strings.Replace(base, `/\ votesGranted = <<{}, {2}, {}>>`, `/\ votesGranted = <<{1, 2}, {2}, {2, 3}>>`, 1)
	project := func(text string) CoverageProjection {
		projection, err := ProjectCoverage([]model.State{{Text: text}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	baseProjection, quorumProjection, staleProjection := project(base), project(quorum), project(nonCandidate)
	if reflect.DeepEqual(baseProjection.StateKeys, quorumProjection.StateKeys) {
		t.Fatal("candidate quorum boundary was merged")
	}
	if !reflect.DeepEqual(baseProjection.StateKeys, staleProjection.StateKeys) {
		t.Fatal("non-Candidate vote bookkeeping changed semantic state")
	}
}

func TestProjectCoverageRejectsMalformedStateInsteadOfFallingBack(t *testing.T) {
	_, err := ProjectCoverage([]model.State{{Key: 42, Text: `/\ currentTerm = <<1, 1, 1>>`}}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing currentActive") {
		t.Fatalf("malformed projection error = %v", err)
	}
}

func baseCoverageState(terms, logs string) string {
	return `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ log = ` + logs + `
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<0, 0, 0>>
/\ currentTerm = ` + terms + `
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}

func storageCoverageState() string {
	return `/\ firstIndex = <<1, 1, 1>>
/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<1, 0, 0>>, <<0, 0, 0>>>>
/\ log = <<<<[term |-> 1, value |-> 0]>>, <<[term |-> 1, value |-> 0]>>, <<[term |-> 1, value |-> 0]>>>>
/\ snapshotTerm = <<1, 1, 1>>
/\ state = <<"follower", "leader", "follower">>
/\ pendingSnapshot = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ appliedIndex = <<1, 1, 1>>
/\ commitIndex = <<1, 1, 1>>
/\ currentTerm = <<1, 1, 1>>
/\ nextIndex = <<<<2, 2, 2>>, <<2, 2, 2>>, <<2, 2, 2>>>>
/\ snapshotIndex = <<1, 1, 1>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}
