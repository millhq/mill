package configuresvc

import (
	"sync"
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/services/compositionsvc"
)

type aiProviderSampleEvidenceKey struct {
	providerID string
	operation  aiprovider.Operation
}

type storedAIProviderSampleEvidence struct {
	evidence       aiprovider.SampleEvidence
	sessionID      string
	adapterVersion int
}

type aiProviderSampleEvidenceSlot struct {
	latestSequence uint64
	lastAttempt    *storedAIProviderSampleEvidence
	lastSuccess    *storedAIProviderSampleEvidence
}

// aiProviderSampleEvidenceState is process local and deliberately independent
// from availabilityReports and availabilityWorkers. A metadata check cannot
// replace or erase a sample attempt, and restart creates an empty state.
type aiProviderSampleEvidenceState struct {
	mu       sync.Mutex
	sequence uint64
	slots    map[aiProviderSampleEvidenceKey]aiProviderSampleEvidenceSlot
}

func newAIProviderSampleEvidenceState() *aiProviderSampleEvidenceState {
	return &aiProviderSampleEvidenceState{slots: make(map[aiProviderSampleEvidenceKey]aiProviderSampleEvidenceSlot)}
}

// AIProviderSampleMetadata returns the passive, credential-free provider
// projection used when preparing a visible sample workflow.
func AIProviderSampleMetadata(c *ConfigureService, providerID string) (compositionsvc.AIProviderSampleMetadata, bool) {
	if c == nil || providerID == "" {
		return compositionsvc.AIProviderSampleMetadata{}, false
	}
	c.availabilityMu.Lock()
	c.mu.Lock()
	provider, exists := c.aiProviderSnapshotLocked(providerID)
	c.mu.Unlock()
	if !exists || c.availabilityClosed {
		c.availabilityMu.Unlock()
		return compositionsvc.AIProviderSampleMetadata{}, false
	}
	metadata := compositionsvc.AIProviderSampleMetadata{
		ProviderID:      provider.ID,
		ProviderLabel:   provider.Label,
		SafeDestination: safeEffectiveAIProviderEndpoint(provider),
		Model:           provider.Model,
		ConfigRevision:  c.aiProviderConfigRevisionLocked(provider),
	}
	c.availabilityMu.Unlock()
	return metadata, true
}

// CaptureAIProviderSampleAttempt snapshots evidence identity before genesis.
// Activation remains separate so a failed durable start cannot supersede a
// real earlier attempt.
func CaptureAIProviderSampleAttempt(c *ConfigureService, providerID, workflowID, runID string, operation aiprovider.Operation, sampleVersion, schemaDigest string) (aiprovider.SampleAttempt, bool) {
	return captureAIProviderSampleAttempt(c, c.aiProviderSampleEvidence, providerID, workflowID, runID, operation, sampleVersion, schemaDigest)
}

// ActivateAIProviderSampleAttempt marks a successfully-created durable run as
// the latest eligible sample attempt.
func ActivateAIProviderSampleAttempt(c *ConfigureService, attempt aiprovider.SampleAttempt) bool {
	return activateAIProviderSampleAttempt(c, c.aiProviderSampleEvidence, attempt)
}

// SettleAIProviderSampleAttempt records terminal evidence for the latest
// eligible run while its captured provider fingerprint remains current.
func SettleAIProviderSampleAttempt(c *ConfigureService, attempt aiprovider.SampleAttempt, outcome aiprovider.SampleOutcome, checkedAt time.Time) bool {
	return settleAIProviderSampleAttempt(c, c.aiProviderSampleEvidence, attempt, outcome, checkedAt)
}

