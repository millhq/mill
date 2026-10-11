package settingssvc

import (
	"context"
	"fmt"
	"github.com/alicoding/mill/internal/services/dataevent"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alicoding/mill/internal/domain/usererror"
	"github.com/wailsapp/wails/v3/pkg/updater"
	updaterGithub "github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// InitUpdater constructs the GitHub Releases provider and initializes
// Wails3's own app.Updater singleton (u, already constructed by
// application.New()), then wires it into s via SetUpdater -- extracted
// from main.go's own wiring to keep that file under its line-count
// convention. AssetMatcher: the default matcher requires the literal
// GOOS in the asset name; Mill's assets say "macos", not "darwin" (see
// UpdaterAssetMatcher). ChecksumAsset: release.yml publishes
// SHA256SUMS next to the zip -- naming it makes the provider verify
// the download against it. Prerelease is set only for channel=="beta":
// the GitHub provider's Check walks /releases/latest (excludes
// prereleases) unless Prerelease is true, in which case it walks
// /releases and returns the newest published non-draft entry --
// exactly the "update to a newer beta OR a newer real release,
// whichever shipped last" behavior goal 0100 wants, with zero extra
// code (confirmed directly against the vendored provider source). A
// release-channel build's Prerelease stays false, so it can never see
// the beta feed -- unaffected, byte-identical to before. A
// construction failure here would only happen from a malformed static
// Config, not a network call (New doesn't hit the network) -- returned
// for the caller to log, never fatal, since a broken updater must
// never block the app from starting.
func InitUpdater(u *updater.Updater, repo, currentVersion, channel string, s *SettingsService) error {
	httpClient := newUpdaterHTTPClient(currentVersion, s.OutboundProxyURL())
	ghProvider, err := updaterGithub.New(updaterGithub.Config{
		Repository:    repo,
		AssetMatcher:  UpdaterAssetMatcher,
		ChecksumAsset: "SHA256SUMS",
		Prerelease:    channel == "beta",
		HTTPClient:    httpClient,
	})
	if err != nil {
		return fmt.Errorf("updater provider init: %w", err)
	}
	if err := u.Init(updater.Config{
		CurrentVersion: currentVersion,
		Providers:      []updater.Provider{ghProvider},
	}); err != nil {
		return fmt.Errorf("updater init: %w", err)
	}
	s.SetUpdater(u)
	// listReleasesNewerThan (settingsservice_updates_notes.go) needs
	// the same repo/channel/client InitUpdater just resolved to
	// enumerate every release newer than installed for the "what's
	// new" notes list -- cached here rather than reconstructed per
	// check.
	s.setUpdaterNotesSource(repo, channel == "beta", httpClient)
	return nil
}

// testUpdateChannelEnv lets the Playwright e2e suite (server mode, no
// build-time ldflags available) render both channels' Updates UI
// against one binary -- same env-override seam as AtlasService's
// MILL_TEST_FOLDER_PICK_PATH.
const testUpdateChannelEnv = "MILL_TEST_UPDATE_CHANNEL"

// testUpdateFakeVersionEnv makes CheckForUpdates return a canned
// "update available" result with no network call, so e2e can render
// and interact with the available-update card without a real newer
// release existing. DownloadAndInstallUpdate honors it too, but only
// to refuse with a plain error -- fake mode never reaches the network.
const testUpdateFakeVersionEnv = "MILL_TEST_UPDATE_FAKE_VERSION"

// testUpdateCheckDelayEnv holds CheckForUpdates' fake-mode response for
// a set number of milliseconds before returning, so e2e can observe the
// "Checking…" state deterministically instead of racing a real
// same-tick promise resolution. Ignored outside fake mode.
const testUpdateCheckDelayEnv = "MILL_TEST_UPDATE_CHECK_DELAY_MS"

// testUpdateCheckFailEnv makes fake mode return a failed outcome
// instead of a canned available update, so e2e can prove a failed
// check renders honestly (never as "up to date") without a real
// network call. Ignored outside fake mode.
const testUpdateCheckFailEnv = "MILL_TEST_UPDATE_CHECK_FAIL"

// testUpdateUpToDateEnv makes fake mode return a clean nothing-new
// outcome, so e2e can prove a user-run check ANSWERS when there is no
// update (goal 0275) without a real network call. Ignored outside
// fake mode.
const testUpdateUpToDateEnv = "MILL_TEST_UPDATE_UP_TO_DATE"

// testUpdateDownloadDelayEnv holds DownloadAndInstallUpdate's fake-mode
// refusal for a set number of milliseconds before returning, so e2e can
// observe the Downloading phase (goal 0142's UpdateNotice.Downloading)
// of a background auto-download deterministically instead of racing a
// same-tick failure. Ignored outside fake mode.
const testUpdateDownloadDelayEnv = "MILL_TEST_UPDATE_DOWNLOAD_DELAY_MS"

// SetUpdater wires Wails3's own app.Updater singleton (constructed by
// application.New() itself, already Init'd by main.go with a GitHub
// Releases provider) -- set after app construction, same "wire the
// rest after construction" shape as SetWindow. docs/SPEC.md §3.7's
// research confirmed this needs no new dependency: v3/pkg/updater is
// Wails3's own first-party package.
//
//wails:ignore
func (s *SettingsService) SetUpdater(u *updater.Updater) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updater = u
}

