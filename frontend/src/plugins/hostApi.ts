import { Events } from '@wailsio/runtime'
import { PluginService } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc'
import { AtlasService } from '../../bindings/github.com/alicoding/mill/internal/services/atlassvc'
import { contentEntryFromWire } from './pluginQuery'
import { attachPluginViewMessages, collectPluginView, getPluginView } from './pluginViews'
import { attachPluginCaptureMessages, collectPluginCapture, getPluginCapture } from './pluginCaptures'
import { SettingsService } from '../shared/bindings'
import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import { useUISignalStore } from '../shared/uiSignalStore'
import { collectPluginCommand } from './pluginCommands'
import { buildThirdPartyNoun, seatCanvasTool } from './canvasToolAdapter'
import { registerLocalCanvasObjectFace, registerLocalCanvasTool } from './canvasToolLocal'
import { measureMarkup } from './canvasMeasure'
import { parseObjectMeasure } from './canvasToolProtocol'
import { settingDeclsFromManifest } from './pluginSettings'
import { secretTitleOf } from '../shared/secretTitleCache'
import { buildPluginStorage } from './pluginStorage'
import { settingsPluginStorageDoors } from './pluginStorageHostDoors'
import { pluginContextWriter } from './pluginContextKeys'
import { buildFetchJSON } from './pluginFetchJSON'
import { buildElement } from './pluginElementBuilder'
import { formatPluginDate } from './pluginDateFormat'
import { pushNotice } from '../shared/noticeStore'
import { getExtensionExports } from './extensionExports'
import { resolveExtensionSetting, subscribeExtensionSetting } from '../shared/extensionSettingsStore'
import type { CanvasObjectDecl, CanvasToolDecl, ContentQuery, LifecycleEventPayload, LinkQuery, MillPluginAPI, PluginContextValue, PluginFetchInit, PluginOutputOptions, PluginElAttrs, PluginElChild } from './sdk'
import type { MenuPath } from '../shared/menuSkeleton'
import type { Command } from '../shared/commands'
import { commandHasEnablement, commandIsEnabled } from './pluginCommandEnablement'
import { whenClauseError } from './whenClause'

// buildPluginAPI constructs the ONE object a plugin ever holds
// (docs/adr/0047 §2: capabilities arrive as api calls the host
// mediates, never as importable primitives). Frozen so a plugin
// cannot re-point a sibling's callbacks. Validation here is the
// host-side twin of pluginsvc's manifest validation: registration
// inputs are checked at the door, with the plugin's own id in every
// error so a broken plugin names itself.
const KIND_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/
const SOURCES = new Set(['board-local', 'url', 'file'])
const EDIT_ROUTES = new Set(['inline', 'external-app', 'none'])

type ContentWriteWire = Parameters<typeof PluginService.WriteContentForPlugin>[1]

async function writeContent(pluginId: string, req: Partial<ContentWriteWire>) {
	const r = await PluginService.WriteContentForPlugin(pluginId, {
		op: '', text: '', title: '', note: '', kindId: '', cardId: '', parentId: '', listId: '', fields: {}, values: {}, position: null, description: '', columns: [], rows: [],
		...req,
	} as ContentWriteWire)
	return { approved: r.approved, effect: r.effect, ruleLabel: r.ruleLabel, id: r.id }
}

// warnedCommands keeps the declare-first nudge to once per command id:
// a command registered without a contributes.commands entry still
// works (declaring is required only for a command a manifest tool
// names), so this must not become noise on every reload.
const warnedCommands = new Set<string>()
const warnedEnablements = new Set<string>()

function warnUndeclaredCommand(manifest: Manifest, commandId: string): void {
	if ((manifest.contributes?.commands ?? []).some((c) => c.id === commandId)) return
	const key = `${manifest.id}.${commandId}`
	if (warnedCommands.has(key)) return
	warnedCommands.add(key)
	console.warn(`plugin ${manifest.id}: command "${commandId}" is not declared in the manifest's contributes.commands. Declare it to make it reachable by an agent`)
}

