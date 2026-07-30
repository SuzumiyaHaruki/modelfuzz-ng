package facet

import (
	"testing"
)

func TestDefinitionValidationAndDefensiveCopy(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeState, "a", "b")
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	copy := definition.Copy()
	copy.Classes[0].ID = "mutated"
	copy.RelatedPropertyFamilies[0] = "mutated"
	copy.RequiredEvidence[0] = EvidenceTraceV1
	if definition.Classes[0].ID != "a" ||
		definition.RelatedPropertyFamilies[0] != "A" ||
		definition.RequiredEvidence[0] != EvidenceCompletedRecord {
		t.Fatal("DefinitionV1.Copy shared mutable slices")
	}

	invalid := definition
	invalid.ID = "no_namespace"
	if err := invalid.Validate(); err == nil {
		t.Fatal("definition without namespace was accepted")
	}
	invalid = definition
	invalid.Classes = append(invalid.Classes, invalid.Classes[0])
	if err := invalid.Validate(); err == nil {
		t.Fatal("duplicate class was accepted")
	}
	invalid = definition
	invalid.CardinalityBound = 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("undersized cardinality was accepted")
	}
}

func TestKeyV1CanonicalStringAndDigest(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeState, "a")
	key, err := NewKeyV1(definition, "a")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := key.CanonicalString()
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "modelfuzz-ng-facet-key-v1/test.alpha/v1/state/a" {
		t.Fatalf("canonical = %q", canonical)
	}
	digest, err := key.Digest()
	if err != nil {
		t.Fatal(err)
	}
	const manuallyCalculated = "e429201f0f3503d1cd243f99901c7c66b61c6e404c1a0f1f6d349ad535a18b6b"
	if digest != manuallyCalculated {
		t.Fatalf("digest = %s, want %s", digest, manuallyCalculated)
	}
	for iteration := 0; iteration < 20; iteration++ {
		got, err := key.Digest()
		if err != nil || got != digest {
			t.Fatalf("iteration %d digest/error = %q/%v", iteration, got, err)
		}
	}
	if _, err := NewKeyV1(definition, "unknown"); err == nil {
		t.Fatal("unknown class was accepted")
	}
	changed := key
	changed.Scope = ScopeTransition
	if err := changed.Validate(definition); err == nil {
		t.Fatal("scope mismatch was accepted")
	}
	changed = key
	changed.FacetVersion = 2
	if err := changed.Validate(definition); err == nil {
		t.Fatal("version mismatch was accepted")
	}
	changed = key
	changed.FacetID = "test.unknown"
	if err := changed.Validate(definition); err == nil {
		t.Fatal("unknown facet was accepted")
	}
	changed = key
	changed.ClassID = "unknown"
	if err := changed.Validate(definition); err == nil {
		t.Fatal("unknown class was accepted")
	}
}

func TestEvaluationUnionDedupAndFirstOccurrence(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeTransition, "a", "b")
	second, err := NewObservation(definition, "b", TransitionEffectOccurrence(1, 0), "second")
	if err != nil {
		t.Fatal(err)
	}
	firstA, err := NewObservation(definition, "a", TransitionEffectOccurrence(0, 2), "first")
	if err != nil {
		t.Fatal(err)
	}
	laterA, err := NewObservation(definition, "a", TransitionEffectOccurrence(2, 1), "later")
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := NewEvaluation(
		definition, StatusEvaluated,
		[]ObservationV1{second, firstA, laterA}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.Observations) != 2 ||
		evaluation.Observations[0].Key.ClassID != "a" ||
		evaluation.Observations[0].Explanation != "first" ||
		*evaluation.Observations[0].Occurrence.StepIndex != 0 ||
		evaluation.Observations[1].Key.ClassID != "b" {
		t.Fatalf("canonical first-occurrence union = %+v", evaluation.Observations)
	}
}

