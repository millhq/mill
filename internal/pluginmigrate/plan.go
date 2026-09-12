// Package pluginmigrate plans and applies source-plugin manifest migrations.
package pluginmigrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alicoding/mill/internal/services/pluginsvc"
	"github.com/tailscale/hujson"
)

const (
	PlanFormatVersion           = 1
	ConfigurationKeyMigrationID = "configuration-key"
	CommandNamespaceMigrationID = "command-namespace"
)

var noPatch = json.RawMessage("[]")

// ManualDecision names an ambiguity the author must resolve.
type ManualDecision struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

// Plan is the inspectable migration result. Patch is an RFC 6902 document
// consumed directly by HuJSON.
type Plan struct {
	FormatVersion int              `json:"formatVersion"`
	PluginID      string           `json:"pluginId"`
	Migrations    []string         `json:"migrations"`
	Patch         json.RawMessage  `json:"patch"`
	Manual        []ManualDecision `json:"manual"`
	Applied       bool             `json:"applied"`
}

// HasPatch reports whether the plan carries migration operations.
func (p Plan) HasPatch() bool { return !bytes.Equal(p.Patch, noPatch) }

// Prepared holds the checked prospective bytes until the caller explicitly
// applies them.
type Prepared struct {
	Plan       Plan
	root       string
	appVersion string
	original   []byte
	candidate  []byte
}

// Prepare validates the source boundary and builds the ordered migration patch.
func Prepare(sourceDir, installedDir, appVersion string) (*Prepared, error) {
	id, root, raw, value, err := loadSource(sourceDir, installedDir)
	if err != nil {
		return nil, err
	}
	manifest, err := pluginsvc.DecodeManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("decode manifest.json: %w", err)
	}
	plan := Plan{
		FormatVersion: PlanFormatVersion,
		PluginID:      id,
		Migrations:    []string{},
		Patch:         append(json.RawMessage(nil), noPatch...),
		Manual:        []ManualDecision{},
	}
	var operations [][]byte
	settings, hasSettings, hasConfiguration := configurationMembers(value)
	if hasSettings && hasConfiguration {
		plan.Migrations = append(plan.Migrations, ConfigurationKeyMigrationID)
		plan.Manual = append(plan.Manual, ManualDecision{
			ID: ConfigurationKeyMigrationID, Path: "/contributes",
			Summary: "Both contributes.settings and contributes.configuration exist; choose the canonical value.",
		})
	} else if hasSettings {
		plan.Migrations = append(plan.Migrations, ConfigurationKeyMigrationID)
		operations = append(operations, configurationOperations(settings)...)
	}

	commandOperations, commandManual, commandMigration := commandNamespaceOperations(id, manifest)
	if commandMigration {
		plan.Migrations = append(plan.Migrations, CommandNamespaceMigrationID)
	}
	operations = append(operations, commandOperations...)
	plan.Manual = append(plan.Manual, commandManual...)
	if len(plan.Manual) > 0 {
		return &Prepared{Plan: plan, root: root, appVersion: appVersion, original: raw}, nil
	}
	plan.Patch = patchDocument(operations)
	if !plan.HasPatch() {
		if problems := pluginsvc.ConformDirWithManifest(root, raw, appVersion); len(problems) > 0 {
			return nil, fmt.Errorf("plugin does not conform: %s", strings.Join(problems, "; "))
		}
		return &Prepared{Plan: plan, root: root, appVersion: appVersion, original: raw}, nil
	}

	candidate, err := patched(value, plan.Patch)
	if err != nil {
		return nil, err
	}
	if problems := pluginsvc.ConformDirWithManifest(root, candidate, appVersion); len(problems) > 0 {
		return nil, fmt.Errorf("migrated plugin does not conform: %s", strings.Join(problems, "; "))
	}
	return &Prepared{Plan: plan, root: root, appVersion: appVersion, original: raw, candidate: candidate}, nil
}

func loadSource(sourceDir, installedDir string) (string, string, []byte, hujson.Value, error) {
	source, err := canonicalExistingDir(sourceDir)
	if err != nil {
		return "", "", nil, hujson.Value{}, err
	}
	installed, err := canonicalPath(installedDir)
	if err != nil {
		return "", "", nil, hujson.Value{}, fmt.Errorf("resolve installed plugin directory: %w", err)
	}
	if within(installed, source) {
		return "", "", nil, hujson.Value{}, fmt.Errorf("refusing installed plugin directory %q", sourceDir)
	}
	id, root, err := pluginsvc.ManifestIDIn(source)
	if err != nil {
		return "", "", nil, hujson.Value{}, fmt.Errorf("read source plugin: %w", err)
	}
	root, err = canonicalExistingDir(root)
	if err != nil {
		return "", "", nil, hujson.Value{}, err
	}
	if within(installed, root) {
		return "", "", nil, hujson.Value{}, fmt.Errorf("refusing installed plugin directory %q", sourceDir)
	}
	if err := refuseReceipts(source, root); err != nil {
		return "", "", nil, hujson.Value{}, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json")) // #nosec G304 -- root came from the explicit source directory
	if err != nil {
		return "", "", nil, hujson.Value{}, fmt.Errorf("read manifest.json: %w", err)
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return "", "", nil, hujson.Value{}, fmt.Errorf("parse manifest.json: %w", err)
	}
	return id, root, raw, value, nil
}

func refuseReceipts(dirs ...string) error {
	for _, dir := range dirs {
		_, err := os.Stat(filepath.Join(dir, pluginsvc.InstallRecordFile))
		switch {
		case err == nil:
			return fmt.Errorf("refusing installed plugin receipt in %q", dir)
		case !os.IsNotExist(err):
			return fmt.Errorf("inspect plugin receipt in %q: %w", dir, err)
		}
	}
	return nil
}

func configurationMembers(value hujson.Value) (hujson.Value, bool, bool) {
	root, ok := value.Value.(*hujson.Object)
	if !ok {
		return hujson.Value{}, false, false
	}
	contributes, ok := objectMember(root, "contributes")
	if !ok {
		return hujson.Value{}, false, false
	}
	object, ok := contributes.Value.(*hujson.Object)
	if !ok {
		return hujson.Value{}, false, false
	}
	settings, hasSettings := objectMember(object, "settings")
	_, hasConfiguration := objectMember(object, "configuration")
	return settings, hasSettings, hasConfiguration
}

func objectMember(object *hujson.Object, name string) (hujson.Value, bool) {
	for _, member := range object.Members {
		literal, ok := member.Name.Value.(hujson.Literal)
		if ok && literal.String() == name {
			return member.Value, true
		}
	}
	return hujson.Value{}, false
}

func configurationOperations(settings hujson.Value) [][]byte {
	return [][]byte{
		bytes.Join([][]byte{
			[]byte(`{"op":"test","path":"/contributes/settings","value":`),
			settings.Pack(),
			[]byte(`}`),
		}, nil),
		[]byte(`{"op":"move","from":"/contributes/settings","path":"/contributes/configuration"}`),
	}
}

func patchDocument(operations [][]byte) json.RawMessage {
	if len(operations) == 0 {
		return append(json.RawMessage(nil), noPatch...)
	}
	return json.RawMessage(bytes.Join([][]byte{[]byte("["), bytes.Join(operations, []byte(",")), []byte("]")}, nil))
}

func patched(value hujson.Value, patch []byte) ([]byte, error) {
	candidate := value.Clone()
	if err := candidate.Patch(patch); err != nil {
		return nil, fmt.Errorf("apply migration patch: %w", err)
	}
	return candidate.Pack(), nil
}
