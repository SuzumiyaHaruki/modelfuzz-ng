package coverageanalysis

import (
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
)

func TestFactorizeReportsCardinalityAblationsAndIndependentFacets(t *testing.T) {
	nodes := frameNodes()
	runs := []RunArtifact{
		{
			Name: "run-0000", Source: "random_init", ModelConfig: raftmodel.DefaultConfig(),
			Initial: core.Observation{Nodes: nodes},
			Trace: core.Trace{
				Version: core.CurrentTraceVersion, ExecutionID: "run-0000", Seed: 1,
			},
			ModelStates: []model.State{{Text: comparisonState(2)}},
		},
		{
			Name: "run-0001", Source: "snapshot-partition_init", ModelConfig: raftmodel.DefaultConfig(),
			Initial: core.Observation{Nodes: nodes},
			Trace: core.Trace{
				Version: core.CurrentTraceVersion, ExecutionID: "run-0001", Seed: 2,
			},
			ModelStates: []model.State{{Text: comparisonState(3)}},
		},
	}
	report, err := Factorize(runs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != "raft-coverage-factorization-v1" ||
		report.FacetSchema != raftmodel.CoverageFacetsSchemaVersion ||
		report.Executions != 2 || report.CoverageFrames != 2 {
		t.Fatalf("report header=%+v", report)
	}
	if len(report.FieldCardinality) == 0 || report.NodeClassDistinct == 0 ||
		len(report.Ablations) == 0 || len(report.Facets) != 5 || len(report.Interactions) != 4 {
		t.Fatalf("missing factorization sections: fields=%d nodes=%d ablations=%d facets=%d interactions=%d",
			len(report.FieldCardinality), report.NodeClassDistinct, len(report.Ablations),
			len(report.Facets), len(report.Interactions))
	}
	if !report.RepeatedAnalysisEqual {
		t.Fatal("repeated facet projection was not deterministic")
	}
	if findDimension(t, report.Facets, "election").DistinctValues != 1 {
		t.Fatalf("absolute detailed-log difference leaked into election facet")
	}
	if findDimension(t, report.Facets, "replication").DistinctValues != 1 {
		t.Fatalf("same replication topology was split")
	}
	if len(report.Scenarios) != 2 {
		t.Fatalf("source scenario grouping=%+v", report.Scenarios)
	}
	repeated, err := Factorize(runs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, repeated) {
		t.Fatal("complete factorization report changed across repeated analysis")
	}
}

func TestFactorizeRejectsMissingContextArtifacts(t *testing.T) {
	_, err := Factorize([]RunArtifact{{
		Name: "missing-context", ModelConfig: raftmodel.DefaultConfig(),
		ModelStates: []model.State{{Text: comparisonState(2)}},
	}})
	if err == nil {
		t.Fatal("accepted an invalid empty trace without execution identity")
	}
}

func findDimension(t *testing.T, values []DimensionReport, name string) DimensionReport {
	t.Helper()
	for _, value := range values {
		if value.Name == name {
			return value
		}
	}
	t.Fatalf("dimension %q not found", name)
	return DimensionReport{}
}
