package mcpsvc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type aiProviderSampleService interface {
	PrepareAIProviderFeatureSample(string, aiprovider.Operation) (aiprovider.SamplePreview, error)
	RestoreAIProviderFeatureSample(string, aiprovider.Operation) (aiprovider.SamplePreview, error)
}

type aiProviderSampleArgs struct {
	ProviderID string               `json:"providerId" jsonschema:"the configured AI provider's ID"`
	Operation  aiprovider.Operation `json:"operation" jsonschema:"the provider operation to demonstrate: text, structured, or classification"`
}

// registerAIProviderSampleTools exposes Prepare and Restore through the same
// durable MCP write gate as ordinary workflow mutations. The concrete service
// is passed explicitly so this new module does not invent a parallel registry.
func (m *MillMCPService) registerAIProviderSampleTools(samples aiProviderSampleService) {
	m.registerAIProviderSampleTool(aiProviderSampleTool{
		name:        "prepare_ai_provider_feature_sample",
		verb:        "Prepare",
		description: "Prepare one ordinary visible workflow that demonstrates a configured AI provider operation. Reuses the provider-operation sample and preserves user edits. Requires the MCP writes toggle and ordinary per-write approval.",
		annotations: editAnnotations,
		execute:     samples.PrepareAIProviderFeatureSample,
	})
	m.registerAIProviderSampleTool(aiProviderSampleTool{
		name:        "restore_ai_provider_feature_sample",
		verb:        "Restore",
		description: "Restore a modified AI provider sample workflow to Mill's current sample definition. Keeps the workflow ID and draft history; does not publish or run it. Requires the MCP writes toggle and ordinary per-write approval.",
		annotations: replaceAnnotations,
		execute:     samples.RestoreAIProviderFeatureSample,
	})
}

type aiProviderSampleTool struct {
	name        string
	verb        string
	description string
	annotations *mcp.ToolAnnotations
	execute     func(string, aiprovider.Operation) (aiprovider.SamplePreview, error)
}

func (m *MillMCPService) registerAIProviderSampleTool(tool aiProviderSampleTool) {
	m.registerWriteExecutor(tool.name, func(argsJSON string) (string, error) {
		var in aiProviderSampleArgs
		if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
			return "", err
		}
		preview, err := tool.execute(in.ProviderID, in.Operation)
		if err != nil {
			return "", err
		}
		return jsonText(preview)
	})
	mcp.AddTool(m.server, &mcp.Tool{
		Name:        tool.name,
		Description: tool.description,
		Annotations: tool.annotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in aiProviderSampleArgs) (*mcp.CallToolResult, any, error) {
		if err := m.requireWriteEnabled(); err != nil {
			return nil, nil, err
		}
		argsJSON, err := marshalArgs(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.gateWrite(
			tool.name,
			fmt.Sprintf("%s the %s sample workflow for AI provider %s", tool.verb, in.Operation, in.ProviderID),
			argsJSON,
		)
		return res, nil, err
	})
}
