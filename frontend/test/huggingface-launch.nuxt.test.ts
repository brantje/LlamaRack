import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/pages/discover.vue'
import NewModelPage from '~/pages/models/new.vue'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const artifact = {
  id: 'artifact-q4', name: 'model-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 4, total_bytes: 5,
  shard_count: 1, expected_shards: 1, complete: true,
  files: [{ path: 'model-Q4_K_M.gguf', size: 4 }],
  dependencies: [{ kind: 'mmproj', name: 'mmproj-F16.gguf', total_bytes: 1, files: [{ path: 'mmproj-F16.gguf', size: 1 }] }]
}

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Hugging Face launch import', () => {
  it('shows parameter count, last update, and Launch above Download', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-27T08:00:00Z'))
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/search?')) {
        return [{
          id: 'Qwen/Qwen3.8-Flash-Next', downloads: 2550, likes: 3770, parameter_count: 180_000_000_000,
          last_modified: '2026-08-27T05:00:00Z', private: false, gated: false, tags: ['gguf']
        }]
      }
      if (path.startsWith('/api/v1/huggingface/model?repo=')) {
        return { id: 'Qwen/Qwen3.8-Flash-Next', revision: 'rev1', artifacts: [artifact], downloads: 2550, likes: 3770, private: false, gated: false }
      }
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { route: '/discover' })
    await vi.advanceTimersByTimeAsync(350)
    await flushPromises()
    expect(wrapper.text()).toContain('Model size 180B params')
    expect(wrapper.text()).toContain('Updated 3h ago')

    await wrapper.findAll('[class*="cursor-pointer"]')[0]!.trigger('click')
    await flushPromises()
    const actions = wrapper.findAll('a,button').filter(node => ['Launch', 'Download'].includes(node.text()))
    expect(actions.map(node => node.text())).toEqual(['Launch', 'Download'])
    const launch = actions[0]!
    expect(launch.attributes('href')).toContain('/models/new')
    expect(launch.attributes('href')).toContain('artifact=artifact-q4')
    expect(decodeURIComponent(launch.attributes('href'))).toContain('repo=Qwen/Qwen3.8-Flash-Next')
    wrapper.unmount()
  })

  it('reuses the Add model form and creates a downloading Instance', async () => {
    const manager = seedManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) {
        return { id: 'acme/demo', revision: 'rev1', artifacts: [artifact] }
      }
      if (path === '/api/v1/huggingface/import' && options?.method === 'POST') {
        return {
          model: { id: 'm-import' },
          instance: { id: 'demo-q4' },
          download: { id: 'job-1', state: 'DOWNLOADING' }
        }
      }
      if (path === '/api/v1/models') return [{ id: 'm-import', name: 'demo Q4_K_M', gguf_path: 'huggingface/acme/demo/model-Q4_K_M.gguf', total_bytes: 4, context_length: 0 }]
      if (path === '/api/v1/instances') return [{ id: 'demo-q4', model_id: 'm-import', name: 'demo Q4_K_M', enabled: false, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto' }]
      if (path === '/api/v1/instances/demo-q4/runtime') return { instance_id: 'demo-q4', model_id: 'm-import', state: 'UNLOADED' }
      if (path === '/api/v1/llamacpp/profile') throw new Error('no profile')
      return []
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new?repo=acme%2Fdemo&artifact=artifact-q4' })
    await flushPromises()
    expect(wrapper.text()).toContain('Launch Hugging Face model')
    expect(wrapper.text()).toContain('Instance is created immediately')
    expect(wrapper.text()).toContain('Launch this Instance when the download completes')
    expect(wrapper.text()).toContain('Vision projector: mmproj-F16.gguf')
    expect((wrapper.get('[data-testid="model-name"]').element as HTMLInputElement).value).toBe('demo Q4_K_M')
    expect((wrapper.get('[data-testid="instance-slug"]').element as HTMLInputElement).value).toBe('demo-q4-k-m')

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/import', {
      method: 'POST',
      body: {
        repo_id: 'acme/demo', artifact_id: 'artifact-q4', name: 'demo Q4_K_M', context_length: 0, options: {},
        first_instance: {
          name: 'demo Q4_K_M', slug: 'demo-q4-k-m', always_on: false, autoload_enabled: true, eviction_enabled: true, start: false
        }
      }
    })
    expect(manager.instances.value.some(instance => instance.id === 'demo-q4')).toBe(true)
    wrapper.unmount()
  })

  it('shows pending provider Instances as DOWNLOADING and blocks runtime controls', async () => {
    const manager = seedManager()
    manager.models.value = [{ id: 'm-import', name: 'Demo', gguf_path: 'huggingface/acme/demo/model.gguf', total_bytes: 4, context_length: 0 }]
    manager.instances.value = [{ id: 'demo', model_id: 'm-import', name: 'Demo', enabled: false, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto' }]
    manager.runtimes.value = { 'm-import': [{ instance_id: 'demo', model_id: 'm-import', state: 'UNLOADED' }] }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/imports') {
        return [{ id: 'import-1', job_id: 'job-1', model_id: 'm-import', instance_id: 'demo', state: 'DOWNLOADING', start_when_ready: true }]
      }
      return []
    })

    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()
    expect(wrapper.text()).toContain('DOWNLOADING')
    expect(wrapper.text()).toContain('Model is downloading')
    expect(wrapper.text()).toContain('launch automatically')
    expect(wrapper.text()).toContain('View download')
    expect(wrapper.text()).not.toContain('Restart')
    expect(wrapper.text()).not.toContain('Kill')
    expect(wrapper.text()).not.toContain('Duplicate')
    wrapper.unmount()
  })
})
