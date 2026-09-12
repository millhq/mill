package compositionsvc

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/typedfield"
)

const aiProviderSampleVersion = "1"

const (
	aiProviderSampleOperationText           = "text"
	aiProviderSampleOperationStructured     = "structured"
	aiProviderSampleOperationClassification = "classification"
)

type aiProviderSampleDefinition struct {
	Operation      string
	SampleVersion  string
	SyntheticInput string
	Workflow       composition.Workflow
}

func buildAIProviderSampleDefinition(operation, providerID, providerLabel, safeDestination, model string) (aiProviderSampleDefinition, error) {
	var definition aiProviderSampleDefinition
	definition.Operation = operation
	definition.SampleVersion = aiProviderSampleVersion

	var aiNode composition.Node
	switch operation {
	case aiProviderSampleOperationText:
		definition.SyntheticInput = "The workshop starts at 10:00 and ends at 11:00."
		definition.Workflow.Label = "Test text: " + providerLabel
		aiNode = composition.Node{
			ID:         "provider-sample-ai",
			NodeTypeID: "process-ai-completion",
			Config: map[string]string{
				"aiproviderId": providerID,
				"systemPrompt": "Reply with plain text only.",
				"prompt":       "Summarize this sentence briefly:",
			},
		}
	case aiProviderSampleOperationStructured:
		definition.SyntheticInput = "Item: notebook. Quantity: 3. In stock: true."
		definition.Workflow.Label = "Test extraction: " + providerLabel
		outputFields := []typedfield.Field{
			{Key: "item", Label: "Item", Type: typedfield.TypeText},
			{Key: "quantity", Label: "Quantity", Type: typedfield.TypeInteger},
			{Key: "inStock", Label: "In stock", Type: typedfield.TypeBoolean},
		}
		encodedFields, err := json.Marshal(outputFields)
		if err != nil {
			return aiProviderSampleDefinition{}, fmt.Errorf("encode structured sample output fields: %w", err)
		}
		definition.Workflow.Attributes = append([]composition.AttributeDef(nil), outputFields...)
		aiNode = composition.Node{
			ID:         "provider-sample-ai",
			NodeTypeID: "process-ai-extract-structured",
			Config: map[string]string{
				"aiproviderId": providerID,
				"prompt":       "Extract the item, quantity, and in-stock status.",
				"outputFields": string(encodedFields),
			},
		}
	case aiProviderSampleOperationClassification:
		definition.SyntheticInput = "Please send the agenda when you have time."
		definition.Workflow.Label = "Test classification: " + providerLabel
		definition.Workflow.Attributes = []composition.AttributeDef{
			{Key: "category", Label: "Category", Type: typedfield.TypeText},
		}
		aiNode = composition.Node{
			ID:         "provider-sample-ai",
			NodeTypeID: "process-ai-classify",
			Config: map[string]string{
				"aiproviderId":    providerID,
				"instruction":     "Classify the message as urgent or normal.",
				"categories":      "urgent\nnormal",
				"outputAttribute": "category",
			},
		}
	default:
		return aiProviderSampleDefinition{}, fmt.Errorf("unsupported AI provider sample operation %q", operation)
	}

	definition.Workflow.Description = fmt.Sprintf(
		"Synthetic input: %s\nDestination: %s\nModel: %s\nYour provider may charge for this test.",
		definition.SyntheticInput,
		safeDestination,
		model,
	)
	nodes, err := composition.ResolveNodeDefaults([]composition.Node{
		{ID: "provider-sample-trigger", NodeTypeID: "trigger-manual"},
		{
			ID:         "provider-sample-input",
			NodeTypeID: "process-inject-text",
			Config: map[string]string{
				"text":      definition.SyntheticInput,
				"placement": "append",
			},
		},
		aiNode,
	})
	if err != nil {
		return aiProviderSampleDefinition{}, fmt.Errorf("resolve AI provider sample: %w", err)
	}
	definition.Workflow.Nodes = nodes
	definition.Workflow.Edges = []composition.Edge{
		{ID: "provider-sample-edge-input", Source: "provider-sample-trigger", Target: "provider-sample-input"},
		{ID: "provider-sample-edge-ai", Source: "provider-sample-input", Target: "provider-sample-ai"},
	}

	return definition, nil
}

func matchesAIProviderSampleRun(definition aiProviderSampleDefinition, workflow composition.Workflow, payload string, values map[string]string) bool {
	if payload != "" || len(values) != 0 {
		return false
	}
	want, ok := projectAIProviderSampleSemantics(definition.Workflow)
	if !ok {
		return false
	}
	got, ok := projectAIProviderSampleSemantics(workflow)
	return ok && reflect.DeepEqual(got, want)
}