function warnInvalidEnablement(manifest: Manifest, commandId: string): void {
	const expression = manifest.contributes?.commands?.find((command) => command.id === commandId)?.enablement?.trim()
	if (!expression) return
	const problem = whenClauseError(expression)
	if (!problem) return
	const key = `${manifest.id}\u0000${commandId}\u0000${expression}`
	if (warnedEnablements.has(key)) return
	warnedEnablements.add(key)
	console.warn(`plugin ${manifest.id}: command "${commandId}" has invalid enablement: ${problem}`)
}

// The manifest's own contributes.commands[].menu (goal 0335) is the
// seat's declaration; registerCommand's own decl (MillPluginAPI's
// registerCommand parameter, sdk/commands.ts's PluginCommandDecl)
// carries no menu field -- a manifest tool statically reads
// contributes.commands before any plugin code runs, so the seat lives
// there, joined onto the live command here by matching id. Exported
// as its own pure function so the join is testable without the rest
// of buildPluginAPI's side-effecting door set.
export function menuForDeclaredCommand(manifest: Manifest, commandId: string): Command['menu'] {
	const declaredMenu = manifest.contributes?.commands?.find((c) => c.id === commandId)?.menu
	if (!declaredMenu) return undefined
	// declaredMenu.path is a loose string on the wire; pluginsvc's
	// validateCommands already fail-closed it to "workflow" | "atlas" |
	// "help" before this manifest could ever load.
	return { path: declaredMenu.path as MenuPath, group: declaredMenu.group, order: declaredMenu.order }
}

function resolvedCommandID(manifest: Manifest, registeredID: string): string {
	const commands = manifest.contributes?.commands ?? []
	if (commands.some((command) => command.id === registeredID)) return registeredID
	if (registeredID.includes('.')) return registeredID
	const canonical = `${manifest.id}.${registeredID}`
	return commands.some((command) => command.id === canonical) ? canonical : registeredID
}

