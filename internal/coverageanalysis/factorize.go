package coverageanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
)

type ValueFrequency struct {
	Value       string   `json:"value"`
	Occurrences int      `json:"occurrences"`
	Executions  int      `json:"executions"`
	Examples    []string `json:"examples,omitempty"`
}

type FieldCardinality struct {
	Field          string           `json:"field"`
	Source         string           `json:"source"`
	DistinctValues int              `json:"distinct_values"`
	Occurrences    int              `json:"occurrences"`
	HighFrequency  []ValueFrequency `json:"high_frequency"`
	RareValues     []ValueFrequency `json:"rare_values"`
}

type RemovedVariant struct {
	RunID         string            `json:"run_id"`
	StateIndex    int               `json:"state_index"`
	RemovedValues map[string]string `json:"removed_values"`
}

type SplitCase struct {
	SplitCount int              `json:"split_count"`
	SharedKey  string           `json:"shared_key"`
	Variants   []RemovedVariant `json:"variants"`
}

type AblationQuartile struct {
	Quartile         int `json:"quartile"`
	Executions       int `json:"executions"`
	FullNewStates    int `json:"full_new_states"`
	WithoutNewStates int `json:"without_new_states"`
	StateVisits      int `json:"model_state_visits"`
}

type AblationResult struct {
	Name                       string             `json:"name"`
	Kind                       string             `json:"kind"`
	RemovedTopFields           []string           `json:"removed_top_fields,omitempty"`
	RemovedNodeFields          []string           `json:"removed_node_fields,omitempty"`
	FullDistinctStates         int                `json:"full_distinct_states"`
	DistinctWithout            int                `json:"distinct_without"`
	Contribution               float64            `json:"contribution"`
	Quartiles                  []AblationQuartile `json:"quartiles"`
	FinalToFirstQuartileRatio  *float64           `json:"final_to_first_quartile_ratio"`
	FullNewBecameOldExecutions int                `json:"full_new_became_old_executions"`
	TopSplits                  []SplitCase        `json:"top_splits"`
}

type DimensionReport struct {
	Name                  string           `json:"name"`
	DistinctValues        int              `json:"distinct_values"`
	Growth                []GrowthPoint    `json:"growth"`
	Quartiles             []Quartile       `json:"quartiles"`
	FinalToFirstRatio     *float64         `json:"final_to_first_quartile_ratio"`
	ExecutionsWithNew     int              `json:"executions_with_new"`
	LastNewExecution      int              `json:"last_new_execution"`
	HighFrequency         []ValueFrequency `json:"high_frequency"`
	RareValues            []ValueFrequency `json:"rare_values"`
	RepeatedAnalysisEqual bool             `json:"repeated_analysis_equal"`
}

type ScenarioReport struct {
	Name       string         `json:"name"`
	Executions int            `json:"executions"`
	Dimensions map[string]int `json:"distinct_values_by_dimension"`
}

type FactorizationReport struct {
	Schema                string             `json:"schema"`
	FacetSchema           string             `json:"facet_schema"`
	Executions            int                `json:"executions"`
	CoverageFrames        int                `json:"coverage_frames"`
	StateComparison       ComparisonReport   `json:"state_comparison"`
	FieldCardinality      []FieldCardinality `json:"field_cardinality"`
	NodeClassDistinct     int                `json:"node_class_distinct"`
	Ablations             []AblationResult   `json:"ablations"`
	Facets                []DimensionReport  `json:"facets"`
	Interactions          []DimensionReport  `json:"interactions"`
	Scenarios             []ScenarioReport   `json:"scenarios"`
	RepeatedAnalysisEqual bool               `json:"repeated_analysis_equal"`
}

type v2Record struct {
	runIndex   int
	runID      string
	stateIndex int
	full       string
	object     map[string]any
}

type ablationSpec struct {
	name       string
	kind       string
	topFields  []string
	nodeFields []string
}

type fieldAccumulator struct {
	occurrences map[string]int
	executions  map[string]map[int]struct{}
	examples    map[string][]string
	total       int
}

