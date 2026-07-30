package raft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

const fixtureDirectory = "../testdata/facet-v1"

type fixtureManifest struct {
	SchemaID        string                `json:"schema_id"`
	FixtureSchemaID string                `json:"fixture_schema_id"`
	FixtureCount    int                   `json:"fixture_count"`
	Files           []fixtureManifestFile `json:"files"`
	TotalBytes      int64                 `json:"total_fixture_bytes"`
}

type fixtureManifestFile struct {
	Path   string `json:"path"`
	Cases  int    `json:"cases"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type stateFixtureFile struct {
	SchemaID           string           `json:"schema_id"`
	FacetID            string           `json:"facet_id"`
	FacetVersion       uint32           `json:"facet_version"`
	Scope              facet.Scope      `json:"scope"`
	ExpectedOccurrence facet.Occurrence `json:"expected_occurrence"`
	Cases              []stateFixture   `json:"cases"`
}

type stateFixture struct {
	ID      string              `json:"id"`
	Nodes   [][]json.RawMessage `json:"nodes"`
	ClassID string              `json:"class_id"`
	Key     string              `json:"key"`
	Digest  string              `json:"digest"`
}

type snapshotFixtureFile struct {
	SchemaID           string            `json:"schema_id"`
	FacetID            string            `json:"facet_id"`
	FacetVersion       uint32            `json:"facet_version"`
	Scope              facet.Scope       `json:"scope"`
	ExpectedOccurrence facet.Occurrence  `json:"expected_occurrence"`
	Cases              []snapshotFixture `json:"cases"`
}

type snapshotFixture struct {
	ID      string          `json:"id"`
	Marker  core.ModelEvent `json:"marker"`
	ClassID string          `json:"class_id"`
	Key     string          `json:"key"`
	Digest  string          `json:"digest"`
}

func TestGoldenFixturesCoverAllFrozenClasses(t *testing.T) {
	manifestBytes := mustReadFixture(t, "manifest.json")
	var manifest fixtureManifest
	decodeStrictFixture(t, manifestBytes, &manifest)
	if manifest.SchemaID != "modelfuzz-ng-facet-fixture-manifest-v1" ||
		manifest.FixtureSchemaID != "modelfuzz-ng-facet-fixture-v1" ||
		manifest.FixtureCount != 31 || len(manifest.Files) != 3 {
		t.Fatalf("unexpected fixture manifest: %+v", manifest)
	}

	evaluators := map[string]facet.Evaluator{}
	for _, evaluator := range CatalogV1() {
		definition := evaluator.Definition()
		evaluators[definition.ID] = evaluator
	}
	seenCases := make(map[string]struct{})
	seenClasses := make(map[string]map[string]struct{})
	var totalBytes int64
	totalCases := 0

	for _, entry := range manifest.Files {
		data := mustReadFixture(t, entry.Path)
		sum := sha256.Sum256(data)
		if int64(len(data)) != entry.Bytes ||
			hex.EncodeToString(sum[:]) != entry.SHA256 {
			t.Fatalf("%s bytes/SHA mismatch", entry.Path)
		}
		totalBytes += int64(len(data))
		switch entry.Path {
		case "election.json", "replication.json":
			var fixtures stateFixtureFile
			decodeStrictFixture(t, data, &fixtures)
			if len(fixtures.Cases) != entry.Cases {
				t.Fatalf("%s cases = %d, want %d", entry.Path, len(fixtures.Cases), entry.Cases)
			}
			for _, testCase := range fixtures.Cases {
				totalCases++
				assertUniqueFixtureID(t, seenCases, testCase.ID)
				nodes := decodeStateNodes(t, entry.Path, testCase.Nodes)
				assertFixtureEvaluation(
					t, evaluators[fixtures.FacetID],
					facet.EvaluationInputV1{
						Record: stateRecord(), InitialObservation: &core.Observation{Nodes: nodes},
					},
					fixtures, testCase.ClassID, testCase.Key, testCase.Digest,
					fixtures.ExpectedOccurrence, seenClasses,
				)
			}
		case "snapshot.json":
			var fixtures snapshotFixtureFile
			decodeStrictFixture(t, data, &fixtures)
			if len(fixtures.Cases) != entry.Cases {
				t.Fatalf("%s cases = %d, want %d", entry.Path, len(fixtures.Cases), entry.Cases)
			}
			for _, testCase := range fixtures.Cases {
				totalCases++
				assertUniqueFixtureID(t, seenCases, testCase.ID)
				trace := traceWithEffects(core.Effect{
					Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(testCase.Marker),
				})
				assertFixtureEvaluation(
					t, evaluators[fixtures.FacetID],
					facet.EvaluationInputV1{Record: recordForTrace(trace), Trace: &trace},
					fixtures, testCase.ClassID, testCase.Key, testCase.Digest,
					fixtures.ExpectedOccurrence, seenClasses,
				)
			}
		default:
			t.Fatalf("unknown fixture file %q", entry.Path)
		}
	}
	if totalCases != manifest.FixtureCount || totalBytes != manifest.TotalBytes {
		t.Fatalf("fixture totals cases/bytes = %d/%d, want %d/%d",
			totalCases, totalBytes, manifest.FixtureCount, manifest.TotalBytes)
	}
	for facetID, evaluator := range evaluators {
		definition := evaluator.Definition()
		if len(seenClasses[facetID]) != len(definition.Classes) {
			t.Fatalf("%s covered %d/%d classes", facetID, len(seenClasses[facetID]), len(definition.Classes))
		}
		for _, class := range definition.Classes {
			if _, ok := seenClasses[facetID][class.ID]; !ok {
				t.Fatalf("%s class %s has no golden fixture", facetID, class.ID)
			}
		}
	}
}

type fixtureHeader interface {
	stateFixtureFile | snapshotFixtureFile
}

func assertFixtureEvaluation[T fixtureHeader](
	t *testing.T,
	evaluator facet.Evaluator,
	input facet.EvaluationInputV1,
	fixtures T,
	classID, expectedKey, expectedDigest string,
	expectedOccurrence facet.Occurrence,
	seen map[string]map[string]struct{},
) {
	t.Helper()
	if evaluator == nil {
		t.Fatal("fixture references unknown evaluator")
	}
	var schemaID, facetID string
	var version uint32
	var scope facet.Scope
	switch value := any(fixtures).(type) {
	case stateFixtureFile:
		schemaID, facetID, version, scope = value.SchemaID, value.FacetID, value.FacetVersion, value.Scope
	case snapshotFixtureFile:
		schemaID, facetID, version, scope = value.SchemaID, value.FacetID, value.FacetVersion, value.Scope
	}
	if schemaID != "modelfuzz-ng-facet-fixture-v1" {
		t.Fatalf("fixture schema = %q", schemaID)
	}
	definition := evaluator.Definition()
	if definition.ID != facetID || definition.Version != version || definition.Scope != scope {
		t.Fatalf("fixture/evaluator mismatch: %+v / %+v", definition, fixtures)
	}
	result, err := evaluator.Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != facet.StatusEvaluated || len(result.Observations) != 1 {
		t.Fatalf("%s result = %+v", classID, result)
	}
	observation := result.Observations[0]
	canonical, err := observation.Key.CanonicalString()
	if err != nil {
		t.Fatal(err)
	}
	if observation.Key.ClassID != classID || canonical != expectedKey ||
		observation.KeyDigest != expectedDigest ||
		!reflect.DeepEqual(observation.Occurrence, expectedOccurrence) {
		t.Fatalf("%s got class/key/digest/occurrence %s/%s/%s/%+v",
			classID, observation.Key.ClassID, canonical, observation.KeyDigest, observation.Occurrence)
	}
	if seen[facetID] == nil {
		seen[facetID] = make(map[string]struct{})
	}
	if _, duplicate := seen[facetID][classID]; duplicate {
		t.Fatalf("duplicate fixture for %s/%s", facetID, classID)
	}
	seen[facetID][classID] = struct{}{}
}

func decodeStateNodes(t *testing.T, file string, raw [][]json.RawMessage) []core.NodeObservation {
	t.Helper()
	nodes := make([]core.NodeObservation, len(raw))
	for index, fields := range raw {
		if len(fields) != 3 {
			t.Fatalf("%s node %d has %d fields", file, index, len(fields))
		}
		nodes[index] = semanticNode(core.NodeID(index + 1))
		switch file {
		case "election.json":
			var status core.NodeStatus
			var role string
			var term uint64
			decodeRaw(t, fields[0], &status)
			decodeRaw(t, fields[1], &role)
			decodeRaw(t, fields[2], &term)
			nodes[index].Status = status
			nodes[index].Semantic["role"] = role
			nodes[index].Semantic["term"] = term
		case "replication.json":
			var last, commit, applied uint64
			decodeRaw(t, fields[0], &last)
			decodeRaw(t, fields[1], &commit)
			decodeRaw(t, fields[2], &applied)
			nodes[index].Semantic["last_index"] = last
			nodes[index].Semantic["commit"] = commit
			nodes[index].Semantic["applied"] = applied
		default:
			t.Fatalf("unsupported state fixture %s", file)
		}
	}
	return nodes
}

func assertUniqueFixtureID(t *testing.T, seen map[string]struct{}, id string) {
	t.Helper()
	if id == "" {
		t.Fatal("empty fixture ID")
	}
	if _, duplicate := seen[id]; duplicate {
		t.Fatalf("duplicate fixture ID %q", id)
	}
	seen[id] = struct{}{}
}

func decodeStrictFixture(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("fixture has trailing data: %v", err)
	}
}

func decodeRaw(t *testing.T, data json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(path.Join(fixtureDirectory, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func stateRecord() executionrecord.CompletedExecutionRecordV1 {
	return executionrecord.CompletedExecutionRecordV1{
		SchemaID: executionrecord.SchemaIDV1, MajorVersion: executionrecord.MajorVersionV1,
		RecordDigest: strings.Repeat("a", 64),
	}
}

func recordForTrace(trace core.Trace) executionrecord.CompletedExecutionRecordV1 {
	record := stateRecord()
	record.Trace = executionrecord.TraceSummary{
		StepCount: len(trace.Steps), Version: trace.Version,
		ExecutionID: trace.ExecutionID, Seed: trace.Seed,
	}
	record.Engine.TraceStepCount = len(trace.Steps)
	for _, step := range trace.Steps {
		record.Engine.EffectCount += len(step.Effects)
	}
	return record
}

func semanticNode(id core.NodeID) core.NodeObservation {
	return core.NodeObservation{
		ID: id, Epoch: 1, Status: core.NodeRunning,
		Semantic: map[string]any{
			"role": "follower", "term": uint64(1),
			"last_index": uint64(1), "commit": uint64(1), "applied": uint64(1),
		},
	}
}

func traceWithEffects(effects ...core.Effect) core.Trace {
	nodes := []core.NodeObservation{semanticNode(1), semanticNode(2), semanticNode(3)}
	return core.Trace{
		Version: core.CurrentTraceVersion, ExecutionID: "fixture-execution", Seed: 77,
		Steps: []core.StepRecord{{
			Index: 0, Action: core.Action{Kind: core.ActionHeal}, Effects: effects,
			NodesBefore: nodes, NodesAfter: nodes, ObservationDigest: "fixture-observation",
		}},
	}
}

func modelEventPointer(event core.ModelEvent) *core.ModelEvent {
	copy := event.Copy()
	return &copy
}
