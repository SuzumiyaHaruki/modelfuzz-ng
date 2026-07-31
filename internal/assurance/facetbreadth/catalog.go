// Package facetbreadth implements a deterministic, in-memory coverage union
// over completed Facet v1 evaluation summaries.
package facetbreadth

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
)

const (
	CatalogIdentitySchemaIDV1        = "modelfuzz-ng-facet-catalog-identity-v1"
	MajorVersionV1            uint32 = 1
)

type CatalogFacetIdentityV1 struct {
	FacetID        string      `json:"facet_id"`
	FacetVersion   uint32      `json:"facet_version"`
	Scope          facet.Scope `json:"scope"`
	ClassIDs       []string    `json:"class_ids"`
	ClassSetDigest string      `json:"class_set_digest"`
}

type CatalogIdentityV1 struct {
	SchemaID     string                   `json:"schema_id"`
	MajorVersion uint32                   `json:"major_version"`
	Facets       []CatalogFacetIdentityV1 `json:"facets"`
	Fingerprint  string                   `json:"fingerprint"`
}

type frozenFacet struct {
	ID      string
	Version uint32
	Scope   facet.Scope
	Classes []string
}

var frozenCatalogV1 = []frozenFacet{
	{
		ID: "raft.election_role_term_shape", Version: 1, Scope: facet.ScopeState,
		Classes: []string{
			"leaders_multiple_candidates_none_terms_split",
			"leaders_multiple_candidates_none_terms_uniform",
			"leaders_multiple_candidates_some_terms_split",
			"leaders_multiple_candidates_some_terms_uniform",
			"leaders_none_candidates_none_terms_split",
			"leaders_none_candidates_none_terms_uniform",
			"leaders_none_candidates_some_terms_split",
			"leaders_none_candidates_some_terms_uniform",
			"leaders_one_candidates_none_terms_split",
			"leaders_one_candidates_none_terms_uniform",
			"leaders_one_candidates_some_terms_split",
			"leaders_one_candidates_some_terms_uniform",
			"no_running_nodes",
		},
	},
	{
		ID: "raft.replication_alignment_shape", Version: 1, Scope: facet.ScopeState,
		Classes: []string{
			"log_aligned_commit_aligned_applied_aligned",
			"log_aligned_commit_aligned_applied_diverged",
			"log_aligned_commit_diverged_applied_aligned",
			"log_aligned_commit_diverged_applied_diverged",
			"log_diverged_commit_aligned_applied_aligned",
			"log_diverged_commit_aligned_applied_diverged",
			"log_diverged_commit_diverged_applied_aligned",
			"log_diverged_commit_diverged_applied_diverged",
		},
	},
	{
		ID: "raft.snapshot_lifecycle_event", Version: 1, Scope: facet.ScopeTransition,
		Classes: []string{
			"log_compacted",
			"snapshot_applied",
			"snapshot_created",
			"snapshot_delivered",
			"snapshot_fast_forwarded",
			"snapshot_rejected_or_stale",
			"snapshot_sent",
			"snapshot_status_failed",
			"snapshot_status_ignored",
			"snapshot_status_succeeded",
		},
	},
}

type classSetDigestPayload struct {
	FacetID      string      `json:"facet_id"`
	FacetVersion uint32      `json:"facet_version"`
	Scope        facet.Scope `json:"scope"`
	ClassIDs     []string    `json:"sorted_class_ids"`
}

type catalogFingerprintFacet struct {
	FacetID        string      `json:"facet_id"`
	FacetVersion   uint32      `json:"facet_version"`
	Scope          facet.Scope `json:"scope"`
	ClassSetDigest string      `json:"class_set_digest"`
}

type catalogFingerprintPayload struct {
	SchemaID     string                    `json:"schema_id"`
	MajorVersion uint32                    `json:"major_version"`
	Facets       []catalogFingerprintFacet `json:"sorted_facets"`
}

