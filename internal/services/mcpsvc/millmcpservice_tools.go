package mcpsvc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alicoding/mill/internal/contract"
	"github.com/alicoding/mill/internal/services/dataevent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP Tools over the same export/import model the UI's own buttons use
// (compositionservice_export.go, configureservice_export.go) --
// docs/adr/0017's Update: the read side (export_*) is the Resources'
// data reshaped as callable tools (many hosts reach for tools far more
// readily than resources); the write side (import_*) is gated by a
// single, coarse, default-off Settings toggle (ADR-0017 Option B's
// authoring-capability gate) plus, when enabled, docs/adr/0032's
// park-and-poll per-write approval (gateWrite, millmcpservice_approval.go).
//
// Secrets are structurally absent from everything these tools can
// touch: exports never carry one (the wire shapes have no secret
// field), and imports create entities whose secret is simply not set
// yet -- same guarantees the UI's own Export/Import already has.

// MCPWriteEnabledKey stores the gate as the string "true"/"false" --
// matching every other settings key's string convention
// (settingsservice.go).
const MCPWriteEnabledKey = "mcp-write-tools-enabled" //nolint:gosec // a settings-store key name, not a credential (G101 false positive)

func (m *MillMCPService) writeEnabled() bool {
	v, ok := m.store.Get(MCPWriteEnabledKey).(string)
	return ok && v == "true"
}

func (m *MillMCPService) requireWriteEnabled() error {
	if !m.writeEnabled() {
		// Agent-facing copy, same bar as any user copy: never an
		// internal citation. Writes are default-off by design
		// (docs/adr/0017 -- the citation lives here, not in the string).
		return fmt.Errorf("MCP write tools are disabled on this Mill instance -- a human must enable " +
			"\"Allow MCP clients to change content\" in Mill's Settings page first; writes are off by default")
	}
	return nil
}

type exportToolArgs struct {
	ID string `json:"id" jsonschema:"the entity's ID (list the matching mill:// index resource, or the matching export tool's index, to discover IDs)"`
}

type importToolArgs struct {
	JSON string `json:"json" jsonschema:"the entity definition as JSON -- the exact shape the matching export tool (or the UI's Export button) produces"`
}

type importToolResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// jsonText marshals v as indented JSON text -- the shared building
// block behind both jsonResult (millmcpservice_authoring.go) and every
// gated-write executor below, whose return value is a plain string (an
// executor is dispatched by name at approval time, long after any
// *mcp.CallToolResult wrapping would matter).
func jsonText(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// marshalArgs is the shared helper every gated-write tool handler below
// uses to turn its already-typed, already-parsed args back into the
// argsJSON string gateWrite persists -- reusing the same struct for
// both the MCP call's own unmarshal and this re-marshal keeps the
// executor's later json.Unmarshal trivially symmetric.
func marshalArgs(in any) (string, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// contentContractRegisterers maps a contract.ContentContract's Kind to
// the method that actually wires its tools -- goal 0388's RULE made
// mechanical: every file-backed/entity-backed kind is one row in
// contract.ContentContracts (the data both this loop and the served
// mill://contract document read) and one entry here, registered
// through this SAME loop rather than a bespoke call sprinkled through
// registerAtlasTools/registerTools. The diagram tools migrated onto
// this loop with no behavior change -- registerAtlasDiagramTools is
// unchanged, only who calls it moved.
func (m *MillMCPService) contentContractRegisterers() map[string]func() {
	return map[string]func(){
		"diagram": m.registerAtlasDiagramTools,
		"sheet":   m.registerAtlasSheetTools,
		"xlsx":    m.registerAtlasXlsxTools,
		"list":    m.registerListContentTools,
	}
}

// registerContentContracts wires every kind's content contract listed
// in contract.ContentContracts -- called once from registerTools.
func (m *MillMCPService) registerContentContracts() {
	registerers := m.contentContractRegisterers()
	for _, c := range contract.ContentContracts {
		if register, ok := registerers[c.Kind]; ok {
			register()
		}
	}
}

// registerTools wires the export/import tool set. Export tools are
// read-only and ungated; import tools all pass through
// requireWriteEnabled + gateWrite.
func (m *MillMCPService) registerTools() {
	m.registerAuthoringTools()
	m.registerDebugTools()
	m.registerAtlasTools()
	m.registerPluginTools()
	m.registerSecretsTools()
	m.registerAIProviderAvailabilityTools()
	m.registerAIProviderTools()
	m.registerAIProviderSampleTools(m.comp)
	m.registerContentContracts()

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "export_workflow",
		Description: "Export one workflow's full definition as JSON (same shape as the UI's Export button). Read-only.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in workflowIDArgs) (*mcp.CallToolResult, any, error) {
		id, err := in.resolve()
		if err != nil {
			return nil, nil, err
		}
		data, err := m.comp.ExportWorkflow(id)
		if err != nil {
			return nil, nil, err
		}
		return textResult(data), nil, nil
	})

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "export_request",
		Description: "Export one HTTPRequest's full definition as JSON -- never includes a secret. Read-only.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in exportToolArgs) (*mcp.CallToolResult, any, error) {
		data, err := m.cfg.ExportHTTPRequest(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return textResult(data), nil, nil
	})

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "export_list",
		Description: "Export one List's full definition (label + entries) as JSON. Read-only.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in exportToolArgs) (*mcp.CallToolResult, any, error) {
		data, err := m.cfg.ExportList(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return textResult(data), nil, nil
	})

	mcp.AddTool(m.server, &mcp.Tool{
		Name:        "export_mcpserver",
		Description: "Export one configured MCP Server's definition as JSON. Read-only.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in exportToolArgs) (*mcp.CallToolResult, any, error) {
		data, err := m.cfg.ExportMCPServer(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return textResult(data), nil, nil
	})

	m.registerWriteExecutor("import_workflow", func(argsJSON string) (string, error) {
		var in importToolArgs
		if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
			return "", err
		}
		wf, err := m.comp.ImportWorkflow(in.JSON)
		if err != nil {
			return "", err
		}
		// ImportWorkflow delegates to CreateWorkflow (compositionsvc),
		// which already emits "workflow" (goal 0017) -- no manual emit
		// needed here.
		return jsonText(importToolResult{ID: wf.ID, Label: wf.Label})
	})
	mcp.AddTool(m.server, &mcp.Tool{
		Name: "import_workflow",
		Description: "Create a new workflow from an exported-workflow JSON definition. Always mints a new " +
			"workflow ID, never overwrites an existing one. Requires the human-set 'Allow MCP clients to " +
			"import data' toggle in Mill's Settings (default off); when per-write approval is also required " +
			"(the default), the call may return a 'parked pending human approval' result -- poll " +
			"check_write_status with the returned id.",
		Annotations: createAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in importToolArgs) (*mcp.CallToolResult, any, error) {
		if err := m.requireWriteEnabled(); err != nil {
			return nil, nil, err
		}
		argsJSON, err := marshalArgs(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.gateWrite("import_workflow", "An MCP client wants to import a workflow", argsJSON)
		return res, nil, err
	})

	m.registerWriteExecutor("import_request", func(argsJSON string) (string, error) {
		var in importToolArgs
		if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
			return "", err
		}
		r, err := m.cfg.ImportHTTPRequest(in.JSON)
		if err != nil {
			return "", err
		}
		// ImportHTTPRequest delegates to CreateHTTPRequest (configuresvc),
		// which already emits "request" (goal 0017).
		return jsonText(importToolResult{ID: r.ID, Label: r.Label})
	})
	mcp.AddTool(m.server, &mcp.Tool{
		Name: "import_request",
		Description: "Create a new HTTPRequest from an exported-request JSON definition. The imported request " +
			"starts with no secret set (a human sets secrets in Mill's UI only). Requires the human-set " +
			"'Allow MCP clients to change content' toggle in Mill's Settings (default off); may park pending " +
			"approval -- see import_workflow's description for the poll contract.",
		Annotations: createAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in importToolArgs) (*mcp.CallToolResult, any, error) {
		if err := m.requireWriteEnabled(); err != nil {
			return nil, nil, err
		}
		argsJSON, err := marshalArgs(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.gateWrite("import_request", "An MCP client wants to import an HTTP request (integration)", argsJSON)
		return res, nil, err
	})

	m.registerWriteExecutor("import_list", func(argsJSON string) (string, error) {
		var in importToolArgs
		if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
			return "", err
		}
		l, err := m.cfg.ImportList(in.JSON)
		if err != nil {
			return "", err
		}
		// Kept explicit (unlike the other import_* tools above): ImportList
		// does a SECOND mutation after CreateList (attaching rows), which
		// CreateList's own internal emit can't see -- this is the one that
		// actually reflects rows being present.
		dataevent.Emit("list", l.ID)
		return jsonText(importToolResult{ID: l.ID, Label: l.Label})
	})
	mcp.AddTool(m.server, &mcp.Tool{
		Name: "import_list",
		Description: "Create a new List from an exported-list JSON definition. Requires the human-set " +
			"'Allow MCP clients to change content' toggle in Mill's Settings (default off); may park pending " +
			"approval -- see import_workflow's description for the poll contract.",
		Annotations: createAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in importToolArgs) (*mcp.CallToolResult, any, error) {
		if err := m.requireWriteEnabled(); err != nil {
			return nil, nil, err
		}
		argsJSON, err := marshalArgs(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.gateWrite("import_list", "An MCP client wants to import a List", argsJSON)
		return res, nil, err
	})

	m.registerWriteExecutor("import_mcpserver", func(argsJSON string) (string, error) {
		var in importToolArgs
		if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
			return "", err
		}
		s, err := m.cfg.ImportMCPServer(in.JSON)
		if err != nil {
			return "", err
		}
		// ImportMCPServer delegates to CreateMCPServer (configuresvc),
		// which already emits "mcpserver" (goal 0017).
		return jsonText(importToolResult{ID: s.ID, Label: s.Label})
	})
	mcp.AddTool(m.server, &mcp.Tool{
		Name: "import_mcpserver",
		Description: "Create a new configured MCP Server from an exported-mcpserver JSON definition. Requires " +
			"the human-set 'Allow MCP clients to change content' toggle in Mill's Settings (default off); may park " +
			"pending approval -- see import_workflow's description for the poll contract.",
		Annotations: createAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in importToolArgs) (*mcp.CallToolResult, any, error) {
		if err := m.requireWriteEnabled(); err != nil {
			return nil, nil, err
		}
		argsJSON, err := marshalArgs(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.gateWrite("import_mcpserver", "An MCP client wants to import an MCP Server config", argsJSON)
		return res, nil, err
	})

	mcp.AddTool(m.server, &mcp.Tool{
		Name: "check_write_status",
		Description: "Poll a parked write's outcome by id (returned by a gated write tool's 'parked pending " +
			"human approval' response). status is pending, approved (result carries the write's own success " +
			"text), denied, or expired (no human decision within 24h). Outcomes stay queryable for 24h after " +
			"resolution, then are swept.",
		Annotations: readOnlyAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in checkWriteStatusArgs) (*mcp.CallToolResult, any, error) {
		res, ok := m.writeStatus(in.ID)
		if !ok {
			return nil, nil, fmt.Errorf("no MCP write with id %s (it may have already been swept, 24h after resolution)", in.ID)
		}
		text, err := jsonText(res)
		if err != nil {
			return nil, nil, err
		}
		return textResult(text), nil, nil
	})

	mcp.AddTool(m.server, &mcp.Tool{
		Name: "cancel_write",
		Description: "Withdraw your OWN still-pending gated write by id (the id from a 'parked pending human " +
			"approval' response). Cancelled is a distinct outcome from denied -- use this when the write no " +
			"longer matters, not when you expect a human to say no. Ungated: cancelling never needs approval, " +
			"it only ever reduces pending work. Errors if the write was already resolved or doesn't exist.",
		Annotations: editAnnotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in checkWriteStatusArgs) (*mcp.CallToolResult, any, error) {
		if err := m.CancelMCPWrite(in.ID); err != nil {
			return nil, nil, err
		}
		return textResult(fmt.Sprintf("cancelled write %s -- nothing was written", in.ID)), nil, nil
	})
}
