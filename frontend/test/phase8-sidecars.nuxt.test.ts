import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/components/ModelsDiscover.vue'
import NewModelPage from '~/pages/models/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = {
    path: '/app/llama-server', version: 'sidecars', fingerprint: 'abcdefghijklmnopqrstuvwxyz', options: [
      { key: 'mmproj', value_hint: 'FILE', description: 'multimodal projector' },
      { key: 'spec-draft-model', value_hint: 'FILE', description: 'draft model' },
      { key: 'spec-type', value_hint: '<none|draft-mtp>', description: 'speculative decoding type' }
    ]
  }
  return manager
}

async function selectGGUF(wrapper: any, path: string) {
  const input = wrapper.findAll('input[type="radio"][name="gguf_path"]').find((item: any) => item.attributes('value') === path)
  if (!input) throw new Error(`Missing GGUF option ${path}`)
  await input.setValue()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  resetManager()
})

describe('Phase 8 GGUF helper integration', () => {
  it('shows automatically associated projector and MTP artifacts before download', async () => {
    const detail = {
      id: 'acme/vision', downloads: 10, likes: 2, private: false, gated: false, revision: 'rev', artifacts: [{
        id: 'q4', name: 'vision-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 1024 ** 3,
        total_bytes: 1024 ** 3 + 300 * 1024 ** 2, shard_count: 1, expected_shards: 1, complete: true,
        files: [
          { path: 'vision-Q4_K_M.gguf', size: 1024 ** 3 },
          { path: 'mmproj-F16.gguf', size: 200 * 1024 ** 2 },
          { path: 'mtp-vision-Q4_0.gguf', size: 100 * 1024 ** 2 }
        ],
        dependencies: [
          { kind: 'mmproj', name: 'mmproj-F16.gguf', quantization: 'F16', total_bytes: 200 * 1024 ** 2, files: [{ path: 'mmproj-F16.gguf', size: 200 * 1024 ** 2 }] },
          { kind: 'mtp', name: 'mtp-vision-Q4_0.gguf', quantization: 'Q4_0', total_bytes: 100 * 1024 ** 2, files: [{ path: 'mtp-vision-Q4_0.gguf', size: 100 * 1024 ** 2 }] }
        ]
      }]
    }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/model?repo=acme%2Fvision') return detail
      if (path === '/api/v1/downloads' && options?.method === 'POST') return { id: 'job' }
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { props: { repoId: 'acme/vision' }, route: false })
    await flushPromises()

    expect(wrapper.text()).toContain('Vision projector')
    expect(wrapper.text()).toContain('mmproj-F16.gguf')
    expect(wrapper.text()).toContain('MTP draft model')
    expect(wrapper.text()).toContain('mtp-vision-Q4_0.gguf')
    expect(wrapper.text()).toContain('vision-Q4_K_M.gguf · 1.3 GB')
    expect(wrapper.text()).toContain('200 MB')
    expect(wrapper.text()).toContain('100 MB')

    const download = wrapper.findAll('button').find(button => button.text().trim() === 'Download')!
    await download.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads', { method: 'POST', body: { repo_id: 'acme/vision', artifact_id: 'q4' } })
    expect(wrapper.text()).toContain('2 detected helper artifacts were added to Downloads')
  })

  it('preconfigures downloaded helpers and derives radio capabilities from suggested options', async () => {
    const available = [{
      path: 'huggingface/acme/vision/vision-Q4_K_M.gguf', name: 'vision-Q4_K_M.gguf', total_bytes: 1024, quantization: 'Q4_K_M',
      suggested_options: {
        mmproj: '/models/huggingface/acme/vision/mmproj-F16.gguf',
        'spec-draft-model': '/models/huggingface/acme/vision/mtp-vision-Q4_0.gguf',
        'spec-type': 'draft-mtp'
      }
    }, {
      path: 'huggingface/acme/mtp/model-Q5_K_M.gguf', name: 'model-Q5_K_M.gguf', total_bytes: 1024, quantization: 'Q5_K_M',
      suggested_options: { 'spec-type': 'draft-mtp' }
    }, {
      path: 'huggingface/acme/vision-only/model-Q8_0.gguf', name: 'model-Q8_0.gguf', total_bytes: 1024, quantization: 'Q8_0',
      suggested_options: { mmproj: '/models/huggingface/acme/vision-only/mmproj-F16.gguf' }
    }]
    const inspection = {
      id: available[0].path,
      name: available[0].name,
      quantization: available[0].quantization,
      model_bytes: 1024,
      total_bytes: 1324,
      shard_count: 1,
      expected_shards: 1,
      complete: true,
      files: [
        { path: available[0].path, size: 1024 },
        { path: 'huggingface/acme/vision/mmproj-F16.gguf', size: 200 },
        { path: 'huggingface/acme/vision/mtp-vision-Q4_0.gguf', size: 100 }
      ],
      dependencies: [
        { kind: 'mmproj', name: 'mmproj-F16.gguf', quantization: 'F16', total_bytes: 200, files: [{ path: 'huggingface/acme/vision/mmproj-F16.gguf', size: 200 }] },
        { kind: 'mtp', name: 'mtp-vision-Q4_0.gguf', quantization: 'Q4_0', total_bytes: 100, files: [{ path: 'huggingface/acme/vision/mtp-vision-Q4_0.gguf', size: 100 }] }
      ],
      suggested_options: available[0].suggested_options
    }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/available') return available
      if (path === '/api/v1/models/inspect') return inspection
      if (path.startsWith('/api/v1/llamacpp/config')) return { profile: resetManager().profile.value, effective: { global: {}, model: {}, instance: {}, values: {}, sources: {} } }
      if (path === '/api/v1/models' && options?.method === 'POST') return { model: { id: 'm1' } }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') return { available: true, profile: resetManager().profile.value }
      return []
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    const rows = wrapper.get('[data-testid="gguf-select"]').findAll('[data-testid="gguf-option"]')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain(available[0].path)
    expect(rows[0]!.text()).toContain('MTP')
    expect(rows[0]!.text()).toContain('Vision')
    expect(rows[1]!.text()).toContain(available[1].path)
    expect(rows[1]!.text()).toContain('MTP')
    expect(rows[1]!.text()).not.toContain('Vision')
    expect(rows[2]!.text()).toContain(available[2].path)
    expect(rows[2]!.text()).toContain('Vision')
    expect(rows[2]!.text()).not.toContain('MTP')

    await selectGGUF(wrapper, available[0].path)
    expect(wrapper.get('[data-testid="detected-gguf-helpers"]').text()).toContain('Vision projector: mmproj-F16.gguf')
    expect(wrapper.get('[data-testid="detected-gguf-helpers"]').text()).toContain('MTP draft model: mtp-vision-Q4_0.gguf')

    await wrapper.get('[data-testid="model-name"]').setValue('Vision Model')
    await wrapper.get('[data-testid="create-first-instance"]').trigger('click')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', {
      method: 'POST',
      body: {
        gguf_path: available[0].path,
        name: 'Vision Model',
        context_length: 0,
        options: available[0].suggested_options,
        first_instance: undefined
      }
    })
  })
})
