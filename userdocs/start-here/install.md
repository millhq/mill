---
kind: how-to
---

# Install

Two ways to get Mill running: download a release, or build it yourself.
Either way you end up with one binary — no hosted service, no account.

## From a release (macOS)

1. Download the newest `.zip` from the
   [releases page](https://github.com/millhq/mill/releases) — beta
   releases carry every merged change; stable releases are tagged.
2. Unzip and drag `mill.app` to Applications.
3. First launch: the app is not notarized, so macOS shows "Apple could
   not verify…". Click **Done** (not Move to Trash), then open System
   Settings → Privacy & Security, scroll to the Mill message, and
   choose **Open Anyway** — one time only.
4. From then on, update in-app: Settings → Updates → Check for
   updates → Update now. With automatic checks on, a new release on
   your channel also shows a notification — that notification is
   the "Notify when an update is available" workflow, editable like
   any other. If your network blocks app downloads, set
   Settings → Updates → Outbound proxy, or use the browser-download
   button the app offers on failure.
5. A build you downloaded with a browser (steps 1–3, or the
   fallback button) arrives quarantined by macOS. If the app won't
   open even after Open Anyway, clear the quarantine in Terminal:

   ```
   /usr/bin/xattr -dr com.apple.quarantine /Applications/mill.app
   ```

   Write out `/usr/bin/xattr` in full. If Python's `xattr` is
   installed, it shadows the macOS one on your PATH and has no `-r`
   flag at all.

   In-app updates never need this — only browser downloads do.

## From source

```
git clone https://github.com/millhq/mill.git
cd mill
task install:app
```

Requires Go, Node, and the Wails v3 CLI (`go install
github.com/wailsapp/wails/v3/cmd/wails3@latest`). `task dev` runs a
hot-reloading development copy instead of installing.

A source build updates by pulling and rebuilding — the in-app updater
deliberately refuses to overwrite a copy it didn't install.

## Where your data lives

Everything is local: settings and entities in a JSON settings store,
run history in a SQLite file, both under your user's application
support directory. Settings → Backups snapshots them automatically and
can export everything to one file for another machine.
