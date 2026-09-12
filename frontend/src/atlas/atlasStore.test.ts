import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useBackgroundFailureStore } from '../shared/background'

const serviceMocks = vi.hoisted(() => ({
  kinds: vi.fn(),
  linkKinds: vi.fn(),
  cards: vi.fn(),
  links: vi.fn(),
  notes: vi.fn(),
  objects: vi.fn(),
  perspectives: vi.fn(),
}))

vi.mock('../shared/bindings', () => ({
  AtlasService: {
    Kinds: serviceMocks.kinds,
    LinkKinds: serviceMocks.linkKinds,
    Cards: serviceMocks.cards,
    Links: serviceMocks.links,
    Notes: serviceMocks.notes,
    Objects: serviceMocks.objects,
    Perspectives: serviceMocks.perspectives,
  },
}))

import {
  refreshAtlasCards,
  refreshAtlasKinds,
  refreshAtlasLinkKinds,
  refreshAtlasLinks,
  refreshAtlasNotes,
  refreshAtlasObjects,
  refreshAtlasPerspectives,
  useAtlasStore,
} from './atlasStore'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const refreshCases = [
  { name: 'kinds', request: serviceMocks.kinds, refresh: refreshAtlasKinds, read: () => useAtlasStore.getState().kinds },
  { name: 'link kinds', request: serviceMocks.linkKinds, refresh: refreshAtlasLinkKinds, read: () => useAtlasStore.getState().linkKinds },
  { name: 'cards', request: serviceMocks.cards, refresh: refreshAtlasCards, read: () => useAtlasStore.getState().cards },
  { name: 'links', request: serviceMocks.links, refresh: refreshAtlasLinks, read: () => useAtlasStore.getState().links },
  { name: 'notes', request: serviceMocks.notes, refresh: refreshAtlasNotes, read: () => useAtlasStore.getState().notes },
  { name: 'objects', request: serviceMocks.objects, refresh: refreshAtlasObjects, read: () => useAtlasStore.getState().objects },
  { name: 'perspectives', request: serviceMocks.perspectives, refresh: refreshAtlasPerspectives, read: () => useAtlasStore.getState().perspectives },
]

describe('Atlas refresh ordering', () => {
  beforeEach(() => {
    for (const mock of Object.values(serviceMocks)) mock.mockReset()
    useAtlasStore.setState({
      kinds: null,
      linkKinds: null,
      cards: null,
      links: null,
      notes: null,
      objects: null,
      perspectives: null,
    })
    useBackgroundFailureStore.setState({ failures: {} })
  })

  it.each(refreshCases)('keeps the latest requested $name response', async ({ request, refresh, read }) => {
    const older = deferred<unknown[]>()
    const newer = deferred<unknown[]>()
    const olderValue = { ID: 'older' }
    const newerValue = { ID: 'newer' }
    request.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)

    const olderRefresh = refresh()
    const newerRefresh = refresh()

    newer.resolve([newerValue])
    await newerRefresh
    expect(read()).toEqual([newerValue])

    older.resolve([olderValue])
    await olderRefresh
    expect(read()).toEqual([newerValue])
  })

  it('records a rejected latest request without reviving an older response', async () => {
    const older = deferred<unknown[]>()
    const latest = deferred<unknown[]>()
    serviceMocks.objects.mockReturnValueOnce(older.promise).mockReturnValueOnce(latest.promise)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)

    try {
      const olderRefresh = refreshAtlasObjects()
      const latestRefresh = refreshAtlasObjects()

      latest.reject(new Error('latest failed'))
      await latestRefresh
      expect(useBackgroundFailureStore.getState().failures['atlas.objects']).toBe(1)
      expect(useAtlasStore.getState().objects).toBeNull()

      older.resolve([{ ID: 'older' }])
      await olderRefresh
      expect(useAtlasStore.getState().objects).toBeNull()
    } finally {
      warn.mockRestore()
    }
  })
})
