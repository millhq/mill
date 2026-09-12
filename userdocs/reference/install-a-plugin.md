---
kind: how-to
---

# Install a plugin

Read the [plugin standard](plugin-standard.md) first.

A plugin adds a new object type to the canvas without rebuilding Mill.
It is a folder holding two files — `manifest.json` (name, version, and
what the plugin is allowed to ask for) and `main.js` (its code) — and
installing one is copying that folder into Mill's plugins folder.

## Installing

**Extensions** is its own page — press ⇧⌘X, or pick it in the sidebar.
It has three tabs: **Installed**, **Browse**, and **Updates**.

Browse lists everything your marketplaces offer that you have not
installed yet, including the examples Mill ships when policy allows
them. While sources load, fail, or return only installed entries,
Browse says which state it is in instead of calling the result empty. Press **Install**
on a row. Mill shows what the extension can do — the hosts it reaches,
whether it writes to your boards, what it adds — and installs it only
after you confirm. The new extension appears under **Installed**;
reload to load it.

On a Mac an organisation manages, Extensions says **Managed by
<organisation>** and a policy file decides what may install and run;
see [Managed extensions](managed-extensions.md).

### Installing from a link or a folder

Not everything lives in a marketplace. Mill also installs from:

- a repository, as `owner/repo` or `owner/repo@v1.2.0`
- a direct address of a `.zip` archive
- a folder on this Mac, for a plugin you are writing yourself

The folder name must match the plugin's id, and a folder you point
Mill at is copied, not linked.

### How much Mill checked

Every installed extension wears one badge, and it says exactly what
was checked:

| Badge | What it means |
| --- | --- |
| **Verified** | Its files match the hash the marketplace published, and a key this Mill trusts signed them. |
| **Hash-pinned** | Its files match the hash the marketplace published. |
| **Unverified** | Nothing checked these files. Mill asks you to acknowledge that before installing. |
| **Dev** | You installed it from a folder on this Mac. |

The badge is on the row, on the extension's page, and on its
**Verification** tab, which also lists what the extension can do — the
same list you saw before installing.

### Marketplaces

A marketplace is any repository or folder with a `.mill/marketplace.json`
file at its root, listing the plugins it offers. Press **Sources** in
the Browse tab to add one: `owner/repo`, a repository address, a direct
address of a `marketplace.json` file, or a folder path.

Sources shows the canonical location, publisher, last successful refresh,
and any current failure. A failed refresh keeps the last usable catalog.
Removing a source removes its registration and cached catalog; extensions
already installed from it stay installed. Re-adding the same name creates
a new source identity, so an older refresh or confirmation cannot change it.

Opening Sources and pressing **Retry** after a local read error do not use
the network. Mill reads a marketplace only when you add it, press **Refresh**,
install from it, or check for updates. It never reaches out on its own.

### Updates

Mill never looks for a newer version on its own. Open **Extensions →
Updates** and press **Check for updates**: Mill re-reads every
marketplace you added, then asks each installed extension's own
source what it offers now — the marketplace entry it came from, a
repository's latest release, or the folder you installed it from.
Only a strictly newer version is listed; a downgrade is never offered.

Each row has **Update**, and **Update all** applies every row at once.
An update goes through the same door the first install did, with the
same badge and the same prompt: an unverified update still asks you to
acknowledge it, so **Update all** leaves those rows for you to press
one by one. The Updates tab shows a count until you have applied them,
and the row's **…** menu offers the same **Update** and **Check for
updates**.

A repository publishes an update as a release whose tag is the
version and whose asset is named `<id>-<version>.zip`; Mill fetches
that asset by name. An extension you copied in by hand has no source
to ask, so it never appears here.

### MCP servers an extension ships

An extension can ship the definition of an MCP server — the command
that starts it and the environment it needs. Its page lists each one
under **Contributions → MCP servers** with **Add to Configure**: one
press creates the MCP Server entity in Configure, ready for a workflow
to call tools on.

A secret the server needs is named by one of the extension's own
secret settings, never written into the extension. Pick the secret on
the extension's **Settings** tab first; the entity is created with a
reference to it, and Mill resolves the value only when the server is
started. If no secret is picked yet, **Add to Configure** tells you
which setting to fill in.

### Installing by hand

Copying a folder into the plugins folder still works. Open
**Extensions → Installed → Open plugins folder**, copy the folder in,
and press **Reload all**.

A plugin that can't load shows exactly why on its page — a missing
file, invalid manifest, or a capability Mill doesn't recognize —
instead of silently doing nothing. A plugin whose manifest sets
`minMillVersion` to a version newer than your Mill is refused the
same visible way: update Mill, then reload.