func Factorize(runs []RunArtifact) (FactorizationReport, error) {
	if len(runs) == 0 {
		return FactorizationReport{}, fmt.Errorf("coverage factorization requires at least one run artifact")
	}
	executions := make([]Execution, len(runs))
	recordsByRun := make([][]v2Record, len(runs))
	allRecords := make([]v2Record, 0)
	framesByRun := make([][]CoverageFrame, len(runs))
	for runIndex, run := range runs {
		executions[runIndex] = Execution{Name: run.Name, States: run.ModelStates}
		frames, err := BuildCoverageFrames(run)
		if err != nil {
			return FactorizationReport{}, err
		}
		framesByRun[runIndex] = frames
		for stateIndex, state := range run.ModelStates {
			serialized, err := raftmodel.SerializeV2PrototypeState(state)
			if err != nil {
				return FactorizationReport{}, fmt.Errorf(
					"run %q state %d v2 serialization: %w", run.Name, stateIndex, err)
			}
			var object map[string]any
			if err := json.Unmarshal([]byte(serialized), &object); err != nil {
				return FactorizationReport{}, err
			}
			record := v2Record{
				runIndex: runIndex, runID: run.Name, stateIndex: stateIndex,
				full: serialized, object: object,
			}
			recordsByRun[runIndex] = append(recordsByRun[runIndex], record)
			allRecords = append(allRecords, record)
		}
	}
	comparison, err := Compare(executions)
	if err != nil {
		return FactorizationReport{}, err
	}
	report := FactorizationReport{
		Schema:      "raft-coverage-factorization-v1",
		FacetSchema: raftmodel.CoverageFacetsSchemaVersion,
		Executions:  len(runs), StateComparison: comparison,
		RepeatedAnalysisEqual: true,
	}
	for _, frames := range framesByRun {
		report.CoverageFrames += len(frames)
	}
	report.FieldCardinality, report.NodeClassDistinct, err = analyzeFieldCardinality(allRecords)
	if err != nil {
		return FactorizationReport{}, err
	}
	specs := defaultAblationSpecs()
	report.Ablations = make([]AblationResult, 0, len(specs))
	for _, spec := range specs {
		result, err := analyzeAblation(spec, recordsByRun, comparison.DistinctV2States)
		if err != nil {
			return FactorizationReport{}, err
		}
		report.Ablations = append(report.Ablations, result)
	}
	facets, interactions, scenarioValues, deterministic, err := analyzeFacets(runs, framesByRun)
	if err != nil {
		return FactorizationReport{}, err
	}
	report.Facets = facets
	report.Interactions = interactions
	report.Scenarios = scenarioValues
	report.RepeatedAnalysisEqual = deterministic
	return report, nil
}

func analyzeFieldCardinality(records []v2Record) ([]FieldCardinality, int, error) {
	topFields := []string{
		"cluster_size", "quorum_available", "role_topology", "term_topology",
		"leader_term_position", "candidate_term_position", "log_topology",
		"committed_prefixes", "catch_up_topology", "snapshot_topology",
		"voting_topology", "canonical_node_shapes",
	}
	nodeFields := []string{
		"lifecycle", "role", "term_position", "log_relation", "commit_lag",
		"applied_lag", "snapshot_phase", "voted_for", "candidate_votes",
		"leader_peer_lags", "inbound_catch_ups",
	}
	values := make(map[string]*fieldAccumulator)
	newAccumulator := func(name string) *fieldAccumulator {
		result := &fieldAccumulator{
			occurrences: make(map[string]int), executions: make(map[string]map[int]struct{}),
			examples: make(map[string][]string),
		}
		values[name] = result
		return result
	}
	for _, field := range topFields {
		newAccumulator("top." + field)
	}
	nodeFull := newAccumulator("node.full_class")
	for _, field := range nodeFields {
		newAccumulator("node." + field)
	}
	for _, record := range records {
		for _, field := range topFields {
			value, err := canonicalJSON(record.object[field])
			if err != nil {
				return nil, 0, err
			}
			addFieldValue(values["top."+field], value, record, 1)
		}
		shapes, err := decodeNodeShapes(record.object["canonical_node_shapes"])
		if err != nil {
			return nil, 0, fmt.Errorf("run %q state %d node shapes: %w", record.runID, record.stateIndex, err)
		}
		for _, shape := range shapes {
			full, _ := canonicalJSON(shape.fields)
			addFieldValue(nodeFull, full, record, shape.count)
			for _, field := range nodeFields {
				value, valueErr := canonicalJSON(shape.fields[field])
				if valueErr != nil {
					return nil, 0, valueErr
				}
				addFieldValue(values["node."+field], value, record, shape.count)
			}
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]FieldCardinality, 0, len(names))
	for _, name := range names {
		source := "TLC model state"
		if strings.HasPrefix(name, "node.") {
			source = "derived canonical node shape"
		}
		result = append(result, buildFieldCardinality(name, source, values[name]))
	}
	return result, len(nodeFull.occurrences), nil
}

