package facet

import (
	"fmt"
	"reflect"
	"sort"
)

type Evaluator interface {
	Definition() DefinitionV1
	Evaluate(input EvaluationInputV1) (EvaluationV1, error)
}

func EvaluateAll(input EvaluationInputV1, evaluators []Evaluator) ([]EvaluationV1, error) {
	seen := make(map[string]struct{}, len(evaluators))
	results := make([]EvaluationV1, 0, len(evaluators))
	for index, evaluator := range evaluators {
		if evaluator == nil || isNilEvaluator(evaluator) {
			return nil, fmt.Errorf("evaluator %d is nil", index)
		}
		definition := evaluator.Definition()
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("evaluator %d definition: %w", index, err)
		}
		identity := fmt.Sprintf("%s\x00%d", definition.ID, definition.Version)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("duplicate evaluator %s v%d", definition.ID, definition.Version)
		}
		seen[identity] = struct{}{}
		evaluation, err := evaluator.Evaluate(input)
		if err != nil {
			return nil, err
		}
		if err := validateEvaluation(definition, evaluation); err != nil {
			return nil, fmt.Errorf("evaluator %s v%d: %w", definition.ID, definition.Version, err)
		}
		results = append(results, evaluation.Copy())
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].FacetID != results[right].FacetID {
			return results[left].FacetID < results[right].FacetID
		}
		return results[left].FacetVersion < results[right].FacetVersion
	})
	return results, nil
}

func NewEvaluation(
	definition DefinitionV1,
	status EvaluationStatus,
	observations []ObservationV1,
	detail string,
) (EvaluationV1, error) {
	result := EvaluationV1{
		FacetID: definition.ID, FacetVersion: definition.Version, Status: status,
		Observations: make([]ObservationV1, len(observations)), Detail: detail,
	}
	for index, observation := range observations {
		result.Observations[index] = observation.Copy()
	}
	if status != StatusEvaluated {
		result.Observations = []ObservationV1{}
	}
	if err := canonicalizeObservations(definition, &result); err != nil {
		return EvaluationV1{}, err
	}
	if err := validateEvaluation(definition, result); err != nil {
		return EvaluationV1{}, err
	}
	return result, nil
}

func NewObservation(
	definition DefinitionV1,
	classID string,
	occurrence Occurrence,
	explanation string,
) (ObservationV1, error) {
	if err := occurrence.Validate(); err != nil {
		return ObservationV1{}, err
	}
	key, err := NewKeyV1(definition, classID)
	if err != nil {
		return ObservationV1{}, err
	}
	digest, err := key.Digest()
	if err != nil {
		return ObservationV1{}, err
	}
	return ObservationV1{
		Key: key, KeyDigest: digest, Occurrence: occurrence.Copy(), Explanation: explanation,
	}, nil
}

func canonicalizeObservations(definition DefinitionV1, evaluation *EvaluationV1) error {
	if evaluation.Status != StatusEvaluated {
		evaluation.Observations = []ObservationV1{}
		return nil
	}
	first := make(map[string]ObservationV1, len(evaluation.Observations))
	for _, observation := range evaluation.Observations {
		if err := observation.Key.Validate(definition); err != nil {
			return err
		}
		if err := observation.Occurrence.Validate(); err != nil {
			return err
		}
		digest, err := observation.Key.Digest()
		if err != nil || digest != observation.KeyDigest {
			return fmt.Errorf("observation key digest is invalid")
		}
		canonical, err := observation.Key.CanonicalString()
		if err != nil {
			return err
		}
		if _, exists := first[canonical]; !exists {
			first[canonical] = observation.Copy()
		}
	}
	keys := make([]string, 0, len(first))
	for key := range first {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	evaluation.Observations = make([]ObservationV1, len(keys))
	for index, key := range keys {
		evaluation.Observations[index] = first[key].Copy()
	}
	return nil
}

func validateEvaluation(definition DefinitionV1, evaluation EvaluationV1) error {
	if !evaluation.Status.Valid() ||
		evaluation.FacetID != definition.ID ||
		evaluation.FacetVersion != definition.Version {
		return fmt.Errorf("evaluation identity or status is invalid")
	}
	if evaluation.Status != StatusEvaluated && len(evaluation.Observations) != 0 {
		return fmt.Errorf("non-evaluated result contains observations")
	}
	previous := ""
	for _, observation := range evaluation.Observations {
		if err := observation.Key.Validate(definition); err != nil {
			return err
		}
		if err := observation.Occurrence.Validate(); err != nil {
			return err
		}
		digest, err := observation.Key.Digest()
		if err != nil || digest != observation.KeyDigest {
			return fmt.Errorf("observation digest does not match key")
		}
		canonical, _ := observation.Key.CanonicalString()
		if previous != "" && previous >= canonical {
			return fmt.Errorf("observations are not canonical and unique")
		}
		previous = canonical
	}
	return nil
}

func isNilEvaluator(evaluator Evaluator) bool {
	value := reflect.ValueOf(evaluator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
