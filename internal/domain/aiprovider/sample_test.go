package aiprovider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsSampleOperation(t *testing.T) {
	for _, operation := range []Operation{OperationText, OperationStructured, OperationClassification} {
		if !IsSampleOperation(operation) {
			t.Errorf("IsSampleOperation(%q) = false", operation)
		}
	}
	if IsSampleOperation("embedding") {
		t.Fatal("IsSampleOperation accepted an unsupported operation")
	}
}

func TestSampleEvidenceJSONContainsNoAttemptFingerprintInternals(t *testing.T) {
	evidence := SampleEvidence{
		RunID:          "run-1",
		SampleVersion:  SampleVersion,
		SchemaDigest:   "schema-1",
		ConfigRevision: "revision-1",
		CheckedAt:      time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC),
		Outcome:        SampleOutcomeSucceeded,
		Freshness:      FreshnessFresh,
		Authentication: AuthenticationOperationTested,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, want := range []string{
		`"runID":"run-1"`,
		`"sampleVersion":"1"`,
		`"schemaDigest":"schema-1"`,
		`"configRevision":"revision-1"`,
		`"outcome":"succeeded"`,
		`"freshness":"fresh"`,
		`"authentication":"operation-tested"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON %s does not contain %s", raw, want)
		}
	}
	for _, forbidden := range []string{"providerId", "workflowId", "sessionId", "adapterVersion", "sequence"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("JSON %s exposed attempt-only field %q", raw, forbidden)
		}
	}
}
