import { chromium, expect, test, type Browser, type BrowserContext, type Page, type Request, type TestInfo } from '@playwright/test'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import type { BoardObject } from '../bindings/github.com/alicoding/mill/internal/domain/atlas/models'
import type { UndoResult, UndoState } from '../bindings/github.com/alicoding/mill/internal/services/atlassvc/models'
import { gotoAppReady } from './fixtures/appReady'
import { dragBetween, nonSeededBoardObjectWrapper } from './fixtures/atlasBoard'
import { shapeDrawPoints, shapeObjects } from './fixtures/atlasShapeTool'
import { clickAtlasTrayTool } from './fixtures/atlasTray'
import { paletteDialog } from './fixtures/palette'
import { spawnMillServer, type SpawnedServer } from './fixtures/server'
import { applyCpuThrottle } from './fixtures/throttle'
import { callBindingViaRPC } from './fixtures/wailsRpc'

const ATLAS_SERVICE = 'github.com/alicoding/mill/internal/services/atlassvc.AtlasService.'
const OBJECTS_METHOD = `${ATLAS_SERVICE}Objects`
const OBJECTS_METHOD_ID = 3025538390
const PROMOTE_OBJECT = 'Promote object to card…'

type RuntimeEnvelope = {
  object?: number
  method?: number
  args?: {
    'call-id'?: string
    methodID?: number
    methodName?: string
  }
}

type TraceEvent = {
  seq: number
  event: string
  [key: string]: unknown
}

