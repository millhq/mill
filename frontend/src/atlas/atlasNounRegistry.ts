import type { BoardObject } from '../../bindings/github.com/alicoding/mill/internal/domain/atlas/models'
import type { CanvasObjectExample } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import type { EnclosedIDs, Rect } from './atlasEnclosure'
import type { ComponentType } from 'react'
import type { Icon } from '@primer/octicons-react'
import { ATLAS_TOOL_IDENTITIES, type AtlasToolIdentity, type AtlasToolInteraction } from '../shared/atlasToolIdentity'
import type { AtlasStyleField } from './atlasStyleVocabulary'
import type { FrameBox } from './useAtlasDragFiling'
import { registerBoardObjectContent, unregisterBoardObjectContent, type AtlasBoardObjectKind, type AtlasNounContent } from './atlasBoardObjectContent'

// The board-object CONTENT registry (AtlasNounContent, ExtensionRowMeta,
// registerBoardObjectContent, toolLessNounExtensions, ...) lives in its
// own file, atlasBoardObjectContent.ts (architecture.md's 500-line file
// limit split this registry's own real seam along) -- re-exported here
// IN FULL so every existing `from '../atlasNounRegistry'` import keeps
// resolving unchanged; only this file and atlasBoardObjectContent.ts
// know the split happened.
export * from './atlasBoardObjectContent'

// The frontend twin of composition/registry.go's RegisterNodeType
// (ADR-0006, goal 0180 slice 1): each canvas noun's own fat descriptor
// (icon, style picker, commit path) lives in its own
// frontend/src/atlas/tools/<id>Tool.ts file and calls registerNoun()
// at module-eval time -- atlasTools.ts discovers every one of them via
// import.meta.glob(..., { eager: true }) rather than holding a literal
// array every noun is appended to.
export type { AtlasToolInteraction }

// AtlasNounGroup -- which creation-dock cluster a noun belongs to
// (goal 0355), shared by AtlasToolShapeBase.group below AND
// ExtensionRowMeta.group -- one union, so a tool-bearing noun and a
// tool-less noun declare the SAME four values rather than two
// independently-typed fields that could drift apart. Settings >
// Extensions' section grouping (views/ExtensionsSection.tsx) reads
// this field off every row, tool-bearing or not, never a hand-curated
// per-id list.
//
// The dock is FIXED at seven slots and never grows per installed tool
// (goal 0355): 'objects' fills the four named object slots, 'media'
// the Media slot, 'annotate' the Annotate slot, and everything else --
// including every plugin face that declares nothing -- is reachable
// through the More panel's search. atlasToolPlacement.ts is the ONE
// place that maps this field onto a slot.
export type AtlasNounGroup = 'objects' | 'media' | 'annotate' | 'embed'

// Session-only cache seeding a newly created object's own style
// (colour/size, ...) -- never persisted document data.
export type AtlasToolStyleDefaults = Record<string, unknown>

// The React Flow node TYPE (atlasBoardNodeTypes.ts's own rfNodeTypes
// keys, restated as a literal union here rather than imported -- that
// file eagerly imports every node RENDERER component, and this
// registry must stay a light, dependency-free descriptor layer) that
// renders this noun's own placed instance -- null for a tool whose
// gesture never persists anything a node could render at all (eraser
// destroys state, laser is a local-only overlay). goal 0181 S3's own
// join key: the surface-conformance tests below use it to find which
// renderer source a `resizable: true` answer must hold true against.
export type AtlasBoardNodeType = 'atlas-note' | 'atlas-sticky' | 'atlas-group' | 'atlas-object' | null

// ExtensionSettingDecl / ExtensionSettingValue live in
// shared/extensionSettingDecl.ts (shared/ is a dependency leaf and the
// settings store there resolves against the declaration); re-exported
// here so a noun declares its settings from the same module it
// declares everything else.
import type { ExtensionSettingDecl } from '../shared/extensionSettingDecl'
export type { ExtensionSettingDecl, ExtensionSettingValue } from '../shared/extensionSettingDecl'

