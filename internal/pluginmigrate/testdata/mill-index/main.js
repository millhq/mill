// @ts-check
/// <reference path="../../../frontend/plugin-sdk/index.d.ts" />

// Board index -- Mill's example of the two read doors a plugin has
// onto the board's contents (docs/goals/0278): api.query lists cards,
// notes, and objects with the names a person sees them by, and
// api.on('contents:changed') says when to list again. No build step:
// copy this folder into the plugins directory and reload.
//
// It contributes one board-local object whose face is the listing.
// The face re-renders itself on every change -- renderFace only
// re-runs on the object's OWN data, so the change event is the door.

/** @param {import('../../../frontend/plugin-sdk').MillPluginAPI} api */
export function activate(api) {
	const faces = new Set()
	const redrawAll = () => {
		for (const el of faces) {
			if (!el.isConnected) { faces.delete(el); continue }
			void render(el)
		}
	}
	api.on('contents:changed', redrawAll)

	// Declared in the manifest (contributes.commands) and named by the
	// manifest's own tool, so the same refresh a person runs from the
	// palette is what an agent calls over MCP -- one action, two doors.
	api.registerCommand({ id: 'refresh', label: 'Refresh the board index', run: redrawAll })

	api.registerCanvasObject({
		kind: 'index',
		label: 'Board index',
		description: 'Everything on the board, listed by kind and kept current.',
		icon: '🗂️',
		source: 'board-local',
		editRoute: 'none',
		defaultPayload: {},
		renderFace(el) {
			faces.add(el)
			void render(el)
		},
	})

	async function render(el) {
		let entries
		try {
			entries = await api.query({})
		} catch (err) {
			el.replaceChildren()
			el.textContent = 'Could not list the board.'
			api.notify({ level: 'error', text: 'Board index could not list the board: ' + String(err && err.message ? err.message : err) })
			return
		}
		const byKind = new Map()
		for (const e of entries) {
			if (!byKind.has(e.kind)) byKind.set(e.kind, [])
			byKind.get(e.kind).push(e)
		}
		// All text lands via textContent -- never markup -- so a title can
		// never inject anything.
		el.replaceChildren()
		el.style.cssText = 'display:flex;flex-direction:column;gap:6px;padding:10px 12px;font:12px system-ui;height:100%;box-sizing:border-box;overflow:auto'
		el.setAttribute('data-testid', 'index-face')
		if (byKind.size === 0) {
			const empty = document.createElement('div')
			empty.textContent = 'Nothing on the board yet.'
			el.append(empty)
			return
		}
		for (const [kind, list] of [...byKind.entries()].sort(([a], [b]) => a.localeCompare(b))) {
			const heading = document.createElement('div')
			heading.style.cssText = 'font-weight:600;margin-top:4px'
			heading.textContent = kind.charAt(0).toUpperCase() + kind.slice(1) + ' · ' + list.length
			heading.setAttribute('data-testid', 'index-kind-' + kind)
			el.append(heading)
			for (const e of list) {
				const row = document.createElement('div')
				row.textContent = e.title
				row.style.cssText = 'padding-left:8px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis'
				row.setAttribute('data-testid', 'index-row')
				row.setAttribute('data-kind', kind)
				el.append(row)
			}
		}
	}
}
