package configuresvc

import (
	"testing"
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/servicetest"
)

func TestAIProviderSampleEvidenceLatestAttemptWinsAndPreservesSuccess(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	c.aiProviderSampleEvidence = state
	first := captureSampleAttemptForTest(t, c, state, provider.ID, "run-1", aiprovider.OperationText)
	if !activateAIProviderSampleAttempt(c, state, first) {
		t.Fatal("activate first attempt returned false")
	}
	second := captureSampleAttemptForTest(t, c, state, provider.ID, "run-2", aiprovider.OperationText)
	if !activateAIProviderSampleAttempt(c, state, second) {
		t.Fatal("activate second attempt returned false")
	}
	if settleAIProviderSampleAttempt(c, state, first, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("older attempt published after a newer attempt was captured")
	}
	if !settleAIProviderSampleAttempt(c, state, second, aiprovider.SampleOutcomeSucceeded, time.Unix(2, 0)) {
		t.Fatal("latest successful attempt did not publish")
	}

	third := captureSampleAttemptForTest(t, c, state, provider.ID, "run-3", aiprovider.OperationText)
	if !activateAIProviderSampleAttempt(c, state, third) {
		t.Fatal("activate third attempt returned false")
	}
	if !settleAIProviderSampleAttempt(c, state, third, aiprovider.SampleOutcomeFailed, time.Unix(3, 0)) {
		t.Fatal("latest failed attempt did not publish")
	}
	snapshot := snapshotAIProviderSampleEvidence(c, state, provider.ID, aiprovider.OperationText)
	if snapshot.LastAttempt == nil || snapshot.LastAttempt.RunID != "run-3" || snapshot.LastAttempt.Outcome != aiprovider.SampleOutcomeFailed ||
		snapshot.LastAttempt.Authentication != aiprovider.AuthenticationUnknown {
		t.Errorf("LastAttempt = %+v", snapshot.LastAttempt)
	}
	if snapshot.LastSuccess == nil || snapshot.LastSuccess.RunID != "run-2" ||
		snapshot.LastSuccess.Authentication != aiprovider.AuthenticationOperationTested {
		t.Errorf("LastSuccess = %+v", snapshot.LastSuccess)
	}
	feature := sampleFeatureForTest(t, c.GetAIProviderAvailability(provider.ID), aiprovider.OperationText)
	if feature.LastSampleAttempt == nil || feature.LastSampleAttempt.RunID != "run-3" ||
		feature.LastSampleSuccess == nil || feature.LastSampleSuccess.RunID != "run-2" {
		t.Errorf("projected feature = %+v", feature)
	}
	if feature.Support != aiprovider.SupportSupported || feature.Evidence != aiprovider.EvidenceSampleTest {
		t.Errorf("projected support/evidence = %q/%q, want supported/sample-test", feature.Support, feature.Evidence)
	}
}

func TestAIProviderSampleEvidenceBecomesStaleAfterConfigurationChange(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	c.aiProviderSampleEvidence = state
	attempt := captureSampleAttemptForTest(t, c, state, provider.ID, "run-1", aiprovider.OperationStructured)
	if !activateAIProviderSampleAttempt(c, state, attempt) {
		t.Fatal("activate returned false")
	}
	if !settleAIProviderSampleAttempt(c, state, attempt, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("settle returned false")
	}
	if _, err := c.UpdateAIProvider(provider.ID, provider.Label, provider.Kind, provider.BaseURL, "different-model", provider.KeyRef); err != nil {
		t.Fatalf("UpdateAIProvider: %v", err)
	}
	snapshot := snapshotAIProviderSampleEvidence(c, state, provider.ID, aiprovider.OperationStructured)
	if snapshot.LastAttempt == nil || snapshot.LastAttempt.Freshness != aiprovider.FreshnessStale {
		t.Errorf("LastAttempt after change = %+v, want stale", snapshot.LastAttempt)
	}
	if snapshot.LastSuccess == nil || snapshot.LastSuccess.Freshness != aiprovider.FreshnessStale {
		t.Errorf("LastSuccess after change = %+v, want stale", snapshot.LastSuccess)
	}
	feature := sampleFeatureForTest(t, c.GetAIProviderAvailability(provider.ID), aiprovider.OperationStructured)
	if feature.LastSampleAttempt == nil || feature.LastSampleAttempt.Freshness != aiprovider.FreshnessStale ||
		feature.LastSampleSuccess == nil || feature.LastSampleSuccess.Freshness != aiprovider.FreshnessStale {
		t.Errorf("projected feature after change = %+v, want stale sample evidence", feature)
	}
	if settleAIProviderSampleAttempt(c, state, attempt, aiprovider.SampleOutcomeSucceeded, time.Unix(2, 0)) {
		t.Fatal("old fingerprint published after configuration change")
	}
}