### What an extension's page shows

Click a row to open it. **Overview** is the plugin's own README;
**Contributions** is what it adds, what it can reach, and what it
catches; **Changelog** is its CHANGELOG; **Verification** is what
checked it and what it can do; **Settings** is whatever it declares.

## Starting a plugin

`mill plugin new <name>` writes a folder holding `manifest.json` and
`main.js`, already named and valid, and prints where to copy it. Add
`--dir <path>` to create it somewhere other than the current folder.

For autocomplete while you write it, install the types:

```
npm i -D github:alicoding/mill#path:frontend/plugin-sdk
```

Then put two lines at the top of `main.js` and annotate `activate`:

```js
// @ts-check
/// <reference types="@alicoding/mill-plugin-sdk" />

/** @param {import('@alicoding/mill-plugin-sdk').MillPluginAPI} api */
export function activate(api) {}
```

Nothing is compiled — the types are read by your editor, and Mill
loads the same plain file either way. Every type is listed in the
[plugin API reference](plugin-api/index.md).

The SDK also publishes `manifest.schema.json`. Point your editor or
manifest-checking tool at that JSON Schema for field completion and structural
errors. Run the loader's checks below for semantic rules and files named by the
manifest.

To preview the ordered manifest migrations available for a source plugin, run:

```sh
mill plugin migrate path/to/your-source-plugin
```

The preview prints the ordered migration IDs and their aggregate RFC 6902 patch,
and writes nothing. Add `--json` for a versioned machine-readable plan with
`formatVersion`, `migrations`, `patch`, `manual`, and `applied`, or `--apply` to
replace `manifest.json` after the full plugin validates.

The current migrations rename deprecated `contributes.settings` to
`contributes.configuration` and namespace a bare lower-camel command ID with
the plugin ID. Command references in declared tools and menus change with the
declaration. Existing JavaScript may keep registering the old bare command: the
host connects it to the exact namespaced manifest declaration centrally.

Mill leaves the source unchanged when both setting keys or both command
identities exist, when the canonical command target collides, when a bare
command needs an authored rename such as `send-again` to `sendAgain`, or when a
reference is ambiguous. The command refuses installed plugin folders and
install receipts; run it only against the source folder you author.

## Reloading one plugin while you work

Each installed plugin's page has a **Reload** button, and the command
palette carries the same action as "Reload <plugin>". It re-reads that
plugin's `main.js` and re-registers everything it contributes — its
tools, views, captures, and commands — without restarting Mill.
Objects already on your boards stay where they are.

Editing a plugin's files changes what you allowed it to run, so the
reload asks you to allow it again on its page first. **Reload all** at
the top of the list is the other half: it restarts Mill's plugin
loading entirely, which is how a folder you just copied in is noticed.

## Turning a plugin off

Each installed plugin has the same switch every built-in extension
has, on its row. Turning it off removes its tool from the tray and
palette; objects it already placed stay on your boards untouched.

## Removing a plugin

On the plugin's page, open the **…** menu and choose **Remove…**. Mill
asks first, then moves the plugin's folder to the Trash — nothing is
deleted, so you can put it back. Objects it created stay on the board
as unknown kinds until it is installed again, and a folder you restore
asks to be allowed again, the way any newly installed plugin does.
Plugins that ship inside Mill have no Remove.

## Before a plugin runs

A plugin you install after Mill first ran with this check waits for
your review: its page in Settings > Extensions states what it can
request, which hosts it can reach, and what it catches, and nothing of
it runs until you click **Allow** there and reload. A notice in the footer tells you
when one is waiting. Plugins that were already installed when the
check arrived keep running; only new arrivals wait.

Mill remembers what you allowed: the plugin's files are fingerprinted
at that moment, and if they change later — an update you copied in,
or an edit — the plugin stops until you look again and allow it once
more. Its page says "Its files changed since you allowed it."

If an update asks for more than you allowed before — a new capability,
a new host, or drawing directly in Mill's window — the plugin waits
for review again even though you already allowed an earlier version.
Its page shows "Now also asks to" above the full list, naming only
what is new, and its Allow button reads "Allow the new permissions."
An update that asks for the same or less keeps running without asking
again.

An administrator can pin which plugins may run at all by writing an
allow-list into Mill's settings file — the key
`settings-plugin-allowlist`, a JSON array of plugin ids, placed the
way device-management tooling places any managed setting. When it is
set, Settings > Extensions reports it and every plugin off the list
shows as blocked on its page, with no switch on its row. The Drawing
plugin built into Mill is exempt.

