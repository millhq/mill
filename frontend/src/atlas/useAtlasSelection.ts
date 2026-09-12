import { useCallback, useEffect, useRef, useState } from 'react'
import type { OnSelectionChangeFunc } from '@xyflow/react'
import type { BoardObject, Card, Note } from '../../bindings/github.com/alicoding/mill/internal/domain/atlas/models'
import { useAtlasSelectionStore } from '../shared/atlasSelectionStore'
import type { ResolvedBoardEdge } from './atlasLinkResolution'

function retainEqualIDs(current: string[], next: string[]): string[] {
  return current.length === next.length && current.every((id, i) => id === next[i]) ? current : next
}

// Multi-selection state + the two context-menu paths that read it
// (goal 0081; split from AtlasBoard.tsx along the selection seam at
// the 500-line convention).
//
// Ref-only on purpose: selection feeds no render output, and React
// Flow reports a "change" on every nodes-array identity change -- a
// setState here once blew React's update-depth ceiling (React error
// #185) through AnchoredOverlay's inline ref churn. A ref write is
// free and can't re-render.
//
// Two menu paths exist because React Flow renders differently per
// selection size: right-clicking a lone node reaches
// onNodeContextMenu, but with a live multi-selection React Flow draws
// a selection-rectangle overlay OVER the member nodes -- a right-click
// lands on that overlay and never reaches the node handler, so the
// group menu was unreachable through the very gesture that creates
// the selection. onSelectionContextMenu is the overlay's own hook;
// the pointerdown snapshot covers the node-handler path, where React
// Flow re-selects the pressed node (clearing the multi-selection) on
// the SAME pointer-down that later fires onNodeContextMenu.
export function useAtlasSelection({ cards, notes, objects, arteries, spaceId, onMultiSelectContextMenu }: {
  cards: Card[]
  notes: Note[]
  // Board objects (goal 0179/0180): a third split, same shape as
  // cards/notes -- multi-selecting/deleting a mix of cards, notes and
  // objects is one gesture, not three.
  objects: BoardObject[]
  // The board's edges, so a selected single-link artery reaches the
  // shared selection (goal 0346 slice B); an aggregated one is not one
  // link and never does.
  arteries: ResolvedBoardEdge[]
  spaceId: string
  onMultiSelectContextMenu: (cardIDs: string[], noteIDs: string[], objectIDs: string[], pos: { x: number; y: number }) => void
}) {
  const selectedIDsRef = useRef<string[]>([])
  const contextSelectionRef = useRef<string[]>([])
  // A programmatic selection crosses two controlled-state renders: the
  // selection mirror updates first, then AtlasBoard applies selected:true to
  // React Flow's rebuilt node. React Flow can report an intermediate empty
  // selection between those renders. Keep that transport-only report from
  // erasing the selection before the selected node is acknowledged.
  const pendingProgrammaticIDRef = useRef<string | null>(null)
  // Reactive split (owner-caught follow-up to goal 0092): the
  // selection tray and every node type's outline need to re-render on
  // a selection change, unlike the ref-only menu logic above (which
  // deliberately avoids re-rendering -- see this file's own header
  // comment on React error #185). A second, independent state mirror
  // of the same ref, split into card/note/object ids the way
  // openMultiMenu already splits them below.
  const [selectedCards, setSelectedCards] = useState<string[]>([])
  const [selectedNotes, setSelectedNotes] = useState<string[]>([])
  const [selectedObjects, setSelectedObjects] = useState<string[]>([])
  // A one-shot trigger for selectObject below (goal 0199), NOT the
  // selection arrays themselves -- AtlasBoard.tsx's node-rebuild
  // effect only re-fires when ITS OWN dependencies change identity,
  // and selectedIDsRef (a ref) never does that on its own. Wiring
  // selectedObjects itself into that effect instead would reopen the
  // exact React #185 loop this file's header comment already
  // documents: setNodes -> React Flow re-derives its own
  // selectedNodes -> onSelectionChange refires (even with unchanged
  // membership, a fresh array reference) -> setSelectedObjects again
  // -> setNodes again. A plain counter that ONLY selectObject touches
  // can't join that cycle.
  const [applyToken, setApplyToken] = useState(0)

  // The ONE place the shared selection (shared/atlasSelectionStore.ts,
  // goal 0346 slice B) is written from React Flow: this subscription,
  // never a per-node effect (goal 0161's render-count law).
  const onSelectionChange: OnSelectionChangeFunc = useCallback(({ nodes: selected, edges }) => {
    const ids = selected.map((n) => n.id)
    const pendingID = pendingProgrammaticIDRef.current
    if (pendingID !== null && ids.length === 0) return
    pendingProgrammaticIDRef.current = null
    selectedIDsRef.current = ids
    const selectedCardIDs = ids.filter((id) => cards.some((c) => c.ID === id))
    const selectedNoteIDs = ids.filter((id) => notes.some((n) => n.ID === id))
    const selectedObjectIDs = ids.filter((id) => objects.some((o) => o.ID === id))
    // React Flow reports again when a controlled nodes array changes.
    // Keep the existing state references when membership did not change
    // so that report cannot create a render -> nodes -> report loop.
    setSelectedCards((current) => retainEqualIDs(current, selectedCardIDs))
    setSelectedNotes((current) => retainEqualIDs(current, selectedNoteIDs))
    setSelectedObjects((current) => retainEqualIDs(current, selectedObjectIDs))
    const links = edges.map((e) => e.id).filter((id) => arteries.some((a) => a.id === id && a.count === 1))
    useAtlasSelectionStore.getState().setSelection({ spaceId, cards: selectedCardIDs, notes: selectedNoteIDs, objects: selectedObjectIDs, links })
  }, [cards, notes, objects, arteries, spaceId])

  // Leaving the board leaves nothing selected behind for the palette.
  useEffect(() => () => useAtlasSelectionStore.getState().clearSelection(), [])

  // The selection tray's own clear (Escape, or the tray's clear
  // affordance): resets every split so a stale ref can't reopen a
  // menu against members that no longer read as selected.
  const clearSelection = useCallback(() => {
    pendingProgrammaticIDRef.current = null
    selectedIDsRef.current = []
    setSelectedCards([])
    setSelectedNotes([])
    setSelectedObjects([])
    useAtlasSelectionStore.getState().clearSelection()
  }, [])

  // A programmatic single-selection (goal 0199): a discrete tool's
  // freshly placed object is left selected without any pointer event
  // ever reaching React Flow's own selection machinery. Written
  // straight into the ref/state pair React Flow's own onSelectionChange
  // would otherwise populate, then applyToken bumps to force AtlasBoard.tsx's
  // node-rebuild effect to re-read selectedIDsRef.current even though
  // allNodes itself may not have changed since the object was created.
  const selectObject = useCallback((id: string) => {
    pendingProgrammaticIDRef.current = id
    selectedIDsRef.current = [id]
    setSelectedCards([])
    setSelectedNotes([])
    setSelectedObjects([id])
    useAtlasSelectionStore.getState().setSelection({ spaceId, cards: [], notes: [], objects: [id], links: [] })
    setApplyToken((t) => t + 1)
  }, [spaceId])

  // The same programmatic single-selection, for a freshly landed NOTE
  // (goal 0218's paste-fallback note) -- kept as its own function
  // rather than a parameterized selectObject so the selection-tray
  // split (selectedNotes vs selectedObjects) stays correct for the
  // note's own delete/group affordances.
  const selectNote = useCallback((id: string) => {
    pendingProgrammaticIDRef.current = id
    selectedIDsRef.current = [id]
    setSelectedCards([])
    setSelectedNotes([id])
    setSelectedObjects([])
    useAtlasSelectionStore.getState().setSelection({ spaceId, cards: [], notes: [id], objects: [], links: [] })
    setApplyToken((t) => t + 1)
  }, [spaceId])

  // Snapshot the selection BEFORE React Flow's own handlers re-select
  // the pressed node -- unconditional (not button===2) because a macOS
  // ctrl+click context menu arrives as button 0; a stale snapshot from
  // an earlier press is harmless, since only the context-menu readers
  // consume it and every menu-opening press passes through here first.
  const snapshotSelection = useCallback(() => {
    contextSelectionRef.current = selectedIDsRef.current
  }, [])

  const openMultiMenu = useCallback((sel: string[], pos: { x: number; y: number }): boolean => {
    if (sel.length < 2) return false
    const cardIDs = sel.filter((id) => cards.some((c) => c.ID === id))
    const noteIDs = sel.filter((id) => notes.some((n) => n.ID === id))
    const objectIDs = sel.filter((id) => objects.some((o) => o.ID === id))
    onMultiSelectContextMenu(cardIDs, noteIDs, objectIDs, pos)
    return true
  }, [cards, notes, objects, onMultiSelectContextMenu])

  const onSelectionContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    openMultiMenu(selectedIDsRef.current, { x: e.clientX, y: e.clientY })
  }, [openMultiMenu])

  // For onNodeContextMenu: the pre-clear snapshot is the authoritative
  // selection, never the live (already re-selected) state. Returns
  // whether the multi menu opened, so the caller falls through to its
  // per-node menus otherwise.
  const tryNodeMultiMenu = useCallback((nodeID: string, pos: { x: number; y: number }): boolean => {
    const sel = contextSelectionRef.current
    if (!sel.includes(nodeID)) return false
    return openMultiMenu(sel, pos)
  }, [openMultiMenu])

  // The click model's own commit test (goal 0102's gesture table,
  // "click an already-selected card"): true when this node was the
  // SOLE selected node the instant this click gesture began -- the
  // same pre-select snapshot onNodeContextMenu reads above, reused so
  // two rapid plain clicks (select, then commit) and a real
  // double-click land on the identical outcome.
  const isSoleSelected = useCallback((id: string): boolean => {
    const sel = contextSelectionRef.current
    return sel.length === 1 && sel[0] === id
  }, [])

  return { selectedIDsRef, selectedCards, selectedNotes, selectedObjects, applyToken, onSelectionChange, snapshotSelection, onSelectionContextMenu, tryNodeMultiMenu, isSoleSelected, clearSelection, selectObject, selectNote }
}
