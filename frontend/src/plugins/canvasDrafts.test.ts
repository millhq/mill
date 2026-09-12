import { beforeEach, describe, expect, it, vi } from 'vitest'

const beginUndoMark = vi.hoisted(() => vi.fn(() => Promise.resolve()))
const endUndoMark = vi.hoisted(() => vi.fn(() => Promise.resolve()))
vi.mock('../shared/bindings', () => ({ AtlasService: { BeginUndoMark: beginUndoMark, EndUndoMark: endUndoMark } }))

import { CanvasProtocolError } from './canvasToolProtocol'
import { commitDraft, createDraft, discardDraft, dropDraftsFor, liveDraftFor, patchDraft, useCanvasDrafts } from './canvasDrafts'

// A draft is the in-progress object (docs/goals/0380 Decisions 1 and
// 6). Three properties are what the design rests on: a patch never
// reaches the board, a commit is exactly ONE undo entry however many
// frames the drag took, and a discard leaves the board as it was.

const CREATE = { toolId: 'pencil', kind: 'ink', at: { x: 10, y: 20 }, data: { title: 'Sketch' }, preview: { trail: '' } }

function placement() {
  return { place: vi.fn(() => Promise.resolve('object-1')), select: vi.fn() }
}

function noPlacement() {
  return { place: () => Promise.resolve(null), select: () => {} }
}

function drafts() {
  return useCanvasDrafts.getState().drafts
}

beforeEach(() => {
  useCanvasDrafts.setState({ drafts: {} })
  beginUndoMark.mockClear()
  endUndoMark.mockClear()
})

describe('canvas drafts', () => {
  it('patches live without touching the board, and merges rather than replacing', async () => {
    const place = placement()
    const draft = createDraft('mill-drawing', 'pencil', CREATE, place)
    patchDraft('mill-drawing', { id: draft.id, preview: { trail: 'M0 0' } })
    patchDraft('mill-drawing', { id: draft.id, at: { x: 1, y: 2 }, size: { w: 40, h: 40 } })
    const live = drafts()[draft.id]
    expect(live.data).toEqual({ title: 'Sketch' })
    expect(live.preview).toEqual({ trail: 'M0 0' })
    expect(live.at).toEqual({ x: 1, y: 2 })
    expect(live.size).toEqual({ w: 40, h: 40 })
    expect(place.place).not.toHaveBeenCalled()
    expect(beginUndoMark).not.toHaveBeenCalled()
  })

  it('lands a commit as one undo entry and drops the draft with it', async () => {
    const place = placement()
    const draft = createDraft('mill-drawing', 'pencil', CREATE, place)
    patchDraft('mill-drawing', { id: draft.id, preview: { trail: 'M0 0' } })
    patchDraft('mill-drawing', { id: draft.id, preview: { trail: 'M0 0 L1 1' } })
    await expect(commitDraft('mill-drawing', draft.id, true)).resolves.toBe('object-1')
    expect(beginUndoMark).toHaveBeenCalledTimes(1)
    expect(endUndoMark).toHaveBeenCalledTimes(1)
    expect(place.place).toHaveBeenCalledWith(expect.objectContaining({ at: { x: 10, y: 20 } }))
    expect(drafts()[draft.id]).toBeUndefined()
  })

  it('opens the undo mark before issuing the placement mutation', async () => {
    let openMark: (() => void) | undefined
    beginUndoMark.mockImplementationOnce(() => new Promise<void>((resolve) => { openMark = resolve }))
    const place = placement()
    const draft = createDraft('mill-drawing', 'shape', CREATE, place)
    const commit = commitDraft('mill-drawing', draft.id, false)
    await Promise.resolve()
    expect(place.place).not.toHaveBeenCalled()
    openMark?.()
    await commit
    expect(place.place).toHaveBeenCalledTimes(1)
  })

  // Closing the mark refreshes the board, and a selection made before
  // that refresh does not survive it -- the placed shape would come
  // back unselected, with no resize handles to grab.
  it('selects the placed object only after the undo mark has closed', async () => {
    const order: string[] = []
    endUndoMark.mockImplementation(() => { order.push('mark closed'); return Promise.resolve() })
    const place = { place: () => Promise.resolve('object-1'), select: () => { order.push('selected') } }
    const draft = createDraft('mill-drawing', 'shape', CREATE, place)
    await commitDraft('mill-drawing', draft.id, true)
    expect(order).toEqual(['mark closed', 'selected'])
    endUndoMark.mockImplementation(() => Promise.resolve())
  })

  // A placement's select() refreshes the board before touching the
  // selection door (canvasToolFramed.ts) -- commitDraft must await
  // that promise rather than fire it and resolve, or a caller awaiting
  // the commit (this test, a chained placement) can observe the
  // object placed but the refresh-then-select still in flight.
  it('awaits select fully before the commit itself resolves', async () => {
    const order: string[] = []
    const place = {
      place: () => Promise.resolve('object-1'),
      select: () => {
        order.push('select started')
        return Promise.resolve().then(() => { order.push('select settled') })
      },
    }
    const draft = createDraft('mill-drawing', 'shape', CREATE, place)
    await commitDraft('mill-drawing', draft.id, true)
    expect(order).toEqual(['select started', 'select settled'])
  })

  it('closes the undo mark even when the placement itself fails', async () => {
    const draft = createDraft('mill-drawing', 'pencil', CREATE, { place: () => Promise.reject(new Error('nope')), select: () => {} })
    await expect(commitDraft('mill-drawing', draft.id, false)).rejects.toThrow('nope')
    expect(endUndoMark).toHaveBeenCalledTimes(1)
  })

  it('leaves the board untouched on discard', async () => {
    const place = placement()
    const draft = createDraft('mill-drawing', 'pencil', CREATE, place)
    discardDraft('mill-drawing', draft.id)
    expect(drafts()[draft.id]).toBeUndefined()
    expect(place.place).not.toHaveBeenCalled()
    expect(beginUndoMark).not.toHaveBeenCalled()
  })

  // An id from another extension is exactly as untrusted as nonsense:
  // both are refused by name with nothing changed.
  it('refuses a draft the asking extension does not own, without side effects', () => {
    const draft = createDraft('mill-drawing', 'pencil', CREATE, noPlacement())
    expect(() => patchDraft('other-plugin', { id: draft.id, preview: { trail: 'M0 0' } })).toThrow(CanvasProtocolError)
    expect(() => discardDraft('other-plugin', draft.id)).toThrow(CanvasProtocolError)
    expect(() => patchDraft('mill-drawing', { id: 'no-such-draft', data: {} })).toThrow(CanvasProtocolError)
    expect(drafts()[draft.id].preview).toEqual({ trail: '' })
  })

  it('clears what an unloaded extension left half-drawn', () => {
    const mine = createDraft('mill-drawing', 'pencil', CREATE, noPlacement())
    const theirs = createDraft('other-plugin', 'pencil', CREATE, noPlacement())
    dropDraftsFor('mill-drawing')
    expect(drafts()[mine.id]).toBeUndefined()
    expect(drafts()[theirs.id]).toBeDefined()
  })

  it('answers the armed tool’s own live draft, which is what the preview draws from', () => {
    const draft = createDraft('mill-drawing', 'pencil', CREATE, noPlacement())
    expect(liveDraftFor(drafts(), 'pencil')?.id).toBe(draft.id)
    expect(liveDraftFor(drafts(), 'shape')).toBeNull()
  })
})