func BuildCatalogIdentityV1(evaluators []facet.Evaluator) (CatalogIdentityV1, error) {
	if len(evaluators) != len(frozenCatalogV1) {
		return CatalogIdentityV1{}, fmt.Errorf("catalog v1 requires exactly %d evaluators", len(frozenCatalogV1))
	}
	identities := make([]CatalogFacetIdentityV1, 0, len(evaluators))
	seen := make(map[string]struct{}, len(evaluators))
	for index, evaluator := range evaluators {
		if evaluator == nil || nilInterface(evaluator) {
			return CatalogIdentityV1{}, fmt.Errorf("evaluator %d is nil", index)
		}
		definition := evaluator.Definition()
		if err := definition.Validate(); err != nil {
			return CatalogIdentityV1{}, fmt.Errorf("evaluator %d definition: %w", index, err)
		}
		identityKey := fmt.Sprintf("%s\x00%d", definition.ID, definition.Version)
		if _, duplicate := seen[identityKey]; duplicate {
			return CatalogIdentityV1{}, fmt.Errorf("duplicate evaluator %s v%d", definition.ID, definition.Version)
		}
		seen[identityKey] = struct{}{}
		frozen, ok := frozenFacetByID(definition.ID)
		if !ok || definition.Version != frozen.Version || definition.Scope != frozen.Scope {
			return CatalogIdentityV1{}, fmt.Errorf("evaluator %s v%d is not in frozen catalog v1", definition.ID, definition.Version)
		}
		classIDs := make([]string, len(definition.Classes))
		for classIndex, class := range definition.Classes {
			classIDs[classIndex] = class.ID
		}
		if !equalStrings(classIDs, frozen.Classes) {
			return CatalogIdentityV1{}, fmt.Errorf("evaluator %s class set differs from frozen catalog v1", definition.ID)
		}
		classDigest, err := digestJSON(classSetDigestPayload{
			FacetID: definition.ID, FacetVersion: definition.Version,
			Scope: definition.Scope, ClassIDs: append([]string(nil), classIDs...),
		})
		if err != nil {
			return CatalogIdentityV1{}, err
		}
		identities = append(identities, CatalogFacetIdentityV1{
			FacetID: definition.ID, FacetVersion: definition.Version, Scope: definition.Scope,
			ClassIDs: append([]string(nil), classIDs...), ClassSetDigest: classDigest,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].FacetID != identities[j].FacetID {
			return identities[i].FacetID < identities[j].FacetID
		}
		return identities[i].FacetVersion < identities[j].FacetVersion
	})
	catalog := CatalogIdentityV1{
		SchemaID: CatalogIdentitySchemaIDV1, MajorVersion: MajorVersionV1, Facets: identities,
	}
	fingerprint, err := catalogFingerprint(catalog)
	if err != nil {
		return CatalogIdentityV1{}, err
	}
	catalog.Fingerprint = fingerprint
	if err := validateCatalogIdentity(catalog); err != nil {
		return CatalogIdentityV1{}, err
	}
	return copyCatalog(catalog), nil
}

func validateCatalogIdentity(catalog CatalogIdentityV1) error {
	if catalog.SchemaID != CatalogIdentitySchemaIDV1 || catalog.MajorVersion != MajorVersionV1 {
		return fmt.Errorf("unsupported catalog identity schema")
	}
	if len(catalog.Facets) != len(frozenCatalogV1) {
		return fmt.Errorf("catalog identity must contain exactly three facets")
	}
	previous := ""
	for _, identity := range catalog.Facets {
		if previous != "" && previous >= identity.FacetID {
			return fmt.Errorf("catalog facets are not canonical")
		}
		previous = identity.FacetID
		frozen, ok := frozenFacetByID(identity.FacetID)
		if !ok || identity.FacetVersion != frozen.Version || identity.Scope != frozen.Scope ||
			!equalStrings(identity.ClassIDs, frozen.Classes) {
			return fmt.Errorf("catalog facet %s differs from frozen catalog v1", identity.FacetID)
		}
		digest, err := digestJSON(classSetDigestPayload{
			FacetID: identity.FacetID, FacetVersion: identity.FacetVersion,
			Scope: identity.Scope, ClassIDs: append([]string(nil), identity.ClassIDs...),
		})
		if err != nil || digest != identity.ClassSetDigest {
			return fmt.Errorf("catalog facet %s class-set digest is invalid", identity.FacetID)
		}
	}
	fingerprint, err := catalogFingerprint(catalog)
	if err != nil || fingerprint != catalog.Fingerprint {
		return fmt.Errorf("catalog fingerprint is invalid")
	}
	return nil
}

func catalogFingerprint(catalog CatalogIdentityV1) (string, error) {
	items := make([]catalogFingerprintFacet, len(catalog.Facets))
	for index, identity := range catalog.Facets {
		items[index] = catalogFingerprintFacet{
			FacetID: identity.FacetID, FacetVersion: identity.FacetVersion,
			Scope: identity.Scope, ClassSetDigest: identity.ClassSetDigest,
		}
	}
	return digestJSON(catalogFingerprintPayload{
		SchemaID: catalog.SchemaID, MajorVersion: catalog.MajorVersion, Facets: items,
	})
}

func frozenFacetByID(id string) (frozenFacet, bool) {
	for _, frozen := range frozenCatalogV1 {
		if frozen.ID == id {
			return frozen, true
		}
	}
	return frozenFacet{}, false
}

func copyCatalog(catalog CatalogIdentityV1) CatalogIdentityV1 {
	result := catalog
	result.Facets = make([]CatalogFacetIdentityV1, len(catalog.Facets))
	for index, identity := range catalog.Facets {
		result.Facets[index] = identity
		result.Facets[index].ClassIDs = append([]string(nil), identity.ClassIDs...)
	}
	return result
}

func nilInterface(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
