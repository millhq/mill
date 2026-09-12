package compositionsvc

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alicoding/mill/internal/adapters/settings"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/servicetest"
)

func TestAIProviderSampleWorkflowIDIsStableAndOperationScoped(t *testing.T) {
	got, err := aiProviderSampleWorkflowID("provider-1", aiprovider.OperationText)
	if err != nil {
		t.Fatalf("aiProviderSampleWorkflowID: %v", err)
	}
	want := "aiprovider-sample-51513d4c5bf572e43eaef989d8fb56a68a5a0b8939a81cedde6669c7c824fc18"
	if got != want {
		t.Errorf("workflow ID = %q, want %q", got, want)
	}
	structured, err := aiProviderSampleWorkflowID("provider-1", aiprovider.OperationStructured)
	if err != nil {
		t.Fatalf("structured ID: %v", err)
	}
	if structured == got {
		t.Fatal("text and structured operations shared a workflow ID")
	}
}

func TestPrepareAIProviderFeatureSampleReusesAcrossRestart(t *testing.T) {
	store, err := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	metadata := sampleMetadataLookup()
	firstService := NewCompositionService(store)
	first, err := prepareAIProviderFeatureSample(firstService, newAIProviderSampleCoordinator(metadata), "provider-1", aiprovider.OperationStructured)
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	if first.Status != aiprovider.SampleStatusCreated {
		t.Errorf("first status = %q, want created", first.Status)
	}

	secondService := NewCompositionService(store)
	second, err := prepareAIProviderFeatureSample(secondService, newAIProviderSampleCoordinator(metadata), "provider-1", aiprovider.OperationStructured)
	if err != nil {
		t.Fatalf("Prepare after restart: %v", err)
	}
	if second.Status != aiprovider.SampleStatusExisting || second.WorkflowID != first.WorkflowID {
		t.Errorf("Prepare after restart = %+v, want existing ID %q", second, first.WorkflowID)
	}
	if second.SafeDestination != "https://gateway.example/v1" || second.Model != "model-1" || second.ConfigRevision != "revision-1" {
		t.Errorf("preview metadata = %+v", second)
	}
}

func TestConcurrentPrepareAIProviderFeatureSampleCreatesOneWorkflow(t *testing.T) {
	store, err := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	service := NewCompositionService(store)
	coordinator := newAIProviderSampleCoordinator(sampleMetadataLookup())
	const callers = 16
	results := make(chan aiprovider.SamplePreview, callers)
	errors := make(chan error, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			preview, prepareErr := prepareAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationText)
			if prepareErr != nil {
				errors <- prepareErr
				return
			}
			results <- preview
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	for prepareErr := range errors {
		t.Errorf("concurrent Prepare: %v", prepareErr)
	}
	created := 0
	workflowID := ""
	for result := range results {
		if result.Status == aiprovider.SampleStatusCreated {
			created++
		}
		if workflowID == "" {
			workflowID = result.WorkflowID
		} else if result.WorkflowID != workflowID {
			t.Errorf("workflow ID = %q, want %q", result.WorkflowID, workflowID)
		}
	}
	if created != 1 {
		t.Errorf("created results = %d, want 1", created)
	}
	matches := 0
	for _, workflow := range service.Workflows() {
		if workflow.ID == workflowID {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("persisted workflows with sample ID = %d, want 1", matches)
	}
}

func TestPreparePreservesModifiedWorkflowUntilExplicitRestore(t *testing.T) {
	store, err := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	service := NewCompositionService(store)
	coordinator := newAIProviderSampleCoordinator(sampleMetadataLookup())
	created, err := prepareAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationClassification)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	workflow, ok := workflowByID(service, created.WorkflowID)
	if !ok {
		t.Fatal("created workflow missing")
	}
	workflow.Nodes[2].Config["instruction"] = "User instruction"
	if _, err := service.UpdateWorkflow(workflow.ID, "User label", "User description", workflow.Nodes, workflow.Edges); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}

	modified, err := prepareAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationClassification)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if modified.Status != aiprovider.SampleStatusModified {
		t.Errorf("second Prepare status = %q, want modified", modified.Status)
	}
	unchanged, _ := workflowByID(service, created.WorkflowID)
	if unchanged.Nodes[2].Config["instruction"] != "User instruction" {
		t.Fatal("Prepare silently overwrote the user's edit")
	}

	before := unchanged
	service.mu.Lock()
	for i := range service.user {
		if service.user[i].ID == created.WorkflowID {
			service.user[i].BuiltIn = true
			service.user[i].Disabled = true
			service.user[i].PublishedVersion = 2
			service.user[i].OfferOnRequestID = "request-1"
			service.user[i].DefaultEnvironmentID = "environment-1"
			service.user[i].Notes = []composition.Note{{ID: "note-1", Text: "keep me"}}
			before = service.user[i]
		}
	}
	if err := persistCompleteWorkflowsLocked(service); err != nil {
		service.mu.Unlock()
		t.Fatalf("persist test lifecycle state: %v", err)
	}
	service.mu.Unlock()

	restoredPreview, err := restoreAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationClassification)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restoredPreview.WorkflowID != created.WorkflowID {
		t.Errorf("restored ID = %q, want %q", restoredPreview.WorkflowID, created.WorkflowID)
	}
	restored, _ := workflowByID(service, created.WorkflowID)
	if restored.Nodes[2].Config["instruction"] == "User instruction" {
		t.Fatal("Restore did not repair the sample instruction")
	}
	if restored.ID != before.ID || restored.BuiltIn != before.BuiltIn || restored.Disabled != before.Disabled ||
		restored.PublishedVersion != before.PublishedVersion || restored.OfferOnRequestID != before.OfferOnRequestID ||
		restored.DefaultEnvironmentID != before.DefaultEnvironmentID || restored.CreatedAt != before.CreatedAt ||
		len(restored.Notes) != 1 || restored.Notes[0].Text != "keep me" {
		t.Errorf("Restore changed preserved workflow state: before=%+v after=%+v", before, restored)
	}
	if !restored.UpdatedAt.After(before.UpdatedAt) && restored.UpdatedAt != before.UpdatedAt {
		t.Errorf("restored UpdatedAt = %v, before %v", restored.UpdatedAt, before.UpdatedAt)
	}
}

