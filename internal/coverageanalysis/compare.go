// Package coverageanalysis compares persisted v1 semantic coverage with the
// experimental Raft v2 prototype without mutating experiment artifacts.
package coverageanalysis

import (
	"fmt"
	"reflect"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
)

type Execution struct {
	Name   string
	States []model.State
}

type GrowthPoint struct {
	Execution        int    `json:"execution"`
	Name             string `json:"name"`
	NewV1States      int    `json:"new_v1_states"`
	NewV2States      int    `json:"new_v2_states"`
	CumulativeV1     int    `json:"cumulative_v1_states"`
	CumulativeV2     int    `json:"cumulative_v2_states"`
	V1NewButV2Old    bool   `json:"v1_new_but_v2_old"`
	V2New            bool   `json:"v2_new"`
	ModelStateVisits int    `json:"model_state_visits"`
}

type Quartile struct {
	Quartile    int `json:"quartile"`
	Executions  int `json:"executions"`
	NewV1States int `json:"new_v1_states"`
	NewV2States int `json:"new_v2_states"`
	StateVisits int `json:"model_state_visits"`
}

type ComparisonReport struct {
	V1Schema                         string        `json:"v1_schema"`
	V2Schema                         string        `json:"v2_schema"`
	Executions                       int           `json:"executions"`
	ModelStateVisits                 int           `json:"model_state_visits"`
	DistinctV1States                 int           `json:"distinct_v1_states"`
	DistinctV2States                 int           `json:"distinct_v2_states"`
	ReductionRatio                   float64       `json:"reduction_ratio"`
	CompressionFactor                float64       `json:"compression_factor"`
	Growth                           []GrowthPoint `json:"growth"`
	Quartiles                        []Quartile    `json:"quartiles"`
	V1FinalToFirstQuartileRatio      *float64      `json:"v1_final_to_first_quartile_ratio"`
	V2FinalToFirstQuartileRatio      *float64      `json:"v2_final_to_first_quartile_ratio"`
	V1NewButV2OldExecutions          int           `json:"v1_new_but_v2_old_executions"`
	V1NewButV2OldExecutionPercentage float64       `json:"v1_new_but_v2_old_execution_percentage"`
	V2NewExecutions                  int           `json:"v2_new_executions"`
	V2NewExecutionPercentage         float64       `json:"v2_new_execution_percentage"`
	RepeatedV2AnalysisEqual          bool          `json:"repeated_v2_analysis_equal"`
}

func Compare(executions []Execution) (ComparisonReport, error) {
	if len(executions) == 0 {
		return ComparisonReport{}, fmt.Errorf("coverage comparison requires at least one execution")
	}
	ordered := append([]Execution(nil), executions...)

	report := ComparisonReport{
		V1Schema:                raftmodel.SemanticSchemaVersion,
		V2Schema:                raftmodel.SemanticSchemaV2Prototype,
		Executions:              len(ordered),
		Growth:                  make([]GrowthPoint, 0, len(ordered)),
		Quartiles:               make([]Quartile, 4),
		RepeatedV2AnalysisEqual: true,
	}
	for index := range report.Quartiles {
		report.Quartiles[index].Quartile = index + 1
	}
	seenV1 := make(map[int64]struct{})
	seenV2 := make(map[int64]struct{})
	for index, execution := range ordered {
		if execution.Name == "" {
			return ComparisonReport{}, fmt.Errorf("execution %d has an empty name", index)
		}
		if len(execution.States) == 0 {
			return ComparisonReport{}, fmt.Errorf("execution %q has no model states", execution.Name)
		}
		v1, err := raftmodel.ProjectCoverage(execution.States, nil)
		if err != nil {
			return ComparisonReport{}, fmt.Errorf("execution %q v1 projection: %w", execution.Name, err)
		}
		v2, err := raftmodel.ProjectCoverageV2Prototype(execution.States)
		if err != nil {
			return ComparisonReport{}, fmt.Errorf("execution %q v2 projection: %w", execution.Name, err)
		}
		repeated, err := raftmodel.ProjectCoverageV2Prototype(execution.States)
		if err != nil {
			return ComparisonReport{}, fmt.Errorf("execution %q repeated v2 projection: %w", execution.Name, err)
		}
		if !reflect.DeepEqual(v2, repeated) {
			report.RepeatedV2AnalysisEqual = false
		}
		newV1 := addNewKeys(seenV1, v1.StateKeys)
		newV2 := addNewKeys(seenV2, v2.StateKeys)
		point := GrowthPoint{
			Execution: index + 1, Name: execution.Name,
			NewV1States: newV1, NewV2States: newV2,
			CumulativeV1: len(seenV1), CumulativeV2: len(seenV2),
			V1NewButV2Old: newV1 > 0 && newV2 == 0,
			V2New:         newV2 > 0, ModelStateVisits: len(execution.States),
		}
		report.Growth = append(report.Growth, point)
		report.ModelStateVisits += len(execution.States)
		quartile := index * 4 / len(ordered)
		if quartile > 3 {
			quartile = 3
		}
		report.Quartiles[quartile].Executions++
		report.Quartiles[quartile].NewV1States += newV1
		report.Quartiles[quartile].NewV2States += newV2
		report.Quartiles[quartile].StateVisits += len(execution.States)
		if point.V1NewButV2Old {
			report.V1NewButV2OldExecutions++
		}
		if point.V2New {
			report.V2NewExecutions++
		}
	}
	report.DistinctV1States = len(seenV1)
	report.DistinctV2States = len(seenV2)
	if report.DistinctV1States > 0 {
		report.ReductionRatio = float64(report.DistinctV1States-report.DistinctV2States) /
			float64(report.DistinctV1States)
	}
	if report.DistinctV2States > 0 {
		report.CompressionFactor = float64(report.DistinctV1States) / float64(report.DistinctV2States)
	}
	report.V1FinalToFirstQuartileRatio = ratio(
		report.Quartiles[3].NewV1States, report.Quartiles[0].NewV1States)
	report.V2FinalToFirstQuartileRatio = ratio(
		report.Quartiles[3].NewV2States, report.Quartiles[0].NewV2States)
	report.V1NewButV2OldExecutionPercentage =
		float64(report.V1NewButV2OldExecutions) * 100 / float64(report.Executions)
	report.V2NewExecutionPercentage =
		float64(report.V2NewExecutions) * 100 / float64(report.Executions)
	return report, nil
}

func addNewKeys(seen map[int64]struct{}, keys []int64) int {
	added := 0
	for _, key := range keys {
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		added++
	}
	return added
}

func ratio(final, first int) *float64 {
	if first == 0 {
		return nil
	}
	value := float64(final) / float64(first)
	return &value
}