func addFieldValue(acc *fieldAccumulator, value string, record v2Record, count int) {
	acc.occurrences[value] += count
	acc.total += count
	if acc.executions[value] == nil {
		acc.executions[value] = make(map[int]struct{})
	}
	acc.executions[value][record.runIndex] = struct{}{}
	if len(acc.examples[value]) < 3 {
		example := fmt.Sprintf("%s#state-%d", record.runID, record.stateIndex)
		for _, existing := range acc.examples[value] {
			if existing == example {
				return
			}
		}
		acc.examples[value] = append(acc.examples[value], example)
	}
}

func buildFieldCardinality(name, source string, acc *fieldAccumulator) FieldCardinality {
	values := make([]ValueFrequency, 0, len(acc.occurrences))
	for value, count := range acc.occurrences {
		values = append(values, ValueFrequency{
			Value: value, Occurrences: count, Executions: len(acc.executions[value]),
			Examples: append([]string(nil), acc.examples[value]...),
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Occurrences != values[j].Occurrences {
			return values[i].Occurrences > values[j].Occurrences
		}
		return values[i].Value < values[j].Value
	})
	high := append([]ValueFrequency(nil), values[:min(10, len(values))]...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Occurrences != values[j].Occurrences {
			return values[i].Occurrences < values[j].Occurrences
		}
		return values[i].Value < values[j].Value
	})
	rare := append([]ValueFrequency(nil), values[:min(10, len(values))]...)
	return FieldCardinality{
		Field: name, Source: source, DistinctValues: len(acc.occurrences),
		Occurrences: acc.total, HighFrequency: high, RareValues: rare,
	}
}

type decodedNodeShape struct {
	fields map[string]any
	count  int
}

func decodeNodeShapes(value any) ([]decodedNodeShape, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("canonical_node_shapes is not an array")
	}
	result := make([]decodedNodeShape, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("canonical node shape entry is not an object")
		}
		class, classOK := entry["class"].(string)
		count, countOK := jsonInt(entry["count"])
		if !classOK || !countOK || count <= 0 {
			return nil, fmt.Errorf("canonical node shape has invalid class/count")
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(class), &fields); err != nil {
			return nil, fmt.Errorf("decode canonical node class: %w", err)
		}
		result = append(result, decodedNodeShape{fields: fields, count: count})
	}
	return result, nil
}

