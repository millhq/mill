import { AtlasService } from '../shared/bindings'
import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import type { CanvasObjectFaceCtx } from './sdk'
import { seatCanvasTool } from './canvasToolAdapter'
import { activeSession, buildFramedTool, commitErase, draftPlacementFor, endedSession, eraseAt, forgetFramedTools } from './canvasToolFramed'
import { commitDraft, createDraft, discardDraft, dropDraftsFor, patchDraft } from './canvasDrafts'
import { measureMarkup } from './canvasMeasure'
import { CanvasProtocolError, parseErasePoint, parseObjectCommit, parseObjectCreate, parseObjectId, parseObjectMeasure, parseObjectPatch, parseRegisterFace, parseRegisterTool, parseToolId } from './canvasToolProtocol'
import { forgetCanvasObjectFaces, recordCanvasObjectFace, registeredCanvasObjectFaceEntry } from './canvasObjectFaceRegistry'

// The host's routing table for a framed canvas tool's own doors
// (docs/goals/0380 Decision 2), kept beside the other host-door
// modules (pluginStorageHostDoors.ts) rather than inside the
// activation bridge, so the bridge stays one line per door and this
// file owns the whole canvas contract.
//
// Every door PARSES its message first (canvasToolProtocol.ts) and
// refuses a malformed one by name with nothing changed: a frame is
// untrusted, so "reject with a typed error, no side effect" is the
// contract, not a best-effort read.

// CANVAS_TOOL_DOORS is the door list itself, exported so the bridge's
// method table and the frame runtime's own call surface are checked
// against ONE source (plugin-frame/protocol.test.ts).
export const CANVAS_TOOL_DOORS = [
  'register.tool', 'register.face',
  'object.create', 'object.patch', 'object.commit', 'object.discard',
  'object.measure',
  'files.saveImageBytes',
  'board.eraseAt', 'board.eraseCommit',
] as const

// A face function can only exist for an extension sharing this
// document; a framed one never supplies one, and register.tool carries
// no such field over the wire.
export type RenderFace = (el: HTMLElement, ctx: CanvasObjectFaceCtx) => void

export interface CanvasToolDoorContext {
  pluginId: string
  manifest: Manifest
  // post delivers one host->frame event to THIS plugin's activation
  // frame; the bridge owns the frame handle and supplies it.
  post: (event: string, payload: unknown) => void | Promise<void>
}

// erasing is capability-gated exactly as the same-DOM ctx is: without
// "erase-board-items" the doors are not merely unused, they refuse.
function requireErase(ctx: CanvasToolDoorContext): void {
  if (!(ctx.manifest.capabilities ?? []).includes('erase-board-items')) {
    throw new CanvasProtocolError('board.eraseAt', 'needs the "erase-board-items" capability')
  }
}

// Public through this host-door module for its adapter/tests; the map
// itself lives in a dependency-free registry so plugin reload can clear
// same-DOM registrations without importing this module graph.
export { registeredCanvasObjectFaceEntry as framedObjectFaceEntry } from './canvasObjectFaceRegistry'

function registerManifestObjectFace(ctx: CanvasToolDoorContext, descriptor: ReturnType<typeof parseRegisterFace>): void {
  const declared = (ctx.manifest.contributes?.canvasObjects ?? [])
    .find((face) => face.kind === descriptor.objectKind && face.entry === descriptor.entry)
  if (!declared) {
    throw new CanvasProtocolError('register.face', `must exactly match a canvasObjects kind and entry in plugin "${ctx.pluginId}"`)
  }
  recordCanvasObjectFace(ctx.pluginId, descriptor.objectKind, descriptor.entry)
}

// A door naming a tool that is not mid-gesture is refused: a draft
// only exists inside a gesture, and a commit outside one has no
// placement context to land in.
function requireSession(ctx: CanvasToolDoorContext, toolId: string) {
  const session = activeSession(ctx.pluginId, toolId)
  if (!session) throw new CanvasProtocolError('toolId', `has no gesture in progress ("${toolId}")`)
  return session
}

function doCreate(ctx: CanvasToolDoorContext, args: unknown[]): { id: string } {
  const message = parseObjectCreate(args[0])
  requireSession(ctx, message.toolId)
  const place = draftPlacementFor(ctx.pluginId, message.toolId)
  if (!place) throw new CanvasProtocolError('toolId', `has no gesture in progress ("${message.toolId}")`)
  return { id: createDraft(ctx.pluginId, message.toolId, message, place).id }
}

function doErase(ctx: CanvasToolDoorContext, args: unknown[]): boolean {
  requireErase(ctx)
  const { toolId, at } = parseErasePoint(args[0])
  eraseAt(requireSession(ctx, toolId).ctx, at)
  return true
}

function doEraseCommit(ctx: CanvasToolDoorContext, args: unknown[]): boolean {
  requireErase(ctx)
  const toolId = parseToolId(args[0], 'board.eraseCommit')
  // The erase pass commits AFTER pointer-up, one round trip later, by
  // which point the engine has already closed the session -- so the
  // accumulated hits are read off the gesture that just ended, never
  // off a live one that no longer exists.
  const session = endedSession(ctx.pluginId, toolId)
  if (session) commitErase(session.ctx)
  return true
}

export async function callCanvasToolDoor(ctx: CanvasToolDoorContext, method: string, args: unknown[], renderFace?: RenderFace): Promise<unknown> {
  const [first] = args
  switch (method) {
    case 'register.tool': {
      const descriptor = parseRegisterTool(first)
      // The lookup stays on the tool's own manifest kind even when its
      // existing objectKind alias names the persisted object. The
      // registration cannot create that authority: register.face
      // already required the exact manifest kind and entry.
      const faceEntry = registeredCanvasObjectFaceEntry(ctx.pluginId, descriptor.kind)
      seatCanvasTool(ctx.pluginId, buildFramedTool(ctx.pluginId, ctx.manifest, descriptor, ctx.post, renderFace, { entry: faceEntry }), descriptor.styleFields)
      return true
    }
    case 'register.face': {
      const descriptor = parseRegisterFace(first)
      registerManifestObjectFace(ctx, descriptor)
      return true
    }
    case 'object.create': return doCreate(ctx, args)
    case 'object.patch': { patchDraft(ctx.pluginId, parseObjectPatch(first)); return true }
    case 'object.commit': {
      const message = parseObjectCommit(first)
      return { id: await commitDraft(ctx.pluginId, message.id, message.select) }
    }
    case 'object.discard': { discardDraft(ctx.pluginId, parseObjectId(first, 'object.discard')); return true }
    case 'object.measure': return measureMarkup(ctx.pluginId, parseObjectMeasure(first))
    case 'files.saveImageBytes': return AtlasService.SaveImageBytes(String(args[0]), String(args[1]), String(args[2]))
    case 'board.eraseAt': return doErase(ctx, args)
    case 'board.eraseCommit': return doEraseCommit(ctx, args)
    default: throw new CanvasProtocolError('method', `is not a canvas-tool door ("${method}")`)
  }
}

// forgetCanvasTools is the teardown half: a plugin whose frame goes
// away leaves no armed runtime and no half-drawn draft behind, and its
// own register.face declarations go with it -- a reactivation
// re-declares them fresh rather than a stale mapping surviving reload.
export function forgetCanvasTools(pluginId: string): void {
  forgetFramedTools(pluginId)
  dropDraftsFor(pluginId)
  forgetCanvasObjectFaces(pluginId)
}