// SetBackupRunner wires the pre-update-snapshot seam
// DownloadAndInstallUpdate calls before any bundle swap (goal 0100's
// data-safety mandate) -- set from main.go via
// backupService.BackupRunner(), the exact same injected-closure shape
// composition.SetBackupRunner already uses for apply-backup-snapshot
// nodes.
//
//wails:ignore
func (s *SettingsService) SetBackupRunner(fn func(keepN int) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backupRunner = fn
}

// UpdateCheckResult is CheckForUpdates' Wails-bound result shape.
type UpdateCheckResult struct {
	UpdateAvailable bool   `json:"updateAvailable"`
	Version         string `json:"version"`
	CurrentVersion  string `json:"currentVersion"`
	Notes           string `json:"notes"`
}

// SetAppVersion records Mill's own running version for display; wired
// from main.go's millUpdateVersion (millVersion for release/source,
// a per-build beta identifier for the beta channel).
//
//wails:ignore
func (s *SettingsService) SetAppVersion(v string) {
	s.mu.Lock()
	s.appVersion = v
	s.mu.Unlock()
}

// AppVersion returns Mill's own release version (empty until wired).
func (s *SettingsService) AppVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appVersion
}

// SetUpdateChannel records how this binary was distributed (main.go's
// millChannel var, itself ldflags-overridden to "release" only by
// release.yml's macOS build step). testUpdateChannelEnv wins when set,
// the same test-seam shape as AtlasService.DetectSyncRoots's
// MILL_TEST_FOLDER_PICK_PATH -- lets e2e prove both channels' Settings
// UI against one build.
//
//wails:ignore
func (s *SettingsService) SetUpdateChannel(channel string) {
	if v := os.Getenv(testUpdateChannelEnv); v != "" {
		channel = v
	}
	s.mu.Lock()
	s.updateChannel = channel
	s.mu.Unlock()
}

// UpdateChannel reports the resolved distribution channel ("source",
// "release", or "beta") -- the Settings > Updates surface's gate
// between "Update now" and the pull-and-rebuild instructions, and
// DownloadAndInstallUpdate's own server-side enforcement of the same
// gate.
func (s *SettingsService) UpdateChannel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateChannel
}

// updateChannelPreferenceKey persists the user's channel opt-in. A
// source-built copy following the beta feed is a deliberate, explicit
// choice (docs/goals/archive/0100-beta-channel.md's follow-up slice)
// -- source-never-installs survives as the DEFAULT, not a wall.
const updateChannelPreferenceKey = "updateChannelPreference"

// UpdateChannelPreference returns the persisted channel override:
// "" (follow the build's own channel), "beta", or "release".
func (s *SettingsService) UpdateChannelPreference() string {
	v, _ := s.store.Get(updateChannelPreferenceKey).(string)
	if v != "beta" && v != "release" {
		return ""
	}
	return v
}

// SetUpdateChannelPreference persists the channel override. The
// preference resolves at BOOT (ResolveUpdateChannel below): the update
// provider's feed selection is fixed at Init, so a change applies
// after the next restart -- the UI says so rather than pretending a
// live switch happened.
func (s *SettingsService) SetUpdateChannelPreference(pref string) error {
	if pref != "" && pref != "beta" && pref != "release" {
		return fmt.Errorf("unknown update channel %q", pref)
	}
	return s.store.Set(updateChannelPreferenceKey, pref)
}

