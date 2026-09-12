import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildPluginAPI, menuForDeclaredCommand } from './hostApi'
import type { Manifest } from '../../bindings/github.com/alicoding/mill/internal/services/pluginsvc/models'
import { drainedPluginCommands, unregisterPluginCommands } from './pluginCommands'
import { clearPluginContextKeys, pluginContextFacts, setPluginContextKey } from './pluginContextKeys'

function manifestWith(commands: Manifest['contributes']['commands']): Manifest {
  return {
    id: 'mill-index', name: 'Board index', version: '1.0.0', description: '', author: '', minMillVersion: '0.9.0', icon: 'icon.png',
    capabilities: [], dependencies: [], exports: [],
    contributes: { canvasObjects: [], steps: [], captures: [], settings: [], configuration: [], menus: {}, network: [], views: [], commands, tools: [], themes: [], secretSources: [], mcpServers: [] },
  }
}

describe('menuForDeclaredCommand (goal 0335: the manifest -> registerCommand menu join)', () => {
  it('maps a declared menu seat onto Command.menu, joined by id', () => {
    const manifest = manifestWith([{ id: 'mill-index.refresh', label: 'Refresh the board index', menu: { path: 'help', group: 1, order: 2 } }])
    expect(menuForDeclaredCommand(manifest, 'mill-index.refresh')).toEqual({ path: 'help', group: 1, order: 2 })
  })

  it('is undefined for a declared command with no menu field', () => {
    const manifest = manifestWith([{ id: 'mill-index.refresh', label: 'Refresh the board index' }])
    expect(menuForDeclaredCommand(manifest, 'mill-index.refresh')).toBeUndefined()
  })

  it('is undefined for a command id the manifest never declared', () => {
    const manifest = manifestWith([])
    expect(menuForDeclaredCommand(manifest, 'mill-index.refresh')).toBeUndefined()
  })
})

