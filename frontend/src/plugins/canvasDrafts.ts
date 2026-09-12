import { create } from 'zustand'
import { AtlasService } from '../shared/bindings'
import { CanvasProtocolError, type ObjectCreateMessage, type ObjectPatchMessage } from './canvasToolProtocol'

// A DRAFT is what a framed tool draws with (docs/goals/0380 Decisions
// 1 and 6): the in-progress object, live but not yet on the board.
// Every patch between pointer-down and commit is host-side memory
// only -- nothing persists, nothing syncs, nothing lands in the undo
// journal -- so a drag that is abandoned leaves the board exactly as
// it was, and a committed one is ONE journal entry no matter how many
// frames it took to draw.
//
// This is the "preview is data" half of the design: the preview
// renders from a draft's own data, so the previewed thing and the
// committed thing are the same record at two moments, never two
// separately-maintained drawings that can disagree.

export interface CanvasDraftRecord {
  id: string
  pluginId: string
  toolId: string
  kind: string
  at: { x: number; y: number }
  size?: { w: number; h: number }
  // data is what a commit persists as the object's payload; preview is
  // the drawing state the host paints from and NEVER writes -- a stroke
  // in progress, a rubber-band rectangle. Keeping them apart is what
  // lets a preview carry whatever it needs without leaking a key into
  // the placed object, and the preview declaration reads across both.
  data: Record<string, string>
  preview: Record<string, string>
}

// How a draft becomes a real board object. The gesture that opened the
// draft supplies both halves, because parent-frame resolution and
// selection are the BOARD's own concerns and this module deliberately
// knows nothing about either. They are two halves rather than one call
// because selecting has to happen after the undo mark closes.
export interface CanvasDraftPlacement {
  place: (draft: CanvasDraftRecord) => Promise<string | null>
  // Returns a promise so the commit path can await it: selecting reads
  // the board's own node list, which is only guaranteed current once
  // the placement's own post-commit refresh has resolved (see select's
  // implementation in canvasToolFramed.ts).
  select: (id: string) => void | Promise<void>
}

interface CanvasDraftState {
  drafts: Record<string, CanvasDraftRecord>
  set: (draft: CanvasDraftRecord) => void
  drop: (id: string) => void
}

export const useCanvasDrafts = create<CanvasDraftState>()((set) => ({
  drafts: {},
  set: (draft) => set((s) => ({ drafts: { ...s.drafts, [draft.id]: draft } })),
  drop: (id) => set((s) => {
    if (!(id in s.drafts)) return s
    const next = { ...s.drafts }
    delete next[id]
    return { drafts: next }
  }),
}))

const placements = new Map<string, CanvasDraftPlacement>()
let sequence = 0

export function createDraft(pluginId: string, toolId: string, message: ObjectCreateMessage, place: CanvasDraftPlacement): CanvasDraftRecord {
  sequence += 1
  const draft: CanvasDraftRecord = {
    id: `draft-${pluginId}-${toolId}-${sequence}`,
    pluginId,
    toolId,
    kind: message.kind,
    at: message.at,
    size: message.size,
    data: message.data,
    preview: message.preview,
  }
  placements.set(draft.id, place)
  useCanvasDrafts.getState().set(draft)
  return draft
}

// An id this plugin does not own is refused by name, with nothing
// changed -- the same refusal a malformed message gets, since a frame
// reaching for another plugin's draft is exactly as untrusted as one
// sending nonsense.
function ownDraft(pluginId: string, id: string): CanvasDraftRecord {
  const draft = useCanvasDrafts.getState().drafts[id]
  if (!draft || draft.pluginId !== pluginId) throw new CanvasProtocolError('id', `does not name a draft this extension owns ("${id}")`)
  return draft
}

export function patchDraft(pluginId: string, message: ObjectPatchMessage): void {
  const draft = ownDraft(pluginId, message.id)
  useCanvasDrafts.getState().set({
    ...draft,
    at: message.at ?? draft.at,
    size: message.size ?? draft.size,
    data: message.data ? { ...draft.data, ...message.data } : draft.data,
    preview: message.preview ? { ...draft.preview, ...message.preview } : draft.preview,
  })
}

export function discardDraft(pluginId: string, id: string): void {
  ownDraft(pluginId, id)
  placements.delete(id)
  useCanvasDrafts.getState().drop(id)
}

// commitDraft is the ONE journal entry (ADR-0044 decision 2): the mark
// opens here rather than in the gesture engine, because a framed
// tool's commit arrives after pointer-up -- one bridge round trip
// later -- and the engine's own mark has already closed by then.
export async function commitDraft(pluginId: string, id: string, select: boolean): Promise<string | null> {
  const draft = ownDraft(pluginId, id)
  const place = placements.get(id)
  placements.delete(id)
  useCanvasDrafts.getState().drop(id)
  if (!place) return null
  await AtlasService.BeginUndoMark()
  let placed: string | null
  try {
    placed = await place.place(draft)
  } finally {
    await AtlasService.EndUndoMark()
  }
  // After the mark, never inside it: a selection made before it closes
  // does not survive the refresh closing it triggers. Awaited, not
  // fired-and-forgotten -- select's own refresh (canvasToolFramed.ts)
  // must settle before commitDraft resolves, so a caller awaiting the
  // commit never observes the object placed but not yet selected.
  if (placed && select) await place.select(placed)
  return placed
}

// dropDraftsFor clears whatever a plugin left behind when its frame
// goes away mid-drag: an uncommitted draft is memory, so unloading the
// plugin that owns it must not leave a preview painted on the board.
export function dropDraftsFor(pluginId: string): void {
  for (const draft of Object.values(useCanvasDrafts.getState().drafts)) {
    if (draft.pluginId !== pluginId) continue
    placements.delete(draft.id)
    useCanvasDrafts.getState().drop(draft.id)
  }
}

// liveDraftFor -- the draft an armed tool is currently drawing, read by
// the preview slot. At most one per tool: a gesture opens exactly one
// draft and closes it at pointer-up.
export function liveDraftFor(drafts: Record<string, CanvasDraftRecord>, toolId: string): CanvasDraftRecord | null {
  for (const draft of Object.values(drafts)) {
    if (draft.toolId === toolId) return draft
  }
  return null
}
