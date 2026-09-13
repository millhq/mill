import { chromium, expect, test } from '@playwright/test'
import { applyCpuThrottle } from './fixtures/throttle'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { CODING_LOOP_MCP_BASE_PORT, CODING_LOOP_SERVER_BASE_PORT, spawnMillServer, type SpawnedServer } from './fixtures/server'
import { withClipboardLock } from './fixtures/clipboardLock'
import { writeHostClipboardText, hostClipboardAvailable } from './fixtures/hostClipboard'
import { paletteDialog } from './fixtures/palette'
import { callBindingViaRPC } from './fixtures/wailsRpc'

// The coding loop end-to-end (docs/goals/0240 S1): copy a shell command
// block, hit the hotkey/palette, confirm the parsed structure, watch it
// run, copy the result back.
//
// Dedicated server, MILL_CLIPBOARD=host (goal 0356): CompositionService.
// ReadHostClipboardText reads the actual OS pasteboard (pbpaste) with no
// test-injectable override, so this uses the real clipboard via
// withClipboardLock + fixtures/hostClipboard.ts -- there is no seam to
// prefer here. The standard per-worker pool defaults to the in-memory
// clipboard adapter and has no per-spec override, so this file needs its
// own server. On CI's headless ubuntu-latest runner (no pbcopy/pbpaste),
// the RPC read itself fails; the honest error path is asserted below,
// not a payload round-trip the runner's OS can't perform.
//
// Drives the capture entry via the command palette (⌘K -> "Run from
// clipboard"), not the Quick Panel's own separate window -- the Quick
// Panel is a second, auxiliary Wails window Playwright's single-page
// harness doesn't attach to, while both doors render the exact same
// shared CodingLoopSurface/useCodingLoopRun (shared/CodingLoopSurface.tsx),
// so the palette path already exercises the real state machine end to
// end; the Quick Panel's OWN wiring (the door hook, the rich row) is
// proven structurally by quickPanelCommands.test.ts instead (unit
// layer, testing.md's layering).
//
// Live-run class (QUARANTINE.md): this fires a real workflow run and
// waits for its own terminal status before any cleanup.

interface Fixture {
  server: SpawnedServer
  browser: import('@playwright/test').Browser
  page: import('@playwright/test').Page
  dir: string
}

async function setUp(testInfo: { parallelIndex: number }): Promise<Fixture> {
  const idx = testInfo.parallelIndex
  const dir = mkdtempSync(path.join(tmpdir(), `mill-e2e-coding-loop-${idx}-`))
  const server = await spawnMillServer({
    port: CODING_LOOP_SERVER_BASE_PORT + idx,
    mcpPort: CODING_LOOP_MCP_BASE_PORT + idx,
    settingsPath: path.join(dir, 'settings.json'),
    executionDbPath: path.join(dir, 'execution.db'),
    backupDir: path.join(dir, 'backups'),
    extraEnv: { MILL_CLIPBOARD: 'host' },
  })
  const browser = await chromium.launch()
  const context = await browser.newContext({ baseURL: server.baseURL })
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const page = await context.newPage()
  await applyCpuThrottle(page)
  await page.goto(`${server.baseURL}/`)
  return { server, browser, page, dir }
}

async function tearDown(f: Fixture): Promise<void> {
  await f.browser.close()
  await f.server.stop()
  rmSync(f.dir, { recursive: true, force: true })
}

// eslint-disable-next-line no-empty-pattern -- needs `testInfo`, not any fixture.
test('Coding loop: capture, confirm, run, and copy back the result', async ({}, testInfo) => {
  const f = await setUp(testInfo)
  try {
    await withClipboardLock(async () => {
      const { page } = f
      writeHostClipboardText('echo coding-loop-e2e-one\necho coding-loop-e2e-two')

      await page.goto('/')
      await expect(page.getByRole('link', { name: 'Home' })).toBeVisible()

      await page.keyboard.press('Meta+K')
      await expect(paletteDialog(page)).toBeVisible()
      await paletteDialog(page).getByRole('combobox').fill('run from clipboard')
      await paletteDialog(page).getByRole('option', { name: 'Run from clipboard…', exact: true }).click()

      const dialog = page.getByRole('dialog', { name: 'Run from clipboard' })
      await expect(dialog).toBeVisible()

      if (!hostClipboardAvailable) {
        // The honest never-silent failure path (no real pasteboard on
        // this runner) -- see this file's own header comment.
        await expect(page.getByTestId('coding-loop-read-error')).toBeVisible({ timeout: 10_000 })
        return
      }

      // --- Confirm: the parsed structure is visible before anything runs ---
      const confirm = page.getByTestId('coding-loop-confirm')
      await expect(confirm).toBeVisible({ timeout: 10_000 })
      await expect(page.getByTestId('coding-loop-confirm-summary')).toContainText('2 steps')
      const steps = page.getByTestId('coding-loop-confirm-steps').locator('li')
      await expect(steps).toHaveCount(2)
      await expect(steps.nth(0)).toContainText('echo coding-loop-e2e-one')
      await expect(steps.nth(1)).toContainText('echo coding-loop-e2e-two')
      await expect(page.getByTestId('coding-loop-confirm-verdict')).toContainText('Asks for approval')

      // --- Run: the Confirm click is the approval gesture ---
      await page.getByTestId('coding-loop-confirm-run').click()

      // --- Running: per-step state, never stuck ---
      await expect(page.getByTestId('coding-loop-running')).toBeVisible({ timeout: 10_000 })

      // --- Result: full output, saved as a run record ---
      const result = page.getByTestId('coding-loop-result')
      await expect(result).toBeVisible({ timeout: 20_000 })
      await expect(page.getByTestId('coding-loop-result-output')).toContainText('coding-loop-e2e-one')
      await expect(page.getByTestId('coding-loop-result-output')).toContainText('coding-loop-e2e-two')

      // --- Copy result: one click, the real clipboard now holds it ---
      await page.getByTestId('coding-loop-result-copy').click()
      await expect(page.getByTestId('coding-loop-result-copy')).toContainText('Copied')
    })
  } finally {
    await tearDown(f)
  }
})