func jsonInt(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

func canonicalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func defaultAblationSpecs() []ablationSpec {
	topFields := []string{
		"cluster_size", "quorum_available", "role_topology", "term_topology",
		"leader_term_position", "candidate_term_position", "log_topology",
		"committed_prefixes", "catch_up_topology", "snapshot_topology",
		"voting_topology", "canonical_node_shapes",
	}
	nodeFields := []string{
		"lifecycle", "role", "term_position", "log_relation", "commit_lag",
		"applied_lag", "snapshot_phase", "voted_for", "candidate_votes",
		"leader_peer_lags", "inbound_catch_ups",
	}
	result := make([]ablationSpec, 0, len(topFields)+len(nodeFields)+8)
	for _, field := range topFields {
		result = append(result, ablationSpec{
			name: "top." + field, kind: "single_top_field", topFields: []string{field},
		})
	}
	for _, field := range nodeFields {
		result = append(result, ablationSpec{
			name: "node." + field, kind: "single_node_field", nodeFields: []string{field},
		})
	}
	result = append(result,
		ablationSpec{
			name: "group.voting", kind: "field_group",
			topFields: []string{"voting_topology"}, nodeFields: []string{"voted_for", "candidate_votes"},
		},
		ablationSpec{
			name: "group.log_catch_up", kind: "field_group",
			topFields:  []string{"log_topology", "committed_prefixes", "catch_up_topology"},
			nodeFields: []string{"log_relation", "inbound_catch_ups"},
		},
		ablationSpec{
			name: "group.lag", kind: "field_group",
			nodeFields: []string{"commit_lag", "applied_lag", "leader_peer_lags"},
		},
		ablationSpec{
			name: "group.snapshot", kind: "field_group",
			topFields:  []string{"snapshot_topology"},
			nodeFields: []string{"snapshot_phase", "inbound_catch_ups"},
		},
		ablationSpec{
			name: "group.lifecycle", kind: "field_group",
			topFields:  []string{"quorum_available"},
			nodeFields: []string{"lifecycle"},
		},
		ablationSpec{
			name: "group.role_term", kind: "field_group",
			topFields: []string{
				"role_topology", "term_topology", "leader_term_position", "candidate_term_position",
			},
			nodeFields: []string{"role", "term_position"},
		},
		ablationSpec{
			name: "group.canonical_node_shapes", kind: "field_group",
			topFields: []string{"canonical_node_shapes"},
		},
		ablationSpec{
			name: "group.top_summaries_duplicated_by_nodes", kind: "field_group",
			topFields: []string{
				"quorum_available", "role_topology", "log_topology", "catch_up_topology",
				"snapshot_topology", "voting_topology",
			},
		},
	)
	return result
}

func analyzeAblation(
	spec ablationSpec, recordsByRun [][]v2Record, fullDistinct int,
) (AblationResult, error) {
	seen := make(map[string]struct{})
	fullSeen := make(map[string]struct{})
	quartiles := make([]AblationQuartile, 4)
	for index := range quartiles {
		quartiles[index].Quartile = index + 1
	}
	becameOld := 0
	type splitAccumulator struct {
		full     map[string]v2Record
		variants []RemovedVariant
	}
	splits := make(map[string]*splitAccumulator)
	for runIndex, records := range recordsByRun {
		runAblated := make(map[string]struct{})
		runFull := make(map[string]struct{})
		for _, record := range records {
			ablated, removed, err := ablateRecord(record, spec)
			if err != nil {
				return AblationResult{}, err
			}
			runAblated[ablated] = struct{}{}
			runFull[record.full] = struct{}{}
			group := splits[ablated]
			if group == nil {
				group = &splitAccumulator{full: make(map[string]v2Record)}
				splits[ablated] = group
			}
			if _, exists := group.full[record.full]; !exists {
				group.full[record.full] = record
				if len(group.variants) < 6 {
					group.variants = append(group.variants, RemovedVariant{
						RunID: record.runID, StateIndex: record.stateIndex, RemovedValues: removed,
					})
				}
			}
		}
		newFull, newAblated := 0, 0
		for value := range runFull {
			if _, exists := fullSeen[value]; !exists {
				fullSeen[value] = struct{}{}
				newFull++
			}
		}
		for value := range runAblated {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				newAblated++
			}
		}
		if newFull > 0 && newAblated == 0 {
			becameOld++
		}
		quartile := runIndex * 4 / len(recordsByRun)
		if quartile > 3 {
			quartile = 3
		}
		quartiles[quartile].Executions++
		quartiles[quartile].FullNewStates += newFull
		quartiles[quartile].WithoutNewStates += newAblated
		for _, record := range records {
			quartiles[quartile].StateVisits++
			_ = record
		}
	}
	topSplits := make([]SplitCase, 0)
	for shared, group := range splits {
		if len(group.full) < 2 {
			continue
		}
		topSplits = append(topSplits, SplitCase{
			SplitCount: len(group.full), SharedKey: readableSharedKey(shared),
			Variants: append([]RemovedVariant(nil), group.variants...),
		})
	}
	sort.Slice(topSplits, func(i, j int) bool {
		if topSplits[i].SplitCount != topSplits[j].SplitCount {
			return topSplits[i].SplitCount > topSplits[j].SplitCount
		}
		return topSplits[i].SharedKey < topSplits[j].SharedKey
	})
	if len(topSplits) > 5 {
		topSplits = topSplits[:5]
	}
	contribution := 0.0
	if fullDistinct > 0 {
		contribution = 1 - float64(len(seen))/float64(fullDistinct)
	}
	return AblationResult{
		Name: spec.name, Kind: spec.kind,
		RemovedTopFields:   append([]string(nil), spec.topFields...),
		RemovedNodeFields:  append([]string(nil), spec.nodeFields...),
		FullDistinctStates: fullDistinct, DistinctWithout: len(seen),
		Contribution: contribution, Quartiles: quartiles,
		FinalToFirstQuartileRatio: ratio(
			quartiles[3].WithoutNewStates, quartiles[0].WithoutNewStates),
		FullNewBecameOldExecutions: becameOld, TopSplits: topSplits,
	}, nil
}

