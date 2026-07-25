package plan

import (
	"errors"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestPlanActionValidation(t *testing.T) {
	link := core.LinkID{From: 1, To: 2}
	valid := []PlanAction{
		{Kind: ActionDeliver, Messages: &MessageRangeSelector{Link: link, Count: 1}},
		{Kind: ActionDrop, Messages: &MessageRangeSelector{Link: link, Start: 2, Count: 3}},
		{Kind: ActionDuplicate, Messages: &MessageRangeSelector{Link: link, Count: 1}},
		{Kind: ActionAdvanceTicks, Ticks: 2},
		{Kind: ActionTimeout, Node: 1},
		{Kind: ActionCrash, Node: 1},
		{Kind: ActionRestart, Node: 1},
		{Kind: ActionRequest, Node: 1, Request: "1"},
		{Kind: ActionPartition, Partition: &core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2, 3}}}},
		{Kind: ActionHeal},
	}
	for _, action := range valid {
		if err := action.Validate(); err != nil {
			t.Errorf("%s unexpectedly invalid: %v", action.Kind, err)
		}
	}

	invalid := []PlanAction{
		{Kind: "unknown"},
		{Kind: ActionDeliver},
		{Kind: ActionDeliver, Messages: &MessageRangeSelector{Link: link, Count: 0}},
		{Kind: ActionAdvanceTicks},
		{Kind: ActionTimeout},
		{Kind: ActionRequest, Node: 1},
		{Kind: ActionCrash, Node: 1, Ticks: 1},
		{Kind: ActionPartition},
		{Kind: ActionHeal, Node: 1},
	}
	for _, action := range invalid {
		if err := action.Validate(); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("invalid action %+v error = %v, want ErrInvalidPlan", action, err)
		}
	}
}

func TestPlanSequenceCopyDoesNotAlias(t *testing.T) {
	sequence := PlanSequence{
		Actions: []PlanAction{{
			Kind: ActionDeliver,
			Messages: &MessageRangeSelector{
				Link: core.LinkID{From: 1, To: 2}, Count: 2,
			},
		}},
		Metadata: map[string]string{"source": "manual"},
	}
	copy := sequence.Copy()
	copy.Actions[0].Messages.Count = 3
	copy.Metadata["source"] = "random"
	if sequence.Actions[0].Messages.Count != 2 || sequence.Metadata["source"] != "manual" {
		t.Fatal("PlanSequence.Copy shares mutable data with the original")
	}

	partition := PlanAction{Kind: ActionPartition, Partition: &core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2, 3}}}}
	partitionCopy := partition.Copy()
	partitionCopy.Partition.Groups[1][0] = 4
	if partition.Partition.Groups[1][0] != 2 {
		t.Fatal("PlanAction.Copy shares partition groups")
	}
}
