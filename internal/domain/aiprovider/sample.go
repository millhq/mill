package aiprovider

import "time"

const SampleVersion = "1"

type SampleStatus string

const (
	SampleStatusCreated  SampleStatus = "created"
	SampleStatusExisting SampleStatus = "existing"
	SampleStatusModified SampleStatus = "modified"
)

type SampleOutcome string

const (
	SampleOutcomeSucceeded SampleOutcome = "succeeded"
	SampleOutcomeFailed    SampleOutcome = "failed"
	SampleOutcomeCancelled SampleOutcome = "cancelled"
)

// SamplePreview describes an ordinary, visible workflow prepared for one
// provider operation. It contains configuration metadata only, never a key or
// raw provider response.
type SamplePreview struct {
	WorkflowID      string       `json:"workflowID"`
	Operation       Operation    `json:"operation"`
	SampleVersion   string       `json:"sampleVersion"`
	Status          SampleStatus `json:"status"`
	SyntheticInput  string       `json:"syntheticInput"`
	SafeDestination string       `json:"safeDestination"`
	Model           string       `json:"model"`
	ConfigRevision  string       `json:"configRevision"`
}

// SampleEvidence is operation-scoped evidence produced by an ordinary sample
// run. The containing OperationFeature supplies the operation identity.
type SampleEvidence struct {
	RunID          string               `json:"runID"`
	SampleVersion  string               `json:"sampleVersion"`
	SchemaDigest   string               `json:"schemaDigest"`
	ConfigRevision string               `json:"configRevision"`
	CheckedAt      time.Time            `json:"checkedAt"`
	Outcome        SampleOutcome        `json:"outcome"`
	Freshness      Freshness            `json:"freshness"`
	Authentication AuthenticationStatus `json:"authentication"`
}

// SampleAttempt is the in-process fingerprint captured for one eligible fresh
// top-level run. It is never persisted or returned as provider configuration.
// Sequence is process-local and makes a later eligible attempt for the same
// provider operation win even when observers settle out of order.
type SampleAttempt struct {
	ProviderID     string
	WorkflowID     string
	Operation      Operation
	RunID          string
	SampleVersion  string
	SchemaDigest   string
	ConfigRevision string
	AdapterVersion int
	SessionID      string
	Sequence       uint64
}

func IsSampleOperation(operation Operation) bool {
	switch operation {
	case OperationText, OperationStructured, OperationClassification:
		return true
	default:
		return false
	}
}