type HeldResponse = {
  requestSequence: number
  ids: string[]
  release: () => void
  completed: Promise<void>
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

function objectIDs(value: unknown): string[] {
  if (!Array.isArray(value)) throw new Error(`Objects returned ${JSON.stringify(value)}, want an array or null`)
  return value.map((candidate) => {
    if (typeof candidate !== 'object' || candidate === null || !('ID' in candidate) || typeof candidate.ID !== 'string') {
      throw new Error(`Objects returned an entry without a string ID: ${JSON.stringify(candidate)}`)
    }
    return candidate.ID
  })
}

function parseObjectsResponse(value: unknown): string[] {
  return value === null ? [] : objectIDs(value)
}

function objectsCall(request: Request): RuntimeEnvelope | null {
  if (request.method() !== 'POST' || new URL(request.url()).pathname !== '/wails/runtime') return null
  let envelope: RuntimeEnvelope
  try {
    envelope = request.postDataJSON() as RuntimeEnvelope
  } catch {
    return null
  }
  if (envelope.object !== 0 || envelope.method !== 0) return null
  if (envelope.args?.methodID !== OBJECTS_METHOD_ID && envelope.args?.methodName !== OBJECTS_METHOD) return null
  return envelope
}

class ObjectsResponseOrder {
  private requestSequence = 0
  private traceSequence = 0
  private holdNext = false
  private heldValue: HeldResponse | null = null
  readonly responses: Array<{ requestSequence: number; ids: string[]; captured: boolean; fulfilled: boolean; origin: 'app' | 'test' }> = []
  readonly routeErrors: string[] = []
  readonly trace: TraceEvent[] = []

  constructor(private readonly page: Page) {}

  record(event: string, detail: Omit<TraceEvent, 'seq' | 'event'> = {}): void {
    this.trace.push({ seq: ++this.traceSequence, event, ...detail })
  }

  async install(): Promise<void> {
    await this.page.route('**/wails/runtime', async (route) => {
      const request = route.request()
      const envelope = objectsCall(request)
      if (!envelope) {
        await route.continue()
        return
      }

      const requestSequence = ++this.requestSequence
      const callID = envelope.args?.['call-id'] ?? ''
      const origin = envelope.args?.methodID === OBJECTS_METHOD_ID ? 'app' : 'test'
      this.record('objects.requested', { requestSequence, callID, origin })
      try {
        const response = await route.fetch()
        const ids = parseObjectsResponse(await response.json())
        const captured = this.holdNext && this.heldValue === null
        const observation = { requestSequence, ids, captured, fulfilled: false, origin }
        this.responses.push(observation)
        this.record('objects.response', { requestSequence, callID, responseIDs: ids, captured, origin })

        if (!captured) {
          await route.fulfill({ response })
          observation.fulfilled = true
          this.record('objects.fulfilled', { requestSequence, responseIDs: ids, origin })
          return
        }

        this.holdNext = false
        const gate = deferred()
        const completion = deferred()
        this.heldValue = {
          requestSequence,
          ids,
          release: gate.resolve,
          completed: completion.promise,
        }
        this.record('objects.captured', { requestSequence, capturedResponseIDs: ids })
        try {
          await gate.promise
          await route.fulfill({ response })
          observation.fulfilled = true
          this.record('objects.released', { requestSequence, releasedResponseIDs: ids })
        } finally {
          completion.resolve()
        }
      } catch (error) {
        const message = String(error)
        this.routeErrors.push(message)
        this.record('objects.route-error', { requestSequence, message })
        await route.abort('failed').catch(() => undefined)
      }
    })
  }

  armFirstResponse(): void {
    if (this.holdNext || this.heldValue) throw new Error('an Objects response is already armed or held')
    this.holdNext = true
    this.record('objects.hold-armed')
  }

  held(): HeldResponse | null {
    return this.heldValue
  }

  latestRequestSequence(): number {
    return this.requestSequence
  }

  async releaseHeld(reason: string): Promise<void> {
    const held = this.heldValue
    if (!held) throw new Error('no Objects response is held')
    this.record('objects.release-requested', { requestSequence: held.requestSequence, reason })
    held.release()
    await held.completed
    this.heldValue = null
  }

  async cleanup(): Promise<void> {
    this.holdNext = false
    if (this.heldValue) await this.releaseHeld('scenario cleanup')
  }
}

interface ScenarioContext {
  page: Page
  responses: ObjectsResponseOrder
}

async function runScenario(testInfo: TestInfo, body: (scenario: ScenarioContext) => Promise<void>): Promise<void> {
  const dir = mkdtempSync(path.join(tmpdir(), `mill-e2e-atlas-response-order-${testInfo.parallelIndex}-`))
  let server: SpawnedServer | undefined
  let browser: Browser | undefined
  let context: BrowserContext | undefined
  let responses: ObjectsResponseOrder | undefined
  let failure: unknown
  try {
    server = await spawnMillServer({
      settingsPath: path.join(dir, 'settings.json'),
      executionDbPath: path.join(dir, 'execution.db'),
      backupDir: path.join(dir, 'backups'),
    })
    browser = await chromium.launch()
    context = await browser.newContext({ baseURL: server.baseURL, serviceWorkers: 'block' })
    const page = await context.newPage()
    await applyCpuThrottle(page)
    responses = new ObjectsResponseOrder(page)
    await responses.install()
    responses.record('scenario.started', { title: testInfo.title, baseURL: server.baseURL })
    await body({ page, responses })
    expect(responses.routeErrors).toEqual([])
  } catch (error) {
    failure = error
  }

  try {
    await responses?.cleanup()
  } catch (error) {
    failure ??= error
  }
  try {
    const tracePath = testInfo.outputPath('atlas-post-create-selection.json')
    writeFileSync(tracePath, `${JSON.stringify(responses?.trace ?? [], null, 2)}\n`)
    await testInfo.attach('atlas-post-create-selection', { path: tracePath, contentType: 'application/json' })
  } catch (error) {
    failure ??= error
  }
  for (const cleanup of [() => context?.close(), () => browser?.close(), () => server?.stop()]) {
    try {
      await cleanup()
    } catch (error) {
      failure ??= error
    }
  }
  try {
    rmSync(dir, { recursive: true, force: true })
  } catch (error) {
    failure ??= error
  }
  if (failure) throw failure
}

async function openAtlas(page: Page): Promise<void> {
  await gotoAppReady(page)
  await page.getByRole('link', { name: 'Atlas' }).click()
  await expect(page.getByTestId('atlas-board')).toBeVisible()
}

async function readObjects(page: Page, responses: ObjectsResponseOrder, reason: string): Promise<BoardObject[]> {
  const objects = await callBindingViaRPC<BoardObject[] | null>(page, OBJECTS_METHOD, [])
  responses.record('backend.objects-read', { reason, backendObjectIDs: (objects ?? []).map((object) => object.ID) })
  return objects ?? []
}

async function emitAtlasDataChanged(page: Page, responses: ObjectsResponseOrder, reason: string): Promise<void> {
  const result = await page.evaluate(async (clientID) => {
    const response = await fetch(window.location.origin + '/wails/runtime', {
      method: 'POST',
      headers: { 'x-wails-client-id': clientID, 'Content-Type': 'application/json' },
      body: JSON.stringify({ object: 3, method: 0, args: { name: 'mill-data-changed', data: { entity: 'atlas', id: '' } } }),
    })
    return { ok: response.ok, status: response.status, text: await response.text() }
  }, `atlas-response-order-${reason}`)
  responses.record('atlas.event-emitted', { reason, ...result })
  expect(result.ok, `atlas data-change event failed: ${result.status} ${result.text}`).toBe(true)
}

async function drawShape(page: Page, responses: ObjectsResponseOrder): Promise<string> {
  const board = page.getByTestId('atlas-board')
  await clickAtlasTrayTool(page, 'atlas-tray-shape')
  const picker = page.getByTestId('atlas-shape-style-picker')
  await expect(picker).toBeVisible()
  const draw = await shapeDrawPoints(page, board, picker)
  await dragBetween(page, draw.from, draw.to)
  await expect(shapeObjects(page)).toHaveCount(1)
  const wrapper = nonSeededBoardObjectWrapper(page, 'shape')
  await expect(wrapper).toHaveCount(1)
  await expect(wrapper).toHaveClass(/selected/)
  const id = await wrapper.getAttribute('data-id')
  if (!id) throw new Error('drawn shape wrapper has no data-id')
  responses.record('shape.drawn', { objectID: id })
  return id
}

async function wrapperIDs(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).evaluateAll((nodes) => nodes.flatMap((node) => node.getAttribute('data-id') ?? []))
}