An administrator can also require signatures: the key
`settings-plugin-signing-keys`, a JSON array of minisign public keys.
With keys pinned, a plugin runs only when its folder holds
`mill-plugin.minisig`, a minisign signature of the folder's content
hash (Export plugin audit shows each plugin's `contentHash`; sign that
string with `minisign -S`). Unsigned plugins show as such and cannot be
turned on.

**Export plugin audit** (Settings > Extensions, or the command
palette) saves one JSON file: every installed plugin with its declared
reach and whether it is allowed and on, every action a plugin asked
Mill to perform within the last day, and every secret a plugin read.

## What a plugin can and cannot do

A plugin draws its own objects and edits their data through Mill.
It is never handed the ability to open network connections, touch
files, or leave the app on its own. When it needs something like
that — opening a web address in your browser, say — it must:

1. **Declare** the capability in its manifest, visible on its
   Extensions row before you ever run it.
2. **Ask** at the moment of use. Every ask runs through your
   guardrail rules: you can allow it, deny it, or leave the default,
   which parks the request in **Review** for your explicit approval.

Undeclared asks are refused outright. Approved actions are performed
by Mill itself, never by the plugin's own code.

## Catching drops and pastes

A plugin can claim the two ways outside content lands on the board:
a file dragged in from your file manager, and content pasted from
another app. Claims are declared in the manifest, so a plugin's
Extensions page shows what it catches before it ever runs:

```json
"contributes": {
  "canvasObjects": [
    { "kind": "bookmark", "pastesURLs": true, "fileExtensions": [".webloc"] }
  ]
}
```

- `fileExtensions` — dropped files with a listed extension land as
  this plugin's object, pointing at the file where it is. Requires
  `source: "file"` on the registered object.
- `pastesURLs` — a web address pasted from any app lands as this
  plugin's object instead of a note. Requires `source: "url"`.

Mill's own built-in shapes always win first — a diagram, image, or
spreadsheet file keeps landing as its built-in object — and anything
no one claims still lands the way it does today. With the Bookmark
example installed, pasting a link from your browser drops a bookmark
right on the board.

When two plugins claim pasted links (Bookmark and the Web clipper both
do), the first one lands and the board's toast offers the other:
"Pasted as Bookmark · Paste as Web clipper instead" re-types that same
object in place, undo included. Settings > Extensions' "Pasted links
become" picks which one lands first; without a choice, plugins take
turns in id order.

## Drawing tools

An extension isn't limited to click-to-place objects: it can register
a DRAG tool that rides the same gesture engine, style picker and live
preview Mill's own drawing tools use — in fact Mill's own pencil,
shape, eraser and laser ARE such an extension (**Drawing**, built into
the app; its row sits under Installed, and a folder named
`mill-drawing` in your extensions folder replaces it).

`api.registerCanvasTool(decl)` is the door. Mill owns every pointer
event and paints the live preview itself, so the tool runs fully
sandboxed: it never reaches the board, and nothing it draws can escape
its own frame. Declare the kind with `"tool": true` beside it in
`contributes.canvasObjects`.

- `onPointer(event, ctx)` — called once per phase, once per frame.
  `event.phase` is `"down"`, `"move"`, `"up"`, `"cancel"` (the drag
  was abandoned) or `"fade"` (a trail still aging out after release).
  `event.point` is the newest sample in BOARD coordinates and
  `event.coalesced` carries the samples folded into the same frame,
  oldest first, so a freehand stroke loses no detail. `event.zoom` is
  the board's current scale — divide by it to keep a trail the same
  thickness however far the board is zoomed out. `event.modifiers`
  carries the keys held; `event.styleValues` the picker's current
  values; `event.target` the object under the pointer, if any.
- `ctx.createDraft({ at, size?, data?, preview? })` — starts the
  in-progress object. `draft.patch(...)` merges into it as often as
  you like: nothing persists, nothing syncs, and nothing lands in the
  undo history until `draft.commit({ select? })`, which places it as
  ONE undo step. `draft.discard()` throws it away and the board is
  exactly as it was.
- `preview` — what Mill draws while the drag is live, instead of
  mounting the object's face on every move. `kind` is `"rect"`,
  `"ellipse"`, `"line"`, `"path"` or `"shapes"`, and `from` names the
  draft key carrying the geometry: `"x,y,width,height"` for a box,
  `"x1,y1,x2,y2"` for a line, an SVG path for `"path"`, and a JSON
  list of parts for `"shapes"` — the one to use when each part carries
  its own paint, like a trail whose points fade independently.
  `fill`, `stroke`, `strokeWidth` and `opacity` each name a key
  holding that value. A draft's `data` is what a commit saves;
  `preview` is drawing state that is never saved, and the declaration
  reads across both.
