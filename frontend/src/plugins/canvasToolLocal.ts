import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import type { CanvasDraft, CanvasToolCtx, CanvasToolDecl, CanvasToolPointerEvent, RegisterFaceDescriptor } from './sdk/canvasTools'
import { seatCanvasTool } from './canvasToolAdapter'
import { buildFramedTool } from './canvasToolFramed'
import { callCanvasToolDoor, type CanvasToolDoorContext } from './canvasToolHostDoors'
import { parseRegisterTool, toolWireDescriptor, type ToolPointerPayload } from './canvasToolProtocol'

// registerCanvasTool for a plugin running in Mill's OWN document
// (docs/goals/0380): the same declaration, the same doors, the same
// validation -- only the transport differs, because there is no frame
// to cross. Keeping one implementation means a tool cannot behave one
// way bundled and another way sandboxed, which is the whole reason the
// declaration is data rather than callbacks.

function localCtx(door: CanvasToolDoorContext, decl: CanvasToolDecl, event: ToolPointerPayload, canErase: boolean): CanvasToolCtx {
  const call = (method: string, ...args: unknown[]) => callCanvasToolDoor(door, method, args)
  const draft = (id: string): CanvasDraft => ({
    id,
    patch: (patch) => call('object.patch', { id, ...patch }).then(() => undefined),
    commit: (opts) => call('object.commit', { id, select: !!opts?.select }).then((r) => (r as { id: string | null } | null)?.id ?? null),
    discard: () => call('object.discard', { id }).then(() => undefined),
  })
  const ctx: CanvasToolCtx = {
    styleValues: event.styleValues,
    createDraft: (input) => call('object.create', { toolId: decl.kind, kind: decl.objectKind || decl.kind, at: input.at, size: input.size, data: input.data || {}, preview: input.preview || {} })
      .then((r) => draft((r as { id: string }).id)),
  }
  if (canErase) {
    ctx.eraseAt = (at) => call('board.eraseAt', { toolId: decl.kind, at }).then(() => undefined)
    ctx.commitErase = () => call('board.eraseCommit', { toolId: decl.kind }).then(() => undefined)
  }
  return ctx
}

export function registerLocalCanvasTool(pluginId: string, manifest: Manifest, decl: CanvasToolDecl): void {
  const descriptor = parseRegisterTool(toolWireDescriptor(decl))
  const canErase = (manifest.capabilities ?? []).includes('erase-board-items')
  let pointerQueue: Promise<void> | null = null
  const door: CanvasToolDoorContext = {
    pluginId,
    manifest,
    // "Posting" to a plugin that shares this document is calling it:
    // the pointer event goes straight to its handler instead of onto a
    // message queue, and a throw inside plugin code stays inside it.
    post: (event, payload) => {
      if (event !== 'tool.pointer') return
      const pointer = payload as ToolPointerPayload
      const run = () => decl.onPointer(pointer as unknown as CanvasToolPointerEvent, localCtx(door, decl, pointer, canErase))
      // Start an idle queue in this stack. The down handler opens its draft
      // through a session-gated door before pointer-up can close that session;
      // later phases still wait for the async phase before them to finish.
      const completion = (pointerQueue ? pointerQueue.then(run) : new Promise<void>((resolve) => resolve(run())))
        .catch((err: unknown) => console.error(`plugin ${pluginId}: tool "${decl.kind}" failed`, err))
      pointerQueue = completion
      void completion.finally(() => {
        if (pointerQueue === completion) pointerQueue = null
      })
      return completion
    },
  }
  seatCanvasTool(pluginId, buildFramedTool(pluginId, manifest, descriptor, door.post, decl.renderFace), descriptor.styleFields)
}

// Same-DOM activation exposes the same manifest-bound registration
// shape as framed activation. The shared host door performs the exact
// kind+entry check before it records anything.
export function registerLocalCanvasObjectFace(pluginId: string, manifest: Manifest, descriptor: RegisterFaceDescriptor): Promise<void> {
  return callCanvasToolDoor({ pluginId, manifest, post: () => {} }, 'register.face', [descriptor]).then(() => undefined)
}