func ablateRecord(record v2Record, spec ablationSpec) (string, map[string]string, error) {
	encoded, _ := json.Marshal(record.object)
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return "", nil, err
	}
	removed := make(map[string]string)
	removedNodeValues := make(map[string]map[string]int)
	for _, field := range spec.topFields {
		if value, exists := object[field]; exists {
			removed["top."+field], _ = canonicalJSON(value)
			delete(object, field)
		}
	}
	if len(spec.nodeFields) > 0 {
		shapes, err := decodeNodeShapes(object["canonical_node_shapes"])
		if err != nil {
			return "", nil, err
		}
		counts := make(map[string]int)
		for _, shape := range shapes {
			for _, field := range spec.nodeFields {
				if value, exists := shape.fields[field]; exists {
					key := "node." + field
					serializedValue, serializeErr := canonicalJSON(value)
					if serializeErr != nil {
						return "", nil, serializeErr
					}
					if removedNodeValues[key] == nil {
						removedNodeValues[key] = make(map[string]int)
					}
					removedNodeValues[key][serializedValue] += shape.count
					delete(shape.fields, field)
				}
			}
			class, _ := canonicalJSON(shape.fields)
			counts[class] += shape.count
		}
		classes := make([]string, 0, len(counts))
		for class := range counts {
			classes = append(classes, class)
		}
		sort.Strings(classes)
		rebuilt := make([]map[string]any, len(classes))
		for index, class := range classes {
			rebuilt[index] = map[string]any{"class": class, "count": counts[class]}
		}
		object["canonical_node_shapes"] = rebuilt
	}
	for field, values := range removedNodeValues {
		classes := make([]string, 0, len(values))
		for value := range values {
			classes = append(classes, value)
		}
		sort.Strings(classes)
		counted := make([]map[string]any, len(classes))
		for index, value := range classes {
			counted[index] = map[string]any{"value": json.RawMessage(value), "count": values[value]}
		}
		serialized, err := canonicalJSON(counted)
		if err != nil {
			return "", nil, err
		}
		removed[field] = serialized
	}
	serialized, err := canonicalJSON(object)
	if err != nil {
		return "", nil, err
	}
	return serialized, removed, nil
}

func readableSharedKey(value string) string {
	if len(value) <= 1200 {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return value[:1000] + "...#sha256=" + hex.EncodeToString(digest[:])
}

func analyzeFacets(
	runs []RunArtifact, framesByRun [][]CoverageFrame,
) ([]DimensionReport, []DimensionReport, []ScenarioReport, bool, error) {
	facetNames := []string{"election", "replication", "snapshot", "recovery", "network"}
	interactionNames := []string{
		"election_network", "replication_network", "snapshot_recovery", "recovery_term_relation",
	}
	facetRuns := make(map[string][][]string)
	interactionRuns := make(map[string][][]string)
	for _, name := range facetNames {
		facetRuns[name] = make([][]string, len(runs))
	}
	for _, name := range interactionNames {
		interactionRuns[name] = make([][]string, len(runs))
	}
	scenarioRuns := make(map[string]map[int]struct{})
	deterministic := true
	for runIndex, frames := range framesByRun {
		tags := scenarioTags(frames)
		tags["source:"+runs[runIndex].Source] = true
		for tag := range tags {
			if scenarioRuns[tag] == nil {
				scenarioRuns[tag] = make(map[int]struct{})
			}
			scenarioRuns[tag][runIndex] = struct{}{}
		}
		for _, frame := range frames {
			projection, err := raftmodel.ProjectCoverageFacets(frame.ModelState, frame.Context)
			if err != nil {
				return nil, nil, nil, false, fmt.Errorf(
					"run %q step %d facet projection: %w", frame.RunID, frame.StepIndex, err)
			}
			repeated, err := raftmodel.ProjectCoverageFacets(frame.ModelState, frame.Context)
			if err != nil {
				return nil, nil, nil, false, err
			}
			if !reflect.DeepEqual(projection, repeated) {
				deterministic = false
			}
			serialized, err := serializeProjectionFacets(projection)
			if err != nil {
				return nil, nil, nil, false, err
			}
			for name, value := range serialized {
				facetRuns[name][runIndex] = append(facetRuns[name][runIndex], value)
			}
			for _, interaction := range projection.Interactions {
				interactionRuns[interaction.Name][runIndex] =
					append(interactionRuns[interaction.Name][runIndex], interaction.Value)
			}
		}
	}
	facets := make([]DimensionReport, 0, len(facetNames))
	for _, name := range facetNames {
		facets = append(facets, analyzeDimension(name, facetRuns[name], runs, deterministic))
	}
	interactions := make([]DimensionReport, 0, len(interactionNames))
	for _, name := range interactionNames {
		interactions = append(interactions, analyzeDimension(name, interactionRuns[name], runs, deterministic))
	}
	scenarios := make([]ScenarioReport, 0, len(scenarioRuns))
	for name, indices := range scenarioRuns {
		dimensions := make(map[string]int)
		for _, facet := range facetNames {
			values := make(map[string]struct{})
			for index := range indices {
				for _, value := range facetRuns[facet][index] {
					values[value] = struct{}{}
				}
			}
			dimensions[facet] = len(values)
		}
		scenarios = append(scenarios, ScenarioReport{
			Name: name, Executions: len(indices), Dimensions: dimensions,
		})
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Name < scenarios[j].Name })
	return facets, interactions, scenarios, deterministic, nil
}

