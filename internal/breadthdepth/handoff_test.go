package breadthdepth

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestBudgetRequiresExactPhaseSums(t *testing.T) {
	valid := Budget{
		TotalCandidates: 90, GlobalCandidates: 60, LocalCandidates: 30,
		TotalActions: 13500, GlobalActions: 9000, LocalActions: 4500,
		MaxPlanActions: 180,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid budget: %v", err)
	}
	invalid := valid
	invalid.LocalCandidates = 31
	if err := invalid.Validate(); err == nil {
		t.Fatal("candidate mismatch accepted")
	}
	invalid = valid
	invalid.LocalActions = 4501
	if err := invalid.Validate(); err == nil {
		t.Fatal("action mismatch accepted")
	}
}

func TestHandoffOrderingAndDiversityAreDeterministic(t *testing.T) {
	candidates := []HandoffSeed{
		seed("shallower", 1, 0, "a", "f1", "q1", 2),
		seed("deep-long", 2, 3, "same", "f1", "q1", 9),
		seed("deep-short", 2, 3, "same", "f1", "q1", 4),
		seed("deep-diverse", 2, 3, "different", "f2", "q2", 6),
	}
	first, err := SelectHandoff("goal", candidates, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SelectHandoff("goal", candidates, 3)
	if err != nil {
		t.Fatal(err)
	}
	if first.StableKey != second.StableKey {
		t.Fatalf("selection changed: %s != %s", first.StableKey, second.StableKey)
	}
	got := []string{
		first.Selected[0].GlobalCorpusID,
		first.Selected[1].GlobalCorpusID,
		first.Selected[2].GlobalCorpusID,
	}
	want := []string{"deep-short", "deep-diverse", "deep-long"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rank %d=%q want %q (%v)", index+1, got[index], want[index], got)
		}
	}
}

func TestHandoffRejectsIneligibleAndInvalidReplay(t *testing.T) {
	noEntry := seed("no-entry", 3, 0, "a", "f", "q", 1)
	noEntry.Progress.EntryCondition = false
	badReplay := seed("bad-replay", 4, 0, "b", "g", "r", 1)
	badReplay.Replayable = false
	result, err := SelectHandoff("goal", []HandoffSeed{noEntry, badReplay}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if result.Eligible != 0 || len(result.Selected) != 0 {
		t.Fatalf("unexpected selection: %+v", result)
	}
}

func TestHandoffIdentityFieldsDoNotCreateDiversity(t *testing.T) {
	left := seed("left", 2, 1, "semantic", "facet", "queue", 4)
	right := seed("right", 2, 1, "semantic", "facet", "queue", 4)
	left.Observation.Messages = []core.MessageObservation{{ID: 1, From: 1, To: 2}}
	right.Observation.Messages = []core.MessageObservation{{ID: 99, From: 3, To: 1}}
	left.Progress.BindingRoles = map[string]string{"Leader": "leader|max", "Target": "follower|behind"}
	right.Progress.BindingRoles = map[string]string{"Leader": "leader|max", "Target": "follower|behind"}
	gainLeft := diversityGain(left, []HandoffSeed{right})
	gainRight := diversityGain(right, []HandoffSeed{left})
	if gainLeft != gainRight || gainLeft != [4]int{} {
		t.Fatalf("identity fields created diversity: left=%v right=%v", gainLeft, gainRight)
	}
}

func seed(id string, completed, distance int, semantic, facet, queue string, length int) HandoffSeed {
	return HandoffSeed{
		GlobalCorpusID: id, GlobalAdmissionRank: 1,
		Progress: GoalProgress{
			EntryCondition: true, Completed: completed, Distance: distance,
			BindingRoles: map[string]string{"Leader": "leader|max"},
		},
		PlanPrefixLength: length, SemanticTraceDigest: semantic,
		FacetCombinationKey: facet, QueueShapeKey: queue,
		Replayable: true, ReplayStatus: "completed",
	}
}