func TestEvaluateAllValidationOrderingAndNoGlobalState(t *testing.T) {
	left := &staticEvaluator{definition: testDefinition("test.zeta", ScopeState, "a")}
	right := &staticEvaluator{definition: testDefinition("test.alpha", ScopeState, "a")}
	results, err := EvaluateAll(EvaluationInputV1{}, []Evaluator{left, right})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].FacetID != "test.alpha" || results[1].FacetID != "test.zeta" ||
		left.calls != 1 || right.calls != 1 {
		t.Fatalf("results/calls = %+v %d/%d", results, left.calls, right.calls)
	}
	results[0].Detail = "mutated"
	again, err := EvaluateAll(EvaluationInputV1{}, []Evaluator{right})
	if err != nil || again[0].Detail != "stable" {
		t.Fatalf("second evaluation shared state: %+v/%v", again, err)
	}
	if _, err := EvaluateAll(EvaluationInputV1{}, []Evaluator{nil}); err == nil {
		t.Fatal("nil evaluator was accepted")
	}
	if _, err := EvaluateAll(EvaluationInputV1{}, []Evaluator{right, right}); err == nil {
		t.Fatal("duplicate evaluator was accepted")
	}
	var typedNil *staticEvaluator
	if _, err := EvaluateAll(EvaluationInputV1{}, []Evaluator{typedNil}); err == nil {
		t.Fatal("typed nil evaluator was accepted")
	}
}

func TestOrdinaryEvidenceStatusesDoNotReturnErrors(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeState, "a")
	for _, status := range []EvaluationStatus{
		StatusNotApplicable, StatusInsufficientEvidence, StatusInvalidEvidence,
	} {
		evaluation, err := NewEvaluation(definition, status, []ObservationV1{{}}, "detail")
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if evaluation.Status != status || len(evaluation.Observations) != 0 {
			t.Fatalf("%s evaluation = %+v", status, evaluation)
		}
	}
}

func TestEvaluationDefensiveCopy(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeState, "a")
	observation, err := NewObservation(definition, "a", ExplicitInitialOccurrence(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	source := []ObservationV1{observation}
	evaluation, err := NewEvaluation(definition, StatusEvaluated, source, "")
	if err != nil {
		t.Fatal(err)
	}
	source[0].Explanation = "mutated"
	source[0].Key.ClassID = "mutated"
	if evaluation.Observations[0].Explanation != "stable" ||
		evaluation.Observations[0].Key.ClassID != "a" {
		t.Fatal("evaluation shares source observation")
	}
	copy := evaluation.Copy()
	copy.Observations[0].Explanation = "changed"
	if evaluation.Observations[0].Explanation != "stable" {
		t.Fatal("evaluation copy shares observations")
	}
}

func testDefinition(id string, scope Scope, classes ...string) DefinitionV1 {
	definitions := make([]ClassDefinition, len(classes))
	for index, class := range classes {
		definitions[index] = ClassDefinition{ID: class, Name: class, Description: class}
	}
	return DefinitionV1{
		ID: id, Version: 1, Name: id, Protocol: "test", Grounding: GroundingImplementation,
		Scope: scope, Rationale: "test", RelatedPropertyFamilies: []string{"A"},
		RequiredEvidence: []EvidenceRequirement{EvidenceCompletedRecord},
		OptionalEvidence: []EvidenceRequirement{}, Classes: definitions,
		CardinalityBound: len(classes), Invariances: InvarianceSet{}, ValidationStatus: "test",
	}
}

type staticEvaluator struct {
	definition DefinitionV1
	calls      int
}

func (e *staticEvaluator) Definition() DefinitionV1 {
	return e.definition.Copy()
}

func (e *staticEvaluator) Evaluate(EvaluationInputV1) (EvaluationV1, error) {
	e.calls++
	return NewEvaluation(e.definition, StatusNotApplicable, nil, "stable")
}

func TestIdentifierSafety(t *testing.T) {
	definition := testDefinition("test.alpha", ScopeState, "a")
	for _, bad := range []string{
		"bad/id", "bad id", "bad\\id", "../bad", "1bad", ".bad", "bad.", "bad..id",
	} {
		changed := definition
		changed.ID = bad
		if err := changed.Validate(); err == nil {
			t.Fatalf("unsafe ID %q accepted", bad)
		}
	}
}
