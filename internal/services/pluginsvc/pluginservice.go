// Package pluginsvc is the out-of-tree plugin platform's backend
// (docs/goals/0249, un-gating docs/adr/0047 §4's loader): it scans the
// plugins directory for manifests, serves each plugin's own files to
// the webview, and carries the capability model's enforcement seam --
// a plugin never holds a dangerous primitive; it requests a guarded
// action here, the manifest's declared capability set is checked
// first (declare-in-manifest), and the guardrail rule core evaluates
// the actual use (evaluate-per-action, docs/adr/0047 §2/§3).
package pluginsvc

import (
	"sync"

	"context"
	"fmt"
	"github.com/alicoding/mill/internal/adapters/osopen"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/alicoding/mill/internal/adapters/auditstore"
	"github.com/alicoding/mill/internal/adapters/pluginstate"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
)

// Manifest is the converged plugin manifest shape (docs/adr/0047 §1:
// identity metadata + a declared capability set). Rendering
// contributions happen at activate() time through the host API, so
// they are not restated here; INGESTION claims are the deliberate
// exception (docs/goals/0251) -- both ingestion chains must consult
// them without running plugin code, so they live in Contributes.
type Manifest struct {
	ID             string `json:"id" jsonschema:"required"`
	Name           string `json:"name" jsonschema:"required"`
	Version        string `json:"version" jsonschema:"required"`
	Description    string `json:"description"`
	Author         string `json:"author"`
	MinMillVersion string `json:"minMillVersion"`
	// Icon names this plugin's 128x128 icon file, relative to its own
	// folder (the standard's identity rule -- pluginsvc/conform_standard.go
	// checks it decodes to exactly that size). A sibling file with
	// "@dark" inserted before the extension, when present, is the
	// dark-appearance variant.
	Icon         string   `json:"icon"`
	Capabilities []string `json:"capabilities"`
	// Dependencies/Exports: see DependencyContribution's doc (pluginservice_dependencies.go).
	Dependencies []DependencyContribution `json:"dependencies"`
	Exports      []string                 `json:"exports"`
	Contributes  ManifestContributes      `json:"contributes"`
}

// ManifestContributes is the manifest's declarative contribution
// point (docs/goals/0251, the VSCode-shaped convention): data other
// parts of Mill read to ROUTE to a plugin, distinct from capabilities
// (what a plugin may ask to do).
type ManifestContributes struct {
	CanvasObjects []CanvasObjectContribution `json:"canvasObjects"`
	// Steps (ADR-0051 §5, pluginservice_steps.go): workflow steps the
	// plugin implements in steps.js, declared here so the catalog and
	// the Extensions row know them before any code runs.
	Steps []StepContribution `json:"steps"`
	// Captures (goal 0309, pluginservice_captures.go): quick-capture
	// surfaces the plugin renders in the capture window.
	Captures []CaptureContribution `json:"captures"`
	// Settings (docs/goals/0258 slice 1): the plugin's own declared
	// user settings, the same declare -> host renders/stores/serves
	// contract compiled-in nouns use. Declared in the manifest, not
	// at activate() time, so the Extensions row can render them
	// without running plugin code and validation fails the LOAD.
	//
	// Deprecated: superseded by Configuration (0349 S2), kept working
	// as an alias.
	Settings []SettingContribution `json:"settings"`
	// Configuration is docs/goals/0349 S2's canonical settings key
	// (VS Code's contributes.configuration); EffectiveSettings
	// resolves it against the deprecated Settings alias, and a
	// manifest declaring both refuses to load.
	Configuration []SettingContribution `json:"configuration"`
	// Menus is docs/goals/0349 S2's accepted contributes.menus shape
	// (VS Code's menu-id -> command list), classified onto Mill's own
	// seats by pluginservice_menus.go's one mapping table.
	Menus map[string][]MenuItemContribution `json:"menus"`
	// Network (docs/goals/0288): the hosts a plugin may fetch from,
	// declared so the Extensions row can state them before the plugin
	// runs and so an undeclared host is refused before any rule. Only
	// meaningful with the "fetch" capability.
	Network []NetworkContribution `json:"network"`
	// Views (docs/goals/0290): the work tabs a plugin may open, declared
	// so the Extensions row can state them before the plugin runs and
	// so activate-time registerView is checked against a declaration.
	Views []ViewContribution `json:"views"`
	// Commands (docs/goals/0324): the palette commands the plugin
	// registers at activate() time. Declaring one is what lets a tool
	// name it; an undeclared registerCommand still works.
	Commands []CommandContribution `json:"commands"`
	// Themes (docs/goals/0342): color themes the plugin ships as CSS
	// data files. Declared here because the picker lists them before
	// any plugin code runs, and a theme needs no code at all.
	Themes []ThemeContribution `json:"themes"`
	// SecretSources (goal 0306 S4, pluginservice_secretsources.go): the
	// stores this plugin can read secrets out of, implemented in
	// secrets.js. Declared here because the Sources page's Kind picker
	// lists them, and their path fields render, before any plugin code
	// runs.
	SecretSources []SecretSourceContribution `json:"secretSources"`
	// Tools (docs/goals/0324): the automation-reachable surface --
	// which of this plugin's commands, steps and reads an agent may
	// call over MCP, each with its own typed input contract.
	Tools []ToolContribution `json:"tools"`
	// MCPServers (docs/goals/0349 S5, pluginservice_mcpservers.go): MCP
	// server definitions the plugin ships for "Add to Configure".
	MCPServers []MCPServerContribution `json:"mcpServers"`
}