interface AtlasToolShapeBase {
  icon: Icon
  // label: the command/button text, sourced from identityOf(id).commandLabel
  // (a verb phrase -- "Add a note", "Draw a shape") -- read by the
  // command palette (shared/commands.ts) and every tray tooltip/aria-label.
  // NEVER read by Settings > Extensions' row title (goal 0237 S3's
  // review rider) -- that reads nounName below instead, since a single
  // field can't honestly serve both "what click does" (a verb) and
  // "what this thing is called" (a noun) at once.
  label: string
  // nounName: the bare noun a user would call this thing -- "Card",
  // "Note", "Pencil" -- read ONLY by Settings > Extensions' row title
  // (views/extensionMeta.ts's toolRowSource). A tool-less noun has no
  // separate field for this at all: its own `extension.label`
  // (ExtensionRowMeta) already IS the noun, since it has no command
  // verb phrase to disambiguate from in the first place.
  nounName: string
  // description (goal 0211's plugin-manager UX slice): a one-sentence,
  // user-vocabulary summary of what this noun does, read by the
  // Extensions section's per-row disclosure (views/ExtensionsSection.tsx,
  // views/extensionMeta.ts's descriptionLabel). Optional -- a noun that
  // hasn't been given one yet still compiles and falls back to `label`
  // there rather than rendering nothing.
  description?: string
  shortcutKey: string | null
  tray: 'quick' | 'palette'
  // group (goal 0355): which creation-dock cluster this noun belongs to
  // -- 'objects' (Card/Note/Area/Table: the things a board is made of),
  // 'media' (Image and every file-backed artifact that primarily
  // arrives by drop/import, sharing the dock's one Media flyout),
  // 'annotate' (the freehand-marking family, sharing the Annotate
  // flyout), or 'embed' (a face a plugin brings, reachable through the
  // More panel). REQUIRED, the same honest-declaration shape
  // lockable/resizable/sticky already establish, so a new tool declares
  // its own cluster instead of silently landing wherever an array
  // happens to iterate to. atlasToolPlacement.ts maps this field onto a
  // dock slot -- the dock's own visible order is fixed and never grows
  // per installed tool, so a fifth 'objects' tool is panel-only by
  // design, keeping the four letter chips stable.
  group: AtlasNounGroup
  // settings (goal 0258): this noun's own declared user settings,
  // rendered generically by Settings > Extensions' row disclosure
  // (views/ExtensionRow.tsx) and persisted centrally
  // (shared/extensionSettingsStore.ts) -- the extension declares, the
  // host renders and stores, so no extension ever ships its own
  // settings page. Optional: most nouns have none, and an absent
  // array renders no settings block at all.
  settings?: readonly ExtensionSettingDecl[]
  styleDefaults?: AtlasToolStyleDefaults
  // styleFields (goal 0209): this noun's own declared styleable
  // properties, drawn from atlasStyleVocabulary.ts's closed
  // property-type vocabulary -- REQUIRED (never optional), the same
  // honest-false/empty pattern lockable/resizable/dragBand below
  // establish. An empty array is the honest answer for a noun with no
  // style surface at all (every noun but shape/pencil this slice), not
  // an omission. AtlasCreationTray.tsx's own 'drag-to-draw' branch
  // renders AtlasStylePanel.tsx anchored to the tool's button whenever
  // this array is non-empty -- registry-driven, so a THIRD noun
  // gaining a style surface never needs a hardcoded branch naming it
  // by id (docs/goals/0211-extension-tiers.md's standing rule: no core
  // file enumerates which nouns are styleable).
  styleFields: readonly AtlasStyleField[]
  // goal 0181 S3's own three declarations, one per surface that shipped
  // half-wired without one -- REQUIRED (never optional) so a noun that
  // omits one fails to compile rather than half-existing. `false`/`null`
  // are legitimate, honest answers, never omissions.
  //
  // lockable: does re-clicking this tool's OWN already-armed tray
  // button lock it for deliberate repeated placement (the Excalidraw
  // convention, goal 0199) instead of disarming on the second click?
  // Only meaningful for an armable tool (AtlasArmableTool, atlasTools.ts);
  // a non-armable tool (table, image) still declares it -- always false,
  // since neither one's own arming state ever reads a lock flag at all
  // (they arm through a picker/popover, never the toggleArm state
  // machine `isLockableArmTool` below reads).
  lockable: boolean
  // resizable: can the user drag this noun's own placed instance to a
  // new size via the shared NodeResizer (goal 0193 -- no board object
  // was resizable until this existed anywhere)? A container that
  // auto-fits its own children (area) and a tool that never persists an
  // instance (eraser, laser) both legitimately declare false.
  resizable: boolean
  // boardNodeType: which shared React Flow node type renders this
  // noun's placed instance -- see AtlasBoardNodeType above for why this
  // exists.
  boardNodeType: AtlasBoardNodeType
  // dragBand (goal 0206): does this noun's own placed instance need the
  // shared 'atlas-object' renderer's chrome band as its drag surface? A
  // Kind's whole body already drags UNLESS its own content captures
  // pointer events (a table's grid, a diagram's vendored pan/zoom) --
  // rendering the band on a Kind that doesn't need it reads as floating
  // debris, not an affordance (the goal's own defect 2). Only meaningful
  // for a noun whose boardNodeType is 'atlas-object'; every other noun
  // still declares it -- always false, since it has no shared band to
  // opt into at all. diagram carries the same true answer as table but
  // has no tray descriptor of its own (drop-only, goal 0179 S2) -- it
  // can't declare this field and is exempted from this registry's own
  // conformance check the same way it already is for resizable/
  // boardNodeType above; AtlasBoardObjectNode.tsx's own dragBand
  // constant states its true answer directly, and atlas-diagram-object.spec.ts
  // proves it live since the static check can't reach it.
  dragBand: boolean
  // fileBacked (goal 0232 S1): does this noun's own placed instance
  // read Payload.mirrorPath as a real external file (image/ink/diagram/
  // sheet), or is its render entirely live Payload/List data with no
  // backing file at all (shape/table, and every noun with boardObjectKind
  // null)? REQUIRED for every tool, the same "declare honestly even
  // when meaningless" shape dragBand/resizable/lockable/sticky already
  // establish -- registerNoun below folds it into the registry's own
  // AtlasBoardObjectContent the same way it folds dragBand.
  fileBacked: boolean
  // boardObjectKind (goal 0215 S3): the persisted BoardObject.Kind this
  // tool's own placed instance carries, or null for a tool that never
  // routes through the shared 'atlas-object' renderer (card/note/area
  // persist as their own Kind-less node types). Exists because a
  // tool's own id is not always its Kind (the Drawing plugin's pencil
  // places Kind 'ink') -- content below resolves purely off this
  // field, via registerBoardObjectContent, never off id.
  boardObjectKind: AtlasBoardObjectKind | null
  // content (goal 0215 S3): this noun's own placed-instance content
  // contribution -- registerNoun below feeds it straight into
  // boardObjectContentFor's registry when boardObjectKind is non-null,
  // killing AtlasBoardObjectNode.tsx's former per-Kind hand branch.
  // null exactly when boardObjectKind is null, never omitted.
  content: AtlasNounContent | null
  // capabilities (ADR-0047's declared-capability set): the external
  // reach this noun's own manifest declares. No current noun sets it --
  // every noun today reaches nothing outside Mill -- so the Extensions
  // section's reach line (views/extensionMeta.ts's reachLabel) derives
  // an honest "Reaches nothing outside Mill." from its absence instead
  // of a hardcoded string, and lists these values verbatim once a noun
  // actually declares some. Deliberately a plain string list, not an
  // enum -- the capability vocabulary itself is undesigned here (ADR-0047
  // defers it to when the compiled-in plugin tier is built).
  capabilities?: readonly string[]
  // sticky (goal 0215 S2): does this tool stay armed after a completed
  // gesture (pencil/eraser/laser -- repeated strokes/passes are the
  // point), or disarm after one (area, and shape via its OWN lockable
  // flag)? REQUIRED for every tool, not just the five drag ones -- a
  // non-drag tool (card/note/table/image) declares false honestly,
  // since it never reads this at all (same pattern as dragBand above).
  // useAtlasToolGesture.ts's gestureDisarmFns reads this to decide
  // whether a gesture's own onEnd may call ctx.disarm/disarmUnlessLocked
  // at all -- a sticky tool gets no-ops for both, so it cannot disarm
  // itself even if its own onEnd tried.
  sticky: boolean
  // gesture (goal 0215 S2): the ONE seam a drag-shaped tool declares
  // instead of hand-rolling its own capture-phase pointer hook --
  // useAtlasToolGesture.ts is the sole engine that reads this, and the
  // sole site left calling preventDefault/stopPropagation for a canvas
  // tool. null for every tool whose interaction never drags (card/note/
  // table/image), never omitted.
  gesture: AtlasToolGesture | null
  // Each concrete tool's own commit signature differs (a card commits
  // kind+title, a table mints a backing List); this base only has to
  // accept every one of them for the registry's own element type to
  // work, never call through it generically.
  commit: (input: never) => unknown
}