func TestDeletingSampleAllowsExplicitPrepareToRecreateSameID(t *testing.T) {
	store, err := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	service := NewCompositionService(store)
	coordinator := newAIProviderSampleCoordinator(sampleMetadataLookup())
	first, err := prepareAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationText)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := service.DeleteWorkflow(first.WorkflowID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	second, err := prepareAIProviderFeatureSample(service, coordinator, "provider-1", aiprovider.OperationText)
	if err != nil {
		t.Fatalf("Prepare after delete: %v", err)
	}
	if second.Status != aiprovider.SampleStatusCreated || second.WorkflowID != first.WorkflowID {
		t.Errorf("recreated preview = %+v, want created with ID %q", second, first.WorkflowID)
	}
}

func TestPreparePersistenceFailureLeavesNoHalfWorkflow(t *testing.T) {
	store := servicetest.NewFakeStore()
	service := NewCompositionService(store)
	before := len(service.Workflows())
	store.SetErr = fmt.Errorf("disk full")
	preview, err := prepareAIProviderFeatureSample(service, newAIProviderSampleCoordinator(sampleMetadataLookup()), "provider-1", aiprovider.OperationStructured)
	if err == nil {
		t.Fatalf("Prepare = %+v, want persistence error", preview)
	}
	if got := len(service.Workflows()); got != before {
		t.Errorf("workflow count after failed persist = %d, want %d", got, before)
	}
}

func TestPrepareRejectsUnknownProviderAndOperation(t *testing.T) {
	service := NewCompositionService(servicetest.NewFakeStore())
	coordinator := newAIProviderSampleCoordinator(sampleMetadataLookup())
	if _, err := prepareAIProviderFeatureSample(service, coordinator, "missing", aiprovider.OperationText); err == nil {
		t.Fatal("Prepare accepted unknown provider")
	}
	if _, err := prepareAIProviderFeatureSample(service, coordinator, "provider-1", "embedding"); err == nil {
		t.Fatal("Prepare accepted unsupported operation")
	}
}

func TestMatchAIProviderSampleRunRequiresStableIdentityAndCanonicalInputs(t *testing.T) {
	service := NewCompositionService(servicetest.NewFakeStore())
	preview, err := prepareAIProviderFeatureSample(service, newAIProviderSampleCoordinator(sampleMetadataLookup()), "provider-1", aiprovider.OperationStructured)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	workflow, _ := workflowByID(service, preview.WorkflowID)
	providerID, operation, digest, ok := MatchAIProviderSampleRun(workflow, "", nil)
	if !ok || providerID != "provider-1" || operation != aiprovider.OperationStructured || digest == "" {
		t.Fatalf("MatchAIProviderSampleRun = (%q, %q, %q, %t)", providerID, operation, digest, ok)
	}

	clone := workflow
	clone.ID = "ordinary-copy"
	if _, _, _, ok := MatchAIProviderSampleRun(clone, "", nil); ok {
		t.Fatal("ordinary workflow copy matched the stable sample identity")
	}
	if _, _, _, ok := MatchAIProviderSampleRun(workflow, "override", nil); ok {
		t.Fatal("nonempty payload matched")
	}
	if _, _, _, ok := MatchAIProviderSampleRun(workflow, "", map[string]string{"item": "override"}); ok {
		t.Fatal("values override matched")
	}
	workflow.Nodes[2].Config["prompt"] = "changed"
	if _, _, _, ok := MatchAIProviderSampleRun(workflow, "", nil); ok {
		t.Fatal("modified sample graph matched")
	}
}

func sampleMetadataLookup() func(string) (AIProviderSampleMetadata, bool) {
	return func(id string) (AIProviderSampleMetadata, bool) {
		if id != "provider-1" {
			return AIProviderSampleMetadata{}, false
		}
		return AIProviderSampleMetadata{
			ProviderID:      id,
			ProviderLabel:   "Provider",
			SafeDestination: "https://gateway.example/v1",
			Model:           "model-1",
			ConfigRevision:  "revision-1",
		}, true
	}
}
