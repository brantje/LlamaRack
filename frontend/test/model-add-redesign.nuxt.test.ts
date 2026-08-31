import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import AppButton from '~/components/AppButton.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

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
}

async function expandModelDefaults(wrapper: any) {
  const toggle = wrapper.get('[data-testid="model-form-defaults"]').find('[data-testid="frame-collapse-toggle"]')
  if (toggle.exists() && toggle.attributes('aria-expanded') === 'false') {
    await toggle.trigger('click')
    await flushPromises()
  }
}

async function modelOptions(wrapper: any) {
  await expandModelDefaults(wrapper)
  return wrapper.findComponent(LlamaCppOptionsEditor).props('modelValue') as Record<string, string>
}

async function chooseGGUF(wrapper: any, path: string) {
  const input = wrapper.findAll('input[type="radio"][name="gguf_path"]').find((item: any) => item.attributes('value') === path)
  if (!input) throw new Error(`Missing GGUF option ${path}`)
  await input.setValue()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('Add model redesign', () => {
  it('explains the local artifact dependency and offers Discover when no GGUF can be selected', async () => {
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/models/available' ? [] : {})

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()

    const empty = wrapper.get('[data-testid="gguf-empty-state"]')
    expect(empty.text()).toContain('No unregistered GGUF files found')
    expect(empty.text()).toContain('Create model is unavailable until a GGUF artifact is selected.')
    expect(empty.text()).toContain('Open Discover')
    const sectionNav = wrapper.get('[data-testid="model-form-section-nav"]')
    expect(sectionNav.attributes('aria-label')).toBe('Model form sections')
    expect(sectionNav.findAll('a').map(link => link.attributes('href'))).toEqual([
      '#model-artifact', '#model-companions', '#model-identity', '#model-defaults', '#model-first-instance'
    ])
    expect(wrapper.get('[data-testid="model-submit-requirements"]').text()).toContain('Required: a GGUF artifact and Model name.')
    expect(wrapper.get('[data-testid="model-add-header"]').classes()).toContain('flex-wrap')
    await expandModelDefaults(wrapper)
    expect(wrapper.findComponent(LlamaCppOptionsEditor).exists()).toBe(true)
    expect(wrapper.get('[data-testid="llamacpp-mode-basic"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="llamacpp-mode-advanced"]').exists()).toBe(true)
    const rescans = wrapper.findAllComponents(AppButton).filter(button => button.text() === 'Rescan')
    expect(rescans.map(button => button.props('intent'))).toEqual(['secondary', 'ghost'])
  })

  it('renders bordered GGUF radios with real modified time and metadata-driven capabilities', async () => {
    const now = Date.now()
    const available = [
      {
        path: 'models/vision-looking-name.gguf', name: 'vision-looking-name.gguf', total_bytes: 1024,
        modified_at: new Date(now - 30 * 60 * 1000).toISOString(), suggested_options: {}
      },
      {
        path: 'models/plain-model.gguf', name: 'plain-model.gguf', total_bytes: 1536,
        modified_at: new Date(now - 2 * 60 * 60 * 1000).toISOString(),
        suggested_options: { mmproj: '/models/mmproj-F16.gguf', 'spec-type': 'draft-mtp' }
      }
    ]
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/models/available' ? available : {})

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    const rows = wrapper.get('[data-testid="gguf-select"]').findAll('[data-testid="gguf-option"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('1.0 KiB · modified 30m ago')
    expect(rows[0]!.text()).not.toContain('Vision')
    expect(rows[1]!.text()).toContain('1.5 KiB · modified 2h ago')
    expect(rows[1]!.text()).toContain('Vision')
    expect(rows[1]!.text()).toContain('MTP')
    const capabilityBadges = rows[1]!.findAll('span.border')
    expect(capabilityBadges.find(badge => badge.text() === 'MTP')!.classes()).toEqual(expect.arrayContaining([
      'border-[var(--color-success)]', 'bg-[var(--success-100)]', 'text-[var(--success-700)]'
    ]))
    expect(capabilityBadges.find(badge => badge.text() === 'Vision')!.classes()).toEqual(expect.arrayContaining([
      'border-[var(--color-accent)]', 'bg-[var(--accent-100)]', 'text-[var(--accent-800)]'
    ]))

    await chooseGGUF(wrapper, available[1]!.path)
    expect(rows[1]!.classes()).toContain('bg-[var(--accent-100)]')
  })

  it('lets detected local companion alternates and enable/disable actions mutate Model options', async () => {
    const ggufPath = 'local/model-Q4_K_M.gguf'
    const selectedProjector = '/models/local/projector-F16.gguf'
    const alternateProjector = '/models/local/projector-Q8_0.gguf'
    const selectedDraft = '/models/local/draft-Q4_0.gguf'
    const alternateDraft = '/models/local/draft-Q8_0.gguf'
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return [{
        path: ggufPath, name: 'model-Q4_K_M.gguf', total_bytes: 4096,
        suggested_options: { mmproj: selectedProjector, 'spec-draft-model': selectedDraft, 'spec-type': 'draft-mtp' }
      }]
      if (path === '/api/v1/models/inspect') return {
        id: ggufPath, name: 'model-Q4_K_M.gguf', model_bytes: 4096, total_bytes: 4400,
        shard_count: 1, expected_shards: 1, complete: true, files: [{ path: ggufPath, size: 4096 }],
        dependencies: [
          { kind: 'mmproj', name: 'projector-F16.gguf', quantization: 'F16', total_bytes: 200, files: [{ path: 'local/projector-F16.gguf', size: 200 }] },
          { kind: 'mtp', name: 'draft-Q4_0.gguf', quantization: 'Q4_0', total_bytes: 104, files: [{ path: 'local/draft-Q4_0.gguf', size: 104 }] }
        ],
        dependency_candidates: [
          { kind: 'mmproj', name: 'projector-F16.gguf', quantization: 'F16', total_bytes: 200, files: [], option_path: selectedProjector },
          { kind: 'mmproj', name: 'projector-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 220, files: [], option_path: alternateProjector },
          { kind: 'mtp', name: 'draft-Q4_0.gguf', quantization: 'Q4_0', total_bytes: 104, files: [], option_path: selectedDraft },
          { kind: 'mtp', name: 'draft-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 120, files: [], option_path: alternateDraft }
        ],
        suggested_options: {
          mmproj: selectedProjector,
          'spec-draft-model': selectedDraft,
          'spec-type': 'draft-mtp',
          'spec-draft-n-max': '16'
        }
      }
      return {}
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    await chooseGGUF(wrapper, ggufPath)

    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('Auto-detected')
    expect(wrapper.get('[data-testid="companion-mtp"]').text()).toContain('Auto-detected')
    expect(wrapper.findAll('[data-testid="companion-candidate-mmproj"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="companion-candidate-mtp"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="companion-candidate-mmproj"]')[0]!.attributes('aria-pressed')).toBe('true')
    expect(await modelOptions(wrapper)).toMatchObject({
      mmproj: selectedProjector,
      'spec-draft-model': selectedDraft,
      'spec-type': 'draft-mtp'
    })

    await wrapper.findAll('[data-testid="companion-candidate-mmproj"]')[1]!.trigger('click')
    expect((await modelOptions(wrapper)).mmproj).toBe(alternateProjector)
    expect(wrapper.findAll('[data-testid="companion-candidate-mmproj"]')[1]!.attributes('aria-pressed')).toBe('true')

    const projectorSlot = wrapper.get('[data-testid="companion-mmproj"]')
    await projectorSlot.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect((await modelOptions(wrapper)).mmproj).toBe('')
    expect(projectorSlot.text()).toContain('Ignored')
    expect(projectorSlot.text()).toContain('value cleared — the flag is not passed')
    expect(projectorSlot.get('[data-testid="companion-disabled-mmproj"]').classes()).toContain('text-[var(--neutral-800)]')

    await projectorSlot.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect((await modelOptions(wrapper)).mmproj).toBe(selectedProjector)
    const projectorInput = projectorSlot.findAllComponents({ name: 'UInput' })[0] || projectorSlot.findAllComponents({ name: 'Input' })[0]
    projectorInput!.vm.$emit('update:modelValue', '/custom/projector.gguf')
    await flushPromises()
    expect((await modelOptions(wrapper)).mmproj).toBe('/custom/projector.gguf')

    const mtpSlot = wrapper.get('[data-testid="companion-mtp"]')
    await mtpSlot.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect((await modelOptions(wrapper))['spec-draft-model']).toBe('')
    expect((await modelOptions(wrapper))['spec-type']).toBe('')
  })

  it('surfaces built-in MTP from inspect features and lets extra MTP params be cleared', async () => {
    const ggufPath = 'huggingface/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF/Huihui-Qwen3.8-27B-abliterated-Q4_K.gguf'
    const projector = '/models/huggingface/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF/mmproj-model-bf16.gguf'
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return [{
        path: ggufPath, name: 'Huihui-Qwen3.8-27B-abliterated-Q4_K.gguf', total_bytes: 10 * 1024 ** 3,
        suggested_options: {
          mmproj: projector,
          'spec-type': 'draft-mtp',
          'spec-draft-n-max': '16',
          'spec-draft-p-min': '0.8'
        }
      }]
      if (path === '/api/v1/models/inspect') return {
        id: ggufPath, name: 'Huihui-Qwen3.8-27B-abliterated-Q4_K.gguf', model_bytes: 10 * 1024 ** 3, total_bytes: 10 * 1024 ** 3 + 888 * 1024 ** 2,
        shard_count: 1, expected_shards: 1, complete: true, files: [{ path: ggufPath, size: 10 * 1024 ** 3 }],
        architecture: 'qwen35',
        features: { architecture: 'qwen35', has_mtp: true, mtp_only: false, projector: false, nextn_predict_layers: 1 },
        dependencies: [
          { kind: 'mmproj', name: 'mmproj-model-bf16.gguf', quantization: 'BF16', total_bytes: 888 * 1024 ** 2, files: [{ path: 'huggingface/huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF/mmproj-model-bf16.gguf', size: 888 * 1024 ** 2 }] }
        ],
        suggested_options: {
          mmproj: projector,
          'spec-type': 'draft-mtp',
          'spec-draft-n-max': '16',
          'spec-draft-p-min': '0.8'
        }
      }
      return {}
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    expect(wrapper.get('[data-testid="gguf-option"]').text()).toContain('MTP')
    expect(wrapper.get('[data-testid="gguf-option"]').text()).toContain('Vision')
    await chooseGGUF(wrapper, ggufPath)

    const mtpSlot = wrapper.get('[data-testid="companion-mtp"]')
    expect(mtpSlot.text()).toContain('Built-in MTP')
    expect(mtpSlot.text()).toContain('Built-in')
    expect(mtpSlot.text()).not.toContain('None found')
    expect(mtpSlot.text()).not.toContain('MTP draft model')
    expect(wrapper.get('[data-testid="companion-native-mtp"]').text()).toContain('Packed into this GGUF')
    expect(wrapper.get('[data-testid="companion-native-mtp"]').text()).toContain('nextn_predict_layers 1')
    expect(wrapper.get('[data-testid="companion-native-mtp-params"]').text()).toContain('spec-type=draft-mtp')
    expect(wrapper.get('[data-testid="companion-native-mtp-params"]').text()).toContain('spec-draft-n-max=16')
    expect(wrapper.get('[data-testid="companion-native-mtp-params"]').text()).toContain('spec-draft-p-min=0.8')
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('Auto-detected')
    expect(await modelOptions(wrapper)).toMatchObject({
      mmproj: projector,
      'spec-type': 'draft-mtp',
      'spec-draft-n-max': '16',
      'spec-draft-p-min': '0.8'
    })
    expect(Object.prototype.hasOwnProperty.call(await modelOptions(wrapper), 'spec-draft-model')).toBe(false)

    await mtpSlot.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(mtpSlot.text()).toContain('Ignored')
    expect(mtpSlot.get('[data-testid="companion-disabled-mtp"]').text()).toContain('MTP defaults cleared')
    expect(await modelOptions(wrapper)).toMatchObject({
      mmproj: projector,
      'spec-type': '',
      'spec-draft-n-max': '',
      'spec-draft-p-min': ''
    })

    await mtpSlot.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(mtpSlot.text()).toContain('Built-in')
    expect(await modelOptions(wrapper)).toMatchObject({
      'spec-type': 'draft-mtp',
      'spec-draft-n-max': '16',
      'spec-draft-p-min': '0.8'
    })
  })

  it('treats inspect spec-type without a draft file as built-in MTP before features arrive', async () => {
    const ggufPath = 'local/native-mtp.gguf'
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return [{
        path: ggufPath, name: 'native-mtp.gguf', total_bytes: 2048,
        suggested_options: { 'spec-type': 'draft-mtp', 'spec-draft-n-max': '16', 'spec-draft-p-min': '0.8' }
      }]
      if (path === '/api/v1/models/inspect') return {
        id: ggufPath, name: 'native-mtp.gguf', model_bytes: 2048, total_bytes: 2048,
        shard_count: 1, expected_shards: 1, complete: true, files: [{ path: ggufPath, size: 2048 }],
        suggested_options: { 'spec-type': 'draft-mtp', 'spec-draft-n-max': '16', 'spec-draft-p-min': '0.8' }
      }
      return {}
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    await chooseGGUF(wrapper, ggufPath)
    const mtpSlot = wrapper.get('[data-testid="companion-mtp"]')
    expect(mtpSlot.text()).toContain('Built-in MTP')
    expect(mtpSlot.get('[data-testid="companion-native-mtp"]').text()).toContain('Packed into this GGUF')
    expect(mtpSlot.text()).not.toContain('nextn_predict_layers')
    expect(await modelOptions(wrapper)).toMatchObject({ 'spec-type': 'draft-mtp' })
  })

  it('renders the remote artifact card, forces the first Instance block and preserves helper opt-out tombstones', async () => {
    const artifact = {
      id: 'q4', name: 'model-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 4096, total_bytes: 4300,
      shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'model-Q4_K_M.gguf', size: 4096 }],
      dependencies: [{ kind: 'mmproj', name: 'mmproj-F16.gguf', total_bytes: 204, files: [{ path: 'mmproj-F16.gguf', size: 204 }] }]
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/demo', revision: 'rev', artifacts: [artifact] }
      return []
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new?repo=acme%2Fdemo&artifact=q4' })
    await flushPromises()
    const summary = wrapper.get('[data-testid="remote-artifact-summary"]')
    expect(summary.text()).toContain('acme/demo')
    expect(summary.text()).toContain('Q4_K_M')
    expect(summary.text()).toContain('model-Q4_K_M.gguf')
    expect(summary.text()).toContain('including detected helpers')
    expect(wrapper.find('[data-testid="gguf-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-first-instance"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="model-form-first-instance"]').text()).toContain('shown as Downloading until the GGUF completes')
    expect(wrapper.get('[data-testid="instance-name"]').exists()).toBe(true)

    const projectorSlot = wrapper.get('[data-testid="companion-mmproj"]')
    expect(projectorSlot.text()).toContain('Auto-detected')
    await projectorSlot.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect((await modelOptions(wrapper)).mmproj).toBe('')
    await projectorSlot.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(Object.prototype.hasOwnProperty.call(await modelOptions(wrapper), 'mmproj')).toBe(false)
  })
})