- `styleFields` — the tool's styleable properties (`color`,
  `color-or-none`, `stroke-width`, and `shape-kind`, an icon-button
  picker whose options name their icons from the same named glyph set
  as `icon`), each with its own options, default and optional `label`.
  Declaring any renders Mill's style picker next to the armed tool
  automatically.
- `ephemeral: true` — the drag draws a trail and places nothing (a
  laser pointer, an eraser). Add `fadeMs` and the trail keeps aging
  out after release, one `"fade"` frame at a time.
- Identity — `icon` takes an emoji or a named glyph (`pencil`, `zap`,
  `trash`, `diamond`, `square`, `circle`, `arrow-up-right`);
  `cursor` sets the pointer shape while the tool is armed;
  `shortcutKey` gives it a bare-letter shortcut and tray key chip;
  `group: "annotate"` files its button into the tray's Annotate
  drawer; `objectKind` lets the saved object kind differ from the tool
  id (the pencil places `ink` objects); `dragBand: false` drops the
  drag-handle band when the object's whole body already drags. Drag
  tools stay armed across strokes by default (`sticky: false` opts
  out, and a non-sticky tool may add `lockable: true` so re-clicking
  its armed button locks it for deliberate repetition).
- `api.files.saveImageBytes(base64, ext, title)` bakes drawn bytes
  into a Mill-owned file, ready to use as a file-backed object's
  payload — how the pencil turns a finished stroke into a picture.
- `api.measure(markup, maxWidth)` lays markup out off the board at
  real pixel size and answers `{ width, height }`, for a face whose
  own layout depends on how big its content turned out.
- Erasing — a tool that erases instead of creating declares the
  `erase-board-items` capability in its manifest. Its ctx then carries
  `eraseAt(point)` (accumulates whatever board item sits under the
  point) and `commitErase()` (erases the accumulated set through the
  same undoable quick-delete a Delete key press uses — one undo step
  per pass). What was hit stays on Mill's side; the extension never
  sees item identities.

The object's own face is an entry page: name it as `"entry"` beside
the kind, the same way any other framed face is declared. A tool that
places nothing needs none.

## Settings

A plugin declares its own settings in the manifest, and Mill does the
rest: the controls render on the plugin's page under Installed, the
values are stored centrally, and the plugin reads them
back through `api.settings`. A plugin never builds a settings screen.

```json
"contributes": {
  "settings": [
    { "key": "titleStyle", "type": "enum", "label": "Title",
      "description": "What a bookmark shows as its title.",
      "default": "hostname",
      "options": [
        { "value": "hostname", "label": "Site name" },
        { "value": "address", "label": "Full address" }
      ] },
    { "key": "placeholderTitle", "type": "string",
      "label": "Title before an address", "default": "Bookmark" }
  ]
}
```

Six types: `boolean` (a checkbox), `string` (a text field),
`number` (a number field, with optional `min` and `max`), `enum`
(a dropdown over `options`), `secretRef` (a picker over the
vault's entries — see below), and `entityRef` (a picker over a
Configure entity — see below). `default` is the value in effect until
the user changes the control; a mistyped manifest — a default of the
wrong type, an enum default missing from its options, a default on a
`secretRef` or `entityRef` — blocks the plugin from loading and names
the key on its page.

A `secretRef` setting names a credential without ever holding it:

```json
{ "key": "auth", "type": "secretRef", "label": "Authorization",
  "description": "Sent as a bearer token with every request." }
```

The user picks one of their vault entries in the plugin's row; the
stored value is a reference, and `api.settings.get('auth')` answers
the entry's title (or an empty string when nothing is picked). The
value itself only ever travels inside `api.fetch` — see Reaching the
network. A picked entry that is later deleted shows "This secret no
longer exists. Pick another." on its page, and a request naming it is
refused with the same words.

An `entityRef` setting points at a Configure entity — an Integration,
a List, an MCP server, and the rest of the kinds a workflow node's own
reference field can point at — through `entityKind`:

```json
{ "key": "integrationId", "type": "entityRef", "entityKind": "request",
  "label": "Integration",
  "description": "Which Integration this view reads from." }
```

The user picks it from the same live picker a workflow node's own
reference field uses; the stored value is the entity's id, and
`api.settings.get('integrationId')` answers that id back — never a
label, since the plugin already has the doors that resolve one.
Deleting an entity a plugin still has picked is refused, naming the
plugin.