type aiProviderSampleSemantics struct {
	Nodes      []aiProviderSampleNodeSemantics
	Edges      []aiProviderSampleEdgeSemantics
	Attributes []composition.AttributeDef
}

type aiProviderSampleNodeSemantics struct {
	Kind       composition.NodeKind
	NodeTypeID string
	Config     map[string]string
}

type aiProviderSampleEdgeSemantics struct {
	SourceHandle string
}

func projectAIProviderSampleSemantics(workflow composition.Workflow) (aiProviderSampleSemantics, bool) {
	nodesByID, outgoing, rootID, ok := indexAIProviderSampleGraph(workflow)
	if !ok {
		return aiProviderSampleSemantics{}, false
	}
	projection := aiProviderSampleSemantics{
		Nodes:      make([]aiProviderSampleNodeSemantics, 0, len(workflow.Nodes)),
		Edges:      make([]aiProviderSampleEdgeSemantics, 0, len(workflow.Edges)),
		Attributes: projectAIProviderSampleAttributes(workflow.Attributes),
	}
	return walkAIProviderSampleGraph(projection, nodesByID, outgoing, rootID, len(workflow.Edges))
}

func indexAIProviderSampleGraph(workflow composition.Workflow) (map[string]composition.Node, map[string][]composition.Edge, string, bool) {
	if len(workflow.Nodes) == 0 {
		return nil, nil, "", false
	}
	nodesByID := make(map[string]composition.Node, len(workflow.Nodes))
	incoming := make(map[string]int, len(workflow.Nodes))
	outgoing := make(map[string][]composition.Edge, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		if node.ID == "" {
			return nil, nil, "", false
		}
		if _, exists := nodesByID[node.ID]; exists {
			return nil, nil, "", false
		}
		nodesByID[node.ID] = node
	}
	for _, edge := range workflow.Edges {
		if _, exists := nodesByID[edge.Source]; !exists {
			return nil, nil, "", false
		}
		if _, exists := nodesByID[edge.Target]; !exists {
			return nil, nil, "", false
		}
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
		incoming[edge.Target]++
	}
	rootID, ok := uniqueAIProviderSampleRoot(workflow.Nodes, incoming)
	return nodesByID, outgoing, rootID, ok
}

func uniqueAIProviderSampleRoot(nodes []composition.Node, incoming map[string]int) (string, bool) {
	rootID := ""
	for _, node := range nodes {
		if incoming[node.ID] == 0 {
			if rootID != "" {
				return "", false
			}
			rootID = node.ID
		}
	}
	return rootID, rootID != ""
}

func projectAIProviderSampleAttributes(attributes []composition.AttributeDef) []composition.AttributeDef {
	projected := append([]composition.AttributeDef(nil), attributes...)
	for i := range projected {
		projected[i].Label = ""
		projected[i].Description = ""
	}
	return projected
}

func walkAIProviderSampleGraph(
	projection aiProviderSampleSemantics,
	nodesByID map[string]composition.Node,
	outgoing map[string][]composition.Edge,
	rootID string,
	edgeCount int,
) (aiProviderSampleSemantics, bool) {
	visited := make(map[string]bool, len(nodesByID))
	for nodeID := rootID; ; {
		if visited[nodeID] {
			return aiProviderSampleSemantics{}, false
		}
		visited[nodeID] = true
		node := nodesByID[nodeID]
		config, ok := projectAIProviderSampleNodeConfig(node)
		if !ok {
			return aiProviderSampleSemantics{}, false
		}
		projection.Nodes = append(projection.Nodes, aiProviderSampleNodeSemantics{
			Kind:       node.Kind,
			NodeTypeID: node.NodeTypeID,
			Config:     config,
		})

		edges := outgoing[nodeID]
		if len(edges) == 0 {
			break
		}
		if len(edges) != 1 {
			return aiProviderSampleSemantics{}, false
		}
		projection.Edges = append(projection.Edges, aiProviderSampleEdgeSemantics{SourceHandle: edges[0].SourceHandle})
		nodeID = edges[0].Target
	}
	if len(visited) != len(nodesByID) || len(projection.Edges) != edgeCount {
		return aiProviderSampleSemantics{}, false
	}
	return projection, true
}

func projectAIProviderSampleNodeConfig(node composition.Node) (map[string]string, bool) {
	config := make(map[string]string, len(node.Config))
	for key, value := range node.Config {
		config[key] = value
	}
	if node.NodeTypeID != "process-ai-extract-structured" {
		return config, true
	}

	var fields []typedfield.Field
	if err := json.Unmarshal([]byte(config["outputFields"]), &fields); err != nil {
		return nil, false
	}
	for i := range fields {
		fields[i].Label = ""
		fields[i].Description = ""
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	config["outputFields"] = string(encoded)
	return config, true
}
