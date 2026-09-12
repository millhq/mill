package composition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const aiProviderTextSampleOutputContract = "mill.ai.text-output.v1"

// AIProviderSampleOutputContractDigest hashes the exact output contract used
// by one sample AI node. Structured nodes reuse their existing schema builders
// so this cannot drift into a second schema implementation.
func AIProviderSampleOutputContractDigest(node Node) (string, error) {
	contract, err := aiProviderSampleOutputContract(node)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(contract)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateAIProviderSampleOutput checks a persisted sample step result against
// the same schema bytes used to build its provider request. Unrelated workflow
// attributes are excluded because they are outside the AI node's output
// contract.
func ValidateAIProviderSampleOutput(node Node, output ExecContext) error {
	contract, err := aiProviderSampleOutputContract(node)
	if err != nil {
		return err
	}
	if node.NodeTypeID == "process-ai-completion" {
		return validateAIProviderTextSampleOutput(output)
	}
	instance, err := aiProviderSampleValidationInstance(node, output)
	if err != nil {
		return err
	}
	return validateAIProviderSampleSchema(contract, instance)
}

func validateAIProviderSampleSchema(contract []byte, instance map[string]any) error {
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(contract))
	if err != nil {
		return fmt.Errorf("read AI provider sample output contract: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "mill://schema/ai-provider-sample-output/v1"
	if err := compiler.AddResource(resource, schemaDocument); err != nil {
		return fmt.Errorf("load AI provider sample output contract: %w", err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return fmt.Errorf("compile AI provider sample output contract: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("persisted AI provider sample output: %w", err)
	}
	return nil
}

func validateAIProviderTextSampleOutput(output ExecContext) error {
	if strings.TrimSpace(output.Payload) == "" {
		return fmt.Errorf("process-ai-completion: empty text result")
	}
	return nil
}

func aiProviderSampleValidationInstance(node Node, output ExecContext) (map[string]any, error) {
	switch node.NodeTypeID {
	case "process-ai-extract-structured":
		return aiProviderStructuredSampleInstance(node, output)
	case "process-ai-classify":
		outputAttribute := strings.TrimSpace(node.Config["outputAttribute"])
		value, ok := output.Attributes[outputAttribute]
		if outputAttribute == "" || !ok {
			return nil, fmt.Errorf("process-ai-classify: persisted output is missing its category")
		}
		return map[string]any{"category": value}, nil
	default:
		return nil, fmt.Errorf("node type %q has no AI provider sample output contract", node.NodeTypeID)
	}
}

func aiProviderStructuredSampleInstance(node Node, output ExecContext) (map[string]any, error) {
	fields, err := parseAIExtractFields(node.Config["outputFields"])
	if err != nil {
		return nil, fmt.Errorf("process-ai-extract-structured: %w", err)
	}
	instance := make(map[string]any, len(fields))
	for _, field := range fields {
		value, ok := output.Attributes[field.Key]
		if !ok {
			return nil, fmt.Errorf("process-ai-extract-structured: persisted output is missing %q", field.Key)
		}
		instance[field.Key] = value
	}
	return instance, nil
}

func aiProviderSampleOutputContract(node Node) ([]byte, error) {
	switch node.NodeTypeID {
	case "process-ai-completion":
		return []byte(aiProviderTextSampleOutputContract), nil
	case "process-ai-extract-structured":
		fields, err := parseAIExtractFields(node.Config["outputFields"])
		if err != nil {
			return nil, fmt.Errorf("process-ai-extract-structured: %w", err)
		}
		if len(fields) == 0 {
			return nil, fmt.Errorf("process-ai-extract-structured: no output fields configured")
		}
		contract, err := buildExtractSchema(fields)
		if err != nil {
			return nil, fmt.Errorf("process-ai-extract-structured: build schema: %w", err)
		}
		return contract, nil
	case "process-ai-classify":
		categories := parseAIClassifyCategories(node.Config["categories"])
		if len(categories) == 0 {
			return nil, fmt.Errorf("process-ai-classify: no categories configured")
		}
		return buildClassifySchema(categories), nil
	default:
		return nil, fmt.Errorf("node type %q has no AI provider sample output contract", node.NodeTypeID)
	}
}
