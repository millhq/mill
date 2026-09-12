package plugincli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alicoding/mill/internal/pluginmigrate"
	"github.com/alicoding/mill/internal/services/pluginsvc"
)

const testMillVersion = "1.0.0"

func TestRunPreservesPluginNew(t *testing.T) {
	parent := t.TempDir()
	var out, errOut bytes.Buffer
	if code := Run([]string{"new", "Router Demo", "--dir", parent}, filepath.Join(t.TempDir(), "installed"), "0.5.0", &out, &errOut); code != 0 {
		t.Fatalf("Run exit = %d, stderr = %s", code, errOut.String())
	}
	if problems := pluginsvc.ConformDir(filepath.Join(parent, "router-demo"), "0.5.0"); len(problems) > 0 {
		t.Fatalf("routed scaffold does not conform: %v", problems)
	}
}

func TestRunMigrateJSONEmitsRFC6902PlanWithoutWriting(t *testing.T) {
	dir := copyHistoricalPlugin(t)
	before := read(t, filepath.Join(dir, "manifest.json"))
	var out, errOut bytes.Buffer
	if code := Run([]string{"migrate", dir, "--json"}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 0 {
		t.Fatalf("Run exit = %d, stderr = %s", code, errOut.String())
	}
	var plan pluginmigrate.Plan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if plan.FormatVersion != pluginmigrate.PlanFormatVersion || plan.PluginID != "mill-bookmark" ||
		!slices.Equal(plan.Migrations, []string{pluginmigrate.ConfigurationKeyMigrationID}) || !plan.HasPatch() || plan.Applied {
		t.Fatalf("plan = %+v", plan)
	}
	var operations []map[string]any
	if err := json.Unmarshal(plan.Patch, &operations); err != nil || len(operations) != 2 {
		t.Fatalf("patch = %s, error = %v", plan.Patch, err)
	}
	if after := read(t, filepath.Join(dir, "manifest.json")); !bytes.Equal(after, before) {
		t.Fatal("JSON preview wrote the source")
	}
}

func TestRunMigrateApplyAndCurrentOutput(t *testing.T) {
	dir := copyHistoricalPlugin(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"migrate", "--apply", dir}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 0 {
		t.Fatalf("apply exit = %d, stderr = %s", code, errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Applied to manifest.json.")) {
		t.Fatalf("apply output = %q", out.String())
	}
	out.Reset()
	if code := Run([]string{"migrate", dir}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 0 {
		t.Fatalf("second preview exit = %d, stderr = %s", code, errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("This plugin is current.")) {
		t.Fatalf("second preview output = %q", out.String())
	}
}

func TestRunMigratePrintsOrderedCommandPlanWithoutHardCodedSteps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mill-index")
	if err := pluginsvc.CopyPluginFolder(filepath.Join("..", "pluginmigrate", "testdata", "mill-index"), dir); err != nil {
		t.Fatal(err)
	}
	before := read(t, filepath.Join(dir, "manifest.json"))
	var out, errOut bytes.Buffer
	if code := Run([]string{"migrate", dir}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 0 {
		t.Fatalf("Run exit = %d, stderr = %s", code, errOut.String())
	}
	for _, want := range []string{
		"Plan format: 1", "  - command-namespace", `"path": "/contributes/commands/0/id"`,
		`"path": "/contributes/tools/0/run/commandId"`, "Run again with --apply",
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if after := read(t, filepath.Join(dir, "manifest.json")); !bytes.Equal(after, before) {
		t.Fatal("human preview wrote the source")
	}
}

func TestRunMigrateUsesMillVersionForProspectiveValidation(t *testing.T) {
	dir := copyHistoricalPlugin(t)
	path := filepath.Join(dir, "manifest.json")
	before := read(t, path)
	tooNew := bytes.Replace(before, []byte(`"minMillVersion": "0.9.0"`), []byte(`"minMillVersion": "99.0.0"`), 1)
	if bytes.Equal(tooNew, before) {
		t.Fatal("historical fixture has no minMillVersion to replace")
	}
	if err := os.WriteFile(path, tooNew, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := Run([]string{"migrate", dir, "--apply"}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 1 {
		t.Fatalf("Run exit = %d, want 1; stderr = %s", code, errOut.String())
	}
	if !bytes.Contains(errOut.Bytes(), []byte("needs Mill 99.0.0 or newer")) {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if after := read(t, path); !bytes.Equal(after, tooNew) {
		t.Fatal("failed version validation wrote the source")
	}
}

func TestRunMigrateBothKeysExitsTwo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "both")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"id":"both","contributes":{"settings":[],"configuration":[]}}`)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"migrate", dir, "--apply"}, filepath.Join(t.TempDir(), "installed"), testMillVersion, &out, &errOut); code != 2 {
		t.Fatalf("Run exit = %d, want 2; stderr = %s", code, errOut.String())
	}
	if got := read(t, filepath.Join(dir, "manifest.json")); !bytes.Equal(got, manifest) {
		t.Fatal("manual decision wrote the manifest")
	}
}

func TestRunMigrateUsage(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"migrate", "a", "b"}, {"migrate", "a", "--force"}, {"unknown"}} {
		var errOut bytes.Buffer
		if code := Run(args, "/plugins", "0.5.0", &bytes.Buffer{}, &errOut); code != 1 || errOut.Len() == 0 {
			t.Fatalf("Run(%v) = %d, stderr = %q", args, code, errOut.String())
		}
	}
}

func copyHistoricalPlugin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mill-bookmark")
	if err := pluginsvc.CopyPluginFolder(filepath.Join("..", "..", "examples", "plugins", "mill-bookmark"), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- test helper reads its own temporary plugin
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
