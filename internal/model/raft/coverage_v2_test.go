package raft

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestV2PrototypeSerializationIsDeterministic(t *testing.T) {
	state := model.State{Key: 17, Text: prototypeSymmetryStateA()}
	first, err := SerializeV2PrototypeState(state)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		next, nextErr := SerializeV2PrototypeState(state)
		if nextErr != nil || next != first {
			t.Fatalf("attempt %d serialization changed:\n%s\n%s\nerr=%v", attempt, first, next, nextErr)
		}
	}
	left, err := ProjectCoverageV2Prototype([]model.State{state, state})
	if err != nil {
		t.Fatal(err)
	}
	right, err := ProjectCoverageV2Prototype([]model.State{state})
	if err != nil {
		t.Fatal(err)
	}
	if left.Schema != SemanticSchemaV2Prototype || !reflect.DeepEqual(left, right) || len(left.StateKeys) != 1 {
		t.Fatalf("deterministic projection left=%+v right=%+v", left, right)
	}
}

func TestV2PrototypeCanonicalizesSymmetricNodeRenaming(t *testing.T) {
	left := projectV2State(t, prototypeSymmetryStateA())
	right := projectV2State(t, prototypeSymmetryStateB())
	if !reflect.DeepEqual(left.StateKeys, right.StateKeys) {
		t.Fatalf("symmetric node rename changed v2 key: %v != %v", left.StateKeys, right.StateKeys)
	}

	v1Left, err := ProjectCoverage([]model.State{{Text: prototypeSymmetryStateA()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1Right, err := ProjectCoverage([]model.State{{Text: prototypeSymmetryStateB()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(v1Left.StateKeys, v1Right.StateKeys) {
		t.Fatal("fixture did not demonstrate the intended v1 node-identity distinction")
	}
}

func TestV2PrototypeIgnoresAbsoluteTermShift(t *testing.T) {
	base := prototypeSymmetryStateA()
	shifted := strings.NewReplacer(
		`currentTerm = <<2, 2, 2>>`, `currentTerm = <<7, 7, 7>>`,
		`term |-> 2`, `term |-> 7`,
	).Replace(base)
	left := projectV2State(t, base)
	right := projectV2State(t, shifted)
	if !reflect.DeepEqual(left.StateKeys, right.StateKeys) {
		t.Fatalf("absolute term shift changed v2 key: %v != %v", left.StateKeys, right.StateKeys)
	}
}

func TestV2PrototypeIgnoresAbsoluteLogAndIndexShift(t *testing.T) {
	left := projectV2State(t, prototypeShiftedStorageState(false))
	right := projectV2State(t, prototypeShiftedStorageState(true))
	if !reflect.DeepEqual(left.StateKeys, right.StateKeys) {
		t.Fatalf("absolute log/index shift changed v2 key: %v != %v", left.StateKeys, right.StateKeys)
	}
}

func TestV2PrototypeLagBuckets(t *testing.T) {
	tests := []struct {
		lag  uint64
		want string
	}{
		{0, "zero"},
		{1, "one"},
		{2, "small"},
		{PrototypeLagSmallMax, "small"},
		{PrototypeLagSmallMax + 1, "large"},
		{20, "large"},
	}
	for _, test := range tests {
		if got := lagBucket(test.lag); got != test.want {
			t.Errorf("lagBucket(%d)=%q want %q", test.lag, got, test.want)
		}
	}
}

func TestV2PrototypePreservesHighLevelDistinctions(t *testing.T) {
	stableLeader := prototypeSymmetryStateA()
	noLeader := strings.Replace(stableLeader,
		`/\ state = <<"follower", "leader", "follower">>`,
		`/\ state = <<"follower", "follower", "follower">>`, 1)
	noQuorum := strings.Replace(stableLeader,
		`/\ currentActive = {1, 2, 3}`,
		`/\ currentActive = {2}`, 1)
	crashedFollower := strings.Replace(stableLeader,
		`/\ currentActive = {1, 2, 3}`,
		`/\ currentActive = {2, 3}`, 1)
	snapshotRequired := prototypeSnapshotCatchUpState(true)
	snapshotNotRequired := prototypeSnapshotCatchUpState(false)

	tests := []struct {
		name  string
		left  string
		right string
	}{
		{"stable leader versus no leader", stableLeader, noLeader},
		{"quorum available versus unavailable", stableLeader, noQuorum},
		{"active versus crashed follower", stableLeader, crashedFollower},
		{"ordinary catch-up versus snapshot required", snapshotNotRequired, snapshotRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := projectV2State(t, test.left)
			right := projectV2State(t, test.right)
			if reflect.DeepEqual(left.StateKeys, right.StateKeys) {
				t.Fatalf("important states were merged; serialized left=%s\nright=%s",
					serializeV2State(t, test.left), serializeV2State(t, test.right))
			}
		})
	}
}

func TestV2PrototypePreservesCandidateVoteBoundaries(t *testing.T) {
	selfOnly := prototypeFiveNodeCandidateState(`{1}`)
	oneShort := prototypeFiveNodeCandidateState(`{1, 2}`)
	quorum := prototypeFiveNodeCandidateState(`{1, 2, 3}`)
	projections := []PrototypeCoverageProjection{
		projectV2State(t, selfOnly),
		projectV2State(t, oneShort),
		projectV2State(t, quorum),
	}
	for left := range projections {
		for right := left + 1; right < len(projections); right++ {
			if reflect.DeepEqual(projections[left].StateKeys, projections[right].StateKeys) {
				t.Fatalf("candidate vote boundaries %d and %d were merged", left, right)
			}
		}
	}
}

func TestV2PrototypeMergesDetailedLogShapesWithinSameTopology(t *testing.T) {
	short := prototypeDetailedLogState(2)
	long := prototypeDetailedLogState(3)
	v2Short := projectV2State(t, short)
	v2Long := projectV2State(t, long)
	if !reflect.DeepEqual(v2Short.StateKeys, v2Long.StateKeys) {
		t.Fatalf("same coarse log topology was not merged:\n%s\n%s",
			serializeV2State(t, short), serializeV2State(t, long))
	}
	v1Short, err := ProjectCoverage([]model.State{{Text: short}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1Long, err := ProjectCoverage([]model.State{{Text: long}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(v1Short.StateKeys, v1Long.StateKeys) {
		t.Fatal("fixture did not retain the expected v1 detailed-log distinction")
	}
}

func TestV1ProjectionSerializationRemainsStable(t *testing.T) {
	state := model.State{Text: baseCoverageState(
		`<<0, 0, 0>>`,
		`<<<<>>, <<>>, <<>>>>`,
	)}
	got, err := projectState(state)
	if err != nil {
		t.Fatal(err)
	}
	const want = `schema=raft-coverage-v1|active={1,2,3}|roles=<<"follower","leader","follower">>|terms=<<0,0,0>>|log=[];[];[]|commit=zero,zero,zero|replication=2>1:zero,3:zero|votes=none|votedFor=none,none,none`
	if got != want {
		t.Fatalf("v1 serialized state changed:\ngot  %s\nwant %s", got, want)
	}
	projection, err := ProjectCoverage([]model.State{state}, nil)
	if err != nil || len(projection.StateKeys) != 1 {
		t.Fatalf("v1 projection=%+v err=%v", projection, err)
	}
}

func projectV2State(t *testing.T, text string) PrototypeCoverageProjection {
	t.Helper()
	projection, err := ProjectCoverageV2Prototype([]model.State{{Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func serializeV2State(t *testing.T, text string) string {
	t.Helper()
	serialized, err := SerializeV2PrototypeState(model.State{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}

func prototypeSymmetryStateA() string {
	return `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<1, 0, 2>>, <<0, 0, 0>>>>
/\ log = <<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>>>
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<1, 1, 1>>
/\ currentTerm = <<2, 2, 2>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}

func prototypeSymmetryStateB() string {
	return `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 1, 2>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ log = <<<<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>>>
/\ state = <<"leader", "follower", "follower">>
/\ commitIndex = <<1, 1, 1>>
/\ currentTerm = <<2, 2, 2>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}

func prototypeDetailedLogState(entries int) string {
	log := `<<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>`
	commit := "1"
	match := "1"
	if entries == 3 {
		log = `<<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2], [term |-> 2, value |-> 3]>>`
		commit = "2"
		match = "2"
	}
	return `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<` + match + `, 0, ` + match + `>>, <<0, 0, 0>>>>
/\ log = <<` + log + `, ` + log + `, ` + log + `>>
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<` + commit + `, ` + commit + `, ` + commit + `>>
/\ currentTerm = <<2, 2, 2>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}

func prototypeShiftedStorageState(shifted bool) string {
	entry := `[term |-> 3, value |-> 1]`
	log := `<<` + entry + `, ` + entry + `, ` + entry + `, ` + entry + `>>`
	commit, applied, snapshot, first := "3", "3", "2", "2"
	match, next := "3", "4"
	if shifted {
		log = `<<` + entry + `, ` + entry + `, ` + entry + `, ` + entry + `, ` + entry + `, ` + entry + `>>`
		commit, applied, snapshot, first = "5", "5", "4", "4"
		match, next = "5", "6"
	}
	return `/\ firstIndex = <<` + first + `, ` + first + `, ` + first + `>>
/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<` + match + `, 0, ` + match + `>>, <<0, 0, 0>>>>
/\ log = <<` + log + `, ` + log + `, ` + log + `>>
/\ snapshotTerm = <<3, 3, 3>>
/\ state = <<"follower", "leader", "follower">>
/\ pendingSnapshot = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ appliedIndex = <<` + applied + `, ` + applied + `, ` + applied + `>>
/\ commitIndex = <<` + commit + `, ` + commit + `, ` + commit + `>>
/\ currentTerm = <<3, 3, 3>>
/\ nextIndex = <<<<1, 1, 1>>, <<` + next + `, ` + next + `, ` + next + `>>, <<1, 1, 1>>>>
/\ snapshotIndex = <<` + snapshot + `, ` + snapshot + `, ` + snapshot + `>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}

func prototypeSnapshotCatchUpState(required bool) string {
	first, next := "1", "2"
	if required {
		first, next = "3", "2"
	}
	log := `<<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2], [term |-> 2, value |-> 3], [term |-> 2, value |-> 4]>>`
	return `/\ firstIndex = <<1, ` + first + `, 1>>
/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<1, 0, 4>>, <<0, 0, 0>>>>
/\ log = <<` + log + `, ` + log + `, ` + log + `>>
/\ snapshotTerm = <<0, 2, 0>>
/\ state = <<"follower", "leader", "follower">>
/\ pendingSnapshot = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ appliedIndex = <<1, 2, 1>>
/\ commitIndex = <<1, 2, 1>>
/\ currentTerm = <<2, 2, 2>>
/\ nextIndex = <<<<1, 1, 1>>, <<` + next + `, 5, 5>>, <<1, 1, 1>>>>
/\ snapshotIndex = <<0, 2, 0>>
/\ votesGranted = <<{}, {}, {}>>
	/\ votedFor = <<0, 0, 0>>`
}

func prototypeFiveNodeCandidateState(votes string) string {
	return `/\ currentActive = {1, 2, 3, 4, 5}
/\ matchIndex = <<<<0, 0, 0, 0, 0>>, <<0, 0, 0, 0, 0>>, <<0, 0, 0, 0, 0>>, <<0, 0, 0, 0, 0>>, <<0, 0, 0, 0, 0>>>>
/\ log = <<<<>>, <<>>, <<>>, <<>>, <<>>>>
/\ state = <<"candidate", "follower", "follower", "follower", "follower">>
/\ commitIndex = <<0, 0, 0, 0, 0>>
/\ currentTerm = <<4, 4, 4, 4, 4>>
/\ votesGranted = <<` + votes + `, {}, {}, {}, {}>>
/\ votedFor = <<1, 1, 0, 0, 0>>`
}