```js
export function activate(api) {
  const style = api.settings.get('titleStyle')        // 'hostname' | 'address'
  const stop = api.settings.onChange('titleStyle', (next) => {
    // Redraw whatever depends on it -- a face only re-renders on its
    // own data changes, so this is the door for a settings change.
  })
}
```

Commands a plugin registers can carry `enabled: () => boolean`, the
same state check built-in commands use; a disabled command is left
out of the palette rather than shown doing nothing. A default
keyboard shortcut is not something a plugin ships — the user assigns
one under Keyboard shortcuts.

## Notices

A plugin tells the user something through Mill's own notice pill in
the footer, labelled with the plugin's name. Info and success notices
leave on their own after a few seconds; warnings and errors stay until
dismissed. A user-started action that fails should always say so here,
never only in the console.

```js
const dismiss = api.notify({ level: 'error', text: 'Could not save the bookmark address.' })
// Optional: a secondary link running one of this plugin's own commands.
api.notify({ text: 'Imported 12 rows.', action: { label: 'Show', commandId: 'showImport' } })
```

## Storage

A plugin keeps its own state in `api.storage`, saved centrally under
the plugin's id: any JSON value, read synchronously, written through
on `set`. Nothing else in Mill reads it. The Drawing plugin uses it to
remember the last-used pencil and shape style across restarts.

```js
const saved = api.storage.get('pencil') || {}          // undefined when never set
await api.storage.set('pencil', { color, size })
await api.storage.delete('pencil')
api.storage.keys()                                      // ['pencil', …]
```
## Reading the board

A plugin lists what is on the board through `api.query`, and hears
about changes through `api.on`. Every entry carries the name a person
sees it by: a card's title, a note's first line, an object's title or
kind. Cards, notes, and every object kind — built-in or another
plugin's — come back the same shape.

```js
const notes = await api.query({ kind: 'note' })        // [{ id, kind, title, parentId, position, size, payload }]
const everything = await api.query({})
const children = await api.query({ parentId: someCardId })

const stop = api.on('contents:changed', ({ id }) => {
  // Something on the board was created, edited, moved, or deleted.
  // A face only re-renders on its own data, so re-list here.
})
```

The Board index example (`examples/plugins/mill-index`) is exactly
this: one object whose face lists the board by kind and re-renders
on every change.

## Reaching the network

A plugin never opens a connection itself. It declares the hosts it
needs in the manifest, asks Mill to fetch, and Mill decides through
your guardrail rules — allow, ask you first, or deny — the same way it
decides an agent's write. Approved requests run inside Mill, confined
to the declared host on every hop (a redirect elsewhere is refused),
and the response comes back to the plugin. A host or method the
manifest does not declare is refused before any rule runs.

```json
"capabilities": ["fetch"],
"contributes": {
  "network": [
    { "host": "api.example.com" },
    { "host": "hooks.example.com", "methods": ["GET", "POST"] }
  ]
}
```

An entry without `methods` allows GET only. Responses are capped at
4 MB. A plugin whose hosts are typed by the user — a request tester,
say — declares `{ "host": "*" }`: every request to a host not
otherwise declared then asks you first, every time, and no rule can
make it silent. The Extensions row says so before the plugin runs.

```js
const res = await api.fetch('https://api.example.com/issues?open=1')
if (res.approved) console.log(res.status, res.body)   // headers in res.headers
else api.notify({ level: 'warning', text: 'Not allowed' + (res.ruleLabel ? ' (' + res.ruleLabel + ')' : '') })
```

Credentials belong in the vault, not in plugin code. A request that
needs one names a `secretRef` setting, and Mill attaches the entry's
value itself — after you approve the request — as a header:

```js
const res = await api.fetch('https://api.example.com/me', {
  secret: { settingKey: 'auth' }                 // Authorization: Bearer <value>
  // or: secret: { settingKey: 'auth', header: 'X-Api-Key', prefix: '' }
})
```

Every request that carries a secret asks you first, whatever your
other rules say: the Review row reads "GET api.example.com · uses
secret ‘Jira PAT’", and the vault's access history records the read
as sent by the extension. The value is redacted from the response
before the plugin sees it — a server echoing the token back gets
`[redacted]`.

## Opening a path in another app, and listing a folder

Two more guarded doors, both declared as capabilities:

