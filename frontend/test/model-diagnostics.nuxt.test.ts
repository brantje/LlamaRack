import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn(), writeText: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seed() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Coder model', gguf_path: 'coder.gguf', total_bytes: 4, quantization: 'Q4_K_M', context_length: 8192 }]
  manager.instances.value = [{ id: 'coder-primary', model_id: 'm1', name: 'Coder Primary', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [] }]
  manager.runtimes.value = { m1: [{ instance_id: 'coder-primary', model_id: 'm1', state: 'UNLOADED' }] }
  return manager
}

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

function overflowMenu(root: any) {
  const menu = root.findComponent({ name: 'DropdownMenu' }).exists()
    ? root.findComponent({ name: 'DropdownMenu' })
    : root.findComponent({ name: 'UDropdownMenu' })
  if (!menu.exists()) throw new Error('Missing instance overflow menu')
  return menu
}

async function selectOverflow(root: any, label: string) {
  const item = (overflowMenu(root).props('items') as Array<Array<{ label?: string; onSelect?: () => void }>>)
    .flat()
    .find(entry => entry.label === label)
  if (!item?.onSelect) throw new Error(`Missing overflow item ${label}`)
  item.onSelect()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.writeText.mockReset().mockResolvedValue(undefined)
  sessionStorage.setItem('llamarack.instances.view', 'cards')
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: mocks.writeText } })
  seed()
})

