package pluginmigrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alicoding/mill/internal/services/pluginsvc"
)

const (
	pre643ManifestSHA256 = "f149dca9b56944750132f360c0790cca55d9251f35b1c49d81cb7673cc5c2273"
	pre643MainSHA256     = "e08b1f074d35f0c9ac6847b0215ad54eb059ac4c40131271c1693c78a380a8c9"
)

func TestHistoricalCommandMigrationPreservesJavaScriptAndConforms(t *testing.T) {
	dir := copyCommandFixture(t)
	manifestBefore := readManifest(t, dir)
	mainBefore := readFile(t, filepath.Join(dir, "main.js"))
	assertHistoricalFixtureProvenance(t, dir, manifestBefore, mainBefore)

	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !slices.Equal(prepared.Plan.Migrations, []string{CommandNamespaceMigrationID}) {
		t.Fatalf("migrations = %v", prepared.Plan.Migrations)
	}
	assertTestReplacePaths(t, prepared.Plan.Patch, []string{
		"/contributes/commands/0/id",
		"/contributes/tools/0/run/commandId",
	})
	if got := readManifest(t, dir); !bytes.Equal(got, manifestBefore) {
		t.Fatal("preview changed manifest.json")
	}

	if err := prepared.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	manifestAfter := readManifest(t, dir)
	wantManifest := bytes.Replace(manifestBefore, []byte(`"id": "refresh"`), []byte(`"id": "mill-index.refresh"`), 1)
	wantManifest = bytes.Replace(wantManifest, []byte(`"commandId": "refresh"`), []byte(`"commandId": "mill-index.refresh"`), 1)
	if !bytes.Equal(manifestAfter, wantManifest) {
		t.Fatal("migration changed bytes outside the two typed command references")
	}
	var manifest pluginsvc.Manifest
	if err := json.Unmarshal(manifestAfter, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Contributes.Commands[0].ID; got != "mill-index.refresh" {
		t.Fatalf("command id = %q", got)
	}
	if got := manifest.Contributes.Tools[0].Run.CommandID; got != "mill-index.refresh" {
		t.Fatalf("tool command id = %q", got)
	}
	if problems := pluginsvc.ConformDir(dir, testMillVersion); len(problems) > 0 {
		t.Fatalf("migrated historical plugin does not conform: %v", problems)
	}
	if mainAfter := readFile(t, filepath.Join(dir, "main.js")); !bytes.Equal(mainAfter, mainBefore) {
		t.Fatal("migration changed historical main.js")
	}
	assertSecondPlanIsEmpty(t, dir, manifestAfter)
}

func TestOrderedPlanAggregatesConfigurationBeforeCommandNamespace(t *testing.T) {
	dir := copyRealPlugin(t, "mill-bookmark")
	mutateManifest(t, dir, func(document map[string]any) {
		contributes := document["contributes"].(map[string]any)
		contributes["commands"] = []any{map[string]any{"id": "refresh", "label": "Refresh"}}
	})
	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !slices.Equal(prepared.Plan.Migrations, []string{ConfigurationKeyMigrationID, CommandNamespaceMigrationID}) {
		t.Fatalf("migrations = %v", prepared.Plan.Migrations)
	}
	var operations []map[string]any
	if err := json.Unmarshal(prepared.Plan.Patch, &operations); err != nil {
		t.Fatal(err)
	}
	paths := []string{operations[0]["path"].(string), operations[1]["path"].(string), operations[2]["path"].(string), operations[3]["path"].(string)}
	if want := []string{"/contributes/settings", "/contributes/configuration", "/contributes/commands/0/id", "/contributes/commands/0/id"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestCommandMigrationUpdatesEveryTypedReferenceInStableOrder(t *testing.T) {
	dir := copyCommandFixture(t)
	mutateManifest(t, dir, func(document map[string]any) {
		contributes := document["contributes"].(map[string]any)
		contributes["commands"] = []any{
			map[string]any{"id": "refresh", "label": "Refresh"},
			map[string]any{"id": "openItem", "label": "Open item"},
		}
		contributes["tools"] = []any{
			commandTool("open_item", "openItem"),
			commandTool("refresh_index", "refresh"),
		}
		contributes["menus"] = map[string]any{
			"view/title":     []any{map[string]any{"command": "refresh", "when": "true"}},
			"editor/context": []any{map[string]any{"command": "openItem", "when": "true"}},
		}
	})

	prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	assertTestReplacePaths(t, prepared.Plan.Patch, []string{
		"/contributes/commands/0/id",
		"/contributes/commands/1/id",
		"/contributes/tools/0/run/commandId",
		"/contributes/tools/1/run/commandId",
		"/contributes/menus/editor~1context/0/command",
		"/contributes/menus/view~1title/0/command",
	})
	if err := prepared.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if problems := pluginsvc.ConformDir(dir, testMillVersion); len(problems) > 0 {
		t.Fatalf("migrated plugin does not conform: %v", problems)
	}
}

func TestCommandMigrationReportsManualDecisionsWithoutWriting(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mutate func(map[string]any)
	}{
		{
			name: "canonical target collision",
			path: "/contributes/commands/0/id",
			mutate: func(document map[string]any) {
				contributes := document["contributes"].(map[string]any)
				contributes["commands"] = []any{
					map[string]any{"id": "refresh", "label": "Refresh"},
					map[string]any{"id": "mill-index.refresh", "label": "Refresh canonical"},
				}
			},
		},
		{
			name: "bare kebab suffix",
			path: "/contributes/commands/0/id",
			mutate: func(document map[string]any) {
				contributes := document["contributes"].(map[string]any)
				contributes["commands"] = []any{map[string]any{"id": "send-again", "label": "Send again"}}
			},
		},
		{
			name: "ambiguous tool reference",
			path: "/contributes/tools/0/run/commandId",
			mutate: func(document map[string]any) {
				contributes := document["contributes"].(map[string]any)
				contributes["commands"] = []any{map[string]any{"id": "mill-index.refresh", "label": "Refresh"}}
			},
		},
		{
			name: "ambiguous menu reference",
			path: "/contributes/menus/view~1title/0/command",
			mutate: func(document map[string]any) {
				contributes := document["contributes"].(map[string]any)
				contributes["commands"] = []any{map[string]any{"id": "mill-index.refresh", "label": "Refresh"}}
				contributes["tools"] = []any{}
				contributes["menus"] = map[string]any{"view/title": []any{map[string]any{"command": "refresh", "when": "true"}}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copyCommandFixture(t)
			mutateManifest(t, dir, test.mutate)
			before := readManifest(t, dir)
			prepared, err := Prepare(dir, filepath.Join(t.TempDir(), "installed"), testMillVersion)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if !slices.Contains(prepared.Plan.Migrations, CommandNamespaceMigrationID) || len(prepared.Plan.Manual) == 0 || prepared.Plan.HasPatch() {
				t.Fatalf("plan = %+v", prepared.Plan)
			}
			if !slices.ContainsFunc(prepared.Plan.Manual, func(decision ManualDecision) bool { return decision.Path == test.path }) {
				t.Fatalf("manual = %+v, want path %s", prepared.Plan.Manual, test.path)
			}
			if err := prepared.Apply(); err == nil {
				t.Fatal("Apply succeeded despite a manual decision")
			}
			if after := readManifest(t, dir); !bytes.Equal(after, before) {
				t.Fatal("manual decision changed manifest.json")
			}
		})
	}
}

func TestPatchOperationsEncodeQuotedFields(t *testing.T) {
	path := `/contributes/menus/view"quoted\\slash/0/command`
	oldValue := `refresh"old\\value`
	newValue := `mill-index.refresh"new\\value`
	operations := testReplaceOperations(path, oldValue, newValue)
	wantOps := []string{"test", "replace"}
	wantValues := []string{oldValue, newValue}
	for i, raw := range operations {
		var operation struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &operation); err != nil {
			t.Fatalf("operation %d is not JSON: %v", i, err)
		}
		if operation.Op != wantOps[i] || operation.Path != path || operation.Value != wantValues[i] {
			t.Fatalf("operation %d = %+v", i, operation)
		}
	}
}

func assertHistoricalFixtureProvenance(t *testing.T, dir string, manifest, main []byte) {
	t.Helper()
	if got := fmt.Sprintf("%x", sha256.Sum256(main)); got != pre643MainSHA256 {
		t.Fatalf("main.js hash = %s, want pre-#643 %s", got, pre643MainSHA256)
	}
	iconLine := []byte("  \"icon\": \"icon.png\",\n")
	if bytes.Count(manifest, iconLine) != 1 {
		t.Fatal("fixture must contain exactly one documented icon normalization")
	}
	historical := bytes.Replace(manifest, iconLine, nil, 1)
	if got := fmt.Sprintf("%x", sha256.Sum256(historical)); got != pre643ManifestSHA256 {
		t.Fatalf("manifest without icon normalization hash = %s, want pre-#643 %s", got, pre643ManifestSHA256)
	}
	fixtureIcon := readFile(t, filepath.Join(dir, "icon.png"))
	currentIcon := readFile(t, filepath.Join("..", "..", "examples", "plugins", "mill-index", "icon.png"))
	if !bytes.Equal(fixtureIcon, currentIcon) {
		t.Fatal("fixture icon is not the current mill-index identity asset")
	}
}

func assertTestReplacePaths(t *testing.T, patch []byte, want []string) {
	t.Helper()
	var operations []map[string]any
	if err := json.Unmarshal(patch, &operations); err != nil {
		t.Fatalf("decode patch: %v\n%s", err, patch)
	}
	if len(operations) != len(want)*2 {
		t.Fatalf("operations = %d, want %d: %s", len(operations), len(want)*2, patch)
	}
	for i, path := range want {
		test, replace := operations[i*2], operations[i*2+1]
		if test["op"] != "test" || replace["op"] != "replace" || test["path"] != path || replace["path"] != path {
			t.Fatalf("operation pair %d = %#v / %#v", i, test, replace)
		}
	}
}

func copyCommandFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mill-index")
	if err := pluginsvc.CopyPluginFolder(filepath.Join("testdata", "mill-index"), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mutateManifest(t *testing.T, dir string, mutate func(map[string]any)) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readManifest(t, dir), &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func commandTool(name, commandID string) map[string]any {
	return map[string]any{
		"name": name, "description": "Runs the declared command.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"effect":      "read",
		"run":         map[string]any{"kind": "command", "commandId": commandID},
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- test helper reads its own fixture or temporary copy
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
