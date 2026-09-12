import { create } from 'zustand'
import { AtlasService } from '../shared/bindings'
import type { BoardObject, Card, Kind, Link, LinkKind, Note, Perspective } from '../../bindings/github.com/alicoding/mill/internal/domain/atlas/models'
import { background } from '../shared/background'

// The Atlas surface's own "one fetch, many consumers" store (mirrors
// shared/configureEntityStore.ts's shape) -- kept inside atlas/ rather
// than shared/ since only atlas/ itself and app/ (QuickPanel's card
// search, App.tsx's mill-data-changed router) ever read it, and
// dependency-cruiser's boundary lets app/ import from atlas/.
interface AtlasState {
  kinds: Kind[] | null
  linkKinds: LinkKind[] | null
  cards: Card[] | null
  links: Link[] | null
  // Notes (goal 0081 slice A1): its own family, deliberately never
  // merged into cards -- structural exclusion from every semantic
  // consumer (projections/matrix/coverage/jump) starts with them never
  // sharing a collection.
  notes: Note[] | null
  // Board objects (goal 0179/0180): board-local canvas nouns (image,
  // ink, ...) -- its own family, same "never merged into cards" posture
  // notes above already carries.
  objects: BoardObject[] | null
  // Perspectives (ADR-0041, goal 0095 slice 2): every perspective across
  // every space -- the switcher/board/membership surfaces scope by
  // SpaceID themselves, the same "return everything, caller filters"
  // contract Perspectives() itself documents.
  perspectives: Perspective[] | null
  setKinds: (kinds: Kind[]) => void
  setLinkKinds: (linkKinds: LinkKind[]) => void
  setCards: (cards: Card[]) => void
  setLinks: (links: Link[]) => void
  setNotes: (notes: Note[]) => void
  setObjects: (objects: BoardObject[]) => void
  setPerspectives: (perspectives: Perspective[]) => void
}

export const useAtlasStore = create<AtlasState>()((set) => ({
  kinds: null,
  linkKinds: null,
  cards: null,
  links: null,
  notes: null,
  objects: null,
  perspectives: null,
  setKinds: (kinds) => set({ kinds }),
  setLinkKinds: (linkKinds) => set({ linkKinds }),
  setCards: (cards) => set({ cards }),
  setLinks: (links) => set({ links }),
  setNotes: (notes) => set({ notes }),
  setObjects: (objects) => set({ objects }),
  setPerspectives: (perspectives) => set({ perspectives }),
}))

type AtlasCollection = 'kinds' | 'linkKinds' | 'cards' | 'links' | 'notes' | 'objects' | 'perspectives'

const requestGeneration: Record<AtlasCollection, number> = {
  kinds: 0,
  linkKinds: 0,
  cards: 0,
  links: 0,
  notes: 0,
  objects: 0,
  perspectives: 0,
}

function refreshLatest<T>(
  collection: AtlasCollection,
  request: () => Promise<T[] | null>,
  apply: (items: T[]) => void,
  source: string,
): Promise<void> {
  const generation = ++requestGeneration[collection]
  return background(request().then((list) => {
    if (generation === requestGeneration[collection]) apply(list ?? [])
  }), source)
}

export function refreshAtlasKinds(): Promise<void> {
  return refreshLatest('kinds', () => AtlasService.Kinds(), (list) => useAtlasStore.getState().setKinds(list), 'atlas.kinds')
}

export function refreshAtlasLinkKinds(): Promise<void> {
  return refreshLatest('linkKinds', () => AtlasService.LinkKinds(), (list) => useAtlasStore.getState().setLinkKinds(list), 'atlas.linkKinds')
}

export function refreshAtlasCards(): Promise<void> {
  return refreshLatest('cards', () => AtlasService.Cards(), (list) => useAtlasStore.getState().setCards(list), 'atlas.cards')
}

export function refreshAtlasLinks(): Promise<void> {
  return refreshLatest('links', () => AtlasService.Links(), (list) => useAtlasStore.getState().setLinks(list), 'atlas.links')
}

export function refreshAtlasNotes(): Promise<void> {
  return refreshLatest('notes', () => AtlasService.Notes(), (list) => useAtlasStore.getState().setNotes(list), 'atlas.notes')
}

export function refreshAtlasObjects(): Promise<void> {
  return refreshLatest('objects', () => AtlasService.Objects(), (list) => useAtlasStore.getState().setObjects(list), 'atlas.objects')
}

export function refreshAtlasPerspectives(): Promise<void> {
  return refreshLatest('perspectives', () => AtlasService.Perspectives(), (list) => useAtlasStore.getState().setPerspectives(list), 'atlas.perspectives')
}

// The one call site every mounter of the Atlas surface (AtlasView on
// mount, App.tsx/QuickPanel.tsx's mill-data-changed 'atlas' handler)
// uses -- refetches all seven entity families together since they're
// one storage blob server-side (atlassvc's atlasStateKey) and a single
// dataevent.Emit("atlas", ...) never says which family actually
// changed.
export function refreshAtlas(): Promise<void> {
  return Promise.all([refreshAtlasKinds(), refreshAtlasLinkKinds(), refreshAtlasCards(), refreshAtlasLinks(), refreshAtlasNotes(), refreshAtlasObjects(), refreshAtlasPerspectives()]).then(() => undefined)
}

// scheduleAtlasRefresh is the EVENT-DRIVEN door (goal 0147): every
// mill-data-changed listener routes through this trailing debounce so
// a burst (a workflow run writing N cards/rows = N events) costs ONE
// six-collection refetch, not N. Direct post-user-action callers keep
// calling refreshAtlas() for immediacy.
let atlasRefreshTimer: ReturnType<typeof setTimeout> | null = null

export function scheduleAtlasRefresh(): void {
  if (atlasRefreshTimer !== null) return
  atlasRefreshTimer = setTimeout(() => {
    atlasRefreshTimer = null
    void refreshAtlas()
  }, 150)
}