// AtlasGesturePoint -- one accumulated point of an in-flight gesture,
// always carrying its own capture timestamp so an ephemeral tool
// (laser's fadeMs) can age individual points out independently; every
// other tool simply ignores `t`.
export interface AtlasGesturePoint { x: number; y: number; t: number }

// AtlasGestureCtx -- what a tool's own gesture.onPoint/onEnd may reach,
// assembled fresh each render by AtlasBoard.tsx and threaded through by
// the engine. Deliberately NOT the wrapper box or React Flow's own
// screenToFlowPosition internals beyond the function itself -- kept to
// exactly what the five hooks this contract replaces actually consumed
// (goal 0215 S2 design lock item 1).
export interface AtlasGestureCtx {
  screenToFlowPosition: (p: { x: number; y: number }) => { x: number; y: number }
  parentID: string
  cardBoxes: FrameBox[]
  noteBoxes: { id: string; x: number; y: number; width: number; height: number }[]
  // Every board-local object's (ink/shape/image/table/diagram) own
  // rendered flow-space box -- read off React Flow's own measured node
  // state (goal 0230), since a BoardObject's persisted Size stays null
  // until first resize and its rendered footprint is otherwise CSS-
  // intrinsic (atlasBuildBoardObjectNodes.ts's own header comment).
  objectBoxes: { id: string; x: number; y: number; width: number; height: number }[]
  // The spatial-query door (goal 0310): which top-level cards, notes
  // and objects a board-space rect encloses (atlasEnclosure.ts's own
  // center-inside rule) -- the Area tool's and a plugin gesture's one
  // shared answer.
  enclosedIn: (rect: Rect) => EnclosedIDs
  onDeleteSelection: (cardIDs: string[], noteIDs: string[], objectIDs: string[]) => void
  openAreaPopover: (screenPos: { x: number; y: number }, flowPos: { x: number; y: number }, enclosedCardIDs: string[], enclosedNoteIDs: string[], enclosedObjectIDs: string[]) => void
  onShapeCreated: (objectID: string) => void
  // Real functions for a one-shot tool; no-ops for a sticky one (the
  // engine's own gestureDisarmFns enforces this, not each tool).
  disarm: () => void
  disarmUnlessLocked: () => void
  // Fresh per-gesture scratch space the engine allocates at pointerdown
  // and discards after onEnd -- eraser's own onPoint is the sole
  // consumer today; no other tool touches it.
  hitAccumulator: { cardIDs: Set<string>; noteIDs: Set<string>; objectIDs: Set<string> }
  // The modifier keys held for the pointer event this call came from
  // (docs/goals/0380). Shift-to-constrain is a converged drawing
  // expectation, and a tool running outside this document has no event
  // of its own to read it off, so the engine carries it.
  modifiers: { shift: boolean; alt: boolean; ctrl: boolean; meta: boolean }
}

