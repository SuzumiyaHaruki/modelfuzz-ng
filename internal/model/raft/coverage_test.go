package raft

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestProjectCoverageNormalizesAbsoluteTerms(t *testing.T) {
	statesA := []model.State{
		{Text: `/\ currentTerm = <<0, 2, 2>> /\ log = <<<<>>, <<[term |-> 2, value |-> 1]>>, <<>>>> /\ state = <<"follower", "leader", "follower">>`},
		{Text: `/\ currentTerm = <<2, 2, 2>> /\ log = <<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1]>>, <<>>>> /\ state = <<"follower", "leader", "follower">>`},
	}
	statesB := []model.State{
		{Text: `/\ currentTerm = <<0, 7, 7>> /\ log = <<<<>>, <<[term |-> 7, value |-> 1]>>, <<>>>> /\ state = <<"follower", "leader", "follower">>`},
		{Text: `/\ currentTerm = <<7, 7, 7>> /\ log = <<<<[term |-> 7, value |-> 1]>>, <<[term |-> 7, value |-> 1]>>, <<>>>> /\ state = <<"follower", "leader", "follower">>`},
	}
	events := []model.Event{model.NewEvent("HandleAppendEntriesRequest", nil)}
	if left, right := ProjectCoverage(statesA, events), ProjectCoverage(statesB, events); !reflect.DeepEqual(left, right) {
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
	left := ProjectCoverage([]model.State{{Text: base}}, nil)
	right := ProjectCoverage([]model.State{{Text: bookkeepingOnly}}, nil)
	if !reflect.DeepEqual(left.StateKeys, right.StateKeys) {
		t.Fatalf("nextIndex-only change affected semantic state: %v != %v", left.StateKeys, right.StateKeys)
	}
}

func TestProjectCoverageDistinguishesActionClass(t *testing.T) {
	states := []model.State{{Key: 1}, {Key: 1}}
	left := ProjectCoverage(states, []model.Event{model.NewEvent("Remove", nil)})
	right := ProjectCoverage(states, []model.Event{model.NewEvent("Add", nil)})
	if len(left.TransitionKeys) != 1 || reflect.DeepEqual(left.TransitionKeys, right.TransitionKeys) {
		t.Fatalf("transition keys left=%v right=%v", left.TransitionKeys, right.TransitionKeys)
	}
}