// captureAIProviderSampleAttempt snapshots the current no-secret provider
// fingerprint for an already-matched fresh top-level run.
func captureAIProviderSampleAttempt(
	c *ConfigureService,
	state *aiProviderSampleEvidenceState,
	providerID,
	workflowID,
	runID string,
	operation aiprovider.Operation,
	sampleVersion,
	schemaDigest string,
) (aiprovider.SampleAttempt, bool) {
	if c == nil || state == nil || providerID == "" || workflowID == "" || runID == "" ||
		sampleVersion != aiprovider.SampleVersion || schemaDigest == "" || !aiprovider.IsSampleOperation(operation) {
		return aiprovider.SampleAttempt{}, false
	}

	c.availabilityMu.Lock()
	c.mu.Lock()
	provider, exists := c.aiProviderSnapshotLocked(providerID)
	c.mu.Unlock()
	if !exists || c.availabilityClosed {
		c.availabilityMu.Unlock()
		return aiprovider.SampleAttempt{}, false
	}
	revision := c.aiProviderConfigRevisionLocked(provider)
	sessionID := c.availabilitySessionID
	c.availabilityMu.Unlock()

	state.mu.Lock()
	state.sequence++
	attempt := aiprovider.SampleAttempt{
		ProviderID:     providerID,
		WorkflowID:     workflowID,
		Operation:      operation,
		RunID:          runID,
		SampleVersion:  sampleVersion,
		SchemaDigest:   schemaDigest,
		ConfigRevision: revision,
		AdapterVersion: aiprovider.AvailabilityAdapterVersion,
		SessionID:      sessionID,
		Sequence:       state.sequence,
	}
	state.mu.Unlock()
	return attempt, true
}

// activateAIProviderSampleAttempt advances latest eligibility only after DBOS
// accepted the run genesis. The caller holds runStartMu across RunWorkflow and
// this activation, so a failed genesis cannot displace an earlier real run.
func activateAIProviderSampleAttempt(c *ConfigureService, state *aiProviderSampleEvidenceState, attempt aiprovider.SampleAttempt) bool {
	if c == nil || state == nil || !validAIProviderSampleAttempt(attempt) {
		return false
	}
	revision, sessionID, current := currentAIProviderSampleFingerprint(c, attempt.ProviderID)
	if !current || revision != attempt.ConfigRevision || sessionID != attempt.SessionID ||
		attempt.AdapterVersion != aiprovider.AvailabilityAdapterVersion {
		return false
	}
	key := aiProviderSampleEvidenceKey{providerID: attempt.ProviderID, operation: attempt.Operation}
	state.mu.Lock()
	defer state.mu.Unlock()
	slot := state.slots[key]
	if attempt.Sequence <= slot.latestSequence {
		return false
	}
	slot.latestSequence = attempt.Sequence
	state.slots[key] = slot
	return true
}