describe('instance control plane', () => {
  it('renders stopped instances as addressable cards with a copyable model ID', async () => {
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    expect(wrapper.findAll('[data-testid="instance-card"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Coder Primary')
    expect(wrapper.find('[data-testid="instance-id"]').text()).toBe('coder-primary')
    expect(wrapper.text()).not.toContain('model=coder-primary')
    expect(wrapper.text()).toContain('UNLOADED')
    expect(wrapper.text()).toContain('Launch')
    expect(wrapper.find('[data-testid="instance-card-more"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Logs')

    const copy = wrapper.find('[data-testid="copy-instance-id"]')
    expect(copy.attributes('aria-label')).toBe('Copy coder-primary')
    await copy.trigger('click')
    await flushPromises()
    expect(mocks.writeText).toHaveBeenCalledWith('coder-primary')
    expect(copy.attributes('aria-label')).toBe('Copied coder-primary')
    expect(copy.attributes('title')).toBe('Copied')
  })

  it('surfaces clipboard copy failures', async () => {
    mocks.writeText.mockRejectedValueOnce(new Error('clipboard blocked'))
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.find('[data-testid="copy-instance-id"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('clipboard blocked')
    expect(wrapper.find('[data-testid="copy-instance-id"]').attributes('aria-label')).toBe('Copy coder-primary')
  })

  it('launches, stops, restarts, duplicates and kills the exact instance', async () => {
    const manager = seed()
    let runtimeState = 'UNLOADED'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances/coder-primary/start' && options?.method === 'POST') runtimeState = 'READY'
      if (path === '/api/v1/instances/coder-primary/stop' && options?.method === 'POST') runtimeState = 'UNLOADED'
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances/coder-primary/runtime') return { instance_id: 'coder-primary', model_id: 'm1', state: runtimeState }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/coder-primary/start', expect.anything())
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/start', { method: 'POST' })

    for (const label of ['Restart', 'Duplicate']) {
      await selectOverflow(wrapper, label)
    }
    await selectOverflow(wrapper, 'Kill')
    expect(document.body.textContent).toContain('Active requests may fail')
    await clickConfirmation('confirm')

    const stop = wrapper.findAll('button').find(button => button.text() === 'Stop')
    if (stop) { await stop.trigger('click'); await flushPromises() }
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/restart', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/duplicate', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/kill', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/stop', { method: 'POST' })
  })

  it('shows logs and deletes without deleting the registered model', async () => {
    const manager = seed()
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/logs?instance_id=coder-primary&limit=2000') {
        return {
          instance_id: 'coder-primary',
          entries: [
            { source: 'manager', timestamp: '2026-08-28T12:00:00Z', text: 'worker ready' },
            { source: 'stdout', timestamp: '2026-08-28T12:00:01Z', text: 'request complete' }
          ]
        }
      }
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances/coder-primary/runtime') return { instance_id: 'coder-primary', model_id: 'm1', state: 'UNLOADED' }
      return {}
    })
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?instance_id=coder-primary&limit=2000')
    expect(document.body.textContent).toContain('worker ready')
    await selectOverflow(wrapper, 'Delete')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/coder-primary', expect.anything())
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary', { method: 'DELETE' })
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/m1', expect.anything())
  })

  it('keeps instances visible on action errors and supports cancelled destructive actions', async () => {
    mocks.request.mockRejectedValue({ data: { error: 'worker failed' } })
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await flushPromises()
    // Mounting the control plane now performs one import-status read so a
    // completed Hugging Face context-detection warning can survive reloads.
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/imports')
    mocks.request.mockClear()

    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalled()

    await selectOverflow(wrapper, 'Restart')
    expect(wrapper.text()).toContain('worker failed')
    expect(wrapper.findAll('[data-testid="instance-card"]')).toHaveLength(1)
  })

  it('covers policy variants, state badges, model fallback and empty/error log branches', async () => {
    const manager = seed()
    manager.instances.value = [
      { id: 'protected', model_id: 'missing-model', name: 'Protected', enabled: true, autoload_enabled: false, always_on: true, priority: 'high', eviction_enabled: false, idle_unload_seconds: 60, gpu_mode: 'manual', gpu_devices: ['0'] },
      { id: 'ready', model_id: 'm1', name: 'Ready Instance', enabled: true, autoload_enabled: true, always_on: false, priority: 'low', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [] }
    ]
    manager.runtimes.value = {
      'missing-model': [{ instance_id: 'protected', model_id: 'missing-model', state: 'FAILED' }],
      m1: [{ instance_id: 'ready', model_id: 'm1', state: 'READY' }]
    }
    let logMode: 'empty' | 'error' = 'empty'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/logs?instance_id=protected&limit=2000') {
        if (logMode === 'error') throw new Error('log retrieval failed')
        return { instance_id: 'protected', entries: [] }
      }
      if (path === '/api/v1/instances/protected/start' && options?.method === 'POST') throw {}
      if (path === '/api/v1/instances/protected/restart' && options?.method === 'POST') throw new Error('restart failed')
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances/protected/runtime') return { instance_id: 'protected', model_id: 'missing-model', state: 'FAILED' }
      if (path === '/api/v1/instances/ready/runtime') return { instance_id: 'ready', model_id: 'm1', state: 'READY' }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })

    const wrapper = await mountSuspended(InstancesPage, { route: false })
    expect(wrapper.text()).toContain('missing-model')
    expect(wrapper.text()).toContain('FAILED')
    expect(wrapper.text()).toContain('READY')
    expect(wrapper.text()).toContain('Always On')
    expect(wrapper.text()).toContain('Manual load')
    expect(wrapper.text()).toContain('Protected from resource-pressure eviction')

    const protectedCard = wrapper.findAll('[data-testid="instance-card"]')[0]!
    await protectedCard.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?instance_id=protected&limit=2000')
    expect(document.body.textContent).toContain('No logs in the current view')

    await protectedCard.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to start Instance')

    await selectOverflow(protectedCard, 'Restart')
    expect(wrapper.text()).toContain('restart failed')

    logMode = 'error'
    const reconnect = [...document.body.querySelectorAll<HTMLButtonElement>('button')]
      .filter(button => button.textContent?.trim() === 'Reconnect')
      .at(-1)
    if (!reconnect) throw new Error('Missing log reconnect button')
    reconnect.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?instance_id=protected&limit=2000')
    expect(document.body.textContent).toContain('log retrieval failed')

    mocks.request.mockClear()
    await selectOverflow(protectedCard, 'Kill')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('renders the empty state', async () => {
    const manager = seed()
    manager.instances.value = []
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    expect(wrapper.text()).toContain('No Instances configured')
    expect(wrapper.text()).toContain('New Instance')
  })
})