// AtlasToolGesture -- a drag-shaped tool's own pure behavior
// contribution. onEnd receives the FULL client-space point list
// unconditionally (even a below-threshold stray click) -- deciding
// whether that constitutes a real gesture (a distance threshold, a
// hit count, or nothing at all) is each tool's own call, matching how
// the five hooks this contract replaces each guarded their own commit
// differently (eraser's own guard is "did we hit anything", never a
// distance).
export interface AtlasToolGesture {
  onPoint?: (pt: AtlasGesturePoint, ctx: AtlasGestureCtx) => void
  onEnd: (points: AtlasGesturePoint[], ctx: AtlasGestureCtx) => void | Promise<void>
  ownsUndo?: boolean // Canvas-draft commits own their actor mark; the engine must not nest one.
  // AtlasBoard renders this from the engine's wrapper-local points in its one overlay slot.
  preview?: ComponentType<{ points: AtlasGesturePoint[]; now: number }>
  // Ephemeral tools (laser) never commit -- their accumulated points
  // fade out on their own timer instead of clearing at pointerup, the
  // one generic mechanism useAtlasToolGesture.ts owns for an
  // 'ephemeral-drag' tool so no tool needs its own rAF loop.
  fadeMs?: number
  // onCancel/onFade are the two gesture boundaries a tool running
  // OUTSIDE this document cannot observe for itself (docs/goals/0380):
  // an abandoned gesture (Escape), and each aging frame of an
  // ephemeral trail. A same-DOM tool reads both off its own state and
  // declares neither.
  onCancel?: () => void
  onFade?: (now: number) => void
}