// ResolveUpdateChannel returns the effective channel for this run: the
// persisted preference when set, else the build's ldflags stamp.
// main.go calls this once before SetUpdateChannel/InitUpdater so the
// guard, the UI label, and the provider's feed all agree.
func (s *SettingsService) ResolveUpdateChannel(buildChannel string) string {
	if pref := s.UpdateChannelPreference(); pref != "" {
		return pref
	}
	return buildChannel
}

// ResolveUpdateCurrentVersion returns what the updater should compare
// the feed against. A source build stamps the bare release version
// ("0.4.0"), and SemVer puts every prerelease BELOW its release --
// so a source build opted into the beta channel could never see any
// 0.4.0-beta.N as newer and always read "latest". When the
// effective channel is beta and the
// stamped version carries no prerelease, floor it at "-beta.0": every
// real beta.N then registers as newer, and a future plain release
// still wins over any beta. Every other combination passes through
// untouched.
func ResolveUpdateCurrentVersion(effectiveChannel, updateVersion string) string {
	if effectiveChannel == "beta" && !strings.Contains(updateVersion, "-") {
		return updateVersion + "-beta.0"
	}
	return updateVersion
}

// CheckForUpdates asks the configured provider (GitHub Releases,
// millhq/mill) whether a newer version exists. The Wails-bound RPC
// surface (no context parameter is bindable); checkForUpdates below
// does the real work against a caller-supplied context, letting the
// background loop (settingsservice_updatenotice.go) propagate its own
// cancellable context instead of a fresh context.Background() call
// happening underneath it.
func (s *SettingsService) CheckForUpdates() (UpdateCheckResult, error) {
	return s.checkForUpdates(context.Background())
}

func (s *SettingsService) checkForUpdates(ctx context.Context) (UpdateCheckResult, error) {
	s.mu.Lock()
	s.checking = true
	s.mu.Unlock()
	dataevent.Emit("update-notice", "checking")
	defer func() {
		s.mu.Lock()
		s.checking = false
		s.mu.Unlock()
		dataevent.Emit("update-notice", "check-finished")
	}()
	if fake := os.Getenv(testUpdateFakeVersionEnv); fake != "" {
		if ms, err := strconv.Atoi(os.Getenv(testUpdateCheckDelayEnv)); err == nil && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if os.Getenv(testUpdateCheckFailEnv) != "" {
			err := fmt.Errorf("simulated check failure")
			s.recordCheckOutcome(UpdateCheckOutcomeFailed, err.Error())
			return UpdateCheckResult{}, err
		}
		if os.Getenv(testUpdateUpToDateEnv) != "" {
			s.recordCheckOutcome(UpdateCheckOutcomeUpToDate, "")
			return UpdateCheckResult{UpdateAvailable: false, CurrentVersion: s.AppVersion()}, nil
		}
		s.recordAvailableUpdate(fake)
		s.recordCheckOutcome(UpdateCheckOutcomeFound, "")
		s.triggerAutoDownloadPolicy(fake)
		// Carries the marker + below-the-fold text so e2e proves the trim
		// through the real render path.
		fakeNotes := trimReleaseNotesForApp("## What's new\n\n- Fake note one\n- Fake note two\n\n" + inAppNotesEndMarker + "\n## Manual install\nxattr slop that must never render in-app")
		s.recordUpdateNotes(fakeNoteEntries(fake, fakeNotes), false)
		return UpdateCheckResult{
			UpdateAvailable: true,
			Version:         fake,
			CurrentVersion:  s.AppVersion(),
			Notes:           fakeNotes,
		}, nil
	}
	s.mu.Lock()
	u := s.updater
	s.mu.Unlock()
	if u == nil {
		err := fmt.Errorf("updater not configured")
		s.recordCheckOutcome(UpdateCheckOutcomeFailed, err.Error())
		return UpdateCheckResult{}, err
	}
	rel, err := u.Check(ctx)
	if err != nil {
		sanitized := sanitizeUpdaterError(err)
		s.recordCheckOutcome(UpdateCheckOutcomeFailed, sanitized.Error())
		return UpdateCheckResult{}, sanitized
	}
	if rel == nil {
		s.recordCheckOutcome(UpdateCheckOutcomeUpToDate, "")
		return UpdateCheckResult{UpdateAvailable: false, CurrentVersion: s.AppVersion()}, nil
	}
	s.recordAvailableUpdate(rel.Version)
	s.recordCheckOutcome(UpdateCheckOutcomeFound, "")
	s.triggerAutoDownloadPolicy(rel.Version)
	notes := trimReleaseNotesForApp(rel.Notes)
	entries, truncated := s.buildUpdateNoteEntries(ctx, u.CurrentVersion(), rel)
	s.recordUpdateNotes(entries, truncated)
	return UpdateCheckResult{UpdateAvailable: true, Version: rel.Version, CurrentVersion: s.AppVersion(), Notes: notes}, nil
}

