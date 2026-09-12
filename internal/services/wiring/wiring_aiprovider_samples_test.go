package wiring

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicoding/mill/internal/adapters/credential"
	"github.com/alicoding/mill/internal/adapters/dataownership"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/configuresvc"
	"github.com/alicoding/mill/internal/services/executionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/alicoding/mill/internal/services/servicetest"
)

func TestWireAIProviderSamplesRecordsOrdinaryRunEvidenceAcrossProtocols(t *testing.T) {
	tests := []struct {
		name         string
		kind         aiprovider.Kind
		operation    aiprovider.Operation
		wantPath     string
		responseBody string
	}{
		{
			name:         "OpenAI-compatible text",
			kind:         aiprovider.KindOpenAICompat,
			operation:    aiprovider.OperationText,
			wantPath:     "/v1/chat/completions",
			responseBody: `{"choices":[{"message":{"content":"brief summary"}}]}`,
		},
		{
			name:         "Anthropic classification",
			kind:         aiprovider.KindAnthropic,
			operation:    aiprovider.OperationClassification,
			wantPath:     "/v1/messages",
			responseBody: `{"content":[{"type":"tool_use","id":"tool-1","name":"classification","input":{"category":"normal"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("provider request path = %q, want %q", r.URL.Path, tt.wantPath)
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tt.responseBody)
			}))
			defer providerServer.Close()

			store := servicetest.NewFakeStore()
			comp := compositionsvc.NewCompositionService(store)
			cfg := configuresvc.NewConfigureService(store, comp, credential.NewInMemory())
			configuresvc.SetAIProviderMutationCoordinator(cfg, func(mutate func(assertUnused func(id string) error) error) error {
				return mutate(func(string) error { return nil })
			}, nil)
			provider, err := cfg.CreateAIProvider(tt.name, tt.kind, providerServer.URL, "sample-model", "")
			if err != nil {
				t.Fatalf("CreateAIProvider: %v", err)
			}

			exec, err := executionsvc.NewExecutionServiceWithOwnership(
				"sqlite:"+filepath.Join(t.TempDir(), "sample-evidence.db"),
				dataownership.ExecutionOwnershipPrivateMemory,
				comp,
				guardrailsvc.NewGuardrailService(store, comp),
				func(exec *executionsvc.ExecutionService) { WireAIProviderSamples(comp, cfg, exec) },
			)
			if err != nil {
				t.Fatalf("NewExecutionServiceWithOwnership: %v", err)
			}
			defer func() {
				if err := exec.Shutdown(2 * time.Second); err != nil {
					t.Errorf("Shutdown: %v", err)
				}
			}()

			preview, err := comp.PrepareAIProviderFeatureSample(provider.ID, tt.operation)
			if err != nil {
				t.Fatalf("PrepareAIProviderFeatureSample: %v", err)
			}
			run, err := exec.RunWorkflow(preview.WorkflowID, executionsvc.RunKindTest, nil)
			if err != nil {
				t.Fatalf("RunWorkflow: %v", err)
			}
			if run.Status != "SUCCESS" {
				t.Fatalf("run status = %q, want SUCCESS", run.Status)
			}

			feature := awaitAIProviderSampleFeature(t, cfg, provider.ID, tt.operation, run.RunID)
			if requests.Load() != 1 {
				t.Errorf("provider requests = %d, want exactly 1", requests.Load())
			}
			if feature.Support != aiprovider.SupportSupported || feature.Evidence != aiprovider.EvidenceSampleTest {
				t.Errorf("feature support/evidence = %q/%q, want supported/sample-test", feature.Support, feature.Evidence)
			}
			if feature.LastSampleAttempt == nil || feature.LastSampleSuccess == nil {
				t.Fatalf("sample evidence = attempt %+v success %+v", feature.LastSampleAttempt, feature.LastSampleSuccess)
			}
			if feature.LastSampleAttempt.RunID != run.RunID || feature.LastSampleSuccess.RunID != run.RunID {
				t.Errorf("sample run IDs = attempt %q success %q, want %q", feature.LastSampleAttempt.RunID, feature.LastSampleSuccess.RunID, run.RunID)
			}
			if feature.LastSampleSuccess.Outcome != aiprovider.SampleOutcomeSucceeded ||
				feature.LastSampleSuccess.Freshness != aiprovider.FreshnessFresh ||
				feature.LastSampleSuccess.Authentication != aiprovider.AuthenticationOperationTested {
				t.Errorf("successful evidence = %+v", feature.LastSampleSuccess)
			}
		})
	}
}

func awaitAIProviderSampleFeature(t *testing.T, cfg *configuresvc.ConfigureService, providerID string, operation aiprovider.Operation, runID string) aiprovider.OperationFeature {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, feature := range cfg.GetAIProviderAvailability(providerID).Operations {
			if feature.Operation == operation && feature.LastSampleAttempt != nil && feature.LastSampleAttempt.RunID == runID {
				return feature
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for sample evidence for provider %q operation %q run %q", providerID, operation, runID)
	return aiprovider.OperationFeature{}
}
