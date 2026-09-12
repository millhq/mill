// Package plugincli routes Mill's plugin authoring commands.
package plugincli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alicoding/mill/internal/pluginmigrate"
	"github.com/alicoding/mill/internal/pluginscaffold"
)

const usage = `Usage:
  mill plugin new <name> [--dir <path>]
  mill plugin migrate <source-plugin-dir> [--apply] [--json]
`

// Run executes the plugin authoring route.
func Run(args []string, pluginsDir, millVersion string, out, errOut io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	if args[0] == "new" {
		return pluginscaffold.Run(args, pluginsDir, millVersion, out, errOut)
	}
	if args[0] != "migrate" {
		_, _ = fmt.Fprint(errOut, usage)
		return 1
	}
	dir, apply, asJSON, problem := parseMigrateArgs(args[1:])
	if problem != "" {
		_, _ = fmt.Fprintf(errOut, "%s\n\n%s", problem, usage)
		return 1
	}
	prepared, err := pluginmigrate.Prepare(dir, pluginsDir, millVersion)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if len(prepared.Plan.Manual) > 0 {
		writePlan(out, prepared.Plan, asJSON)
		return 2
	}
	if apply {
		if err := prepared.Apply(); err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return 1
		}
	}
	writePlan(out, prepared.Plan, asJSON)
	return 0
}

func parseMigrateArgs(args []string) (dir string, apply, asJSON bool, problem string) {
	for _, arg := range args {
		switch arg {
		case "--apply":
			apply = true
		case "--json":
			asJSON = true
		case "":
			return "", false, false, "source plugin directory is empty"
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, false, fmt.Sprintf("unknown option %q", arg)
			}
			if dir != "" {
				return "", false, false, "give exactly one source plugin directory"
			}
			dir = arg
		}
	}
	if dir == "" {
		return "", false, false, "give a source plugin directory"
	}
	return dir, apply, asJSON, ""
}

func writePlan(out io.Writer, plan pluginmigrate.Plan, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(out).Encode(plan)
		return
	}
	_, _ = fmt.Fprintf(out, "Plugin: %s\n", plan.PluginID)
	_, _ = fmt.Fprintf(out, "Plan format: %d\n", plan.FormatVersion)
	if len(plan.Migrations) == 0 && len(plan.Manual) == 0 {
		_, _ = fmt.Fprintln(out, "This plugin is current.")
		return
	}
	if len(plan.Migrations) > 0 {
		_, _ = fmt.Fprintln(out, "Migrations:")
		for _, migration := range plan.Migrations {
			_, _ = fmt.Fprintf(out, "  - %s\n", migration)
		}
	}
	if plan.HasPatch() {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, plan.Patch, "  ", "  "); err == nil {
			_, _ = fmt.Fprintln(out, "RFC 6902 patch:")
			_, _ = fmt.Fprintln(out, formatted.String())
		}
	}
	if len(plan.Manual) > 0 {
		_, _ = fmt.Fprintf(out, "Manual decisions: %d\n", len(plan.Manual))
		for _, decision := range plan.Manual {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", decision.ID, decision.Summary)
		}
		return
	}
	if plan.Applied {
		_, _ = fmt.Fprintln(out, "Applied to manifest.json.")
	} else {
		_, _ = fmt.Fprintln(out, "Run again with --apply to update the source folder.")
	}
}
