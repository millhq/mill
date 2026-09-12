package mcpsvc

import (
	"testing"

	"github.com/alicoding/mill/internal/services/servicetest"
)

// Goal 0388: every Mill-registered MCP tool states its own effect
// class through the SDK's own ToolAnnotations, mechanically derived at
// registration time -- never per-call.

func TestBuiltInTools_EveryToolCarriesAnnotations(t *testing.T) {
	tools, err := BuiltInTools(servicetest.NewFakeStore())
	if err != nil {
		t.Fatalf("BuiltInTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range tools {
		if tool.Annotations == nil {
			t.Errorf("%s: no annotations set", tool.Name)
		}
	}
}

// readOnlyToolNames names every tool this package registers that must
// carry ReadOnlyHint true -- a fixed, explicit list rather than a
// name-prefix guess, so a newly added read tool is a deliberate
// addition here, not a silent pass.
var readOnlyToolNames = map[string]bool{
	"atlas_list_kinds": true, "atlas_search_cards": true, "atlas_read_card": true,
	"atlas_read_board_objects": true, "atlas_read_board_object": true,
	"atlas_list_contents": true, "atlas_read_diagram": true, "atlas_sheet_read_range": true,
	"atlas_xlsx_read_range":  true,
	"atlas_get_write_status": true, "check_write_status": true,
	"list_step_types": true, "list_node_types": true, "list_runs": true, "get_run": true,
	"validate_workflow": true, "list_plugins": true,
	"export_workflow": true, "export_request": true, "export_list": true, "export_mcpserver": true,
	"export_aiprovider": true, "get_aiprovider_change_impact": true, "preview_aiprovider_import": true,
	"secrets_list_references":      true,
	"get_ai_provider_availability": true,
}

func TestBuiltInTools_ReadOnlyToolsAreMarkedReadOnly(t *testing.T) {
	tools, err := BuiltInTools(servicetest.NewFakeStore())
	if err != nil {
		t.Fatalf("BuiltInTools: %v", err)
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		want, checked := readOnlyToolNames[tool.Name]
		if !checked {
			continue
		}
		seen[tool.Name] = true
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != want {
			t.Errorf("%s: readOnlyHint annotation missing or wrong (got %+v)", tool.Name, tool.Annotations)
		}
	}
	for name := range readOnlyToolNames {
		if !seen[name] {
			t.Errorf("%s: expected in the built-in tool inventory, not found", name)
		}
	}
}

// destructiveToolNames names every tool that can remove or wholesale-
// overwrite something that already existed -- every one of these must
// carry DestructiveHint true.
var destructiveToolNames = map[string]bool{
	"atlas_diagram_delete_cells": true, "atlas_diagram_import": true,
	"atlas_propose_card_write": true, "atlas_propose_kind_write": true,
	"update_workflow": true, "delete_workflow": true,
	"run_workflow": true, "run_workflow_stepped": true, "step_run": true, "resume_run": true, "stop_run": true,
	"test_request":            true,
	"start_ai_provider_check": true,
	"import_aiprovider":       true, "apply_aiprovider_import": true,
	"restore_ai_provider_feature_sample": true,
}

func TestBuiltInTools_DestructiveToolsAreMarkedDestructive(t *testing.T) {
	tools, err := BuiltInTools(servicetest.NewFakeStore())
	if err != nil {
		t.Fatalf("BuiltInTools: %v", err)
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if !destructiveToolNames[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Errorf("%s: destructiveHint not true (got %+v)", tool.Name, tool.Annotations)
		}
	}
	for name := range destructiveToolNames {
		if !seen[name] {
			t.Errorf("%s: expected in the built-in tool inventory, not found", name)
		}
	}
}

// idempotentEditToolNames names id/address-addressed edit tools whose
// repeated call (same arguments) must leave the same end state.
var idempotentEditToolNames = map[string]bool{
	"atlas_diagram_edit_cells": true, "atlas_sheet_edit_cells": true, "atlas_xlsx_edit_cells": true,
	"cancel_ai_provider_check":           true,
	"prepare_ai_provider_feature_sample": true, "restore_ai_provider_feature_sample": true,
}

// nonIdempotentAppendToolNames names tools that add a new row/cell
// each call -- calling twice must NOT be idempotent.
var nonIdempotentAppendToolNames = map[string]bool{
	"atlas_diagram_add_cells": true, "list_append_row": true, "atlas_create_board_object": true,
}

func TestBuiltInTools_EditIdempotentAppendIsNot(t *testing.T) {
	tools, err := BuiltInTools(servicetest.NewFakeStore())
	if err != nil {
		t.Fatalf("BuiltInTools: %v", err)
	}
	byName := map[string]*struct{ found, idempotent bool }{}
	for name := range idempotentEditToolNames {
		byName[name] = &struct{ found, idempotent bool }{}
	}
	for name := range nonIdempotentAppendToolNames {
		byName[name] = &struct{ found, idempotent bool }{}
	}
	for _, tool := range tools {
		entry, ok := byName[tool.Name]
		if !ok {
			continue
		}
		entry.found = true
		if tool.Annotations != nil {
			entry.idempotent = tool.Annotations.IdempotentHint
		}
	}
	for name := range idempotentEditToolNames {
		e := byName[name]
		if !e.found {
			t.Errorf("%s: not found in the built-in tool inventory", name)
			continue
		}
		if !e.idempotent {
			t.Errorf("%s: idempotentHint should be true (an edit addressed by a stable id/address)", name)
		}
	}
	for name := range nonIdempotentAppendToolNames {
		e := byName[name]
		if !e.found {
			t.Errorf("%s: not found in the built-in tool inventory", name)
			continue
		}
		if e.idempotent {
			t.Errorf("%s: idempotentHint should be false (each call mints something new)", name)
		}
	}
}