func TestAIProviderSampleEvidenceIsIndependentFromMetadataReport(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	c.aiProviderSampleEvidence = state
	attempt := captureSampleAttemptForTest(t, c, state, provider.ID, "run-1", aiprovider.OperationClassification)
	if !activateAIProviderSampleAttempt(c, state, attempt) {
		t.Fatal("activate returned false")
	}
	if !settleAIProviderSampleAttempt(c, state, attempt, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("settle returned false")
	}

	c.availabilityMu.Lock()
	report := c.initialAvailabilityReport(provider, "check-1", attempt.ConfigRevision, provider.BaseURL)
	report.Lifecycle = aiprovider.CheckTimedOut
	report.Freshness = aiprovider.FreshnessStale
	c.availabilityReports[provider.ID] = report
	c.availabilityMu.Unlock()

	snapshot := snapshotAIProviderSampleEvidence(c, state, provider.ID, aiprovider.OperationClassification)
	if snapshot.LastSuccess == nil || snapshot.LastSuccess.Freshness != aiprovider.FreshnessFresh {
		t.Errorf("metadata timeout changed sample evidence: %+v", snapshot.LastSuccess)
	}
	if got := c.GetAIProviderAvailability(provider.ID); got.Lifecycle != aiprovider.CheckTimedOut {
		t.Errorf("sample evidence changed metadata lifecycle: %q", got.Lifecycle)
	} else if feature := sampleFeatureForTest(t, got, aiprovider.OperationClassification); feature.LastSampleSuccess == nil ||
		feature.LastSampleSuccess.Freshness != aiprovider.FreshnessFresh || feature.Evidence != aiprovider.EvidenceSampleTest {
		t.Errorf("metadata timeout erased projected sample evidence: %+v", feature)
	}
}

func TestAIProviderSampleEvidenceDoesNotRestoreAfterRestart(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	attempt := captureSampleAttemptForTest(t, c, state, provider.ID, "run-1", aiprovider.OperationText)
	if !activateAIProviderSampleAttempt(c, state, attempt) {
		t.Fatal("activate returned false")
	}
	if !settleAIProviderSampleAttempt(c, state, attempt, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("settle returned false")
	}
	restartedState := newAIProviderSampleEvidenceState()
	if got := snapshotAIProviderSampleEvidence(c, restartedState, provider.ID, aiprovider.OperationText); got.LastAttempt != nil || got.LastSuccess != nil {
		t.Errorf("new process state restored historical evidence: %+v", got)
	}
}

func TestCaptureAIProviderSampleAttemptRejectsIncompleteFingerprint(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	if _, ok := captureAIProviderSampleAttempt(c, state, provider.ID, "workflow-1", "run-1", aiprovider.OperationText, aiprovider.SampleVersion, ""); ok {
		t.Fatal("capture accepted an empty schema digest")
	}
	if _, ok := captureAIProviderSampleAttempt(c, state, provider.ID, "workflow-1", "run-1", "embedding", aiprovider.SampleVersion, "digest"); ok {
		t.Fatal("capture accepted an unsupported operation")
	}
}

func TestFailedGenesisCaptureDoesNotDisplaceEarlierActivatedAttempt(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	first := captureSampleAttemptForTest(t, c, state, provider.ID, "run-a", aiprovider.OperationText)
	if !activateAIProviderSampleAttempt(c, state, first) {
		t.Fatal("activate first returned false")
	}
	_ = captureSampleAttemptForTest(t, c, state, provider.ID, "run-b-failed-genesis", aiprovider.OperationText)
	if !settleAIProviderSampleAttempt(c, state, first, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("failed later genesis suppressed the earlier activated attempt")
	}
	snapshot := snapshotAIProviderSampleEvidence(c, state, provider.ID, aiprovider.OperationText)
	if snapshot.LastSuccess == nil || snapshot.LastSuccess.RunID != "run-a" {
		t.Errorf("LastSuccess = %+v, want run-a", snapshot.LastSuccess)
	}
}

func TestUnactivatedSampleAttemptCannotPublish(t *testing.T) {
	c, provider := newAIProviderSampleEvidenceTestService(t)
	state := newAIProviderSampleEvidenceState()
	attempt := captureSampleAttemptForTest(t, c, state, provider.ID, "never-started", aiprovider.OperationText)
	if settleAIProviderSampleAttempt(c, state, attempt, aiprovider.SampleOutcomeSucceeded, time.Unix(1, 0)) {
		t.Fatal("unactivated token published evidence")
	}
	if got := snapshotAIProviderSampleEvidence(c, state, provider.ID, aiprovider.OperationText); got.LastAttempt != nil || got.LastSuccess != nil {
		t.Errorf("unactivated token changed evidence: %+v", got)
	}
}

func newAIProviderSampleEvidenceTestService(t *testing.T) (*ConfigureService, aiprovider.AIProvider) {
	t.Helper()
	store := servicetest.NewFakeStore()
	c := NewConfigureService(store, compositionsvc.NewCompositionService(store), servicetest.FakeCredentialStore{})
	SetAIProviderMutationCoordinator(c, func(mutate func(assertUnused func(id string) error) error) error {
		return mutate(func(string) error { return nil })
	}, nil)
	provider, err := c.CreateAIProvider("Provider", aiprovider.KindOpenAICompat, "https://gateway.example/v1", "model", "")
	if err != nil {
		t.Fatalf("CreateAIProvider: %v", err)
	}
	return c, provider
}

func captureSampleAttemptForTest(
	t *testing.T,
	c *ConfigureService,
	state *aiProviderSampleEvidenceState,
	providerID,
	runID string,
	operation aiprovider.Operation,
) aiprovider.SampleAttempt {
	t.Helper()
	attempt, ok := captureAIProviderSampleAttempt(c, state, providerID, "workflow-1", runID, operation, aiprovider.SampleVersion, "digest-1")
	if !ok {
		t.Fatal("captureAIProviderSampleAttempt returned false")
	}
	return attempt
}

func sampleFeatureForTest(t *testing.T, report aiprovider.Report, operation aiprovider.Operation) aiprovider.OperationFeature {
	t.Helper()
	for _, feature := range report.Operations {
		if feature.Operation == operation {
			return feature
		}
	}
	t.Fatalf("availability report has no %q feature: %+v", operation, report.Operations)
	return aiprovider.OperationFeature{}
}
