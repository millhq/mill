import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import { thirdPartyNounFor, unregisterThirdPartyNouns, type AtlasGestureCtx } from '../atlas/atlasNounRegistry'
import { forgetCanvasTools } from './canvasToolHostDoors'
import { registerLocalCanvasTool } from './canvasToolLocal'
import { unregisterPluginCommands } from './pluginCommands'
import type { CanvasToolDecl } from './sdk/canvasTools'

vi.mock('./PluginFaceContent', () => ({
  pluginFaceComponent: () => () => null,
  pluginObjectCtx: () => ({}),
}))

const PLUGIN_ID = 'local-queue-probe'
const MANIFEST = {
  id: PLUGIN_ID,
  name: 'Local queue probe',
  version: '1.0.0',
  capabilities: [],
  contributes: { canvasObjects: [{ kind: 'queue-probe', tool: true }] },
} as unknown as Manifest

function gestureCtx(): AtlasGestureCtx {
  return {
    screenToFlowPosition: (point) => point,
    parentID: '',
    cardBoxes: [],
    noteBoxes: [],
    objectBoxes: [],
    onDeleteSelection: () => {},
    openAreaPopover: () => {},
    onShapeCreated: () => {},
    enclosedIn: () => ({ cardIDs: [], noteIDs: [], objectIDs: [] }),
    disarm: () => {},
    disarmUnlessLocked: () => {},
    modifiers: { shift: false, alt: false, ctrl: false, meta: false },
    hitAccumulator: { cardIDs: new Set(), noteIDs: new Set(), objectIDs: new Set() },
  }
}

describe('local canvas pointer ordering', () => {
  afterEach(() => {
    forgetCanvasTools(PLUGIN_ID)
    unregisterThirdPartyNouns(PLUGIN_ID)
    unregisterPluginCommands(PLUGIN_ID)
  })

  it('opens a draft during pointer down before pointer up closes the gesture session', async () => {
    let created = false
    let releaseDown = () => {}
    const holdDown = new Promise<void>((resolve) => { releaseDown = resolve })
    const phases: string[] = []
    const decl: CanvasToolDecl = {
      kind: 'queue-probe',
      label: 'Queue probe',
      icon: 'circle',
      source: 'board-local',
      editRoute: 'none',
      sticky: true,
      renderFace: () => {},
      async onPointer(event, ctx) {
        phases.push(event.phase)
        if (event.phase === 'down') {
          await ctx.createDraft({ at: event.point })
          created = true
          await holdDown
        }
      },
    }
    registerLocalCanvasTool(PLUGIN_ID, MANIFEST, decl)

    const gesture = thirdPartyNounFor('queue-probe')?.gesture
    if (!gesture?.onPoint) throw new Error('queue probe gesture was not registered')
    const ctx = gestureCtx()
    const down = { x: 10, y: 10, t: 0 }
    const up = { x: 30, y: 30, t: 1 }
    gesture.onPoint(down, ctx)
    expect(phases).toEqual(['down'])
    const completion = gesture.onEnd([down, up], ctx)
    await Promise.resolve()
    await Promise.resolve()

    expect(created).toBe(true)
    expect(phases).toEqual(['down'])
    releaseDown()
    await completion
    expect(phases).toEqual(['down', 'up'])
  })
})
