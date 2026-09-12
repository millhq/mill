import { AtlasService } from '../shared/bindings'
import { refreshAtlas } from '../atlas/atlasStore'
import { frameContainingPoint } from '../atlas/atlasFramePoint'
import { pointHitIDs } from '../atlas/atlasEnclosure'
import { useAtlasStyleValues } from '../atlas/atlasStyleValueStore'
import type { AtlasGestureCtx, AtlasGesturePoint, AtlasToolGesture, ThirdPartyNounShape } from '../atlas/atlasNounRegistry'
import { meetsDragThreshold } from '../atlas/useAtlasToolGesture'
import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import type { CanvasObjectDecl } from './sdk'
import type { CanvasToolPoint } from './sdk/canvasTools'
import { buildThirdPartyNoun, styleFieldDefault } from './canvasToolAdapter'
import { createElement } from 'react'
import { PreviewOverlay } from './canvasToolPreview'
import type { CanvasToolDescriptor, ToolPointerPayload } from './canvasToolProtocol'
import type { CanvasDraftPlacement } from './canvasDrafts'

// The host half of a framed canvas tool (docs/goals/0380 Decisions 1
// and 2): Mill owns the pointer, converts it to board space, coalesces
// a frame's worth of moves into ONE message, and hands the tool
// nothing but data. The tool answers with draft writes; the host draws
// the declared preview and performs the commit.
//
// The registration itself goes through buildThirdPartyNoun unchanged
// -- a framed tool is the same registry shape a same-DOM one is, with
// a synthesized gesture whose onPoint/onEnd forward over the bridge
// instead of calling into the plugin's own module. Everything else
// (icon resolution, style-field adaptation, disarm semantics, the
// ingestion claim check) is the one existing path, not a second one.

// FramedToolSession is one armed tool's live gesture state: what the
// engine handed it, and the frame's worth of points not yet sent.
interface FramedToolSession {
  ctx: AtlasGestureCtx
  pending: CanvasToolPoint[]
  frame: number | null
  zoom: number
  modifiers: ToolPointerPayload['modifiers']
}

export interface FramedToolRuntime {
  pluginId: string
  descriptor: CanvasToolDescriptor
  // post delivers one host->frame event; supplied by the activation
  // bridge, which owns the frame handle.
  post: (event: string, payload: unknown) => void | Promise<void>
  session: FramedToolSession | null
  // The gesture that just ended, kept until the next one opens: a
  // tool's answer to pointer-up arrives a round trip LATER, by which
  // point the engine has already closed the session -- an eraser
  // committing its pass is exactly that, and reading a null session
  // there would silently erase nothing.
  lastSession: FramedToolSession | null
}

const runtimes = new Map<string, FramedToolRuntime>()

function runtimeKey(pluginId: string, toolId: string): string {
  return `${pluginId}::${toolId}`
}

export function framedToolRuntime(pluginId: string, toolId: string): FramedToolRuntime | undefined {
  return runtimes.get(runtimeKey(pluginId, toolId))
}

// armedToolCursor -- the pointer shape a tool asked for while it is
// armed. Mill shows a crosshair for every armed tool by default
// (AtlasBoard.module.css), so this narrows that for the tools whose
// job reads as something else; an undeclared tool keeps the default.
export function armedToolCursor(toolId: string | null): string | null {
  if (!toolId) return null
  for (const runtime of runtimes.values()) {
    if (runtime.descriptor.kind === toolId) return runtime.descriptor.cursor ?? null
  }
  return null
}

export function forgetFramedTools(pluginId: string): void {
  for (const key of [...runtimes.keys()]) {
    if (key.startsWith(`${pluginId}::`)) runtimes.delete(key)
  }
}

// boardZoom derives the viewport scale from the ONE conversion the
// gesture ctx already carries: a 100px screen span measured in board
// units is 100/zoom, so no new ctx field is needed to tell a tool how
// big a screen pixel currently is.
function boardZoom(ctx: AtlasGestureCtx): number {
  const a = ctx.screenToFlowPosition({ x: 0, y: 0 })
  const b = ctx.screenToFlowPosition({ x: 100, y: 0 })
  const span = b.x - a.x
  return span > 0 ? 100 / span : 1
}

