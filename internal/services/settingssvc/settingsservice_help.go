package settingssvc

import (
	"github.com/alicoding/mill/internal/adapters/osopen"
	"github.com/alicoding/mill/internal/adapters/windowing"
)

// issueTrackerURL is where "Report an issue" lands. The only outbound
// request either method here can cause, and only ever because the user
// chose the action -- the constraint in docs/SPEC.md §1.1 forbids
// unprompted outbound traffic, not a link the user clicked.
const issueTrackerURL = "https://github.com/millhq/mill/issues/new"

// ReportIssue opens the issue tracker in the user's default browser.
func (s *SettingsService) ReportIssue() error {
	return windowing.OpenURL(issueTrackerURL)
}

// OpenDataFolder opens the directory Mill keeps its settings, database,
// backups and vault in, in the OS file manager -- the same directory
// every default data path is resolved against.
func (s *SettingsService) OpenDataFolder() error {
	return osopen.Open(windowing.ConfigDir())
}
