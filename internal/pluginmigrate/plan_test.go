package pluginmigrate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alicoding/mill/internal/services/pluginsvc"
	"github.com/tailscale/hujson"
)

const testMillVersion = "1.0.0"

func TestPrepareApplyAndSecondPlanOnHistoricalSettingsManifest(t *testing.T) {
	dir := copyRealPlugin(t, "mill-bookmark")
	before := readManifest(t, dir)
	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.Plan.FormatVersion != PlanFormatVersion || prepared.Plan.PluginID != "mill-bookmark" ||
		!slices.Equal(prepared.Plan.Migrations, []string{ConfigurationKeyMigrationID}) || !prepared.Plan.HasPatch() {
		t.Fatalf("plan = %+v", prepared.Plan)
	}
	assertConfigurationPatch(t, prepared.Plan.Patch)
	if afterPreview := readManifest(t, dir); !bytes.Equal(afterPreview, before) {
		t.Fatal("preview changed manifest.json")
	}

	if err := prepared.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after := assertMigratedManifest(t, dir, before, prepared.Plan)
	assertSecondPlanIsEmpty(t, dir, after)
}

func assertMigratedManifest(t *testing.T, dir string, before []byte, plan Plan) []byte {
	t.Helper()
	if !plan.Applied {
		t.Fatal("applied plan did not record success")
	}
	after := readManifest(t, dir)
	if bytes.Equal(after, before) {
		t.Fatal("apply left the deprecated key unchanged")
	}
	var manifest map[string]any
	if err := json.Unmarshal(after, &manifest); err != nil {
		t.Fatalf("applied manifest is not strict JSON: %v", err)
	}
	contributes := manifest["contributes"].(map[string]any)
	if _, ok := contributes["settings"]; ok {
		t.Error("deprecated contributes.settings remains")
	}
	if _, ok := contributes["configuration"]; !ok {
		t.Error("contributes.configuration is absent")
	}
	if problems := pluginsvc.ConformDir(dir, ""); len(problems) > 0 {
		t.Fatalf("migrated real plugin does not conform: %v", problems)
	}
	return after
}

func assertSecondPlanIsEmpty(t *testing.T, dir string, after []byte) {
	t.Helper()
	second, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if second.Plan.HasPatch() || len(second.Plan.Migrations) != 0 {
		t.Fatalf("second plan = %+v, want current", second.Plan)
	}
	if err := second.Apply(); err != nil {
		t.Fatalf("empty second Apply: %v", err)
	}
	if final := readManifest(t, dir); !bytes.Equal(final, after) {
		t.Fatal("empty second apply changed manifest.json")
	}
}

func TestPrepareReportsCurrentRealPluginManifests(t *testing.T) {
	for _, id := range []string{"mill-textcase", "mill-scribble"} {
		t.Run(id, func(t *testing.T) {
			dir := copyRealPlugin(t, id)
			prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if prepared.Plan.HasPatch() || len(prepared.Plan.Manual) != 0 {
				t.Fatalf("plan = %+v, want current", prepared.Plan)
			}
		})
	}
}

func TestPrepareReportsBothKeysAsManualAndDoesNotWrite(t *testing.T) {
	dir := writePlugin(t, `{
  "id": "both-keys",
  "contributes": {
    "settings": [],
    "configuration": []
  }
}`)
	before := readManifest(t, dir)
	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(prepared.Plan.Manual) != 1 || prepared.Plan.Manual[0].ID != ConfigurationKeyMigrationID || prepared.Plan.HasPatch() {
		t.Fatalf("plan = %+v", prepared.Plan)
	}
	if err := prepared.Apply(); err == nil {
		t.Fatal("Apply succeeded despite a manual decision")
	}
	if after := readManifest(t, dir); !bytes.Equal(after, before) {
		t.Fatal("manual decision changed manifest.json")
	}
}

func TestManualDecisionSuppressesOtherwiseSafeOperations(t *testing.T) {
	dir := writePlugin(t, `{
  "id": "mixed-plan",
  "contributes": {
    "settings": [],
    "commands": [
      {"id": "refresh", "label": "Refresh"},
      {"id": "mixed-plan.refresh", "label": "Refresh canonical"}
    ]
  }
}`)
	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !slices.Equal(prepared.Plan.Migrations, []string{ConfigurationKeyMigrationID, CommandNamespaceMigrationID}) ||
		len(prepared.Plan.Manual) == 0 || prepared.Plan.HasPatch() {
		t.Fatalf("plan = %+v", prepared.Plan)
	}
}

func TestPrepareRefusesMalformedJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "malformed")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"id":"malformed",`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion); err == nil {
		t.Fatal("Prepare accepted malformed JSON")
	}
}

func TestPrepareRejectsHuJSONExtensionsLikeTheLoader(t *testing.T) {
	tests := map[string]func([]byte) []byte{
		"comment": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"id":`), []byte("// extension syntax\n  \"id\":"), 1)
		},
		"trailing comma": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("\n}"), []byte(",\n}"), 1)
		},
	}
	for name, extend := range tests {
		t.Run(name, func(t *testing.T) {
			dir := copyCommandFixture(t)
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, extend(readManifest(t, dir)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion); err == nil {
				t.Fatal("Prepare accepted syntax rejected by the plugin loader")
			}
			if problems := pluginsvc.ConformDir(dir, testMillVersion); !slices.Contains(problems, "manifest.json is not valid JSON") {
				t.Fatalf("ConformDir problems = %v", problems)
			}
		})
	}
}

func TestPrepareRefusesInstalledLocationsAndReceipts(t *testing.T) {
	t.Run("installed directory descendant", func(t *testing.T) {
		installed := t.TempDir()
		dir := copyRealPluginInto(t, "mill-bookmark", installed)
		if _, err := Prepare(dir, installed, testMillVersion); err == nil || !strings.Contains(err.Error(), "installed plugin directory") {
			t.Fatalf("Prepare error = %v", err)
		}
	})
	t.Run("receipt", func(t *testing.T) {
		dir := copyRealPlugin(t, "mill-bookmark")
		if err := os.WriteFile(filepath.Join(dir, pluginsvc.InstallRecordFile), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion); err == nil || !strings.Contains(err.Error(), "receipt") {
			t.Fatalf("Prepare error = %v", err)
		}
	})
}

func TestFailedProspectiveValidationLeavesSourceUnchanged(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "wrong-folder")
	copyPlugin(t, "mill-bookmark", dir)
	before := readManifest(t, dir)
	if _, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion); err == nil || !strings.Contains(err.Error(), "does not conform") {
		t.Fatalf("Prepare error = %v", err)
	}
	if after := readManifest(t, dir); !bytes.Equal(after, before) {
		t.Fatal("failed validation changed manifest.json")
	}
}

func TestHuJSONPatchPreservesOwnedCommentsAndUnrelatedLayout(t *testing.T) {
	raw := []byte("{\n\t\"id\": \"demo\",\n\t\"contributes\": {\n\t\t// setting docs\n\t\t\"settings\": [\n\t\t\t{\"key\": \"demo.mode\"}\n\t\t], // setting tail\n\t\t\"commands\": []\n\t},\n\t\"description\": \"unchanged\"\n}\n")
	value, err := hujson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	settings, present, canonical := configurationMembers(value)
	if !present || canonical {
		t.Fatal("settings member was not found")
	}
	patch := patchDocument(configurationOperations(settings))
	got, err := patched(value, patch)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"// setting docs", "// setting tail", "\t\"commands\": []", "\t\"description\": \"unchanged\""} {
		if !bytes.Contains(got, []byte(preserved)) {
			t.Errorf("patched manifest lost %q:\n%s", preserved, got)
		}
	}
	if !bytes.Contains(got, []byte("\"configuration\"")) || bytes.Contains(got, []byte("\"settings\"")) {
		t.Errorf("patch did not move the key:\n%s", got)
	}
}

func TestApplyRefusesAChangedSource(t *testing.T) {
	dir := copyRealPlugin(t, "mill-bookmark")
	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(readManifest(t, dir), '\n')
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Apply(); err == nil || !strings.Contains(err.Error(), "changed after preview") {
		t.Fatalf("Apply error = %v", err)
	}
	if got := readManifest(t, dir); !bytes.Equal(got, changed) {
		t.Fatal("refused apply overwrote the changed source")
	}
}

func assertConfigurationPatch(t *testing.T, patch []byte) {
	t.Helper()
	var operations []map[string]any
	if err := json.Unmarshal(patch, &operations); err != nil {
		t.Fatalf("patch is not JSON: %v\n%s", err, patch)
	}
	if len(operations) != 2 || operations[0]["op"] != "test" || operations[1]["op"] != "move" {
		t.Fatalf("operations = %#v", operations)
	}
	if operations[0]["path"] != "/contributes/settings" || operations[1]["from"] != "/contributes/settings" || operations[1]["path"] != "/contributes/configuration" {
		t.Fatalf("operations = %#v", operations)
	}
}

func copyRealPlugin(t *testing.T, id string) string {
	t.Helper()
	return copyRealPluginInto(t, id, t.TempDir())
}

func copyRealPluginInto(t *testing.T, id, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, id)
	copyPlugin(t, id, dir)
	return dir
}

func copyPlugin(t *testing.T, id, dir string) {
	t.Helper()
	source := filepath.Join("..", "..", "examples", "plugins", id)
	if err := pluginsvc.CopyPluginFolder(source, dir); err != nil {
		t.Fatalf("copy real plugin %s: %v", id, err)
	}
}

func writePlugin(t *testing.T, manifest string) string {
	t.Helper()
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(manifest), &decoded); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), decoded.ID)
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readManifest(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")) // #nosec G304 -- test helper reads its own temporary plugin
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
