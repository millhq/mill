// @vitest-environment jsdom
import { act, type ButtonHTMLAttributes, type HTMLAttributes, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  downloadBlob: vi.fn(),
  exportAuditTrail: vi.fn(),
  exportContract: vi.fn(),
  exportSkillDoc: vi.fn(),
}))

vi.mock('../shared/downloadBlob', () => ({ downloadBlob: mocks.downloadBlob }))
vi.mock('../shared/bindings', () => ({
  AuditService: { ExportAuditTrail: mocks.exportAuditTrail },
  SettingsService: {
    ExportContract: mocks.exportContract,
    ExportSkillDoc: mocks.exportSkillDoc,
  },
}))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
vi.mock('@primer/react', () => ({
  Button: ({ children, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) => <button {...props}>{children}</button>,
  Stack: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Text: ({ children, ...props }: HTMLAttributes<HTMLSpanElement>) => <span {...props}>{children}</span>,
}))

import ContractSection from './ContractSection'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.addEventListener('load', () => resolve(String(reader.result)))
    reader.addEventListener('error', () => reject(reader.error))
    reader.readAsText(blob)
  })
}

describe('ContractSection exports', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(async () => {
    vi.clearAllMocks()
    mocks.downloadBlob.mockResolvedValue(undefined)
    mocks.exportContract.mockResolvedValue('{"name":"Café ☕"}')
    mocks.exportSkillDoc.mockResolvedValue('# 技能\nCrème brûlée')
    mocks.exportAuditTrail.mockResolvedValue('{"actor":"Δ"}\n')
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => root.render(<ContractSection />))
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  async function click(testID: string, expectedDownloadCount: number): Promise<void> {
    await act(async () => container.querySelector<HTMLButtonElement>(`[data-testid="${testID}"]`)?.click())
    await vi.waitFor(() => expect(mocks.downloadBlob).toHaveBeenCalledTimes(expectedDownloadCount))
  }

  it('sends exact Unicode RPC content, filenames, and MIME types through downloadBlob', async () => {
    await click('export-contract', 1)
    await click('export-skill-doc', 2)
    await click('export-audit-trail', 3)

    const expected = [
      ['mill-contract.json', 'application/json', '{"name":"Café ☕"}'],
      ['mill-skill.md', 'text/markdown', '# 技能\nCrème brûlée'],
      ['mill-audit-trail.jsonl', 'application/json', '{"actor":"Δ"}\n'],
    ] as const
    for (const [index, [filename, type, content]] of expected.entries()) {
      const [actualFilename, blob] = mocks.downloadBlob.mock.calls[index] as [string, Blob]
      expect(actualFilename).toBe(filename)
      expect(blob.type).toBe(type)
      await expect(readBlob(blob)).resolves.toBe(content)
    }
    expect(mocks.exportAuditTrail).toHaveBeenCalledWith([])
  })

  it('maps RPC, metadata, and save rejections to their existing inline error slots', async () => {
    mocks.exportContract.mockRejectedValueOnce(new Error('RPC failed'))
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="export-contract"]')?.click())
    await vi.waitFor(() => expect(container.textContent).toContain('settings.contract.exportError'))

    mocks.downloadBlob.mockRejectedValueOnce(new Error('metadata unavailable'))
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="export-skill-doc"]')?.click())
    await vi.waitFor(() => expect(container.textContent).toContain('settings.contract.exportSkillError'))

    mocks.downloadBlob.mockRejectedValueOnce(new Error('save failed'))
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="export-audit-trail"]')?.click())
    await vi.waitFor(() => expect(container.textContent).toContain('settings.contract.exportAuditError'))

    for (const button of container.querySelectorAll<HTMLButtonElement>('button')) {
      expect(button.disabled).toBe(false)
    }
  })

  it('treats a cancelled save as quiet and keeps the actions available', async () => {
    await click('export-contract', 1)

    expect(container.textContent).not.toContain('settings.contract.exportError')
    expect(container.textContent).not.toContain('settings.contract.exportSkillError')
    expect(container.textContent).not.toContain('settings.contract.exportAuditError')
    for (const button of container.querySelectorAll<HTMLButtonElement>('button')) {
      expect(button.disabled).toBe(false)
    }
  })
})