- `open-app` — `ctx.requestGuardedAction('open-app', { app: 'Bruno', path: '/abs/folder' }, 'Open the collection in Bruno')` hands a local path to a named application through the OS's own open-with. It asks like every guarded action (Review shows the plugin's name) and, once approved, opens the app.
- `list-files` — `api.files.list('/abs/folder')` returns the folder's direct children (`{ name, path, isDir, size }`), hidden entries and dependency folders left out. It is a read: allowed unless one of your rules denies or parks it, and audited either way; `entries` is empty when it was not approved.

The Bruno collection example uses both: its face lists the collection's requests and offers "Open in Bruno".

## Writing to the board

A plugin creates notes and cards, updates cards, and adds rows to a
List through the same guarded door an agent uses: each write goes to
your guardrail rules — allow, ask you first in Review, or deny — with
the plugin named as the source, and lands with its own undo history.
The manifest declares the capability; without it every write is
refused before any rule runs.

```json
"capabilities": ["write-content"]
```

```js
const note = await api.content.createNote({ text: 'Call the bank\ntomorrow', parentId })
const card = await api.content.createCard({ kindId, title: 'Acme', fields: { status: 'active' } })
await api.content.updateCard(card.id, { note: 'Renewal due in March' })
await api.content.appendListRow(listId, { vendor: 'Acme', tier: 'gold' })
const list = await api.content.createList({ title: 'Vendors', columns: [{ name: 'Vendor' }, { name: 'Tier', type: 'text' }], rows: [{ Vendor: 'Acme', Tier: 'gold' }] })
if (!note.approved) api.notify({ level: 'warning', text: 'Not allowed' + (note.ruleLabel ? ' (' + note.ruleLabel + ')' : '') })
```

A note without a position lands just right of the last item in its
parent. A denied write resolves with `approved: false`; an approved
one carries the new entity's `id`. `createList` takes columns by
display name with an optional type (`text`, `number`, `integer`,
`boolean`, `date`, `datetime`; text when omitted) and first rows keyed
by column name.

## Workflow steps

A plugin can add steps to the workflow palette. Declare them in the
manifest and implement them in a `steps.js` next to `main.js`:

```json
"contributes": {
  "steps": [
    { "id": "text-case", "label": "Text case", "description": "Changes the text's case.",
      "config": [ { "key": "mode", "label": "Mode", "type": "options", "options": ["upper", "lower", "title"], "default": "upper" } ] }
  ]
}
```

```js
// steps.js -- plain script, no imports or exports
registerStep('text-case', {
  perform: function (input) {
    // input.payload: the text arriving from the previous step
    // input.config: this step's authored fields (input.config.mode)
    // input.attributes: the run's attribute values
    return input.payload.toUpperCase()          // or { payload, attributes }
  },
})
```

`steps.js` runs inside Mill's workflow engine, not in the window: a
step works in a scheduled or headless run exactly as it does from the
editor, and Try this step in the Inspector runs the same function. It
sees only its input — no network, no files, no other plugin — and a
step that runs longer than a few seconds fails the run instead of
hanging it. Config fields are text or a fixed option list. The step
appears in the palette under Transform as "<label>", and the
Extensions row lists "Adds workflow steps". The **Text case** example
(`examples/plugins/mill-textcase`) is the whole pattern in one file.

## Secret sources

A plugin can turn a store of credentials on your machine into secrets
Mill offers everywhere a secret is asked for. Declare each store in the
manifest and implement it in a `secrets.js` next to `main.js`:

```json
"capabilities": ["read-file"],
"contributes": {
  "secretSources": [
    { "id": "netrc", "label": "Netrc file",
      "path": { "kind": "file", "label": "File", "placeholder": "~/.netrc", "default": "~/.netrc" },
      "capabilities": ["list", "resolve"] }
  ]
}
```

```js
// secrets.js -- plain script, no imports or exports
registerSource('netrc', {
  list: function (ctx) {
    // ctx.path: the file the user configured; ctx.readFile() its bytes
    return namesIn(ctx.readFile())            // names only, never a value
  },
  resolve: function (ctx, key) {
    return valueFor(key, ctx.readFile())      // one value, at the moment of use
  },
})
```

`secrets.js` runs on Mill's own side, never in the window, and a source
reaches nothing but the file — or, for a `"folder"` path, the folder —
the user pointed it at. `list` returns names; `resolve` returns one
value, which Mill applies itself through the same gate and access
history every other secret passes. The value never returns to the
plugin, and nothing is copied into Mill's vault.

Add the source under **Secrets › Sources**: its label appears in the
Kind picker, and the path field renders with the label, placeholder and
default the manifest declares. A source whose extension is removed or
turned off says so in its own row instead of listing nothing. Two
optional capabilities go alongside: `"discover"` offers stores found
under a configured folder, and `"import"` reads several names at once.
The **Netrc file** example (`examples/plugins/netrc-secrets`) is the
whole pattern in one file.

