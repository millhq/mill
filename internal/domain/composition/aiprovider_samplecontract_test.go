package composition

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestAIProviderSampleOutputContractDigestUsesExistingSchemas(t *testing.T) {
	structured := Node{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{
		"outputFields": `[{"Key":"item","Label":"Item","Type":"text"},{"Key":"quantity","Label":"Quantity","Type":"integer"},{"Key":"inStock","Label":"In stock","Type":"boolean"}]`,
	}}
	fields, err := parseAIExtractFields(structured.Config["outputFields"])
	if err != nil {
		t.Fatalf("parseAIExtractFields: %v", err)
	}
	schema, err := buildExtractSchema(fields)
	if err != nil {
		t.Fatalf("buildExtractSchema: %v", err)
	}
	want := sha256Hex(schema)
	got, err := AIProviderSampleOutputContractDigest(structured)
	if err != nil {
		t.Fatalf("AIProviderSampleOutputContractDigest(structured): %v", err)
	}
	if got != want {
		t.Errorf("structured digest = %q, want real request schema digest %q", got, want)
	}

	classification := Node{NodeTypeID: "process-ai-classify", Config: map[string]string{"categories": "urgent\nnormal"}}
	want = sha256Hex(buildClassifySchema([]string{"urgent", "normal"}))
	got, err = AIProviderSampleOutputContractDigest(classification)
	if err != nil {
		t.Fatalf("AIProviderSampleOutputContractDigest(classification): %v", err)
	}
	if got != want {
		t.Errorf("classification digest = %q, want real request schema digest %q", got, want)
	}
}

func TestAIProviderSampleOutputContractDigestChangesWithContract(t *testing.T) {
	structuredA := Node{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{
		"outputFields": `[{"Key":"quantity","Type":"integer"}]`,
	}}
	structuredB := Node{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{
		"outputFields": `[{"Key":"quantity","Type":"number"}]`,
	}}
	assertDifferentAIProviderSampleDigests(t, structuredA, structuredB)

	classificationA := Node{NodeTypeID: "process-ai-classify", Config: map[string]string{"categories": "urgent\nnormal"}}
	classificationB := Node{NodeTypeID: "process-ai-classify", Config: map[string]string{"categories": "urgent\nnormal\nunknown"}}
	assertDifferentAIProviderSampleDigests(t, classificationA, classificationB)
}

func TestAIProviderTextSampleOutputContractDigestIsVersionedLiteral(t *testing.T) {
	got, err := AIProviderSampleOutputContractDigest(Node{NodeTypeID: "process-ai-completion"})
	if err != nil {
		t.Fatalf("AIProviderSampleOutputContractDigest: %v", err)
	}
	want := sha256Hex([]byte("mill.ai.text-output.v1"))
	if got != want {
		t.Errorf("text digest = %q, want %q", got, want)
	}
}

func TestAIProviderSampleOutputContractDigestRejectsInvalidNode(t *testing.T) {
	for _, node := range []Node{
		{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{"outputFields": "not JSON"}},
		{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{}},
		{NodeTypeID: "process-ai-classify", Config: map[string]string{}},
		{NodeTypeID: "process-http-request"},
	} {
		if _, err := AIProviderSampleOutputContractDigest(node); err == nil {
			t.Errorf("AIProviderSampleOutputContractDigest(%q) returned nil error", node.NodeTypeID)
		}
	}
}

func TestValidateAIProviderSampleOutput(t *testing.T) {
	structured := Node{NodeTypeID: "process-ai-extract-structured", Config: map[string]string{
		"outputFields": `[{"Key":"item","Type":"text"},{"Key":"quantity","Type":"integer"},{"Key":"inStock","Type":"boolean"}]`,
	}}
	classification := Node{NodeTypeID: "process-ai-classify", Config: map[string]string{
		"categories": "urgent\nnormal", "outputAttribute": "category",
	}}
	tests := []struct {
		name   string
		node   Node
		output ExecContext
		valid  bool
	}{
		{name: "text", node: Node{NodeTypeID: "process-ai-completion"}, output: ExecContext{Payload: "summary"}, valid: true},
		{name: "empty text", node: Node{NodeTypeID: "process-ai-completion"}, output: ExecContext{Payload: "  "}},
		{name: "structured", node: structured, output: ExecContext{Attributes: map[string]any{"item": "tea", "quantity": float64(2), "inStock": true, "unrelated": "ignored"}}, valid: true},
		{name: "structured missing", node: structured, output: ExecContext{Attributes: map[string]any{"item": "tea", "quantity": float64(2)}}},
		{name: "structured wrong type", node: structured, output: ExecContext{Attributes: map[string]any{"item": "tea", "quantity": "two", "inStock": true}}},
		{name: "classification", node: classification, output: ExecContext{Attributes: map[string]any{"category": "urgent"}}, valid: true},
		{name: "classification out of enum", node: classification, output: ExecContext{Attributes: map[string]any{"category": "later"}}},
		{name: "classification empty", node: classification, output: ExecContext{Attributes: map[string]any{"category": ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAIProviderSampleOutput(tt.node, tt.output)
			if (err == nil) != tt.valid {
				t.Fatalf("ValidateAIProviderSampleOutput error = %v, valid want %t", err, tt.valid)
			}
		})
	}
}

func assertDifferentAIProviderSampleDigests(t *testing.T, a, b Node) {
	t.Helper()
	aDigest, err := AIProviderSampleOutputContractDigest(a)
	if err != nil {
		t.Fatalf("digest A: %v", err)
	}
	bDigest, err := AIProviderSampleOutputContractDigest(b)
	if err != nil {
		t.Fatalf("digest B: %v", err)
	}
	if aDigest == bDigest {
		t.Fatalf("different contracts shared digest %q", aDigest)
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
