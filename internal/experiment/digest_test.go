package experiment

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestSemanticDigestsIgnoreIdentityAndPreserveStatePathOrder(t *testing.T) {
	firstPlan := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}},
		Metadata: map[string]string{"source": "one"}}
	secondPlan := firstPlan.Copy()
	secondPlan.Metadata["source"] = "two"
	if digestPlan(firstPlan) != digestPlan(secondPlan) {
		t.Fatal("plan metadata changed semantic digest")
	}

	firstTrace := core.Trace{Version: core.CurrentTraceVersion, ExecutionID: "first", Seed: 1,
		Steps: []core.StepRecord{}, Metadata: map[string]string{"source": "one"}}
	secondTrace := firstTrace.Copy()
	secondTrace.ExecutionID, secondTrace.Seed = "second", 2
	secondTrace.Metadata["source"] = "two"
	if digestTrace(firstTrace) != digestTrace(secondTrace) {
		t.Fatal("trace identity changed semantic digest")
	}
	if digestStatePath([]model.State{{Key: 1}, {Key: 2}, {Key: 1}}) ==
		digestStatePath([]model.State{{Key: 1}, {Key: 1}, {Key: 2}}) {
		t.Fatal("state path order was ignored")
	}
	if digestStatePath(nil) != "" {
		t.Fatal("missing model execution should not create an empty-path digest")
	}
}