## Captures

A plugin can offer a quick capture: a small face that opens in its own
floating window from the Quick Panel or the command palette, away from
the canvas, and lands what the user writes where they choose. Declare
it in the manifest and register the face:

```json
"contributes": { "captures": [ { "id": "thought", "label": "Thought", "description": "A one-line thought." } ] }
```

```js
api.registerCapture({
  id: 'thought',
  render(el, ctx) {
    // Draw the face into el. ctx.destinationId is the card the user
    // chose in the window's header ("" for the top level) -- pass it
    // as parentId to a content door, then call ctx.done().
    // ctx.cancel() closes without writing.
  },
})
```

The Quick Panel lists "New <label>…" straight off the manifest, so the
row is there before any plugin code runs; the capture window loads the
plugin and calls `render`. Writes go through the same guarded content
doors as everywhere else. Mill's own note is the first capture (the
"New note…" row); the destination is remembered per capture.

## Views

A plugin can own a work tab — the same strip a workflow editor opens
in. Declare the view in the manifest (its title is the tab's label,
and the Extensions row lists it), register how to draw it when the
plugin activates, and it appears in the command palette under that
title. The panel keeps what you drew while another tab is in front;
after Mill restarts, the tab comes back and draws again.

```json
"contributes": { "views": [{ "id": "issues", "title": "Issues" }] }
```

```js
export function activate(api) {
  api.registerView({ id: 'issues', render(el, ctx) {
    el.replaceChildren()            // plain DOM, same as renderFace
    // ... list issues here; api.query / api.fetch / api.storage all work
  } })
}
```

Your own commands can open it too: run the registry command
`view.open.<plugin id>.issues`.

## Context-menu items

A plugin can add items to the right-click menu of its own objects.
Each item acts on the object that was clicked and receives the same
context the face gets; an item can say when it applies, and Mill leaves
it out of the menu until then. Other kinds of object never show it.

```js
api.registerCanvasObject({
  kind: 'bookmark',
  menuItems: [
    { id: 'open', label: 'Open in browser',
      enabled: (ctx) => !!ctx.object.Payload.url,
      run: (ctx) => ctx.requestGuardedAction('open-url', { url: ctx.object.Payload.url }, 'Open the bookmark') },
  ],
  // ...
})
```

## The example plugins

Mill's repository ships seven working examples: **Bookmark**
(`examples/plugins/mill-bookmark`) — a web address pinned to the
board, edited in place, opened through a guarded ask, with two
declared settings — **Scribble** (`examples/plugins/mill-scribble`) —
a freehand drawing tool exercising the drag interaction, style fields,
and live preview above — and **Board index**
(`examples/plugins/mill-index`) — a live listing of the board through
`api.query` and `api.on` — and **Request tester**
(`examples/plugins/mill-request-tester`), a real tool on nothing but
the doors: a work tab, any-host guarded fetch, a storage-backed
history, and a declared setting — and **Mind map**
(`examples/plugins/mill-markmap`), a view over a note's headings that
follows the note as it changes, its rendering engine vendored as one
committed bundle (`scripts/vendor-markmap.sh`) so it never loads
anything from the network — and **Web clipper**
(`examples/plugins/mill-clipper`), which fetches a page through the
guarded network door, extracts the article with Mozilla's Readability
(vendored the same way), converts it through the SDK's convert door
(`api.convert.htmlToMarkdown`), and saves it as a note through the
guarded content door — and **Netrc file**
(`examples/plugins/netrc-secrets`), which turns the machines in a
`.netrc` file into secrets Mill can reference. Copy any folder into
your plugins folder to try it, or use it as the starting point for
your own.

## Integrating a real tool

Mill never rebuilds a tool you already use; it puts the tool's files
and its command line on the board and in workflows. The pattern, with
Bruno (an API client) as the worked example:

1. **Find the tool's own seams.** Bruno keeps a collection as a folder
   of `.bru` files with a `bruno.json`, and its CLI runs one with
   `bru run --reporter-json`. Files and a CLI are exactly what Mill
   integrates through — a file-backed object kind and a shell step.
2. **Place the artifact as an object.** The Bruno collection example
   (`examples/plugins/mill-bruno`) registers a file-backed kind over
   `bruno.json`: the face names the collection, lists its requests
   through the files door, and offers "Open in Bruno" through the
   open-app door. Editing stays in Bruno.