export function buildPluginAPI(manifest: Manifest, millVersion: string, storageSnapshot: Record<string, string> = {}): MillPluginAPI {
	const pluginId = manifest.id
	const setContext = pluginContextWriter(pluginId)
	const requestGuardedAction = async (kind: string, attributes: Record<string, string>, description: string) => {
		const d = await PluginService.RequestGuardedAction(pluginId, kind, attributes, description)
		return { approved: d.Approved, effect: d.Effect, ruleLabel: d.RuleLabel, performed: d.Performed }
	}
	// evaluateGuardedAction/callIntegration (goal 0374): a pure read
	// (what would this do? / read through the Integration the user
	// picked), never itself a confirmation surface — the inline "ask"
	// banner for external.comment/external.transition is PluginFrame's
	// own guardedWrite control, never this generic api object, since it
	// needs to render outside the sandboxed frame.
	const evaluateGuardedAction = async (kind: string, attributes: Record<string, string>) => {
		const e = await PluginService.EvaluateGuardedActionForPlugin(pluginId, kind, attributes)
		return { effect: e.Effect, ruleLabel: e.RuleLabel }
	}
	const callIntegration = (integrationId: string, path: string, method: string, values: Record<string, string>) =>
		PluginService.CallIntegrationForPlugin(pluginId, integrationId, path, method, values)
	// The settings door (goal 0258 slice 1): declarations come from the
	// validated manifest, values from the same central store the
	// Settings row writes -- one resolver for built-ins and plugins.
	const settingDecls = settingDeclsFromManifest(manifest)
	const declFor = (key: string) => {
		const decl = settingDecls.find((d) => d.key === key)
		if (!decl) throw new Error(`plugin ${pluginId}: setting "${key}" is not declared in the manifest's contributes.configuration`)
		return decl
	}
	const settings = Object.freeze({
		get: (key: string) => {
			const decl = declFor(key)
			const value = resolveExtensionSetting(pluginId, decl)
			// A secretRef answers the entry's title, never its value or id.
			return decl.type === 'secretRef' ? secretTitleOf(String(value)) : value
		},
		onChange: (key: string, fn: (value: boolean | string | number) => void) => subscribeExtensionSetting(pluginId, declFor(key), fn),
	})
	// The notice door (goal 0277): the plugin's display name opens the
	// notice's own text, so the reader recognizes the origin before
	// reading it; an action must be one of the plugin's OWN registered
	// commands, namespaced exactly as registerCommand did.
	const notify = (input: { text: string; level?: 'info' | 'success' | 'warning' | 'error'; action?: { label: string; commandId: string } }) => {
		if (typeof input?.text !== 'string' || input.text.trim() === '') throw new Error(`plugin ${pluginId}: notify needs a non-empty text`)
		return pushNotice({
			text: `${manifest.name || pluginId}: ${input.text}`,
			level: input.level,
			source: pluginId,
			actions: input.action ? [{ label: input.action.label, commandId: `plugin.${pluginId}.${input.action.commandId}` }] : undefined,
		})
	}
	// The network door (goal 0288): the bound call does every check --
	// capability, declared host + method, guardrail -- and executes
	// host-side; this is only the shape adapter. Named so fetchJSON
	// (goal 0386 S1) has a fetch function to wrap.
	const fetchDoor = async (url: string, init: PluginFetchInit = {}) => {
		const r = await PluginService.FetchForPlugin(pluginId, {
			method: init.method ?? 'GET', url, headers: init.headers ?? {}, body: init.body ?? '',
			secret: init.secret ? { settingKey: init.secret.settingKey, header: init.secret.header ?? '', prefix: init.secret.prefix ?? '' } : null,
		})
		const headers: Record<string, string> = {}
		for (const [k, v] of Object.entries(r.headers ?? {})) if (v !== undefined) headers[k] = v
		return { approved: r.approved, effect: r.effect, ruleLabel: r.ruleLabel, status: r.status, headers, body: r.body }
	}
	const api = Object.freeze({
		millVersion,
		pluginId,
		settings,
		// The context-key door (goal 0349 S2c): a plugin's own facts, read
		// back by a declared item's `when` clause as `plugin.<key>`.
		context: Object.freeze({ set: (key: string, value: PluginContextValue) => setContext(key, value) }),
		notify,
		storage: buildPluginStorage(pluginId, storageSnapshot, settingsPluginStorageDoors(pluginId)),
		// The read doors (goal 0278): query is the bound content index
		// (the same Go index the MCP atlas_list_contents tool reads);
		// on('contents:changed') is the existing 'atlas' dataevent every
		// card/note/object mutation already emits.
		query: async (q: ContentQuery = {}) => ((await AtlasService.ListContents(q.kind ?? '', q.parentId ?? '')) ?? []).map(contentEntryFromWire),
		// The kinds door (goal 0357): the same Kinds() index the board's
		// own surfaces read, restated in the SDK's camelCase shape like
		// query above -- a read needs no capability, exactly as query does.
		kinds: async () => ((await AtlasService.Kinds()) ?? []).map((k) => ({
			id: k.ID,
			label: k.Label,
			icon: k.Icon || undefined,
			fields: (k.Fields ?? []).map((f) => ({ key: f.Key, label: f.Label, type: String(f.Type), options: f.Options ?? undefined })),
		})),
		// The links door (goal 0357 S2): an adapter over the same Links()
		// edge list the board's own Matrix/Coverage panes read, filtered
		// here rather than by a new query engine -- q narrows an
		// already-fetched list exactly as query's own kind/parentId do.
		links: async (q: LinkQuery = {}) => ((await AtlasService.Links()) ?? [])
			.filter((l) => (!q.kind || l.LinkKindID === q.kind) && (!q.source || l.FromCardID === q.source) && (!q.target || l.ToCardID === q.target))
			.map((l) => ({ id: l.ID, kind: l.LinkKindID, source: l.FromCardID, target: l.ToCardID })),
		// The link-kinds door (goal 0357 S2): the same LinkKinds() index
		// the board's own panes read, restated like kinds above -- a read
		// needs no capability, exactly as query/kinds do.
		linkKinds: async () => ((await AtlasService.LinkKinds()) ?? []).map((lk) => ({ id: lk.ID, label: lk.Label })),
		// The open door (goal 0357): the store write a projection's own
		// chip click performs (goal 0064's openCardFromProjection) --
		// board view, then the card's page. The store is imported lazily
		// for the loader's import discipline, as collectOpenViewCommand's
		// own run() does.
		open: (cardId: string) => {
			void import('../shared/store').then(({ useAppStore }) => {
				const state = useAppStore.getState()
				if (state.view.kind !== 'atlas') state.setView({ kind: 'atlas' })
				useAppStore.getState().setAtlasBoardView('board')
				useUISignalStore.getState().requestAtlasOpenCard(cardId)
			})
		},
		// The network door (goal 0288): the bound call does every check --
		// capability, declared host + method, guardrail -- and executes
		// host-side; this is only the shape adapter. fetchJSON (goal 0386
		// S1) is pure sugar over this same door, kept as a named const so
		// it has a fetch function to wrap.
		fetch: fetchDoor,
		fetchJSON: buildFetchJSON(fetchDoor),
		// The content-write door (goal 0289): every check and the write
		// itself live host-side (WriteContentForPlugin); these are shape
		// adapters over one bound call.
		content: Object.freeze({
			createNote: (input) => writeContent(pluginId, { op: 'note', text: input.text, parentId: input.parentId ?? '', position: input.position ? { X: input.position.x, Y: input.position.y } : null }),
			createCard: (input) => writeContent(pluginId, { op: 'card', kindId: input.kindId, title: input.title, note: input.note ?? '', fields: input.fields ?? {}, parentId: input.parentId ?? '' }),
			updateCard: (id, patch) => writeContent(pluginId, { op: 'card-update', cardId: id, title: patch.title ?? '', note: patch.note ?? '', fields: patch.fields ?? {} }),
			appendListRow: (listId, values) => writeContent(pluginId, { op: 'list-row', listId, values }),
			createList: (input) => writeContent(pluginId, { op: 'list', title: input.title, description: input.description ?? '', columns: input.columns.map((c) => ({ name: c.name, type: c.type ?? '' })), rows: input.rows ?? [] }),
			// The field-edit door (goal 0357), armed only while the
			// manifest declares "edit-card-fields" -- the same host-side
			// declare-first enforcement erase-board-items takes, with the
			// error naming the missing capability rather than acting on
			// a plugin that never asked for it.
			setCardFields: async (cardId, fields) => {
				if (!(manifest.capabilities ?? []).includes('edit-card-fields')) {
					throw new Error(`plugin ${pluginId}: content.setCardFields needs the "edit-card-fields" capability declared in the manifest`)
				}
				const r = await PluginService.SetCardFieldsForPlugin(pluginId, cardId, fields)
				return { approved: r.approved, effect: r.effect, ruleLabel: r.ruleLabel, id: r.id }
			},
		}),
		// The files door (goal 0310): a folder listing through Mill's
		// read-class evaluation, never the plugin's own filesystem.
		files: Object.freeze({
			list: async (path: string) => {
				const r = await PluginService.ListDirForPlugin(pluginId, path)
				return { approved: r.approved, effect: r.effect, ruleLabel: r.ruleLabel, entries: (r.entries ?? []).map((e) => ({ name: e.name, path: e.path, isDir: e.isDir, size: e.size })) }
			},
			// Baking bytes into Mill's own file store (a drawing tool's
			// "draw, save as SVG, place with the returned path" shape),
			// moved here from the gesture ctx (docs/goals/0380) so a
			// framed tool reaches it through the same door a face does.
			saveImageBytes: (base64: string, ext: string, title: string) => AtlasService.SaveImageBytes(base64, ext, title),
		}),
		// The convert door (goal 0282, reverse direction goal 0386 S1):
		// the shared Markdown<->HTML converters as pure transforms over
		// one bound call each.
		convert: Object.freeze({
			htmlToMarkdown: (html: string) => PluginService.ConvertHTMLToMarkdown(html),
			markdownToHtml: (markdown: string) => PluginService.ConvertMarkdownToHTML(markdown),
		}),
		on: (event, handler, filter) => {
			if (event === 'contents:changed') {
				return Events.On('mill-data-changed', (evt) => {
					const data = evt.data as { entity?: string; id?: string; kind?: string } | undefined
					if (data?.entity !== 'atlas') return
					if (filter?.kinds && !filter.kinds.includes(data.kind ?? '')) return
					handler({ id: data.id ?? '', kind: data.kind } as never)
				})
			}
			// entity.*/object.* (docs/goals/0392 S2): one wire event carries
			// the whole lifecycle family, discriminated by its own `event`
			// field; kinds narrows by entityKind for entity.* and by the
			// object's own kind for object.*.
			if (event === 'entity.*' || event === 'object.*') {
				const prefix = event === 'entity.*' ? 'entity.' : 'object.'
				const kindField = event === 'entity.*' ? 'entityKind' : 'kind'
				return Events.On('mill-lifecycle-event', (evt) => {
					const data = evt.data as LifecycleEventPayload | undefined
					if (!data?.event.startsWith(prefix)) return
					if (filter?.kinds && !filter.kinds.includes(data[kindField] ?? '')) return
					handler(data as never)
				})
			}
			throw new Error(`plugin ${pluginId}: unknown event "${String(event)}"`)
		},
		registerCanvasObject: (decl: CanvasObjectDecl) => {
			if (!KIND_PATTERN.test(decl.kind)) throw new Error(`plugin ${pluginId}: canvas object kind "${decl.kind}" must be a lowercase slug`)
			if (!SOURCES.has(decl.source)) throw new Error(`plugin ${pluginId}: unknown source "${decl.source}"`)
			if (typeof decl.editRoute !== 'function' && !EDIT_ROUTES.has(decl.editRoute)) throw new Error(`plugin ${pluginId}: unknown editRoute "${decl.editRoute}"`)
			seatCanvasTool(pluginId, buildThirdPartyNoun(pluginId, manifest, decl), decl.styleFields ?? [])
		},
		registerCanvasTool: (decl: CanvasToolDecl) => registerLocalCanvasTool(pluginId, manifest, decl),
		registerCanvasObjectFace: (descriptor) => registerLocalCanvasObjectFace(pluginId, manifest, descriptor),
		measure: (markup: string, maxWidth: number) => measureMarkup(pluginId, parseObjectMeasure({ markup, maxWidth })),
		// A plugin view (goal 0290): declared in the manifest, registered
		// here with its render, opened by a registry command. The store is
		// imported lazily inside run() -- at activation time the app module
		// graph must not be pulled forward (loader.ts's import discipline).
		registerView: (decl) => {
			const declared = (manifest.contributes?.views ?? []).find((v) => v.id === decl.id)
			if (!declared) throw new Error(`plugin ${pluginId}: view "${decl.id}" is not declared in the manifest's contributes.views`)
			// A view whose manifest names an entry page is already
			// collected from the manifest (collectFrameSurfaces, run
			// before activation), so registering it is how the plugin
			// opts into the page's message relay, never a second
			// collection.
			if (declared.entry) {
				attachPluginViewMessages(pluginId, decl.id, decl.onMessage)
			} else {
				if (typeof decl.render !== 'function') throw new Error(`plugin ${pluginId}: view "${decl.id}" needs a render function, or an entry page in the manifest`)
				collectPluginView({ pluginId, pluginName: manifest.name || pluginId, viewId: decl.id, title: declared.title, version: manifest.version, render: decl.render, onMessage: decl.onMessage, placement: declared.placement || 'tab', icon: manifest.icon || undefined })
				// A board-switcher-placed render view opens through the
				// switcher's own command, exactly as an entry page with
				// that placement does (goal 0357).
				if (declared.placement === 'board-switcher') {
					collectBoardSwitcherViewCommand(pluginId, decl.id, declared.title)
				} else {
					collectOpenViewCommand(pluginId, decl.id, declared.title)
				}
			}
			return { postMessage: (message: unknown) => getPluginView(pluginId, decl.id)?.post?.(message) }
		},
		// registerCapture (goal 0309): declare-first like views; the face
		// is kept here for the capture window, and a palette command
		// summons that window on it.
		registerCapture: (decl) => {
			const declared = (manifest.contributes?.captures ?? []).find((c) => c.id === decl.id)
			if (!declared) throw new Error(`plugin ${pluginId}: capture "${decl.id}" is not declared in the manifest's contributes.captures`)
			if (declared.entry) {
				attachPluginCaptureMessages(pluginId, decl.id, decl.onMessage)
			} else {
				if (typeof decl.render !== 'function') throw new Error(`plugin ${pluginId}: capture "${decl.id}" needs a render function, or an entry page in the manifest`)
				collectPluginCapture({ pluginId, pluginName: manifest.name || pluginId, captureId: decl.id, label: declared.label, version: manifest.version, render: decl.render, onMessage: decl.onMessage })
				collectShowCaptureCommand(pluginId, decl.id, declared.label)
			}
			return { postMessage: (message: unknown) => getPluginCapture(pluginId, decl.id)?.post?.(message) }
		},
		registerCommand: (decl) => {
			const commandID = resolvedCommandID(manifest, decl.id)
			warnUndeclaredCommand(manifest, commandID)
			warnInvalidEnablement(manifest, commandID)
			const declaredMenu = menuForDeclaredCommand(manifest, commandID)
			const enabled = commandHasEnablement(manifest, commandID, decl.enabled)
				? () => commandIsEnabled(manifest, commandID, decl.enabled)
				: undefined
			collectPluginCommand({
				id: `plugin.${pluginId}.${commandID}`,
				label: decl.label,
				pluginId,
				enabled,
				run: decl.run,
				menu: declaredMenu,
			})
		},
		requestGuardedAction,
		evaluateGuardedAction,
		callIntegration,
		// The output door (goal 0326): Mill's own output viewer, drawn
		// into the plugin's element. Loaded on first use so activation
		// never pulls the app's module graph forward, and so a plugin
		// that renders no output never pays for the viewer's chunk. The
		// returned disposer awaits the same import, which keeps the
		// call synchronous for the plugin while the work is not.
		ui: Object.freeze({
			renderOutput: (el: HTMLElement, value: unknown, options: PluginOutputOptions = {}) => {
				const loading = import('./pluginOutputHost')
				void loading.then((m) => m.renderOutputInto(el, value, options, pluginId))
				return () => { void loading.then((m) => m.unmountOutput(el)) }
			},
			// el (goal 0386 S1): the text-safe DOM builder every
			// createElement/textContent-hand-rolling face already needed --
			// built in whichever document el's own caller runs in, so a
			// canvas object's face document works the same as Mill's own.
			el: <K extends keyof HTMLElementTagNameMap>(tag: K, attrs?: PluginElAttrs, children?: PluginElChild[]) => buildElement(document, tag, attrs, children),
		}),
		// formatDate (goal 0386 S1): pure computation, so it is a plain
		// function rather than a bound call, over shared/inventorySort.ts's
		// own formatUpdated -- the same relative-time phrasing Mill's own
		// interface renders everywhere, not a plugin's own Date math.
		formatDate: formatPluginDate,
		// The extension-interop door (goal 0364): a declared dependency's
		// export surface only, gated by ITS OWN manifest exports
		// allowlist -- never a live handle into another extension.
		extensions: Object.freeze({
			get: (id: string) => getExtensionExports(manifest, id),
		}),
	})
	// The api object is kept, not just handed to activate(): a framed
	// view or capture reaches Mill through a message bridge, and the
	// bridge routes its whitelisted calls onto this very object, so the
	// frame is held to exactly the same guards the plugin's own code is.
	pluginAPIs.set(pluginId, api)
	return api
}