// ViewContribution declares one plugin-owned work tab: a slug id
// unique within the plugin and the tab's title. Entry names an .html
// page inside the plugin's own folder (docs/goals/0349): a view that
// declares one is mounted in its own sandboxed frame and needs no
// plugin code at all; a view that leaves it empty is the legacy
// same-DOM form, rendered by the render callback registered at
// activate(). Placement (docs/goals/0357) names WHERE the view is
// reachable: "" or "tab" is an ordinary work tab (today's shape);
// "board-switcher" lists it in the Atlas board's own view switcher,
// after Mill's four core entries. An unknown placement is a load
// refusal (validateViews).
type ViewContribution struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Entry     string `json:"entry"`
	Placement string `json:"placement"`
}

// NetworkContribution names one host (lowercase, optional :port) and
// the HTTP methods a plugin may use against it. An empty Methods means
// GET only -- the read-only default.
type NetworkContribution struct {
	Host    string   `json:"host"`
	Methods []string `json:"methods"`
}

// pluginIDPattern pins ids to a filesystem- and URL-safe slug: the id
// doubles as the plugin's folder name and its asset-route segment, so
// anything outside this set would be a traversal or encoding hazard,
// not a style choice.
var pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// PluginService is Wails-bound. openURL is injected so tests never
// shell out to the real OS handler. appVersion is the build-stamped
// Mill version minMillVersion enforcement compares against.
type PluginService struct {
	dir        string
	installMu  sync.Mutex
	state      *pluginstate.Store
	guardrail  *guardrailsvc.GuardrailService
	openURL    func(url string) error
	appVersion string
	// content is the guarded content-write seam (docs/goals/0289),
	// nil until the composition root wires it.
	content ContentWriter
	// secretRefs / readSetting are the secretRef door's seams
	// (pluginservice_fetch_secret.go), nil until wired -- a fetch
	// naming a secret then refuses rather than sends unauthenticated.
	secretRefs  SecretRefResolver
	readSetting SettingReader
	// trust / secretAccess are the audit export's read seams
	// (pluginservice_audit.go), nil until wired.
	trust        PluginTrustReader
	secretAccess func(actorPrefix string) ([]PluginSecretAccess, error)
	// mayRun / packs are the step-pack door's policy and cache
	// (pluginservice_steps.go).
	mayRun  func(id string, builtin bool) bool
	packsMu sync.Mutex
	packs   map[string]loadedPack
	// signingKeys is the signed tier's policy source
	// (pluginservice_signing.go), nil until wired.
	signingKeys func() []string
	// runCommand is the command-tool bridge to the webview
	// (pluginservice_toolrun.go), injected so a test never needs a live
	// window.
	runCommand func(pluginID, commandID string) (string, error)
	// examples is the embedded example tree the bundled "mill"
	// marketplace offers (marketplace_examples.go), injected because
	// go:embed paths are package-relative.
	examples fs.FS
	// download is the user-initiated HTTP seam every marketplace and
	// install fetch goes through (marketplace_store.go), nil for the
	// real client -- a test never reaches a host.
	download func(url string, limit int64) ([]byte, error)
	// sourceRead observes local acquisition attempts in tests; policy
	// refusal happens before it and os.Root performs the acquisition.
	sourceRead func(path string)
	// integrations resolves a Configure Integration's declared
	// operation for the live-view door (pluginservice_integration.go,
	// pluginservice_guardedwrite.go), nil until OpenAudit's sibling
	// composition-root wire runs.
	integrations IntegrationExecutor
	// auditStore / auditLog are the guarded-action write door's own
	// connection (pluginservice_writeaudit.go), nil until OpenAudit
	// runs -- a plugin still works with no audit store wired.
	auditStore *auditstore.Store
	auditLog   *slog.Logger
}

