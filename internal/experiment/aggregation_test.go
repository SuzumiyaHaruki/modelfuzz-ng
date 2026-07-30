package experiment

import "testing"

func TestValidateCorpusAdmissionsAcceptsGuidanceReasons(t *testing.T) {
	report := Report{
		Succeeded:     4,
		CorpusEntries: 2,
		CorpusAdmissionCounts: map[string]int{
			"admitted_new_v2":                 2,
			"rejected_no_guidance_novelty":    1,
			"rejected_unsuccessful_execution": 1,
		},
	}
	if err := validateCorpusAdmissions(report); err != nil {
		t.Fatalf("guided admission validation failed: %v", err)
	}
}

func TestValidateCorpusAdmissionsRejectsUnknownReason(t *testing.T) {
	report := Report{
		Succeeded:     1,
		CorpusEntries: 1,
		CorpusAdmissionCounts: map[string]int{
			"unknown": 1,
		},
	}
	if err := validateCorpusAdmissions(report); err == nil {
		t.Fatal("expected unknown admission reason to fail validation")
	}
}
