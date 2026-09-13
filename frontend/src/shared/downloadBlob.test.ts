// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  getBuildInfo: vi.fn(),
  saveBinaryFile: vi.fn(),
}))

vi.mock('./bindings', () => ({
  SettingsService: {
    GetBuildInfo: mocks.getBuildInfo,
    SaveBinaryFile: mocks.saveBinaryFile,
  },
}))

import { downloadBlob } from './downloadBlob'

describe('downloadBlob', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uses the native binary save adapter with exact base64 bytes', async () => {
    mocks.getBuildInfo.mockResolvedValue({ Server: false })
    mocks.saveBinaryFile.mockResolvedValue('/chosen/export.bin')

    await downloadBlob('export.bin', new Blob([new Uint8Array([0, 0xff, 1])]))

    expect(mocks.saveBinaryFile).toHaveBeenCalledWith('export.bin', 'AP8B')
  })

  it('keeps the browser download and deterministic URL cleanup in server mode', async () => {
    mocks.getBuildInfo.mockResolvedValue({ Server: true })
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    await downloadBlob('export.bin', new Blob(['bytes']))

    expect(create).toHaveBeenCalledOnce()
    expect(click).toHaveBeenCalledOnce()
    expect(revoke).toHaveBeenCalledWith('blob:test')
    expect(mocks.saveBinaryFile).not.toHaveBeenCalled()
  })

  it('propagates build metadata and native save failures', async () => {
    mocks.getBuildInfo.mockRejectedValueOnce(new Error('metadata unavailable'))
    await expect(downloadBlob('export.bin', new Blob())).rejects.toThrow('metadata unavailable')
    expect(mocks.saveBinaryFile).not.toHaveBeenCalled()

    mocks.getBuildInfo.mockResolvedValueOnce({ Server: false })
    mocks.saveBinaryFile.mockRejectedValueOnce(new Error('save failed'))
    await expect(downloadBlob('export.bin', new Blob())).rejects.toThrow('save failed')
  })

  it('returns quietly when the native save dialog is cancelled', async () => {
    mocks.getBuildInfo.mockResolvedValue({ Server: false })
    mocks.saveBinaryFile.mockResolvedValue('')

    await expect(downloadBlob('export.bin', new Blob(['bytes']))).resolves.toBeUndefined()

    expect(mocks.saveBinaryFile).toHaveBeenCalledOnce()
  })
})