func New(dir string, guardrail *guardrailsvc.GuardrailService, appVersion string) *PluginService {
	// osopen, not the runtime's Browser API: the adapter's server build
	// is a documented no-op (ErrUnsupportedInServerMode), so an approved
	// open-url in server mode -- every e2e run of the plugin spec --
	// never reaches the machine's real browser. The runtime opener did.
	return &PluginService{dir: dir, state: pluginstate.New(dir), guardrail: guardrail, openURL: osopen.Open, appVersion: appVersion, runCommand: invokeCommandInWebview}
}

func (p *PluginService) openInOS(url string) error {
	if p.openURL == nil {
		return fmt.Errorf("no URL opener available in this mode")
	}
	return p.openURL(url)
}

// ListPlugins scans the plugins directory fresh on every call (the
// Extensions page's Rescan is just another call) and returns every
// plugin folder with its manifest -- valid ones ready to load,
// invalid ones carrying their human-readable Error.
func (p *PluginService) ListPlugins() ([]PluginInfo, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read plugins directory: %w", err)
	}
	// A missing plugins dir is normal (nothing installed yet) -- the
	// built-ins below still list.
	infos := make([]PluginInfo, 0, len(entries))
	scanned := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		infos = append(infos, p.scanOne(e.Name()))
		scanned[e.Name()] = true
	}
	// Built-ins fill in behind the scanned directory: a user folder
	// with the same id shadows its built-in entirely (even an invalid
	// one -- its own error row is the honest state, and deleting the
	// folder restores the built-in).
	for _, id := range builtinPluginIDs() {
		if !scanned[id] {
			infos = append(infos, scanBuiltin(id, p.appVersion))
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Manifest.ID < infos[j].Manifest.ID })
	return infos, nil
}

// resolvePlugin is the by-id lookup every non-list path uses
// (guarded actions, asset serving): the user's own folder first, the
// built-in behind it -- the same shadowing rule ListPlugins applies.
// The pattern gate up front makes the joined path traversal-safe for
// a caller-supplied id (RequestGuardedAction's pluginID arrives
// straight off the wire).
func (p *PluginService) resolvePlugin(id string) PluginInfo {
	if !pluginIDPattern.MatchString(id) {
		return PluginInfo{Manifest: Manifest{ID: id}, Error: "the manifest id must be lowercase letters, digits, and hyphens"}
	}
	if _, err := os.Stat(filepath.Join(p.dir, id)); err == nil { // #nosec G703 -- id passed pluginIDPattern above (no separators, no dots)
		return p.scanOne(id)
	}
	if isBuiltinPluginID(id) {
		return scanBuiltin(id, p.appVersion)
	}
	return p.scanOne(id)
}

