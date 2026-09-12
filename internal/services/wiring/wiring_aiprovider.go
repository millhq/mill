package wiring

import (
	"context"
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/configuresvc"
	"github.com/alicoding/mill/internal/services/executionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
)

// WireAIProviderCheckAuthorizer routes provider inspection through the shared guardrail door.
func WireAIProviderCheckAuthorizer(configureService *configuresvc.ConfigureService, guardrailService *guardrailsvc.GuardrailService) {
	configuresvc.SetAIProviderCheckAuthorizer(configureService, func(ctx context.Context, request configuresvc.ProviderCheckPermissionRequest) (aiprovider.PermissionResult, error) {
		decision, err := guardrailService.RequestGuardedAction(ctx, guardrailsvc.GuardedAction{
			Kind:        "provider-inspect",
			Attributes:  map[string]string{"provider_id": request.ProviderID, "check_id": request.CheckID, "endpoint": request.Endpoint},
			Description: "Check this AI provider's metadata endpoint.",
			Source:      request.Actor,
		})
		result := aiprovider.PermissionResult{Status: aiprovider.PermissionDenied, Source: string(decision.Effect), RuleID: decision.RuleID, RuleLabel: decision.RuleLabel}
		if decision.Approved {
			result.Status = aiprovider.PermissionAllowed
		}
		return result, err
	})
}

// WireAIProviderSamples connects visible sample preparation and session-local
// evidence to the existing Configure and durable-execution services.
func WireAIProviderSamples(compositionService *compositionsvc.CompositionService, configureService *configuresvc.ConfigureService, executionService *executionsvc.ExecutionService) {
	compositionsvc.SetAIProviderSampleMetadataLookup(compositionService, func(providerID string) (compositionsvc.AIProviderSampleMetadata, bool) {
		return configuresvc.AIProviderSampleMetadata(configureService, providerID)
	})
	executionsvc.SetAIProviderSampleRuntime(
		executionService,
		func(workflow composition.Workflow, payload string, values map[string]string, runID string) (aiprovider.SampleAttempt, bool) {
			providerID, operation, digest, ok := compositionsvc.MatchAIProviderSampleRun(workflow, payload, values)
			if !ok {
				return aiprovider.SampleAttempt{}, false
			}
			return configuresvc.CaptureAIProviderSampleAttempt(configureService, providerID, workflow.ID, runID, operation, aiprovider.SampleVersion, digest)
		},
		func(attempt aiprovider.SampleAttempt) bool {
			return configuresvc.ActivateAIProviderSampleAttempt(configureService, attempt)
		},
		func(attempt aiprovider.SampleAttempt, outcome aiprovider.SampleOutcome, checkedAt time.Time) bool {
			return configuresvc.SettleAIProviderSampleAttempt(configureService, attempt, outcome, checkedAt)
		},
	)
}