3. **Run it as a workflow.** The seeded "Example: Run a Bruno
   collection" runs the CLI on an execution environment, reads the JSON
   report it wrote, and lands the results as rows of the seeded "Bruno
   results" List — guarded and audited like every command Mill runs.
4. **Keep secrets in the tool's own store.** Bruno reads a `.env` at
   the collection root; point Secrets > Sources at that file
   and the keys appear in every secret picker without a copy.

Nothing here is Bruno-specific in the platform: the same four moves
fit any tool with files and a command line.

## Checking a plugin

Two commands run the same checks Mill's own examples pass:

```sh
go run ./internal/pluginconform path/to/your-plugin   # the loader's rules, ahead of time
cd frontend && npm run plugin:conform                 # activates every example against a recording host
```

The first refuses what the loader would refuse — id and folder name,
capabilities, contributions, file types the plugin route serves, a
symlink leaving the folder. The second activates each plugin against a
recording stand-in for `api` and checks that every object, view, and
setting it registers or reads is declared in its manifest.

## Writing one

A plugin's `main.js` is a plain JavaScript module — no build step —
exporting one function:

```js
export function activate(api) {
  api.registerCanvasObject({
    kind: 'my-thing',
    label: 'My thing',
    icon: '⭐',
    source: 'board-local',
    editRoute: 'inline', // or 'external-app' | 'none', or (object) => one of them
    defaultPayload: {},
    renderFace(el, ctx) {
      // Draw into el with plain DOM. ctx.object holds the data;
      // ctx.updatePayload(patch) saves changes (undo included);
      // ctx.requestGuardedAction(kind, attrs, description) asks Mill
      // to act on the plugin's behalf.
    },
  })
}
```

`el` is scaled with the board: zoom out and every pixel inside it
shrinks. Plain DOM does not care, but a rendering engine that measures
its own labels or fits a layout from screen rectangles (a mind map, a
graph layout, a text-measuring chart) lays out wrong inside a scaled
box. For those, `ctx.mountOffBoard(element, { w, h })` parks your
element off the board at exactly that size, unscaled; render there,
copy the finished drawing into `el`, then call the detach it returned.
The Mind map example does exactly this on every repaint, and Mill
detaches anything you left mounted when the object leaves the board.

## Tools: make it reachable by an agent

Anything your plugin already built can be declared as a tool, and an
agent connected over MCP can then call it. Declare it in
`manifest.json` under `contributes.tools`:

```json
"tools": [
  {
    "name": "change_text_case",
    "description": "Changes the case of text: upper, lower or title.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "text": { "type": "string" },
        "mode": { "type": "string", "enum": ["upper", "lower", "title"] }
      },
      "required": ["text", "mode"]
    },
    "effect": "read",
    "run": { "kind": "step", "stepId": "text-case" }
  }
]
```

- **`name`** is `verb_noun` in lowercase. The agent sees it as
  `plugin_<your-plugin-id>_<name>`.
- **`description`** is one sentence, 200 characters or fewer. An agent
  reads every description before choosing, so keep it about what the
  tool does.
- **`inputSchema`** is your own JSON Schema, passed to the agent
  untouched.
- **`effect`** is `read` (answers straight away) or `write` (needs the
  Settings toggle and parks for the person's approval).
- **`run`** says what it runs:
  - `{"kind": "step", "stepId": "..."}` runs one of your declared
    steps. Its `text` argument is the step's input; every other
    argument names one of that step's config fields.
  - `{"kind": "command", "commandId": "..."}` runs one of your
    registered commands. A command takes no arguments, so a
    command-kind tool declares none.
  - `{"kind": "query"}` lists the board's contents, filtered by the
    optional `kind` and `parentId` arguments.

A command a tool names must also be declared, its id namespaced
`<your plugin id>.<verb>`:

```json
"commands": [{ "id": "board-index.refresh", "label": "Refresh the board index" }]
```

`api.registerCommand` still works for a command you never declared —
declaring is what makes it reachable by an agent, and Mill logs one
warning for an undeclared id.

A tool contributes nothing while your plugin is turned off, and a
write-effect tool never skips the person's approval. See
[What plugins expose to agents](../agents/plugins.md) for the agent's
side of this.

## Scheduled and background work

There is no timer or alarm API. Work that should happen on a schedule,
on an event, or while no window is open is a workflow: ship a
workflow that uses your step or object, and it runs, pauses for
approval, and shows in Activity like anything a user builds. Your
plugin can open or reference it, and a user can edit it.

The full contract — every field, every capability, and what stays
stable between versions — is in [Extending the canvas](extending-the-canvas.md),
and every type is listed in the [plugin API reference](plugin-api/index.md).