function targetObjectAt(ctx: AtlasGestureCtx, board: { x: number; y: number }): string | undefined {
  return pointHitIDs(board, ctx.objectBoxes)[0]
}

const NO_MODIFIERS = { shift: false, alt: false, ctrl: false, meta: false } as const

function sendPointer(runtime: FramedToolRuntime, phase: ToolPointerPayload['phase'], point: CanvasToolPoint, coalesced: CanvasToolPoint[], zoom: number, modifiers: ToolPointerPayload['modifiers'], target?: string): void | Promise<void> {
  return runtime.post('tool.pointer', {
    toolId: runtime.descriptor.kind,
    phase, point, coalesced, modifiers, zoom,
    styleValues: currentStyleValues(runtime.descriptor),
    target,
  } satisfies ToolPointerPayload)
}

// flushMoves is the rAF coalescing (Decision 1): at most one message
// per animation frame, carrying the newest sample plus every sample
// folded into the same frame, so a freehand stroke keeps its detail
// without one postMessage per pointermove. One rAF, never a retry
// loop -- the frame is scheduled by an arriving point and cancels
// itself when the gesture ends.
function flushMoves(runtime: FramedToolRuntime): void {
  const session = runtime.session
  if (!session) return
  session.frame = null
  const pending = session.pending
  if (pending.length === 0) return
  session.pending = []
  const point = pending[pending.length - 1]
  void sendPointer(runtime, 'move', point, pending.slice(0, -1), session.zoom, session.modifiers, targetObjectAt(session.ctx, point))
}

function scheduleFlush(runtime: FramedToolRuntime): void {
  const session = runtime.session
  if (!session || session.frame !== null) return
  session.frame = requestAnimationFrame(() => flushMoves(runtime))
}

function boardPoint(ctx: AtlasGestureCtx, pt: AtlasGesturePoint): CanvasToolPoint {
  const flow = ctx.screenToFlowPosition({ x: pt.x, y: pt.y })
  return { x: flow.x, y: flow.y, t: pt.t }
}

function endSession(runtime: FramedToolRuntime): void {
  const session = runtime.session
  if (session?.frame !== null && session?.frame !== undefined) cancelAnimationFrame(session.frame)
  runtime.lastSession = session
  runtime.session = null
}

// placementFor is how a committed draft reaches the board: the SAME
// AtlasService calls a same-DOM tool's own ctx.createObject makes,
// with parent-frame resolution and selection done here rather than
// inside the frame, which knows nothing about either.
function placementFor(descriptor: CanvasToolDescriptor, ctx: AtlasGestureCtx): CanvasDraftPlacement {
  return {
    place: async (draft) => {
      if (descriptor.ephemeral) return null
      const parent = frameContainingPoint(ctx.cardBoxes, draft.at) ?? ctx.parentID
      const created = await AtlasService.CreateBoardObject(draft.kind, draft.data, { X: draft.at.x, Y: draft.at.y }, parent)
      if (draft.size) await AtlasService.SetBoardObjectSize(created.ID, { W: draft.size.w, H: draft.size.h })
      await refreshAtlas()
      return created.ID
    },
    // Selecting refreshes FIRST and awaits it -- the same refresh-then-
    // select ordering createObject (canvasToolAdapter.ts) and the paste
    // fallback note (useAtlasPaste.ts) already use. The apply effect
    // (AtlasBoard.tsx) only marks a node selected if it is already
    // present in allNodes; place()'s own refresh runs before the undo
    // mark closes, so selecting straight off that snapshot races the
    // refresh the mark's close triggers. A fresh, awaited refresh here
    // removes the race without a timeout.
    select: async (id) => {
      await refreshAtlas()
      ctx.onShapeCreated(id)
    },
  }
}

