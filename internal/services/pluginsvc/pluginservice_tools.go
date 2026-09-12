package pluginsvc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The declare-first automation contribution (docs/goals/0324): a
// plugin states, in its manifest, which of the things it already
// built are reachable by an agent -- the converged shape across the
// surveyed extension platforms (a typed input contract attached to an
// existing capability, explicitly opted in, with consent kept as a
// separate layer). Declaring a tool grants nothing: a write-effect
// tool still passes the same write gate and the same per-write
// approval every other MCP write takes, and a plugin the user turned
// off contributes no tools at all.

// CommandContribution declares one palette command the plugin
// registers at activate() time. Declaring it is what lets a tool name
// it; api.registerCommand still works for an undeclared id (the host
// warns once), so this is required only for automation reach.
type CommandContribution struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Enablement is the command's global declarative predicate. Menu
	// item When expressions control only their own seats.
	Enablement string `json:"enablement,omitempty"`
	// Menu seats this command in the native menu bar (goal 0335): the
	// host cross-references this declaration by id when the plugin
	// actually registers the command, since the manifest carries no
	// run() to seat directly. Restricted to the three menus a plugin's
	// own contributions can plausibly belong to -- never "app" or
	// "file", which stay Mill's own.
	Menu *CommandMenuContribution `json:"menu,omitempty"`
}

// CommandMenuContribution is a plugin-contributed command's menu seat.
// Group/Order default to 0 when omitted -- a single-command plugin
// never needs to think about band position.
type CommandMenuContribution struct {
	Path  string `json:"path"`
	Group int    `json:"group"`
	Order int    `json:"order"`
}

// pluginMenuPaths is the menu-bar seats a plugin-contributed command
// may ask for -- Mill's own menus (app, file, window, help.developer)
// never take a third-party item.
var pluginMenuPaths = map[string]bool{"workflow": true, "atlas": true, "help": true}

// ToolRun says WHAT a tool runs. Kind "command" runs a declared
// palette command in the webview (argument-less: Command.run takes
// none); "step" runs one of the plugin's own declared workflow steps;
// "query" reads the board's contents through the host's own content
// index.
type ToolRun struct {
	Kind      string `json:"kind"`
	CommandID string `json:"commandId,omitempty"`
	StepID    string `json:"stepId,omitempty"`
}

// ToolContribution declares one agent-reachable tool. InputSchema is
// the tool's own JSON Schema, carried verbatim to the MCP tool list --
// the plugin author writes the contract the agent reads. Effect is the
// consent layer's input, not the schema's: "write" routes through the
// write gate and the approval park, "read" answers directly.
type ToolContribution struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Effect      string          `json:"effect"`
	Run         ToolRun         `json:"run"`
}

// toolNamePattern pins a tool name to verb_noun in lowercase: the name
// is concatenated into the MCP tool id an agent types, so it must
// survive that join without ambiguity, and the verb_noun shape is what
// every surveyed tool catalog converged on.
var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)

// maxToolDescriptionLen keeps a tool list readable: an agent reads
// every description before choosing, so a paragraph here costs every
// call.
const maxToolDescriptionLen = 200

// queryToolArguments are the only arguments a query-kind tool may
// declare -- the content index's own two filters.
var queryToolArguments = map[string]bool{"kind": true, "parentId": true}

// ToolPayloadArgument is the argument name a step-kind tool must
// declare: it carries the step's input payload. Every OTHER argument
// of a step-kind tool names one of that step's declared config fields.
const ToolPayloadArgument = "text"

// toolSchema is the part of a declared tool's JSON Schema this
// validation reads. The schema itself is passed through untouched.
type toolSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// commandVerbPattern is a command id's part after its plugin's own
// "<id>." prefix (standard rule 17): a lowercase-led camelCase verb, so
// the id a tool names is unambiguous about which plugin owns it. The
// LOADER accepts either this namespaced shape or the older bare slug
// (pluginIDPattern) so an existing plugin keeps loading; ConformDir's
// conformCommandNamespace is what actually holds a plugin to the
// namespaced shape the standard requires.
var commandVerbPattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

