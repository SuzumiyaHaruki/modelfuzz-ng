package executionrecord

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordV1JSONRoundTripStrict(t *testing.T) {
	record, err := BuildV1(validBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeV1(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("round trip changed JSON:\n%s\n%s", encoded, roundTrip)
	}

	cases := []struct {
		name string
		data []byte
	}{
		{name: "unknown-field", data: append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...)},
		{name: "wrong-schema", data: []byte(strings.Replace(string(encoded), SchemaIDV1, "wrong-schema", 1))},
		{name: "wrong-version", data: []byte(strings.Replace(string(encoded), `"major_version":1`, `"major_version":2`, 1))},
		{name: "trailing-json", data: append(append([]byte(nil), encoded...), []byte(` {}`)...)},
		{name: "invalid-utf8", data: append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte{0xff, '}'}...)},
	}
	tampered := record
	tampered.Candidate.ID = "tampered"
	tamperedJSON, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		name string
		data []byte
	}{name: "digest-mismatch", data: tamperedJSON})

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeV1(bytes.NewReader(test.data)); err == nil {
				t.Fatal("DecodeV1 unexpectedly succeeded")
			}
		})
	}
}

func TestDecodeV1RejectsNonCanonicalSlices(t *testing.T) {
	record, err := BuildV1(validBuildInput())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("artifacts", func(t *testing.T) {
		changed := record
		changed.Artifacts = append([]ArtifactReference(nil), record.Artifacts...)
		changed.Artifacts[0], changed.Artifacts[1] = changed.Artifacts[1], changed.Artifacts[0]
		data, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeV1(bytes.NewReader(data)); err == nil {
			t.Fatal("unsorted artifacts unexpectedly accepted")
		}
	})
	t.Run("oracle-codes", func(t *testing.T) {
		changed := record
		changed.Oracle.Codes = []string{"z:last", "a:first"}
		data, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeV1(bytes.NewReader(data)); err == nil {
			t.Fatal("unsorted oracle codes unexpectedly accepted")
		}
	})
}