async function waitForAppObjectsResponse(page: Page, responses: ObjectsResponseOrder, after: number, id: string): Promise<void> {
  await expect.poll(() => responses.responses.some((response) => (
    response.origin === 'app'
      && response.fulfilled
      && response.requestSequence > after
      && response.ids.includes(id)
  ))).toBe(true)
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

async function observeSelection(page: Page, responses: ObjectsResponseOrder, id: string, reason: string, soft = false): Promise<void> {
  const visibleIDs = await wrapperIDs(page, '.react-flow__node:not([data-id^="atlas-object-example-"])')
  const selectedIDs = await wrapperIDs(page, '.react-flow__node.selected:not([data-id^="atlas-object-example-"])')
  responses.record('selection.sampled', { reason, visibleWrapperIDs: visibleIDs, selectedWrapperIDs: selectedIDs })

  await page.keyboard.press('Meta+/')
  const palette = paletteDialog(page)
  await expect(palette).toBeVisible()
  await palette.getByRole('combobox').fill('Promote object to card')
  const promoteOption = palette.getByRole('option', { name: PROMOTE_OBJECT, exact: true })
  if (soft) await expect.soft(promoteOption, `${reason}: shared command context exposes object promotion`).toHaveCount(1)
  else await expect(promoteOption, `${reason}: shared command context exposes object promotion`).toHaveCount(1)
  const promoteCount = await promoteOption.count()
  responses.record('palette.sampled', { reason, promoteOptionCount: promoteCount })
  await page.keyboard.press('Escape')
  await expect(palette).toHaveCount(0)
  const selectedAfterClose = await wrapperIDs(page, '.react-flow__node.selected:not([data-id^="atlas-object-example-"])')
  responses.record('palette.closed', { reason, selectedWrapperIDs: selectedAfterClose })

  if (soft) {
    expect.soft(visibleIDs, `${reason}: created object remains present`).toContain(id)
    expect.soft(selectedIDs, `${reason}: created object remains selected`).toContain(id)
    expect.soft(selectedAfterClose, `${reason}: palette open/close does not clear selection`).toContain(id)
    return
  }
  expect(visibleIDs, `${reason}: created object remains present`).toContain(id)
  expect(selectedIDs, `${reason}: created object remains selected`).toContain(id)
  expect(selectedAfterClose, `${reason}: palette open/close does not clear selection`).toContain(id)
}

async function proveSingleActorMark(page: Page, responses: ObjectsResponseOrder, id: string): Promise<void> {
  const first = await callBindingViaRPC<UndoResult>(page, `${ATLAS_SERVICE}Undo`, [])
  responses.record('undo.first', { result: first })
  expect(first.Applied).toBe(true)
  await expect(page.locator(`.react-flow__node[data-id="${id}"]`)).toHaveCount(0)
  expect((await readObjects(page, responses, 'after first undo')).map((object) => object.ID)).not.toContain(id)

  const second = await callBindingViaRPC<UndoResult>(page, `${ATLAS_SERVICE}Undo`, [])
  responses.record('undo.second', { result: second })
  expect(second.Applied).toBe(false)

  const redo = await callBindingViaRPC<UndoResult>(page, `${ATLAS_SERVICE}Redo`, [])
  responses.record('redo.first', { result: redo })
  expect(redo.Applied).toBe(true)
  await expect(page.locator(`.react-flow__node[data-id="${id}"]`)).toBeVisible()
  expect((await readObjects(page, responses, 'after redo')).map((object) => object.ID)).toContain(id)
}

async function assertFreshUndo(page: Page, responses: ObjectsResponseOrder): Promise<void> {
  const state = await callBindingViaRPC<UndoState>(page, `${ATLAS_SERVICE}UndoState`, [])
  responses.record('undo.initial', { state })
  expect(state.HasUndo).toBe(false)
}

// Dedicated servers are required because each case controls global Atlas response order.
// eslint-disable-next-line no-empty-pattern -- dedicated setup needs testInfo, not a shared page fixture.
test('a settled shape remains present and selected with one actor undo mark', async ({}, testInfo) => {
  await runScenario(testInfo, async ({ page, responses }) => {
    await openAtlas(page)
    await assertFreshUndo(page, responses)
    const id = await drawShape(page, responses)
    expect((await readObjects(page, responses, 'after settled draw')).map((object) => object.ID)).toContain(id)
    await observeSelection(page, responses, id, 'settled draw')
    await proveSingleActorMark(page, responses, id)
  })
})

// eslint-disable-next-line no-empty-pattern -- dedicated setup needs testInfo, not a shared page fixture.
test('an unchanged-data refresh preserves the created shape and selection', async ({}, testInfo) => {
  await runScenario(testInfo, async ({ page, responses }) => {
    await openAtlas(page)
    await assertFreshUndo(page, responses)
    const id = await drawShape(page, responses)
    expect((await readObjects(page, responses, 'before identity refresh')).map((object) => object.ID)).toContain(id)
    await observeSelection(page, responses, id, 'before identity refresh')

    const beforeEvent = responses.latestRequestSequence()
    await emitAtlasDataChanged(page, responses, 'identity-refresh')
    await waitForAppObjectsResponse(page, responses, beforeEvent, id)
    await observeSelection(page, responses, id, 'after identity refresh')
    await proveSingleActorMark(page, responses, id)
  })
})

// eslint-disable-next-line no-empty-pattern -- dedicated setup needs testInfo, not a shared page fixture.
test('an earlier Objects response cannot replace newer post-create state', async ({}, testInfo) => {
  await runScenario(testInfo, async ({ page, responses }) => {
    await openAtlas(page)
    await assertFreshUndo(page, responses)
    responses.armFirstResponse()
    await emitAtlasDataChanged(page, responses, 'capture-pre-create')
    await expect.poll(() => responses.held()?.requestSequence ?? 0).toBeGreaterThan(0)

    const heldSequence = responses.held()?.requestSequence ?? 0
    const id = await drawShape(page, responses)
    await waitForAppObjectsResponse(page, responses, heldSequence, id)
    const newerObjects = await readObjects(page, responses, 'newer post-create response')
    const held = responses.held()
    if (!held) throw new Error('pre-create Objects response was not held')
    expect(held.ids).not.toContain(id)
    expect(newerObjects.map((object) => object.ID)).toContain(id)
    await observeSelection(page, responses, id, 'before stale response release')

    await responses.releaseHeld('release pre-create response after newer draw')
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    await observeSelection(page, responses, id, 'after stale response release', true)
    await proveSingleActorMark(page, responses, id)
  })
})