// activePlacement/activeCtx -- what the door router needs about the
// gesture currently in flight: where a committed draft should land,
// and the erase accumulator the engine allocated for this pass.
export function activeSession(pluginId: string, toolId: string): { ctx: AtlasGestureCtx; descriptor: CanvasToolDescriptor } | null {
  const runtime = framedToolRuntime(pluginId, toolId)
  return runtime?.session ? { ctx: runtime.session.ctx, descriptor: runtime.descriptor } : null
}

// endedSession -- the gesture whose pointer-up a tool is still
// answering. Falls back to the live one, so a door called mid-drag and
// one called a round trip after it read the same gesture.
export function endedSession(pluginId: string, toolId: string): { ctx: AtlasGestureCtx; descriptor: CanvasToolDescriptor } | null {
  const runtime = framedToolRuntime(pluginId, toolId)
  const session = runtime?.session ?? runtime?.lastSession
  return runtime && session ? { ctx: session.ctx, descriptor: runtime.descriptor } : null
}

export function draftPlacementFor(pluginId: string, toolId: string): CanvasDraftPlacement | null {
  const session = activeSession(pluginId, toolId)
  return session ? placementFor(session.descriptor, session.ctx) : null
}

// currentStyleValues -- the same picker values a same-DOM gesture ctx
// carries, resolved against each declared field's default so a tool
// reads a complete set before the first pick.
export function currentStyleValues(descriptor: CanvasToolDescriptor): Record<string, string | number> {
  const out: Record<string, string | number> = {}
  for (const field of descriptor.styleFields) out[field.key] = styleFieldDefault(field)
  return { ...out, ...(useAtlasStyleValues.getState().values[descriptor.kind] ?? {}) }
}

export function eraseAt(ctx: AtlasGestureCtx, at: { x: number; y: number }): void {
  for (const id of pointHitIDs(at, ctx.cardBoxes.filter((b) => !b.isFrame))) ctx.hitAccumulator.cardIDs.add(id)
  for (const id of pointHitIDs(at, ctx.noteBoxes)) ctx.hitAccumulator.noteIDs.add(id)
  for (const id of pointHitIDs(at, ctx.objectBoxes)) ctx.hitAccumulator.objectIDs.add(id)
}

export function commitErase(ctx: AtlasGestureCtx): void {
  const cardIDs = [...ctx.hitAccumulator.cardIDs]
  const noteIDs = [...ctx.hitAccumulator.noteIDs]
  const objectIDs = [...ctx.hitAccumulator.objectIDs]
  if (cardIDs.length + noteIDs.length + objectIDs.length === 0) return
  ctx.onDeleteSelection(cardIDs, noteIDs, objectIDs)
}

// declFromDescriptor rebuilds the plain-data CanvasObjectDecl the
// existing registration path already validates and adapts -- icon
// resolution, style-field adaptation, ingestion claims, face content,
// tray group. Its `gesture` here is a PLACEHOLDER that satisfies the
// drag-interaction pairing rule and is then replaced wholesale by
// framedGesture below: a framed tool's gesture is host code, not
// plugin code wrapped in a ctx. renderFace is absent on purpose -- a
// framed tool's face is an entry page or it has none, which is the
// whole point of this goal.
function declFromDescriptor(d: CanvasToolDescriptor, renderFace?: CanvasObjectDecl['renderFace']): CanvasObjectDecl {
  return {
    kind: d.kind,
    objectKind: d.objectKind,
    label: d.label,
    description: d.description,
    icon: d.icon,
    shortcutKey: d.shortcutKey,
    group: d.group,
    source: d.source ?? 'board-local',
    editRoute: d.editRoute ?? 'none',
    interaction: d.ephemeral ? 'ephemeral-drag' : 'drag-to-draw',
    sticky: d.sticky,
    lockable: d.lockable,
    dragBand: d.dragBand,
    styleFields: d.styleFields,
    renderFace,
    gesture: { onEnd: () => {} },
  }
}