func (p *PluginService) scanOne(folder string) PluginInfo {
	dir := filepath.Join(p.dir, folder)
	info := PluginInfo{Dir: dir, Manifest: Manifest{ID: folder}}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")) // #nosec G304 G703 -- dir is this service's own plugins root joined with a ReadDir entry name
	if err != nil {
		info.Error = "manifest.json is missing or unreadable"
		return info
	}
	m, err := DecodeManifest(raw)
	if err != nil {
		info.Error = "manifest.json is not valid JSON"
		return info
	}
	info.Manifest = m
	info.Grants = pluginGrants(false, m)
	info.Widened = widenedInfo(p.trust, m)
	info.Warnings = manifestWarnings(m)
	_, mainErr := os.Stat(filepath.Join(dir, "main.js")) // #nosec G703 -- folder passed pluginIDPattern (no separators, no dots)
	info.Error = manifestProblem(m, folder, mainErr == nil, p.appVersion)
	dataOnly := info.Error == "" && mainErr != nil && isDataOnlyManifest(m)
	if dataOnly {
		info.Error = dataOnlyFolderProblem(os.DirFS(dir))
	}
	if info.Error == "" {
		info.Error = stepsFileProblem(dir, m)
	}
	if info.Error == "" {
		info.Error = secretsFileProblem(dir, m)
	}
	if info.Error == "" {
		info.Error = entryFileProblem(m, func(rel string) bool {
			_, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))) // #nosec G703 -- rel passed entryPathProblem (no traversal, no absolute path)
			return statErr == nil
		})
	}
	if info.Error == "" {
		if h, err := ContentHash(dir); err == nil {
			info.ContentHash = h
		}
		if h, err := CodeHash(dir); err == nil {
			info.CodeHash = h
		}
	}
	info.DataOnly = info.Error == "" && dataOnly
	if keys := p.signingKeySet(); len(keys) > 0 {
		info.SigningPolicy = true
		info.Signed = SignatureVerified(dir, info.ContentHash, keys)
	}
	info.Tier = InstalledTier(dir, false)
	if rec, ok := ReadInstallRecord(dir); ok {
		info.Marketplace = rec.Marketplace
		if info.Error == "" && rec.Source.Kind == "theme-file" && rec.ContentHash == info.ContentHash {
			info.ThemeImport = readThemeImportMetadata(dir, m, rec)
		}
	}
	p.applyPolicy(&info)
	return info
}

// manifestProblem runs every load-blocking validation shared by the
// scanned-directory and built-in scan paths, returning the first
// human-readable problem or "".
func manifestProblem(m Manifest, folder string, mainJSExists bool, appVersion string) string {
	switch {
	case !pluginIDPattern.MatchString(m.ID):
		return "the manifest id must be lowercase letters, digits, and hyphens"
	case m.ID != folder:
		// The Obsidian convention, adopted deliberately: the folder IS
		// the identity, so a copied folder can never impersonate a
		// different plugin's id.
		return fmt.Sprintf("the manifest id %q must match the folder name %q", m.ID, folder)
	case strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "":
		return "the manifest needs a name and a version"
	case !mainJSExists && !isDataOnlyManifest(m):
		return "main.js is missing"
	}
	for _, c := range m.Capabilities {
		if !knownCapabilities[c] {
			// Fail-closed: an unknown capability blocks the LOAD,
			// never silently narrows to the known set -- the user
			// sees exactly why the plugin won't run.
			return fmt.Sprintf("unknown capability %q", c)
		}
	}
	if problem := validateContributes(m.ID, m.Capabilities, m.Contributes); problem != "" {
		return problem
	}
	if problem := validateDependencyShape(m.ID, m.Dependencies); problem != "" {
		return problem
	}
	if problem := validateExportsShape(m.Exports); problem != "" {
		return problem
	}
	return checkMinMillVersion(m.MinMillVersion, appVersion)
}

// isDataOnlyManifest is deliberately narrow: themes are the only declared
// contribution and there is no capability, dependency, or export that could
// imply executable behavior.
func isDataOnlyManifest(m Manifest) bool {
	kinds := contributionKinds(m.Contributes)
	return len(m.Capabilities) == 0 && len(m.Dependencies) == 0 && len(m.Exports) == 0 &&
		len(kinds) == 1 && kinds[0] == "themes" && len(m.Contributes.Themes) > 0
}

