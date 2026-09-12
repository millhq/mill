package compositionsvc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/typedfield"
)

func TestBuildAIProviderSampleDefinition(t *testing.T) {
	const (
		providerID      = "provider-1"
		providerLabel   = "Example provider"
		safeDestination = "https://gateway.example/v1"
		model           = "example-model"
	)

	tests := []struct {
		name           string
		operation      string
		wantLabel      string
		wantInput      string
		wantAIType     string
		wantAIConfig   map[string]string
		wantAttributes []composition.AttributeDef
	}{
		{
			name:       "text",
			operation:  aiProviderSampleOperationText,
			wantLabel:  "Test text: Example provider",
			wantInput:  "The workshop starts at 10:00 and ends at 11:00.",
			wantAIType: "process-ai-completion",
			wantAIConfig: map[string]string{
				"aiproviderId": providerID,
				"systemPrompt": "Reply with plain text only.",
				"prompt":       "Summarize this sentence briefly:",
			},
		},
		{
			name:       "structured",
			operation:  aiProviderSampleOperationStructured,
			wantLabel:  "Test extraction: Example provider",
			wantInput:  "Item: notebook. Quantity: 3. In stock: true.",
			wantAIType: "process-ai-extract-structured",
			wantAIConfig: map[string]string{
				"aiproviderId": providerID,
				"prompt":       "Extract the item, quantity, and in-stock status.",
			},
			wantAttributes: []composition.AttributeDef{
				{Key: "item", Label: "Item", Type: typedfield.TypeText},
				{Key: "quantity", Label: "Quantity", Type: typedfield.TypeInteger},
				{Key: "inStock", Label: "In stock", Type: typedfield.TypeBoolean},
			},
		},
		{
			name:       "classification",
			operation:  aiProviderSampleOperationClassification,
			wantLabel:  "Test classification: Example provider",
			wantInput:  "Please send the agenda when you have time.",
			wantAIType: "process-ai-classify",
			wantAIConfig: map[string]string{
				"aiproviderId":    providerID,
				"instruction":     "Classify the message as urgent or normal.",
				"categories":      "urgent\nnormal",
				"outputAttribute": "category",
			},
			wantAttributes: []composition.AttributeDef{
				{Key: "category", Label: "Category", Type: typedfield.TypeText},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition, err := buildAIProviderSampleDefinition(tt.operation, providerID, providerLabel, safeDestination, model)
			if err != nil {
				t.Fatalf("buildAIProviderSampleDefinition: %v", err)
			}
			if definition.Operation != tt.operation {
				t.Errorf("Operation = %q, want %q", definition.Operation, tt.operation)
			}
			if definition.SampleVersion != "1" {
				t.Errorf("SampleVersion = %q, want 1", definition.SampleVersion)
			}
			if definition.SyntheticInput != tt.wantInput {
				t.Errorf("SyntheticInput = %q, want %q", definition.SyntheticInput, tt.wantInput)
			}
			if definition.Workflow.Label != tt.wantLabel {
				t.Errorf("Workflow.Label = %q, want %q", definition.Workflow.Label, tt.wantLabel)
			}
			wantDescription := "Synthetic input: " + tt.wantInput + "\nDestination: " + safeDestination + "\nModel: " + model + "\nYour provider may charge for this test."
			if definition.Workflow.Description != wantDescription {
				t.Errorf("Workflow.Description = %q, want %q", definition.Workflow.Description, wantDescription)
			}
			if strings.Contains(definition.Workflow.Description, "Execution location") {
				t.Errorf("Workflow.Description inferred execution locality: %q", definition.Workflow.Description)
			}

			if len(definition.Workflow.Nodes) != 3 {
				t.Fatalf("len(Workflow.Nodes) = %d, want 3", len(definition.Workflow.Nodes))
			}
			trigger, input, ai := definition.Workflow.Nodes[0], definition.Workflow.Nodes[1], definition.Workflow.Nodes[2]
			if trigger.NodeTypeID != "trigger-manual" {
				t.Errorf("trigger NodeTypeID = %q, want trigger-manual", trigger.NodeTypeID)
			}
			if input.NodeTypeID != "process-inject-text" {
				t.Errorf("input NodeTypeID = %q, want process-inject-text", input.NodeTypeID)
			}
			if got := input.Config; !reflect.DeepEqual(got, map[string]string{"text": tt.wantInput, "placement": "append"}) {
				t.Errorf("input Config = %#v", got)
			}
			if ai.NodeTypeID != tt.wantAIType {
				t.Errorf("AI NodeTypeID = %q, want %q", ai.NodeTypeID, tt.wantAIType)
			}
			for key, want := range tt.wantAIConfig {
				if got := ai.Config[key]; got != want {
					t.Errorf("AI Config[%q] = %q, want %q", key, got, want)
				}
			}
			if !reflect.DeepEqual(definition.Workflow.Attributes, tt.wantAttributes) {
				t.Errorf("Workflow.Attributes = %#v, want %#v", definition.Workflow.Attributes, tt.wantAttributes)
			}
			if len(definition.Workflow.Edges) != 2 ||
				definition.Workflow.Edges[0].Source != trigger.ID || definition.Workflow.Edges[0].Target != input.ID ||
				definition.Workflow.Edges[1].Source != input.ID || definition.Workflow.Edges[1].Target != ai.ID {
				t.Errorf("Workflow.Edges do not form trigger -> input -> AI: %#v", definition.Workflow.Edges)
			}

			if tt.operation == aiProviderSampleOperationStructured {
				var fields []typedfield.Field
				if err := json.Unmarshal([]byte(ai.Config["outputFields"]), &fields); err != nil {
					t.Fatalf("unmarshal outputFields: %v", err)
				}
				if !reflect.DeepEqual(fields, tt.wantAttributes) {
					t.Errorf("outputFields = %#v, want %#v", fields, tt.wantAttributes)
				}
				if !strings.Contains(ai.Config["outputFields"], `"Key":"item"`) || strings.Contains(ai.Config["outputFields"], `"key":`) {
					t.Errorf("outputFields did not use typedfield.Field's uppercase wire names: %s", ai.Config["outputFields"])
				}
			}
		})
	}
}

