import { useCallback, useEffect, useRef, useState } from 'react'
import type { ComponentType, PointerEvent as ReactPointerEvent, RefObject } from 'react'
import { useKeyPress } from '@xyflow/react'
import { AtlasService } from '../shared/bindings'
import type { AtlasGestureCtx, AtlasGesturePoint, AtlasToolGesture, AtlasToolShape } from './atlasNounRegistry'

// A drag under this many screen pixels (either axis) is a stray click
// on the armed tool, not a draw -- the ONE constant every drag-shaped
// tool's own gesture.onEnd checks via meetsDragThreshold below, rather
// than five separately hand-copied local consts.
export const MIN_DRAG_PX = 6

export function isPrimaryButton(button: number): boolean {
  return button === 0
}

// meetsDragThreshold -- extent between the FIRST and LAST accumulated
// point, either axis, at least MIN_DRAG_PX. A tool whose own commit
// guard is something other than distance (eraser's "did we hit
// anything") never calls this at all.
export function meetsDragThreshold(points: readonly { x: number; y: number }[], min = MIN_DRAG_PX): boolean {
  if (points.length < 2) return false
  const start = points[0]
  const end = points[points.length - 1]
  return Math.abs(end.x - start.x) >= min || Math.abs(end.y - start.y) >= min
}

// gestureDisarmFns -- the arming-semantics enforcement point (goal 0215
// S2 design lock item 2): a STICKY tool's own gesture.onEnd receives
// no-ops for ctx.disarm/disarmUnlessLocked, so a sticky tool's own
// commit logic structurally cannot disarm it even if it tried, instead
// of trusting every future sticky tool's onEnd to simply never call
// them. A one-shot tool (sticky: false) gets the real functions.
export function gestureDisarmFns(sticky: boolean, disarm: () => void, disarmUnlessLocked: () => void): { disarm: () => void; disarmUnlessLocked: () => void } {
  if (sticky) return { disarm: () => {}, disarmUnlessLocked: () => {} }
  return { disarm, disarmUnlessLocked }
}

export interface AtlasToolGestureResult {
  // Wrapper-relative points for the armed tool's own gesture.preview,
  // populated from pointerdown through pointerup -- and, for an
  // ephemeral (fadeMs-declaring) tool, for as long as any point is
  // still aging out afterward.
  points: AtlasGesturePoint[]
  now: number
  Preview: ComponentType<{ points: AtlasGesturePoint[]; now: number }> | null
  panning: boolean
  onPointerDown: (e: ReactPointerEvent) => void
  onPointerMove: (e: ReactPointerEvent) => void
  onPointerUp: (e: ReactPointerEvent) => void
}