// dataOnlyFolderProblem closes the classification over shipped files too: a
// package that asks Mill to skip activation cannot carry dormant JavaScript.
func dataOnlyFolderProblem(root fs.FS) string {
	problem := ""
	_ = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			problem = "the data-only theme folder is unreadable"
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".js", ".mjs", ".cjs", ".jsx":
			problem = fmt.Sprintf("data-only theme extensions cannot contain JavaScript (%s)", filepath.ToSlash(path))
			return fs.SkipAll
		default:
			return nil
		}
	})
	return problem
}

// checkMinMillVersion refuses a plugin that declares it needs a newer
// Mill (the converged app-plugin convention: plugins version against
// the APP's version, never a separate API number -- docs/goals/0245's
// stability contract). The app's prerelease/build tags are stripped
// before comparing: a beta is stamped against the NEXT release
// (main.go's build-stamp trio documents exactly this), so plain
// semver would rank it below that release's minimum forever. A
// malformed minimum fails closed like any other manifest error; an
// unparseable app version (an unstamped source build) skips
// enforcement rather than refusing every version-pinned plugin.
func checkMinMillVersion(minVersion, appVersion string) string {
	if strings.TrimSpace(minVersion) == "" {
		return ""
	}
	minV := "v" + strings.TrimPrefix(minVersion, "v")
	if !semver.IsValid(minV) {
		return fmt.Sprintf("the manifest minMillVersion %q must be a version like \"1.2.3\"", minVersion)
	}
	appV := "v" + strings.TrimPrefix(appVersion, "v")
	if !semver.IsValid(appV) {
		return ""
	}
	appV = strings.TrimSuffix(appV, semver.Build(appV))
	appV = strings.TrimSuffix(appV, semver.Prerelease(appV))
	if semver.Compare(appV, minV) < 0 {
		return fmt.Sprintf("needs Mill %s or newer -- this is Mill %s", minVersion, appVersion)
	}
	return ""
}

// GuardedActionDecision is RequestGuardedAction's wire shape.
type GuardedActionDecision struct {
	Approved  bool
	Effect    string
	RuleLabel string
	// Performed is true when Mill executed the approved action itself
	// (the plugin never receives the primitive).
	Performed bool
}

// RequestGuardedAction is the plugin plane's one door to a primitive
// the plugin does not hold (docs/adr/0047 §2). The manifest must
// DECLARE the capability (an undeclared kind is refused here, before
// any rule runs); a declared one is evaluated per-action by the
// guardrail rule core -- allow/deny resolve immediately, ask parks for
// a human and blocks this call until resolved (the same park the MCP
// write plane uses). On approval Mill performs the action itself.
func (p *PluginService) RequestGuardedAction(pluginID string, kind string, attributes map[string]string, description string) (GuardedActionDecision, error) {
	plugin := p.resolvePlugin(pluginID)
	if plugin.Error != "" {
		return GuardedActionDecision{}, fmt.Errorf("plugin %q: %s", pluginID, plugin.Error)
	}
	declared := false
	for _, c := range plugin.Manifest.Capabilities {
		if c == kind {
			declared = true
			break
		}
	}
	if !declared {
		return GuardedActionDecision{}, fmt.Errorf("plugin %q does not declare the %q capability in its manifest", pluginID, kind)
	}
	decision, err := p.guardrail.RequestGuardedAction(context.Background(), guardrailsvc.GuardedAction{
		Kind:        kind,
		Attributes:  pluginActorAttributes(pluginID, plugin, attributes),
		Description: description,
		Source:      "plugin:" + pluginID,
	})
	if err != nil {
		return GuardedActionDecision{}, err
	}
	out := GuardedActionDecision{Approved: decision.Approved, Effect: string(decision.Effect), RuleLabel: decision.RuleLabel}
	if decision.Approved {
		performed, perr := p.perform(kind, attributes)
		if perr != nil {
			return out, perr
		}
		out.Performed = performed
	}
	return out, nil
}