// eslint-disable-next-line no-empty-pattern -- needs `testInfo`, not any fixture.
test('Coding loop: failed output stays visible and Copy result copies that exact result', async ({}, testInfo) => {
  const f = await setUp(testInfo)
  try {
    await withClipboardLock(async () => {
      const { page } = f
      const marker = 'coding-loop-failed-output-marker'
      writeHostClipboardText(`printf '${marker}\\n'; exit 7`)

      await page.goto('/')
      await expect(page.getByRole('link', { name: 'Home' })).toBeVisible()
      await page.keyboard.press('Meta+K')
      await expect(paletteDialog(page)).toBeVisible()
      await paletteDialog(page).getByRole('combobox').fill('run from clipboard')
      await paletteDialog(page).getByRole('option', { name: 'Run from clipboard…', exact: true }).click()

      if (!hostClipboardAvailable) {
        await expect(page.getByTestId('coding-loop-read-error')).toBeVisible({ timeout: 10_000 })
        return
      }

      await expect(page.getByTestId('coding-loop-confirm')).toBeVisible({ timeout: 10_000 })
      await page.getByTestId('coding-loop-confirm-run').click()

      const result = page.getByTestId('coding-loop-result')
      await expect(result).toBeVisible({ timeout: 20_000 })
      await expect(page.getByTestId('coding-loop-result-banner')).toContainText('Failed')
      const output = page.getByTestId('coding-loop-result-output')
      await expect(output).toHaveAttribute('aria-label', 'Failed')
      await expect(output).toContainText(marker)

      const workflows = await callBindingViaRPC<Array<{ id: string; label: string }>>(
        page,
        'github.com/alicoding/mill/internal/services/compositionsvc.CompositionService.Workflows',
        [],
      )
      const workflow = workflows.find((candidate) => candidate.label === 'Run from clipboard')
      if (!workflow) throw new Error('Run from clipboard seed is missing')
      const runs = await callBindingViaRPC<Array<{ runID: string }>>(
        page,
        'github.com/alicoding/mill/internal/services/executionsvc.ExecutionService.ListRunsForWorkflow',
        [workflow.id],
      )
      expect(runs).toHaveLength(1)
      const detail = await callBindingViaRPC<{ status: string; output: string }>(
        page,
        'github.com/alicoding/mill/internal/services/executionsvc.ExecutionService.GetRun',
        [runs[0].runID],
      )
      expect(detail.status).toBe('ERROR')
      expect(detail.output).toContain(marker)

      await page.getByTestId('coding-loop-result-copy').click()
      await expect(page.getByTestId('coding-loop-result-copy')).toContainText('Copied')
      await expect.poll(() => page.evaluate(() => navigator.clipboard.readText()), { timeout: 10_000 })
        .toBe(detail.output)
    })
  } finally {
    await tearDown(f)
  }
})

// The allow/deny pattern-list verdict display (docs/goals/0240 S3): a
// block mixing an allow-listed read-only line (ls) with a deny-listed
// dangerous one (rm -rf) shows EACH step's own guardrail verdict on the
// Confirm screen, distinctly from the plain default-ask copy an
// unlisted command (the spec above's echo lines) gets. Only reaches
// Confirm -- running the deny-listed line for real is already proven at
// the Go layer (executionsvc/codingloop_shellguard_test.go); this spec
// is the presentation layer the Go tests can't express.
// eslint-disable-next-line no-empty-pattern -- needs `testInfo`, not any fixture.
test('Coding loop: Confirm shows an allow-listed and a deny-listed step verdict', async ({}, testInfo) => {
  const f = await setUp(testInfo)
  try {
    await withClipboardLock(async () => {
      const { page } = f
      writeHostClipboardText('ls\nrm -rf /tmp/coding-loop-e2e-does-not-exist')

      await page.goto('/')
      await expect(page.getByRole('link', { name: 'Home' })).toBeVisible()

      await page.keyboard.press('Meta+K')
      await expect(paletteDialog(page)).toBeVisible()
      await paletteDialog(page).getByRole('combobox').fill('run from clipboard')
      await paletteDialog(page).getByRole('option', { name: 'Run from clipboard…', exact: true }).click()

      const dialog = page.getByRole('dialog', { name: 'Run from clipboard' })
      await expect(dialog).toBeVisible()

      if (!hostClipboardAvailable) {
        await expect(page.getByTestId('coding-loop-read-error')).toBeVisible({ timeout: 10_000 })
        return
      }

      const confirm = page.getByTestId('coding-loop-confirm')
      await expect(confirm).toBeVisible({ timeout: 10_000 })
      const steps = page.getByTestId('coding-loop-confirm-steps').locator('li')
      await expect(steps).toHaveCount(2)

      await expect(page.getByTestId('coding-loop-confirm-step-verdict-0')).toContainText('Allowed by "Read-only: ls"')
      await expect(page.getByTestId('coding-loop-confirm-step-verdict-1')).toContainText('Blocked by "Destructive: rm -rf". Approve to run.')

      // The block still needs approval overall (the deny-listed line wins
      // the block-level decision) -- never silently skipped just because
      // one line was allow-listed.
      await expect(page.getByTestId('coding-loop-confirm-verdict')).toContainText('Asks for approval')
    })
  } finally {
    await tearDown(f)
  }
})