// settleAIProviderSampleAttempt publishes only the latest eligible attempt
// while its captured configuration, adapter and session remain current.
func settleAIProviderSampleAttempt(
	c *ConfigureService,
	state *aiProviderSampleEvidenceState,
	attempt aiprovider.SampleAttempt,
	outcome aiprovider.SampleOutcome,
	checkedAt time.Time,
) bool {
	if c == nil || state == nil || !validAIProviderSampleAttempt(attempt) || !validAIProviderSampleOutcome(outcome) {
		return false
	}
	currentRevision, currentSession, current := currentAIProviderSampleFingerprint(c, attempt.ProviderID)
	if !current || currentRevision != attempt.ConfigRevision || currentSession != attempt.SessionID ||
		attempt.AdapterVersion != aiprovider.AvailabilityAdapterVersion {
		return false
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	evidence := aiprovider.SampleEvidence{
		RunID:          attempt.RunID,
		SampleVersion:  attempt.SampleVersion,
		SchemaDigest:   attempt.SchemaDigest,
		ConfigRevision: attempt.ConfigRevision,
		CheckedAt:      checkedAt,
		Outcome:        outcome,
		Freshness:      aiprovider.FreshnessFresh,
		Authentication: aiprovider.AuthenticationUnknown,
	}
	if outcome == aiprovider.SampleOutcomeSucceeded {
		evidence.Authentication = aiprovider.AuthenticationOperationTested
	}
	stored := &storedAIProviderSampleEvidence{
		evidence:       evidence,
		sessionID:      attempt.SessionID,
		adapterVersion: attempt.AdapterVersion,
	}
	key := aiProviderSampleEvidenceKey{providerID: attempt.ProviderID, operation: attempt.Operation}
	state.mu.Lock()
	slot := state.slots[key]
	if slot.latestSequence != attempt.Sequence {
		state.mu.Unlock()
		return false
	}
	slot.lastAttempt = stored
	if outcome == aiprovider.SampleOutcomeSucceeded {
		slot.lastSuccess = stored
	}
	state.slots[key] = slot
	state.mu.Unlock()

	c.availabilityMu.Lock()
	report := c.availabilityReports[attempt.ProviderID]
	c.availabilityMu.Unlock()
	c.emitAIProviderAvailabilityChanged(attempt.ProviderID, report.CheckID)
	return true
}

type aiProviderSampleEvidenceSnapshot struct {
	LastAttempt *aiprovider.SampleEvidence
	LastSuccess *aiprovider.SampleEvidence
}

func snapshotAIProviderSampleEvidence(
	c *ConfigureService,
	state *aiProviderSampleEvidenceState,
	providerID string,
	operation aiprovider.Operation,
) aiProviderSampleEvidenceSnapshot {
	if c == nil || state == nil {
		return aiProviderSampleEvidenceSnapshot{}
	}
	revision, sessionID, current := currentAIProviderSampleFingerprint(c, providerID)
	key := aiProviderSampleEvidenceKey{providerID: providerID, operation: operation}
	state.mu.Lock()
	slot := state.slots[key]
	state.mu.Unlock()
	return aiProviderSampleEvidenceSnapshot{
		LastAttempt: projectStoredAIProviderSampleEvidence(slot.lastAttempt, revision, sessionID, current),
		LastSuccess: projectStoredAIProviderSampleEvidence(slot.lastSuccess, revision, sessionID, current),
	}
}

func (c *ConfigureService) projectAIProviderSampleEvidence(report aiprovider.Report) aiprovider.Report {
	projected := cloneAvailabilityReport(report)
	for i := range projected.Operations {
		operation := &projected.Operations[i]
		snapshot := snapshotAIProviderSampleEvidence(c, c.aiProviderSampleEvidence, projected.ProviderID, operation.Operation)
		operation.LastSampleAttempt = snapshot.LastAttempt
		operation.LastSampleSuccess = snapshot.LastSuccess
		if snapshot.LastSuccess != nil && snapshot.LastSuccess.Freshness == aiprovider.FreshnessFresh {
			operation.Support = aiprovider.SupportSupported
			operation.Evidence = aiprovider.EvidenceSampleTest
			operation.ReasonCodes = removeReasonCode(operation.ReasonCodes, "operation-not-proven-by-metadata")
		}
	}
	return projected
}

func removeReasonCode(values []string, remove string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func projectStoredAIProviderSampleEvidence(stored *storedAIProviderSampleEvidence, revision, sessionID string, current bool) *aiprovider.SampleEvidence {
	if stored == nil {
		return nil
	}
	evidence := stored.evidence
	if !current || evidence.ConfigRevision != revision || stored.sessionID != sessionID ||
		stored.adapterVersion != aiprovider.AvailabilityAdapterVersion {
		evidence.Freshness = aiprovider.FreshnessStale
	}
	return &evidence
}

func currentAIProviderSampleFingerprint(c *ConfigureService, providerID string) (revision, sessionID string, current bool) {
	c.availabilityMu.Lock()
	c.mu.Lock()
	provider, exists := c.aiProviderSnapshotLocked(providerID)
	c.mu.Unlock()
	if exists && !c.availabilityClosed {
		revision = c.aiProviderConfigRevisionLocked(provider)
		sessionID = c.availabilitySessionID
		current = true
	}
	c.availabilityMu.Unlock()
	return revision, sessionID, current
}

func validAIProviderSampleOutcome(outcome aiprovider.SampleOutcome) bool {
	switch outcome {
	case aiprovider.SampleOutcomeSucceeded, aiprovider.SampleOutcomeFailed, aiprovider.SampleOutcomeCancelled:
		return true
	default:
		return false
	}
}

func validAIProviderSampleAttempt(attempt aiprovider.SampleAttempt) bool {
	return attempt.ProviderID != "" && attempt.WorkflowID != "" && attempt.RunID != "" &&
		aiprovider.IsSampleOperation(attempt.Operation) && attempt.SampleVersion == aiprovider.SampleVersion &&
		attempt.SchemaDigest != "" && attempt.ConfigRevision != "" && attempt.AdapterVersion == aiprovider.AvailabilityAdapterVersion &&
		attempt.SessionID != "" && attempt.Sequence != 0
}