// useAtlasToolGesture -- the ONE platform hook owning gesture
// acquisition for every drag-shaped canvas tool (goal 0215 S2). Owns,
// exactly once: the primary-button guard, capture-phase
// stopPropagation()+preventDefault() (goal 0213's fix, absorbed so it
// can never be re-forgotten per tool -- stopping propagation alone
// silences the pane's own pointerdown handler, including the
// preventDefault() THAT handler would have called to suppress the
// browser's synthesized compatibility mousedown; without calling
// preventDefault() here too, that compat mousedown still reaches
// whatever node sits under the cursor and starts its native (d3-drag)
// drag mid-gesture), Space-hold pan step-aside (absorbed from
// useAtlasPanActivation.ts), Escape-cancel of the in-flight gesture
// only (never the tool's own arm state -- useAtlasCreation.ts's global
// listener owns that), and point accumulation in CLIENT space (fed to
// each tool's own gesture.onEnd/onPoint, since screenToFlowPosition
// expects real client coordinates) alongside WRAPPER-LOCAL space (fed
// to gesture.preview, which renders as a wrapper-spanning overlay).
//
// A tool with no declared `gesture` (card/note/table/image) never arms
// this hook at all -- every handler below is a no-op.
export function useAtlasToolGesture({ tool, readOnly, isFree, ctx, wrapperRef }: {
  tool: AtlasToolShape | null
  readOnly: boolean
  isFree: boolean
  ctx: AtlasGestureCtx
  // Kept as its own parameter, not a ctx field -- the wrapper box is
  // this engine's OWN local-space conversion need, never something a
  // tool's own onEnd/onPoint reads (they only ever see CLIENT points).
  wrapperRef: RefObject<HTMLDivElement | null>
}): AtlasToolGestureResult {
  const gesture = tool?.gesture ?? null
  const armed = isFree && !readOnly && gesture !== null

  const [points, setPoints] = useState<AtlasGesturePoint[]>([])
  const [now, setNow] = useState(() => performance.now())
  const [Preview, setPreview] = useState<AtlasToolGestureResult['Preview']>(null)

  const clientPointsRef = useRef<AtlasGesturePoint[]>([])
  const localPointsRef = useRef<AtlasGesturePoint[]>([])
  const drawingRef = useRef(false)
  const rafRef = useRef<number | null>(null)
  const tickRef = useRef<() => void>(() => {})
  // The current gesture's own fade window, captured at pointerdown so
  // a later disarm (which nulls `gesture` below) can never stop an
  // already-fading ephemeral trail mid-fade.
  const fadeMsRef = useRef<number | undefined>(undefined)
  // The gesture captured at pointerdown, kept for the boundaries that
  // fire after a disarm has already nulled `gesture` above: Escape's
  // cancel, and every fade frame of an ephemeral trail.
  const activeGestureRef = useRef<AtlasToolGesture | null>(null)
  // Fresh per-gesture scratch (eraser's own live hit accumulation) --
  // allocated at pointerdown, read at onEnd, discarded after.
  const scratchRef = useRef<{ cardIDs: Set<string>; noteIDs: Set<string>; objectIDs: Set<string> } | null>(null)
  // The undo journal mark this gesture opened (goal 0219 S2) -- the
  // promise BeginUndoMark returned, awaited before EndUndoMark closes
  // it in onPointerUpCore.
  const markOpenRef = useRef<Promise<void> | null>(null)

  const ctxRef = useRef(ctx)
  useEffect(() => { ctxRef.current = ctx }, [ctx])
  const toolRef = useRef(tool)
  useEffect(() => { toolRef.current = tool }, [tool])
  const gestureRef = useRef<AtlasToolGesture | null>(gesture)
  useEffect(() => { gestureRef.current = gesture }, [gesture])
  // Space-hold pan step-aside (goal 0208 defect 2, absorbed from
  // useAtlasPanActivation.ts): React Flow's own panActivationKeyCode
  // ('Space', its own default) already re-enables pane panning the
  // instant Space is held, overriding this board's own panOnDrag={false}
  // -- but only once these capture-phase handlers step aside, since
  // they call stopPropagation() on an ancestor of the pane specifically
  // so React Flow never sees the gesture at all. useKeyPress is the
  // exact primitive React Flow uses internally for the same key. Read
  // via a ref from inside each core handler (below) rather than
  // wrapping them in a step-aside closure built during render -- the
  // wrap-during-render shape trips react-hooks/refs' "passing a ref-
  // reading function during render" check even though nothing is
  // actually READ until the handler later fires.
  const panning = useKeyPress('Space') && armed
  const panningRef = useRef(panning)
  useEffect(() => { panningRef.current = panning }, [panning])

  // wrapperRef is a stable ref object (created once by the caller via
  // useRef) -- reading .current at call time, inside a useCallback
  // whose deps deliberately exclude it, always sees the latest mounted
  // element without needing a second indirection ref.
  const toLocal = (p: { x: number; y: number }): { x: number; y: number } => {
    const box = wrapperRef.current?.getBoundingClientRect()
    return box ? { x: p.x - box.left, y: p.y - box.top } : p
  }

  // The modifier keys the CURRENT pointer event carried, recorded by
  // each core handler below so buildCtx can hand them to a tool that
  // never sees the event itself.
  const modifiersRef = useRef<AtlasGestureCtx['modifiers']>({ shift: false, alt: false, ctrl: false, meta: false })

  const buildCtx = useCallback((): AtlasGestureCtx => {
    const sticky = toolRef.current?.sticky ?? false
    const { disarm, disarmUnlessLocked } = gestureDisarmFns(sticky, ctxRef.current.disarm, ctxRef.current.disarmUnlessLocked)
    return { ...ctxRef.current, disarm, disarmUnlessLocked, modifiers: modifiersRef.current, hitAccumulator: scratchRef.current ?? { cardIDs: new Set(), noteIDs: new Set(), objectIDs: new Set() } }
  }, [])

  const recordModifiers = (e: ReactPointerEvent): void => {
    modifiersRef.current = { shift: e.shiftKey, alt: e.altKey, ctrl: e.ctrlKey, meta: e.metaKey }
  }

  // The ephemeral prune loop (an `ephemeral-drag` tool's own fadeMs,
  // laser today): started at pointerdown, self-perpetuating via rAF
  // until every point has aged past fadeMs, continuing after
  // pointerup/disarm on purpose -- a released trail keeps fading on its
  // own, with nothing left for a reload to read back.
  const tick = useCallback(() => {
    const t = performance.now()
    const ms = fadeMsRef.current
    if (ms !== undefined) {
      clientPointsRef.current = clientPointsRef.current.filter((p) => t - p.t < ms)
      localPointsRef.current = localPointsRef.current.filter((p) => t - p.t < ms)
    }
    setPoints([...localPointsRef.current])
    setNow(t)
    if (ms !== undefined && localPointsRef.current.length > 0) activeGestureRef.current?.onFade?.(t)
    if (localPointsRef.current.length > 0 || drawingRef.current) {
      rafRef.current = requestAnimationFrame(() => tickRef.current())
    } else {
      rafRef.current = null
      setPreview(null)
    }
  }, [])
  useEffect(() => { tickRef.current = tick }, [tick])
  const ensureLoop = () => { if (rafRef.current === null) rafRef.current = requestAnimationFrame(tick) }

  const onPointerDownCore = useCallback((e: ReactPointerEvent) => {
    if (panningRef.current || !armed || !isPrimaryButton(e.button)) return
    recordModifiers(e)
    e.stopPropagation()
    e.preventDefault()
    const g = gestureRef.current
    // A gesture (stroke, eraser) undoes as ONE step regardless of how
    // many entities it touches (ADR-0044 decision 2, goal 0219 S2).
    // Canvas-draft tools own the commit mark because their commit may
    // arrive after pointer-up; every other gesture uses this boundary.
    markOpenRef.current = g?.ownsUndo ? null : AtlasService.BeginUndoMark()
    drawingRef.current = true
    scratchRef.current = { cardIDs: new Set(), noteIDs: new Set(), objectIDs: new Set() }
    fadeMsRef.current = g?.fadeMs
    activeGestureRef.current = g
    setPreview(() => g?.preview ?? null)
    const t = performance.now()
    const client = { x: e.clientX, y: e.clientY, t }
    const local = { ...toLocal(client), t }
    clientPointsRef.current = [client]
    localPointsRef.current = [local]
    setPoints([local])
    g?.onPoint?.(client, buildCtx())
    ensureLoop()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- toLocal/buildCtx read refs at call time, deliberately not dependencies
  }, [armed])

  const onPointerMoveCore = useCallback((e: ReactPointerEvent) => {
    if (panningRef.current || !drawingRef.current) return
    recordModifiers(e)
    e.stopPropagation()
    const t = performance.now()
    const client = { x: e.clientX, y: e.clientY, t }
    const local = { ...toLocal(client), t }
    clientPointsRef.current = [...clientPointsRef.current, client]
    localPointsRef.current = [...localPointsRef.current, local]
    if (fadeMsRef.current === undefined) setPoints([...localPointsRef.current])
    gestureRef.current?.onPoint?.(client, buildCtx())
    // eslint-disable-next-line react-hooks/exhaustive-deps -- same as onPointerDownCore above
  }, [])

  const onPointerUpCore = useCallback((e: ReactPointerEvent) => {
    if (panningRef.current || !drawingRef.current) return
    recordModifiers(e)
    e.stopPropagation()
    drawingRef.current = false
    const clientPoints = clientPointsRef.current
    const g = gestureRef.current
    // Invoke onEnd synchronously because its ctx reads scratchRef and
    // points state cleared below. For engine-owned gestures, keep the
    // mark open until async work settles. Canvas-draft tools have no
    // engine mark here; their commit path closes its own mark before
    // applying selection.
    const mark = markOpenRef.current
    try {
      const completion = g?.onEnd(clientPoints, buildCtx())
      void Promise.resolve(completion)
        .catch((error: unknown) => console.error('atlas tool gesture failed', error))
        .finally(() => { void mark?.then(() => AtlasService.EndUndoMark()) })
    } catch (error) {
      void mark?.then(() => AtlasService.EndUndoMark())
      throw error
    }
    markOpenRef.current = null
    clientPointsRef.current = []
    scratchRef.current = null
    if (fadeMsRef.current === undefined) {
      localPointsRef.current = []
      setPoints([])
      setPreview(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- same as onPointerDownCore above
  }, [])

  // Escape cancels only the IN-PROGRESS gesture's own local state --
  // the tool's own arm state is a SEPARATE global listener
  // (useAtlasCreation.ts's cancelAll), matching every one of the five
  // hooks this engine replaces.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || !drawingRef.current) return
      drawingRef.current = false
      // Escape cancels WITHOUT calling onEnd -- no mutation happens, but
      // a mark opened at pointerdown must still close, or every later,
      // unrelated action would wrongly land inside it. A tool running
      // outside this document is TOLD (onCancel), since it has no other
      // way to learn the gesture it is mid-way through is over.
      activeGestureRef.current?.onCancel?.()
      void markOpenRef.current?.then(() => AtlasService.EndUndoMark())
      markOpenRef.current = null
      clientPointsRef.current = []
      scratchRef.current = null
      if (fadeMsRef.current === undefined) {
        localPointsRef.current = []
        setPoints([])
        setPreview(null)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  useEffect(() => () => {
    if (rafRef.current !== null) cancelAnimationFrame(rafRef.current)
  }, [])

  return {
    points, now, Preview, panning,
    onPointerDown: onPointerDownCore,
    onPointerMove: onPointerMoveCore,
    onPointerUp: onPointerUpCore,
  }
}

export type { AtlasGestureCtx, AtlasGesturePoint, AtlasToolGesture }
