package pluginsvc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConformDir is the conformance half of the platform contract
// (ADR-0051) that lives on the Go side: everything the loader would
// refuse at load time, run ahead of time over a plugin folder -- the
// same validator (manifestProblem), never a second one -- plus the
// folder-level rules the loader only meets file by file: every file
// under the folder must be one the asset route can serve (the
// allowlist in pluginservice_assets.go), and nothing may sit outside
// the folder through a symlink. appVersion is the Mill version the
// manifest's minMillVersion is checked against; pass "" to skip that
// one check (an author's machine has no Mill version to compare).
// Returns the problems found, empty when the folder conforms.
func ConformDir(dir, appVersion string) []string {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")) // #nosec G304 -- the caller's own plugin folder
	if err != nil {
		return []string{"manifest.json is missing or unreadable"}
	}
	return ConformDirWithManifest(dir, raw, appVersion)
}

// ConformDirWithManifest checks a prospective manifest against the folder it
// would govern, without writing it first. It shares every loader, standard and
// cross-file rule with ConformDir.
func ConformDirWithManifest(dir string, raw []byte, appVersion string) []string {
	var problems []string
	folder := filepath.Base(filepath.Clean(dir))
	m, parseProblem := parseManifest(raw)
	if parseProblem != "" {
		return []string{parseProblem}
	}
	_, mainErr := os.Stat(filepath.Join(dir, "main.js"))
	if p := manifestProblem(m, folder, mainErr == nil, appVersion); p != "" {
		problems = append(problems, p)
	}
	if mainErr != nil && isDataOnlyManifest(m) {
		if p := dataOnlyFolderProblem(os.DirFS(dir)); p != "" {
			problems = append(problems, p)
		}
	}
	problems = append(problems, conformStepPack(dir, m)...)
	problems = append(problems, conformSecretSourcePack(dir, m)...)
	problems = append(problems, conformFiles(dir)...)
	problems = append(problems, conformStandard(dir, m)...)
	themeProblems, _ := conformTheme(dir)
	problems = append(problems, themeProblems...)
	installRefusals, _ := InstallChecks(dir, m)
	problems = append(problems, installRefusals...)
	sort.Strings(problems)
	return problems
}

// ConformThemeWarnings returns the theme advice for a folder -- separate
// from ConformDir because a warning is the author's call, not a
// failure, and only the command-line checker surfaces it.
func ConformThemeWarnings(dir string) []string {
	_, warnings := conformTheme(dir)
	return warnings
}

// conformFiles walks the folder: every file must carry an allowlisted
// extension (the asset route serves nothing else, so anything else is
// dead weight at best and a surprise at worst), and no symlink may
// point outside the folder (the route refuses traversal; a symlink
// would be the one way around it).
func conformFiles(dir string) []string {
	var problems []string
	root, err := filepath.Abs(dir)
	if err != nil {
		return []string{fmt.Sprintf("cannot resolve %q: %v", dir, err)}
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			problems = append(problems, fmt.Sprintf("cannot read %q: %v", path, walkErr))
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		problem, skip := entryProblem(root, path, rel, d)
		if problem != "" {
			problems = append(problems, problem)
		}
		if skip {
			return filepath.SkipDir
		}
		return nil
	})
	return problems
}

// entryProblem judges one walked entry: a symlink must resolve inside
// the folder; hidden and dependency directories are skipped wholesale;
// hidden files are ignored; every other file must carry a served
// extension.
func entryProblem(root, path, rel string, d fs.DirEntry) (problem string, skipDir bool) {
	if d.Type()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Sprintf("%s: a symlink must stay inside the plugin folder", rel), false
		}
		return "", false
	}
	if d.IsDir() {
		return "", strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules"
	}
	if strings.HasPrefix(d.Name(), ".") {
		return "", false
	}
	if _, ok := assetExtensions[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
		return fmt.Sprintf("%s: only .js, .css, and .json files are served to the plugin", rel), false
	}
	return "", false
}

func parseManifest(raw []byte) (Manifest, string) {
	m, err := DecodeManifest(raw)
	if err != nil {
		return m, "manifest.json is not valid JSON"
	}
	return m, ""
}

// DecodeManifest applies the strict JSON syntax contract shared by the plugin
// loader, conformance checks, and source migrations.
func DecodeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
