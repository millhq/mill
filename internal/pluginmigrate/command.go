package pluginmigrate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alicoding/mill/internal/services/pluginsvc"
)

func commandNamespaceOperations(pluginID string, manifest pluginsvc.Manifest) ([][]byte, []ManualDecision, bool) {
	declared := declaredCommandCounts(manifest)
	bare, manual := bareCommandTargets(pluginID, manifest, declared)
	manual = append(manual, ambiguousToolReferences(manifest, bare)...)
	manual = append(manual, ambiguousMenuReferences(manifest, bare)...)
	if len(bare) == 0 && len(manual) == 0 {
		return nil, nil, false
	}
	if len(manual) > 0 {
		return nil, manual, true
	}

	operations := commandDeclarationOperations(manifest, bare)
	operations = append(operations, toolReferenceOperations(manifest, bare)...)
	operations = append(operations, menuReferenceOperations(manifest, bare)...)
	return operations, nil, true
}

func declaredCommandCounts(manifest pluginsvc.Manifest) map[string]int {
	declared := make(map[string]int, len(manifest.Contributes.Commands))
	for _, command := range manifest.Contributes.Commands {
		declared[command.ID]++
	}
	return declared
}

func bareCommandTargets(pluginID string, manifest pluginsvc.Manifest, declared map[string]int) (map[string]string, []ManualDecision) {
	bare := make(map[string]string)
	var manual []ManualDecision
	for i, command := range manifest.Contributes.Commands {
		if strings.Contains(command.ID, ".") {
			continue
		}
		canonical := pluginID + "." + command.ID
		bare[command.ID] = canonical
		if decision := commandDecision(pluginID, command.ID, canonical, i, declared); decision != nil {
			manual = append(manual, *decision)
		}
	}
	return bare, manual
}

func commandDecision(pluginID, bare, canonical string, index int, declared map[string]int) *ManualDecision {
	decision := ManualDecision{ID: CommandNamespaceMigrationID, Path: fmt.Sprintf("/contributes/commands/%d/id", index)}
	switch {
	case !pluginsvc.IsNamespacedCommandID(pluginID, canonical):
		decision.Summary = fmt.Sprintf("Command %q is not a lower-camel verb; choose its canonical name.", bare)
	case declared[bare] > 1:
		decision.Summary = fmt.Sprintf("Command %q is declared more than once; choose one declaration.", bare)
	case declared[canonical] > 0:
		decision.Summary = fmt.Sprintf("Commands %q and %q both exist; choose the canonical declaration.", bare, canonical)
	default:
		return nil
	}
	return &decision
}

func commandDeclarationOperations(manifest pluginsvc.Manifest, bare map[string]string) [][]byte {
	var operations [][]byte
	for i, command := range manifest.Contributes.Commands {
		canonical, ok := bare[command.ID]
		if !ok {
			continue
		}
		operations = append(operations, testReplaceOperations(fmt.Sprintf("/contributes/commands/%d/id", i), command.ID, canonical)...)
	}
	return operations
}

func toolReferenceOperations(manifest pluginsvc.Manifest, bare map[string]string) [][]byte {
	var operations [][]byte
	for i, tool := range manifest.Contributes.Tools {
		if tool.Run.Kind != "command" {
			continue
		}
		canonical, ok := bare[tool.Run.CommandID]
		if !ok {
			continue
		}
		operations = append(operations, testReplaceOperations(fmt.Sprintf("/contributes/tools/%d/run/commandId", i), tool.Run.CommandID, canonical)...)
	}
	return operations
}

func menuReferenceOperations(manifest pluginsvc.Manifest, bare map[string]string) [][]byte {
	var operations [][]byte
	for _, menuID := range sortedMenuIDs(manifest) {
		for i, item := range manifest.Contributes.Menus[menuID] {
			canonical, ok := bare[item.Command]
			if !ok {
				continue
			}
			path := fmt.Sprintf("/contributes/menus/%s/%d/command", jsonPointerToken(menuID), i)
			operations = append(operations, testReplaceOperations(path, item.Command, canonical)...)
		}
	}
	return operations
}

func ambiguousToolReferences(manifest pluginsvc.Manifest, bare map[string]string) []ManualDecision {
	var manual []ManualDecision
	for i, tool := range manifest.Contributes.Tools {
		if tool.Run.Kind != "command" || strings.Contains(tool.Run.CommandID, ".") {
			continue
		}
		if _, ok := bare[tool.Run.CommandID]; ok {
			continue
		}
		manual = append(manual, ManualDecision{
			ID: CommandNamespaceMigrationID, Path: fmt.Sprintf("/contributes/tools/%d/run/commandId", i),
			Summary: fmt.Sprintf("Tool command reference %q has no unique bare declaration.", tool.Run.CommandID),
		})
	}
	return manual
}

func ambiguousMenuReferences(manifest pluginsvc.Manifest, bare map[string]string) []ManualDecision {
	var manual []ManualDecision
	for _, menuID := range sortedMenuIDs(manifest) {
		for i, item := range manifest.Contributes.Menus[menuID] {
			if strings.Contains(item.Command, ".") || bare[item.Command] != "" {
				continue
			}
			manual = append(manual, ManualDecision{
				ID:      CommandNamespaceMigrationID,
				Path:    fmt.Sprintf("/contributes/menus/%s/%d/command", jsonPointerToken(menuID), i),
				Summary: fmt.Sprintf("Menu command reference %q has no unique bare declaration.", item.Command),
			})
		}
	}
	return manual
}

func sortedMenuIDs(manifest pluginsvc.Manifest) []string {
	menuIDs := make([]string, 0, len(manifest.Contributes.Menus))
	for id := range manifest.Contributes.Menus {
		menuIDs = append(menuIDs, id)
	}
	sort.Strings(menuIDs)
	return menuIDs
}

func testReplaceOperations(path, oldValue, newValue string) [][]byte {
	return [][]byte{
		marshalPatchOperation("test", path, oldValue),
		marshalPatchOperation("replace", path, newValue),
	}
}

func marshalPatchOperation(op, path, value string) []byte {
	raw, err := json.Marshal(struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value"`
	}{Op: op, Path: path, Value: value})
	if err != nil {
		panic(fmt.Sprintf("marshal RFC 6902 operation: %v", err))
	}
	return raw
}

func jsonPointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