// DownloadAndInstallUpdate downloads the newest release asset,
// verifies it against the published SHA256SUMS digest (wails/v3's own
// fail-closed verification -- a mismatch is refused, nothing
// installed), and stages it over the running app bundle.
// Channel-gated server-side, not just by the UI: a source-channel
// build must never binary-swap itself, since "source" means the
// running copy IS the update mechanism (pull + rebuild).
//
// goal 0100: a beta install swaps its running app over real, primary
// data, so the swap never proceeds without a fresh restore point. The
// backupRunner seam is called first and its error aborts the update
// outright -- no download, no swap -- same fail-closed posture as the
// digest check below it.
func (s *SettingsService) DownloadAndInstallUpdate() error {
	s.mu.Lock()
	if s.updateDownloading {
		s.mu.Unlock()
		return usererror.New(updateInProgressCode, updateInProgressMessage)
	}
	s.updateDownloading = true
	s.resignWarning = ""
	s.lastInstallError = ""
	s.lastInstallStage = ""
	// Captured now, before any later CheckForUpdates tick can overwrite
	// availableUpdate mid-flight -- this is the version this specific
	// call is staging, recorded on success so a repeat sighting of the
	// same version (goal 0175's auto-download policy) can skip it.
	version := s.availableUpdate
	// wasReady distinguishes a supersede attempt (a newer version
	// found while an earlier one already sat staged-and-ready) from a
	// fresh install -- failInstall below needs it to decide whether a
	// failure destroyed the previously-ready build.
	wasReady := s.updateReady
	s.mu.Unlock()
	dataevent.Emit("update-notice", "downloading")
	defer func() {
		s.mu.Lock()
		s.updateDownloading = false
		s.mu.Unlock()
		dataevent.Emit("update-notice", "download-finished")
	}()
	channel := s.UpdateChannel()
	if channel != "release" && channel != "beta" {
		cause := fmt.Errorf("updates only install on the release or beta channel -- this copy was built from source")
		return s.failInstall(wasReady, false, UpdateFailureStageUnknown, cause,
			usererror.Wrap(updateChannelUnavailableCode, updateChannelUnavailableMessage, cause))
	}
	if fake := os.Getenv(testUpdateFakeVersionEnv); fake != "" {
		if ms, err := strconv.Atoi(os.Getenv(testUpdateDownloadDelayEnv)); err == nil && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		// Download-shaped on purpose: fake mode stands in for the real
		// blocked-network case (a corporate proxy answering the asset
		// fetch with 403/timeout), so its error must classify the same
		// way a genuine download failure does.
		cause := fmt.Errorf("github: download: no release asset in test mode")
		return s.failInstall(wasReady, false, UpdateFailureStageDownload, cause,
			updaterBoundaryError(UpdateFailureStageDownload, cause))
	}
	s.mu.Lock()
	backupRunner := s.backupRunner
	u := s.updater
	s.mu.Unlock()

	if backupRunner == nil {
		cause := fmt.Errorf("update aborted: no pre-update backup available")
		return s.failInstall(wasReady, false, UpdateFailureStageBackup, cause,
			usererror.Wrap(updateBackupFailedCode, updateBackupFailedMessage, cause))
	}
	if _, err := backupRunner(0); err != nil {
		cause := fmt.Errorf("update aborted: pre-update backup failed: %w", err)
		return s.failInstall(wasReady, false, UpdateFailureStageBackup, cause,
			usererror.Wrap(updateBackupFailedCode, updateBackupFailedMessage, cause))
	}

	if u == nil {
		cause := fmt.Errorf("updater not configured")
		return s.failInstall(wasReady, false, UpdateFailureStageUnknown, cause,
			usererror.Wrap(updaterUnavailableCode, updaterUnavailableMessage, cause))
	}
	finalVersion, err := s.downloadWithStaleAssetRetry(u, version)
	if err != nil {
		// u.DownloadAndInstall's FIRST action is discardStaging(),
		// unconditionally removing whatever this *updater.Updater
		// already had staged, before the new download even begins
		// (wails/v3 pkg/updater, confirmed against its own source) --
		// so from here on, a previously-ready build is genuinely gone
		// on disk regardless of this call's own outcome, and
		// failInstall(true) below reflects that instead of continuing
		// to claim it's still ready.
		cause := sanitizeUpdaterError(err)
		stage := classifyUpdateFailureStage(cause)
		return s.failInstall(wasReady, true, stage, cause, updaterBoundaryError(stage, cause))
	}
	// u.DownloadAndInstall already verified the staged download
	// against the published SHA256 digest before returning -- signing
	// only ever runs on that already-verified copy, never before or
	// in place of the digest check (goal 0158's binding order:
	// verify, then re-sign, then the swap RestartApp triggers).
	s.resignStagedBundle(u.DownloadedPath())
	s.mu.Lock()
	s.stagedUpdateVersion = finalVersion
	s.mu.Unlock()
	s.markUpdateReady()
	return nil
}

