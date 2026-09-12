package compositionsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/dataevent"
)

const aiProviderSampleIdentityNamespace = "mill.ai-provider-sample.identity.v1"

// AIProviderSampleMetadata is the credential-free snapshot Configure supplies
// while preparing or restoring a sample. SafeDestination is already suitable
// for display; compositionsvc does not parse it or infer execution locality.
type AIProviderSampleMetadata struct {
	ProviderID      string
	ProviderLabel   string
	SafeDestination string
	Model           string
	ConfigRevision  string
}

type aiProviderSampleCoordinator struct {
	mu       sync.Mutex
	metadata func(string) (AIProviderSampleMetadata, bool)
}

func newAIProviderSampleCoordinator(metadata func(string) (AIProviderSampleMetadata, bool)) *aiProviderSampleCoordinator {
	return &aiProviderSampleCoordinator{metadata: metadata}
}

// SetAIProviderSampleMetadataLookup wires the passive Configure projection
// used by the public Prepare/Restore operations. It is a package function so
// the internal callback is never reflected as a Wails RPC.
func SetAIProviderSampleMetadataLookup(c *CompositionService, metadata func(string) (AIProviderSampleMetadata, bool)) {
	if c == nil {
		return
	}
	if c.aiProviderSamples == nil {
		c.aiProviderSamples = newAIProviderSampleCoordinator(metadata)
		return
	}
	c.aiProviderSamples.mu.Lock()
	c.aiProviderSamples.metadata = metadata
	c.aiProviderSamples.mu.Unlock()
}

// PrepareAIProviderFeatureSample prepares or reopens the one ordinary sample
// workflow associated with this provider and operation. It never runs it.
func (c *CompositionService) PrepareAIProviderFeatureSample(providerID string, operation aiprovider.Operation) (aiprovider.SamplePreview, error) {
	return prepareAIProviderFeatureSample(c, c.aiProviderSamples, providerID, operation)
}

// RestoreAIProviderFeatureSample explicitly restores a modified sample's
// saved definition while preserving its workflow identity and lifecycle.
func (c *CompositionService) RestoreAIProviderFeatureSample(providerID string, operation aiprovider.Operation) (aiprovider.SamplePreview, error) {
	return restoreAIProviderFeatureSample(c, c.aiProviderSamples, providerID, operation)
}

func aiProviderSampleWorkflowID(providerID string, operation aiprovider.Operation) (string, error) {
	identity, err := json.Marshal([]string{aiProviderSampleIdentityNamespace, providerID, string(operation)})
	if err != nil {
		return "", fmt.Errorf("encode AI provider sample identity: %w", err)
	}
	sum := sha256.Sum256(identity)
	return "aiprovider-sample-" + hex.EncodeToString(sum[:]), nil
}

// MatchAIProviderSampleRun recognizes only the deterministic provider sample
// identity and its exact canonical execution semantics. It returns the digest
// of the AI node's real output contract for a fresh-run attempt token.
func MatchAIProviderSampleRun(workflow composition.Workflow, payload string, values map[string]string) (string, aiprovider.Operation, string, bool) {
	var providerID string
	var operation aiprovider.Operation
	var aiNode composition.Node
	for _, node := range workflow.Nodes {
		var candidate aiprovider.Operation
		switch node.NodeTypeID {
		case "process-ai-completion":
			candidate = aiprovider.OperationText
		case "process-ai-extract-structured":
			candidate = aiprovider.OperationStructured
		case "process-ai-classify":
			candidate = aiprovider.OperationClassification
		default:
			continue
		}
		if operation != "" {
			return "", "", "", false
		}
		operation = candidate
		providerID = node.Config["aiproviderId"]
		aiNode = node
	}
	if providerID == "" || !aiprovider.IsSampleOperation(operation) {
		return "", "", "", false
	}
	wantID, err := aiProviderSampleWorkflowID(providerID, operation)
	if err != nil || workflow.ID != wantID {
		return "", "", "", false
	}
	definition, err := buildAIProviderSampleDefinition(string(operation), providerID, "", "", "")
	if err != nil || !matchesAIProviderSampleRun(definition, workflow, payload, values) {
		return "", "", "", false
	}
	digest, err := composition.AIProviderSampleOutputContractDigest(aiNode)
	if err != nil {
		return "", "", "", false
	}
	return providerID, operation, digest, true
}