func serializeProjectionFacets(projection raftmodel.CoverageFacetProjection) (map[string]string, error) {
	return raftmodel.SerializeCoverageFacetProjection(projection)
}

func analyzeDimension(
	name string, valuesByRun [][]string, runs []RunArtifact, deterministic bool,
) DimensionReport {
	seen := make(map[string]struct{})
	frequencies := make(map[string]int)
	executions := make(map[string]map[int]struct{})
	examples := make(map[string][]string)
	growth := make([]GrowthPoint, 0, len(valuesByRun))
	quartiles := make([]Quartile, 4)
	for index := range quartiles {
		quartiles[index].Quartile = index + 1
	}
	executionsWithNew, lastNew := 0, 0
	for runIndex, values := range valuesByRun {
		runSet := make(map[string]struct{})
		for _, value := range values {
			frequencies[value]++
			runSet[value] = struct{}{}
			if executions[value] == nil {
				executions[value] = make(map[int]struct{})
			}
			executions[value][runIndex] = struct{}{}
			if len(examples[value]) < 3 {
				examples[value] = append(examples[value], runs[runIndex].Name)
			}
		}
		newValues := 0
		for value := range runSet {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			newValues++
		}
		if newValues > 0 {
			executionsWithNew++
			lastNew = runIndex + 1
		}
		growth = append(growth, GrowthPoint{
			Execution: runIndex + 1, Name: runs[runIndex].Name,
			NewV2States: newValues, CumulativeV2: len(seen), V2New: newValues > 0,
			ModelStateVisits: len(values),
		})
		quartile := runIndex * 4 / len(valuesByRun)
		if quartile > 3 {
			quartile = 3
		}
		quartiles[quartile].Executions++
		quartiles[quartile].NewV2States += newValues
		quartiles[quartile].StateVisits += len(values)
	}
	stats := make([]ValueFrequency, 0, len(frequencies))
	for value, count := range frequencies {
		stats = append(stats, ValueFrequency{
			Value: value, Occurrences: count, Executions: len(executions[value]),
			Examples: append([]string(nil), examples[value]...),
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Occurrences != stats[j].Occurrences {
			return stats[i].Occurrences > stats[j].Occurrences
		}
		return stats[i].Value < stats[j].Value
	})
	high := append([]ValueFrequency(nil), stats[:min(10, len(stats))]...)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Occurrences != stats[j].Occurrences {
			return stats[i].Occurrences < stats[j].Occurrences
		}
		return stats[i].Value < stats[j].Value
	})
	rare := append([]ValueFrequency(nil), stats[:min(10, len(stats))]...)
	return DimensionReport{
		Name: name, DistinctValues: len(seen), Growth: growth, Quartiles: quartiles,
		FinalToFirstRatio: ratio(quartiles[3].NewV2States, quartiles[0].NewV2States),
		ExecutionsWithNew: executionsWithNew, LastNewExecution: lastNew,
		HighFrequency: high, RareValues: rare, RepeatedAnalysisEqual: deterministic,
	}
}

func scenarioTags(frames []CoverageFrame) map[string]bool {
	result := make(map[string]bool)
	for _, frame := range frames {
		if frame.Action != nil {
			switch frame.Action.Kind {
			case "partition", "heal":
				result["partition_heal"] = true
			case "crash", "restart":
				result["crash_restart"] = true
			}
		}
		if frame.ModelEvent != nil {
			switch frame.ModelEvent.Name {
			case "Timeout", "BecomeLeader":
				result["election"] = true
			case "CreateSnapshot", "SendSnapshot", "InstallSnapshot", "RejectSnapshot":
				result["snapshot"] = true
			case "FastForwardSnapshot":
				result["snapshot"] = true
				result["fast_forward"] = true
			}
		}
		if frame.Context.SnapshotOutcome == "failed" ||
			frame.Context.SnapshotOutcome == "retry-pending" ||
			frame.Context.SnapshotOutcome == "retry-succeeded" {
			result["snapshot_failure_retry"] = true
		}
	}
	return result
}