// AtlasToolShape: a discriminated union, one member per
// shared/atlasToolIdentity.ts entry, correlating id<->interaction the
// same way the pre-registry hand-written ATLAS_TOOLS tuple did --
// mapped MECHANICALLY over AtlasToolIdentity['id'] (never over a
// per-noun file list), so a consumer that narrows on `interaction`
// (AtlasCreationTray.tsx discriminates its drag-to-draw/pick-then-
// place/paste-or-drop tray branches) can still read a correlated,
// literal `id` back out afterwards. A plain `{ id: string; interaction:
// AtlasToolInteraction }` shape would satisfy every individual noun
// file fine but silently stop narrowing at every CONSUMER of the
// runtime ATLAS_TOOLS array -- the same "collapses to string, invisible
// until something misroutes" trap one level down (see atlasTools.ts's
// own header for the top-level version).
export type AtlasToolShape = {
  [ID in AtlasToolIdentity['id']]: AtlasToolShapeBase & {
    id: ID
    interaction: Extract<AtlasToolIdentity, { id: ID }>['interaction']
  }
}[AtlasToolIdentity['id']]

const registry = new Map<string, AtlasToolShape>()

// registerNoun -- called once, at module-eval time, from each noun's
// own tools/<id>Tool.ts. Throws on a duplicate id so two files can
// never silently overwrite each other's registration. A descriptor
// that declares boardObjectKind also feeds its content contribution
// into registerBoardObjectContent here -- a tool-bearing noun's board
// rendering registers through the SAME door a tool-less noun
// (diagramNoun.ts) calls directly, never a second mechanism.
export function registerNoun(descriptor: AtlasToolShape): void {
  if (registry.has(descriptor.id)) {
    throw new Error(`atlas noun "${descriptor.id}" registered twice. Check frontend/src/atlas/tools/`)
  }
  registry.set(descriptor.id, descriptor)
  if (descriptor.boardObjectKind) {
    if (!descriptor.content) {
      throw new Error(`atlas noun "${descriptor.id}" declares boardObjectKind "${descriptor.boardObjectKind}" but content: null`)
    }
    registerBoardObjectContent(descriptor.boardObjectKind, { ...descriptor.content, dragBand: descriptor.dragBand, fileBacked: descriptor.fileBacked })
  }
}