// pluginAPIFor answers the api object one loaded plugin holds, or
// undefined for a plugin that never activated.
export function pluginAPIFor(pluginId: string): MillPluginAPI | undefined {
	return pluginAPIs.get(pluginId)
}

const pluginAPIs = new Map<string, MillPluginAPI>()

function collectOpenViewCommand(pluginId: string, viewId: string, title: string): void {
	collectPluginCommand({
		id: `view.open.${pluginId}.${viewId}`,
		label: title,
		pluginId,
		// The store is imported lazily inside run(): at activation time
		// the app module graph must not be pulled forward (the loader's
		// own import discipline).
		run: () => {
			void import('../shared/store').then((m) => m.useAppStore.getState().openWorkTab({ kind: 'plugin-view', pluginId, viewId }))
		},
	})
}

function collectShowCaptureCommand(pluginId: string, captureId: string, label: string): void {
	collectPluginCommand({
		id: `capture.${pluginId}.${captureId}`,
		label,
		pluginId,
		run: () => { void SettingsService.ShowCapture(pluginId, captureId) },
	})
}

// collectFrameSurfaces registers every view and capture the MANIFEST
// declares with an entry page, before any plugin code runs: such a
// surface is a page Mill mounts in its own frame, so it opens from the
// palette whether or not the plugin ever calls registerView.
export function collectFrameSurfaces(manifest: Manifest): void {
	const pluginId = manifest.id
	const pluginName = manifest.name || pluginId
	for (const view of manifest.contributes?.views ?? []) {
		if (!view.entry) continue
		collectPluginView({ pluginId, pluginName, viewId: view.id, title: view.title, version: manifest.version, entry: view.entry, placement: view.placement || 'tab', icon: manifest.icon || undefined })
		if (view.placement === 'board-switcher') {
			collectBoardSwitcherViewCommand(pluginId, view.id, view.title)
		} else {
			collectOpenViewCommand(pluginId, view.id, view.title)
		}
	}
	for (const capture of manifest.contributes?.captures ?? []) {
		if (!capture.entry) continue
		collectPluginCapture({ pluginId, pluginName, captureId: capture.id, label: capture.label, version: manifest.version, entry: capture.entry })
		collectShowCaptureCommand(pluginId, capture.id, capture.label)
	}
}

// A board-switcher-placed view opens from the board's own view
// switcher, which runs this command (goal 0357): the same store write
// the built-in projections' own commands ride, naming the persisted
// 'plugin:<pluginId>.<viewId>' value instead of a literal projection
// kind. Enablement is structural, like collectOpenViewCommand's -- a
// disabled plugin never activates, so its segment and command never
// exist.
function collectBoardSwitcherViewCommand(pluginId: string, viewId: string, title: string): void {
	collectPluginCommand({
		id: `atlas.pluginView.${pluginId}.${viewId}.open`,
		label: title,
		pluginId,
		surface: ['atlas'],
		run: () => {
			void import('../shared/store').then((m) => m.useAppStore.getState().setAtlasBoardView(`plugin:${pluginId}.${viewId}`))
		},
	})
}
