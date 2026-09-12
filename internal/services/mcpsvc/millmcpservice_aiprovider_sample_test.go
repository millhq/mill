package mcpsvc

import (
	"encoding/json"
	"testing"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/configuresvc"
	"github.com/alicoding/mill/internal/services/servicetest"
)

func TestAIProviderSampleMCPExecutorsUseCompositionSampleLifecycle(t *testing.T) {
	store := servicetest.NewFakeStore()
	comp := compositionsvc.NewCompositionService(store)
	cfg := configuresvc.NewConfigureService(store, comp, servicetest.FakeCredentialStore{})
	configuresvc.SetAIProviderMutationCoordinator(cfg, func(mutate func(assertUnused func(id string) error) error) error {
		return mutate(func(string) error { return nil })
	}, nil)
	provider, err := cfg.CreateAIProvider("MCP sample", aiprovider.KindOpenAICompat, "http://localhost:11434", "sample-model", "")
	if err != nil {
		t.Fatalf("CreateAIProvider: %v", err)
	}
	compositionsvc.SetAIProviderSampleMetadataLookup(comp, func(providerID string) (compositionsvc.AIProviderSampleMetadata, bool) {
		return configuresvc.AIProviderSampleMetadata(cfg, providerID)
	})

	service := NewMillMCPService("0.0.0-test", comp, cfg, store, nil)
	args, err := json.Marshal(aiProviderSampleArgs{ProviderID: provider.ID, Operation: aiprovider.OperationStructured})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	prepare := service.executors["prepare_ai_provider_feature_sample"]
	restore := service.executors["restore_ai_provider_feature_sample"]
	if prepare == nil || restore == nil {
		t.Fatalf("sample executors = prepare %v restore %v, want both registered", prepare != nil, restore != nil)
	}

	result, err := prepare(string(args))
	if err != nil {
		t.Fatalf("prepare executor: %v", err)
	}
	var preview aiprovider.SamplePreview
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatalf("decode prepare result: %v", err)
	}
	if preview.Status != aiprovider.SampleStatusCreated || preview.WorkflowID == "" {
		t.Fatalf("prepare preview = %+v", preview)
	}

	result, err = restore(string(args))
	if err != nil {
		t.Fatalf("restore executor: %v", err)
	}
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatalf("decode restore result: %v", err)
	}
	if preview.Status != aiprovider.SampleStatusExisting || preview.WorkflowID == "" {
		t.Fatalf("restore preview = %+v", preview)
	}
}