// failInstall records update state and returns the projected boundary error.
//
// destroyedPriorStaging is true only for the one failure path past
// u.DownloadAndInstall itself -- every earlier failure (channel gate,
// backup) never touches the adopted updater's own staging, so a
// previously-ready build stays genuinely ready through those. When it
// IS true and wasReady was true, the previously-ready build is
// actually gone (discardStaging already ran) -- Mill's own bookkeeping
// is cleared to match, so Ready never keeps claiming a build that no
// longer exists on disk (RestartApp would otherwise fail with no
// explanation once the user finally clicks it). Either way, a
// wasReady failure surfaces through the existing check-error line
// ("a newer update couldn't download") rather than the fresh-install
// error path, since the story from the user's side is the same
// regardless of which failure destroyed the in-flight download.
func (s *SettingsService) failInstall(wasReady, destroyedPriorStaging bool, stage UpdateFailureStage, cause, boundaryErr error) error {
	s.mu.Lock()
	s.lastInstallError = cause.Error()
	s.lastInstallStage = stage
	s.mu.Unlock()
	if wasReady {
		if destroyedPriorStaging {
			s.mu.Lock()
			s.updateReady = false
			s.stagedUpdateVersion = ""
			s.mu.Unlock()
		}
		s.recordCheckOutcome(UpdateCheckOutcomeFailed, "a newer update couldn't download: "+cause.Error())
		return boundaryErr
	}
	return boundaryErr
}

// inAppNotesEndMarker splits a release body's two audiences (goal
// 0127): what the in-app update card shows ends here; the GitHub
// releases page's manual-install instructions live below it --
// nonsense inside an app about to update itself.
const inAppNotesEndMarker = "<!-- in-app-notes-end -->"

// trimReleaseNotesForApp returns only the in-app-facing portion of a
// release body. Bodies without the marker (older releases) pass
// through unchanged; the leading "## What's new" heading is dropped
// since the card supplies its own.
func trimReleaseNotesForApp(body string) string {
	if i := strings.Index(body, inAppNotesEndMarker); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "## What's new")
	return strings.TrimSpace(body)
}

// AppDiagnostics is the copyable root-cause context appended to every
// copyable failure surface across the app (goal 0127: a paste replaces
// a photo) -- version, channel, proxy mode, OS/arch. Proxy reports MODE
// and host only -- a proxy URL may carry credentials, which must
// never enter a paste buffer.
func (s *SettingsService) AppDiagnostics() string {
	proxy := "auto (system)"
	switch raw := s.OutboundProxyURL(); {
	case raw == proxyModeOff:
		proxy = "off (direct)"
	case raw != "":
		if u, err := url.Parse(raw); err == nil {
			proxy = "manual: " + u.Host
		} else {
			proxy = "manual (unparseable)"
		}
	case os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "":
		proxy = "auto (environment)"
	}
	return fmt.Sprintf("Mill %s · channel %s · proxy %s · %s/%s",
		s.AppVersion(), s.UpdateChannel(), proxy, runtime.GOOS, runtime.GOARCH)
}