func validateCommands(pluginID string, commands []CommandContribution) string {
	seen := map[string]bool{}
	for _, c := range commands {
		if !pluginIDPattern.MatchString(c.ID) && !namespacedCommandID(pluginID, c.ID) {
			return fmt.Sprintf("contributed command id %q must be lowercase letters, digits, and hyphens", c.ID)
		}
		if seen[c.ID] {
			return fmt.Sprintf("contributed command %q is declared twice", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Sprintf("contributed command %q needs a label", c.ID)
		}
		if c.Menu != nil && !pluginMenuPaths[c.Menu.Path] {
			return fmt.Sprintf("contributed command %q declares menu path %q, must be \"workflow\", \"atlas\" or \"help\"", c.ID, c.Menu.Path)
		}
	}
	return ""
}

// namespacedCommandID reports whether id is pluginID's own "." prefix
// followed by a lowercase-led camelCase verb -- the standard's shape
// (rule 17), checked strictly at the loader only for an id that would
// otherwise fail the older bare-slug check.
func namespacedCommandID(pluginID, id string) bool {
	verb, hasPrefix := strings.CutPrefix(id, pluginID+".")
	return hasPrefix && commandVerbPattern.MatchString(verb)
}

// IsNamespacedCommandID reports whether id is a canonical command identity
// owned by pluginID. Authoring migrations reuse the loader's grammar through
// this predicate instead of maintaining another command-ID definition.
func IsNamespacedCommandID(pluginID, id string) bool {
	return namespacedCommandID(pluginID, id)
}

func validateTools(c ManifestContributes) string {
	seen := map[string]bool{}
	for _, t := range c.Tools {
		if !toolNamePattern.MatchString(t.Name) {
			return fmt.Sprintf("contributed tool %q must be named verb_noun in lowercase letters, digits and single underscores", t.Name)
		}
		if seen[t.Name] {
			return fmt.Sprintf("contributed tool %q is declared twice", t.Name)
		}
		seen[t.Name] = true
		if problem := validateToolShape(t); problem != "" {
			return problem
		}
		schema, problem := parseToolSchema(t)
		if problem != "" {
			return problem
		}
		if problem := validateToolRun(c, t, schema); problem != "" {
			return problem
		}
	}
	return ""
}

func validateToolShape(t ToolContribution) string {
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Sprintf("contributed tool %q needs a description", t.Name)
	}
	if len(t.Description) > maxToolDescriptionLen {
		return fmt.Sprintf("contributed tool %q needs a description of %d characters or fewer", t.Name, maxToolDescriptionLen)
	}
	if t.Effect != "read" && t.Effect != "write" {
		return fmt.Sprintf("contributed tool %q must declare effect \"read\" or \"write\"", t.Name)
	}
	return ""
}

func parseToolSchema(t ToolContribution) (toolSchema, string) {
	var schema toolSchema
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil || schema.Type != "object" {
		return schema, fmt.Sprintf("contributed tool %q needs an inputSchema that is a JSON Schema object with \"type\": \"object\"", t.Name)
	}
	return schema, ""
}

func validateToolRun(c ManifestContributes, t ToolContribution, schema toolSchema) string {
	switch t.Run.Kind {
	case "command":
		return validateCommandTool(c, t, schema)
	case "step":
		return validateStepTool(c, t, schema)
	case "query":
		for name := range schema.Properties {
			if !queryToolArguments[name] {
				return fmt.Sprintf("contributed tool %q runs a query, so its inputSchema may declare only \"kind\" and \"parentId\"", t.Name)
			}
		}
		return ""
	}
	return fmt.Sprintf("contributed tool %q must declare run.kind \"command\", \"step\" or \"query\"", t.Name)
}

func validateCommandTool(c ManifestContributes, t ToolContribution, schema toolSchema) string {
	declared := false
	for _, cmd := range c.Commands {
		declared = declared || cmd.ID == t.Run.CommandID
	}
	if !declared {
		return fmt.Sprintf("contributed tool %q names command %q, which contributes.commands does not declare", t.Name, t.Run.CommandID)
	}
	// A registered command's run() takes no arguments, so there is
	// nowhere for an argument to go: a tool that needs one runs a step
	// or a query instead.
	if len(schema.Properties) > 0 {
		return fmt.Sprintf("contributed tool %q runs a command, so its inputSchema must declare no properties -- run a step or a query for a tool that takes arguments", t.Name)
	}
	return ""
}

func validateStepTool(c ManifestContributes, t ToolContribution, schema toolSchema) string {
	var step *StepContribution
	for i := range c.Steps {
		if c.Steps[i].ID == t.Run.StepID {
			step = &c.Steps[i]
		}
	}
	if step == nil {
		return fmt.Sprintf("contributed tool %q names step %q, which contributes.steps does not declare", t.Name, t.Run.StepID)
	}
	if _, ok := schema.Properties[ToolPayloadArgument]; !ok {
		return fmt.Sprintf("contributed tool %q runs a step, so its inputSchema must declare a %q property carrying the step's input", t.Name, ToolPayloadArgument)
	}
	configKeys := map[string]bool{}
	for _, f := range step.Config {
		configKeys[f.Key] = true
	}
	for name := range schema.Properties {
		if name == ToolPayloadArgument || configKeys[name] {
			continue
		}
		return fmt.Sprintf("contributed tool %q declares the argument %q, which step %q has no config field for", t.Name, name, step.ID)
	}
	return ""
}