describe('buildPluginAPI command enablement', () => {
  const pluginId = 'mill-index'
  afterEach(() => {
    unregisterPluginCommands(pluginId)
    clearPluginContextKeys(pluginId)
    vi.restoreAllMocks()
  })

  it('warns once per malformed expression and keeps the registered command disabled', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const manifest = manifestWith([{ id: 'mill-index.refresh', label: 'Refresh', enablement: 'plugin.ready &&' }])
    const api = buildPluginAPI(manifest, '1.0.0')
    api.registerCommand({ id: 'mill-index.refresh', label: 'Refresh', run: () => {} })
    const registered = drainedPluginCommands().find((command) => command.id === 'plugin.mill-index.mill-index.refresh')
    expect(registered?.enabled?.()).toBe(false)
    expect(warn).toHaveBeenCalledOnce()
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('plugin mill-index: command "mill-index.refresh" has invalid enablement'))
    unregisterPluginCommands(pluginId)
    api.registerCommand({ id: 'mill-index.refresh', label: 'Refresh', run: () => {} })
    expect(warn).toHaveBeenCalledOnce()
  })

  it('resolves a legacy bare registration through the canonical declaration', () => {
    const manifest = manifestWith([{
      id: 'mill-index.refresh', label: 'Refresh', enablement: 'plugin.ready',
      menu: { path: 'help', group: 1, order: 2 },
    }])
    buildPluginAPI(manifest, '1.0.0').registerCommand({ id: 'refresh', label: 'Refresh', run: () => {} })
    const registered = drainedPluginCommands().find((command) => command.id === 'plugin.mill-index.mill-index.refresh')
    expect(registered?.menu).toEqual({ path: 'help', group: 1, order: 2 })
    expect(registered?.enabled?.()).toBe(false)
    setPluginContextKey(pluginId, 'ready', true)
    expect(registered?.enabled?.()).toBe(true)
  })

  it('keeps an exact bare declaration ahead of its canonical counterpart', () => {
    const manifest = manifestWith([
      { id: 'refresh', label: 'Legacy refresh', menu: { path: 'help', group: 1, order: 2 } },
      { id: 'mill-index.refresh', label: 'Canonical refresh', menu: { path: 'atlas', group: 3, order: 4 } },
    ])
    buildPluginAPI(manifest, '1.0.0').registerCommand({ id: 'refresh', label: 'Refresh', run: () => {} })
    const registered = drainedPluginCommands().find((command) => command.id === 'plugin.mill-index.refresh')
    expect(registered?.menu).toEqual({ path: 'help', group: 1, order: 2 })
  })

  it('keeps an undeclared bare registration unchanged', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    buildPluginAPI(manifestWith([]), '1.0.0').registerCommand({ id: 'legacyOnly', label: 'Legacy only', run: () => {} })
    expect(drainedPluginCommands().some((command) => command.id === 'plugin.mill-index.legacyOnly')).toBe(true)
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('command "legacyOnly" is not declared'))
  })

  it('accepts the same mixed context values as the framed door and refuses non-finite numbers', () => {
    const api = buildPluginAPI(manifestWith([]), '1.0.0')
    api.context.set('values', ['ready', 0, false, null])
    expect(() => api.context.set('invalid', Number.NaN)).toThrow(/finite number/)
  })

  it('keeps sibling APIs in one activation live and retires all of them when context clears', () => {
    const manifest = manifestWith([])
    const apiA = buildPluginAPI(manifest, '1.0.0')
    const sibling = buildPluginAPI(manifest, '1.0.0')
    apiA.context.set('first', true)
    sibling.context.set('second', 2)
    expect(pluginContextFacts(pluginId)).toEqual({ 'plugin.first': true, 'plugin.second': 2 })

    clearPluginContextKeys(pluginId)
    const apiB = buildPluginAPI(manifest, '1.0.0')
    expect(() => apiA.context.set('lateA', true)).toThrow(/retired activation/)
    expect(() => sibling.context.set('lateSibling', true)).toThrow(/retired activation/)
    apiB.context.set('fresh', true)
    expect(() => apiA.context.set('laterStill', true)).toThrow(/retired activation/)
    expect(pluginContextFacts(pluginId)).toEqual({ 'plugin.fresh': true })
  })

  it('feeds the registry the logical AND of command.enablement and the registered callback', () => {
    const manifest = manifestWith([{ id: 'mill-index.refresh', label: 'Refresh', enablement: 'plugin.ready' }])
    const callbackState = { enabled: false }
    buildPluginAPI(manifest, '1.0.0').registerCommand({
      id: 'mill-index.refresh',
      label: 'Refresh',
      run: () => {},
      enabled: () => callbackState.enabled,
    })
    const registered = drainedPluginCommands().find((command) => command.id === 'plugin.mill-index.mill-index.refresh')
    expect(registered?.enabled?.()).toBe(false)
    setPluginContextKey(pluginId, 'ready', true)
    expect(registered?.enabled?.()).toBe(false)
    callbackState.enabled = true
    expect(registered?.enabled?.()).toBe(true)
  })

  it('refuses disabled command dispatch and runs it after its context fact turns true', async () => {
    const run = vi.fn()
    const manifest = manifestWith([{ id: 'mill-index.refresh', label: 'Refresh', enablement: 'plugin.ready' }])
    buildPluginAPI(manifest, '1.0.0').registerCommand({ id: 'mill-index.refresh', label: 'Refresh', run })
    const { runCommand } = await import('../shared/commands')
    await expect(runCommand('plugin.mill-index.mill-index.refresh')).resolves.toBe(false)
    expect(run).not.toHaveBeenCalled()
    setPluginContextKey(pluginId, 'ready', true)
    await expect(runCommand('plugin.mill-index.mill-index.refresh')).resolves.toBe(true)
    expect(run).toHaveBeenCalledOnce()
  }, 15_000)
})
