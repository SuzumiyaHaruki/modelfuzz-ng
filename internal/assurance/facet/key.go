package facet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type KeyV1 struct {
	SchemaID     string `json:"schema_id"`
	FacetID      string `json:"facet_id"`
	FacetVersion uint32 `json:"facet_version"`
	Scope        Scope  `json:"scope"`
	ClassID      string `json:"class_id"`
}

type keyDigestPayload struct {
	SchemaID     string `json:"schema_id"`
	FacetID      string `json:"facet_id"`
	FacetVersion uint32 `json:"facet_version"`
	Scope        Scope  `json:"scope"`
	ClassID      string `json:"class_id"`
}

func NewKeyV1(definition DefinitionV1, classID string) (KeyV1, error) {
	if err := definition.Validate(); err != nil {
		return KeyV1{}, err
	}
	if !definitionHasClass(definition, classID) {
		return KeyV1{}, fmt.Errorf("class %q is not in facet %s v%d", classID, definition.ID, definition.Version)
	}
	return KeyV1{
		SchemaID: KeySchemaIDV1, FacetID: definition.ID, FacetVersion: definition.Version,
		Scope: definition.Scope, ClassID: classID,
	}, nil
}

func (key KeyV1) Validate(definition DefinitionV1) error {
	if err := definition.Validate(); err != nil {
		return err
	}
	if key.SchemaID != KeySchemaIDV1 || key.FacetID != definition.ID ||
		key.FacetVersion != definition.Version || key.Scope != definition.Scope ||
		!definitionHasClass(definition, key.ClassID) {
		return fmt.Errorf("facet key does not match definition %s v%d", definition.ID, definition.Version)
	}
	return nil
}

func (key KeyV1) CanonicalString() (string, error) {
	if key.SchemaID != KeySchemaIDV1 || !safeIdentifier(key.FacetID, true) ||
		key.FacetVersion == 0 || !key.Scope.Valid() || !safeIdentifier(key.ClassID, false) {
		return "", fmt.Errorf("invalid facet key")
	}
	return fmt.Sprintf(
		"%s/%s/v%d/%s/%s",
		key.SchemaID, key.FacetID, key.FacetVersion, key.Scope, key.ClassID,
	), nil
}

func (key KeyV1) Digest() (string, error) {
	if _, err := key.CanonicalString(); err != nil {
		return "", err
	}
	payload := keyDigestPayload{
		SchemaID: key.SchemaID, FacetID: key.FacetID, FacetVersion: key.FacetVersion,
		Scope: key.Scope, ClassID: key.ClassID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal facet key: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func definitionHasClass(definition DefinitionV1, classID string) bool {
	for _, class := range definition.Classes {
		if class.ID == classID {
			return true
		}
	}
	return false
}