// identityOf -- the one lookup every noun's own descriptor file uses to
// source its id/shortcutKey/label/interaction from
// shared/atlasToolIdentity.ts rather than restating them.
export function identityOf<ID extends AtlasToolIdentity['id']>(id: ID): Extract<AtlasToolIdentity, { id: ID }> {
  const found = ATLAS_TOOL_IDENTITIES.find((t): t is Extract<AtlasToolIdentity, { id: ID }> => t.id === id)
  if (!found) throw new Error(`no atlas tool identity registered for "${id}"`)
  return found
}

// The agreement check (goal 0180 S1's own conformance mechanism, and
// goal 0181's first concrete instance): every identity in
// shared/atlasToolIdentity.ts must have exactly one registered
// descriptor, and every registered descriptor must have a matching
// identity. Called from atlasTools.ts once every tools/*.ts module has
// been eagerly imported, so a noun that half-exists on either side
// fails at module-eval time (surfacing in every test/dev/build that
// imports atlasTools.ts) instead of silently misrouting at runtime.
export function assertRegistryAgreesWithIdentity(): void {
  const identityIDs = ATLAS_TOOL_IDENTITIES.map((i) => i.id)
  for (const id of identityIDs) {
    if (!registry.has(id)) {
      throw new Error(`atlas noun "${id}" has an identity (shared/atlasToolIdentity.ts) but no registered descriptor. Add frontend/src/atlas/tools/${id}Tool.ts calling registerNoun()`)
    }
  }
  for (const id of registry.keys()) {
    if (!identityIDs.includes(id as AtlasToolIdentity['id'])) {
      throw new Error(`atlas noun "${id}" registered a descriptor (frontend/src/atlas/tools/) but has no identity in shared/atlasToolIdentity.ts`)
    }
  }
}

// orderedRegisteredTools -- ATLAS_TOOLS' own tray render order comes
// from ATLAS_TOOL_IDENTITIES (declared once, in the order the tray
// renders), never from Map insertion order (which would follow
// import.meta.glob's own alphabetical file-path sort and silently
// reorder the tray the next time a noun's filename changes).
export function orderedRegisteredTools(): AtlasToolShape[] {
  const builtIns = ATLAS_TOOL_IDENTITIES.map((i) => {
    const found = registry.get(i.id)
    if (!found) throw new Error(`atlas noun "${i.id}" missing its registered descriptor`)
    return found
  })
  return [...builtIns, ...thirdPartyRegistry.values()] as AtlasToolShape[]
}

// --- Third-party nouns (docs/goals/0249, ADR-0047's out-of-tree tier) ---
//
// A runtime-loaded plugin's canvas object registers here, through the
// SAME conceptual door built-ins use, with two honest differences:
// its id/kind are open strings (the built-in literal unions stay
// closed and keep guarding built-ins), and it is exempt from
// assertRegistryAgreesWithIdentity (which checks exactly the
// shared/atlasToolIdentity.ts list, where a runtime id cannot appear).
// orderedRegisteredTools appends third-party tools AFTER every
// built-in, cast into the AtlasToolShape array at this ONE site:
// consumers discriminate on `interaction` and read string fields
// generically; a consumer comparing `id` against a built-in literal
// simply never matches a plugin id, which is the correct behavior.
export type ThirdPartyNounShape = Omit<AtlasToolShapeBase, 'boardObjectKind'> & {
  id: string
  // Widened past 'arm-then-click' by goal 0252 S1: a plugin tool may
  // be drag-shaped, riding the SAME gesture engine built-ins do (its
  // `gesture` field carries the host-adapted contribution). The click
  // placement door (atlasThirdPartyPlacement.ts) gates on this.
  interaction: 'arm-then-click' | 'drag-to-draw' | 'ephemeral-drag'
  boardObjectKind: string
  thirdParty: true
  // The owning plugin (manifest id) -- the Extensions page's join key.
  pluginId: string
  defaultPayload: Record<string, string>
  // Dropped-file extensions this object claims (docs/goals/0251),
  // joined from the manifest's contributes by the host -- lowercased
  // ".ext" entries the drop router compares against extensionOf().
  fileExtensions: string[]
  // menuItems (goal 0280): the plugin's own context-menu items for
  // objects of this kind, validated and bound to the object ctx by
  // plugins/canvasToolAdapter.ts, read by atlas/useAtlasObjectMenu.ts.
  menuItems: readonly ThirdPartyMenuItem[]
  // example (goal 0411 S2): this kind's own declared working example,
  // read straight off the manifest's contributed canvasObjects entry
  // (buildThirdPartyNoun) -- null for a kind that ships none.
  // atlasThirdPartyPlacement.ts's placeThirdPartyObject reads this to
  // materialize an empty-payload insert before the object's first
  // render; a non-null value here is otherwise inert.
  example: CanvasObjectExample | null
}

