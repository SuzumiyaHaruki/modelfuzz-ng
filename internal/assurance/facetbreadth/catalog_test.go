package facetbreadth

import (
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
)

type definitionEvaluator struct {
	definition facet.DefinitionV1
}

func (e *definitionEvaluator) Definition() facet.DefinitionV1 {
	return e.definition.Copy()
}

func (e *definitionEvaluator) Evaluate(facet.EvaluationInputV1) (facet.EvaluationV1, error) {
	return facet.EvaluationV1{}, nil
}

func TestCatalogIdentityDeterministicAndDefensive(t *testing.T) {
	evaluators := raftfacet.CatalogV1()
	first, err := BuildCatalogIdentityV1(evaluators)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []facet.Evaluator{evaluators[2], evaluators[1], evaluators[0]}
	second, err := BuildCatalogIdentityV1(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("catalog identity depends on evaluator order")
	}
	for index := 0; index < 20; index++ {
		next, err := BuildCatalogIdentityV1(raftfacet.CatalogV1())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("catalog build %d differs", index)
		}
	}
	first.Facets[0].ClassIDs[0] = "mutated"
	third, err := BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	if third.Facets[0].ClassIDs[0] == "mutated" {
		t.Fatal("catalog identity shares mutable class slice")
	}
}

func TestCatalogIdentityIgnoresDescriptiveMetadata(t *testing.T) {
	base := raftfacet.CatalogV1()
	want, err := BuildCatalogIdentityV1(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := make([]facet.Evaluator, len(base))
	for index, evaluator := range base {
		definition := evaluator.Definition()
		definition.Name += " changed"
		definition.Rationale += " changed"
		for classIndex := range definition.Classes {
			definition.Classes[classIndex].Name += " changed"
			definition.Classes[classIndex].Description += " changed"
		}
		changed[index] = &definitionEvaluator{definition: definition}
	}
	got, err := BuildCatalogIdentityV1(changed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("descriptive metadata changed catalog identity")
	}
}

func TestCatalogIdentityRejectsInvalidCatalogs(t *testing.T) {
	base := raftfacet.CatalogV1()
	cases := map[string][]facet.Evaluator{
		"nil":       {base[0], nil, base[2]},
		"duplicate": {base[0], base[0], base[2]},
		"missing":   {base[0], base[1]},
		"extra":     {base[0], base[1], base[2], base[0]},
	}
	for name, evaluators := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildCatalogIdentityV1(evaluators); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	for _, mutation := range []func(*facet.DefinitionV1){
		func(definition *facet.DefinitionV1) {
			definition.Classes = definition.Classes[1:]
			definition.CardinalityBound = len(definition.Classes)
		},
		func(definition *facet.DefinitionV1) { definition.Version++ },
		func(definition *facet.DefinitionV1) { definition.Scope = facet.ScopeTransition },
		func(definition *facet.DefinitionV1) { definition.ID = "raft.unknown" },
		func(definition *facet.DefinitionV1) { definition.Name = "" },
	} {
		definition := base[0].Definition()
		mutation(&definition)
		changed := []facet.Evaluator{
			&definitionEvaluator{definition: definition}, base[1], base[2],
		}
		if _, err := BuildCatalogIdentityV1(changed); err == nil {
			t.Fatal("mutated frozen definition accepted")
		}
	}
}

func TestClassSetAndCatalogDigestsRespondToIdentity(t *testing.T) {
	frozen := frozenCatalogV1[0]
	basePayload := classSetDigestPayload{
		FacetID: frozen.ID, FacetVersion: frozen.Version,
		Scope: frozen.Scope, ClassIDs: append([]string(nil), frozen.Classes...),
	}
	base, err := digestJSON(basePayload)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []classSetDigestPayload{basePayload, basePayload, basePayload}
	mutations[0].ClassIDs = append([]string(nil), basePayload.ClassIDs[1:]...)
	mutations[1].FacetVersion++
	mutations[2].Scope = facet.ScopeTransition
	for _, mutation := range mutations {
		got, err := digestJSON(mutation)
		if err != nil {
			t.Fatal(err)
		}
		if got == base {
			t.Fatal("class identity mutation did not change digest")
		}
	}
}
