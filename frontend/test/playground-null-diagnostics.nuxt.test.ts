import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import PlaygroundPage from '~/pages/playground.vue'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  runtime: { instance_id: 'coder', model_id: 'model-1', state: 'READY', pid: 77, port: 9101 },
  manager: null as any
}))

mocks.manager = {
  apiBase: { value: 'http://manager.test:8888' },
  instances: { value: [{
    id: 'coder', model_id: 'model-1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false,
    priority: 'normal', eviction_enabled: true, idle_unload_seconds: 300, gpu_mode: 'auto'
  }] },
  models: { value: [{ id: 'model-1', name: 'Qwen Coder', gguf_path: 'qwen.gguf', total_bytes: 1, context_length: 32768 }] },
  runtimeForInstance: vi.fn(() => mocks.runtime),
  telemetryForInstance: vi.fn(() => undefined),
  instanceState: vi.fn(() => mocks.runtime.state),
  request: mocks.request
}

mockNuxtImport('useManager', () => () => mocks.manager)

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

async function activateTab(wrapper: any, text: string) {
  const tab = wrapper.findAll('[role="tab"]').find((candidate: any) => candidate.text().trim() === text)
  if (!tab) throw new Error(`Missing tab: ${text}`)
  await tab.trigger('pointerdown')
  await tab.trigger('click')
}

function promptSubmit(wrapper: any) {
  return wrapper.get('[data-testid="playground-prompt-submit"]')
}

async function sendPlayground(wrapper: any) {
  await promptSubmit(wrapper).trigger('click')
}

beforeEach(() => {
  mocks.request.mockReset()
  sessionStorage.clear()
  localStorage.clear()
  sessionStorage.setItem('llamarack_management_token', 'management-playground')
  vi.unstubAllGlobals()
})

describe('Playground nullable diagnostics', () => {
  it('renders nil state traces, eviction lists and instance IDs safely', async () => {
    mocks.request.mockResolvedValue({
      request: {
        request_id: 'req-null',
        instance_id: null,
        status_code: 200,
        result: 'success',
        duration_ms: 100,
        prompt_tokens: 1,
        generated_tokens: 1,
        total_tokens: 2,
        load_duration_ms: 0,
        autoloaded: false
      },
      state_trace: null,
      evictions_triggered: null
    })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ choices: [{ message: { content: 'ok' } }] }),
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-null' } }
    )))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await activateTab(wrapper, 'Request')
    const raw = { model: 'coder', messages: [{ role: 'user', content: 'ping' }], stream: false }
    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(JSON.stringify(raw))
    await sendPlayground(wrapper)
    await flushPromises()

    const diagnostics = wrapper.get('[data-testid="playground-diagnostics"]').text()
    expect(diagnostics).toContain('Instance—')
    expect(diagnostics).toContain('Instance state—')
    expect(diagnostics).toContain('Evictions triggerednone')
    expect(wrapper.text()).toContain('ok')
  })
})
