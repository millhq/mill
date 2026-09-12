package aiprovider

import "time"

const AvailabilityAdapterVersion = 1

type TransportStatus string

const (
	TransportUnchecked            TransportStatus = "unchecked"
	TransportChecking             TransportStatus = "checking"
	TransportResponded            TransportStatus = "responded"
	TransportUnreachable          TransportStatus = "unreachable"
	TransportInvalidConfiguration TransportStatus = "invalid-configuration"
)

type InspectionStatus string

const (
	InspectionNotChecked  InspectionStatus = "not-checked"
	InspectionAvailable   InspectionStatus = "available"
	InspectionUnsupported InspectionStatus = "unsupported"
	InspectionFailed      InspectionStatus = "failed"
)

type AuthenticationStatus string

const (
	AuthenticationNotRequired        AuthenticationStatus = "not-required"
	AuthenticationUnknown            AuthenticationStatus = "unknown"
	AuthenticationMetadataAuthorized AuthenticationStatus = "metadata-authorized"
	AuthenticationOperationTested    AuthenticationStatus = "operation-tested"
	AuthenticationRejected           AuthenticationStatus = "rejected"
)

type PermissionStatus string

const (
	PermissionUnchecked PermissionStatus = "unchecked"
	PermissionAllowed   PermissionStatus = "allowed"
	PermissionDenied    PermissionStatus = "denied"
)

type CheckStatus string

const (
	CheckNotStarted       CheckStatus = "not-started"
	CheckAwaitingApproval CheckStatus = "awaiting-approval"
	CheckChecking         CheckStatus = "checking"
	CheckCancelled        CheckStatus = "cancelled"
	CheckTimedOut         CheckStatus = "timed-out"
	CheckCompleted        CheckStatus = "completed"
)

type Support string

const (
	SupportSupported   Support = "supported"
	SupportUnsupported Support = "unsupported"
	SupportUnknown     Support = "unknown"
)

type EvidenceSource string

const (
	EvidenceProviderMetadata EvidenceSource = "provider-metadata"
	EvidenceSampleTest       EvidenceSource = "sample-test"
	EvidenceAdapterContract  EvidenceSource = "adapter-contract"
)

type Freshness string

const (
	FreshnessNotChecked Freshness = "not-checked"
	FreshnessFresh      Freshness = "fresh"
	FreshnessStale      Freshness = "stale"
)

type Operation string

const (
	OperationText           Operation = "text"
	OperationStructured     Operation = "structured"
	OperationClassification Operation = "classification"
)

type PermissionResult struct {
	Status    PermissionStatus `json:"status"`
	Source    string           `json:"source"`
	RuleID    string           `json:"ruleId,omitempty"`
	RuleLabel string           `json:"ruleLabel,omitempty"`
}

type OperationFeature struct {
	Operation         Operation       `json:"operation"`
	Support           Support         `json:"support"`
	Evidence          EvidenceSource  `json:"evidence"`
	WireOperation     string          `json:"wireOperation"`
	ReasonCodes       []string        `json:"reasonCodes"`
	LastSampleAttempt *SampleEvidence `json:"lastSampleAttempt,omitempty"`
	LastSampleSuccess *SampleEvidence `json:"lastSampleSuccess,omitempty"`
}

type ModelChoice struct {
	ID string `json:"id"`
}

// Report is machine-local evidence about one configured provider revision.
// It contains neither provider credentials nor raw provider responses and is
// intentionally absent from configuration export schemas.
type Report struct {
	ProviderID             string               `json:"providerId"`
	CheckID                string               `json:"checkId"`
	ConfigRevision         string               `json:"configRevision"`
	Model                  string               `json:"model"`
	AdapterVersion         int                  `json:"adapterVersion"`
	MachineID              string               `json:"machineId"`
	SessionID              string               `json:"sessionId"`
	CheckedAt              time.Time            `json:"checkedAt"`
	CheckedEndpoint        string               `json:"checkedEndpoint"`
	Transport              TransportStatus      `json:"transport"`
	Inspection             InspectionStatus     `json:"inspection"`
	Authentication         AuthenticationStatus `json:"authentication"`
	Permission             PermissionResult     `json:"permission"`
	Operations             []OperationFeature   `json:"operations"`
	ModelChoices           []ModelChoice        `json:"modelChoices"`
	ModelInventoryComplete bool                 `json:"modelInventoryComplete"`
	SelectedModelFound     *bool                `json:"selectedModelFound,omitempty"`
	ReasonCodes            []string             `json:"reasonCodes"`
	Freshness              Freshness            `json:"freshness"`
	Lifecycle              CheckStatus          `json:"lifecycle"`
}
