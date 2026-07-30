package protocolmutation

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestSummaryIsRecomputedFromRawDecisions(t *testing.T) {
	decisions := []Decision{
		{
			LocalStage: "A2", Fallback: "", LocalProgress: true,
			Selected: Candidate{Class: "deliver", ReasonCode: "quorum",
				MessageID: 7, Action: plan.PlanAction{Kind: plan.ActionDeliver}},
		},
		{
			LocalStage: "A2", Fallback: "boundary",
			Selected: Candidate{Class: "return", ReasonCode: "boundary",
				Action: plan.PlanAction{Kind: plan.ActionAdvanceTicks}},
		},
	}
	summary := Summarize(decisions)
	if summary.DecisionCount != 2 || summary.ByStage["A2"] != 2 ||
		summary.LocalProgressCount != 1 || summary.FallbackCount != 1 ||
		summary.CurrentMessageUses != 1 {
		t.Fatalf("unexpected recomputed summary: %+v", summary)
	}
}

func TestFinishDecisionStableKeyIgnoresCurrentMessageID(t *testing.T) {
	makeDecision := func(id core.MessageID) Decision {
		decision := Decision{
			AdvisorID: "test", GoalID: "goal", LocalStage: "stage",
			Candidates: []Candidate{{
				Class: "deliver", MessageID: id, MessageType: "MsgApp",
				From: 1, To: 2,
				Action: plan.PlanAction{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
					Link: core.LinkID{From: 1, To: 2}, Start: 0, Count: 1,
				}},
			}},
		}
		if err := FinishDecision(&decision); err != nil {
			t.Fatal(err)
		}
		return decision
	}
	if left, right := makeDecision(10), makeDecision(99); left.StableKey != right.StableKey {
		t.Fatalf("stable key leaked current MessageID: %s != %s", left.StableKey, right.StableKey)
	}
}
