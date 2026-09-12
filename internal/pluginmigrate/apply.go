package pluginmigrate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alicoding/mill/internal/services/pluginsvc"
)

// Apply atomically replaces manifest.json when the prepared source is still
// unchanged and has no manual decision.
func (p *Prepared) Apply() error {
	if len(p.Plan.Manual) > 0 {
		return fmt.Errorf("migration plan needs a manual decision")
	}
	if !p.Plan.HasPatch() {
		return nil
	}
	path := filepath.Join(p.root, "manifest.json")
	current, err := os.ReadFile(path) // #nosec G304 -- root came from the explicit source directory
	if err != nil {
		return fmt.Errorf("read manifest.json before apply: %w", err)
	}
	if !bytes.Equal(current, p.original) {
		return fmt.Errorf("manifest.json changed after preview; run migrate again")
	}
	if problems := pluginsvc.ConformDirWithManifest(p.root, p.candidate, p.appVersion); len(problems) > 0 {
		return fmt.Errorf("migrated plugin no longer conforms: %v", problems)
	}
	if err := replaceFile(path, p.candidate); err != nil {
		return fmt.Errorf("replace manifest.json: %w", err)
	}
	p.Plan.Applied = true
	return nil
}

func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path) // #nosec G304 -- path is the source plugin's manifest
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".manifest.json.migrate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil { // #nosec G703 -- both paths share the source manifest's directory
		return err
	}
	keep = false
	return nil
}

func canonicalExistingDir(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Abs(resolved)
}

func canonicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Abs(resolved)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return filepath.Abs(path)
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