export interface ThirdPartyMenuItem {
  id: string
  label: string
  run: (object: BoardObject) => void
  enabled: (object: BoardObject) => boolean
}

const thirdPartyRegistry = new Map<string, ThirdPartyNounShape>()

export function registerThirdPartyNoun(shape: ThirdPartyNounShape): void {
  if (registry.has(shape.id) || thirdPartyRegistry.has(shape.id)) {
    throw new Error(`canvas object kind "${shape.id}" is already registered`)
  }
  thirdPartyRegistry.set(shape.id, shape)
  if (shape.content) {
    registerBoardObjectContent(shape.boardObjectKind, { ...shape.content, dragBand: shape.dragBand, fileBacked: shape.fileBacked })
  }
}

// unregisterThirdPartyNouns drops every noun one plugin registered --
// the tool AND the board-object content renderer it registered
// alongside -- so the plugin's re-activation registers them again
// against an empty slot (goal 0319's per-plugin reload). Placed
// objects of the kind stay on the board: between the two calls their
// Kind resolves to unknownKindContent, which is exactly the
// uninstalled-plugin rendering this registry already promises.
export function unregisterThirdPartyNouns(pluginId: string): void {
  for (const [id, noun] of thirdPartyRegistry) {
    if (noun.pluginId !== pluginId) continue
    thirdPartyRegistry.delete(id)
    if (noun.content) unregisterBoardObjectContent(noun.boardObjectKind)
  }
}

export function thirdPartyNouns(): ThirdPartyNounShape[] {
  return [...thirdPartyRegistry.values()]
}

export function isThirdPartyToolId(id: string): boolean {
  return thirdPartyRegistry.has(id)
}

export function thirdPartyNounFor(id: string): ThirdPartyNounShape | undefined {
  return thirdPartyRegistry.get(id)
}

// thirdPartyNounForKind resolves a persisted board-object kind (which
// may differ from the tool id, CanvasObjectDecl.objectKind) to its
// enabled plugin noun -- the label source for anything naming a plugin
// kind to the user (the paste-as offer, the link-paste preference).
export function thirdPartyNounForKind(kind: string): ThirdPartyNounShape | undefined {
  for (const noun of thirdPartyRegistry.values()) {
    if (noun.boardObjectKind === kind) return noun
  }
  return undefined
}

// thirdPartyNounForExtension -- the drop router's claim lookup
// (docs/goals/0251). Registration order decides ties (first claimant
// wins, deterministic: the loader activates plugins in ListPlugins'
// sorted id order). The registry only ever holds ENABLED plugins (the
// loader skips disabled ids at boot), so presence here IS enablement.
export function thirdPartyNounForExtension(ext: string): ThirdPartyNounShape | undefined {
  const lower = ext.toLowerCase()
  for (const noun of thirdPartyRegistry.values()) {
    if (noun.fileExtensions.includes(lower)) return noun
  }
  return undefined
}
