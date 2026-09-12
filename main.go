// Package main embeds the built frontend, sets up the window/tray, and
// constructs + wires every bounded-context service. No domain logic
// lives here (.claude/rules/backend.md).
package main

import (
	"embed"

	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	backupadapter "github.com/alicoding/mill/internal/adapters/backup"
	"github.com/alicoding/mill/internal/adapters/credential"
	"github.com/alicoding/mill/internal/adapters/dataownership"
	"github.com/alicoding/mill/internal/adapters/launchatlogin"
	"github.com/alicoding/mill/internal/adapters/settings"
	"github.com/alicoding/mill/internal/adapters/windowing"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/usererror"
	"github.com/alicoding/mill/internal/plugincli"
	"github.com/alicoding/mill/internal/services/agentloopsvc"
	"github.com/alicoding/mill/internal/services/atlassvc"
	"github.com/alicoding/mill/internal/services/backupsvc"
	"github.com/alicoding/mill/internal/services/capabilitysvc"
	"github.com/alicoding/mill/internal/services/clipboardhistorysvc"
	"github.com/alicoding/mill/internal/services/codeloopsvc"
	"github.com/alicoding/mill/internal/services/companionsvc"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/configuresvc"
	"github.com/alicoding/mill/internal/services/docssvc"
	"github.com/alicoding/mill/internal/services/executionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/alicoding/mill/internal/services/mcpsvc"
	"github.com/alicoding/mill/internal/services/menusvc"
	"github.com/alicoding/mill/internal/services/notificationsvc"
	"github.com/alicoding/mill/internal/services/pluginsvc"
	"github.com/alicoding/mill/internal/services/settingssvc"
	"github.com/alicoding/mill/internal/services/triggersvc"
	"github.com/alicoding/mill/internal/services/wiring"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// millChannel/millVersion/millUpdateVersion are the build-stamp trio release.yml (channel=release) and ci.yml's
// beta job (channel=beta) override via ldflags -X; every other build keeps the defaults below. millChannel gates
// settingssvc's install-and-restart path. millVersion feeds run receipts/the backup manifest/the release updater's
// CurrentVersion, and must agree with the git tag and build/config.yml (release.yml's verify step enforces this) --
// a beta build never touches it. millUpdateVersion is what the updater compares the beta feed's rolling tag against
// and AppVersion shows in Settings: a SemVer prerelease compares strictly below its release, so a beta based on the
// already-shipped millVersion (e.g. "0.5.0-beta.N" once v0.5.0 exists) would rank below that release forever --
// ci.yml's beta job stamps a per-build id based on the NEXT release instead.
var millChannel = "source"

const millVersion = "0.5.0"

var millUpdateVersion = millVersion

//go:embed all:frontend/dist
var assets embed.FS

// The in-app Docs surface's content (goal 0125 phase 1): the same
// userdocs tree the repository publishes and llms.txt indexes,
// embedded so docs ship inside the binary like the frontend does.
//
//go:embed all:userdocs
var userdocsFS embed.FS

// A browser loads an unpacked extension from a FOLDER, and an installed
// copy has no source tree to point at: bridgesvc writes this out.
//
//go:embed examples/browser-extension
var browserExtensionFS embed.FS

// The bundled "mill" marketplace's offerings (docs/goals/0349),
// injected below because go:embed paths are package-relative and these
// live at the repository root -- userdocsFS's own shape.
//
//go:embed all:examples/plugins
var examplePluginsFS embed.FS

//go:embed build/appicon.png
var appIconPNG []byte

//go:embed build/tray-template.png
var trayTemplateIconPNG []byte

// main initializes the application, creates the window, and wires every
// bounded-context service together.
func main() {
	// Reuses Wails3's own default logger: colorized to stderr in dev mode
	// via isatty detection, silently discarded in production builds (see
	// application.DefaultLogger's per-build-tag implementations), instead
	// of a second parallel slog handler. Passed to Mill's own services and
	// application.Options.Logger so app events (a hotkey firing) and
	// Wails3's own system messages share one stream.
	logger := application.DefaultLogger(slog.LevelInfo)

	// application.Path resolves the OS-appropriate app-support directory
	// (~/Library/Application Support on macOS, verified directly against
	// its adrg/xdg backing) -- the same convention Alfred/Raycast/1Password
	// use for their own persisted settings, not something Mill invents.
	// MILL_SETTINGS_PATH overrides this -- needed because server-mode and
	// desktop-mode builds resolve to the exact same real path otherwise,
	// which would let the Playwright e2e suite (server mode) write real
	// composed workflows into the actual desktop dev app's saved state.
	// playwright.config.ts points this at a throwaway temp file.
	defaultSettingsPath := filepath.Join(application.Path(application.PathConfigHome), "mill", "settings.json")
	settingsPath := os.Getenv("MILL_SETTINGS_PATH")
	if settingsPath == "" {
		settingsPath = defaultSettingsPath
	}
	// Plugin authoring commands are routed before any app state opens, so
	// scaffolding and source-manifest migration never touch the settings store.
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		os.Exit(plugincli.Run(os.Args[2:], pluginsvc.ResolveDir(settingsPath), millVersion, os.Stdout, os.Stderr))
	}
	executionDatabaseURL := os.Getenv("MILL_EXECUTION_DATABASE_URL")
	if executionDatabaseURL == "" {
		executionDatabaseURL = "sqlite:" + windowing.ConfigDirOrEnv("MILL_EXECUTION_DB_PATH", "execution.db")
	}

	startup := windowing.NewStartupCoordinates()
	app := application.New(application.Options{
		Name:        "mill",
		Description: "Guardrailed agentic-workflow automation",
		Logger:      logger,
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: startup.AssetMiddleware,
		},
		Mac:            windowing.MacAppOptions(),
		ShouldQuit:     startup.ShouldQuit,
		SingleInstance: singleInstanceOptions(startup.RequestActivation),
	})

	dataOwner, err := dataownership.AcquireForProcess(settingsPath, executionDatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	logger.Debug("data ownership acquired", "execution", dataOwner.ExecutionOwnership())
	settingsStore, err := settings.New(settingsPath)
	if err != nil {
		log.Fatal(err)
	}
	// docs/goals/0171-notification-spine.md: needs only settingsStore.
	notificationService := notificationsvc.New(settingsStore)

	compositionService := compositionsvc.NewCompositionService(settingsStore)
	triggerService := triggersvc.NewTriggerService(compositionService, logger, settingsStore)
	wiring.WireWorkflowLifecycle(compositionService, triggerService) // docs/goals/0250-workflow-delete-releases-hotkey.md
	// MILL_TEST_KEYRING=memory swaps the OS keychain for a process-
	// local store (e2e servers only): Linux CI has no Secret Service,
	// so real-keychain semantics -- including "credential absent" --
	// are otherwise untestable in server mode.
	credentialStore := credential.New()
	if os.Getenv("MILL_TEST_KEYRING") == "memory" {
		credentialStore = credential.NewInMemory()
	}
	configureService := configuresvc.NewConfigureService(settingsStore, compositionService, credentialStore)
	// MILL_SECRETS_PATH overrides for e2e isolation, same convention as MILL_SETTINGS_PATH above (WireSecrets' own doc comment covers the rest of what this wires). Captured in its own variable -- backupsvc's own wiring below needs the identical path, so every backup carries the vault file (goal 0359).
	vaultPath := windowing.ConfigDirOrEnv("MILL_SECRETS_PATH", "secrets.kdbx")
	// MILL_BACKUP_DIR follows the same override convention, captured
	// here (ahead of backupsvc's own construction below) since WireSecrets needs it too (goal 0359's restore-from-backup door).
	backupDir := windowing.ConfigDirOrEnv("MILL_BACKUP_DIR", "backups")
	secretService := wiring.WireSecrets(vaultPath, backupDir, credentialStore, settingsStore, configureService)
	wiring.WireSecretRedaction(secretService) // goal 0185 S4
	// docs/adr/0038: Atlas's storage/CRUD layer (cross-surface wiring
	// arrives below via injected seams, never direct imports).
	atlasService := atlassvc.NewAtlasService(settingsStore)
	// docs/goals/0101 slice 1: the companion's own AI-provider lookup
	// resolves through composition.ResolveAIProvider, which reads the
	// SAME package-level seam configureService wires just below (no
	// separate wiring call needed here).
	companionService := companionsvc.NewCompanionService(atlasService)

	// Separate SQLite file from settings.json (own schema, own lifecycle
	// -- durable-execution checkpoints, not app config) but the same
	// config-dir convention and the same MILL_* env-override shape
	// settingsPath already established above, for the identical reason:
	// desktop-mode and server-mode e2e runs must not share real state.
	//
	// MILL_EXECUTION_DATABASE_URL is a second, independent override: a
	// full DBOS-native DSN (e.g. a Postgres/CockroachDB URL a regulated
	// deployment points at its own audit database -- DBOS accepts this
	// natively, confirmed against dbos.Config's own doc comment, no
	// adapter change needed). Takes precedence over the path-based
	// override below when set; MILL_EXECUTION_DB_PATH and the default
	// sqlite path are otherwise unchanged.
	pluginDir := pluginsvc.ResolveDir(settingsPath)
	snapshotOptions := backupadapter.SnapshotOptions{
		ReadSettings: settingsStore.Snapshot,
		Participants: []backupadapter.Participant{{Name: "plugin-state", Write: func(destination string) error {
			return pluginsvc.SnapshotStoredState(pluginDir, destination)
		}}},
	}
	backupsvc.GuardVersionChange(logger, settingsStore, backupsvc.SQLiteDBPath(executionDatabaseURL), settingsPath, vaultPath, backupDir, millVersion, snapshotOptions)

	mcpAuditService := wiring.WireAuditTrails(secretService, backupsvc.SQLiteDBPath(executionDatabaseURL), logger)

	// goal 0234: composition and secretsvc's own audit store are wired
	// together in wiring.WireClipboardHistory below, once secretService's
	// OpenAudit call (mcpAuditService's own construction, above) has run.
	clipboardHistoryService := clipboardhistorysvc.NewClipboardHistoryService(settingsStore)
	wiring.WireClipboardHistory(clipboardHistoryService, secretService)

	// MUST precede NewGuardrailService -- see MigrateLegacyPendingWrites'
	// own doc comment for why the ordering here is load-bearing.
	if err := mcpsvc.MigrateLegacyPendingWrites(settingsStore); err != nil {
		logger.Error("migrate legacy MCP pending writes", "error", err)
	}
	guardrailService := guardrailsvc.NewGuardrailService(settingsStore, compositionService)
	wiring.WireAIProviderCheckAuthorizer(configureService, guardrailService)
	pluginService := wiring.NewPluginService(settingsPath, guardrailService, millChannel, millUpdateVersion, backupsvc.SQLiteDBPath(executionDatabaseURL), logger)
	pluginService.SetExampleMarketplace(examplePluginsFS)
	// docs/goals/0240 S1: the coding loop's Confirm-screen preview --
	// read-only over guardrailService.Rules(). Its ExecutionService
	// dependency (goal 0240 S2, RunCommandBlock's own doc comment) is
	// wired after the durable runtime is prepared below.
	codeLoopService := codeloopsvc.NewCodeLoopService(guardrailService)
	executionService, err := executionsvc.PrepareExecutionServiceWithOwnership(
		executionDatabaseURL, dataOwner.ExecutionOwnership(), compositionService, guardrailService,
	)
	if err != nil {
		log.Fatal(err)
	}
	triggerService.SetExecutionService(executionService)
	wiring.WireSystemEventNotifications(executionService, triggerService, notificationService)
	wiring.WireAtlasWorkflowRunners(executionService, atlasService)
	executionService.SetVersion(millVersion)
	executionService.SetEnvironmentLabelLookup(configureService.EnvironmentLabel)
	configuresvc.SetAIProviderMutationCoordinator(
		configureService,
		func(mutate func(assertUnused func(id string) error) error) error {
			return executionsvc.WithAIProviderMutations(executionService, mutate)
		},
		func(id string, revision func() string) aiprovider.ChangeImpact {
			return executionsvc.AIProviderChangeImpact(executionService, id, revision)
		},
	)
	wiring.WireAIProviderSamples(compositionService, configureService, executionService)
	codeLoopService.SetExecutionService(executionService)
	wiring.WireCodingLoopSecrets(codeLoopService, secretService)
	wiring.WireVaultWaits(executionService, secretService)                                 // goal 0360 S2
	wiring.WireCodingLoopEnvPreview(codeLoopService, compositionService, configureService) // docs/goals/0240 S4
	atlasService.WireCompositionSeams(triggerService.DispatchAtlasCardChange)              // goal 0066
	// Cross-service seam adapters (recognition, List projection) live in the wiring package -- composition-root code split out of this file at the 500-line limit.
	wiring.WireAtlasProjections(atlasService, configureService, compositionService)
	wiring.WireValidationSeams(configureService)
	wiring.WireConfigureSeams(atlasService, configureService, compositionService, pluginService) // paste conversion + plugin/workflow content writes + undo journals
	wiring.WireCanvasObjectExamples(atlasService, pluginService)                                 // goal 0411: Board gallery seeds every plugin's declared canvasObjects example
	wiring.WireNotify(notificationService)                                                       // goal 0368: apply-notify publishes through the notification spine

	backupService := backupsvc.Wire(backupsvc.SQLiteDBPath(executionDatabaseURL), settingsPath, vaultPath, backupDir, millVersion, compositionService, configureService, atlasService, snapshotOptions)

	// docs/adr/0038, goal 0063/0067: the share model's mirror root, plus the image tool's captures folder (goal 0169 slice 2).
	wiring.WireAtlasStorageDirs(atlasService)
	atlasService.SetGuardedDataPaths(settingsPath, backupsvc.SQLiteDBPath(executionDatabaseURL), backupDir)

	remoteAuthService := wiring.WireRemoteAuth(settingsStore, logger)                                                                                                  // docs/goals/0132-remote-access.md SLICE 1
	bridgeService := wiring.WireBrowserBridge(remoteAuthService, logger, browserExtensionFS, filepath.Dir(settingsPath), backupsvc.SQLiteDBPath(executionDatabaseURL)) // the browser bridge's own loopback listener (docs/goals/0350) and audit connection (goal 0351 S2)
	wiring.WireWebhookRespond(bridgeService, triggerService)                                                                                                           // goal 0368: the webhook door fires a trigger, never a pipe. goal 0373: it can wait for a reply.

	settingsService := settingssvc.NewSettingsService(settingsStore, triggerService, settingsPath != defaultSettingsPath)
	auditService := wiring.WireAuditExport(backupsvc.SQLiteDBPath(executionDatabaseURL), settingsService.GetAuditRetentionEntries(), logger) // goal 0351 S2: export/retention over the shared audit trail
	settingsService.SetAuditRetentionChanged(auditService.PruneNow)                                                                          // a lowered cap takes effect immediately, not only at the next restart
	wiring.WireSettingsEraSeams(settingsService, notificationService, remoteAuthService, triggerService, atlasService, pluginService, secretService)
	wiring.WirePluginEntityRefEvents(settingsService, pluginService, configureService) // goal 0400: an entityRef setting's changed value fires entity.referenced/dereferenced
	settingsService.SetAppVersion(millUpdateVersion)
	// The user's persisted channel opt-in wins over the build stamp for
	// the resolved channel (a source-built copy can deliberately follow
	// the beta feed); wiring.ResolveAndWireUpdateChannel also records
	// the raw stamp for the auto-download policy's local-build guard.
	effectiveChannel := wiring.ResolveAndWireUpdateChannel(settingsService, millChannel)
	// goal 0100: DownloadAndInstallUpdate's pre-swap snapshot seam.
	settingsService.SetBackupRunner(backupService.BackupRunner())
	// Bidirectional hotkey-conflict check (docs/SPEC.md §3.7): a
	// per-workflow hotkey can't silently collide with the app-level
	// summon hotkey, and vice versa -- SettingsService.AssignSummonHotkey
	// already checks triggerService.ClaimedCombos() directly; this wires
	// the other direction.
	triggerService.SetReservedCombo(settingsService.ReservedCombo)
	// docs/goals/0014-home-dashboard.md: Home's TimeSaved metric reads
	// each workflow's user-editable "minutes saved per run" estimate,
	// which SettingsService owns (its own persisted preference, §3.7) --
	// wired in only now since ExecutionService is constructed before
	// SettingsService exists, same late-bound-setter shape as
	// SetReservedCombo just above.
	executionService.SetMinutesSavedLookup(settingsService.GetWorkflowMinutesSaved)
	if err := executionsvc.LaunchExecutionService(executionService); err != nil {
		log.Fatal(err)
	}
	if err := configuresvc.ReconcileBuiltInAIProviders(configureService); err != nil {
		logger.Error("reconcile built-in AI providers", "error", err)
	}

	// docs/SPEC.md §11 (task #12): Mill as MCP server, exposing its own
	// workflows/Configure data as read-only Resources. Bind-address
	// precedence (settingssvc.ResolveMCPAddr): the MILL_MCP_ADDR env
	// ALWAYS wins (a deploy/env-level override, same MILL_* convention
	// as settingsPath/executionDatabaseURL above), then the Settings >
	// MCP access address, then the loopback default -- never 0.0.0.0
	// unless explicitly chosen, since this is a new, unauthenticated
	// local listener and staying loopback-bound is the conservative
	// default until a real access-control need is named. A bind failure
	// is logged, not fatal -- this is additive local tooling, not
	// something the rest of the app depends on to function.
	millMCPAddr, _ := settingssvc.ResolveMCPAddr(os.Getenv("MILL_MCP_ADDR"), settingsService.MCPAccessAddress())
	millMCPService := mcpsvc.NewMillMCPService(millVersion, compositionService, configureService, settingsStore, userdocsFS, mcpAuditService.ServerMiddleware())
	wiring.WireMillMCPService(millMCPService, settingsService, executionService, atlasService, mcpAuditService, guardrailService, secretService, millMCPAddr, logger)
	wiring.WireMCPPluginCatalog(millMCPService, pluginService, settingsService) // plugin contributions over MCP (docs/goals/0324)

	agentLoopService := agentloopsvc.NewAgentLoopService(millMCPService) // an MCP client of it, ADR-0035

	var mainWindow *application.WebviewWindow

	// Every bound service marshals its errors through the same door: a
	// declared usererror crosses as its code and one sentence, anything
	// else is logged with its chain and reaches the UI as the generic
	// sentence (.claude/rules/ux-writing.md). It is set PER SERVICE
	// because Options.MarshalError never reaches a bound call in this
	// Wails version -- see usererror.MarshalForWails.
	boundErrors := application.ServiceOptions{MarshalError: usererror.MarshalForWails(logger)}

	startup.SetAssetMiddleware(wiring.ComposedAssetMiddleware(remoteAuthService, pluginService))
	startup.SetShouldQuit(settingsService.ShouldQuit)
	for _, service := range []application.Service{
		application.NewServiceWithOptions(&capabilitysvc.CapabilitiesService{}, boundErrors),
		application.NewServiceWithOptions(compositionService, boundErrors),
		application.NewServiceWithOptions(triggerService, boundErrors),
		application.NewServiceWithOptions(configureService, boundErrors),
		application.NewServiceWithOptions(secretService, boundErrors),
		application.NewServiceWithOptions(atlasService, boundErrors),
		application.NewServiceWithOptions(companionService, boundErrors),
		application.NewServiceWithOptions(agentLoopService, boundErrors),
		application.NewServiceWithOptions(guardrailService, boundErrors),
		application.NewServiceWithOptions(pluginService, boundErrors),
		application.NewServiceWithOptions(clipboardHistoryService, boundErrors),
		application.NewServiceWithOptions(codeLoopService, boundErrors),
		application.NewServiceWithOptions(executionService, boundErrors),
		application.NewServiceWithOptions(settingsService, boundErrors),
		application.NewServiceWithOptions(backupService, boundErrors),
		application.NewServiceWithOptions(docssvc.New(userdocsFS), boundErrors),
		application.NewServiceWithOptions(mcpAuditService, boundErrors),
		application.NewServiceWithOptions(auditService, boundErrors),
		application.NewServiceWithOptions(remoteAuthService, boundErrors),
		application.NewServiceWithOptions(bridgeService, boundErrors),
		application.NewServiceWithOptions(notificationService, boundErrors),
		application.NewServiceWithOptions(menusvc.New(), boundErrors),
	} {
		app.RegisterService(service)
	}

	// Wails3's own first-party self-updater (v3/pkg/updater) -- app.Updater
	// is constructed by application.New() itself; the provider/Init
	// wiring is extracted to settingssvc.InitUpdater (keeps this file
	// under its own line-count convention). A construction failure here
	// is logged, not fatal -- a broken updater must never block the app
	// from starting.
	if err := settingssvc.InitUpdater(app.Updater, "alicoding/mill", settingssvc.ResolveUpdateCurrentVersion(effectiveChannel, millUpdateVersion), effectiveChannel, settingsService); err != nil {
		logger.Error("updater init", "error", err)
	}
	// Opt-in daily background check (goal 0122) -- a no-op unless the
	// user enabled it in Settings; applies at boot like the channel
	// preference.
	settingsService.StartAutoUpdateChecks()

	// Everything that must wait for the native run loop (hotkeys, trigger
	// sync, the menu-accelerator release) lives in wiring.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		startup.AttachActivation(func() {
			mainWindow.Restore()
			mainWindow.Focus()
		})
		wiring.ApplicationStarted(triggerService, compositionService, settingsService, millMCPService, logger)
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	//
	// Window sized to the golden ratio (1000 / 618 ≈ 1.618) by default --
	// overridden by the persisted geometry from a previous session, if
	// any (docs/SPEC.md §3.7's Update), applied here rather than moved
	// after creation since there's no "move it after creation" path that
	// avoids an initial flash at the default position/size.
	windowWidth, windowHeight := 1000, 618
	windowX, windowY := 0, 0
	windowStartState := application.WindowStateNormal
	hasPosition := false
	if x, y, w, h, maximized, ok := settingsService.LoadWindowGeometry(); ok {
		windowX, windowY, hasPosition = x, y, true
		windowWidth, windowHeight = w, h
		if maximized {
			windowStartState = application.WindowStateMaximised
		}
	}
	windowOptions := application.WebviewWindowOptions{
		// Explicit name so tooling addressing windows by name (the MCP
		// bridge's `window` parameter, internal/webviewbridgesmoke) can
		// target the main window deterministically instead of relying on
		// Wails's auto-generated "window-N" fallback.
		Name:   "main",
		Title:  "Mill",
		Width:  windowWidth,
		Height: windowHeight,
		// MinWidth/MinHeight are Wails3's own mechanism for "don't let the
		// window shrink small enough to break the layout" (see
		// docs/SPEC.md §2.2) -- not a custom guard. Floor chosen so the
		// UnderlineNav tabs, a Runbook card's action row, and the footer
		// all still fit without wrapping into each other.
		MinWidth:   640,
		MinHeight:  420,
		StartState: windowStartState,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
		// EnableFileDrop turns on Wails3's own native OS drag-and-drop:
		// real absolute paths, delivered only to elements carrying
		// data-file-drop-target (AtlasBoard.tsx, AtlasCardOverlay.tsx).
		// In-app HTML5 drag (the creation tray, React Flow node drag) is
		// unaffected -- the runtime only intercepts a 'Files' MIME type.
		EnableFileDrop: true,
	}
	if hasPosition {
		windowOptions.X = windowX
		windowOptions.Y = windowY
		// InitialPosition defaults to WindowCentered (its zero value) --
		// X/Y are silently ignored unless this is explicitly set to
		// WindowXY, confirmed directly against the SDK source before
		// relying on it, not assumed.
		windowOptions.InitialPosition = application.WindowXY
	}
	mainWindow = app.Window.NewWithOptions(windowOptions)
	// Close means hide (goal 0276): the toolkit's default WindowClosing
	// listener DESTROYS the window, after which every show-main path
	// (tray "Open Mill", summon, dock reopen) calls Show() on a dead
	// window -- a silent no-op that strands the user. Hooks run BEFORE
	// that listener and Cancel() stops it (verified against the
	// toolkit's own HandleWindowEvent); quitting has its own gate
	// (ShouldQuit above). Hide-on-close is the archetype goal 0188
	// adopted (ApplicationShouldTerminateAfterLastWindowClosed=false is
	// only honest if the window is still there to bring back).
	mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		settingsService.HideMainWindowGuarded()
	})
	settingsService.SetWindow(windowing.WrapWindow(mainWindow))
	settingsService.WatchWindowGeometry()
	atlasService.WireFileDropWindow(windowing.WrapWindow(mainWindow))
	secretService.WireLockTriggers(windowing.WrapWindow(mainWindow))
	launchatlogin.SetAutostartManager(app.Autostart)

	// ADR-0033 second-window family, built in auxwindows.go.
	wireAuxWindows(app, settingsService)

	setupTray(app, settingsService)

	// Create a goroutine that emits an event containing the current time every second.
	// The frontend can listen to this event and update the UI accordingly.
	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	// Register teardown with Wails' native lifecycle before Run. The adapter
	// also invokes the same exactly-once owner if Run returns or startup fails.
	err = windowing.RunWithShutdown(app, func() {
		wiring.RunShutdown(logger, executionService, backupService, millMCPService, pluginService, mcpAuditService, atlasService, secretService, bridgeService, auditService, configureService)
	})

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