// framedGesture is the whole of a framed tool's board behaviour: four
// boundaries out over the bridge, and the host's own disarm rule --
// the SAME rule adaptGesture applies to a same-DOM tool, restated here
// because a framed tool's onEnd cannot be wrapped around plugin code
// that does not run in this document. A stray armed click still
// reaches onEnd (the engine fires it unconditionally), so disarming is
// gated on the shared drag threshold exactly as it is there.
function framedGesture(runtime: FramedToolRuntime, sticky: boolean): AtlasToolGesture {
  return {
    // Persisted tools commit their draft through canvasDrafts, which owns
    // the actor mark across the bridge round trip. Ephemeral tools place no
    // draft; an eraser's host mutation therefore needs the engine mark.
    ownsUndo: !runtime.descriptor.ephemeral,
    onPoint: (pt, ctx) => {
      const point = boardPoint(ctx, pt)
      if (!runtime.session) {
        runtime.lastSession = null
        runtime.session = { ctx, pending: [], frame: null, zoom: boardZoom(ctx), modifiers: ctx.modifiers }
        void sendPointer(runtime, 'down', point, [], runtime.session.zoom, ctx.modifiers, targetObjectAt(ctx, point))
        return
      }
      runtime.session.ctx = ctx
      runtime.session.modifiers = ctx.modifiers
      runtime.session.pending.push(point)
      scheduleFlush(runtime)
    },
    onEnd: (points, ctx) => {
      try {
        const session = runtime.session
        if (!session) return
        flushMoves(runtime)
        const last = points[points.length - 1]
        const point = last ? boardPoint(ctx, last) : { x: 0, y: 0, t: performance.now() }
        const completion = sendPointer(runtime, 'up', point, [], session.zoom, ctx.modifiers, targetObjectAt(ctx, point))
        // An ephemeral same-document tool serializes pointer handlers and
        // returns that queue here. Keep its session available until queued
        // move doors finish; otherwise eraseAt sees an ended gesture. Persisted
        // tools and framed posts preserve their existing immediate handoff.
        if (completion && runtime.descriptor.ephemeral) return completion.finally(() => endSession(runtime))
        endSession(runtime)
        return completion
      } finally {
        if (!sticky && meetsDragThreshold(points)) ctx.disarmUnlessLocked()
      }
    },
    onCancel: () => {
      const session = runtime.session
      if (!session) return
      void sendPointer(runtime, 'cancel', { x: 0, y: 0, t: performance.now() }, [], session.zoom, NO_MODIFIERS)
      endSession(runtime)
    },
    onFade: (now) => { void sendPointer(runtime, 'fade', { x: 0, y: 0, t: now }, [], 1, NO_MODIFIERS) },
    fadeMs: runtime.descriptor.fadeMs,
  }
}

// buildFramedTool is registerCanvasTool's host side: one descriptor in,
// one registry noun out, with the declared preview taking the overlay
// slot a same-DOM tool's renderPreview would have taken.
export function buildFramedTool(pluginId: string, manifest: Manifest, descriptor: CanvasToolDescriptor, post: (event: string, payload: unknown) => void | Promise<void>, renderFace?: CanvasObjectDecl['renderFace'], frameRegistration?: { entry?: string }): ThirdPartyNounShape {
  const runtime: FramedToolRuntime = { pluginId, descriptor, post, session: null, lastSession: null }
  const noun = buildThirdPartyNoun(pluginId, manifest, declFromDescriptor(descriptor, renderFace), frameRegistration)
  runtimes.set(runtimeKey(pluginId, descriptor.kind), runtime)
  const gesture = framedGesture(runtime, noun.sticky)
  if (!descriptor.preview) return { ...noun, gesture }
  // An ephemeral tool draws a TRAIL (it places nothing and fades); a
  // placing tool draws a PREVIEW of what it is about to place. One
  // slot, two honest names.
  const suffix = descriptor.ephemeral ? 'trail' : 'preview'
  const preview = descriptor.preview
  // The overlay slot's own component shape is {points, now}; a framed
  // tool's preview deliberately reads neither, since what it draws is
  // what its own code decided the in-progress object looks like, not
  // the raw pointer track.
  const Preview = () => createElement(PreviewOverlay, { toolId: descriptor.kind, decl: preview, testid: `atlas-${descriptor.kind}-${suffix}` })
  return { ...noun, gesture: { ...gesture, preview: Preview } }
}
