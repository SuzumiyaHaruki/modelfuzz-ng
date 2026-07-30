package coverageanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestCompareReportsGrowthQuartilesAndCoarseMerges(t *testing.T) {
	two := comparisonState(2)
	three := comparisonState(3)
	noLeader := strings.Replace(three,
		`/\ state = <<"follower", "leader", "follower">>`,
		`/\ state = <<"follower", "follower", "follower">>`, 1)
	executions := []Execution{
		{Name: "run-0000", States: []model.State{{Text: two}}},
		{Name: "run-0001", States: []model.State{{Text: three}}},
		{Name: "run-0002", States: []model.State{{Text: noLeader}}},
		{Name: "run-0003", States: []model.State{{Text: comparisonState(4)}}},
	}
	report, err := Compare(executions)
	if err != nil {
		t.Fatal(err)
	}
	if report.Executions != 4 || report.ModelStateVisits != 4 ||
		report.DistinctV1States != 4 || report.DistinctV2States != 2 {
		t.Fatalf("unexpected totals: %+v", report)
	}
	if report.ReductionRatio != 0.5 || report.CompressionFactor != 2 {
		t.Fatalf("unexpected reduction: ratio=%f factor=%f", report.ReductionRatio, report.CompressionFactor)
	}
	if report.V1NewButV2OldExecutions != 2 ||
		report.V1NewButV2OldExecutionPercentage != 50 ||
		report.V2NewExecutions != 2 || report.V2NewExecutionPercentage != 50 {
		t.Fatalf("unexpected execution novelty: %+v", report)
	}
	if !report.RepeatedV2AnalysisEqual {
		t.Fatal("repeated v2 analysis was not deterministic")
	}
	if len(report.Growth) != 4 || !report.Growth[1].V1NewButV2Old ||
		report.Growth[1].CumulativeV1 != 2 || report.Growth[1].CumulativeV2 != 1 {
		t.Fatalf("unexpected growth: %+v", report.Growth)
	}
	for index, quartile := range report.Quartiles {
		if quartile.Quartile != index+1 || quartile.Executions != 1 || quartile.NewV1States != 1 {
			t.Fatalf("quartile %d: %+v", index+1, quartile)
		}
	}
	if report.V1FinalToFirstQuartileRatio == nil || *report.V1FinalToFirstQuartileRatio != 1 ||
		report.V2FinalToFirstQuartileRatio == nil || *report.V2FinalToFirstQuartileRatio != 0 {
		t.Fatalf("unexpected quartile ratios: v1=%v v2=%v",
			report.V1FinalToFirstQuartileRatio, report.V2FinalToFirstQuartileRatio)
	}
}

func TestCompareRejectsMissingExecutionsAndStates(t *testing.T) {
	if _, err := Compare(nil); err == nil {
		t.Fatal("accepted empty comparison")
	}
	if _, err := Compare([]Execution{{Name: "empty"}}); err == nil {
		t.Fatal("accepted execution without model states")
	}
}

func comparisonState(entries int) string {
	parts := make([]string, entries)
	for index := range parts {
		parts[index] = fmt.Sprintf("[term |-> 2, value |-> %d]", index+1)
	}
	log := "<<" + strings.Join(parts, ", ") + ">>"
	progress := entries - 1
	return fmt.Sprintf(`/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<%d, 0, %d>>, <<0, 0, 0>>>>
/\ log = <<%s, %s, %s>>
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<%d, %d, %d>>
/\ currentTerm = <<2, 2, 2>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`,
		progress, progress, log, log, log, progress, progress, progress)
}