func TestBuildAIProviderSampleDefinitionRejectsUnknownOperation(t *testing.T) {
	if _, err := buildAIProviderSampleDefinition("embedding", "provider-1", "Provider", "destination", "model"); err == nil {
		t.Fatal("buildAIProviderSampleDefinition accepted an unsupported operation")
	}
}

func TestMatchesAIProviderSampleRun(t *testing.T) {
	definition, err := buildAIProviderSampleDefinition(
		aiProviderSampleOperationStructured,
		"provider-1",
		"Provider",
		"https://gateway.example/v1",
		"model",
	)
	if err != nil {
		t.Fatalf("buildAIProviderSampleDefinition: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*composition.Workflow)
		payload string
		values  map[string]string
		want    bool
	}{
		{name: "canonical", want: true},
		{
			name: "copy layout labels and incidental identities are ignored",
			mutate: func(workflow *composition.Workflow) {
				workflow.ID = "persisted-workflow-id"
				workflow.Label = "User label"
				workflow.Description = "User description"
				workflow.Nodes[0].ID = "trigger"
				workflow.Nodes[0].Position = composition.Position{X: 800, Y: -40}
				workflow.Nodes[1].ID = "input"
				workflow.Nodes[1].Position = composition.Position{X: 10, Y: 20}
				workflow.Nodes[2].ID = "ai"
				workflow.Nodes[2].Position = composition.Position{X: -5, Y: 900}
				workflow.Edges[0] = composition.Edge{ID: "edge-a", Source: "trigger", Target: "input"}
				workflow.Edges[1] = composition.Edge{ID: "edge-b", Source: "input", Target: "ai"}
				workflow.Attributes[0].Label = "Renamed item"
				workflow.Attributes[0].Description = "User-facing field help"
				fields := decodeAIProviderSampleOutputFields(t, workflow.Nodes[2].Config["outputFields"])
				fields[0].Label = "Renamed item"
				fields[0].Description = "User-facing field help"
				workflow.Nodes[2].Config["outputFields"] = encodeIndentedAIProviderSampleOutputFields(t, fields)
			},
			want: true,
		},
		{
			name: "equivalent output fields JSON key order and formatting are ignored",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[2].Config["outputFields"] = reencodeAIProviderSampleOutputFieldsAsMaps(t, workflow.Nodes[2].Config["outputFields"])
			},
			want: true,
		},
		{
			name: "node storage order is ignored",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[0], workflow.Nodes[2] = workflow.Nodes[2], workflow.Nodes[0]
				workflow.Edges[0], workflow.Edges[1] = workflow.Edges[1], workflow.Edges[0]
			},
			want: true,
		},
		{
			name: "synthetic input change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[1].Config["text"] = "different fixture"
			},
		},
		{
			name: "AI prompt change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[2].Config["prompt"] = "Different prompt"
			},
		},
		{
			name: "provider change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[2].Config["aiproviderId"] = "provider-2"
			},
		},
		{
			name: "extra config is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[2].Config["unrecognized"] = "value"
			},
		},
		{
			name: "node type change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Nodes[2].NodeTypeID = "process-ai-classify"
			},
		},
		{
			name: "edge topology change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Edges[1].Target = workflow.Nodes[0].ID
			},
		},
		{
			name: "attribute type change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Attributes[1].Type = typedfield.TypeNumber
			},
		},
		{
			name: "output field type change is modified",
			mutate: func(workflow *composition.Workflow) {
				fields := decodeAIProviderSampleOutputFields(t, workflow.Nodes[2].Config["outputFields"])
				fields[1].Type = typedfield.TypeNumber
				workflow.Nodes[2].Config["outputFields"] = encodeIndentedAIProviderSampleOutputFields(t, fields)
			},
		},
		{
			name: "attribute default change is modified",
			mutate: func(workflow *composition.Workflow) {
				workflow.Attributes[1].Default = "4"
			},
		},
		{
			name:    "user payload cannot certify",
			payload: "override",
		},
		{
			name:   "user values cannot certify",
			values: map[string]string{"quantity": "3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflow := cloneAIProviderSampleWorkflow(t, definition.Workflow)
			if tt.mutate != nil {
				tt.mutate(&workflow)
			}
			if got := matchesAIProviderSampleRun(definition, workflow, tt.payload, tt.values); got != tt.want {
				t.Errorf("matchesAIProviderSampleRun = %v, want %v", got, tt.want)
			}
		})
	}
}

func cloneAIProviderSampleWorkflow(t *testing.T, workflow composition.Workflow) composition.Workflow {
	t.Helper()
	raw, err := json.Marshal(workflow)
	if err != nil {
		t.Fatalf("marshal workflow clone: %v", err)
	}
	var clone composition.Workflow
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("unmarshal workflow clone: %v", err)
	}
	return clone
}

func decodeAIProviderSampleOutputFields(t *testing.T, raw string) []typedfield.Field {
	t.Helper()
	var fields []typedfield.Field
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("unmarshal output fields: %v", err)
	}
	return fields
}

func encodeIndentedAIProviderSampleOutputFields(t *testing.T, fields []typedfield.Field) string {
	t.Helper()
	raw, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatalf("marshal output fields: %v", err)
	}
	return string(raw)
}

func reencodeAIProviderSampleOutputFieldsAsMaps(t *testing.T, raw string) string {
	t.Helper()
	var fields []map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("unmarshal output field maps: %v", err)
	}
	reencoded, err := json.MarshalIndent(fields, "", "    ")
	if err != nil {
		t.Fatalf("marshal output field maps: %v", err)
	}
	return string(reencoded)
}