func prepareAIProviderFeatureSample(
	c *CompositionService,
	coordinator *aiProviderSampleCoordinator,
	providerID string,
	operation aiprovider.Operation,
) (aiprovider.SamplePreview, error) {
	if c == nil || coordinator == nil || coordinator.metadata == nil {
		return aiprovider.SamplePreview{}, fmt.Errorf("AI provider sample service is not configured")
	}
	if !aiprovider.IsSampleOperation(operation) {
		return aiprovider.SamplePreview{}, fmt.Errorf("unsupported AI provider sample operation %q", operation)
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	metadata, ok := coordinator.metadata(providerID)
	if !ok || metadata.ProviderID != providerID {
		return aiprovider.SamplePreview{}, fmt.Errorf("unknown AI provider %q", providerID)
	}
	definition, err := buildAIProviderSampleDefinition(
		string(operation),
		providerID,
		metadata.ProviderLabel,
		metadata.SafeDestination,
		metadata.Model,
	)
	if err != nil {
		return aiprovider.SamplePreview{}, err
	}
	workflowID, err := aiProviderSampleWorkflowID(providerID, operation)
	if err != nil {
		return aiprovider.SamplePreview{}, err
	}
	definition.Workflow.ID = workflowID

	status := aiprovider.SampleStatusCreated
	if existing, exists := workflowByID(c, workflowID); exists {
		status = aiprovider.SampleStatusModified
		if matchesAIProviderSampleRun(definition, existing, "", nil) {
			status = aiprovider.SampleStatusExisting
		}
	} else if _, err := createCompleteWorkflow(c, definition.Workflow); err != nil {
		return aiprovider.SamplePreview{}, err
	}
	return samplePreview(definition, metadata, workflowID, status), nil
}

func restoreAIProviderFeatureSample(
	c *CompositionService,
	coordinator *aiProviderSampleCoordinator,
	providerID string,
	operation aiprovider.Operation,
) (aiprovider.SamplePreview, error) {
	if c == nil || coordinator == nil || coordinator.metadata == nil {
		return aiprovider.SamplePreview{}, fmt.Errorf("AI provider sample service is not configured")
	}
	if !aiprovider.IsSampleOperation(operation) {
		return aiprovider.SamplePreview{}, fmt.Errorf("unsupported AI provider sample operation %q", operation)
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	metadata, ok := coordinator.metadata(providerID)
	if !ok || metadata.ProviderID != providerID {
		return aiprovider.SamplePreview{}, fmt.Errorf("unknown AI provider %q", providerID)
	}
	definition, err := buildAIProviderSampleDefinition(
		string(operation),
		providerID,
		metadata.ProviderLabel,
		metadata.SafeDestination,
		metadata.Model,
	)
	if err != nil {
		return aiprovider.SamplePreview{}, err
	}
	workflowID, err := aiProviderSampleWorkflowID(providerID, operation)
	if err != nil {
		return aiprovider.SamplePreview{}, err
	}
	definition.Workflow.ID = workflowID
	if _, exists := workflowByID(c, workflowID); !exists {
		return aiprovider.SamplePreview{}, fmt.Errorf("no AI provider sample workflow for provider %q and operation %q", providerID, operation)
	}
	if _, err := restoreCompleteWorkflow(c, definition.Workflow); err != nil {
		return aiprovider.SamplePreview{}, err
	}
	return samplePreview(definition, metadata, workflowID, aiprovider.SampleStatusExisting), nil
}

func samplePreview(definition aiProviderSampleDefinition, metadata AIProviderSampleMetadata, workflowID string, status aiprovider.SampleStatus) aiprovider.SamplePreview {
	return aiprovider.SamplePreview{
		WorkflowID:      workflowID,
		Operation:       aiprovider.Operation(definition.Operation),
		SampleVersion:   definition.SampleVersion,
		Status:          status,
		SyntheticInput:  definition.SyntheticInput,
		SafeDestination: metadata.SafeDestination,
		Model:           metadata.Model,
		ConfigRevision:  metadata.ConfigRevision,
	}
}

func workflowByID(c *CompositionService, id string) (composition.Workflow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, workflow := range c.user {
		if workflow.ID == id {
			return workflow, true
		}
	}
	return composition.Workflow{}, false
}

// createCompleteWorkflow is the complete-definition counterpart of ordinary
// CreateWorkflow. It validates nodes and Attributes together and publishes one
// complete value to the existing workflows blob.
func createCompleteWorkflow(c *CompositionService, definition composition.Workflow) (composition.Workflow, error) {
	if strings.TrimSpace(definition.Label) == "" {
		return composition.Workflow{}, fmt.Errorf("a workflow needs a label")
	}
	if definition.ID == "" {
		return composition.Workflow{}, fmt.Errorf("a workflow needs an ID")
	}
	if len(definition.Nodes) == 0 {
		return composition.Workflow{}, fmt.Errorf("a workflow needs at least one step")
	}
	resolved, err := composition.ResolveNodeDefaults(definition.Nodes)
	if err != nil {
		return composition.Workflow{}, err
	}
	if err := composition.ValidateGraphStrict(resolved, definition.Edges, definition.Attributes); err != nil {
		return composition.Workflow{}, err
	}
	now := time.Now()
	workflow := composition.Workflow{
		ID:          definition.ID,
		Label:       definition.Label,
		Description: definition.Description,
		Nodes:       resolved,
		Edges:       definition.Edges,
		Notes:       definition.Notes,
		Attributes:  definition.Attributes,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	c.mu.Lock()
	for _, existing := range c.user {
		if existing.ID == workflow.ID {
			c.mu.Unlock()
			return composition.Workflow{}, fmt.Errorf("a workflow with id %q already exists", workflow.ID)
		}
	}
	c.user = append(c.user, workflow)
	if err := persistCompleteWorkflowsLocked(c); err != nil {
		c.removeByIDLocked(workflow.ID)
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("save workflow: %w", err)
	}
	c.mu.Unlock()
	c.notifySyncer()
	dataevent.Emit("workflow", workflow.ID)
	return workflow, nil
}

// restoreCompleteWorkflow replaces authored sample content through one
// validated persistence operation while preserving the workflow's identity,
// history, lifecycle, notes, and environment selection. Like UpdateWorkflow,
// it touches seed provenance because restoring an imported or seeded workflow
// is a real draft content mutation.
func restoreCompleteWorkflow(c *CompositionService, definition composition.Workflow) (composition.Workflow, error) {
	resolved, err := composition.ResolveNodeDefaults(definition.Nodes)
	if err != nil {
		return composition.Workflow{}, err
	}
	if err := composition.ValidateGraphStrict(resolved, definition.Edges, definition.Attributes); err != nil {
		return composition.Workflow{}, err
	}

	return c.mutateWorkflow(definition.ID, func(restored composition.Workflow) (composition.Workflow, error) {
		restored.Label = definition.Label
		restored.Description = definition.Description
		restored.Nodes = resolved
		restored.Edges = definition.Edges
		restored.Attributes = definition.Attributes
		return restored, nil
	})
}

func persistCompleteWorkflowsLocked(c *CompositionService) error {
	data, err := json.Marshal(c.user)
	if err != nil {
		return fmt.Errorf("marshal workflows: %w", err)
	}
	if err := c.store.Set(workflowsKey, string(data)); err != nil {
		return fmt.Errorf("persist workflows: %w", err)
	}
	return nil
}
